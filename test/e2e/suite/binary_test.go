//go:build e2e

package suite

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDefaultBinaryCacheRootIsStableAcrossProcesses(t *testing.T) {
	tempRoot := t.TempDir()
	t.Setenv("TMPDIR", tempRoot)
	t.Setenv("HOME", tempRoot)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(tempRoot, "cache"))

	resetDefaultBinaryRoot := func() {
		defaultBinaryRoot.once = sync.Once{}
		defaultBinaryRoot.path = ""
		defaultBinaryRoot.err = nil
	}
	t.Cleanup(resetDefaultBinaryRoot)

	resetDefaultBinaryRoot()
	first, err := defaultBinaryCacheRoot()
	require.NoError(t, err)

	// Reset the process-local sync.Once state to model a second go test process.
	resetDefaultBinaryRoot()
	second, err := defaultBinaryCacheRoot()
	require.NoError(t, err)

	require.Equal(t, first, second)
}

func TestBinaryCacheBuildsWukongIMBinaryOnce(t *testing.T) {
	var builds int
	cacheRoot := t.TempDir()
	cache := BinaryCache{
		build: func(dst string) error {
			builds++
			require.Contains(t, filepath.Base(dst), ".wukongim-e2e-build-")
			return os.WriteFile(dst, []byte("fake-binary"), 0o755)
		},
	}

	first, err := cache.Path(cacheRoot)
	require.NoError(t, err)
	second, err := cache.Path(t.TempDir())
	require.NoError(t, err)

	require.Equal(t, first, second)
	require.Equal(t, 1, builds)
	require.Equal(t, []byte("fake-binary"), requireFileContents(t, first))
	require.Len(t, requireDirectoryEntries(t, cacheRoot), 1)
}

func TestBinaryCacheDoesNotPublishPartialBuild(t *testing.T) {
	cacheRoot := t.TempDir()
	cache := BinaryCache{
		build: func(dst string) error {
			require.NoError(t, os.WriteFile(dst, []byte("partial-binary"), 0o755))
			return errors.New("injected build failure")
		},
	}

	path, err := cache.Path(cacheRoot)
	require.EqualError(t, err, "injected build failure")
	require.Equal(t, filepath.Join(cacheRoot, e2eBinaryCacheFileName), path)
	require.Empty(t, requireDirectoryEntries(t, cacheRoot))
}

func TestResolveBinaryPathUsesE2EOverride(t *testing.T) {
	fakeBinary := filepath.Join(t.TempDir(), "wukongim")
	require.NoError(t, os.WriteFile(fakeBinary, []byte("fake-binary"), 0o755))
	t.Setenv("WK_E2E_BINARY", fakeBinary)

	got, err := resolveBinaryPath()
	require.NoError(t, err)
	require.Equal(t, fakeBinary, got)
}

func TestResolveBinaryPathReportsBadE2EOverride(t *testing.T) {
	missingBinary := filepath.Join(t.TempDir(), "missing-wukongim")
	t.Setenv("WK_E2E_BINARY", missingBinary)

	_, err := resolveBinaryPath()
	require.Error(t, err)
	require.Contains(t, err.Error(), `WK_E2E_BINARY="`+missingBinary+`"`)
}

func requireFileContents(t *testing.T, path string) []byte {
	t.Helper()
	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	return contents
}

func requireDirectoryEntries(t *testing.T, path string) []os.DirEntry {
	t.Helper()
	entries, err := os.ReadDir(path)
	require.NoError(t, err)
	return entries
}
