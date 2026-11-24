// Package state records what cg-dev-auth changed on this machine, so it can
// later undo it, and keeps a backup of each file before its first edit.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/billgrant/cg-dev-auth/internal/fsutil"
)

// FileRecord describes a file cg-dev-auth has edited.
type FileRecord struct {
	// Existed is whether the file existed before the first edit.
	Existed bool `json:"existed"`
	// Backup is the path of the copy taken before the first edit; empty when
	// the file did not exist.
	Backup string `json:"backup,omitempty"`
	// Mode is the original file mode.
	Mode fs.FileMode `json:"mode,omitempty"`
	// BackedUpAt is when the backup was taken.
	BackedUpAt time.Time `json:"backed_up_at"`
}

// State is the persisted record.
type State struct {
	Files map[string]*FileRecord `json:"files"`
	// Prev holds settings that cg-dev-auth overwrote, keyed by
	// "<ecosystem>.<setting>", so they can be put back. A key present with a
	// nil value means the setting was absent.
	Prev map[string]*string `json:"prev,omitempty"`
	// Disabled lists ecosystems switched off with `cg-dev-auth off`.
	Disabled []string `json:"disabled,omitempty"`
}

// Store reads and writes the state in Dir.
type Store struct {
	Dir   string
	state *State
}

// Open loads the state in dir, or starts an empty one.
func Open(dir string) (*Store, error) {
	s := &Store{Dir: dir, state: &State{Files: map[string]*FileRecord{}, Prev: map[string]*string{}}}
	data, exists, err := fsutil.ReadFileIfExists(s.Path())
	if err != nil {
		return nil, err
	}
	if exists {
		if err := json.Unmarshal(data, s.state); err != nil {
			return nil, fmt.Errorf("parse %s: %w", s.Path(), err)
		}
		if s.state.Files == nil {
			s.state.Files = map[string]*FileRecord{}
		}
		if s.state.Prev == nil {
			s.state.Prev = map[string]*string{}
		}
	}
	return s, nil
}

// Path is the state file.
func (s *Store) Path() string { return filepath.Join(s.Dir, "state.json") }

// State returns the in-memory state for reading and modification; call Save
// to persist it.
func (s *Store) State() *State { return s.state }

// Save persists the state.
func (s *Store) Save() error {
	data, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(s.Path(), append(data, '\n'), 0o600)
}

// EnsureBackup copies path into the backups directory the first time
// cg-dev-auth is about to edit it. Later calls are no-ops. The caller saves
// the state afterwards.
func (s *Store) EnsureBackup(path string) error {
	if _, ok := s.state.Files[path]; ok {
		return nil
	}
	rec := &FileRecord{BackedUpAt: time.Now().UTC()}
	info, err := os.Stat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		rec.Existed = false
	case err != nil:
		return err
	default:
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		name := strings.TrimPrefix(strings.ReplaceAll(path, string(filepath.Separator), "_"), "_")
		rec.Backup = filepath.Join(s.Dir, "backups", fmt.Sprintf("%s.%s", name, rec.BackedUpAt.Format("20060102T150405Z")))
		if err := fsutil.WriteFileAtomic(rec.Backup, data, 0o600); err != nil {
			return fmt.Errorf("back up %s: %w", path, err)
		}
		rec.Existed = true
		rec.Mode = info.Mode().Perm()
	}
	s.state.Files[path] = rec
	return nil
}

// RememberPrev records the previous value of key the first time it is
// overwritten; later calls are no-ops. A nil value means it was absent.
func (s *Store) RememberPrev(key string, value *string) {
	if _, ok := s.state.Prev[key]; ok {
		return
	}
	s.state.Prev[key] = value
}

// Prev returns a previously remembered value and whether one was recorded.
func (s *Store) Prev(key string) (value *string, ok bool) {
	v, ok := s.state.Prev[key]
	return v, ok
}
