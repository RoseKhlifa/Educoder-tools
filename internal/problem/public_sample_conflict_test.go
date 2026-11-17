package problem

import (
	"strings"
	"testing"
)

func syntheticDeleteVertexSample() Snapshot {
	return Snapshot{Kind: "programming", Title: "图表示练习", StatementMarkdown: "使用邻接矩阵表示图并删除顶点。\n#### 输出\n每组数据输出 n - 1 行，每行 n - 1 个数字。", Files: []File{{Path: "practice/solution.h", Language: "cpp"}}, PublicTests: []PublicTest{{
		Stdin:          "4 3\n1 2\n2 3\n3 4\n2\n5 2\n1 4\n2 5\n4\n0 0\n",
		ExpectedStdout: "0 1 0 0\n1 0 1 0\n0 1 0 1\n0 0 1 0\n0 0 0 1 0\n0 0 0 0 1\n0 0 0 0 0\n1 0 0 0 0\n0 1 0 0 0\n",
	}}}
}

func TestPublicSampleConflictRecognizesOnlyCompleteDeleteVertexShapeContradiction(t *testing.T) {
	s := syntheticDeleteVertexSample()
	before := s.PublicTests[0]
	conflict := DetectPublicSampleConflict(s)
	if conflict == nil || conflict.SampleIndex != 1 || conflict.Groups != 2 || conflict.ExpectedShape != "4x4,5x5" || conflict.RequiredShape != "3x3,4x4" {
		t.Fatalf("conflict=%+v", conflict)
	}
	if s.PublicTests[0] != before {
		t.Fatal("conflict inspection rewrote public test data")
	}
	for _, wording := range []string{"每组数据输出n-1行", "每组数据输出 n−1 行", "每组数据输出 $n-1$ 行"} {
		s.StatementMarkdown = "在此图中删除一个顶点；邻接矩阵；\n### 输出格式\n" + wording + "，每行n-1个数。"
		if DetectPublicSampleConflict(s) == nil {
			t.Errorf("explicit row contract not recognized: %q", wording)
		}
	}
}

func TestPublicSampleConflictDoesNotInferUnknownOrOrdinaryWrongAnswerSemantics(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Snapshot)
	}{
		{"different task kind", func(s *Snapshot) { s.Kind = "objective" }},
		{"negated requirement", func(s *Snapshot) {
			s.StatementMarkdown = "删除顶点；邻接矩阵。\n### 输出\n不是每组数据输出n-1行，应输出n行。"
		}},
		{"corrected historical requirement", func(s *Snapshot) {
			s.StatementMarkdown = "删除顶点；邻接矩阵。\n### 输出\n旧版本：每组数据输出n-1行。更正后输出n行。"
		}},
		{"quoted code is not current requirement", func(s *Snapshot) {
			s.StatementMarkdown = "删除顶点；邻接矩阵。\n### 输出\n```text\n每组数据输出n-1行\n```\n每组数据输出n行。"
		}},
		{"no explicit current output section", func(s *Snapshot) { s.StatementMarkdown = "删除顶点；邻接矩阵；每组数据输出n-1行。" }},
		{"missing delete contract", func(s *Snapshot) { s.StatementMarkdown = "邻接矩阵，每组数据输出n-1行。" }},
		{"missing matrix contract", func(s *Snapshot) { s.StatementMarkdown = "删除顶点，每组数据输出n-1行。" }},
		{"vague output contract", func(s *Snapshot) { s.StatementMarkdown = "删除顶点，输出新的邻接矩阵。" }},
		{"different declared row count", func(s *Snapshot) { s.StatementMarkdown = "删除顶点，邻接矩阵，每组数据输出n行。" }},
		{"missing terminator", func(s *Snapshot) { s.PublicTests[0].Stdin = strings.TrimSuffix(s.PublicTests[0].Stdin, "0 0\n") }},
		{"trailing input", func(s *Snapshot) { s.PublicTests[0].Stdin += "1 2\n" }},
		{"bad edge vertex", func(s *Snapshot) {
			s.PublicTests[0].Stdin = strings.Replace(s.PublicTests[0].Stdin, "1 2\n", "1 9\n", 1)
		}},
		{"bad deletion vertex", func(s *Snapshot) {
			s.PublicTests[0].Stdin = strings.Replace(s.PublicTests[0].Stdin, "\n2\n5", "\n7\n5", 1)
		}},
		{"weighted edges", func(s *Snapshot) {
			s.PublicTests[0].Stdin = strings.Replace(s.PublicTests[0].Stdin, "1 2\n", "1 2 9\n", 1)
		}},
		{"fractional input", func(s *Snapshot) {
			s.PublicTests[0].Stdin = strings.Replace(s.PublicTests[0].Stdin, "4 3\n", "4.0 3\n", 1)
		}},
		{"incomplete edge list", func(s *Snapshot) { s.PublicTests[0].Stdin = "4 3\n1 2\n2\n0 0\n" }},
		{"zero groups", func(s *Snapshot) { s.PublicTests[0].Stdin = "0 0\n" }},
		{"extra expected row", func(s *Snapshot) { s.PublicTests[0].ExpectedStdout += "0 0 0\n" }},
		{"wrong expected width", func(s *Snapshot) {
			s.PublicTests[0].ExpectedStdout = strings.Replace(s.PublicTests[0].ExpectedStdout, "0 1 0 0\n", "0 1 0\n", 1)
		}},
		{"non matrix expected", func(s *Snapshot) { s.PublicTests[0].ExpectedStdout = "compilation failed\n" }},
		{"fractional expected", func(s *Snapshot) {
			s.PublicTests[0].ExpectedStdout = strings.Replace(s.PublicTests[0].ExpectedStdout, "0 1 0 0", "0 1.5 0 0", 1)
		}},
		{"corrected expected dimensions", func(s *Snapshot) {
			s.PublicTests[0].ExpectedStdout = "0 0 0\n0 0 1\n0 1 0\n0 0 0 0\n0 0 0 1\n0 0 0 0\n0 1 0 0\n"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := syntheticDeleteVertexSample()
			tc.change(&s)
			if conflict := DetectPublicSampleConflict(s); conflict != nil {
				t.Fatalf("unproven conflict accepted: %+v", conflict)
			}
		})
	}
}
