package educoder

import (
	"reflect"
	"testing"
)

func TestGraphicPathsStatementLanguage(t *testing.T) {
	for _, tc := range []struct {
		name        string
		description string
		want        []string
	}{
		{"save-to-named", "请画出开始节点。将画好的模型保存到workspace/myshixun/submit/submit_step1/下，并名称为step1，格式为mdj。", []string{"submit/submit_step1/step1.mdj"}},
		{"save-to-dot-extension", "将模型保存到 workspace/myshixun/submit/submit_step2/ 下，名称为 step2，格式为 .mdj。", []string{"submit/submit_step2/step2.mdj"}},
		{"save-to-quoted-extension", "将类图保存到“workspace/myshixun/类图/submit/”目录下，名称为“step3”，格式为“.mdj”。", []string{"类图/submit/step3.mdj"}},
		{"save-to-explicit-extension", "保存到 /home/headless/Desktop/workspace/myshixun/submit/ 下，名称为 step4.MDJ，格式为 MDJ。", []string{"submit/step4.MDJ"}},
		{"repeated-instruction", "保存到 workspace/myshixun/submit/ 下，名称为 step1，格式为 mdj。保存在 workspace/myshixun/submit/ 下，命名为 step1，格式为 mdj。", []string{"submit/step1.mdj"}},
		{"different-directories-ambiguous", "保存到 workspace/myshixun/first/ 下，名称为 step1，格式为 mdj；保存到 workspace/myshixun/second/ 下，名称为 step1，格式为 mdj。", nil},
		{"different-names-ambiguous", "保存到 workspace/myshixun/submit/ 下，名称为 step1 或命名为 step2，格式为 mdj。", nil},
		{"no-guessed-directory", "请绘制用例图，名称为 step1，格式为 mdj。", nil},
		{"no-guessed-name", "请将模型保存到 workspace/myshixun/submit/ 下，格式为 mdj。", nil},
		{"different-format", "保存到 workspace/myshixun/submit/ 下，名称为 step1，格式为 png。", nil},
		{"different-file-extension", "保存到 workspace/myshixun/submit/ 下，名称为 step1.txt，格式为 mdj。", nil},
		{"filename-prefix-is-not-mdj", "保存到 workspace/myshixun/submit/ 下，名称为 step1.mdj.bak，格式为 mdj。", nil},
		{"full-path-prefix-is-not-mdj", "模型保存为 workspace/myshixun/submit/step1.mdj.bak。", nil},
		{"full-path-root-boundary", "模型保存为 archiveworkspace/myshixun/submit/step1.mdj。", nil},
		{"outside-directory", "保存到 workspace/myshixun/../submit/ 下，名称为 step1，格式为 mdj。", nil},
		{"protected-directory", "保存到 workspace/myshixun/pingce/ 下，名称为 step1，格式为 mdj。", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			task := graphicTaskFixture(t)
			task.Challenge.Description = tc.description
			task.Challenge.Path = "s/s.txt"
			if got := task.GraphicPaths(); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("GraphicPaths()=%v, want %v", got, tc.want)
			}
			wantKind := TaskKindUnknown
			if len(tc.want) == 1 {
				wantKind = TaskKindGraphic
			}
			if got := task.Kind(); got != wantKind {
				t.Fatalf("Kind()=%s, want %s", got, wantKind)
			}
		})
	}
}

func TestGraphicPathsDoNotFallBackFromAmbiguousStatement(t *testing.T) {
	task := graphicTaskFixture(t)
	task.Challenge.Description = "保存到 workspace/myshixun/first/ 下，名称为 step1，格式为 mdj；保存到 workspace/myshixun/second/ 下，名称为 step1，格式为 mdj。"
	task.Challenge.Path = "editor/placeholder.mdj"
	if got := task.GraphicPaths(); len(got) == 1 {
		t.Fatalf("ambiguous statement resolved to an editor fallback: %v", got)
	}
	if task.Kind() != TaskKindUnknown {
		t.Fatal("ambiguous graphic statement entered an executable route")
	}
}

func TestVNCPlaceholderWithoutGraphicPathIsNotProgramming(t *testing.T) {
	task := graphicTaskFixture(t)
	task.Challenge.Description = "请在桌面图形环境中完成绘图。"
	task.Challenge.Path = "s/s.txt"
	yes := true
	task.Challenge.WithCodeFile = &yes
	if got := task.Kind(); got != TaskKindUnknown {
		t.Fatalf("VNC placeholder entered %s route", got)
	}
}
