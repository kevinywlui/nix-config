// Package todotxt parses and serialises the todo.txt format
// (https://github.com/todotxt/todo.txt) with the GTD conventions this app
// layers on top.
//
// Design note: a Task keeps its description verbatim in Text (including the
// inline @context, +project and key:value tokens). Structured fields the
// format puts *before* the description — the `x` done marker, an optional
// (A) priority, and the completion/creation dates — are parsed out into typed
// fields; everything else stays in Text untouched. This is deliberate: it
// guarantees round-trip safety for tags this app doesn't understand, so a
// task edited by an external todo.txt tool is never silently clobbered.
package todotxt

import (
	"regexp"
	"strings"
)

var (
	dateRe     = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	priorityRe = regexp.MustCompile(`^\(([A-Z])\)$`)
	// A key:value tag: a non-empty key and value, neither containing a colon
	// or whitespace. Matches todo.txt's de-facto extension syntax (t:, due:).
	tagRe = regexp.MustCompile(`^([^\s:]+):([^\s:]+)$`)
)

// Task is a single todo.txt line. The zero value is a valid, empty,
// not-done task.
type Task struct {
	Done      bool   // leading `x`
	Priority  string // single uppercase letter A-Z, or "" for none
	Completed string // completion date YYYY-MM-DD, or ""
	Created   string // creation date YYYY-MM-DD, or ""
	Text      string // description, including inline @ctx +proj key:val tokens
}

// Parse turns one todo.txt line into a Task. A blank line yields an empty
// Task and ok=false so callers can skip it.
func Parse(line string) (Task, bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return Task{}, false
	}

	toks := strings.Fields(line)
	var t Task
	i := 0

	// `x ` done marker, optionally followed by a completion date.
	if toks[i] == "x" {
		t.Done = true
		i++
		if i < len(toks) && dateRe.MatchString(toks[i]) {
			t.Completed = toks[i]
			i++
		}
	} else if m := priorityRe.FindStringSubmatch(toks[i]); m != nil {
		// Priority only applies to not-done tasks in the canonical form.
		t.Priority = m[1]
		i++
	}

	// Optional creation date.
	if i < len(toks) && dateRe.MatchString(toks[i]) {
		t.Created = toks[i]
		i++
	}

	t.Text = strings.Join(toks[i:], " ")
	return t, true
}

// String serialises a Task back to a single todo.txt line, reproducing the
// canonical element order: x, completion-date, priority, creation-date, text.
func (t Task) String() string {
	var b strings.Builder
	sp := func(s string) {
		if s == "" {
			return
		}
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(s)
	}
	if t.Done {
		sp("x")
		sp(t.Completed)
	} else if t.Priority != "" {
		sp("(" + t.Priority + ")")
	}
	sp(t.Created)
	sp(t.Text)
	return b.String()
}

// fields returns the whitespace-split description tokens.
func (t Task) fields() []string { return strings.Fields(t.Text) }

// DisplayText is the description with the app's internal pointer tags removed —
// the note: key and the id:/after: dependency keys — so those opaque tokens
// aren't shown to the user. Their meaning is surfaced separately (a 📝 flag, a
// "blocked / after" line), not as raw text.
func (t Task) DisplayText() string {
	c := t
	c.SetTag("note", "")
	c.SetTag("id", "")
	c.SetTag("after", "")
	return c.Text
}

// Contexts returns the @context tokens (without the leading @), in order.
func (t Task) Contexts() []string {
	var out []string
	for _, f := range t.fields() {
		if len(f) > 1 && f[0] == '@' {
			out = append(out, f[1:])
		}
	}
	return out
}

// Projects returns the +project tokens (without the leading +), in order.
func (t Task) Projects() []string {
	var out []string
	for _, f := range t.fields() {
		if len(f) > 1 && f[0] == '+' {
			out = append(out, f[1:])
		}
	}
	return out
}

// Tag returns the value of a key:value tag, or "" if absent. The first
// occurrence wins.
func (t Task) Tag(key string) string {
	for _, f := range t.fields() {
		if m := tagRe.FindStringSubmatch(f); m != nil && m[1] == key {
			return m[2]
		}
	}
	return ""
}

// HasContext reports whether the task carries @name.
func (t Task) HasContext(name string) bool {
	for _, c := range t.Contexts() {
		if c == name {
			return true
		}
	}
	return false
}

// HasProject reports whether the task carries +name.
func (t Task) HasProject(name string) bool {
	for _, p := range t.Projects() {
		if p == name {
			return true
		}
	}
	return false
}

// RemoveProject strips +name from Text.
func (t *Task) RemoveProject(name string) {
	toks := t.fields()
	out := toks[:0]
	for _, f := range toks {
		if f == "+"+name {
			continue
		}
		out = append(out, f)
	}
	t.Text = strings.Join(out, " ")
}

// sanitizeTagValue forces a value into the single-token shape tagRe accepts: a
// tag whose value carried whitespace (incl. a newline — a store line-injection
// vector) or a colon would be unreadable by Tag and could corrupt the file, so
// internal whitespace collapses to "_" and colons are dropped.
func sanitizeTagValue(v string) string {
	v = strings.Join(strings.Fields(v), "_")
	return strings.ReplaceAll(v, ":", "")
}

// SetTag adds or replaces a key:value tag in Text. An empty value (or one that
// sanitises to empty) removes the tag.
func (t *Task) SetTag(key, value string) {
	value = sanitizeTagValue(value)
	toks := t.fields()
	out := toks[:0]
	replaced := false
	for _, f := range toks {
		if m := tagRe.FindStringSubmatch(f); m != nil && m[1] == key {
			if value != "" && !replaced {
				out = append(out, key+":"+value)
				replaced = true
			}
			continue
		}
		out = append(out, f)
	}
	if value != "" && !replaced {
		out = append(out, key+":"+value)
	}
	t.Text = strings.Join(out, " ")
}

// AddContext appends @name if not already present.
func (t *Task) AddContext(name string) {
	if name == "" || t.HasContext(name) {
		return
	}
	t.Text = strings.TrimSpace(t.Text + " @" + name)
}

// RemoveContext strips @name from Text.
func (t *Task) RemoveContext(name string) {
	toks := t.fields()
	out := toks[:0]
	for _, f := range toks {
		if f == "@"+name {
			continue
		}
		out = append(out, f)
	}
	t.Text = strings.Join(out, " ")
}

// AddProject appends +name if not already present.
func (t *Task) AddProject(name string) {
	if name == "" {
		return
	}
	for _, p := range t.Projects() {
		if p == name {
			return
		}
	}
	t.Text = strings.TrimSpace(t.Text + " +" + name)
}
