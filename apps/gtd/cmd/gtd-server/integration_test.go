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
	for _, p := range []string{"/", "/capture", "/process", "/review", "/next", "/contexts", "/waiting", "/projects", "/done", "/help"} {
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

// Capture can carry the full set of Edit-style fields (context, project,
// waiting-on, dates, notes) so an item is filed directly instead of waiting in
// the inbox. A bare capture still lands in the inbox.
func TestCaptureWithDetails(t *testing.T) {
	srv, store := newTestServer(t)

	// A context/due/notes capture files the item straight onto a context list,
	// skipping the inbox, with the note stored in its own file.
	note := "See thread from Friday\nAttach the Q2 numbers"
	do(t, srv, "POST", "/capture", url.Values{
		"text":    {"Email Bob the report"},
		"context": {"computer"},
		"due":     {"2026-07-01"},
		"notes":   {note},
	})
	// A bare capture still parks in the inbox to be processed later.
	do(t, srv, "POST", "/capture", url.Values{"text": {"Some loose thought"}})

	active, _ := store.Read(todotxt.ActiveFile)
	var filed, loose *todotxt.Task
	for i := range active {
		switch {
		case strings.Contains(active[i].Text, "Email Bob"):
			filed = &active[i]
		case strings.Contains(active[i].Text, "loose thought"):
			loose = &active[i]
		}
	}
	if filed == nil || loose == nil {
		t.Fatalf("both captures should persist: %v", active)
	}
	if !filed.HasContext("computer") || filed.HasContext("inbox") {
		t.Errorf("detailed capture should be filed on @computer, not @inbox: %q", filed.Text)
	}
	if filed.Tag("due") != "2026-07-01" {
		t.Errorf("due not applied: %q", filed.Text)
	}
	if filed.Created == "" {
		t.Error("captured item should carry a creation date")
	}
	key := filed.Tag("note")
	if key == "" {
		t.Fatalf("note tag was not attached: %q", filed.Text)
	}
	if stored, _ := store.ReadNote(key); stored != note {
		t.Errorf("note content = %q, want %q", stored, note)
	}
	if !loose.HasContext("inbox") {
		t.Errorf("bare capture should land in the inbox: %q", loose.Text)
	}

	// Capturing someone to wait on files it onto @waiting, out of the inbox.
	do(t, srv, "POST", "/capture", url.Values{
		"text": {"Approval from finance"}, "person": {"alice"},
	})
	active, _ = store.Read(todotxt.ActiveFile)
	got := active[len(active)-1]
	if !got.HasContext("waiting") || got.Tag("for") != "alice" || got.HasContext("inbox") {
		t.Errorf("waiting capture not filed correctly: %q", got.Text)
	}
}

// The weekly review surfaces the inbox count, an overdue hard-landscape item, and
// a stalled project on one read-only page, and writes nothing (no undo armed).
func TestReviewPage(t *testing.T) {
	srv, store := newTestServer(t)
	// A stalled project (a +project whose only task is still in the inbox), an
	// overdue next action, and a leftover inbox item.
	store.Append(todotxt.ActiveFile, "2026-01-01 plan the gala +Gala @inbox")
	store.Append(todotxt.ActiveFile, "2020-01-01 file taxes @computer due:2020-04-15")
	store.Append(todotxt.ActiveFile, "loose capture @inbox")

	before, _ := store.Raw(todotxt.ActiveFile)
	body := do(t, srv, "GET", "/review", nil).Body.String()
	if !strings.Contains(body, "Weekly review") {
		t.Fatal("review page missing heading")
	}
	if !strings.Contains(body, "Overdue") || !strings.Contains(body, "file taxes") {
		t.Errorf("review should surface the overdue item:\n%s", body)
	}
	if !strings.Contains(body, "+Gala") {
		t.Errorf("review should list the stalled project +Gala")
	}
	if !strings.Contains(body, "to clarify") {
		t.Errorf("review should report a non-empty inbox")
	}
	// It's read-only: rendering must leave the file byte-for-byte unchanged.
	if after, _ := store.Raw(todotxt.ActiveFile); string(after) != string(before) {
		t.Error("review page must not mutate the store")
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
	for _, p := range []string{"/process/do", "/undo", "/redo", "/restore"} {
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

// Editing changes only the description, leaving the task's place and its
// structured fields (here, the creation date) untouched.
func TestEditFlow(t *testing.T) {
	srv, store := newTestServer(t)
	do(t, srv, "POST", "/capture", url.Values{"text": {"Call dentits"}})
	do(t, srv, "POST", "/process/do", url.Values{"id": {"0"}, "decision": {"next"}, "context": {"calls"}})

	before, _ := store.Read(todotxt.ActiveFile)
	created := before[0].Created
	if created == "" {
		t.Fatal("captured task should carry a creation date to preserve")
	}

	// The edit form is reachable and prefilled with the current text.
	if b := do(t, srv, "GET", "/edit?id=0&back=/next", nil).Body.String(); !strings.Contains(b, "Call dentits") {
		t.Fatalf("edit form missing current text; got %s", b)
	}

	if rec := do(t, srv, "POST", "/edit", url.Values{
		"id": {"0"}, "text": {"Call dentist @calls"}, "back": {"/next"},
	}); rec.Code != http.StatusSeeOther {
		t.Fatalf("edit = %d, want 303", rec.Code)
	}
	after, _ := store.Read(todotxt.ActiveFile)
	if len(after) != 1 || after[0].Text != "Call dentist @calls" {
		t.Fatalf("edit did not replace the text: %v", after)
	}
	if after[0].Created != created {
		t.Errorf("edit clobbered the creation date: %q -> %q", created, after[0].Created)
	}
}

// The edit form offers the Clarify controls: a name in "waiting on" marks the
// item @waiting with a for: tag, and the date fields set due:/t:.
func TestEditStructuredFields(t *testing.T) {
	srv, store := newTestServer(t)
	do(t, srv, "POST", "/capture", url.Values{"text": {"ask Bob about the report"}})
	do(t, srv, "POST", "/process/do", url.Values{"id": {"0"}, "decision": {"next"}, "context": {"calls"}})

	if rec := do(t, srv, "POST", "/edit", url.Values{
		"id": {"0"}, "text": {"ask Bob about the report @calls"},
		"person": {"bob"}, "due": {"2026-07-01"}, "back": {"/next"},
	}); rec.Code != http.StatusSeeOther {
		t.Fatalf("edit = %d, want 303", rec.Code)
	}
	active, _ := store.Read(todotxt.ActiveFile)
	got := active[0]
	if !got.HasContext("waiting") || got.Tag("for") != "bob" {
		t.Errorf("waiting-on not applied: %q", got.Text)
	}
	if got.Tag("due") != "2026-07-01" {
		t.Errorf("due not applied: %q", got.Text)
	}
}

// Notes are attached via a note: pointer tag and stored in their own file; the
// long key never shows up in the displayed text, only a 📝 indicator.
func TestEditNotes(t *testing.T) {
	srv, store := newTestServer(t)
	do(t, srv, "POST", "/capture", url.Values{"text": {"plan the trip"}})
	do(t, srv, "POST", "/process/do", url.Values{"id": {"0"}, "decision": {"next"}, "context": {"computer"}})

	note := "Flights: see link\nHotel: the one near the park"
	do(t, srv, "POST", "/edit", url.Values{
		"id": {"0"}, "text": {"plan the trip @computer"}, "notes": {note}, "back": {"/next"},
	})

	active, _ := store.Read(todotxt.ActiveFile)
	key := active[0].Tag("note")
	if key == "" {
		t.Fatal("note tag was not attached to the task")
	}
	stored, _ := store.ReadNote(key)
	if stored != note {
		t.Errorf("note content = %q, want %q", stored, note)
	}
	// The next list shows the 📝 flag but never the raw note key.
	b := do(t, srv, "GET", "/next", nil).Body.String()
	if !strings.Contains(b, "📝") {
		t.Error("next list should show a note indicator")
	}
	if strings.Contains(b, "note:"+key) {
		t.Error("raw note key leaked into the rendered list")
	}
	// Re-editing prefills the note so it isn't lost.
	if e := do(t, srv, "GET", "/edit?id=0&back=/next", nil).Body.String(); !strings.Contains(e, "near the park") {
		t.Error("edit form should prefill the existing note")
	}

	// Clearing the notes field removes the tag and the file.
	do(t, srv, "POST", "/edit", url.Values{
		"id": {"0"}, "text": {"plan the trip @computer"}, "notes": {""}, "back": {"/next"},
	})
	active, _ = store.Read(todotxt.ActiveFile)
	if active[0].Tag("note") != "" {
		t.Error("emptying notes should drop the note: tag")
	}
	if got, _ := store.ReadNote(key); got != "" {
		t.Error("emptying notes should delete the note file")
	}
}

// A newline in the edited text must be collapsed, never written through as a
// second line that would forge an extra task in the file.
func TestEditRejectsLineInjection(t *testing.T) {
	srv, store := newTestServer(t)
	do(t, srv, "POST", "/capture", url.Values{"text": {"thing @home"}})

	do(t, srv, "POST", "/edit", url.Values{
		"id": {"0"}, "text": {"line one\nx forged done line"}, "back": {"/next"},
	})
	active, _ := store.Read(todotxt.ActiveFile)
	if len(active) != 1 {
		t.Fatalf("edit injected a line: active = %v", active)
	}
	if strings.Contains(active[0].Text, "\n") {
		t.Errorf("newline survived into the task text: %q", active[0].Text)
	}

	// An all-whitespace edit is rejected, not silently blanked.
	if rec := do(t, srv, "POST", "/edit", url.Values{"id": {"0"}, "text": {"   "}}); rec.Code != 400 {
		t.Errorf("blank edit = %d, want 400", rec.Code)
	}
}

// Undo is transient and scoped: it's offered only on the page you land on right
// after an action (the redirect arms ?undo=1), not as a persistent control that
// follows you around to be mis-tapped.
func TestUndoIsTransientAndScoped(t *testing.T) {
	srv, store := newTestServer(t)

	rec := do(t, srv, "POST", "/capture", url.Values{"text": {"oops"}})
	if loc := rec.Header().Get("Location"); !strings.Contains(loc, "undo=1") {
		t.Fatalf("capture redirect should arm undo; Location=%q", loc)
	}

	// The post-action page (carrying the flag) offers Undo...
	if b := do(t, srv, "GET", "/capture?ok=1&undo=1", nil).Body.String(); !strings.Contains(b, "Undo that") {
		t.Fatal("undo control missing on the post-action page")
	}
	// ...but a plain navigation does NOT, even though the store could still undo.
	// This is the fix: no persistent button sitting in the thumb zone.
	if !store.CanUndo() {
		t.Fatal("precondition: the store should still be able to undo")
	}
	if b := do(t, srv, "GET", "/next", nil).Body.String(); strings.Contains(b, "Undo that") {
		t.Fatal("undo control must not persist onto unrelated pages")
	}

	if rec := do(t, srv, "POST", "/undo", url.Values{}); rec.Code != http.StatusSeeOther {
		t.Fatalf("undo = %d, want 303", rec.Code)
	}
	if active, _ := store.Read(todotxt.ActiveFile); len(active) != 0 {
		t.Fatalf("undo did not remove the captured item: %v", active)
	}
}

// An accidental undo is one tap to recover: undo arms a redo, and redo reapplies
// the change and re-arms undo (the two toggle).
func TestUndoRedoRoundTrip(t *testing.T) {
	srv, store := newTestServer(t)
	do(t, srv, "POST", "/capture", url.Values{"text": {"keep me"}})

	rec := do(t, srv, "POST", "/undo", url.Values{})
	if loc := rec.Header().Get("Location"); !strings.Contains(loc, "redo=1") {
		t.Fatalf("undo should arm redo; Location=%q", loc)
	}
	if active, _ := store.Read(todotxt.ActiveFile); len(active) != 0 {
		t.Fatalf("undo should remove the item: %v", active)
	}

	// A page carrying redo=1 offers Redo and not Undo (undo is spent).
	b := do(t, srv, "GET", "/?redo=1", nil).Body.String()
	if !strings.Contains(b, "Redo that") || strings.Contains(b, "Undo that") {
		t.Fatalf("redo page should show only Redo; got %s", b)
	}

	rec = do(t, srv, "POST", "/redo", url.Values{})
	if loc := rec.Header().Get("Location"); !strings.Contains(loc, "undo=1") {
		t.Fatalf("redo should re-arm undo; Location=%q", loc)
	}
	active, _ := store.Read(todotxt.ActiveFile)
	if len(active) != 1 || !strings.Contains(active[0].Text, "keep me") {
		t.Fatalf("redo should bring the item back: %v", active)
	}
}

// The inbox-emoji favicon is served from the embedded static FS and linked in
// every page's head.
func TestFavicon(t *testing.T) {
	srv, _ := newTestServer(t)
	rec := do(t, srv, "GET", "/static/favicon.svg", nil)
	if rec.Code != 200 {
		t.Fatalf("GET favicon = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "image/svg+xml") {
		t.Errorf("favicon Content-Type = %q, want image/svg+xml", ct)
	}
	if !strings.Contains(rec.Body.String(), "📥") {
		t.Errorf("favicon should render the inbox emoji; got %s", rec.Body.String())
	}
	if b := do(t, srv, "GET", "/capture", nil).Body.String(); !strings.Contains(b, `rel="icon" href="/static/favicon.svg"`) {
		t.Errorf("pages should link the favicon; got %s", b)
	}
}

// Completing a task and then undoing must put it back on the active list and
// out of done.txt in one step.
func TestUndoRestoresCompletedTask(t *testing.T) {
	srv, store := newTestServer(t)
	do(t, srv, "POST", "/capture", url.Values{"text": {"task"}})
	do(t, srv, "POST", "/process/do", url.Values{"id": {"0"}, "decision": {"next"}, "context": {"home"}})
	do(t, srv, "POST", "/done", url.Values{"id": {"0"}, "back": {"/next"}})

	do(t, srv, "POST", "/undo", url.Values{})
	active, _ := store.Read(todotxt.ActiveFile)
	done, _ := store.Read(todotxt.DoneFile)
	if len(active) != 1 {
		t.Errorf("undo of done: active = %v, want one task", active)
	}
	if len(done) != 0 {
		t.Errorf("undo of done: done.txt = %v, want empty", done)
	}
}

// Next actions can be filtered by project, and the projects screen links there
// (not to the old dead /next?context= link).
func TestNextFilterByProject(t *testing.T) {
	srv, _ := newTestServer(t)
	do(t, srv, "POST", "/capture", url.Values{"text": {"buy tiles"}})
	do(t, srv, "POST", "/process/do", url.Values{"id": {"0"}, "decision": {"next"}, "context": {"errands"}})
	do(t, srv, "POST", "/edit", url.Values{"id": {"0"}, "text": {"buy tiles @errands"}, "project": {"Reno"}})
	do(t, srv, "POST", "/capture", url.Values{"text": {"email Sam"}})
	do(t, srv, "POST", "/process/do", url.Values{"id": {"1"}, "decision": {"next"}, "context": {"computer"}})

	// Filtered to +Reno: only the tiles task.
	b := do(t, srv, "GET", "/next?project=Reno", nil).Body.String()
	if !strings.Contains(b, "buy tiles") || strings.Contains(b, "email Sam") {
		t.Fatalf("/next?project=Reno should show only the Reno task; got %s", b)
	}
	// The projects screen links into each project's planning page.
	p := do(t, srv, "GET", "/projects", nil).Body.String()
	if !strings.Contains(p, `href="/project?name=Reno"`) {
		t.Errorf("projects screen should link to the project page; got %s", p)
	}
	// API parity.
	a := do(t, srv, "GET", "/api/tasks?view=next&project=Reno", nil).Body.String()
	if !strings.Contains(a, "buy tiles") || strings.Contains(a, "email Sam") {
		t.Errorf("api project filter wrong; got %s", a)
	}
}

// The project page lets you add tasks and chain them with dependencies: a
// blocked task stays out of Next until its prerequisite is completed.
func TestProjectPlanningWithDependencies(t *testing.T) {
	srv, store := newTestServer(t)

	// Add a first step, then a second blocked by it (the first task is index 0).
	if rec := do(t, srv, "POST", "/project/add", url.Values{
		"project": {"Reno"}, "text": {"measure the wall"}, "context": {"home"},
	}); rec.Code != http.StatusSeeOther {
		t.Fatalf("add first = %d, want 303", rec.Code)
	}
	if rec := do(t, srv, "POST", "/project/add", url.Values{
		"project": {"Reno"}, "text": {"order cabinets"}, "context": {"calls"}, "after": {"0"},
	}); rec.Code != http.StatusSeeOther {
		t.Fatalf("add blocked = %d, want 303", rec.Code)
	}

	active, _ := store.Read(todotxt.ActiveFile)
	if len(active) != 2 {
		t.Fatalf("want 2 tasks, got %v", active)
	}
	// The prerequisite got an id:, the dependent an after: pointing at it.
	depID := active[0].Tag("id")
	if depID == "" || active[1].Tag("after") != depID {
		t.Fatalf("dependency not linked: %q / %q", active[0].Text, active[1].Text)
	}

	// While blocked, "order cabinets" is not a next action; "measure" is.
	next := do(t, srv, "GET", "/api/tasks?view=next", nil).Body.String()
	if !strings.Contains(next, "measure the wall") || strings.Contains(next, "order cabinets") {
		t.Fatalf("blocked task should be hidden from Next; got %s", next)
	}
	// The project page shows both, under Available and Blocked, and never leaks
	// the raw id/after keys.
	page := do(t, srv, "GET", "/project?name=Reno", nil).Body.String()
	if !strings.Contains(page, "measure the wall") || !strings.Contains(page, "order cabinets") {
		t.Fatalf("project page missing tasks; got %s", page)
	}
	if strings.Contains(page, "id:"+depID) || strings.Contains(page, "after:"+depID) {
		t.Errorf("project page leaked raw dependency keys; got %s", page)
	}

	// Completing the prerequisite (index 0) unblocks the dependent.
	if rec := do(t, srv, "POST", "/done", url.Values{"id": {"0"}, "back": {"/project?name=Reno"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("done = %d, want 303", rec.Code)
	}
	next = do(t, srv, "GET", "/api/tasks?view=next", nil).Body.String()
	if !strings.Contains(next, "order cabinets") {
		t.Fatalf("dependent should unblock once its prerequisite is done; got %s", next)
	}
}

// Editing a task's wording preserves its (hidden) dependency link.
func TestEditPreservesDependency(t *testing.T) {
	srv, store := newTestServer(t)
	do(t, srv, "POST", "/project/add", url.Values{"project": {"P"}, "text": {"first"}, "context": {"home"}})
	do(t, srv, "POST", "/project/add", url.Values{"project": {"P"}, "text": {"second"}, "context": {"home"}, "after": {"0"}})

	before, _ := store.Read(todotxt.ActiveFile)
	want := before[1].Tag("after")
	if want == "" {
		t.Fatal("setup: second task should carry an after: tag")
	}
	// The edit form must not expose the raw after: key in its textbox.
	form := do(t, srv, "GET", "/edit?id=1&back=/project?name=P", nil).Body.String()
	if strings.Contains(form, "after:"+want) {
		t.Errorf("edit box leaked the dependency key; got %s", form)
	}
	// Rewording the task keeps the dependency.
	do(t, srv, "POST", "/edit", url.Values{"id": {"1"}, "text": {"second, reworded"}})
	after, _ := store.Read(todotxt.ActiveFile)
	if after[1].Tag("after") != want {
		t.Errorf("edit dropped the after: tag: %q", after[1].Text)
	}
	if !strings.Contains(after[1].Text, "second, reworded") {
		t.Errorf("edit did not apply new text: %q", after[1].Text)
	}
}

// Assigning a project via the edit form adds the +tag (existing projects stay).
func TestEditAddsProject(t *testing.T) {
	srv, store := newTestServer(t)
	do(t, srv, "POST", "/capture", url.Values{"text": {"pick paint"}})
	do(t, srv, "POST", "/process/do", url.Values{"id": {"0"}, "decision": {"next"}, "context": {"errands"}})

	do(t, srv, "POST", "/edit", url.Values{"id": {"0"}, "text": {"pick paint @errands"}, "project": {"Reno"}})
	active, _ := store.Read(todotxt.ActiveFile)
	if !active[0].HasProject("Reno") {
		t.Fatalf("project not added: %q", active[0].Text)
	}
	// Idempotent: re-adding the same project doesn't duplicate it.
	do(t, srv, "POST", "/edit", url.Values{"id": {"0"}, "text": {"pick paint @errands +Reno"}, "project": {"Reno"}})
	active, _ = store.Read(todotxt.ActiveFile)
	if got := strings.Count(active[0].Text, "+Reno"); got != 1 {
		t.Errorf("project should appear once, got %d in %q", got, active[0].Text)
	}
}

// The help screen explains the method and walks through the five GTD phases.
func TestHelpScreen(t *testing.T) {
	srv, _ := newTestServer(t)
	b := do(t, srv, "GET", "/help", nil).Body.String()
	for _, phase := range []string{"Capture", "Clarify", "Organize", "Reflect", "Engage"} {
		if !strings.Contains(b, phase) {
			t.Errorf("help screen missing the %q phase", phase)
		}
	}
}

// The Done screen lists completed tasks and Restore brings one back to the
// active list, un-completed and out of done.txt.
func TestDoneScreenAndRestore(t *testing.T) {
	srv, store := newTestServer(t)
	do(t, srv, "POST", "/capture", url.Values{"text": {"Mop the floor"}})
	do(t, srv, "POST", "/process/do", url.Values{"id": {"0"}, "decision": {"next"}, "context": {"home"}})
	do(t, srv, "POST", "/done", url.Values{"id": {"0"}, "back": {"/next"}})

	// The Done screen (GET /done) shows the completed item.
	if b := do(t, srv, "GET", "/done", nil).Body.String(); !strings.Contains(b, "Mop the floor") {
		t.Fatalf("done screen missing the completed task; got %s", b)
	}

	if rec := do(t, srv, "POST", "/restore", url.Values{"id": {"0"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("restore = %d, want 303", rec.Code)
	}
	active, _ := store.Read(todotxt.ActiveFile)
	done, _ := store.Read(todotxt.DoneFile)
	if len(done) != 0 {
		t.Errorf("restore left the task in done.txt: %v", done)
	}
	if len(active) != 1 || active[0].Done {
		t.Fatalf("restore did not return an un-completed task: %v", active)
	}
	// It kept its real context, so it rejoins next actions.
	if !active[0].HasContext("home") {
		t.Errorf("restored task lost its context: %v", active[0])
	}
}

// A task completed with no real context (a "do it now") must not vanish on
// restore — it falls back to the inbox so it can be re-clarified.
func TestRestoreContextlessGoesToInbox(t *testing.T) {
	srv, store := newTestServer(t)
	do(t, srv, "POST", "/capture", url.Values{"text": {"two-minute thing"}})
	do(t, srv, "POST", "/process/do", url.Values{"id": {"0"}, "decision": {"donow"}})

	do(t, srv, "POST", "/restore", url.Values{"id": {"0"}})
	active, _ := store.Read(todotxt.ActiveFile)
	if len(active) != 1 || !active[0].HasContext("inbox") {
		t.Fatalf("context-less restore should land in the inbox: %v", active)
	}
}

// A blank capture is a no-op and must not flash "Captured." — the redirect
// drops the ok=1 marker.
func TestCaptureEmptyIsNoOp(t *testing.T) {
	srv, store := newTestServer(t)
	rec := do(t, srv, "POST", "/capture", url.Values{"text": {"   "}})
	if loc := rec.Header().Get("Location"); loc != "/capture" {
		t.Errorf("empty capture redirect = %q, want /capture (no ok=1)", loc)
	}
	if active, _ := store.Read(todotxt.ActiveFile); len(active) != 0 {
		t.Errorf("empty capture should add nothing, got %v", active)
	}
}

// The Clarify screen strips @inbox cleanly (token-exact, no leftover double
// space) even when the marker sits mid-description.
func TestProcessStripsInboxCleanly(t *testing.T) {
	srv, _ := newTestServer(t)
	do(t, srv, "POST", "/capture", url.Values{"text": {"email @inbox boss"}})
	b := do(t, srv, "GET", "/process", nil).Body.String()
	if !strings.Contains(b, "email boss") {
		t.Errorf("expected cleanly-stripped 'email boss'; got %s", b)
	}
	if strings.Contains(b, "email  boss") {
		t.Error("leftover double space from substring strip")
	}
}

// The done API view matches the web Done screen: newest completion first.
func TestAPIDoneNewestFirst(t *testing.T) {
	srv, _ := newTestServer(t)
	for _, txt := range []string{"first", "second"} {
		do(t, srv, "POST", "/capture", url.Values{"text": {txt}})
		do(t, srv, "POST", "/process/do", url.Values{"id": {"0"}, "decision": {"donow"}})
	}
	b := do(t, srv, "GET", "/api/tasks?view=done", nil).Body.String()
	if strings.Index(b, "second") > strings.Index(b, "first") {
		t.Errorf("done API should be newest-first (second before first); got %s", b)
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
