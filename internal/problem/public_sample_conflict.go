package problem

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// PublicSampleConflict contains only mechanical dimensions, never source,
// question text, sample values, or a proposed replacement expected output.
type PublicSampleConflict struct {
	SampleIndex   int
	Groups        int
	ExpectedShape string
	RequiredShape string
}

// DetectPublicSampleConflict recognizes one deliberately narrow contract:
// deleting one vertex from an adjacency matrix must reduce its row count by
// one, but every complete public sample instead provides the original n-by-n
// dimensions. It does not infer algorithms, correctness, or replacement values.
// Unrecognized prose, input layout or output is not evidence of a conflict.
func DetectPublicSampleConflict(s Snapshot) *PublicSampleConflict {
	if s.Kind != "programming" || s.EditMode == "terminal" || s.EditMode == "graphic" || len(s.PublicTests) == 0 || len(s.PublicTests) > 32 {
		return nil
	}
	compact := compactPublicProse(s.StatementMarkdown)
	if !strings.Contains(compact, "邻接矩阵") || (!strings.Contains(compact, "删除顶点") && !strings.Contains(compact, "删除一个顶点")) || !explicitDeleteVertexOutput(s.StatementMarkdown) {
		return nil
	}
	var first *PublicSampleConflict
	for i, test := range s.PublicTests {
		groups, ok := deleteVertexInputSizes(test.Stdin)
		if !ok || !completeIntegerMatrices(test.ExpectedStdout, groups) {
			return nil
		}
		if first == nil {
			actual, required := make([]string, 0, len(groups)), make([]string, 0, len(groups))
			for _, n := range groups {
				actual = append(actual, fmt.Sprintf("%dx%d", n, n))
				required = append(required, fmt.Sprintf("%dx%d", n-1, n-1))
			}
			first = &PublicSampleConflict{SampleIndex: i + 1, Groups: len(groups), ExpectedShape: strings.Join(actual, ","), RequiredShape: strings.Join(required, ",")}
		}
	}
	return first
}

func compactPublicProse(text string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || r == '$' || r == '`' || r == '*' {
			return -1
		}
		if r == '−' || r == '－' {
			return '-'
		}
		return r
	}, text)
}

// Only an explicit current output section establishes a prose contract.
// Quoted code, historical/corrected text and negations are not instructions
// about the current output shape, and remain unknown rather than blocking.
func explicitDeleteVertexOutput(markdown string) bool {
	active, found, affirmative := false, false, false
	var fence string
	for _, raw := range strings.Split(markdown, "\n") {
		line := strings.TrimSpace(raw)
		if fence != "" {
			if strings.HasPrefix(line, fence) {
				fence = ""
			}
			continue
		}
		if strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~") {
			fence = line[:3]
			continue
		}
		if strings.HasPrefix(line, "#") {
			heading := strings.TrimLeft(line, "#")
			if heading == line || len(heading) == 0 || (heading[0] != ' ' && heading[0] != '\t') {
				continue
			}
			heading = strings.TrimSpace(strings.TrimRight(strings.TrimSpace(heading), "#"))
			active = heading == "输出" || heading == "输出格式"
			if active {
				if found {
					return false
				}
				found = true
			}
			continue
		}
		if !active || strings.HasPrefix(line, ">") {
			continue
		}
		for _, uncertain := range []string{"不", "无需", "旧版", "原先", "曾经", "更正", "改为", "错误", "示例"} {
			if strings.Contains(line, uncertain) {
				return false
			}
		}
		compact := compactPublicProse(line)
		if strings.HasPrefix(compact, "每组数据输出n-1行") {
			affirmative = true
		}
	}
	return found && affirmative
}

func sampleDataLines(text string) ([]string, bool) {
	if ValidatePublicText(text) != nil || len(text) > 128<<10 {
		return nil, false
	}
	var lines []string
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return lines, true
}

func integerLine(line string, width int) ([]int, bool) {
	fields := strings.Fields(line)
	if len(fields) != width {
		return nil, false
	}
	numbers := make([]int, width)
	for i, field := range fields {
		// Decimal integer text only: no floats, expressions, labels or JSON.
		digits := strings.TrimPrefix(strings.TrimPrefix(field, "+"), "-")
		if digits == "" || strings.IndexFunc(digits, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
			return nil, false
		}
		value, err := strconv.ParseInt(field, 10, 32)
		if err != nil {
			return nil, false
		}
		numbers[i] = int(value)
	}
	return numbers, true
}

func deleteVertexInputSizes(input string) ([]int, bool) {
	lines, ok := sampleDataLines(input)
	if !ok {
		return nil, false
	}
	var groups []int
	for at := 0; at < len(lines); {
		header, ok := integerLine(lines[at], 2)
		if !ok {
			return nil, false
		}
		at++
		n, m := header[0], header[1]
		if n == 0 && m == 0 {
			return groups, at == len(lines) && len(groups) > 0
		}
		if n < 2 || n > 1024 || m < 0 || m > n*(n-1)/2 || len(groups) >= 16 || len(lines)-at < m+1 {
			return nil, false
		}
		seen := make(map[[2]int]bool, m)
		for edge := 0; edge < m; edge++ {
			pair, ok := integerLine(lines[at], 2)
			if !ok || pair[0] < 1 || pair[0] > n || pair[1] < 1 || pair[1] > n || pair[0] == pair[1] {
				return nil, false
			}
			if pair[0] > pair[1] {
				pair[0], pair[1] = pair[1], pair[0]
			}
			key := [2]int{pair[0], pair[1]}
			if seen[key] {
				return nil, false
			}
			seen[key] = true
			at++
		}
		deleted, ok := integerLine(lines[at], 1)
		if !ok || deleted[0] < 1 || deleted[0] > n {
			return nil, false
		}
		at++
		groups = append(groups, n)
	}
	return nil, false // A complete final 0 0 marker is required.
}

func completeIntegerMatrices(output string, groups []int) bool {
	lines, ok := sampleDataLines(output)
	if !ok {
		return false
	}
	at := 0
	for _, n := range groups {
		if len(lines)-at < n {
			return false
		}
		for row := 0; row < n; row++ {
			if _, ok := integerLine(lines[at], n); !ok {
				return false
			}
			at++
		}
	}
	return at == len(lines)
}
