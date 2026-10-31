package educoder

import "testing"

func TestPublicOutputMatchesPreservesOutputData(t *testing.T) {
	for _, tc := range []struct {
		name             string
		actual, expected string
		want             bool
	}{
		{name: "CRLF and one terminal LF", actual: "first\r\nsecond\r\n", expected: "first\nsecond", want: true},
		{name: "missing terminal LF", actual: "first\nsecond", expected: "first\nsecond\n", want: true},
		{name: "same blank lines", actual: "first\n\nsecond\n\n", expected: "first\r\n\r\nsecond\r\n\r\n", want: true},
		{name: "extra blank line", actual: "first\nsecond\n\n", expected: "first\nsecond\n"},
		{name: "internal blank line", actual: "first\n\nsecond\n", expected: "first\nsecond\n"},
		{name: "leading spaces", actual: " 42\n", expected: "42\n"},
		{name: "trailing spaces", actual: "42 \n", expected: "42\n"},
		{name: "bare CR", actual: "42\r", expected: "42\n"},
		{name: "genuinely empty runtime", actual: "", expected: "42\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := PublicOutputMatches(tc.actual, tc.expected); got != tc.want {
				t.Fatalf("actual=%q expected=%q matches=%t", tc.actual, tc.expected, got)
			}
		})
	}
}
