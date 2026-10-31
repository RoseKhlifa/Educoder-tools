package taskservice

import (
	"context"
	"html"
	"regexp"
	"strings"
	"time"

	"educoder-cli/internal/answer"
	"educoder-cli/internal/educoder"
)

var objectiveHTMLImage = regexp.MustCompile(`(?is)<img\b[^>]*>`)
var objectiveHTMLSource = regexp.MustCompile("(?is)(?:^|\\s)src\\s*=\\s*(?:\"([^\"]*)\"|'([^']*)'|([^\\s'\"<>`]+))")
var objectiveMarkdownImage = regexp.MustCompile(`!\[[^\]]*\]\(\s*(?:<([^>]+)>|([^\s)]+))(?:\s+["'][^"']*["'])?\s*\)`)
var objectiveUnsupportedMedia = regexp.MustCompile(`(?is)<\s*(?:svg|video|audio|iframe|object|embed|picture|source|canvas)\b|\bsrcset\s*=|\b(?:background(?:-image)?\s*:|style\s*=)[^>]*\burl\s*\(`)
var objectiveRemainingImage = regexp.MustCompile(`(?is)<\s*img\b|!\[`)
var objectiveImageHashPlaceholder = regexp.MustCompile(`<img src="sha256:[0-9a-f]{64}">`)

// prepareObjectiveResources turns each observed image into an in-memory model
// attachment and a stable content-hash placeholder. The returned snapshot is
// safe to persist: it never contains the fetched image's signed URL. Unsupported
// image syntaxes fail closed rather than letting a model guess unseen diagrams.
func prepareObjectiveResources(ctx context.Context, client *educoder.Client, choice *educoder.ChoiceTask) (answer.ObjectiveSnapshot, []answer.ImageResource, error) {
	if choice == nil || choice.Task == nil {
		return answer.ObjectiveSnapshot{}, nil, &educoder.QuestionImageError{Kind: "objective_image_invalid"}
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	snapshot := objectiveSnapshot(choice)
	resources := []answer.ImageResource{}
	cache := map[string]educoder.QuestionImage{}
	locators := map[string]string{}
	total := 0
	var resourceErr error
	appendImage := func(locator string, position int, label string) string {
		if resourceErr != nil {
			return ""
		}
		rawLocator := locator
		locator = html.UnescapeString(strings.TrimSpace(locator))
		if len(resources) >= 128 || locator == "" || strings.ContainsAny(locator, "\r\n\t") {
			resourceErr = &educoder.QuestionImageError{Kind: "objective_image_limit"}
			return ""
		}
		image, found := cache[locator]
		if !found {
			image, resourceErr = client.FetchQuestionImage(ctx, locator)
			if resourceErr != nil {
				return ""
			}
			cache[locator] = image
		}
		total += len(image.Content)
		if total > 40<<20 {
			resourceErr = &educoder.QuestionImageError{Kind: "objective_image_limit"}
			return ""
		}
		resources = append(resources, answer.ImageResource{QuestionPosition: position, OptionLabel: label, MediaType: image.MediaType, Content: image.Content, SHA256: image.SHA256})
		placeholder := `<img src="sha256:` + image.SHA256 + `">`
		locators[rawLocator], locators[locator] = placeholder, placeholder
		return placeholder
	}
	prepareText := func(text string, position int, label string) string {
		if objectiveUnsupportedMedia.MatchString(text) {
			resourceErr = &educoder.QuestionImageError{Kind: "objective_image_unsupported"}
			return ""
		}
		// Remove each complete tag, including original src/query and alt text.
		text = objectiveHTMLImage.ReplaceAllStringFunc(text, func(tag string) string {
			m := objectiveHTMLSource.FindStringSubmatch(tag)
			if m == nil || len(objectiveHTMLSource.FindAllStringSubmatch(tag, -1)) != 1 {
				resourceErr = &educoder.QuestionImageError{Kind: "objective_image_unsupported"}
				return ""
			}
			for _, locator := range m[1:] {
				if locator != "" {
					return appendImage(locator, position, label)
				}
			}
			resourceErr = &educoder.QuestionImageError{Kind: "objective_image_unsupported"}
			return ""
		})
		text = objectiveMarkdownImage.ReplaceAllStringFunc(text, func(markup string) string {
			m := objectiveMarkdownImage.FindStringSubmatch(markup)
			locator := m[1]
			if locator == "" {
				locator = m[2]
			}
			return appendImage(locator, position, label)
		})
		if objectiveRemainingImage.MatchString(objectiveImageHashPlaceholder.ReplaceAllString(text, "")) {
			resourceErr = &educoder.QuestionImageError{Kind: "objective_image_unsupported"}
		}
		return text
	}
	// A shared statement image is attached to every question it contextualizes,
	// not silently attributed to just the first question. Normal question and
	// option images bind only to their own exact position and option label.
	if len(snapshot.Questions) == 0 {
		return answer.ObjectiveSnapshot{}, nil, &educoder.QuestionImageError{Kind: "objective_image_invalid"}
	}
	title, statement := snapshot.Title, snapshot.Statement
	for i := range snapshot.Questions {
		q := &snapshot.Questions[i]
		preparedTitle := prepareText(title, q.Position, "")
		preparedStatement := prepareText(statement, q.Position, "")
		if i == 0 {
			snapshot.Title, snapshot.Statement = preparedTitle, preparedStatement
		}
		q.Question = prepareText(q.Question, q.Position, "")
		for j := range q.Options {
			q.Options[j].Text = prepareText(q.Options[j].Text, q.Position, q.Options[j].Label)
		}
	}
	if resourceErr != nil {
		return answer.ObjectiveSnapshot{}, nil, resourceErr
	}
	// A locator can appear again in a surrounding link or a caption. Remove
	// those exact occurrences too; signed queries never enter snapshot storage.
	redactLocators := func(text string) string {
		for locator, placeholder := range locators {
			text = strings.ReplaceAll(text, locator, placeholder)
			text = strings.ReplaceAll(text, html.EscapeString(locator), placeholder)
		}
		return text
	}
	snapshot.Title, snapshot.Statement = redactLocators(snapshot.Title), redactLocators(snapshot.Statement)
	for i := range snapshot.Questions {
		q := &snapshot.Questions[i]
		q.Question = redactLocators(q.Question)
		for j := range q.Options {
			q.Options[j].Text = redactLocators(q.Options[j].Text)
		}
	}
	// Use the exact model contract validator (including MIME, dimensions, hash
	// and aggregate-byte checks) without performing any provider network call.
	if err := answer.ValidateObjectiveResources(snapshot, resources); err != nil {
		return answer.ObjectiveSnapshot{}, nil, &educoder.QuestionImageError{Kind: "objective_image_invalid"}
	}
	return snapshot, resources, nil
}
