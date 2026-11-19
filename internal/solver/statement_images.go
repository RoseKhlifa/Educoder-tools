package solver

import (
	"context"
	"html"
	"regexp"
	"strings"
	"time"

	"educoder-cli/internal/answer"
	"educoder-cli/internal/problem"
)

// statementImageReader is implemented by adapters that can read statement
// figures with the authenticated session. Failures are reported as
// unavailable references, never as a run error: a missing figure degrades the
// prompt to the existing text-only behaviour instead of stopping the level.
type statementImageReader interface {
	StatementImages(context.Context, Target, problem.Snapshot) ([]answer.StatementImage, []string)
}

func loadStatementImages(ctx context.Context, adapter Adapter, target Target, snap problem.Snapshot) ([]answer.StatementImage, []string) {
	reader, ok := adapter.(statementImageReader)
	if !ok || snap.EditMode == "graphic" {
		return nil, nil
	}
	return reader.StatementImages(ctx, target, snap)
}

var (
	statementHTMLImage     = regexp.MustCompile(`(?is)<img\b[^>]*>`)
	statementHTMLSource    = regexp.MustCompile("(?is)(?:^|\\s)src\\s*=\\s*(?:\"([^\"]*)\"|'([^']*)'|([^\\s'\"<>`]+))")
	statementMarkdownImage = regexp.MustCompile(`!\[[^\]]*\]\(\s*(?:<([^>]+)>|([^\s)]+))(?:\s+["'][^"']*["'])?\s*\)`)
)

// statementImageLocators returns each distinct image reference exactly as it
// appears in the statement, in order of first appearance.
func statementImageLocators(statement string) []string {
	type hit struct {
		at      int
		locator string
	}
	var hits []hit
	for _, span := range statementHTMLImage.FindAllStringIndex(statement, -1) {
		tag := statement[span[0]:span[1]]
		if m := statementHTMLSource.FindStringSubmatch(tag); m != nil {
			for _, value := range m[1:] {
				if value != "" {
					hits = append(hits, hit{span[0], value})
					break
				}
			}
		}
	}
	for _, m := range statementMarkdownImage.FindAllStringSubmatchIndex(statement, -1) {
		for group := 1; group <= 2; group++ {
			if start, end := m[2*group], m[2*group+1]; start >= 0 {
				hits = append(hits, hit{m[0], statement[start:end]})
				break
			}
		}
	}
	// Restore statement order across both syntaxes.
	for i := 1; i < len(hits); i++ {
		for j := i; j > 0 && hits[j].at < hits[j-1].at; j-- {
			hits[j], hits[j-1] = hits[j-1], hits[j]
		}
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		locator := strings.TrimSpace(h.locator)
		if locator == "" || seen[locator] || strings.ContainsAny(locator, "\r\n\t") {
			continue
		}
		seen[locator] = true
		out = append(out, locator)
	}
	return out
}

// StatementImages reads the statement's figures through the same guarded
// attachment reader used for objective images (same-origin attachment path,
// verified redirects, size and type limits). Signed redirect URLs never leave
// the protocol client; the prompt only carries the statement's own reference.
func (a *EducoderAdapter) StatementImages(ctx context.Context, _ Target, snap problem.Snapshot) ([]answer.StatementImage, []string) {
	locators := statementImageLocators(snap.StatementMarkdown)
	if len(locators) == 0 || a == nil || a.Client == nil {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	var images []answer.StatementImage
	var unavailable []string
	total := 0
	for _, locator := range locators {
		if problem.ValidatePublicText(locator) != nil {
			// A signed or credential-bearing reference is never echoed into a prompt.
			continue
		}
		if len(images) >= answer.MaxStatementImages || ctx.Err() != nil {
			unavailable = append(unavailable, locator)
			continue
		}
		image, err := a.Client.FetchQuestionImage(ctx, html.UnescapeString(locator))
		figure := answer.StatementImage{Locator: locator, MediaType: image.MediaType, SHA256: image.SHA256, Content: image.Content}
		if err != nil || total+len(image.Content) > answer.MaxStatementImageBytes || !answer.AcceptStatementImage(figure) {
			unavailable = append(unavailable, locator)
			continue
		}
		total += len(image.Content)
		images = append(images, figure)
	}
	return images, unavailable
}
