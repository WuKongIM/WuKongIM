//go:build e2e

package suite

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBinaryCacheBuildsWukongIMV2BinaryOnce(t *testing.T) {
	var builds int
	cache := BinaryCache{
		build: func(dst string) error {
			builds++
			require.Equal(t, "wukongimv2-e2e", filepath.Base(dst))
			return os.WriteFile(dst, []byte("fake-v2-binary"), 0o755)
		},
	}

	first, err := cache.Path(t.TempDir())
	require.NoError(t, err)
	second, err := cache.Path(t.TempDir())
	require.NoError(t, err)

	require.Equal(t, first, second)
	require.Equal(t, 1, builds)
}

func TestResolveBinaryPathUsesE2EV2Override(t *testing.T) {
	fakeBinary := filepath.Join(t.TempDir(), "wukongimv2")
	require.NoError(t, os.WriteFile(fakeBinary, []byte("fake-v2-binary"), 0o755))
	t.Setenv("WK_E2EV2_BINARY", fakeBinary)

	got, err := resolveBinaryPath()
	require.NoError(t, err)
	require.Equal(t, fakeBinary, got)
}
