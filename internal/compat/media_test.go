package compat

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestLiteMediaEmbeddedDocumentsAndConditionalRequests(t *testing.T) {
	s := NewServer(Config{StaticDir: t.TempDir()})
	total := 0
	for _, name := range []string{"hero", "carousel-1", "carousel-2", "carousel-3", "announcement", "banner", "video", "cover", "logo"} {
		t.Run(name, func(t *testing.T) {
			target := liteMediaURL(name)
			w := httptest.NewRecorder()
			s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
			if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "image/svg+xml") {
				t.Fatalf("status %d, content type %q", w.Code, w.Header().Get("Content-Type"))
			}
			body := w.Body.Bytes()
			total += len(body)
			if len(body) > 2048 || len(body) < 100 || strings.Contains(string(body), "<script") || strings.Contains(string(body), "<image") || strings.Contains(string(body), "data:") {
				t.Fatalf("SVG contains an executable, embedded bitmap or exceeds its 2 KiB budget: %d bytes", len(body))
			}
			decoder := xml.NewDecoder(bytes.NewReader(body))
			for {
				_, err := decoder.Token()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatalf("invalid SVG: %v", err)
				}
			}
			if w.Header().Get("Content-Length") != strconv.Itoa(len(body)) || !strings.Contains(w.Header().Get("Cache-Control"), "immutable") {
				t.Fatal("missing deterministic length or immutable cache control")
			}
			for _, method := range []string{http.MethodGet, http.MethodHead} {
				conditional := httptest.NewRequest(method, target, nil)
				conditional.Header.Set("If-None-Match", w.Header().Get("ETag"))
				cached := httptest.NewRecorder()
				s.ServeHTTP(cached, conditional)
				if cached.Code != http.StatusNotModified || cached.Body.Len() != 0 {
					t.Fatalf("conditional %s: %d, %d bytes", method, cached.Code, cached.Body.Len())
				}
			}
			head := httptest.NewRecorder()
			s.ServeHTTP(head, httptest.NewRequest(http.MethodHead, target, nil))
			if head.Code != http.StatusOK || head.Body.Len() != 0 || head.Header().Get("Content-Length") != w.Header().Get("Content-Length") {
				t.Fatal("HEAD differs from GET metadata")
			}
		})
	}
	if total > 12<<10 {
		t.Fatalf("all 9 placeholders exceed 12 KiB total: %d", total)
	}
}

func TestLiteMediaAliasesAndPathGuards(t *testing.T) {
	for _, target := range []string{liteMediaURL("logo"), "/_assets/data.educoder.net/_lite/media-v1/logo.svg", "/_assets/ali-cdn.educoder.net//_lite/media-v1/logo.svg"} {
		if name, ok := mediaName(target); !ok || name != "logo" {
			t.Errorf("valid local prefix %q: %q, %v", target, name, ok)
		}
	}
	for _, target := range []string{"/_lite/media-v1/../logo.svg", "/_assets/data.educoder.net/../_lite/media-v1/logo.svg", "/_assets/evil.example/_lite/media-v1/logo.svg", "/_lite/media-v1/no-such.svg", "/_lite/media-v1/a\\logo.svg", "/images/avatars/User/b", "/_assets/data.educoder.net/images/avatars/User/student", "/_assets/prod-attachments.oss-accelerate.aliyuncs.com/course/diagram.png"} {
		if name, ok := mediaName(target); ok {
			t.Errorf("unexpected media substitution %q -> %q", target, name)
		}
	}
	s := NewServer(Config{StaticDir: t.TempDir()})
	for target, name := range decorativeMediaAliases {
		if strings.Contains(target, "/avatars/User/") {
			t.Fatalf("person avatar included in decorative deletion aliases: %s", target)
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target+"?t=123", nil))
		want, err := liteMediaFiles.ReadFile("media/" + name + ".svg")
		if err != nil || w.Code != http.StatusOK || !bytes.Equal(w.Body.Bytes(), want) {
			t.Errorf("removed media alias %q -> %q: %d, %v", target, name, w.Code, err)
		}
	}
	post := httptest.NewRecorder()
	s.ServeHTTP(post, httptest.NewRequest(http.MethodPost, liteMediaURL("logo"), nil))
	if post.Code != http.StatusMethodNotAllowed || post.Header().Get("Allow") != "GET, HEAD" {
		t.Fatal("embedded media accepted a write request")
	}
}

