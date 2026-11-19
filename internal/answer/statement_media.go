package answer

import (
	"encoding/base64"
	"strings"

	"educoder-cli/internal/problem"
)

// MaxStatementImages and MaxStatementImageBytes bound the figures attached to
// one programming request. Statements with more figures attach the first ones
// in statement order and list the rest as unavailable.
const (
	MaxStatementImages     = 16
	MaxStatementImageBytes = 24 << 20
	maxStatementImageSize  = 10 << 20
)

// StatementImage is one figure referenced by a programming statement. Content
// is transport-only: JSON (and therefore generation keys, checkpoints and
// logs) carries only the statement reference, media type and digest.
type StatementImage struct {
	Locator   string `json:"locator"`
	MediaType string `json:"media_type"`
	SHA256    string `json:"sha256"`
	Content   []byte `json:"-"`
}

func (StatementImage) String() string   { return "statement image (content redacted)" }
func (StatementImage) GoString() string { return "statement image (content redacted)" }

type statementImageReference struct {
	ImageIndex int    `json:"image_index"`
	Locator    string `json:"locator"`
	MediaType  string `json:"media_type"`
	SHA256     string `json:"sha256"`
}

// prepareStatementImages binds each image to a reference that literally
// appears in the statement, validates its bytes and deduplicates identical
// figures into one provider attachment.
func prepareStatementImages(statement string, images []StatementImage, unavailable []string) ([]string, []statementImageReference, error) {
	if len(images) > MaxStatementImages || len(unavailable) > 256 {
		return nil, nil, &Error{Kind: "invalid_statement_image"}
	}
	urls := make([]string, 0, len(images))
	refs := make([]statementImageReference, 0, len(images))
	indexes := make(map[string]int, len(images))
	total := 0
	for _, img := range images {
		if img.Locator == "" || len(img.Locator) > 2048 || !strings.Contains(statement, img.Locator) || problem.ValidatePublicText(img.Locator) != nil {
			return nil, nil, &Error{Kind: "invalid_statement_image"}
		}
		hash, valid := validatedImage(img.MediaType, img.Content, maxStatementImageSize)
		if !valid || (img.SHA256 != "" && img.SHA256 != hash) {
			return nil, nil, &Error{Kind: "invalid_statement_image"}
		}
		index, seen := indexes[hash]
		if !seen {
			if total+len(img.Content) > MaxStatementImageBytes {
				return nil, nil, &Error{Kind: "invalid_statement_image"}
			}
			total += len(img.Content)
			urls = append(urls, "data:"+img.MediaType+";base64,"+base64.StdEncoding.EncodeToString(img.Content))
			index = len(urls)
			indexes[hash] = index
		}
		refs = append(refs, statementImageReference{ImageIndex: index, Locator: img.Locator, MediaType: img.MediaType, SHA256: hash})
	}
	for _, locator := range unavailable {
		if locator == "" || len(locator) > 2048 || !strings.Contains(statement, locator) || problem.ValidatePublicText(locator) != nil {
			return nil, nil, &Error{Kind: "invalid_statement_image"}
		}
	}
	return urls, refs, nil
}

const statementImagePrompt = " The statement's figures are attached as images in attachment order. statement_images maps each image_index (1-based) to the exact reference used in statement_markdown. Inspect the actual figures (diagrams, tables, formulas, sample input/output screenshots) instead of guessing from the reference text. statement_images_unavailable lists references whose figure could not be loaded; do not invent their content and rely on the remaining statement text."

// AcceptStatementImage reports whether a fetched figure satisfies the
// provider contract. Adapters use it to list a figure as unavailable instead
// of letting one undecodable image fail the whole request.
func AcceptStatementImage(img StatementImage) bool {
	hash, valid := validatedImage(img.MediaType, img.Content, maxStatementImageSize)
	return valid && (img.SHA256 == "" || img.SHA256 == hash)
}
