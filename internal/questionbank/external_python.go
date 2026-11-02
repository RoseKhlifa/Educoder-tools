package questionbank

import (
	"path"
	"strings"

	"educoder-cli/internal/problem"
)

func isExternalPythonFile(target problem.File) bool {
	extension := path.Ext(target.Path)
	language := strings.TrimSpace(target.Language)
	if language != "" && !isExternalPythonLanguage(language) {
		return false
	}
	if extension != "" && !strings.EqualFold(extension, ".py") {
		return false
	}
	return strings.EqualFold(extension, ".py") || isExternalPythonLanguage(language)
}

func isExternalPythonLanguage(language string) bool {
	switch strings.ToLower(strings.TrimSpace(language)) {
	case "python", "python2", "python3", "py":
		return true
	default:
		return false
	}
}

// Three observed Python editor round trips expand leading tabs to four spaces
// and replace a lone NBSP on a blank line with an ASCII space. Apply only those
// changes before candidate hashing. Strings, EOF and provenance remain exact;
// ambiguous indentation or string syntax leaves the original whitespace intact.
func normalizeExternalPythonWhitespace(content string) string {
	var out strings.Builder
	var quote byte
	triple := false
	for _, line := range strings.SplitAfter(content, "\n") {
		body := strings.TrimSuffix(line, "\n")
		if quote == 0 {
			indent := 0
			for indent < len(body) && (body[indent] == ' ' || body[indent] == '\t' || body[indent] == '\f') {
				indent++
			}
			prefix := body[:indent]
			if strings.ContainsRune(prefix, '\f') || strings.ContainsRune(prefix, '\t') && strings.ContainsRune(prefix, ' ') {
				return content
			}
			if body == "\u00a0" {
				out.WriteByte(' ')
				out.WriteString(line[len(body):])
			} else if strings.HasPrefix(prefix, "\t") {
				out.WriteString(strings.Repeat("    ", len(prefix)))
				out.WriteString(line[indent:])
			} else {
				out.WriteString(line)
			}
		} else {
			out.WriteString(line)
		}

		for i := 0; i < len(line); i++ {
			ch := line[i]
			if quote != 0 {
				if ch == '\\' {
					// This also preserves escaped physical newlines in ordinary
					// and raw strings without mistaking the next line for code.
					i++
				} else if ch == quote {
					if !triple {
						quote = 0
					} else if i+2 < len(line) && line[i+1] == quote && line[i+2] == quote {
						quote = 0
						triple = false
						i += 2
					}
				} else if ch == '\n' && !triple {
					return content
				}
				continue
			}
			if ch == '#' {
				break
			}
			if ch != '\'' && ch != '"' {
				continue
			}
			// Formatted/template strings can nest Python expressions with
			// their own quotes. Leave such files alone rather than treating
			// this small string scanner as a complete Python parser.
			start := i
			for start > 0 && (line[start-1] >= 'a' && line[start-1] <= 'z' || line[start-1] >= 'A' && line[start-1] <= 'Z') {
				start--
			}
			prefix := strings.ToLower(line[start:i])
			switch prefix {
			case "", "r", "b", "u", "br", "rb":
			default:
				return content
			}
			quote = ch
			triple = i+2 < len(line) && line[i+1] == ch && line[i+2] == ch
			if triple {
				i += 2
			}
		}
	}
	if quote != 0 {
		return content
	}
	return out.String()
}
