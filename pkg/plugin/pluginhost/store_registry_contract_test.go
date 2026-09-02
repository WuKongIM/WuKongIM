package pluginhost

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRegistryRemoveAndCopiesMutableConfiguration(t *testing.T) {
	registry := NewRegistry()
	plugin := ObservedPlugin{
		No:                "alpha",
		Methods:           []Method{MethodConfigUpdate},
		ConfigTemplateRaw: []byte(`{"enabled":true}`),
		Status:            StatusRunning,
		Enabled:           true,
	}
	registry.Upsert(plugin)
	plugin.ConfigTemplateRaw[0] = 'X'

	got, ok := registry.Get("alpha")
	require.True(t, ok)
	require.Equal(t, []byte(`{"enabled":true}`), got.ConfigTemplateRaw)
	got.ConfigTemplateRaw[0] = 'Y'
	require.Equal(t, []byte(`{"enabled":true}`), registry.List()[0].ConfigTemplateRaw)

	registry.Remove("alpha")
	registry.Remove("alpha")
	_, ok = registry.Get("alpha")
	require.False(t, ok)
	require.Empty(t, registry.List())
}

func TestStoreSaveRejectsInvalidRawConfiguration(t *testing.T) {
	store := NewStore(t.TempDir())

	err := store.Save(DesiredState{No: "alpha", Config: []byte(`{"unterminated":`)})

	require.ErrorContains(t, err, "marshal plugin desired state")
}

func TestStoreReportsDirectoryCreationFailure(t *testing.T) {
	dir := t.TempDir()
	blockingPath := filepath.Join(dir, "file")
	require.NoError(t, os.WriteFile(blockingPath, []byte("x"), 0o600))
	store := NewStore(filepath.Join(blockingPath, "state"))

	err := store.Save(DesiredState{No: "alpha", Enabled: true})

	require.ErrorContains(t, err, "create plugin state dir")
}

func TestStoreDeleteReportsDurabilityFailureAfterRemoval(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	require.NoError(t, store.Save(DesiredState{No: "alpha", Enabled: true}))
	expected := errors.New("directory sync failed")
	originalSync := syncParentDirAfterRename
	syncParentDirAfterRename = func(string) error { return expected }
	t.Cleanup(func() { syncParentDirAfterRename = originalSync })

	err := store.Delete("alpha")

	require.ErrorIs(t, err, expected)
	_, statErr := os.Stat(store.pathFor("alpha"))
	require.ErrorIs(t, statErr, os.ErrNotExist)
}

func TestStoreDeleteRejectsUnsafePluginNumber(t *testing.T) {
	store := NewStore(t.TempDir())

	err := store.Delete("../escape")

	require.ErrorIs(t, err, ErrInvalidPluginNo)
}

func TestStoreDeleteReportsFilesystemFailure(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	target := store.pathFor("alpha")
	require.NoError(t, os.Mkdir(target, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(target, "child"), []byte("x"), 0o600))

	err := store.Delete("alpha")

	require.ErrorContains(t, err, "delete plugin desired state")
}

func TestSyncDirReportsMissingDirectory(t *testing.T) {
	err := syncDir(filepath.Join(t.TempDir(), "missing"))

	require.Error(t, err)
}

func TestScanPluginsMissingDirectoryIsEmpty(t *testing.T) {
	specs, err := ScanPlugins(filepath.Join(t.TempDir(), "missing"))

	require.NoError(t, err)
	require.Empty(t, specs)
}

func TestScanPluginsReportsNonDirectoryPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plugins")
	require.NoError(t, os.WriteFile(path, []byte("x"), 0o600))

	specs, err := ScanPlugins(path)

	require.ErrorContains(t, err, "scan plugin dir")
	require.Nil(t, specs)
}

func TestEnsurePathUnderDirRejectsDirectoryItself(t *testing.T) {
	dir := t.TempDir()

	err := ensurePathUnderDir(dir, dir)

	require.ErrorContains(t, err, "escapes configured dir")
}

func TestRemoveIfUnderDirDoesNotFollowEscapingSymlink(t *testing.T) {
	dir := t.TempDir()
	outsideDir := t.TempDir()
	outsidePath := filepath.Join(outsideDir, "alpha.wkp")
	require.NoError(t, os.WriteFile(outsidePath, []byte("plugin"), 0o755))
	linkPath := filepath.Join(dir, "alpha.wkp")
	require.NoError(t, os.Symlink(outsidePath, linkPath))

	removed, err := removeIfUnderDir(dir, linkPath)

	require.NoError(t, err)
	require.False(t, removed)
	require.FileExists(t, outsidePath)
	require.FileExists(t, linkPath)
}

func TestRemoveIfUnderDirTreatsMissingBinaryAsAlreadyRemoved(t *testing.T) {
	dir := t.TempDir()

	removed, err := removeIfUnderDir(dir, filepath.Join(dir, "missing.wkp"))

	require.NoError(t, err)
	require.False(t, removed)
}
