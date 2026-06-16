package main

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/kevinywlui/nix-config/apps/gtd/internal/gtd"
	"github.com/kevinywlui/nix-config/apps/gtd/internal/todotxt"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

func today() string { return time.Now().Format("2006-01-02") }

type server struct {
	store *todotxt.Store
	tmpl  *template.Template
	mux   *http.ServeMux
}

func newServer(store *todotxt.Store) (*server, error) {
	tmpl, err := template.ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parsing templates: %w", err)
	}
	s := &server{store: store, tmpl: tmpl, mux: http.NewServeMux()}

	s.mux.HandleFunc("/", s.handleDashboard)
	s.mux.HandleFunc("/capture", s.handleCapture)
	s.mux.HandleFunc("/process", s.handleProcess)
	s.mux.HandleFunc("/process/do", s.handleProcessDo)
	s.mux.HandleFunc("/next", s.handleNext)
	s.mux.HandleFunc("/contexts", s.handleContexts)
	s.mux.HandleFunc("/waiting", s.handleWaiting)
	s.mux.HandleFunc("/projects", s.handleProjects)
	s.mux.HandleFunc("/raw", s.handleRaw)
	s.mux.HandleFunc("/help", s.handleHelp)
	s.mux.HandleFunc("/done", s.handleDone)
	s.mux.HandleFunc("/restore", s.handleRestore)
	s.mux.HandleFunc("/edit", s.handleEdit)
	s.mux.HandleFunc("/undo", s.handleUndo)

	s.mux.Handle("/static/", http.FileServer(http.FS(staticFS)))

	s.mux.HandleFunc("/api/tasks", s.apiTasks)
	s.mux.HandleFunc("/api/capture", s.apiCapture)
	s.mux.HandleFunc("/api/done", s.apiDone)
	s.mux.HandleFunc("/api/edit", s.apiEdit)
	s.mux.HandleFunc("/api/undo", s.apiUndo)
	s.mux.HandleFunc("/api/restore", s.apiRestore)
	return s, nil
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

// --- helpers ----------------------------------------------------------------

func (s *server) activeItems() ([]gtd.Item, error) {
	tasks, err := s.store.Read(todotxt.ActiveFile)
	if err != nil {
		return nil, err
	}
	return gtd.Items(tasks), nil
}

// reverseItems flips a slice in place (newest-appended first) without disturbing
// each Item's ID, so a reversed done list still restores by its true file index.
func reverseItems(items []gtd.Item) {
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
}

func (s *server) render(w http.ResponseWriter, name string, data any) {
	s.renderStatus(w, http.StatusOK, name, data)
}

// renderStatus renders a template with an explicit status code. The status is
// written after the body buffers successfully, so a template error still yields
// a clean 500 rather than a half-written page with the wrong code.
func (s *server) renderStatus(w http.ResponseWriter, code int, name string, data any) {
	data = s.withCommon(data)
	var buf bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	buf.WriteTo(w)
}

// withCommon injects view data every page shares — currently CanUndo, which
// drives the global Undo bar in the layout footer. Page handlers pass a
// map[string]any (or nil); a page that already set CanUndo keeps its value.
func (s *server) withCommon(data any) any {
	m, ok := data.(map[string]any)
	if !ok {
		m = map[string]any{}
	}
	if _, set := m["CanUndo"]; !set {
		m["CanUndo"] = s.store.CanUndo()
	}
	return m
}

// csrfOK rejects cross-site state-changing requests. A same-origin browser form
// carries an Origin/Referer matching Host; the CLI sets X-GTD-Client. A foreign
// site can forge neither (custom headers trip a CORS preflight we never allow,
// and Origin/Referer are browser-controlled), so this defeats CSRF without an
// app-level token while the tailnet remains the auth boundary.
func (s *server) csrfOK(r *http.Request) bool {
	if r.Header.Get("X-GTD-Client") != "" {
		return true
	}
	for _, h := range []string{r.Header.Get("Origin"), r.Header.Get("Referer")} {
		if u, err := url.Parse(h); err == nil && u.Host == r.Host {
			return true
		}
	}
	return false
}

func markDone(t todotxt.Task) todotxt.Task {
	if t.Done { // already complete — don't restamp the completion date
		return t
	}
	t.RemoveContext(gtd.ContextInbox)
	if t.Priority != "" { // preserve priority across completion (spec drops it)
		t.SetTag("pri", t.Priority)
		t.Priority = ""
	}
	t.Done = true
	t.Completed = today()
	return t
}

