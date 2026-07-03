//go:build e2e

package suite

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBinaryCacheBuildsWukongIMBinaryOnce(t *testing.T) {
	var builds int
	cache := BinaryCache{
		build: func(dst string) error {
			builds++
			require.Equal(t, "wukongim-e2e", filepath.Base(dst))
			return os.WriteFile(dst, []byte("fake-binary"), 0o755)
		},
	}

	first, err := cache.Path(t.TempDir())
	require.NoError(t, err)
	second, err := cache.Path(t.TempDir())
	require.NoError(t, err)

	require.Equal(t, first, second)
	require.Equal(t, 1, builds)
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
