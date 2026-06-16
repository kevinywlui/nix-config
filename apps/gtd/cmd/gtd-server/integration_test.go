package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/kevinywlui/nix-config/apps/gtd/internal/todotxt"
)

func newTestServer(t *testing.T) (*server, *todotxt.Store) {
	t.Helper()
	store, err := todotxt.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv, err := newServer(store)
	if err != nil {
		t.Fatal(err)
	}
	return srv, store
}

// do issues a request and returns the recorder. Same-origin POSTs set Origin so
// they pass the CSRF check; pass form values via vals (nil for GET).
func do(t *testing.T, srv *server, method, target string, vals url.Values) *httptest.ResponseRecorder {
	t.Helper()
	var body strings.Reader
	req := httptest.NewRequest(method, target, &body)
	if vals != nil {
		req = httptest.NewRequest(method, target, strings.NewReader(vals.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", "http://"+req.Host)
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

func TestPagesRender(t *testing.T) {
	srv, _ := newTestServer(t)
	for _, p := range []string{"/", "/capture", "/process", "/next", "/contexts", "/waiting", "/projects"} {
		if rec := do(t, srv, "GET", p, nil); rec.Code != 200 {
			t.Errorf("GET %s = %d, want 200; body=%s", p, rec.Code, rec.Body.String())
		}
	}
}

func TestCaptureRejectsCrossOrigin(t *testing.T) {
	srv, _ := newTestServer(t)
	// No Origin/Referer and no X-GTD-Client header -> must be rejected.
	req := httptest.NewRequest("POST", "/capture", strings.NewReader("text=sneaky"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin capture = %d, want 403", rec.Code)
	}
}

func TestCaptureThenProcessAndClarify(t *testing.T) {
	srv, _ := newTestServer(t)

	if rec := do(t, srv, "POST", "/capture", url.Values{"text": {"Call the dentist"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("capture = %d, want 303", rec.Code)
	}
	// It should now show as the inbox item on /process.
	rec := do(t, srv, "GET", "/process", nil)
	if !strings.Contains(rec.Body.String(), "Call the dentist") {
		t.Fatalf("/process missing captured item: %s", rec.Body.String())
	}
	// Clarify it into a next action.
	if rec := do(t, srv, "POST", "/process/do", url.Values{
		"id": {"0"}, "decision": {"next"}, "context": {"calls"},
	}); rec.Code != http.StatusSeeOther {
		t.Fatalf("clarify next = %d, want 303", rec.Code)
	}
	// And it should appear in the next-actions API.
	rec = do(t, srv, "GET", "/api/tasks?view=next", nil)
	if !strings.Contains(rec.Body.String(), "Call the dentist") || !strings.Contains(rec.Body.String(), "calls") {
		t.Fatalf("next API missing the action: %s", rec.Body.String())
	}
}

func TestClarifyValidation(t *testing.T) {
	srv, _ := newTestServer(t)
	do(t, srv, "POST", "/capture", url.Values{"text": {"something"}})

	cases := []struct {
		name string
		vals url.Values
		want int
	}{
		{"next without context", url.Values{"id": {"0"}, "decision": {"next"}}, 400},
		{"unknown decision", url.Values{"id": {"0"}, "decision": {"bogus"}}, 400},
		{"non-numeric id", url.Values{"id": {"x"}, "decision": {"trash"}}, 400},
		{"project missing fields", url.Values{"id": {"0"}, "decision": {"project"}, "project": {"p"}}, 400},
		{"out-of-range id", url.Values{"id": {"99"}, "decision": {"trash"}}, 500},
	}
	for _, c := range cases {
		if rec := do(t, srv, "POST", "/process/do", c.vals); rec.Code != c.want {
			t.Errorf("%s: got %d, want %d", c.name, rec.Code, c.want)
		}
	}
}

func TestMutatingEndpointsRejectGET(t *testing.T) {
	srv, _ := newTestServer(t)
	for _, p := range []string{"/process/do", "/done"} {
		if rec := do(t, srv, "GET", p, nil); rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("GET %s = %d, want 405", p, rec.Code)
		}
	}
}

func TestDoneFlow(t *testing.T) {
	srv, store := newTestServer(t)
	do(t, srv, "POST", "/capture", url.Values{"text": {"Quick task"}})
	do(t, srv, "POST", "/process/do", url.Values{"id": {"0"}, "decision": {"next"}, "context": {"home"}})

	if rec := do(t, srv, "POST", "/done", url.Values{"id": {"0"}, "back": {"/next"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("done = %d, want 303", rec.Code)
	}
	active, _ := store.Read(todotxt.ActiveFile)
	done, _ := store.Read(todotxt.DoneFile)
	if len(active) != 0 {
		t.Errorf("active = %d, want 0", len(active))
	}
	if len(done) != 1 || !done[0].Done {
		t.Errorf("done file = %v, want one completed task", done)
	}
}

// TestClarifyAllDestinations drives every Clarify branch and checks the task
// lands in the right file and leaves the active list.
func TestClarifyAllDestinations(t *testing.T) {
	cases := []struct {
		decision string
		extra    url.Values
		destFile string // "" => stays in active (next/waiting), checked separately
	}{
		{"someday", nil, todotxt.SomedayFile},
		{"reference", nil, todotxt.ReferenceFile},
		{"donow", nil, todotxt.DoneFile},
		{"waiting", url.Values{"person": {"bob"}}, ""},
		{"project", url.Values{"project": {"Reno"}, "na_text": {"Measure wall"}, "na_context": {"home"}}, ""},
	}
	for _, c := range cases {
		t.Run(c.decision, func(t *testing.T) {
			srv, store := newTestServer(t)
			do(t, srv, "POST", "/capture", url.Values{"text": {"a captured thing"}})
			vals := url.Values{"id": {"0"}, "decision": {c.decision}}
			for k, v := range c.extra {
				vals[k] = v
			}
			if rec := do(t, srv, "POST", "/process/do", vals); rec.Code != http.StatusSeeOther {
				t.Fatalf("%s = %d, want 303", c.decision, rec.Code)
			}
			active, _ := store.Read(todotxt.ActiveFile)
			switch c.decision {
			case "waiting":
				if len(active) != 1 || !strings.Contains(active[0].Text, "@waiting") {
					t.Fatalf("waiting: active=%v", active)
				}
			case "project":
				if len(active) != 1 || !strings.Contains(active[0].Text, "+Reno") {
					t.Fatalf("project: active=%v", active)
				}
			default:
				if len(active) != 0 {
					t.Fatalf("%s: active should be empty, got %v", c.decision, active)
				}
				dest, _ := store.Read(c.destFile)
				if len(dest) != 1 {
					t.Fatalf("%s: dest %s has %d entries, want 1", c.decision, c.destFile, len(dest))
				}
			}
		})
	}
}

func TestAPIBadInputs(t *testing.T) {
	srv, _ := newTestServer(t)

	if rec := do(t, srv, "GET", "/api/tasks?view=bogus", nil); rec.Code != 400 {
		t.Errorf("bogus view = %d, want 400", rec.Code)
	}

	// API capture with the CLI header but empty text -> 400.
	req := httptest.NewRequest("POST", "/api/capture", strings.NewReader(`{"text":"  "}`))
	req.Header.Set("X-GTD-Client", "cli")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Errorf("empty api capture = %d, want 400", rec.Code)
	}

	// API done with malformed JSON -> 400.
	req = httptest.NewRequest("POST", "/api/done", strings.NewReader(`{bad`))
	req.Header.Set("X-GTD-Client", "cli")
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Errorf("malformed api done = %d, want 400", rec.Code)
	}

	// API mutation without the CLI header -> 403.
	req = httptest.NewRequest("POST", "/api/capture", strings.NewReader(`{"text":"x"}`))
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("api capture no-auth = %d, want 403", rec.Code)
	}
}

// The capture screen surfaces how many items are waiting to be processed.
func TestCaptureShowsInboxCount(t *testing.T) {
	srv, _ := newTestServer(t)
	if b := do(t, srv, "GET", "/capture", nil).Body.String(); !strings.Contains(b, "Inbox empty") {
		t.Fatalf("empty capture page should say inbox is empty; got %s", b)
	}
	do(t, srv, "POST", "/capture", url.Values{"text": {"one"}})
	do(t, srv, "POST", "/capture", url.Values{"text": {"two"}})
	b := do(t, srv, "GET", "/capture", nil).Body.String()
	if !strings.Contains(b, "<b>2</b> items waiting to be processed") {
		t.Fatalf("capture page should show the inbox count of 2; got %s", b)
	}
}

// A missed required field re-renders the Clarify form inline (400 + HTML +
// message + the chosen decision preselected), not a plain error page.
func TestClarifyErrorRendersInline(t *testing.T) {
	srv, _ := newTestServer(t)
	do(t, srv, "POST", "/capture", url.Values{"text": {"Call the dentist"}})

	rec := do(t, srv, "POST", "/process/do", url.Values{"id": {"0"}, "decision": {"next"}})
	if rec.Code != 400 {
		t.Fatalf("next-without-context = %d, want 400", rec.Code)
	}
	b := rec.Body.String()
	if !strings.Contains(b, "Pick a context") {
		t.Errorf("missing the inline validation message; got %s", b)
	}
	if !strings.Contains(b, "Clarify") || !strings.Contains(b, "Call the dentist") {
		t.Errorf("should re-render the full Clarify page for the same item; got %s", b)
	}
	// The "next" radio must come back preselected so its panel reopens.
	if !strings.Contains(b, `value="next" checked`) {
		t.Errorf("chosen decision should be preserved as checked; got %s", b)
	}
}

// The raw view shows the on-disk file verbatim and rejects unknown selectors.
func TestRawView(t *testing.T) {
	srv, _ := newTestServer(t)
	do(t, srv, "POST", "/capture", url.Values{"text": {"buy milk"}})

	rec := do(t, srv, "GET", "/raw", nil)
	if rec.Code != 200 {
		t.Fatalf("GET /raw = %d, want 200", rec.Code)
	}
	b := rec.Body.String()
	if !strings.Contains(b, "buy milk") || !strings.Contains(b, "@inbox") {
		t.Errorf("raw todo.txt should contain the captured line verbatim; got %s", b)
	}
	if !strings.Contains(b, "done.txt") { // sibling-file tabs are present
		t.Errorf("raw view should offer the sibling-file tabs; got %s", b)
	}
	if rec := do(t, srv, "GET", "/raw?file=bogus", nil); rec.Code != 400 {
		t.Errorf("unknown file selector = %d, want 400", rec.Code)
	}
}
