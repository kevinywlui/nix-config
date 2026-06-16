package todotxt

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// File names within the store directory.
const (
	ActiveFile    = "todo.txt"
	DoneFile      = "done.txt"
	SomedayFile   = "someday.txt"
	ReferenceFile = "reference.txt"
	backupDir     = "backups"
)

// keepBackups bounds the timestamped backups retained per file. Older ones are
// pruned on each write so a long-lived list cannot grow the dir unbounded.
const keepBackups = 50

// Store is a file-backed collection of todo.txt files in a single directory.
// All mutations are serialised through mu and written atomically (temp file +
// rename) with a timestamped backup of the prior content, so a crash mid-write
// can never corrupt the canonical file and an accidental bulk edit is
// recoverable.
type Store struct {
	dir string
	mu  sync.Mutex
}

// New returns a Store rooted at dir. The directory and its backups/ subdir are
// created if absent.
func New(dir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, backupDir), 0o750); err != nil {
		return nil, err
	}
	return &Store{dir: dir}, nil
}

// Read loads and parses the named file. A missing file is treated as empty.
func (s *Store) Read(name string) ([]Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readLocked(name)
}

func (s *Store) readLocked(name string) ([]Task, error) {
	data, err := os.ReadFile(filepath.Join(s.dir, name))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var tasks []Task
	for _, line := range strings.Split(string(data), "\n") {
		if t, ok := Parse(line); ok {
			tasks = append(tasks, t)
		}
	}
	return tasks, nil
}

// Mutate loads the named file, hands the slice to fn, and atomically writes the
// returned slice back. fn runs under the store lock; it must not call other
// Store methods (which would deadlock).
func (s *Store) Mutate(name string, fn func([]Task) ([]Task, error)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tasks, err := s.readLocked(name)
	if err != nil {
		return err
	}
	out, err := fn(tasks)
	if err != nil {
		return err
	}
	return s.writeLocked(name, out)
}

// Append adds a single line to the named file without rewriting the rest. It
// uses an O_APPEND write so lines other tools maintain in done.txt/someday.txt
// are preserved byte-for-byte (only the file's existing content is trusted to
// already end in a newline, which our own writes guarantee).
func (s *Store) Append(name, line string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := Parse(line)
	if !ok {
		return fmt.Errorf("refusing to append blank line to %s", name)
	}
	return s.appendLineLocked(name, t)
}

// Transfer atomically moves the task at id from srcName to destName, applying
// transform (if non-nil) to the task as it moves. The whole read-validate-
// append-remove sequence runs under a single lock acquisition, so concurrent
// requests can never shift indices between the check and the act.
func (s *Store) Transfer(srcName string, id int, destName string, transform func(Task) Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	src, err := s.readLocked(srcName)
	if err != nil {
		return err
	}
	if id < 0 || id >= len(src) {
		return fmt.Errorf("task %d not found in %s", id, srcName)
	}
	moved := src[id]
	if transform != nil {
		moved = transform(moved)
	}
	if err := s.appendLineLocked(destName, moved); err != nil {
		return err
	}
	return s.writeLocked(srcName, append(src[:id:id], src[id+1:]...))
}

// appendLineLocked appends one serialised task to a file. Caller holds the lock.
func (s *Store) appendLineLocked(name string, t Task) error {
	f, err := os.OpenFile(filepath.Join(s.dir, name), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(t.String() + "\n")
	return err
}

func (s *Store) writeLocked(name string, tasks []Task) error {
	path := filepath.Join(s.dir, name)

	// Back up the current content before overwriting.
	if cur, err := os.ReadFile(path); err == nil {
		stamp := time.Now().Format("20060102-150405.000")
		bpath := filepath.Join(s.dir, backupDir, name+"."+stamp)
		_ = os.WriteFile(bpath, cur, 0o640)
		s.pruneBackups(name)
	}

	var b strings.Builder
	for _, t := range tasks {
		b.WriteString(t.String())
		b.WriteByte('\n')
	}

	tmp, err := os.CreateTemp(s.dir, ".tmp-"+name+"-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeds
	if _, err := tmp.WriteString(b.String()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o640); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// pruneBackups keeps only the newest keepBackups snapshots of name. Caller
// holds the lock.
func (s *Store) pruneBackups(name string) {
	entries, err := os.ReadDir(filepath.Join(s.dir, backupDir))
	if err != nil {
		return
	}
	var mine []string
	prefix := name + "."
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), prefix) {
			mine = append(mine, e.Name())
		}
	}
	if len(mine) <= keepBackups {
		return
	}
	sort.Strings(mine) // timestamp format sorts chronologically
	for _, old := range mine[:len(mine)-keepBackups] {
		_ = os.Remove(filepath.Join(s.dir, backupDir, old))
	}
}
