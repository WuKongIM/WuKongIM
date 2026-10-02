package mqttowner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	runtime "github.com/WuKongIM/WuKongIM/internal/runtime/mqttsession"
	"github.com/stretchr/testify/require"
)

func crashedOwner(boot string, id uint64) contract.Owner {
	return contract.Owner{Key: contract.Key{Namespace: "main", ClientID: "a"}, SessionGeneration: 1, OwnerGeneration: 1, NodeID: 3, BootID: boot, ConnectionID: id}
}

func TestRecoverProvesCrashedBootAndKeepsCurrentBootLive(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	first, err := NewRetirements(dir, 3)
	require.NoError(t, err)
	require.NoError(t, first.Recover(ctx, "boot-a"))
	require.ErrorIs(t, first.Quiesce(ctx, crashedOwner("boot-a", 1)), runtime.ErrOwnerUnknown, "the running boot is never retired")

	// A second process on the same data directory must not start.
	second, err := NewRetirements(dir, 3)
	require.NoError(t, err)
	require.Error(t, second.Recover(ctx, "boot-b"))

	// Simulated SIGKILL: the lock is released without a graceful receipt.
	require.NoError(t, first.Close())
	next, err := NewRetirements(dir, 3)
	require.NoError(t, err)
	require.NoError(t, next.Recover(ctx, "boot-b"))
	defer next.Close()
	require.NoError(t, next.Quiesce(ctx, crashedOwner("boot-a", 1)))
	require.NoError(t, next.Quiesce(ctx, crashedOwner("boot-a", 1<<62)), "a crashed boot covers every connection it could have issued")
	require.ErrorIs(t, next.Quiesce(ctx, crashedOwner("boot-b", 1)), runtime.ErrOwnerUnknown)
	other := crashedOwner("boot-a", 1)
	other.NodeID = 4
	require.ErrorIs(t, next.Quiesce(ctx, other), runtime.ErrOwnerUnknown)
	// The proven marker is consumed; only the current boot remains.
	markers, err := filepath.Glob(filepath.Join(dir, "*.started"))
	require.NoError(t, err)
	require.Len(t, markers, 1)
}

func TestRecoverAfterGracefulRetirementAddsNoCrashFact(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	s, err := NewRetirements(dir, 3)
	require.NoError(t, err)
	require.NoError(t, s.Recover(ctx, "old-boot"))
	proof, o := retiredFixture(t, "old-boot", 2)
	require.NoError(t, s.Record(ctx, proof))
	require.NoError(t, s.Close())
	markers, err := filepath.Glob(filepath.Join(dir, "*.started"))
	require.NoError(t, err)
	require.Empty(t, markers, "graceful receipt removes its boot marker")

	s, err = NewRetirements(dir, 3)
	require.NoError(t, err)
	require.NoError(t, s.Recover(ctx, "new-boot"))
	defer s.Close()
	require.NoError(t, s.Quiesce(ctx, o))
	// The graceful bound stays exact: it is not widened to a crash receipt.
	o.ConnectionID++
	require.ErrorIs(t, s.Quiesce(ctx, o), runtime.ErrOwnerUnknown)
}

func TestRecoverRetriesCrashBetweenReceiptAndMarkerRemoval(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	s, err := NewRetirements(dir, 3)
	require.NoError(t, err)
	require.NoError(t, s.Recover(ctx, "boot-a"))
	require.NoError(t, s.Close())
	s, err = NewRetirements(dir, 3)
	require.NoError(t, err)
	require.NoError(t, s.Recover(ctx, "boot-b"))
	require.NoError(t, s.Close())
	// Re-create boot-a's marker as if removal was lost after its receipt.
	marker, err := encodeStartedMarker(3, "boot-a")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(s.markerPath("boot-a"), marker, 0600))
	s, err = NewRetirements(dir, 3)
	require.NoError(t, err)
	require.NoError(t, s.Recover(ctx, "boot-c"))
	defer s.Close()
	require.NoError(t, s.Quiesce(ctx, crashedOwner("boot-a", 9)))
	require.NoError(t, s.Quiesce(ctx, crashedOwner("boot-b", 9)))
}

func TestRecoverFailsClosedOnCorruptOrForeignMarker(t *testing.T) {
	for _, mode := range []string{"corrupt", "foreign", "renamed"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			ctx := context.Background()
			s, err := NewRetirements(dir, 3)
			require.NoError(t, err)
			data, err := encodeStartedMarker(3, "boot-a")
			require.NoError(t, err)
			path := s.markerPath("boot-a")
			switch mode {
			case "corrupt":
				data[len(data)-1] ^= 1
			case "foreign":
				data, err = encodeStartedMarker(4, "boot-a")
				require.NoError(t, err)
			case "renamed":
				path = strings.Replace(path, filepath.Base(path), strings.Repeat("0", 64)+".started", 1)
			}
			require.NoError(t, os.WriteFile(path, data, 0600))
			require.Error(t, s.Recover(ctx, "boot-b"))
			require.ErrorIs(t, s.Quiesce(ctx, crashedOwner("boot-a", 1)), runtime.ErrOwnerUnknown)
			require.NoError(t, s.Close())
		})
	}
}

func TestRecoverRejectsInvalidUse(t *testing.T) {
	ctx := context.Background()
	s, err := NewRetirements(t.TempDir(), 3)
	require.NoError(t, err)
	require.Error(t, s.Recover(ctx, ""))
	require.NoError(t, s.Recover(ctx, "boot-a"))
	require.Error(t, s.Recover(ctx, "boot-b"), "one Retirements recovers exactly one boot")
	require.NoError(t, s.Close())
	require.NoError(t, s.Close())
}
