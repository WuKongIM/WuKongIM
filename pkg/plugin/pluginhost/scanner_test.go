package pluginhost

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestScanPluginsIncludesExecutableWKPFilesSortedByPath(t *testing.T) {
	dir := t.TempDir()
	writeExecutablePlugin(t, filepath.Join(dir, "zeta.wkp"))
	writeExecutablePlugin(t, filepath.Join(dir, "alpha.wkp"))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ignored.txt"), []byte("x"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "not-executable.wkp"), []byte("x"), 0o644))
	require.NoError(t, os.Mkdir(filepath.Join(dir, "directory.wkp"), 0o755))

	got, err := ScanPlugins(dir)

	require.NoError(t, err)
	require.Equal(t, []ProcessSpec{
		{No: "alpha", Path: filepath.Join(dir, "alpha.wkp")},
		{No: "zeta", Path: filepath.Join(dir, "zeta.wkp")},
	}, got)
}

func TestScanPluginsReturnsAbsoluteCleanPaths(t *testing.T) {
	dir := t.TempDir()
	writeExecutablePlugin(t, filepath.Join(dir, "alpha.wkp"))
	cwd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(filepath.Dir(dir)))
	t.Cleanup(func() { require.NoError(t, os.Chdir(cwd)) })

	got, err := ScanPlugins(filepath.Base(dir))

	require.NoError(t, err)
	require.Len(t, got, 1)
	require.True(t, filepath.IsAbs(got[0].Path))
	gotResolved, err := filepath.EvalSymlinks(got[0].Path)
	require.NoError(t, err)
	wantResolved, err := filepath.EvalSymlinks(filepath.Join(dir, "alpha.wkp"))
	require.NoError(t, err)
	require.Equal(t, wantResolved, gotResolved)
}

func TestScanPluginsRejectsInvalidPluginNoDerivedFromFilename(t *testing.T) {
	dir := t.TempDir()
	writeExecutablePlugin(t, filepath.Join(dir, "bad name.wkp"))

	got, err := ScanPlugins(dir)

	require.ErrorIs(t, err, ErrInvalidPluginNo)
	require.Nil(t, got)
}

func TestScanPluginsRejectsBrokenSymlink(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Symlink(filepath.Join(dir, "missing.wkp"), filepath.Join(dir, "broken.wkp")))

	got, err := ScanPlugins(dir)

	require.Error(t, err)
	require.Nil(t, got)
}

func TestScanPluginsRejectsSymlinkEscapingConfiguredDir(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	outsidePlugin := filepath.Join(outside, "escape.wkp")
	writeExecutablePlugin(t, outsidePlugin)
	require.NoError(t, os.Symlink(outsidePlugin, filepath.Join(dir, "escape.wkp")))

	got, err := ScanPlugins(dir)

	require.Error(t, err)
	require.Nil(t, got)
}

func writeExecutablePlugin(t *testing.T, path string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755))
}
