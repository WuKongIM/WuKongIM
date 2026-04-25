//go:build e2e

package suite

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolveBinaryPathBuildsOncePerProcess(t *testing.T) {
	var calls int
	cache := BinaryCache{
		build: func(dst string) error {
			calls++
			return os.WriteFile(dst, []byte("fake-binary"), 0o755)
		},
	}

	first, err := cache.Path(t.TempDir())
	require.NoError(t, err)
	second, err := cache.Path(t.TempDir())
	require.NoError(t, err)

	require.Equal(t, 1, calls)
	require.Equal(t, first, second)
	require.FileExists(t, first)
}

func TestNewResolvesBinaryPathWithoutCallerSuppliedPath(t *testing.T) {
	fakeBinary := filepath.Join(t.TempDir(), "wukongim-e2e")
	require.NoError(t, os.WriteFile(fakeBinary, []byte("fake-binary"), 0o755))
	t.Setenv("WK_E2E_BINARY", fakeBinary)

	suite := New(t)

	require.Equal(t, fakeBinary, suite.binaryPath)
	require.DirExists(t, suite.workspace.RootDir)
}
