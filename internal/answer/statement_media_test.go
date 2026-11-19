package answer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func figureSnapshot() Request {
	snap := testSnapshot()
	snap.StatementMarkdown = "按图输出：![示意](/api/attachments/7) 以及 <img src=\"/api/attachments/8\"> 和 ![缺图](/api/attachments/9)"
	return Request{Problem: snap, Profile: DefaultProfile()}
}

func TestProgrammingRequestAttachesStatementFigures(t *testing.T) {
	pngBytes := fixturePNG(t)
	digest := sha256.Sum256(pngBytes)
	req := figureSnapshot()
	// Two references to the same bytes become one provider attachment.
	req.Images = []StatementImage{
		{Locator: "/api/attachments/7", MediaType: "image/png", SHA256: hex.EncodeToString(digest[:]), Content: pngBytes},
		{Locator: "/api/attachments/8", MediaType: "image/png", Content: pngBytes},
	}
	req.ImagesUnavailable = []string{"/api/attachments/9"}
	wire, err := buildRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	user := wire.Messages[1].Content
	if user.imageCount() != 1 || len(user.parts) != 2 || !strings.HasPrefix(user.parts[1].ImageURL.URL, "data:image/png;base64,") {
		t.Fatalf("figures not attached: images=%d parts=%d", user.imageCount(), len(user.parts))
	}
	var payload struct {
		Images      []statementImageReference `json:"statement_images"`
		Unavailable []string                  `json:"statement_images_unavailable"`
	}
	if err := json.Unmarshal([]byte(user.textOnly()), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Images) != 2 || payload.Images[0].ImageIndex != 1 || payload.Images[1].ImageIndex != 1 || payload.Images[1].Locator != "/api/attachments/8" || payload.Images[0].SHA256 != hex.EncodeToString(digest[:]) {
		t.Fatalf("figure mapping=%+v", payload.Images)
	}
	if len(payload.Unavailable) != 1 || payload.Unavailable[0] != "/api/attachments/9" {
		t.Fatalf("unavailable figures=%v", payload.Unavailable)
	}
	if system := wire.Messages[0].Content.textOnly(); !strings.Contains(system, "statement_images maps each image_index") || !strings.Contains(system, "do not invent their content") {
		t.Fatal("system prompt does not explain attached figures")
	}
	// Generation keys and checkpoints never carry image bytes.
	raw, _ := json.Marshal(req)
	if strings.Contains(string(raw), hex.EncodeToString(pngBytes[:8])) || strings.Contains(string(raw), "base64") {
		t.Fatal("request JSON contains image content")
	}
}

func TestStatementFiguresMustBelongToTheStatementAndDecode(t *testing.T) {
	pngBytes := fixturePNG(t)
	cases := map[string]StatementImage{
		"foreign reference": {Locator: "/api/attachments/404", MediaType: "image/png", Content: pngBytes},
		"digest mismatch":   {Locator: "/api/attachments/7", MediaType: "image/png", SHA256: strings.Repeat("0", 64), Content: pngBytes},
		"wrong media type":  {Locator: "/api/attachments/7", MediaType: "image/jpeg", Content: pngBytes},
		"not an image":      {Locator: "/api/attachments/7", MediaType: "image/png", Content: []byte("plain text")},
	}
	for name, img := range cases {
		req := figureSnapshot()
		req.Images = []StatementImage{img}
		_, err := buildRequest(req)
		var providerErr *Error
		if !errors.As(err, &providerErr) || providerErr.Kind != "invalid_statement_image" {
			t.Fatalf("%s accepted: %v", name, err)
		}
		if name != "foreign reference" && AcceptStatementImage(img) {
			t.Fatalf("%s passed adapter pre-validation", name)
		}
	}
	if !AcceptStatementImage(StatementImage{Locator: "/api/attachments/7", MediaType: "image/png", Content: pngBytes}) {
		t.Fatal("valid figure rejected by adapter pre-validation")
	}
}

func TestStatementFiguresAreReservedInTheEstimate(t *testing.T) {
	provider, err := NewDeepSeek(Config{APIKey: "test"})
	if err != nil {
		t.Fatal(err)
	}
	plain := figureSnapshot()
	withFigure := figureSnapshot()
	withFigure.Images = []StatementImage{{Locator: "/api/attachments/7", MediaType: "image/png", Content: fixturePNG(t)}}
	a, errA := provider.Estimate(plain)
	b, errB := provider.Estimate(withFigure)
	if errA != nil || errB != nil || b.InputTokens-a.InputTokens < 8192 || b.CostMicroUSD <= a.CostMicroUSD {
		t.Fatalf("figure reservation missing: plain=%+v figure=%+v errs=%v/%v", a, b, errA, errB)
	}
}
