package todotxt

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTransferIsAtomicMove(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range []string{"one @a", "two @b", "three @c"} {
		if err := s.Append(ActiveFile, l); err != nil {
			t.Fatal(err)
		}
	}
	// Move the middle task to done.txt, marking it done in the same step.
	err = s.Transfer(ActiveFile, 1, "", DoneFile, func(task Task) Task {
		task.Done = true
		task.Completed = "2026-06-15"
		return task
	})
	if err != nil {
		t.Fatal(err)
	}

	active, _ := s.Read(ActiveFile)
	if len(active) != 2 || active[0].Text != "one @a" || active[1].Text != "three @c" {
		t.Errorf("active after transfer = %v, want [one three]", active)
	}
	done, _ := s.Read(DoneFile)
	if len(done) != 1 || !done[0].Done || done[0].Text != "two @b" {
		t.Errorf("done after transfer = %v, want [done two]", done)
	}

	if err := s.Transfer(ActiveFile, 9, "", DoneFile, nil); err == nil {
		t.Error("Transfer with out-of-range id should error")
	}
}

func TestTransferStaleGuard(t *testing.T) {
	dir := t.TempDir()
	s, _ := New(dir)
	for _, l := range []string{"alpha @a", "beta @b"} {
		_ = s.Append(ActiveFile, l)
	}
	// want doesn't match the task at id 0 -> ErrChanged, nothing moves.
	err := s.Transfer(ActiveFile, 0, "stale @x", DoneFile, nil)
	if !errors.Is(err, ErrChanged) {
		t.Fatalf("got %v, want ErrChanged", err)
	}
	active, _ := s.Read(ActiveFile)
	done, _ := s.Read(DoneFile)
	if len(active) != 2 || len(done) != 0 {
		t.Fatalf("stale guard moved data: active=%d done=%d", len(active), len(done))
	}
	// Matching want -> succeeds.
	if err := s.Transfer(ActiveFile, 0, "alpha @a", DoneFile, nil); err != nil {
		t.Fatalf("matching want should succeed: %v", err)
	}
}

func TestAppendPreservesExistingContent(t *testing.T) {
	dir := t.TempDir()
	s, _ := New(dir)
	// Pre-seed done.txt with a line a foreign tool might have written, with
	// non-canonical spacing the parser would otherwise normalise on rewrite.
	foreign := "x 2026-01-01 already here  @misc\n"
	if err := os.WriteFile(filepath.Join(dir, DoneFile), []byte(foreign), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := s.Append(DoneFile, "x 2026-06-15 new one @a"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, DoneFile))
	if !strings.HasPrefix(string(got), foreign) {
		t.Errorf("Append rewrote existing content; file = %q", string(got))
	}
	if !strings.Contains(string(got), "new one @a") {
		t.Errorf("appended line missing; file = %q", string(got))
	}
}
