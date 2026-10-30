package answer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func fixturePNG(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestObjectiveSendsActualImagesWithExplicitQuestionOptionMapping(t *testing.T) {
	pngBytes := fixturePNG(t)
	digest := sha256.Sum256(pngBytes)
	var wire wireRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&wire); err != nil {
			t.Error("request JSON did not decode")
		}
		_, _ = w.Write([]byte(responseFor(`{"kind":"objective","answers":["A","AB"]}`, "stop")))
	}))
	defer server.Close()
	p, _ := NewDeepSeek(Config{APIKey: "local-test", BaseURL: server.URL})
	result, err := p.GenerateObjective(context.Background(), ObjectiveRequest{
		Problem: objectiveFixture(),
		Resources: []ImageResource{
			{QuestionPosition: 1, OptionLabel: "B", MediaType: "image/png", Content: pngBytes, SHA256: hex.EncodeToString(digest[:])},
			{QuestionPosition: 2, MediaType: "image/png", DataURL: "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngBytes)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if wire.Messages[0].Content.parts != nil || len(wire.Messages[1].Content.parts) != 3 || wire.Messages[1].Content.parts[0].Type != "text" {
		t.Fatal("expected string system prompt and one user text part followed by two images")
	}
	var payload struct {
		Questions []ObjectiveQuestion `json:"questions"`
		Resources []imageReference    `json:"resources"`
	}
	if err := json.Unmarshal([]byte(wire.Messages[1].Content.textOnly()), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Resources) != 2 || payload.Resources[0].QuestionPosition != 1 || payload.Resources[0].OptionLabel != "B" || payload.Resources[0].ImageIndex != 1 || payload.Resources[1].QuestionPosition != 2 || payload.Resources[1].OptionLabel != "" || payload.Resources[1].ImageIndex != 2 {
		t.Fatal("question/option mapping lost")
	}
	if payload.Questions[0].Category != 1 || payload.Questions[1].Category != 2 || payload.Questions[0].Options[0].Label != "A" {
		t.Fatal("question type or option order lost")
	}
	for _, part := range wire.Messages[1].Content.parts[1:] {
		if part.Type != "image_url" || part.ImageURL == nil {
			t.Fatal("image is not a multimodal image_url part")
		}
		data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(part.ImageURL.URL, "data:image/png;base64,"))
		if err != nil || !bytes.Equal(data, pngBytes) {
			t.Fatal("actual image bytes missing from request")
		}
	}
	if !result.Usage.Known || result.CostMicroUSD != 24 {
		t.Fatal("reasoning tokens must be reported but not charged twice")
	}
	if strings.Contains(fmt.Sprintf("%+v", wire), "base64,") {
		t.Fatal("request formatting leaked transport image")
	}
}

func TestObjectiveImageEstimateReservesTokensWithoutCountingBase64(t *testing.T) {
	req := ObjectiveRequest{Problem: objectiveFixture(), Resources: []ImageResource{{QuestionPosition: 1, MediaType: "image/png", Content: fixturePNG(t)}}}
	low, _ := NewDeepSeek(Config{APIKey: "test", ImageTokenReserve: 4096})
	high, _ := NewDeepSeek(Config{APIKey: "test", ImageTokenReserve: 8192})
	a, err := low.EstimateObjective(req)
	if err != nil {
		t.Fatal(err)
	}
	b, err := high.EstimateObjective(req)
	if err != nil {
		t.Fatal(err)
	}
	if b.InputTokens-a.InputTokens != 4096 || b.CostMicroUSD <= a.CostMicroUSD {
		t.Fatal("configured per-image token reservation not applied")
	}
	w, err := buildObjectiveRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	input := int64(256 + 8192)
	for _, msg := range w.Messages {
		input += int64(len(msg.Content.textOnly()) + len(msg.Role) + 64)
	}
	if b.InputTokens != input {
		t.Fatal("image base64 was included in text-token estimate")
	}
	programming, err := buildRequest(Request{Problem: testSnapshot()})
	if err != nil || programming.Messages[1].Content.parts != nil {
		t.Fatal("programming request must retain string content")
	}
}

func TestObjectiveImageValidationAndSerializationAreOpaque(t *testing.T) {
	imageBytes := fixturePNG(t)
	valid := ImageResource{QuestionPosition: 1, MediaType: "image/png", Content: imageBytes}
	for _, mutate := range []func(*ImageResource){
		func(r *ImageResource) { r.QuestionPosition = 99 },
		func(r *ImageResource) { r.OptionLabel = "Z" },
		func(r *ImageResource) { r.SHA256 = "mismatch" },
		func(r *ImageResource) { r.MediaType = "image/svg+xml" },
		func(r *ImageResource) { r.Content = []byte("not-an-image") },
		func(r *ImageResource) { r.Content = nil; r.DataURL = "https://example.invalid/image?token=private" },
	} {
		r := valid
		mutate(&r)
		_, _, err := prepareObjectiveResources(objectiveFixture(), []ImageResource{r})
		if err == nil || strings.Contains(err.Error(), "private") {
			t.Fatal("malformed image was accepted or error leaked image data")
		}
	}
	valid.DataURL = "data:image/png;base64,private-body"
	b, err := json.Marshal(valid)
	if err != nil || strings.Contains(string(b), "private-body") || strings.Contains(string(b), "Content") || strings.Contains(fmt.Sprintf("%#v", valid), "private-body") {
		t.Fatal("transport-only data leaked through serialization or formatting")
	}
}
