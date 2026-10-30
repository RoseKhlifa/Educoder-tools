package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"text/tabwriter"

	"educoder-cli/internal/educoder"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
}

func run() error {
	command := "flow"
	args := os.Args[1:]
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		command = args[0]
		args = args[1:]
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	login := flags.String("login", os.Getenv("EDUCODER_LOGIN"), "登录账号（也可设置 EDUCODER_LOGIN）")
	passwordStdin := flags.Bool("password-stdin", false, "从标准输入读取密码，不写入参数/文件")
	course := flags.String("course", "", "课堂标识，例如 NTHPZ75R")
	homework := flags.Int64("homework", 0, "进入的作业 ID")
	shixun := flags.String("shixun", "", "可选实训标识，须与作业列表一致")
	category := flags.String("category", "", "作业分类 ID")
	status := flags.String("status", "processing", "课堂状态（默认 processing；ended 为已结束）")
	baseURL := flags.String("base-url", educoder.DefaultBaseURL, "API 根地址")
	jsonOutput := flags.Bool("json", false, "输出 JSON")
	trace := flags.Bool("trace", false, "向 stderr 输出请求方法/路径/状态码")
	flags.Usage = func() {
		fmt.Fprint(flags.Output(), "Educoder 协议终端客户端\n\n用法: go run ./cmd/educoder <login|courses|tasks|enter|flow> [参数]\n\nflow 默认列课堂和实训任务；提供 --course 与 --homework 后进入指定任务。\n账号密码只在本次进程内存中使用。\n")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("未识别的位置参数: %s", flags.Arg(0))
	}
	if command != "login" && command != "courses" && command != "tasks" && command != "enter" && command != "flow" {
		return fmt.Errorf("未知命令: %s", command)
	}
	if (command == "tasks" || command == "enter") && *course == "" {
		return fmt.Errorf("%s 需要 --course", command)
	}
	if command == "enter" && *homework == 0 {
		return fmt.Errorf("enter 需要 --homework")
	}
	if *homework != 0 && *course == "" {
		return fmt.Errorf("--homework 需要 --course")
	}
	password := os.Getenv("EDUCODER_PASSWORD")
	if *passwordStdin {
		value, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && err != io.EOF {
			return err
		}
		password = strings.TrimRight(value, "\r\n")
	}
	if *login == "" || password == "" {
		return fmt.Errorf("请设置 EDUCODER_LOGIN 和 EDUCODER_PASSWORD，或使用 --login 与 --password-stdin")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	client := educoder.New(*baseURL)
	if *trace {
		client.Trace = func(line string) { fmt.Fprintln(os.Stderr, line) }
	}
	user, err := client.Login(ctx, *login, password)
	password = ""
	if err != nil {
		return err
	}
	info, err := client.UserInfo(ctx, "")
	if err != nil {
		return err
	}
	if info.Login != user.Login {
		return fmt.Errorf("登录身份与会话身份不一致")
	}
	if command == "login" {
		if *jsonOutput {
			return outputJSON(user)
		}
		fmt.Printf("登录成功：%s (%s)，身份：%s\n", user.Name, user.Login, user.Identity)
		return nil
	}
	if !*jsonOutput {
		fmt.Printf("登录成功：%s (%s)\n", user.Name, user.Login)
	}
	if command == "enter" || command == "flow" && *homework > 0 {
		task, err := client.Enter(ctx, *course, *homework, *shixun)
		if err != nil {
			return err
		}
		if *jsonOutput {
			return outputJSON(task)
		}
		fmt.Printf("已进入任务：%s\n关卡：%s\n作业 ID：%d\n任务标识：%s\nMyShixun ID：%d\n文件：%s\n地址：%s\n\n任务描述：\n%s\n", task.Shixun.Name, task.Challenge.Subject, *homework, task.Game.Identifier, task.Game.MyShixunID, task.Challenge.Path, task.URL, task.Challenge.Description)
		return nil
	}
	if command == "tasks" {
		homeworks, err := client.Homeworks(ctx, *course, *category)
		if err != nil {
			return err
		}
		if *jsonOutput {
			return outputJSON(homeworks)
		}
		printHomeworks(*course, homeworks)
		return nil
	}
	courses, err := client.Courses(ctx, *status)
	if err != nil {
		return err
	}
	if command == "courses" {
		if *jsonOutput {
			return outputJSON(courses)
		}
		printCourses(courses)
		return nil
	}
	type classroom struct {
		Course    educoder.Course     `json:"course"`
		Homeworks []educoder.Homework `json:"homeworks"`
	}
	classrooms := []classroom{}
	if !*jsonOutput {
		printCourses(courses)
	}
	for _, item := range courses {
		if *course != "" && item.Identifier != *course {
			continue
		}
		if item.Identifier == "" {
			return fmt.Errorf("课堂 %d 缺少 first_category_url 中的课堂标识", item.ID)
		}
		homeworks, err := client.Homeworks(ctx, item.Identifier, *category)
		if err != nil {
			return err
		}
		classrooms = append(classrooms, classroom{item, homeworks})
		if !*jsonOutput {
			printHomeworks(item.Identifier, homeworks)
		}
	}
	if *course != "" && len(classrooms) == 0 {
		return fmt.Errorf("当前课堂列表中没有 %s", *course)
	}
	if *jsonOutput {
		return outputJSON(classrooms)
	}
	return nil
}

func outputJSON(value any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(value)
}

func printCourses(courses []educoder.Course) {
	fmt.Printf("\n课堂：%d 个\n", len(courses))
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "课堂标识\t名称\t作业数")
	for _, c := range courses {
		fmt.Fprintf(w, "%s\t%s\t%d\n", c.Identifier, c.Name, c.HomeworkCount)
	}
	w.Flush()
}

func printHomeworks(course string, homeworks []educoder.Homework) {
	fmt.Printf("\n课堂 %s：%d 个实训任务\n", course, len(homeworks))
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "作业 ID\t实训标识\t状态\t任务名称")
	for _, h := range homeworks {
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\n", h.ID, h.ShixunIdentifier, strings.Join(h.Status, ","), h.Name)
	}
	w.Flush()
}