func TestHomeMediaPolicyPreservesCourseMetadataAndPeople(t *testing.T) {
	raw := []byte(`{"images_url":[{"image_url":"https://public.educoder.net/PortalImage/1","path":"https://ad.example/"}],"new_discipline":[{"name":"真实课程名","image_url":"large.png","subjects":[{"id":9007199254740993,"image_url":"cover.png","url":"/paths/1","teacher":{"image_url":"avatars/User/b"},"avatar_url":"person.png"}]}],"shixun_demo":[{"clazz":"normal","video":"https://video.educoder.net/large.mp4","url":"/api/shixuns/demo/shixun_exec.json"}],"team_members":[{"avatar_url":"real-person.png"}]}`)
	modified, err := lightweightPublicMedia("/api/home/index.json", raw)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(modified, &body); err != nil {
		t.Fatal(err)
	}
	slides := body["images_url"].([]any)
	if len(slides) != 3 {
		t.Fatalf("expected 3 lightweight carousel slides, got %d", len(slides))
	}
	for i, slide := range slides {
		item := slide.(map[string]any)
		if item["image_url"] != liteMediaURL("carousel-"+strconv.Itoa(i+1)) || item["path"] != "/" {
			t.Fatalf("slide still requests an advertisement: %#v", item)
		}
	}
	for _, preserved := range []string{`9007199254740993`, `真实课程名`, `"url":"/paths/1"`, `"image_url":"avatars/User/b"`, `"avatar_url":"person.png"`, `"avatar_url":"real-person.png"`, `"url":"/api/shixuns/demo/shixun_exec.json"`} {
		if !strings.Contains(string(modified), preserved) {
			t.Errorf("lost useful course/person metadata %s in %s", preserved, modified)
		}
	}
	if strings.Contains(string(modified), ".mp4") || !strings.Contains(string(modified), `"poster":"/_lite/media-v1/video.svg"`) || !strings.Contains(string(modified), `"image_url":"/_lite/media-v1/cover.svg"`) {
		t.Fatalf("heavy home media remains: %s", modified)
	}
}

func TestSettingsAndExistingPopupMediaAreReplaced(t *testing.T) {
	raw := []byte(`{"setting":{"nav_logo_url":"images/avatars/LaboratorySetting/1nav","course_banner_url":"large.png","competition_banner_url_b":"large.png","oj_banner_url":null,"footer":"<img src='https://public.educoder.net/logo.png'><a href='/classrooms'>课堂</a>","adv_course":{"image_url":"advertisement.png","jump_url":"https://ad.example","name":"广告"},"public_bucket_host":"https://data.educoder.net","navbar":[{"link":"/classrooms","name":"教学课堂"}]}}`)
	modified, err := lightweightPublicMedia("/api/setting.json", raw)
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Setting map[string]any `json:"setting"`
	}
	if err := json.Unmarshal(modified, &body); err != nil {
		t.Fatal(err)
	}
	if body.Setting["nav_logo_url"] != "_lite/media-v1/logo.svg" || body.Setting["course_banner_url"] != "_lite/media-v1/banner.svg" || body.Setting["oj_banner_url"] != nil {
		t.Fatalf("setting image contract changed: %#v", body.Setting)
	}
	if body.Setting["footer"] != "<img src='/_lite/media-v1/logo.svg'><a href='/classrooms'>课堂</a>" {
		t.Fatalf("footer image not replaced: %s", body.Setting["footer"])
	}
	popup, err := lightweightPublicMedia("/api/popup_windows/get_popup_windows.json", []byte(`{"status":0,"data":[{"id":42,"show_page":[0,1],"width":800,"height":440,"content":["![big](https://ad.example/large.png)"],"image_url":"old.png"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{`"id":42`, `"width":800`, `"height":440`, `"show_page":[0,1]`, liteMediaURL("announcement"), "页面在减肥"} {
		if !strings.Contains(string(popup), value) {
			t.Errorf("popup lost %s: %s", value, popup)
		}
	}
	if strings.Contains(string(popup), "https://ad.example") {
		t.Fatalf("popup still includes remote advertisement: %s", popup)
	}
	empty, err := lightweightPublicMedia("/api/popup_windows/get_popup_windows.json", []byte(`{"status":0,"message":"success"}`))
	if err != nil || strings.Contains(string(empty), "data") {
		t.Fatal("empty popup response introduced a new modal")
	}
}

func TestMediaPolicyDoesNotTouchTaskOrAvatarPayloads(t *testing.T) {
	raw := []byte(`{"description":"![diagram](https://public.educoder.net/PortalImage/1)","image_url":"https://data.educoder.net/images/avatars/User/b","video":"https://video.educoder.net/lecture.mp4","cover_url":"instructional.png"}`)
	for _, path := range []string{"/api/users/get_user_info.json", "/api/courses/classroom/homework_commons.json", "/api/homework_commons/123.json", "/api/tasks/demo.json"} {
		modified, err := lightweightPublicMedia(path, raw)
		if err != nil || !bytes.Equal(modified, raw) {
			t.Errorf("non-presentation response changed for %s: %s, %v", path, modified, err)
		}
	}
}