func stripInbox(t todotxt.Task) todotxt.Task {
	t.RemoveContext(gtd.ContextInbox)
	return t
}

// markUndone is the inverse of markDone: it clears the done marker and
// completion date and restores a priority that markDone parked in a pri: tag.
// A restored task with no real context (and not a waiting item) would be
// invisible on every list, so it falls back to the inbox to be re-clarified.
func markUndone(t todotxt.Task) todotxt.Task {
	t.Done = false
	t.Completed = ""
	if p := t.Tag("pri"); p != "" {
		t.Priority = p
		t.SetTag("pri", "")
	}
	if !gtd.IsWaiting(t) && !gtd.HasRealContext(t) {
		t.AddContext(gtd.ContextInbox)
	}
	return t
}

// safeBack confines a redirect target to a local path, rejecting absolute and
// scheme-relative URLs so the back param can't become an open redirect.
func safeBack(back string) string {
	// Must be a local absolute path. Reject scheme-relative ("//host") and the
	// backslash variant ("/\host") that browsers normalise to scheme-relative.
	if !strings.HasPrefix(back, "/") || strings.HasPrefix(back, "//") || strings.HasPrefix(back, "/\\") {
		return "/next"
	}
	return back
}

// removeActive deletes the task at id from the active file.
func (s *server) removeActive(id int) error {
	return s.store.Mutate(todotxt.ActiveFile, func(ts []todotxt.Task) ([]todotxt.Task, error) {
		if id < 0 || id >= len(ts) {
			return nil, fmt.Errorf("task %d not found", id)
		}
		return append(ts[:id:id], ts[id+1:]...), nil
	})
}

// replaceActive applies fn to the task at id in place.
func (s *server) replaceActive(id int, fn func(*todotxt.Task)) error {
	return s.store.Mutate(todotxt.ActiveFile, func(ts []todotxt.Task) ([]todotxt.Task, error) {
		if id < 0 || id >= len(ts) {
			return nil, fmt.Errorf("task %d not found", id)
		}
		t := ts[id]
		fn(&t)
		ts[id] = t
		return ts, nil
	})
}

// completeActive marks the task at id done and moves it to done.txt atomically.
func (s *server) completeActive(id int) error {
	return s.store.Transfer(todotxt.ActiveFile, id, todotxt.DoneFile, markDone)
}

// restoreDone un-completes the done-file task at id and moves it back to the
// active list atomically.
func (s *server) restoreDone(id int) error {
	return s.store.Transfer(todotxt.DoneFile, id, todotxt.ActiveFile, markUndone)
}

// editActive replaces the description of the task at id, preserving its
// structured fields (done marker, priority, dates). The new text is the
// caller's responsibility to normalise — see normalizeText.
func (s *server) editActive(id int, text string) error {
	return s.replaceActive(id, func(t *todotxt.Task) { t.Text = text })
}

// normalizeText collapses all runs of whitespace (including newlines, a
// line-injection vector into the todo.txt file) to single spaces, matching how
// Parse treats a captured line. Returns "" for blank input.
func normalizeText(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// --- HTML handlers ----------------------------------------------------------

// handleHelp serves the static guide to GTD and how this app implements it.
// It's GET-only content with no state, so it needs no CSRF check.
func (s *server) handleHelp(w http.ResponseWriter, r *http.Request) {
	s.render(w, "help", nil)
}

type counts struct {
	Inbox, Next, Waiting, Projects, Stalled int
}

func (s *server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	items, err := s.activeItems()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	t := today()
	projs := gtd.Projects(items, t)
	stalled := 0
	for _, p := range projs {
		if p.Stalled() {
			stalled++
		}
	}
	c := counts{
		Inbox:    len(gtd.Inbox(items)),
		Next:     len(gtd.NextActions(items, t, "", "")),
		Waiting:  len(gtd.Waiting(items)),
		Projects: len(projs),
		Stalled:  stalled,
	}
	s.render(w, "dashboard", map[string]any{"Counts": c})
}

func (s *server) handleCapture(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		if !s.csrfOK(r) {
			http.Error(w, "cross-origin request rejected", http.StatusForbidden)
			return
		}
		text := strings.TrimSpace(r.FormValue("text"))
		dest := "/capture" // a blank submit is a no-op, not a "Captured." success
		if text != "" {
			if err := s.capture(text); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			dest = "/capture?ok=1"
		}
		http.Redirect(w, r, dest, http.StatusSeeOther)
		return
	}
	inbox := 0
	if items, err := s.activeItems(); err == nil {
		inbox = len(gtd.Inbox(items))
	}
	s.render(w, "capture", map[string]any{
		"Saved": r.URL.Query().Get("ok") == "1",
		"Inbox": inbox,
	})
}

