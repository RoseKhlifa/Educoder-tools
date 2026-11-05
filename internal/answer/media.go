package answer

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"strings"
)

// ImageBinding identifies the question stem or option where an image appears.
// One image can be shared by a statement and several questions, so a resource
// may carry more than one binding while the provider still receives one image.
type ImageBinding struct {
	QuestionPosition int    `json:"question_position"`
	OptionLabel      string `json:"option_label,omitempty"`
}

// ImageResource binds actual public image bytes to one or more question or
// option positions. DataURL and Content are transport-only and deliberately
// excluded from JSON. A caller persisting a snapshot should persist the
// SHA256 reference instead. Bindings supersedes the legacy singular fields
// when present; the singular fields remain for compatibility with callers that
// provide one binding per resource.
type ImageResource struct {
	QuestionPosition int            `json:"question_position"`
	OptionLabel      string         `json:"option_label,omitempty"`
	MediaType        string         `json:"media_type"`
	DataURL          string         `json:"-"`
	Content          []byte         `json:"-"`
	SHA256           string         `json:"sha256,omitempty"`
	Bindings         []ImageBinding `json:"-"`
}

// EffectiveBindings returns the explicit shared-image bindings, or the legacy
// singular binding when a caller supplied the original resource shape.
func (r ImageResource) EffectiveBindings() []ImageBinding {
	if len(r.Bindings) > 0 {
		return r.Bindings
	}
	if r.QuestionPosition <= 0 {
		return nil
	}
	return []ImageBinding{{QuestionPosition: r.QuestionPosition, OptionLabel: r.OptionLabel}}
}

func (ImageResource) String() string   { return "objective image resource (content redacted)" }
func (ImageResource) GoString() string { return "objective image resource (content redacted)" }

type imageReference struct {
	ImageIndex       int    `json:"image_index"`
	QuestionPosition int    `json:"question_position"`
	OptionLabel      string `json:"option_label,omitempty"`
	MediaType        string `json:"media_type"`
	SHA256           string `json:"sha256"`
}

// ValidateObjectiveResources checks the same image contract used to construct
// provider requests without constructing a client or sending a network call.
func ValidateObjectiveResources(snapshot ObjectiveSnapshot, resources []ImageResource) error {
	if err := ValidateObjectiveSnapshot(snapshot); err != nil {
		return err
	}
	_, _, err := prepareObjectiveResources(snapshot, resources)
	return err
}

type imageURLPart struct {
	URL string `json:"url"`
}

type contentPart struct {
	Type     string        `json:"type"`
	Text     string        `json:"text,omitempty"`
	ImageURL *imageURLPart `json:"image_url,omitempty"`
}

// messageContent keeps binary image bodies out of text token estimates and
// formatting, while encoding exactly the string-or-array API union.
type messageContent struct {
	text  string
	parts []contentPart
}

func textContent(s string) messageContent { return messageContent{text: s} }

func (m messageContent) textOnly() string {
	if m.parts == nil {
		return m.text
	}
	var b strings.Builder
	for _, p := range m.parts {
		if p.Type == "text" {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

func (m messageContent) imageCount() int {
	n := 0
	for _, p := range m.parts {
		if p.Type == "image_url" {
			n++
		}
	}
	return n
}

func (m messageContent) MarshalJSON() ([]byte, error) {
	if m.parts != nil {
		return json.Marshal(m.parts)
	}
	return json.Marshal(m.text)
}

func (m *messageContent) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '[' {
		m.text = ""
		return json.Unmarshal(b, &m.parts)
	}
	m.parts = nil
	return json.Unmarshal(b, &m.text)
}

func (m messageContent) String() string   { return m.textOnly() }
func (m messageContent) GoString() string { return m.textOnly() }

func prepareObjectiveResources(snapshot ObjectiveSnapshot, resources []ImageResource) ([]string, []imageReference, error) {
	const maxImageBytes = 10 << 20
	const maxTotalBytes = 40 << 20
	questions := make(map[int]ObjectiveQuestion, len(snapshot.Questions))
	for _, q := range snapshot.Questions {
		questions[q.Position] = q
	}
	images := make([]string, 0, len(resources))
	refs := make([]imageReference, 0, len(resources))
	imageIndexes := make(map[string]int, len(resources))
	total := 0
	for _, resource := range resources {
		bindings := resource.EffectiveBindings()
		if len(bindings) == 0 {
			return nil, nil, &Error{Kind: "invalid_objective_resource"}
		}
		for _, binding := range bindings {
			q, ok := questions[binding.QuestionPosition]
			if !ok {
				return nil, nil, &Error{Kind: "invalid_objective_resource"}
			}
			if binding.OptionLabel != "" {
				found := false
				for _, option := range q.Options {
					if option.Label == binding.OptionLabel {
						found = true
						break
					}
				}
				if !found {
					return nil, nil, &Error{Kind: "invalid_objective_resource"}
				}
			}
		}
		mediaType := resource.MediaType
		switch mediaType {
		case "image/jpeg", "image/png", "image/gif", "image/webp":
		default:
			return nil, nil, &Error{Kind: "invalid_objective_resource"}
		}
		content := resource.Content
		if resource.DataURL != "" {
			prefix := "data:" + mediaType + ";base64,"
			if len(content) != 0 || !strings.HasPrefix(resource.DataURL, prefix) || len(resource.DataURL) > maxImageBytes*4/3+len(prefix)+8 {
				return nil, nil, &Error{Kind: "invalid_objective_resource"}
			}
			var err error
			content, err = base64.StdEncoding.Strict().DecodeString(strings.TrimPrefix(resource.DataURL, prefix))
			if err != nil {
				return nil, nil, &Error{Kind: "invalid_objective_resource"}
			}
		}
		if len(content) == 0 || len(content) > maxImageBytes || http.DetectContentType(content) != mediaType {
			return nil, nil, &Error{Kind: "invalid_objective_resource"}
		}
		// WebP is identified by its RIFF/WEBP signature by DetectContentType.
		// The standard library decoders additionally validate PNG/JPEG/GIF
		// dimensions without allocating the decompressed image.
		if mediaType != "image/webp" {
			cfg, _, err := image.DecodeConfig(bytes.NewReader(content))
			if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > 100000000 {
				return nil, nil, &Error{Kind: "invalid_objective_resource"}
			}
		}
		digest := sha256.Sum256(content)
		hash := hex.EncodeToString(digest[:])
		if resource.SHA256 != "" && resource.SHA256 != hash {
			return nil, nil, &Error{Kind: "invalid_objective_resource"}
		}
		imageIndex, found := imageIndexes[hash]
		if !found {
			if len(images) >= 128 || total+len(content) > maxTotalBytes {
				return nil, nil, &Error{Kind: "invalid_objective_resource"}
			}
			total += len(content)
			images = append(images, "data:"+mediaType+";base64,"+base64.StdEncoding.EncodeToString(content))
			imageIndex = len(images)
			imageIndexes[hash] = imageIndex
		}
		for _, binding := range bindings {
			refs = append(refs, imageReference{ImageIndex: imageIndex, QuestionPosition: binding.QuestionPosition, OptionLabel: binding.OptionLabel, MediaType: mediaType, SHA256: hash})
		}
	}
	return images, refs, nil
}
