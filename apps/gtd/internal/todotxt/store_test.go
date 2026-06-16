package todotxt

import (
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
	err = s.Transfer(ActiveFile, 1, DoneFile, func(task Task) Task {
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

	if err := s.Transfer(ActiveFile, 9, DoneFile, nil); err == nil {
		t.Error("Transfer with out-of-range id should error")
	}
}

func TestUndoRollsBackLastMutation(t *testing.T) {
	dir := t.TempDir()
	s, _ := New(dir)

	if s.CanUndo() {
		t.Fatal("a fresh store should have nothing to undo")
	}
	if err := s.Undo(); err != ErrNothingToUndo {
		t.Fatalf("Undo on fresh store = %v, want ErrNothingToUndo", err)
	}

	// Append, then undo it: the file should be gone again (it didn't exist
	// before the append).
	if err := s.Append(ActiveFile, "buy milk @errands"); err != nil {
		t.Fatal(err)
	}
	if !s.CanUndo() {
		t.Fatal("CanUndo should be true after a mutation")
	}
	if err := s.Undo(); err != nil {
		t.Fatal(err)
	}
	if active, _ := s.Read(ActiveFile); len(active) != 0 {
		t.Errorf("after undoing the only append, active = %v, want empty", active)
	}
	if s.CanUndo() {
		t.Error("undo is single-level: CanUndo should be false after undoing")
	}

	// A cross-file Transfer must undo as one step, restoring BOTH files.
	s.Append(ActiveFile, "one @a")
	s.Append(ActiveFile, "two @b")
	if err := s.Transfer(ActiveFile, 0, DoneFile, func(x Task) Task { x.Done = true; return x }); err != nil {
		t.Fatal(err)
	}
	if err := s.Undo(); err != nil {
		t.Fatal(err)
	}
	active, _ := s.Read(ActiveFile)
	done, _ := s.Read(DoneFile)
	if len(active) != 2 || active[0].Text != "one @a" {
		t.Errorf("transfer-undo did not restore active: %v", active)
	}
	if len(done) != 0 {
		t.Errorf("transfer-undo did not roll back done.txt: %v", done)
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
