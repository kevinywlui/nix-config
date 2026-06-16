package todotxt

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
	notesDir      = "notes"
)

// noteKeyRe constrains a note key to a filename-safe shape, so a key read from
// a (user-editable) task tag can't escape the notes directory via path
// traversal.
var noteKeyRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// managedFiles are the files Undo snapshots and restores as one set, so a single
// undo rolls back a mutation that touched more than one file (e.g. a Transfer
// that moved a task from todo.txt to done.txt).
var managedFiles = []string{ActiveFile, DoneFile, SomedayFile, ReferenceFile}

// ErrNothingToUndo is returned by Undo when no mutation has been recorded yet.
var ErrNothingToUndo = errors.New("nothing to undo")

// keepBackups bounds the timestamped backups retained per file. Older ones are
// pruned on each write so a long-lived list cannot grow the dir unbounded.
const keepBackups = 50

// Store is a file-backed collection of todo.txt files in a single directory.
// All mutations are serialised through mu and written atomically (temp file +
// rename) with a timestamped backup of the prior content, so a crash mid-write
// can never corrupt the canonical file and an accidental bulk edit is
// recoverable.
type Store struct {
	dir  string
	mu   sync.Mutex
	undo *snapshot // content of every managed file just before the last mutation
}

// snapshot is the verbatim content of every managed file captured immediately
// before a mutation, so Undo can restore the whole set in one step.
type snapshot struct {
	files map[string]fileState
}

// fileState records one file's bytes and whether it existed at snapshot time, so
// Undo can recreate a deleted file or remove one that the mutation created.
type fileState struct {
	data    []byte
	existed bool
}

// New returns a Store rooted at dir. The directory and its backups/ subdir are
// created if absent.
func New(dir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, backupDir), 0o750); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(dir, notesDir), 0o750); err != nil {
		return nil, err
	}
	return &Store{dir: dir}, nil
}

// ReadNote returns the free-form note text attached under key (the value of a
// task's note: tag), or "" if there is none.
func (s *Store) ReadNote(key string) (string, error) {
	if !noteKeyRe.MatchString(key) {
		return "", fmt.Errorf("invalid note key %q", key)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(filepath.Join(s.dir, notesDir, key+".txt"))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(data), nil
}

// WriteNote stores free-form note text under key, replacing any existing note.
// Empty content deletes the note. Notes live in their own files (not the
// todo.txt line), so they may contain newlines freely.
func (s *Store) WriteNote(key, content string) error {
	if !noteKeyRe.MatchString(key) {
		return fmt.Errorf("invalid note key %q", key)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path := filepath.Join(s.dir, notesDir, key+".txt")
	if content == "" {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	tmp, err := os.CreateTemp(filepath.Join(s.dir, notesDir), ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.WriteString(content); err != nil {
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

// Read loads and parses the named file. A missing file is treated as empty.
func (s *Store) Read(name string) ([]Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readLocked(name)
}

// Raw returns the verbatim on-disk bytes of the named file (nil if missing),
// for a read-only "show me the actual todo.txt" view. It does not parse, so
// what the caller sees is exactly what any other todo.txt tool would read.
func (s *Store) Raw(name string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(filepath.Join(s.dir, name))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return data, nil
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
	snap := s.takeSnapshotLocked()
	if err := s.writeLocked(name, out); err != nil {
		return err
	}
	s.undo = snap
	return nil
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
	snap := s.takeSnapshotLocked()
	if err := s.appendLineLocked(name, t); err != nil {
		return err
	}
	s.undo = snap
	return nil
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
	snap := s.takeSnapshotLocked()
	if err := s.appendLineLocked(destName, moved); err != nil {
		return err
	}
	if err := s.writeLocked(srcName, append(src[:id:id], src[id+1:]...)); err != nil {
		return err
	}
	s.undo = snap
	return nil
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
	var b strings.Builder
	for _, t := range tasks {
		b.WriteString(t.String())
		b.WriteByte('\n')
	}
	return s.rawWriteLocked(name, []byte(b.String()))
}

// rawWriteLocked overwrites name with data, backing up the prior content first
// and using a temp file + rename so a crash mid-write can't corrupt the file.
// Caller holds the lock.
func (s *Store) rawWriteLocked(name string, data []byte) error {
	path := filepath.Join(s.dir, name)

	// Back up the current content before overwriting.
	if cur, err := os.ReadFile(path); err == nil {
		stamp := time.Now().Format("20060102-150405.000")
		bpath := filepath.Join(s.dir, backupDir, name+"."+stamp)
		_ = os.WriteFile(bpath, cur, 0o640)
		s.pruneBackups(name)
	}

	tmp, err := os.CreateTemp(s.dir, ".tmp-"+name+"-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeds
	if _, err := tmp.Write(data); err != nil {
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

// takeSnapshotLocked captures the current bytes of every managed file. Caller
// holds the lock. The result is assigned to s.undo only after the mutation that
// follows succeeds, so a failed write leaves the prior undo point intact.
func (s *Store) takeSnapshotLocked() *snapshot {
	snap := &snapshot{files: make(map[string]fileState, len(managedFiles))}
	for _, name := range managedFiles {
		data, err := os.ReadFile(filepath.Join(s.dir, name))
		if err != nil {
			snap.files[name] = fileState{existed: false}
			continue
		}
		snap.files[name] = fileState{data: data, existed: true}
	}
	return snap
}

// CanUndo reports whether a mutation is available to roll back.
func (s *Store) CanUndo() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.undo != nil
}

// Undo restores every managed file to its content from just before the last
// mutation, then clears the undo point — undo is single-level and not itself
// undoable. It returns ErrNothingToUndo if no mutation has been recorded. The
// pre-undo state is itself backed up (rawWriteLocked snapshots before
// overwriting), so even an unwanted undo is recoverable from backups/.
func (s *Store) Undo() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.undo == nil {
		return ErrNothingToUndo
	}
	for name, fs := range s.undo.files {
		if !fs.existed {
			if err := os.Remove(filepath.Join(s.dir, name)); err != nil && !os.IsNotExist(err) {
				return err
			}
			continue
		}
		if err := s.rawWriteLocked(name, fs.data); err != nil {
			return err
		}
	}
	s.undo = nil
	return nil
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