// capture appends a raw inbox item with today's creation date.
func (s *server) capture(text string) error {
	t, _ := todotxt.Parse(text)
	t.AddContext(gtd.ContextInbox)
	if t.Created == "" {
		t.Created = today()
	}
	return s.store.Append(todotxt.ActiveFile, t.String())
}

func (s *server) handleProcess(w http.ResponseWriter, r *http.Request) {
	items, err := s.activeItems()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	inbox := gtd.Inbox(items)
	if len(inbox) == 0 {
		s.render(w, "process-empty", nil)
		return
	}
	data := processData(inbox, "", "")
	data["Projects"] = s.projectNames()
	s.render(w, "process", data)
}

// processData builds the Clarify view model for the first inbox item. err and
// decision are non-empty only on a failed submission we're re-rendering: err is
// shown as an inline banner and decision re-selects the radio the user picked,
// so a missed field (e.g. a forgotten context) reopens the right panel instead
// of dumping them on a plain error page.
func processData(inbox []gtd.Item, errMsg, decision string) map[string]any {
	it := inbox[0]
	disp := it.Task
	disp.RemoveContext(gtd.ContextInbox)
	return map[string]any{
		"Item":      it,
		"Text":      disp.DisplayText(),
		"Remaining": len(inbox),
		"Today":     today(),
		"Error":     errMsg,
		"Decision":  decision,
	}
}

// processError re-renders the Clarify page for the current first inbox item with
// an inline validation message and a 400, preserving the chosen decision. Used
// instead of http.Error so a user mistake stays inside the app's UI.
func (s *server) processError(w http.ResponseWriter, msg, decision string) {
	items, err := s.activeItems()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	inbox := gtd.Inbox(items)
	if len(inbox) == 0 { // item got processed elsewhere meanwhile
		s.render(w, "process-empty", nil)
		return
	}
	data := processData(inbox, msg, decision)
	data["Projects"] = s.projectNames()
	s.renderStatus(w, http.StatusBadRequest, "process", data)
}

func (s *server) handleProcessDo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if !s.csrfOK(r) {
		http.Error(w, "cross-origin request rejected", http.StatusForbidden)
		return
	}
	id, err := strconv.Atoi(r.FormValue("id"))
	if err != nil {
		http.Error(w, "bad id", 400)
		return
	}
	f := func(k string) string { return strings.TrimSpace(r.FormValue(k)) }

	switch f("decision") {
	case "trash":
		err = s.removeActive(id)
	case "reference":
		err = s.moveOut(id, todotxt.ReferenceFile)
	case "someday":
		err = s.moveOut(id, todotxt.SomedayFile)
	case "donow":
		err = s.completeActive(id)
	case "next":
		ctx := f("context")
		if ctx == "" {
			s.processError(w, "Pick a context — a next action has to live on a context list (calls, computer, home…) so you can find it when you're there.", "next")
			return
		}
		err = s.replaceActive(id, func(t *todotxt.Task) {
			t.RemoveContext(gtd.ContextInbox)
			t.AddContext(ctx)
			t.SetTag("due", f("due"))
			t.SetTag("t", f("threshold"))
		})
	case "waiting":
		err = s.replaceActive(id, func(t *todotxt.Task) {
			t.RemoveContext(gtd.ContextInbox)
			t.AddContext(gtd.ContextWaiting)
			t.SetTag("for", f("person"))
			t.SetTag("t", f("followup"))
		})
	case "project":
		proj, naText, naCtx := f("project"), f("na_text"), f("na_context")
		if proj == "" || naText == "" || naCtx == "" {
			s.processError(w, "A project needs three things: a short tag, its very next physical action, and a context for that action.", "project")
			return
		}
		err = s.store.Mutate(todotxt.ActiveFile, func(ts []todotxt.Task) ([]todotxt.Task, error) {
			if id < 0 || id >= len(ts) {
				return nil, fmt.Errorf("task %d not found", id)
			}
			ts = append(ts[:id:id], ts[id+1:]...)
			na, _ := todotxt.Parse(naText)
			na.Created = today()
			na.AddContext(naCtx)
			na.AddProject(proj)
			return append(ts, na), nil
		})
	default:
		s.processError(w, "Choose what this item is first — pick one of the options above.", f("decision"))
		return
	}
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	http.Redirect(w, r, "/process", http.StatusSeeOther)
}

