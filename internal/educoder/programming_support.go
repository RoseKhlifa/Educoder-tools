package educoder

import (
	"context"
	"path"
	"regexp"
	"sort"
	"strings"

	"educoder-cli/internal/problem"
)

// ReadProgrammingSupport supplies only a header-only task's visible sibling
// main or exact matching-header-stem caller. It follows observed entries along the
// editable header path, never scans other directories or evaluation materials.
// Missing/incomplete context is optional; no partial or truncated source escapes.
func (c *Client) ReadProgrammingSupport(ctx context.Context, t *Task) ([]CodeFile, error) {
	paths := t.CodePaths()
	if len(paths) == 0 {
		return nil, nil
	}
	directories := map[string][]string{}
	for _, file := range paths {
		if !headerExtension(file) {
			return nil, nil
		}
		if !repositoryPathValid(file) {
			return nil, &ProgrammingSupportError{Stage: "repository_task", Reason: "invalid_path"}
		}
		dir := path.Dir(file)
		if dir == "." {
			dir = ""
		}
		if strings.Count(dir, "/") >= 8 {
			return nil, &ProgrammingSupportError{Stage: "directory", Reason: "depth_limit"}
		}
		directories[dir] = append(directories[dir], path.Base(file))
	}
	if len(directories) > 8 {
		return nil, &ProgrammingSupportError{Stage: "directory", Reason: "directory_limit"}
	}
	root, err := c.ListRepository(ctx, t, nil)
	if err != nil {
		return nil, err
	}
	listed := map[string][]RepositoryEntry{"": root}
	dirs := make([]string, 0, len(directories))
	for dir := range directories {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	var files []CodeFile
	total := 0
	for _, dir := range dirs {
		entries := root
		current := ""
		if dir != "" {
			for _, component := range strings.Split(dir, "/") {
				wanted := path.Join(current, component)
				cached, ok := listed[wanted]
				if ok {
					entries, current = cached, wanted
					continue
				}
				var observed *RepositoryEntry
				for i := range entries {
					if entries[i].Path == wanted && entries[i].Type == "tree" {
						observed = &entries[i]
						break
					}
				}
				if observed == nil {
					return nil, supportFailure("directory", "no_matching_driver")
				}
				entries, err = c.ListRepository(ctx, t, observed)
				if err != nil {
					return nil, err
				}
				listed[wanted], current = entries, wanted
			}
		}
		if len(entries) > 512 {
			return nil, &ProgrammingSupportError{Stage: "directory", Reason: "directory_limit"}
		}
		for _, entry := range entries {
			name := path.Base(entry.Path)
			if entry.Type != "blob" || !headerDriverName(name, directories[dir]) {
				continue
			}
			file, readErr := c.ReadRepositoryFile(ctx, t, entry)
			if readErr != nil {
				return nil, readErr
			}
			if file.Content == "" || len(file.Content) > 64<<10 || file.Language == "" || problem.ValidatePublicText(file.Content) != nil {
				return nil, &ProgrammingSupportError{Stage: "driver", Reason: "unsupported_content"}
			}
			// Do not mistake an unrelated sibling program for this header's caller.
			if !headerCallerMatches(file.Content, directories[dir]) || !sourceHasMainDefinition(file.Content) {
				continue
			}
			total += len(file.Content)
			if total > 128<<10 || len(files) >= 8 {
				return nil, &ProgrammingSupportError{Stage: "driver", Reason: "content_limit"}
			}
			files = append(files, *file)
		}
	}
	return files, nil
}

func headerExtension(file string) bool {
	switch strings.ToLower(path.Ext(file)) {
	case ".h", ".hpp", ".hh", ".hxx":
		return true
	}
	return false
}

func headerDriverName(name string, headers []string) bool {
	ext := path.Ext(name)
	switch strings.ToLower(ext) {
	case ".c", ".cc", ".cpp", ".cxx":
	default:
		return false
	}
	stem := strings.TrimSuffix(name, ext)
	lower := strings.ToLower(stem)
	for _, forbidden := range []string{"grader", "test", "checker", "judge", "evaluate"} {
		if strings.Contains(lower, forbidden) {
			return false
		}
	}
	if stem == "main" {
		return true
	}
	for _, header := range headers {
		if stem == strings.TrimSuffix(header, path.Ext(header)) {
			return true
		}
	}
	return false
}

var localHeaderInclude = regexp.MustCompile(`^#\s*include\s*"([^"\r\n]+)"`)
var conditionalDirective = regexp.MustCompile(`^#\s*(?:if|ifdef|ifndef|elif|else|endif)\b`)

func headerCallerMatches(source string, allowed []string) bool {
	// Multiple local headers make this conservative caller association
	// ambiguous. Standard-library angle-bracket includes do not affect it.
	var includes [][]string
	for _, directive := range sourceDirectives(source) {
		// Determining active includes inside conditional compilation needs the
		// build flags. Do not invent those flags or infer a caller association.
		if conditionalDirective.MatchString(directive) {
			return false
		}
		if match := localHeaderInclude.FindStringSubmatch(directive); match != nil {
			includes = append(includes, match)
		}
	}
	if len(includes) != 1 {
		return false
	}
	include := strings.TrimPrefix(includes[0][1], "./")
	if path.Base(include) != include || !repositoryPathValid(include) {
		return false
	}
	for _, header := range allowed {
		if include == header {
			return true
		}
	}
	return false
}

// sourceDirectives recognizes line-prefix # directives outside C/C++ comments,
// quoted literals and raw strings. It is deliberately not a C preprocessor.
func sourceDirectives(source string) []string {
	source = strings.ReplaceAll(strings.ReplaceAll(source, "\\\r\n", ""), "\\\n", "")
	var directives []string
	prefix := true
	for i := 0; i < len(source); {
		switch {
		case source[i] == '\n':
			prefix = true
			i++
		case source[i] == ' ' || source[i] == '\t' || source[i] == '\r' || source[i] == '\f' || source[i] == '\v':
			i++
		case strings.HasPrefix(source[i:], "//"):
			if end := strings.IndexByte(source[i:], '\n'); end >= 0 {
				i += end
			} else {
				i = len(source)
			}
		case strings.HasPrefix(source[i:], "/*"):
			end := strings.Index(source[i+2:], "*/")
			if end < 0 {
				return directives
			}
			comment := source[i : i+end+4]
			if strings.ContainsRune(comment, '\n') {
				prefix = true
			}
			i += end + 4
		case source[i] == 'R' && i+1 < len(source) && source[i+1] == '"':
			prefix = false
			open := strings.IndexByte(source[i+2:], '(')
			if open >= 0 && open <= 16 {
				delimiter := source[i+2 : i+2+open]
				if !strings.ContainsAny(delimiter, " ()\\\t\r\n") {
					closing := ")" + delimiter + "\""
					if end := strings.Index(source[i+3+open:], closing); end >= 0 {
						i += 3 + open + end + len(closing)
						continue
					}
				}
			}
			i++
		case source[i] == '\'' || source[i] == '"':
			prefix = false
			quote := source[i]
			i++
			for i < len(source) {
				if source[i] == '\\' {
					i += 2
					continue
				}
				if source[i] == quote {
					i++
					break
				}
				i++
			}
		case source[i] == '#' && prefix:
			end := strings.IndexByte(source[i:], '\n')
			if end < 0 {
				end = len(source) - i
			}
			directives = append(directives, source[i:i+end])
			// Continue lexing the directive line so a trailing /* comment
			// still suppresses apparent includes on following lines.
			i++
			prefix = false
		default:
			prefix = false
			i++
		}
	}
	return directives
}
