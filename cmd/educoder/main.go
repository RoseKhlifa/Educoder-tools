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
	"time"

	"educoder-cli/internal/answer"
	"educoder-cli/internal/educoder"
	"educoder-cli/internal/questionbank"
	"educoder-cli/internal/solver"
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
	game := flags.String("game", "", "已进入关卡的 game.identifier")
	filePath := flags.String("path", "", "challenge.path 中的精确文件路径")
	source := flags.String("source", "", "待保存的本地 UTF-8 代码文件")
	stdinFile := flags.String("stdin-file", "", "自测标准输入文件（保留空格和换行）")
	expectedFile := flags.String("expected-file", "", "自测预期输出文件（仅归一 CRLF）")
	forSubmit := flags.Bool("for-submit", false, "save 使用 evaluate=1，默认 evaluate=0")
	timeout := flags.Duration("timeout", 2*time.Minute, "代码题读取/运行/提交的总时限")
	pollInterval := flags.Duration("poll-interval", time.Second, "评测轮询间隔")
	maxOutput := flags.Int64("max-output-bytes", 1<<20, "自测输出字节上限")
	bankPath := flags.String("bank", os.Getenv("EDUCODER_BANK_PATH"), "题库 SQLite 路径（solve 使用）")
	mode := flags.String("mode", "submit", "solve 模式：generate_only、selftest 或 submit")
	idempotencyKey := flags.String("idempotency-key", "", "solve 本地幂等键")
	modelBaseURL := flags.String("model-base-url", os.Getenv("DEEPSEEK_BASE_URL"), "模型 API 根地址（默认 DeepSeek 官方地址）")
	flags.Usage = func() {
		fmt.Fprint(flags.Output(), "Educoder 协议终端客户端\n\n用法: go run ./cmd/educoder <login|courses|tasks|enter|flow|read|save|self-test|submit|progress|solve> [参数]\n\nflow 默认列课堂和实训任务；提供 --course 与 --homework 后进入指定任务。\nread 读取题干与现存编辑器代码；save 保存；self-test 保存后自测；submit 保存后提交并轮询；progress 读取作业关卡状态。\nsolve 执行题库优先的 AI 代码题流程，--mode 可选 generate_only、selftest、submit。\n代码操作使用 --course --homework --game；save/自测/提交另需 --path --source，自测另需 --stdin-file。\n账号密码和模型密钥只在本次进程内存中使用。\n")
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
	programmingCommand := command == "read" || command == "save" || command == "self-test" || command == "submit" || command == "progress" || command == "solve"
	objectiveCommand := command == "objective"
	if command != "login" && command != "courses" && command != "tasks" && command != "enter" && command != "flow" && !programmingCommand && !objectiveCommand {
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
	if programmingCommand {
		if *course == "" || *homework <= 0 {
			return fmt.Errorf("%s 需要 --course 与 --homework", command)
		}
		if command != "progress" && *game == "" {
			return fmt.Errorf("%s 需要 --game", command)
		}
		if command == "save" || command == "self-test" || command == "submit" {
			if *filePath == "" || *source == "" {
				return fmt.Errorf("%s 需要 --path 与 --source", command)
			}
		}
		if command == "self-test" && *stdinFile == "" {
			return fmt.Errorf("self-test 需要 --stdin-file")
		}
		if command == "solve" {
			if *mode != string(solver.ModeGenerateOnly) && *mode != string(solver.ModeSelfTest) && *mode != string(solver.ModeSubmit) {
				return fmt.Errorf("solve 的 --mode 必须为 generate_only、selftest 或 submit")
			}
			if *bankPath == "" {
				return fmt.Errorf("solve 需要 --bank 或 EDUCODER_BANK_PATH")
			}
			if strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY")) == "" {
				return fmt.Errorf("solve 需要 DEEPSEEK_API_KEY")
			}
		}
		if *timeout <= 0 || *pollInterval <= 0 || *maxOutput <= 0 {
			return fmt.Errorf("timeout、poll-interval、max-output-bytes 必须大于 0")
		}
	}
	if objectiveCommand {
		if *course == "" || *homework <= 0 || *game == "" {
			return fmt.Errorf("objective 需要 --course、--homework 与 --game")
		}
		if *mode != "generate_only" && *mode != "submit" {
			return fmt.Errorf("objective 的 --mode 只能为 generate_only 或 submit")
		}
		if strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY")) == "" {
			return fmt.Errorf("objective 需要 DEEPSEEK_API_KEY")
		}
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
	if objectiveCommand {
		ctx, stop := context.WithTimeout(ctx, *timeout)
		defer stop()
		choice, err := client.LoadChoiceTask(ctx, *course, *homework, *game)
		if err != nil {
			return err
		}
		questions := make([]answer.ObjectiveQuestion, 0, len(choice.Questions))
		for _, q := range choice.Questions {
			options := make([]answer.ObjectiveOption, 0, len(q.Options))
			for _, option := range q.Options {
				options = append(options, answer.ObjectiveOption{Label: string(rune('A' + option.Position)), Text: option.OptionName, Position: option.Position})
			}
			questions = append(questions, answer.ObjectiveQuestion{Position: q.Position, Question: q.Subject, QuestionType: q.QuestionType, Category: q.Category, Options: options})
		}
		provider, err := answer.NewDeepSeek(answer.Config{APIKey: os.Getenv("DEEPSEEK_API_KEY"), BaseURL: *modelBaseURL, Timeout: *timeout})
		if err != nil {
			return err
		}
		result, err := provider.GenerateObjective(ctx, answer.ObjectiveRequest{Problem: answer.ObjectiveSnapshot{Kind: "objective", Title: choice.Task.Challenge.Subject, Statement: choice.Task.Challenge.Description, Questions: questions}, Profile: answer.DefaultProfile()})
		if err != nil {
			return err
		}
		if *mode == "submit" {
			submitted, submitErr := client.ObjectiveSubmit(ctx, choice, result.Candidate.Answers)
			if submitErr != nil {
				return submitErr
			}
			if *jsonOutput {
				return outputJSON(map[string]any{"candidate": result.Candidate, "evaluation": submitted})
			}
			if submitted.ScoreKnown {
				fmt.Printf("客观题提交：得分=%s/%s，通过=%t\n", submitted.Score, submitted.MaxScore, submitted.Passed)
			} else {
				fmt.Printf("客观题提交：正确数=%d/%d，通过=%t（平台未公开满分分值）\n", valueOrZero(submitted.Correct), submitted.Count, submitted.Passed)
			}
			if !submitted.Passed {
				return fmt.Errorf("客观题未达到关卡满分，不进入下一关")
			}
			return nil
		}
		if *jsonOutput {
			return outputJSON(result.Candidate)
		}
		fmt.Printf("客观题候选答案：%s\n", strings.Join(result.Candidate.Answers, ","))
		return nil
	}
	if programmingCommand {
		ctx, stop := context.WithTimeout(ctx, *timeout)
		defer stop()
		if command == "progress" {
			progress, err := client.HomeworkProgress(ctx, *course, *homework)
			if err != nil {
				return err
			}
			if *jsonOutput {
				return outputJSON(progress)
			}
			fmt.Printf("作业关卡：%d，已通过：%d\n", len(progress.Challenges), progress.PassedCount)
			for _, item := range progress.Challenges {
				fmt.Printf("%d\t%s\tpassed_status=%d\n", item.ChallengeID, item.Name, item.PassedStatus)
			}
			return nil
		}
		if command == "solve" {
			store, err := questionbank.Open(*bankPath)
			if err != nil {
				return fmt.Errorf("打开题库: %w", err)
			}
			defer store.Close()
			provider, err := answer.NewDeepSeek(answer.Config{APIKey: os.Getenv("DEEPSEEK_API_KEY"), BaseURL: *modelBaseURL, Timeout: *timeout})
			if err != nil {
				return err
			}
			adapter, err := solver.NewEducoderAdapter(client)
			if err != nil {
				return err
			}
			service := solver.New(store, provider, solver.Config{Timeout: *timeout})
			result, runErr := service.Run(ctx, adapter, solver.Request{Owner: user.Login, IdempotencyKey: *idempotencyKey, Course: *course, HomeworkID: *homework, Game: *game, Mode: solver.Mode(*mode), Timeout: *timeout})
			if result != nil {
				if *jsonOutput {
					if err := outputJSON(result); err != nil {
						return err
					}
				} else {
					fmt.Printf("AI 答题运行 %s：状态=%s，模型调用=%d，候选=%d\n", result.RunID, result.Status, result.ModelCalls, result.CandidateID)
					for _, diagnostic := range result.Diagnostics {
						fmt.Printf("诊断：%s\n", diagnostic)
					}
				}
			}
			return runErr
		}
		task, err := client.LoadTask(ctx, *course, *homework, *game)
		if err != nil {
			return err
		}
		if command == "read" {
			if task.Kind() != educoder.TaskKindProgramming {
				return outputJSON(map[string]any{"kind": task.Kind(), "task": task})
			}
			question, err := client.ReadProgramming(ctx, task)
			if err != nil {
				return err
			}
			if *jsonOutput {
				return outputJSON(question)
			}
			fmt.Printf("题型：%s\n关卡：%s\n作业关卡数：%d\n\n%s\n", question.Kind, task.Challenge.Subject, task.GameCount, question.Markdown)
			for _, file := range question.Files {
				fmt.Printf("\n现存编辑器文件：%s\n文件名：%s\n语言：%s，后缀：%s\n%s\n", file.Path, file.Filename, file.Language, file.Extension, file.Content)
			}
			return nil
		}
		content, err := os.ReadFile(*source)
		if err != nil {
			return fmt.Errorf("读取代码文件: %w", err)
		}
		switch command {
		case "save":
			saved, err := client.SaveCode(ctx, task, *filePath, string(content), *forSubmit)
			if err != nil {
				return err
			}
			if *jsonOutput {
				return outputJSON(saved)
			}
			fmt.Printf("已保存：%s，evaluate=%d，commitID=%s\n", *filePath, saved.Evaluate, saved.CommitID)
			return nil
		case "self-test":
			input, err := os.ReadFile(*stdinFile)
			if err != nil {
				return fmt.Errorf("读取标准输入: %w", err)
			}
			options := educoder.SelfTestOptions{Timeout: *timeout, MaxOutputBytes: *maxOutput}
			if *expectedFile != "" {
				expected, err := os.ReadFile(*expectedFile)
				if err != nil {
					return fmt.Errorf("读取预期输出: %w", err)
				}
				value := string(expected)
				options.ExpectedOutput = &value
			}
			result, err := client.SelfTest(ctx, task, *filePath, string(content), string(input), options)
			if result != nil {
				if *jsonOutput {
					if outputErr := outputJSON(result); outputErr != nil {
						return outputErr
					}
				} else {
					fmt.Print(result.Output)
					fmt.Printf("\n自测结束：%t", result.Completed)
					if result.Matches != nil {
						fmt.Printf("，输出匹配：%t", *result.Matches)
					}
					fmt.Println()
				}
			}
			return err
		case "submit":
			result, err := client.SubmitCode(ctx, task, *filePath, string(content), educoder.PollOptions{Timeout: *timeout, Interval: *pollInterval})
			if err != nil {
				return err
			}
			if *jsonOutput {
				if err := outputJSON(result); err != nil {
					return err
				}
			} else {
				fmt.Printf("评测结束：%t，通过：%t\n%s\n", result.Completed, result.Passed, result.LastCompileOutput)
				if result.NextGame != nil {
					fmt.Printf("下一关标识：%s\n", *result.NextGame)
				}
			}
			if !result.Passed {
				return fmt.Errorf("评测已完成，当前关卡未通过")
			}
			return nil
		}
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

func valueOrZero(value *int) int {
	if value == nil {
		return 0
	}
	return *value
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