// moveOut strips the inbox marker and relocates the task to another file
// atomically (single lock acquisition in the store).
func (s *server) moveOut(id int, dest string) error {
	return s.store.Transfer(todotxt.ActiveFile, id, dest, stripInbox)
}

func (s *server) handleNext(w http.ResponseWriter, r *http.Request) {
	items, err := s.activeItems()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	ctx := r.URL.Query().Get("context")
	proj := r.URL.Query().Get("project")
	s.render(w, "next", map[string]any{
		"Items":   gtd.NextActions(items, today(), ctx, proj),
		"Context": ctx,
		"Project": proj,
		// Self is this page's own URL, used as the back target for the Done and
		// Edit links so they return to the same filtered view.
		"Self": nextURL(ctx, proj),
	})
}

// nextURL builds the /next URL for the given context/project filters, omitting
// empty ones.
func nextURL(ctx, proj string) string {
	q := url.Values{}
	if ctx != "" {
		q.Set("context", ctx)
	}
	if proj != "" {
		q.Set("project", proj)
	}
	if e := q.Encode(); e != "" {
		return "/next?" + e
	}
	return "/next"
}

func (s *server) handleContexts(w http.ResponseWriter, r *http.Request) {
	items, err := s.activeItems()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.render(w, "contexts", map[string]any{"Contexts": gtd.Contexts(items, today())})
}

func (s *server) handleWaiting(w http.ResponseWriter, r *http.Request) {
	items, err := s.activeItems()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.render(w, "waiting", map[string]any{"Items": gtd.Waiting(items)})
}

func (s *server) handleProjects(w http.ResponseWriter, r *http.Request) {
	items, err := s.activeItems()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.render(w, "projects", map[string]any{"Projects": gtd.Projects(items, today())})
}

// projectNames returns the active project names, for autocomplete datalists on
// the Clarify and Edit forms (keeping names consistent avoids +Foo/+foo drift).
func (s *server) projectNames() []string {
	items, err := s.activeItems()
	if err != nil {
		return nil
	}
	projs := gtd.Projects(items, today())
	names := make([]string, len(projs))
	for i, p := range projs {
		names[i] = p.Name
	}
	return names
}

// rawFiles maps the ?file= selector to the on-disk file, in display order.
var rawFiles = []struct{ Key, Name string }{
	{"todo", todotxt.ActiveFile},
	{"done", todotxt.DoneFile},
	{"someday", todotxt.SomedayFile},
	{"reference", todotxt.ReferenceFile},
}

// handleRaw shows the verbatim on-disk todo.txt (and its siblings) read-only, so
// you can see and trust exactly what the app is writing. It's GET-only and never
// mutates, so it needs no CSRF check.
func (s *server) handleRaw(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("file")
	if key == "" {
		key = "todo"
	}
	name := ""
	for _, f := range rawFiles {
		if f.Key == key {
			name = f.Name
		}
	}
	if name == "" {
		http.Error(w, "unknown file", http.StatusBadRequest)
		return
	}
	data, err := s.store.Raw(name)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.render(w, "raw", map[string]any{
		"Tabs":    rawFiles,
		"File":    key,
		"Name":    name,
		"Content": string(data),
	})
}

// handleDone serves the Done screen (GET) — completed actions, newest first —
// and completes an active task (POST), the action the task lists post to.
func (s *server) handleDone(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		if !s.csrfOK(r) {
			http.Error(w, "cross-origin request rejected", http.StatusForbidden)
			return
		}
		id, err := strconv.Atoi(r.FormValue("id"))
		if err != nil {
			http.Error(w, "bad id", 400)
			return
		}
		if err := s.completeActive(id); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		http.Redirect(w, r, safeBack(r.FormValue("back")), http.StatusSeeOther)
		return
	}
	tasks, err := s.store.Read(todotxt.DoneFile)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	// done.txt is appended chronologically; show the most recently completed
	// first while keeping each Item's ID as its true done-file index so Restore
	// targets the right line.
	items := gtd.Items(tasks)
	reverseItems(items)
	s.render(w, "done", map[string]any{"Items": items})
}

