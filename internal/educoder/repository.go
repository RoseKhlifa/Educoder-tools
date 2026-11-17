package educoder

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"unicode/utf8"
)

// RepositoryEntry is an upstream-visible task repository item. It grants read
// access only to the exact item returned for this task, never code-save access.
type RepositoryEntry struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Type        string `json:"type"`
	game        string
	myshixun    string
	listedPath  string
	listedType  string
	pathPresent bool
	client      *Client
}

func (entry *RepositoryEntry) UnmarshalJSON(data []byte) error {
	var wire struct {
		Name string          `json:"name"`
		Path json.RawMessage `json:"path"`
		Type string          `json:"type"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	entry.Name, entry.Type, entry.pathPresent = wire.Name, wire.Type, len(wire.Path) > 0
	entry.Path = ""
	if entry.pathPresent {
		return json.Unmarshal(wire.Path, &entry.Path)
	}
	return nil
}

func repositoryPathValid(value string) bool {
	return value != "" && value != "." && !strings.HasPrefix(value, "/") && !strings.Contains(value, "\\") && !strings.ContainsRune(value, 0) && path.Clean(value) == value && value != ".." && !strings.HasPrefix(value, "../")
}

func repositoryTaskValid(t *Task) error {
	if err := requireProgramming(t); err != nil {
		return &ProgrammingSupportError{Stage: "repository_task", Reason: "unsupported_task"}
	}
	if validIdentifier(t.MyShixun.Identifier) != nil {
		return &ProgrammingSupportError{Stage: "repository_task", Reason: "missing_task_identifier"}
	}
	return nil
}

func (entry RepositoryEntry) boundTo(c *Client, t *Task, kind string) bool {
	return entry.client == c && entry.game == t.Game.Identifier && entry.myshixun == t.MyShixun.Identifier && entry.Path == entry.listedPath && entry.Type == entry.listedType && entry.Type == kind && repositoryPathValid(entry.Path)
}

// ListRepository reads the root when parent is nil, or an observed tree entry.
// The published task frontend implements this read through POST repository.json.
func (c *Client) ListRepository(ctx context.Context, t *Task, parent *RepositoryEntry) ([]RepositoryEntry, error) {
	if err := repositoryTaskValid(t); err != nil {
		return nil, err
	}
	directory := ""
	if parent != nil {
		if !parent.boundTo(c, t, "tree") {
			return nil, &ProgrammingSupportError{Stage: "directory", Reason: "invalid_parent"}
		}
		directory = parent.Path
	}
	var response struct {
		Trees *[]RepositoryEntry `json:"trees"`
	}
	stage := "root"
	if parent != nil {
		stage = "directory"
	}
	if err := c.requestWithReadDiagnostics(ctx, http.MethodPost, "/api/myshixuns/"+t.MyShixun.Identifier+"/repository.json", nil, map[string]any{"path": directory}, taskRef(t), &response, true); err != nil {
		return nil, supportRequestFailure(stage, err)
	}
	if response.Trees == nil {
		return nil, &ProgrammingSupportError{Stage: stage, Reason: "missing_trees"}
	}
	expectedParent := directory
	if expectedParent == "" {
		expectedParent = "."
	}
	for i := range *response.Trees {
		entry := &(*response.Trees)[i]
		// The official tree mapper uses the requested parent plus returned
		// basename. Reconstruct missing paths only; never repair conflicts.
		validName := repositoryPathValid(entry.Name) && path.Base(entry.Name) == entry.Name
		if !entry.pathPresent && entry.Path == "" {
			if !validName {
				return nil, supportFailure(stage, "invalid_entry")
			}
			entry.Path = path.Join(directory, entry.Name)
		}
		if entry.Name != "" && (!validName || path.Base(entry.Path) != entry.Name) {
			return nil, supportFailure(stage, "invalid_entry")
		}
		if !repositoryPathValid(entry.Path) || path.Dir(entry.Path) != expectedParent || (entry.Type != "tree" && entry.Type != "blob") {
			return nil, &ProgrammingSupportError{Stage: stage, Reason: "invalid_entry"}
		}
		entry.game, entry.myshixun, entry.listedPath, entry.listedType = t.Game.Identifier, t.MyShixun.Identifier, entry.Path, entry.Type
		entry.client = c
	}
	return *response.Trees, nil
}

// ReadRepositoryFile reads supporting source shown by ListRepository. The
// result remains read-only: SaveCode still requires challenge.path.
func (c *Client) ReadRepositoryFile(ctx context.Context, t *Task, entry RepositoryEntry) (*CodeFile, error) {
	if err := repositoryTaskValid(t); err != nil {
		return nil, err
	}
	if !entry.boundTo(c, t, "blob") {
		return nil, &ProgrammingSupportError{Stage: "driver", Reason: "invalid_path"}
	}
	var response struct {
		Content struct {
			Content *string `json:"content"`
			Size    int64   `json:"size"`
		} `json:"content"`
		Filename string `json:"filename"`
		Language string `json:"language"`
		FileType string `json:"file_type"`
	}
	query := url.Values{"path": {entry.Path}, "homework_common_id": {strconv.FormatInt(t.HomeworkID, 10)}}
	if err := c.requestWithReadDiagnostics(ctx, http.MethodGet, "/api/tasks/"+t.Game.Identifier+"/rep_content.json", query, nil, taskRef(t), &response, true); err != nil {
		return nil, supportRequestFailure("driver", err)
	}
	if response.Filename != path.Base(entry.Path) {
		return nil, &ProgrammingSupportError{Stage: "driver", Reason: "filename_mismatch"}
	}
	if response.Content.Content == nil {
		return nil, &ProgrammingSupportError{Stage: "driver", Reason: "missing_content"}
	}
	content, err := base64.StdEncoding.DecodeString(*response.Content.Content)
	if err != nil || !utf8.Valid(content) {
		return nil, &ProgrammingSupportError{Stage: "driver", Reason: "invalid_content"}
	}
	if response.Content.Size != 0 && response.Content.Size != int64(len(content)) {
		return nil, &ProgrammingSupportError{Stage: "driver", Reason: "content_size_mismatch"}
	}
	return &CodeFile{Path: entry.Path, Filename: response.Filename, Extension: path.Ext(response.Filename), Language: response.Language, FileType: response.FileType, Content: string(content), Size: response.Content.Size}, nil
}
