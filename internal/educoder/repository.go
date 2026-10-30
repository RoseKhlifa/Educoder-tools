package educoder

import (
	"context"
	"encoding/base64"
	"errors"
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
	Name       string `json:"name"`
	Path       string `json:"path"`
	Type       string `json:"type"`
	game       string
	myshixun   string
	listedPath string
	listedType string
	client     *Client
}

func repositoryPathValid(value string) bool {
	return value != "" && value != "." && !strings.HasPrefix(value, "/") && !strings.Contains(value, "\\") && !strings.ContainsRune(value, 0) && path.Clean(value) == value && value != ".." && !strings.HasPrefix(value, "../")
}

func repositoryTaskValid(t *Task) error {
	if err := requireProgramming(t); err != nil {
		return err
	}
	if validIdentifier(t.MyShixun.Identifier) != nil {
		return errors.New("repository requires the task myshixun identifier")
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
			return nil, errors.New("repository directory must come from this task listing")
		}
		directory = parent.Path
	}
	var response struct {
		Trees []RepositoryEntry `json:"trees"`
	}
	if err := c.request(ctx, http.MethodPost, "/api/myshixuns/"+t.MyShixun.Identifier+"/repository.json", nil, map[string]any{"path": directory}, taskRef(t), &response); err != nil {
		return nil, err
	}
	expectedParent := directory
	if expectedParent == "" {
		expectedParent = "."
	}
	for i := range response.Trees {
		entry := &response.Trees[i]
		if !repositoryPathValid(entry.Path) || path.Dir(entry.Path) != expectedParent || (entry.Type != "tree" && entry.Type != "blob") {
			return nil, errors.New("repository returned an invalid or unexpected entry")
		}
		entry.game, entry.myshixun, entry.listedPath, entry.listedType = t.Game.Identifier, t.MyShixun.Identifier, entry.Path, entry.Type
		entry.client = c
	}
	return response.Trees, nil
}

// ReadRepositoryFile reads supporting source shown by ListRepository. The
// result remains read-only: SaveCode still requires challenge.path.
func (c *Client) ReadRepositoryFile(ctx context.Context, t *Task, entry RepositoryEntry) (*CodeFile, error) {
	if err := repositoryTaskValid(t); err != nil {
		return nil, err
	}
	if !entry.boundTo(c, t, "blob") {
		return nil, errors.New("repository file must come from this task listing")
	}
	var response struct {
		Content struct {
			Content string `json:"content"`
			Size    int64  `json:"size"`
		} `json:"content"`
		Filename string `json:"filename"`
		Language string `json:"language"`
		FileType string `json:"file_type"`
	}
	query := url.Values{"path": {entry.Path}, "homework_common_id": {strconv.FormatInt(t.HomeworkID, 10)}}
	if err := c.request(ctx, http.MethodGet, "/api/tasks/"+t.Game.Identifier+"/rep_content.json", query, nil, taskRef(t), &response); err != nil {
		return nil, err
	}
	content, err := base64.StdEncoding.DecodeString(response.Content.Content)
	if err != nil || !utf8.Valid(content) {
		return nil, errors.New("repository file content is not Base64 UTF-8")
	}
	return &CodeFile{Path: entry.Path, Filename: response.Filename, Extension: path.Ext(response.Filename), Language: response.Language, FileType: response.FileType, Content: string(content), Size: response.Content.Size}, nil
}
