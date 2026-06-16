package main

import (
	"bytes"
	"embed"
	"encoding/json"
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
	s.mux.HandleFunc("/done", s.handleDone)

	s.mux.Handle("/static/", http.FileServer(http.FS(staticFS)))

	s.mux.HandleFunc("/api/tasks", s.apiTasks)
	s.mux.HandleFunc("/api/capture", s.apiCapture)
	s.mux.HandleFunc("/api/done", s.apiDone)
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

func (s *server) render(w http.ResponseWriter, name string, data any) {
	s.renderStatus(w, http.StatusOK, name, data)
}

// renderStatus renders a template with an explicit status code. The status is
// written after the body buffers successfully, so a template error still yields
// a clean 500 rather than a half-written page with the wrong code.
func (s *server) renderStatus(w http.ResponseWriter, code int, name string, data any) {
	var buf bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	buf.WriteTo(w)
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

// --- HTML handlers ----------------------------------------------------------

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
		if p.Actions == 0 {
			stalled++
		}
	}
	c := counts{
		Inbox:    len(gtd.Inbox(items)),
		Next:     len(gtd.NextActions(items, t, "")),
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
		if text != "" {
			if err := s.capture(text); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
		}
		http.Redirect(w, r, "/capture?ok=1", http.StatusSeeOther)
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
	s.render(w, "process", processData(inbox, "", ""))
}

// processData builds the Clarify view model for the first inbox item. err and
// decision are non-empty only on a failed submission we're re-rendering: err is
// shown as an inline banner and decision re-selects the radio the user picked,
// so a missed field (e.g. a forgotten context) reopens the right panel instead
// of dumping them on a plain error page.
func processData(inbox []gtd.Item, errMsg, decision string) map[string]any {
	it := inbox[0]
	return map[string]any{
		"Item":      it,
		"Text":      strings.TrimSpace(strings.ReplaceAll(it.Task.Text, "@inbox", "")),
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
	s.renderStatus(w, http.StatusBadRequest, "process", processData(inbox, msg, decision))
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
	s.render(w, "next", map[string]any{
		"Items":   gtd.NextActions(items, today(), ctx),
		"Context": ctx,
	})
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

func (s *server) handleDone(w http.ResponseWriter, r *http.Request) {
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
	if err := s.completeActive(id); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	http.Redirect(w, r, safeBack(r.FormValue("back")), http.StatusSeeOther)
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
		out = gtd.NextActions(items, t, q.Get("context"))
	case "all":
		out = items
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