// handleRestore brings a completed task back to the active list, un-completing
// it. Like /done it's a mutation, so the global Undo can roll it back.
func (s *server) handleRestore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if !s.csrfOK(r) {
		http.Error(w, "cross-origin request rejected", http.StatusForbidden)
		return
	}
	id, err := strconv.Atoi(r.FormValue("id"))
	if err != nil {
		http.Error(w, "bad id", 400)
		return
	}
	if err := s.restoreDone(id); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	http.Redirect(w, r, "/done", http.StatusSeeOther)
}

// noteMax bounds a single note's size — generous for free-form notes and
// references, but a guard against a runaway paste filling the disk.
const noteMax = 100_000

// handleEdit serves the edit form (GET) and applies the edit (POST). The form
// mirrors the Clarify screen's controls: besides the description you can set a
// context, mark the item as waiting on someone, set defer/due dates, and attach
// free-form notes & references. Edits happen in place, so the task keeps its
// position and completion state.
func (s *server) handleEdit(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		s.editPost(w, r)
		return
	}
	id, err := strconv.Atoi(r.URL.Query().Get("id"))
	if err != nil {
		http.Error(w, "bad id", 400)
		return
	}
	items, err := s.activeItems()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if id < 0 || id >= len(items) {
		http.Error(w, "task not found", http.StatusNotFound)
		return
	}
	t := items[id].Task
	note := ""
	if key := t.Tag("note"); key != "" {
		note, _ = s.store.ReadNote(key)
	}
	// Show the description without the tags the form surfaces as their own
	// fields, so they aren't edited in two places at once.
	disp := t
	for _, k := range []string{"due", "t", "for", "note"} {
		disp.SetTag(k, "")
	}
	s.render(w, "edit", map[string]any{
		"Item":      items[id],
		"Back":      safeBack(r.URL.Query().Get("back")),
		"Text":      disp.Text,
		"Due":       t.Tag("due"),
		"Threshold": t.Tag("t"),
		"Person":    t.Tag("for"),
		"Note":      note,
		"Projects":  s.projectNames(),
	})
}

// editPost applies the edit form. Structured fields are layered onto the new
// description; an empty date/person field clears its tag, so the form is the
// task's full state. The note lives in its own file, keyed by a note: tag we
// mint on first use.
func (s *server) editPost(w http.ResponseWriter, r *http.Request) {
	if !s.csrfOK(r) {
		http.Error(w, "cross-origin request rejected", http.StatusForbidden)
		return
	}
	id, err := strconv.Atoi(r.FormValue("id"))
	if err != nil {
		http.Error(w, "bad id", 400)
		return
	}
	f := func(k string) string { return strings.TrimSpace(r.FormValue(k)) }
	text := normalizeText(r.FormValue("text"))
	if text == "" {
		http.Error(w, "an item can't be blank", 400)
		return
	}
	notes := strings.TrimRight(r.FormValue("notes"), " \t\r\n")
	if len(notes) > noteMax {
		http.Error(w, "note too large", 400)
		return
	}

	// Resolve (and if needed mint) the note key before mutating the task, so we
	// can write the note file outside the store lock the mutation takes.
	noteKey := ""
	if items, err := s.activeItems(); err == nil && id >= 0 && id < len(items) {
		noteKey = items[id].Task.Tag("note")
	}
	if notes != "" && noteKey == "" {
		noteKey = newNoteKey()
	}
	if noteKey != "" {
		if err := s.store.WriteNote(noteKey, notes); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	}

	ctx, person, proj := f("context"), f("person"), f("project")
	err = s.replaceActive(id, func(t *todotxt.Task) {
		t.Text = text
		if ctx != "" {
			t.RemoveContext(gtd.ContextInbox)
			t.AddContext(ctx)
		}
		if proj != "" {
			t.AddProject(proj)
		}
		if person != "" {
			t.AddContext(gtd.ContextWaiting)
		}
		t.SetTag("for", person)
		t.SetTag("due", f("due"))
		t.SetTag("t", f("threshold"))
		if notes != "" {
			t.SetTag("note", noteKey)
		} else {
			t.SetTag("note", "")
		}
	})
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	http.Redirect(w, r, safeBack(r.FormValue("back")), http.StatusSeeOther)
}

