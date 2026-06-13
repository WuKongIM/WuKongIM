package contextcmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	contextsDirName = "contexts"
	currentFileName = "current_context"
)

// Store persists named contexts on the local filesystem.
type Store struct {
	dir string
}

// DefaultStoreDir returns the default user-level wkcli context directory.
func DefaultStoreDir() string {
	configDir, err := os.UserConfigDir()
	if err != nil || configDir == "" {
		return filepath.Join(".wkcli")
	}
	return filepath.Join(configDir, "wukongim", "wkcli")
}

// NewStore creates a context store rooted at dir.
func NewStore(dir string) *Store {
	return &Store{dir: dir}
}

// Save writes or replaces one context.
func (s *Store) Save(ctx Context) error {
	if err := validateContext(ctx); err != nil {
		return err
	}
	if err := os.MkdirAll(s.contextsDir(), 0o700); err != nil {
		return fmt.Errorf("create contexts directory: %w", err)
	}
	data, err := json.MarshalIndent(ctx, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal context: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(s.contextPath(ctx.Name), data, 0o600); err != nil {
		return fmt.Errorf("write context %q: %w", ctx.Name, err)
	}
	return nil
}

// Load reads a context by name.
func (s *Store) Load(name string) (Context, error) {
	if err := validateContextName(name); err != nil {
		return Context{}, err
	}
	data, err := os.ReadFile(s.contextPath(name))
	if errors.Is(err, os.ErrNotExist) {
		return Context{}, fmt.Errorf("context %q not found", name)
	}
	if err != nil {
		return Context{}, fmt.Errorf("read context %q: %w", name, err)
	}
	var ctx Context
	if err := json.Unmarshal(data, &ctx); err != nil {
		return Context{}, fmt.Errorf("decode context %q: %w", name, err)
	}
	if err := validateContext(ctx); err != nil {
		return Context{}, fmt.Errorf("invalid context %q: %w", name, err)
	}
	if ctx.Name != name {
		return Context{}, fmt.Errorf("context file %q contains name %q", name, ctx.Name)
	}
	return ctx, nil
}

// List returns all saved contexts sorted by name.
func (s *Store) List() ([]Context, error) {
	entries, err := os.ReadDir(s.contextsDir())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list contexts: %w", err)
	}
	contexts := make([]Context, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".json")
		ctx, err := s.Load(name)
		if err != nil {
			return nil, err
		}
		contexts = append(contexts, ctx)
	}
	sort.Slice(contexts, func(i, j int) bool {
		return contexts[i].Name < contexts[j].Name
	})
	return contexts, nil
}

// Select sets name as the current context after confirming it exists.
func (s *Store) Select(name string) error {
	if _, err := s.Load(name); err != nil {
		return err
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("create context directory: %w", err)
	}
	if err := os.WriteFile(s.currentPath(), []byte(name+"\n"), 0o600); err != nil {
		return fmt.Errorf("write current context: %w", err)
	}
	return nil
}

// Current returns the currently selected context name, or an empty string.
func (s *Store) Current() (string, error) {
	data, err := os.ReadFile(s.currentPath())
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read current context: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

// Remove deletes a context and clears current selection when it was selected.
func (s *Store) Remove(name string) (bool, error) {
	if _, err := s.Load(name); err != nil {
		return false, err
	}
	if err := os.Remove(s.contextPath(name)); err != nil {
		return false, fmt.Errorf("remove context %q: %w", name, err)
	}
	current, err := s.Current()
	if err != nil {
		return false, err
	}
	if current != name {
		return false, nil
	}
	if err := os.Remove(s.currentPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("clear current context: %w", err)
	}
	return true, nil
}

func (s *Store) contextsDir() string {
	return filepath.Join(s.dir, contextsDirName)
}

func (s *Store) contextPath(name string) string {
	return filepath.Join(s.contextsDir(), name+".json")
}

func (s *Store) currentPath() string {
	return filepath.Join(s.dir, currentFileName)
}
