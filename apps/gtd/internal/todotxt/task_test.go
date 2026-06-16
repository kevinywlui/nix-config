package todotxt

import (
	"strings"
	"testing"
)

// DisplayText hides the app's internal pointer tags (note:, id:, after:) while
// keeping the real description, contexts and projects.
func TestDisplayTextHidesPlumbing(t *testing.T) {
	task, _ := Parse("order cabinets +Reno after:k1 id:k2 note:n1 @calls")
	got := task.DisplayText()
	for _, leak := range []string{"note:", "id:", "after:", "k1", "k2", "n1"} {
		if strings.Contains(got, leak) {
			t.Errorf("DisplayText leaked %q: %q", leak, got)
		}
	}
	for _, keep := range []string{"order cabinets", "+Reno", "@calls"} {
		if !strings.Contains(got, keep) {
			t.Errorf("DisplayText dropped %q: %q", keep, got)
		}
	}
}

func TestParseRoundTrip(t *testing.T) {
	cases := []string{
		"Call the dentist @calls",
		"(A) Email Bob about the report +Q3Report @computer due:2026-06-20",
		"x 2026-06-15 2026-06-10 Buy milk @errands",
		"Defer this t:2026-07-01 @home",
		"x Pay rent",
		"Plain task with no tags",
	}
	for _, in := range cases {
		got, ok := Parse(in)
		if !ok {
			t.Fatalf("Parse(%q) returned ok=false", in)
		}
		if out := got.String(); out != in {
			t.Errorf("round trip: Parse(%q).String() = %q", in, out)
		}
	}
}

func TestParseBlank(t *testing.T) {
	if _, ok := Parse("   "); ok {
		t.Error("blank line should parse as ok=false")
	}
}

func TestParseFields(t *testing.T) {
	in := "(B) 2026-06-01 Review notes +bigproj @computer @home for:alice t:2026-06-05 due:2026-06-10"
	task, ok := Parse(in)
	if !ok {
		t.Fatal("Parse failed")
	}
	if task.Priority != "B" {
		t.Errorf("Priority = %q, want B", task.Priority)
	}
	if task.Created != "2026-06-01" {
		t.Errorf("Created = %q, want 2026-06-01", task.Created)
	}
	if got := task.Contexts(); len(got) != 2 || got[0] != "computer" || got[1] != "home" {
		t.Errorf("Contexts = %v, want [computer home]", got)
	}
	if got := task.Projects(); len(got) != 1 || got[0] != "bigproj" {
		t.Errorf("Projects = %v, want [bigproj]", got)
	}
	if task.Tag("t") != "2026-06-05" {
		t.Errorf("Tag(t) = %q, want 2026-06-05", task.Tag("t"))
	}
	if task.Tag("due") != "2026-06-10" {
		t.Errorf("Tag(due) = %q, want 2026-06-10", task.Tag("due"))
	}
	if task.Tag("for") != "alice" {
		t.Errorf("Tag(for) = %q, want alice", task.Tag("for"))
	}
}

func TestSetTagReplaceAndRemove(t *testing.T) {
	task, _ := Parse("Do thing @home due:2026-01-01")
	task.SetTag("due", "2026-02-02")
	if task.Tag("due") != "2026-02-02" {
		t.Errorf("after replace, due = %q", task.Tag("due"))
	}
	task.SetTag("due", "")
	if task.Tag("due") != "" {
		t.Errorf("after remove, due = %q, want empty", task.Tag("due"))
	}
	// Unknown tags and context must survive the edits (round-trip safety).
	if !task.HasContext("home") {
		t.Error("@home lost during SetTag edits")
	}
}

func TestSetTagSanitizesValue(t *testing.T) {
	task, _ := Parse("Chase invoice @waiting")
	// A value with whitespace (incl. a newline) must not break the file or the
	// tag's own readability.
	task.SetTag("for", "Alice Smith")
	if task.Tag("for") != "Alice_Smith" {
		t.Errorf("Tag(for) = %q, want Alice_Smith", task.Tag("for"))
	}
	task.SetTag("note", "a:b\nx forged line")
	if got := task.String(); containsNewline(got) {
		t.Errorf("serialised task must not contain a newline: %q", got)
	}
	// Replacing the value should still find the existing tag, not duplicate it.
	task.SetTag("for", "bob")
	if n := countTag(task, "for"); n != 1 {
		t.Errorf("for: tag appears %d times, want 1", n)
	}
}

func containsNewline(s string) bool {
	for _, r := range s {
		if r == '\n' {
			return true
		}
	}
	return false
}

func countTag(t Task, key string) int {
	n := 0
	for _, f := range t.fields() {
		if m := tagRe.FindStringSubmatch(f); m != nil && m[1] == key {
			n++
		}
	}
	return n
}

func TestAddContextProjectIdempotent(t *testing.T) {
	task, _ := Parse("Write spec")
	task.AddContext("computer")
	task.AddContext("computer")
	task.AddProject("spec")
	task.AddProject("spec")
	if got := task.Contexts(); len(got) != 1 {
		t.Errorf("Contexts = %v, want one entry", got)
	}
	if got := task.Projects(); len(got) != 1 {
		t.Errorf("Projects = %v, want one entry", got)
	}
}
