package pluginhost

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

var (
	// ErrDesiredStateNotFound is returned when no desired state file exists.
	ErrDesiredStateNotFound = errors.New("plugin desired state not found")
	// ErrCorruptDesiredState is returned when a desired state file is invalid.
	ErrCorruptDesiredState = errors.New("corrupt plugin desired state")
	// ErrInvalidPluginNo is returned when a plugin number is not filename-safe.
	ErrInvalidPluginNo = errors.New("invalid plugin no")
)

var pluginNoPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

var syncParentDirAfterRename = syncDir

// DesiredState is the node-local desired plugin state persisted on disk.
type DesiredState struct {
	// No is the filename-safe plugin number.
	No string `json:"no"`
	// Config stores plugin configuration bytes as JSON.
	Config json.RawMessage `json:"config,omitempty"`
	// Enabled controls whether this node should run the plugin.
	Enabled bool `json:"enabled"`
	// CreatedAt records when the desired state was first created.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt records when the desired state was last changed.
	UpdatedAt time.Time `json:"updated_at"`
}

// Store persists node-local desired plugin state as one JSON file per plugin.
type Store struct {
	dir string
}

// NewStore creates a desired-state store rooted at dir.
func NewStore(dir string) *Store {
	return &Store{dir: dir}
}

// Save writes state atomically through a temporary file and rename.
func (s *Store) Save(state DesiredState) error {
	if err := validatePluginNo(state.No); err != nil {
		return err
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("create plugin state dir: %w", err)
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal plugin desired state %q: %w", state.No, err)
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(s.dir, "."+state.No+"-*.tmp")
	if err != nil {
		return fmt.Errorf("create plugin desired state temp file %q: %w", state.No, err)
	}
	tmpName := tmp.Name()
	closed := false
	defer func() {
		if !closed {
			_ = tmp.Close()
		}
		_ = os.Remove(tmpName)
	}()

	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("write plugin desired state temp file %q: %w", state.No, err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync plugin desired state temp file %q: %w", state.No, err)
	}
	if err := tmp.Close(); err != nil {
		closed = true
		return fmt.Errorf("close plugin desired state temp file %q: %w", state.No, err)
	}
	closed = true

	if err := os.Rename(tmpName, s.pathFor(state.No)); err != nil {
		return fmt.Errorf("rename plugin desired state file %q: %w", state.No, err)
	}
	if err := syncParentDirAfterRename(s.dir); err != nil {
		return fmt.Errorf("sync plugin desired state dir %q: %w", s.dir, err)
	}
	return nil
}

// Load reads desired state for no. Missing files return ErrDesiredStateNotFound.
func (s *Store) Load(no string) (DesiredState, error) {
	if err := validatePluginNo(no); err != nil {
		return DesiredState{}, err
	}

	data, err := os.ReadFile(s.pathFor(no))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return DesiredState{}, fmt.Errorf("%w: %s", ErrDesiredStateNotFound, no)
		}
		return DesiredState{}, fmt.Errorf("read plugin desired state %q: %w", no, err)
	}

	var state DesiredState
	if err := json.Unmarshal(data, &state); err != nil {
		return DesiredState{}, fmt.Errorf("%w: decode %q: %w", ErrCorruptDesiredState, no, err)
	}
	if err := validatePluginNo(state.No); err != nil {
		return DesiredState{}, fmt.Errorf("%w: decoded plugin no %q: %w", ErrCorruptDesiredState, state.No, err)
	}
	if state.No != no {
		return DesiredState{}, fmt.Errorf("%w: decoded plugin no %q does not match requested %q", ErrCorruptDesiredState, state.No, no)
	}
	return state, nil
}

// Delete removes desired state for no. Missing files are treated as already deleted.
func (s *Store) Delete(no string) error {
	if err := validatePluginNo(no); err != nil {
		return err
	}
	if err := os.Remove(s.pathFor(no)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("delete plugin desired state %q: %w", no, err)
	}
	if err := syncParentDirAfterRename(s.dir); err != nil {
		return fmt.Errorf("sync plugin desired state dir %q: %w", s.dir, err)
	}
	return nil
}

func (s *Store) pathFor(no string) string {
	return filepath.Join(s.dir, no+".json")
}

func validatePluginNo(no string) error {
	if no == "." || no == ".." || !pluginNoPattern.MatchString(no) {
		return fmt.Errorf("%w: %q", ErrInvalidPluginNo, no)
	}
	return nil
}

func syncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
