package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// capture records what the CLI actually sent so we can assert its API contract:
// correct method, path, the X-GTD-Client header (its CSRF credential), and body.
type captured struct {
	method, path, client, body string
}

func stubServer(t *testing.T, status int, respBody string, rec *captured) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		rec.method = r.Method
		rec.path = r.URL.RequestURI()
		rec.client = r.Header.Get("X-GTD-Client")
		rec.body = string(b)
		w.WriteHeader(status)
		io.WriteString(w, respBody)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestCLIAddSendsCorrectRequest(t *testing.T) {
	var rec captured
	srv := stubServer(t, http.StatusCreated, "", &rec)
	t.Setenv("GTD_ENDPOINT", srv.URL)

	if err := add("Call the dentist"); err != nil {
		t.Fatalf("add: %v", err)
	}
	if rec.method != "POST" || rec.path != "/api/capture" {
		t.Errorf("add hit %s %s, want POST /api/capture", rec.method, rec.path)
	}
	if rec.client == "" {
		t.Error("add did not set the X-GTD-Client header")
	}
	var body struct{ Text string }
	if err := json.Unmarshal([]byte(rec.body), &body); err != nil || body.Text != "Call the dentist" {
		t.Errorf("add body = %q, want text=Call the dentist", rec.body)
	}
}

func TestCLIListSendsViewAndContext(t *testing.T) {
	var rec captured
	srv := stubServer(t, http.StatusOK, `[{"id":2,"text":"do x @home","due":"2026-07-01"}]`, &rec)
	t.Setenv("GTD_ENDPOINT", srv.URL)

	if err := list("next", "home"); err != nil {
		t.Fatalf("list: %v", err)
	}
	if rec.method != "GET" || !strings.Contains(rec.path, "/api/tasks") ||
		!strings.Contains(rec.path, "view=next") || !strings.Contains(rec.path, "context=home") {
		t.Errorf("list hit %s, want GET /api/tasks?view=next&context=home", rec.path)
	}
}

func TestCLIDoneSendsID(t *testing.T) {
	var rec captured
	srv := stubServer(t, http.StatusNoContent, "", &rec)
	t.Setenv("GTD_ENDPOINT", srv.URL)

	if err := done("3"); err != nil {
		t.Fatalf("done: %v", err)
	}
	if rec.method != "POST" || rec.path != "/api/done" {
		t.Errorf("done hit %s %s, want POST /api/done", rec.method, rec.path)
	}
	var body struct{ ID int }
	if err := json.Unmarshal([]byte(rec.body), &body); err != nil || body.ID != 3 {
		t.Errorf("done body = %q, want id=3", rec.body)
	}
}

func TestCLIDoneRejectsNonNumeric(t *testing.T) {
	t.Setenv("GTD_ENDPOINT", "http://127.0.0.1:0")
	if err := done("abc"); err == nil {
		t.Error("done with non-numeric id should error before any request")
	}
}

func TestCLISurfacesServerError(t *testing.T) {
	var rec captured
	srv := stubServer(t, http.StatusInternalServerError, "boom", &rec)
	t.Setenv("GTD_ENDPOINT", srv.URL)
	if err := add("x"); err == nil {
		t.Error("add should surface a non-201 response as an error")
	}
}

func TestEndpointTrimsTrailingSlash(t *testing.T) {
	t.Setenv("GTD_ENDPOINT", "https://t480.example/")
	if got := endpoint(); got != "https://t480.example" {
		t.Errorf("endpoint() = %q, want trailing slash trimmed", got)
	}
}
