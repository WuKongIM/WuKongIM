package pluginhost

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStoreSaveLoadDesiredStateAtomically(t *testing.T) {
	store := NewStore(t.TempDir())
	created := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)
	updated := created.Add(time.Minute)
	state := DesiredState{
		No:        "plugin.alpha-1",
		Config:    json.RawMessage(`{"threshold":3}`),
		Enabled:   true,
		CreatedAt: created,
		UpdatedAt: updated,
	}

	require.NoError(t, store.Save(state))

	got, err := store.Load("plugin.alpha-1")
	require.NoError(t, err)
	require.Equal(t, state.No, got.No)
	require.JSONEq(t, string(state.Config), string(got.Config))
	require.True(t, got.Enabled)
	require.True(t, state.CreatedAt.Equal(got.CreatedAt))
	require.True(t, state.UpdatedAt.Equal(got.UpdatedAt))
}

func TestStoreLoadMissingReturnsNotFound(t *testing.T) {
	store := NewStore(t.TempDir())

	got, err := store.Load("missing")

	require.ErrorIs(t, err, ErrDesiredStateNotFound)
	require.Equal(t, DesiredState{}, got)
}

func TestStoreDeleteRemovesDesiredState(t *testing.T) {
	store := NewStore(t.TempDir())
	require.NoError(t, store.Save(DesiredState{No: "alpha", Enabled: true}))

	require.NoError(t, store.Delete("alpha"))
	require.NoError(t, store.Delete("alpha"))
	got, err := store.Load("alpha")
	require.ErrorIs(t, err, ErrDesiredStateNotFound)
	require.Equal(t, DesiredState{}, got)
}

func TestStoreLoadCorruptJSONReturnsError(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "broken.json"), []byte(`{"no":`), 0o600))
	store := NewStore(dir)

	got, err := store.Load("broken")

	require.Error(t, err)
	require.NotErrorIs(t, err, ErrDesiredStateNotFound)
	require.Equal(t, DesiredState{}, got)
}

func TestStoreLoadRejectsMismatchedDecodedPluginNo(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "expected.json"), []byte(`{"no":"other","enabled":true}`), 0o600))
	store := NewStore(dir)

	got, err := store.Load("expected")

	require.ErrorIs(t, err, ErrCorruptDesiredState)
	require.Equal(t, DesiredState{}, got)
}

func TestStoreLoadRejectsInvalidDecodedPluginNo(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "expected.json"), []byte(`{"no":"../escape","enabled":true}`), 0o600))
	store := NewStore(dir)

	got, err := store.Load("expected")

	require.ErrorIs(t, err, ErrCorruptDesiredState)
	require.Equal(t, DesiredState{}, got)
}

func TestStoreSaveSyncsParentDirectoryAfterRename(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	var syncedDir string
	originalSync := syncParentDirAfterRename
	syncParentDirAfterRename = func(dir string) error {
		syncedDir = dir
		return nil
	}
	t.Cleanup(func() {
		syncParentDirAfterRename = originalSync
	})

	err := store.Save(DesiredState{No: "sync-test", Enabled: true})

	require.NoError(t, err)
	require.Equal(t, dir, syncedDir)
}

func TestStoreRejectsUnsafePluginNo(t *testing.T) {
	store := NewStore(t.TempDir())

	for _, no := range []string{"", "../escape", "nested/plugin", `back\\slash`, "white space"} {
		t.Run(no, func(t *testing.T) {
			require.ErrorIs(t, store.Save(DesiredState{No: no}), ErrInvalidPluginNo)
			_, err := store.Load(no)
			require.ErrorIs(t, err, ErrInvalidPluginNo)
		})
	}
}

func TestValidatePluginNoRejectsDotSegments(t *testing.T) {
	for _, no := range []string{".", ".."} {
		t.Run(no, func(t *testing.T) {
			if err := validatePluginNo(no); err == nil {
				t.Fatalf("validatePluginNo(%q) returned nil", no)
			}
		})
	}
}
