package mqttowner

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	runtime "github.com/WuKongIM/WuKongIM/internal/runtime/mqttsession"
	"github.com/stretchr/testify/require"
)

func retiredFixture(t *testing.T, boot string, count int) (runtime.RetiredBoot, contract.Owner) {
	t.Helper()
	r, err := runtime.NewOwners(runtime.OwnerOptions{NodeID: 3, BootID: boot, Capacity: 4, MaxOperations: 1, PendingTimeout: time.Second, MaxLease: time.Minute, CloseRetry: time.Second})
	require.NoError(t, err)
	var owner contract.Owner
	for range count {
		owner, err = r.Reserve(runtime.Claim{Key: contract.Key{Namespace: "main", ClientID: "a"}, UID: "alice", SessionGeneration: 1, OwnerGeneration: 1}, func(context.Context) error { return nil })
		require.NoError(t, err)
	}
	require.NoError(t, r.Close(context.Background()))
	proof, err := r.Retirement()
	require.NoError(t, err)
	return proof, owner
}

func TestRetirementsPersistExactBoundedBoot(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	s, err := NewRetirements(dir, 3)
	require.NoError(t, err)
	proof, o := retiredFixture(t, "old-boot", 2)
	require.ErrorIs(t, s.Quiesce(ctx, o), runtime.ErrOwnerUnknown)
	require.Error(t, s.Record(ctx, runtime.RetiredBoot{}))
	require.NoError(t, s.Record(ctx, proof))
	require.NoError(t, s.Record(ctx, proof))
	s, err = NewRetirements(dir, 3)
	require.NoError(t, err)
	require.NoError(t, s.Quiesce(ctx, o))
	first := o
	first.ConnectionID = 1
	require.NoError(t, s.Quiesce(ctx, first))
	for _, change := range []func(*contract.Owner){func(o *contract.Owner) { o.ConnectionID++ }, func(o *contract.Owner) { o.NodeID++ }, func(o *contract.Owner) { o.BootID = "other" }, func(o *contract.Owner) { o.ConnectionID = 0 }} {
		bad := o
		change(&bad)
		require.Error(t, s.Quiesce(ctx, bad))
	}
	conflict, _ := retiredFixture(t, "old-boot", 3)
	require.Error(t, s.Record(ctx, conflict))
	require.NoError(t, s.Quiesce(ctx, o))
	wrong, err := NewRetirements(dir, 4)
	require.NoError(t, err)
	require.Error(t, wrong.Record(ctx, proof))
	require.Error(t, wrong.Quiesce(ctx, o))
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	require.ErrorIs(t, s.Record(cancelled, proof), context.Canceled)
	require.ErrorIs(t, s.Quiesce(cancelled, o), context.Canceled)
	files, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, files, 1)
	path := filepath.Join(dir, files[0].Name())
	bytes, err := os.ReadFile(path)
	require.NoError(t, err)
	require.LessOrEqual(t, len(bytes), 1024)
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0600), info.Mode().Perm())
	for _, bad := range [][]byte{nil, []byte("{}"), bytes[:len(bytes)-1], append(append([]byte(nil), bytes...), 0), make([]byte, 1025)} {
		require.NoError(t, os.WriteFile(path, bad, 0600))
		require.Error(t, s.Quiesce(ctx, o))
	}
	require.NoError(t, os.WriteFile(path, bytes, 0600))
	require.NoError(t, s.Quiesce(ctx, o))
	// Every byte is integrity-protected, including the format version and bound.
	for j := range bytes {
		bad := append([]byte(nil), bytes...)
		bad[j] ^= 0x80
		require.NoError(t, os.WriteFile(path, bad, 0600))
		require.Error(t, s.Quiesce(ctx, o), "byte %d", j)
	}
}

func TestRetirementsDoNotPublishEmptyOrFailedWrites(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	s, err := NewRetirements(dir, 3)
	require.NoError(t, err)
	empty, _ := retiredFixture(t, "empty", 0)
	require.NoError(t, s.Record(ctx, empty))
	files, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Empty(t, files)
	proof, o := retiredFixture(t, "pending", 1)
	require.NoError(t, os.Remove(dir))
	require.NoError(t, os.WriteFile(dir, []byte("blocked"), 0600))
	require.Error(t, s.Record(ctx, proof))
	require.Error(t, s.Quiesce(ctx, o))
}
