package educoder

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
)

type questionImageRoundTripper func(*http.Request) (*http.Response, error)

func (f questionImageRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func questionImagePNG(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func questionImageResponse(status int, body []byte, location string) *http.Response {
	h := make(http.Header)
	if location != "" {
		h.Set("Location", location)
	}
	return &http.Response{StatusCode: status, Header: h, Body: io.NopCloser(bytes.NewReader(body))}
}

func TestQuestionImageAuthenticatedThenCredentialFree(t *testing.T) {
	content := questionImagePNG(t)
	base, _ := url.Parse(DefaultBaseURL)
	jar, _ := cookiejar.New(nil)
	jar.SetCookies(base, []*http.Cookie{{Name: "_educoder_session", Value: "fixture-session"}})
	initial, public := 0, 0
	c := New("")
	c.HTTP.Jar = jar
	c.HTTP.Transport = questionImageRoundTripper(func(r *http.Request) (*http.Response, error) {
		initial++
		if r.Method != http.MethodGet || r.URL.Path != "/api/attachments/123" || r.Header.Get("Pc-Authorization") != "fixture-session" || r.Header.Get("X-EDU-Signature") == "" || r.Header.Get("Cookie") == "" {
			t.Fatal("attachment request missed its authenticated protocol headers")
		}
		return questionImageResponse(302, nil, "https://public.educoder.net/images/question.png?signature=private-value"), nil
	})
	transport := questionImageRoundTripper(func(r *http.Request) (*http.Response, error) {
		public++
		for name := range r.Header {
			if strings.EqualFold(name, "Cookie") || strings.EqualFold(name, "Authorization") || strings.EqualFold(name, "Pc-Authorization") || strings.HasPrefix(strings.ToLower(name), "x-edu-") || name == "Referer" || name == "Origin" {
				t.Fatal("credential header reached image storage")
			}
		}
		return questionImageResponse(200, content, ""), nil
	})
	got, err := c.fetchQuestionImage(context.Background(), "/api/attachments/123?type=image", transport)
	if err != nil || initial != 1 || public != 1 || got.MediaType != "image/png" || !bytes.Equal(got.Content, content) || len(got.SHA256) != 64 {
		t.Fatalf("unexpected image result: initial=%d public=%d err=%v", initial, public, err)
	}
}

func TestQuestionImageRejectsUntrustedInitialLocators(t *testing.T) {
	c := New("")
	c.HTTP.Transport = questionImageRoundTripper(func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid locator reached the transport")
		return nil, nil
	})
	for _, locator := range []string{
		"https://evil.example/api/attachments/123", "//data.educoder.net/api/attachments/123", "/api/users/get_user_info.json", "/api/attachments/../users/123", "/api/attachments/%2e%2e/users", "/api/attachments/123%2f456", "/api/attachments/%252e%252e", "data:image/png;base64,aGVsbG8=", "https://fixture-user@data.educoder.net/api/attachments/123", "/api/attachments/123#fragment",
	} {
		if _, err := c.FetchQuestionImage(context.Background(), locator); err == nil {
			t.Fatal("invalid initial locator accepted")
		}
	}
}

func TestQuestionImageRejectsUntrustedRedirectsAndLoops(t *testing.T) {
	for _, destination := range []string{"http://public.educoder.net/a", "https://evil.example/a", "https://public.educoder.net.evil.example/a", "https://127.0.0.1/a", "https://169.254.169.254/a", "https://fixture-user@public.educoder.net/a", "https://public.educoder.net:443/a", "https://public.educoder.net/a/../b", "https://public.educoder.net/a#private"} {
		c := New("")
		c.HTTP.Transport = questionImageRoundTripper(func(*http.Request) (*http.Response, error) {
			return questionImageResponse(302, nil, destination), nil
		})
		public := questionImageRoundTripper(func(*http.Request) (*http.Response, error) {
			t.Fatal("untrusted redirect reached public transport")
			return nil, nil
		})
		_, err := c.fetchQuestionImage(context.Background(), "/api/attachments/123", public)
		var typed *QuestionImageError
		if !errors.As(err, &typed) || typed.Kind != "objective_image_redirect" || strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "https:") {
			t.Fatal("untrusted redirect lacked redacted typed error")
		}
	}
	c := New("")
	c.HTTP.Transport = questionImageRoundTripper(func(*http.Request) (*http.Response, error) {
		return questionImageResponse(302, nil, "https://public.educoder.net/loop"), nil
	})
	calls := 0
	_, err := c.fetchQuestionImage(context.Background(), "/api/attachments/123", questionImageRoundTripper(func(*http.Request) (*http.Response, error) {
		calls++
		return questionImageResponse(302, nil, "https://public.educoder.net/loop"), nil
	}))
	if err == nil || calls != 1 {
		t.Fatal("redirect loop was not bounded")
	}
}

func TestQuestionImageLocalInitialAPIAndLimits(t *testing.T) {
	content := questionImagePNG(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/attachments/valid":
			w.Write(content)
		case "/api/attachments/large":
			w.Write(bytes.Repeat([]byte{1}, MaxQuestionImageBytes+1))
		case "/api/attachments/not-image":
			w.Write([]byte("<html>login form</html>"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	c := New(server.URL)
	if _, err := c.FetchQuestionImage(context.Background(), "/api/attachments/valid"); err != nil {
		t.Fatal(err)
	}
	for _, locator := range []string{"large", "not-image", "missing"} {
		if _, err := c.FetchQuestionImage(context.Background(), "/api/attachments/"+locator); err == nil {
			t.Fatalf("invalid response %s accepted", locator)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.FetchQuestionImage(ctx, "/api/attachments/valid"); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation not preserved")
	}
}

func TestQuestionImagePublicAddressGuard(t *testing.T) {
	for _, address := range []string{"127.0.0.1", "10.0.0.1", "172.16.0.1", "192.168.1.1", "169.254.169.254", "100.100.100.200", "::1", "::ffff:127.0.0.1", "fe80::1", "fd00::1", "0.0.0.0", "192.0.2.1", "2001:db8::1", "224.0.0.1", "255.255.255.255"} {
		if publicQuestionImageIP(netip.MustParseAddr(address)) {
			t.Fatalf("special-use address accepted: %s", address)
		}
	}
	if !publicQuestionImageIP(netip.MustParseAddr("1.1.1.1")) || !publicQuestionImageIP(netip.MustParseAddr("2606:4700:4700::1111")) {
		t.Fatal("public address unexpectedly rejected")
	}
}
