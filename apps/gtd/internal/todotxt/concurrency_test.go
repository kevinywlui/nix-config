package todotxt

import (
	"fmt"
	"sync"
	"testing"
)

// TestConcurrentAppendIntegrity hammers Append from many goroutines and asserts
// every line lands exactly once with no torn writes. Run under -race this also
// proves the store mutex covers the append path.
func TestConcurrentAppendIntegrity(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	const n = 200
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := s.Append(ActiveFile, fmt.Sprintf("task number %d @ctx", i)); err != nil {
				t.Errorf("append %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	tasks, err := s.Read(ActiveFile)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != n {
		t.Fatalf("got %d tasks, want %d (lost or torn writes)", len(tasks), n)
	}
	seen := map[string]bool{}
	for _, task := range tasks {
		if task.Text == "" {
			t.Fatal("found an empty/torn task line")
		}
		if seen[task.Text] {
			t.Fatalf("duplicate line: %q", task.Text)
		}
		seen[task.Text] = true
	}
}

// TestConcurrentTransferConserves runs many concurrent moves of the first task
// out of the active file and asserts the destination ends up with exactly the
// number moved — no double-move, no loss, no corruption — proving Transfer's
// read-validate-append-remove is a single atomic critical section.
func TestConcurrentTransferConserves(t *testing.T) {
	dir := t.TempDir()
	s, _ := New(dir)
	const n = 200
	for i := 0; i < n; i++ {
		if err := s.Append(ActiveFile, fmt.Sprintf("item %d @c", i)); err != nil {
			t.Fatal(err)
		}
	}

	var wg sync.WaitGroup
	for i := 0; i < n+20; i++ { // 20 extra racers that should harmlessly error out
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Always target index 0; once empty, Transfer returns an error we ignore.
			_ = s.Transfer(ActiveFile, 0, "", DoneFile, func(task Task) Task {
				task.Done = true
				return task
			})
		}()
	}
	wg.Wait()

	active, _ := s.Read(ActiveFile)
	done, _ := s.Read(DoneFile)
	if len(active) != 0 {
		t.Errorf("active = %d, want 0", len(active))
	}
	if len(done) != n {
		t.Errorf("done = %d, want %d (double-move or loss under concurrency)", len(done), n)
	}
	seen := map[string]bool{}
	for _, task := range done {
		if seen[task.Text] {
			t.Fatalf("task moved twice: %q", task.Text)
		}
		seen[task.Text] = true
	}
}
