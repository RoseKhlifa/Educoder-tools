package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"educoder-cli/internal/platform"
)

func ticketHTTPDetail(t *testing.T, r jsonResult) platform.TicketDetail {
	t.Helper()
	var out struct {
		Data platform.TicketDetail `json:"data"`
	}
	if err := json.Unmarshal([]byte(r.Body), &out); err != nil {
		t.Fatal(err)
	}
	return out.Data
}

func TestTicketHTTPAuthOwnershipContractAndPrivacy(t *testing.T) {
	f := newPlatformHTTPFixture(t, nil)
	for _, p := range []string{"/api/tickets", "/api/tickets/00000000000000000000000000000000", "/api/admin/tickets", "/api/admin/tickets/00000000000000000000000000000000"} {
		got := f.request(t, f.server.Client(), "GET", p, "")
		if got.StatusCode != 401 {
			t.Fatalf("anonymous %s %d", p, got.StatusCode)
		}
	}
	if got := f.request(t, f.aliceClient, "GET", "/api/admin/tickets", ""); got.StatusCode != 403 {
		t.Fatal(got.Body)
	}
	body := `{"subject":"任务显示问题","category":"task","body":"<script>plain text</script> PRIVATE_TICKET_BODY","idempotency_key":"create-http-test"}`
	created := f.request(t, f.aliceClient, "POST", "/api/tickets", body)
	if created.StatusCode != 201 {
		t.Fatalf("create %d %s", created.StatusCode, created.Body)
	}
	d := ticketHTTPDetail(t, created)
	id := d.Ticket.ID
	if id == "" || len(d.Messages) != 1 || d.Ticket.User != nil || d.Messages[0].Body != "<script>plain text</script> PRIVATE_TICKET_BODY" {
		t.Fatalf("detail %+v", d)
	}
	if got := f.request(t, f.aliceClient, "POST", "/api/tickets", body); got.StatusCode != 200 || ticketHTTPDetail(t, got).Ticket.ID != id {
		t.Fatalf("idempotency %d %s", got.StatusCode, got.Body)
	}
	if got := f.request(t, f.aliceClient, "POST", "/api/tickets", strings.ReplaceAll(body, "任务显示问题", "different")); got.StatusCode != 409 {
		t.Fatal(got.Body)
	}
	for _, browser := range []*http.Client{f.bobClient, f.adminClient} {
		for _, route := range []struct{ method, path, body string }{{"GET", "/api/tickets/" + id, ""}, {"POST", "/api/tickets/" + id + "/messages", `{"body":"reply","idempotency_key":"foreign-reply"}`}, {"POST", "/api/tickets/" + id + "/status", `{"status":"closed"}`}} {
			got := f.request(t, browser, route.method, route.path, route.body)
			if got.StatusCode != 404 {
				t.Fatalf("cross owner %d %s", got.StatusCode, got.Body)
			}
		}
	}
	got := f.request(t, f.adminClient, "GET", "/api/admin/tickets/"+id, "")
	adminDetail := ticketHTTPDetail(t, got)
	if got.StatusCode != 200 || adminDetail.Ticket.User == nil || adminDetail.Ticket.User.Name != f.alice.Name {
		t.Fatal(got.Body)
	}
	got = f.request(t, f.adminClient, "POST", "/api/admin/tickets/"+id+"/messages", `{"body":"已收到反馈","idempotency_key":"admin-http-reply"}`)
	if got.StatusCode != 201 || ticketHTTPDetail(t, got).Ticket.Status != "processing" {
		t.Fatal(got.Body)
	}
	got = f.request(t, f.adminClient, "POST", "/api/admin/tickets/"+id+"/status", `{"status":"resolved"}`)
	if got.StatusCode != 200 || ticketHTTPDetail(t, got).Ticket.Status != "resolved" {
		t.Fatal(got.Body)
	}
	got = f.request(t, f.aliceClient, "POST", "/api/tickets/"+id+"/messages", `{"body":"还有问题","idempotency_key":"owner-http-reply"}`)
	if got.StatusCode != 201 || ticketHTTPDetail(t, got).Ticket.Status != "open" {
		t.Fatal(got.Body)
	}
	got = f.request(t, f.adminClient, "GET", "/api/admin/audit", "")
	if got.StatusCode != 200 || strings.Contains(got.Body, "PRIVATE_TICKET_BODY") || strings.Contains(got.Body, "任务显示问题") || strings.Contains(got.Body, "已收到反馈") {
		t.Fatal("audit exposed content")
	}
	got = f.request(t, f.bobClient, "GET", "/api/tickets", "")
	if got.StatusCode != 200 || strings.Contains(got.Body, id) || strings.Contains(got.Body, f.alice.Name) {
		t.Fatal("list exposed other owner")
	}
	if err := f.platform.SetStatus(context.Background(), f.admin.ID, f.alice.ID, "disabled"); err != nil {
		t.Fatal(err)
	}
	if got = f.request(t, f.aliceClient, "GET", "/api/tickets", ""); got.StatusCode != 401 {
		t.Fatal("disabled account accessed tickets")
	}
}

func TestTicketHTTPRejectsUnknownFieldsCredentialsBadMethodsAndOrigin(t *testing.T) {
	f := newPlatformHTTPFixture(t, nil)
	for _, body := range []string{
		`{"subject":"subject","category":"task","body":"body","idempotency_key":"create-reject","owner_id":"other"}`,
		`{"subject":"subject","category":"task","body":"password: secret-fixture","idempotency_key":"create-reject"}`,
		`{"subject":"subject","category":"task","body":"Cookie: fake-session","idempotency_key":"create-reject"}`,
		`{"subject":"subject","category":"task","body":"body","idempotency_key":"create-reject"} {}`,
		`{"subject":"subject","category":"bad","body":"body","idempotency_key":"create-reject"}`,
	} {
		got := f.request(t, f.aliceClient, "POST", "/api/tickets", body)
		if got.StatusCode != 400 || strings.Contains(got.Body, "secret-fixture") {
			t.Fatalf("rejected input leaked or accepted %d %s", got.StatusCode, got.Body)
		}
	}
	if got := f.request(t, f.aliceClient, "GET", "/api/tickets?limit=101", ""); got.StatusCode != 400 {
		t.Fatal(got.Body)
	}
	if got := f.request(t, f.aliceClient, "GET", "/api/tickets?cursor=bad", ""); got.StatusCode != 400 {
		t.Fatal(got.Body)
	}
	if got := f.request(t, f.aliceClient, "DELETE", "/api/tickets", ""); got.StatusCode != 405 {
		t.Fatal(got.Body)
	}
	if got := f.request(t, f.adminClient, "POST", "/api/admin/tickets", `{}`); got.StatusCode != 405 {
		t.Fatal(got.Body)
	}
	r := httptest.NewRequest("POST", "http://service.example/api/tickets", strings.NewReader(`{}`))
	r.Header.Set("Origin", "https://foreign.example")
	w := httptest.NewRecorder()
	f.app.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("ticket bypassed origin protection")
	}
	for i := 0; i < 5; i++ {
		body := `{"subject":"subject","category":"other","body":"body","idempotency_key":"create-limit-` + strings.Repeat("x", i+1) + `"}`
		if got := f.request(t, f.aliceClient, "POST", "/api/tickets", body); got.StatusCode != 201 {
			t.Fatal(got.Body)
		}
	}
	if got := f.request(t, f.aliceClient, "POST", "/api/tickets", `{"subject":"over","category":"other","body":"over","idempotency_key":"create-limit-over"}`); got.StatusCode != 429 {
		t.Fatalf("create rate %d %s", got.StatusCode, got.Body)
	}
}