// newNoteKey mints a filename-safe, collision-resistant key for a fresh note.
func newNoteKey() string { return time.Now().Format("20060102-150405.000000000") }

// handleUndo rolls back the last mutation and returns to the page the request
// came from. A no-op (nothing to undo) is not an error — just redirect back.
func (s *server) handleUndo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if !s.csrfOK(r) {
		http.Error(w, "cross-origin request rejected", http.StatusForbidden)
		return
	}
	if err := s.store.Undo(); err != nil && !errors.Is(err, todotxt.ErrNothingToUndo) {
		http.Error(w, err.Error(), 500)
		return
	}
	http.Redirect(w, r, refererPath(r), http.StatusSeeOther)
}

// refererPath returns the local path+query of a same-origin Referer, so Undo
// (which carries no back field) returns to wherever its button was clicked. It
// falls back to the dashboard, and runs through safeBack so a crafted Referer
// can't become an open redirect.
func refererPath(r *http.Request) string {
	if u, err := url.Parse(r.Header.Get("Referer")); err == nil && u.Host == r.Host && u.Path != "" {
		p := u.Path
		if u.RawQuery != "" {
			p += "?" + u.RawQuery
		}
		return safeBack(p)
	}
	return "/"
}

// --- JSON API (CLI) ---------------------------------------------------------

type apiTask struct {
	ID       int      `json:"id"`
	Text     string   `json:"text"`
	Done     bool     `json:"done"`
	Contexts []string `json:"contexts,omitempty"`
	Projects []string `json:"projects,omitempty"`
	Due      string   `json:"due,omitempty"`
}

func toAPI(items []gtd.Item) []apiTask {
	out := make([]apiTask, len(items))
	for i, it := range items {
		out[i] = apiTask{
			ID:       it.ID,
			Text:     it.Task.Text,
			Done:     it.Task.Done,
			Contexts: it.Task.Contexts(),
			Projects: it.Task.Projects(),
			Due:      it.Task.Tag("due"),
		}
	}
	return out
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (s *server) apiTasks(w http.ResponseWriter, r *http.Request) {
	items, err := s.activeItems()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	t := today()
	q := r.URL.Query()
	var out []gtd.Item
	switch q.Get("view") {
	case "inbox":
		out = gtd.Inbox(items)
	case "waiting":
		out = gtd.Waiting(items)
	case "next", "":
		out = gtd.NextActions(items, t, q.Get("context"), q.Get("project"))
	case "all":
		out = items
	case "done":
		done, err := s.store.Read(todotxt.DoneFile)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		out = gtd.Items(done)
		reverseItems(out) // newest-first, matching the web Done screen
	default:
		http.Error(w, "unknown view", 400)
		return
	}
	writeJSON(w, toAPI(out))
}

func (s *server) apiCapture(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !s.csrfOK(r) {
		http.Error(w, "POST with X-GTD-Client required", http.StatusForbidden)
		return
	}
	var body struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad body", 400)
		return
	}
	if strings.TrimSpace(body.Text) == "" {
		http.Error(w, "empty text", 400)
		return
	}
	if err := s.capture(body.Text); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (s *server) apiDone(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !s.csrfOK(r) {
		http.Error(w, "POST with X-GTD-Client required", http.StatusForbidden)
		return
	}
	var body struct {
		ID int `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad body", 400)
		return
	}
	if err := s.completeActive(body.ID); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) apiEdit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !s.csrfOK(r) {
		http.Error(w, "POST with X-GTD-Client required", http.StatusForbidden)
		return
	}
	var body struct {
		ID   int    `json:"id"`
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad body", 400)
		return
	}
	text := normalizeText(body.Text)
	if text == "" {
		http.Error(w, "empty text", 400)
		return
	}
	if err := s.editActive(body.ID, text); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) apiRestore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !s.csrfOK(r) {
		http.Error(w, "POST with X-GTD-Client required", http.StatusForbidden)
		return
	}
	var body struct {
		ID int `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad body", 400)
		return
	}
	if err := s.restoreDone(body.ID); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) apiUndo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !s.csrfOK(r) {
		http.Error(w, "POST with X-GTD-Client required", http.StatusForbidden)
		return
	}
	if err := s.store.Undo(); err != nil {
		if errors.Is(err, todotxt.ErrNothingToUndo) {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), 500)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
