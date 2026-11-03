package educoder

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

const (
	metadataCourses   = "courses"
	metadataHomeworks = "homeworks"
	metadataHeader    = "homework_header"
	metadataRoster    = "homework_roster"
	metadataProgress  = "homework_progress"
)

func metadataOperationLabel(operation string) string {
	switch operation {
	case "", metadataProgress:
		return metadataProgress
	case metadataCourses, metadataHomeworks, metadataHeader, metadataRoster:
		return operation
	default:
		return "metadata_read"
	}
}

// GET alone is not a read guarantee: shixun_exec can create a runtime. This
// helper accepts only the five metadata operations already used as reads.
func metadataReadPath(operation, path string) bool {
	parts := strings.Split(path, "/")
	if len(parts) != 5 || parts[0] != "" || parts[1] != "api" || parts[3] == "" {
		return false
	}
	switch operation {
	case metadataCourses:
		return parts[2] == "users" && parts[4] == "courses.json"
	case metadataHomeworks:
		return parts[2] == "courses" && parts[4] == "homework_commons.json"
	case metadataHeader:
		return parts[2] == "shixun_homeworks" && parts[4] == "header_info.json"
	case metadataRoster:
		return parts[2] == "myshixuns" && parts[4] == "challenges.json"
	case metadataProgress:
		return parts[2] == "homework_commons" && parts[4] == "shixun_challenge_data.json"
	}
	return false
}

// Each successful response must also satisfy its collection shape. Missing
// data is not an empty plan and never triggers a second semantic interpretation.
func (c *Client) readMetadata(ctx context.Context, operation, path string, query url.Values, ref string, out any, valid func() bool) error {
	if !metadataReadPath(operation, path) {
		return &MetadataReadError{Operation: "metadata_read", Stage: "prepare", Category: "unsupported_operation"}
	}
	// CollectionReader already retries within its transport and enforces its
	// own read budget. Never multiply its three wire attempts by an outer loop.
	if _, collecting := c.HTTP.Transport.(*collectionTransport); collecting {
		if err := c.request(ctx, http.MethodGet, path, query, nil, ref, out); err != nil {
			return err
		}
		if valid != nil && !valid() {
			return &MetadataReadError{Operation: operation, Stage: "decode", Category: "schema", HTTPStatus: http.StatusOK, Attempts: 1}
		}
		return nil
	}
	for attempt := 1; ; attempt++ {
		err := c.requestWithReadDiagnostics(ctx, http.MethodGet, path, query, nil, ref, out, true, 0)
		if err == nil {
			if valid != nil && !valid() {
				return &MetadataReadError{Operation: operation, Stage: "decode", Category: "schema", HTTPStatus: http.StatusOK, Attempts: attempt}
			}
			return nil
		}
		var readError *MetadataReadError
		if !errors.As(err, &readError) {
			return err
		}
		readError.Operation, readError.Attempts = operation, attempt
		if attempt >= homeworkProgressReadAttempts || !readError.transient() {
			return readError
		}
		if err := waitHomeworkProgressRetry(ctx, attempt); err != nil {
			return &MetadataReadError{Operation: operation, Stage: "backoff", Category: "canceled", Attempts: attempt, canceled: err}
		}
	}
}
