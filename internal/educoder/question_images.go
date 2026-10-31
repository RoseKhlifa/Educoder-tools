package educoder

// Question images are fetched only from the authenticated attachment endpoint.
// Binary bodies remain in memory; URLs, signatures and session data are never
// included in errors or the returned value.
import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
)

const MaxQuestionImageBytes = 10 << 20

type QuestionImage struct {
	MediaType string `json:"media_type"`
	Content   []byte `json:"-"`
	SHA256    string `json:"sha256"`
}

func (QuestionImage) String() string   { return "question image (content redacted)" }
func (QuestionImage) GoString() string { return "question image (content redacted)" }

type QuestionImageError struct{ Kind string }

func (e *QuestionImageError) Error() string { return e.Kind }

func imageError(kind string) error { return &QuestionImageError{Kind: kind} }

// FetchQuestionImage performs one authenticated attachment request followed by
// at most three credential-free, allowlisted HTTPS redirects. No arbitrary URL
// supplied in a question can reach an authenticated request or the public dialer.
func (c *Client) FetchQuestionImage(ctx context.Context, locator string) (QuestionImage, error) {
	return c.fetchQuestionImage(ctx, locator, questionImageTransport)
}

func (c *Client) fetchQuestionImage(ctx context.Context, locator string, publicTransport http.RoundTripper) (QuestionImage, error) {
	var result QuestionImage
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if c == nil || c.HTTP == nil {
		return result, imageError("objective_image_unavailable")
	}
	base, err := url.Parse(c.BaseURL)
	if err != nil || base.Host == "" || (base.Scheme != "https" && base.Scheme != "http") || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return result, imageError("objective_image_invalid")
	}
	if len(locator) > 8192 {
		return result, imageError("objective_image_limit")
	}
	u, err := url.Parse(strings.TrimSpace(locator))
	if err != nil || u.User != nil || u.Fragment != "" || u.Opaque != "" || u.Host != "" && u.Scheme == "" {
		return result, imageError("objective_image_invalid")
	}
	u = base.ResolveReference(u)
	if u.Scheme != base.Scheme || u.Host != base.Host || !questionAttachmentPath(u) {
		return result, imageError("objective_image_unsupported")
	}
	initialURL := u.String()
	seen := map[string]bool{}
	for hop := 0; ; hop++ {
		if seen[u.String()] || hop > 3 {
			return result, imageError("objective_image_redirect")
		}
		seen[u.String()] = true
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return result, imageError("objective_image_invalid")
		}
		req.Header.Set("Accept", "image/png,image/jpeg,image/gif,image/webp")
		// Each hop gets a new request and a client with no cookie jar. Only the
		// initial API request receives explicitly selected account credentials.
		client := &http.Client{Transport: publicTransport, CheckRedirect: noImageRedirect, Timeout: 30 * time.Second}
		if hop == 0 {
			client.Transport = c.HTTP.Transport
			stamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
			req.Header.Set("Origin", c.Origin)
			req.Header.Set("Referer", c.Origin+"/")
			req.Header.Set("X-EDU-Type", "pc")
			req.Header.Set("X-EDU-Timestamp", stamp)
			req.Header.Set("X-EDU-Signature", Signature(http.MethodGet, stamp))
			if c.HTTP.Jar != nil {
				for _, cookie := range c.HTTP.Jar.Cookies(u) {
					req.AddCookie(cookie)
					if cookie.Name == "_educoder_session" {
						req.Header.Set("Pc-Authorization", cookie.Value)
					}
				}
			}
		}
		resp, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return result, ctx.Err()
			}
			return result, imageError("objective_image_unavailable")
		}
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			next, locationErr := resp.Location()
			resp.Body.Close()
			if locationErr != nil || next.String() == initialURL || !trustedQuestionImageURL(next) {
				return result, imageError("objective_image_redirect")
			}
			u = next
			continue
		}
		if resp.StatusCode != http.StatusOK || resp.ContentLength > MaxQuestionImageBytes {
			resp.Body.Close()
			return result, imageError("objective_image_unavailable")
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, MaxQuestionImageBytes+1))
		resp.Body.Close()
		if readErr != nil {
			if ctx.Err() != nil {
				return result, ctx.Err()
			}
			return result, imageError("objective_image_unavailable")
		}
		if len(body) == 0 || len(body) > MaxQuestionImageBytes {
			return result, imageError("objective_image_limit")
		}
		mediaType := http.DetectContentType(body)
		switch mediaType {
		case "image/png", "image/jpeg", "image/gif", "image/webp":
		default:
			return result, imageError("objective_image_unsupported")
		}
		hash := sha256.Sum256(body)
		return QuestionImage{MediaType: mediaType, Content: body, SHA256: hex.EncodeToString(hash[:])}, nil
	}
}

func noImageRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

func questionAttachmentPath(u *url.URL) bool {
	// Reject encoded separators and traversal rather than normalizing a path
	// into a different API endpoint that would receive account credentials.
	escaped := strings.ToLower(u.EscapedPath())
	if strings.Contains(escaped, "%2f") || strings.Contains(escaped, "%5c") || strings.Contains(escaped, "%25") || strings.Contains(u.Path, "\\") || path.Clean(u.Path) != u.Path {
		return false
	}
	return strings.HasPrefix(u.Path, "/api/attachments/") && len(u.Path) > len("/api/attachments/")
}

func trustedQuestionImageURL(u *url.URL) bool {
	if u == nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" || u.Opaque != "" || u.Port() != "" || u.Hostname() != u.Host || strings.Contains(u.Path, "\\") || path.Clean(u.Path) != u.Path || len(u.String()) > 8192 {
		return false
	}
	escaped := strings.ToLower(u.EscapedPath())
	if strings.Contains(escaped, "%2f") || strings.Contains(escaped, "%5c") || strings.Contains(escaped, "%25") {
		return false
	}
	switch u.Host {
	case "data.educoder.net", "ali-cdn.educoder.net", "public.educoder.net", "www.educoder.net", "prod-attachments.oss-accelerate.aliyuncs.com":
		return true
	}
	return false
}

var questionImageTransport = func() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = nil
	t.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil || port != "443" {
			return nil, imageError("objective_image_redirect")
		}
		ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil || len(ips) == 0 {
			return nil, imageError("objective_image_unavailable")
		}
		for _, ip := range ips {
			if !publicQuestionImageIP(ip) {
				return nil, imageError("objective_image_redirect")
			}
		}
		dialer := net.Dialer{Timeout: 10 * time.Second}
		// Pin the validated address in this connection, preventing a second DNS
		// resolution from redirecting the request to metadata/private networks.
		return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
	}
	t.TLSHandshakeTimeout = 10 * time.Second
	t.ResponseHeaderTimeout = 15 * time.Second
	return t
}()

func publicQuestionImageIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return false
	}
	for _, prefix := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "64:ff9b::/96", "64:ff9b:1::/48", "2001::/32", "2001:db8::/32", "2002::/16"} {
		if netip.MustParsePrefix(prefix).Contains(ip) {
			return false
		}
	}
	return true
}
