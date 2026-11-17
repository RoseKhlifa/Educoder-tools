package educoder

import (
	"regexp"
	"strings"
)

var mainDefinition = regexp.MustCompile(`\b(?:int|signed(?:\s+int)?|void|auto)\s+main\s*\([^;{}]*\)\s*(?:noexcept(?:\s*\([^)]*\))?\s*)?(?:->\s*int\s*)?\{`)

// A matching filename/include alone does not establish a caller. Require a
// main definition in actual source, not a declaration, comment or string.
func sourceHasMainDefinition(source string) bool {
	source = strings.ReplaceAll(strings.ReplaceAll(source, "\\\r\n", ""), "\\\n", "")
	code := []byte(source)
	prefix := true
	directive := false
	for i := 0; i < len(source); {
		start, end := i, i
		switch {
		case source[i] == '\n':
			prefix = true
			directive = false
			i++
			continue
		case source[i] == ' ' || source[i] == '\t' || source[i] == '\r':
			i++
			continue
		case strings.HasPrefix(source[i:], "//"):
			end = strings.IndexByte(source[i:], '\n')
			if end < 0 {
				end = len(source)
			} else {
				end += i
			}
		case strings.HasPrefix(source[i:], "/*"):
			end = strings.Index(source[i+2:], "*/")
			if end < 0 {
				return false
			}
			end += i + 4
		case source[i] == '#' && prefix:
			// Continue scanning this line so a trailing block comment can
			// suppress fake definitions on following lines as well.
			directive, prefix = true, false
			code[i] = ' '
			i++
			continue
		case source[i] == 'R' && i+1 < len(source) && source[i+1] == '"':
			open := strings.IndexByte(source[i+2:], '(')
			if open < 0 || open > 16 {
				return false
			}
			delimiter := source[i+2 : i+2+open]
			if strings.ContainsAny(delimiter, " ()\\\t\r\n") {
				return false
			}
			closing := ")" + delimiter + "\""
			end = strings.Index(source[i+3+open:], closing)
			if end < 0 {
				return false
			}
			end += i + 3 + open + len(closing)
		case source[i] == '\'' || source[i] == '"':
			quote := source[i]
			end = i + 1
			for end < len(source) {
				if source[end] == '\\' {
					end += 2
					continue
				}
				if source[end] == quote {
					end++
					break
				}
				end++
			}
			if end > len(source) {
				return false
			}
		default:
			if directive {
				code[i] = ' '
			}
			prefix = false
			i++
			continue
		}
		for j := start; j < end; j++ {
			if code[j] != '\n' {
				code[j] = ' '
			}
		}
		if strings.ContainsRune(source[start:end], '\n') {
			prefix = true
			directive = false
		}
		i = end
	}
	return mainDefinition.Match(code)
}
