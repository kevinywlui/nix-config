package todotxt

import (
	"strings"
	"testing"
)

// FuzzParseRoundTrip asserts the parser/serialiser is stable: once a line has
// been normalised by one Parse→String, parsing and re-serialising it again must
// be a fixed point, and a serialised task must never contain a newline (which
// would split into a second task on the next read — a corruption vector).
func FuzzParseRoundTrip(f *testing.F) {
	seeds := []string{
		"Call the dentist @calls",
		"(A) Email Bob +Q3 @computer due:2026-06-20",
		"x 2026-06-15 2026-06-10 Buy milk @errands",
		"   ", "x", "(A)", "@only", "+proj", "key:val", "a:b:c",
		"weird\ttabs\there", "trailing spaces    ", "x 2026-13-99 bad date",
		"unicode ☃ café déjà @home", "(Z) z", "x (A) done with pri",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, line string) {
		task, ok := Parse(line)
		if !ok {
			return // blank line, nothing to check
		}
		s1 := task.String()
		if strings.ContainsAny(s1, "\n\r") {
			t.Fatalf("serialised task contains a newline: input=%q out=%q", line, s1)
		}
		// Re-parsing the serialised form must be a fixed point.
		task2, ok2 := Parse(s1)
		if !ok2 {
			t.Fatalf("re-parse of %q (from %q) returned ok=false", s1, line)
		}
		if s2 := task2.String(); s1 != s2 {
			t.Fatalf("not a fixed point: %q -> %q -> %q", line, s1, s2)
		}
	})
}

// FuzzSetTag asserts SetTag never corrupts a task: no newline ever lands in the
// serialised form, the value is always readable back, and a second SetTag with
// the same key replaces rather than duplicates.
func FuzzSetTag(f *testing.F) {
	f.Add("Do thing @home", "due", "2026-01-01")
	f.Add("Task +p", "for", "Alice Smith")
	f.Add("x done", "k", "v:with:colons and spaces\nand newline")
	f.Fuzz(func(t *testing.T, base, key, value string) {
		task, ok := Parse(base)
		if !ok {
			return
		}
		// A key containing whitespace or a colon isn't a valid tag key; skip
		// those — the handler only ever uses fixed keys (due, t, for, pri).
		if key == "" || strings.ContainsAny(key, " \t\n\r:") {
			return
		}
		task.SetTag(key, value)
		out := task.String()
		if strings.ContainsAny(out, "\n\r") {
			t.Fatalf("SetTag introduced a newline: key=%q value=%q out=%q", key, value, out)
		}
		got := task.Tag(key)
		// Either the value sanitised to empty (tag removed) or it must read back.
		if got != "" && strings.ContainsAny(got, " \t\n\r:") {
			t.Fatalf("Tag(%q) returned an unparseable value %q", key, got)
		}
		// Idempotent replace: setting the same key again keeps exactly one tag.
		task.SetTag(key, "x")
		if n := countTag(task, key); got != "" && n != 1 {
			t.Fatalf("key %q appears %d times after replace, want 1", key, n)
		}
	})
}
