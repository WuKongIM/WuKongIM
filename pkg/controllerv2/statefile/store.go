package statefile

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/state"
)

// Store atomically persists and loads one ControllerV2 cluster-state JSON file.
type Store struct {
	path           string
	afterTempWrite func() error
}

// Option customizes a Store.
type Option func(*Store)

// New creates a state file store rooted at path.
func New(path string, opts ...Option) *Store {
	s := &Store{path: path}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// WithAfterTempWriteHook runs hook after the temp file is fsynced and before rename.
func WithAfterTempWriteHook(hook func() error) Option {
	return func(s *Store) {
		s.afterTempWrite = hook
	}
}

// Path returns the main cluster-state file path.
func (s *Store) Path() string {
	return s.path
}

// Load reads and validates the main cluster-state file, ignoring any temp files.
func (s *Store) Load(ctx context.Context) (state.ClusterState, error) {
	if err := ctx.Err(); err != nil {
		return state.ClusterState{}, err
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return state.ClusterState{}, fmt.Errorf("statefile: read %s: %w", s.path, err)
	}
	if err := ctx.Err(); err != nil {
		return state.ClusterState{}, err
	}
	st, err := state.Decode(data)
	if err != nil {
		return state.ClusterState{}, err
	}
	return st, nil
}

// Save writes st to a same-directory temp file and atomically replaces the main file.
func (s *Store) Save(ctx context.Context, st state.ClusterState) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := state.Encode(st)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	dir := filepath.Dir(s.path)
	base := filepath.Base(s.path)
	tmp, err := os.CreateTemp(dir, base+".*.tmp")
	if err != nil {
		return fmt.Errorf("statefile: create temp for %s: %w", s.path, err)
	}
	tmpPath := tmp.Name()
	keepTemp := false
	defer func() {
		if !keepTemp {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("statefile: write temp %s: %w", tmpPath, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("statefile: fsync temp %s: %w", tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("statefile: close temp %s: %w", tmpPath, err)
	}
	if s.afterTempWrite != nil {
		if err := s.afterTempWrite(); err != nil {
			return fmt.Errorf("statefile: after temp write: %w", err)
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		return fmt.Errorf("statefile: rename %s to %s: %w", tmpPath, s.path, err)
	}
	keepTemp = true
	if err := syncDir(dir); err != nil {
		return err
	}
	return nil
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("statefile: open dir %s: %w", dir, err)
	}
	defer d.Close()
	if err := d.Sync(); err != nil {
		return fmt.Errorf("statefile: fsync dir %s: %w", dir, err)
	}
	return nil
}
