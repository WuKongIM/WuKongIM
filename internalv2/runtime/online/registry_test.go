package online

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRegisterPendingRejectsInvalidConnection(t *testing.T) {
	reg := NewRegistry(RegistryOptions{ShardCount: 4})
	if err := reg.RegisterPending(OnlineConn{SessionID: 1}); err == nil {
		t.Fatal("RegisterPending() error = nil, want invalid connection")
	}
	if err := reg.RegisterPending(OnlineConn{UID: "u1"}); err == nil {
		t.Fatal("RegisterPending() error = nil, want invalid connection")
	}
}

func TestRegistryPendingActiveClosingLifecycle(t *testing.T) {
	reg := NewRegistry(RegistryOptions{ShardCount: 4})
	conn := OnlineConn{UID: "u1", HashSlot: 3, OwnerBootID: 9, SessionID: 11}
	if err := reg.RegisterPending(conn); err != nil {
		t.Fatalf("RegisterPending() error = %v", err)
	}
	if got, ok := reg.Connection(11); !ok || got.State != RouteStatePending {
		t.Fatalf("pending connection = %#v,%v", got, ok)
	}
	if err := reg.MarkActive(11); err != nil {
		t.Fatalf("MarkActive() error = %v", err)
	}
	if got, ok := reg.Connection(11); !ok || got.State != RouteStateActive {
		t.Fatalf("active connection = %#v,%v", got, ok)
	}
	if got, ok := reg.MarkClosingAndUnregister(11); !ok || got.State != RouteStateClosing {
		t.Fatalf("closing connection = %#v,%v", got, ok)
	}
	if _, ok := reg.Connection(11); ok {
		t.Fatal("connection still indexed after unregister")
	}
}

func TestRegistryMarkTouchedMarksActiveRouteDirty(t *testing.T) {
	reg := NewRegistry(RegistryOptions{ShardCount: 2})
	conn := OnlineConn{UID: "u1", HashSlot: 7, OwnerNodeID: 1, OwnerBootID: 11, OwnerSeq: 21, SessionID: 101, ConnectedUnix: 10}
	require.NoError(t, reg.RegisterPending(conn))
	require.NoError(t, reg.MarkActive(conn.SessionID))

	got, ok := reg.MarkTouched(conn.SessionID, 15)
	require.True(t, ok)
	require.Equal(t, int64(15), got.LastActivityUnix)

	batch := reg.DrainTouched(10)
	require.Len(t, batch, 1)
	require.Equal(t, conn.SessionID, batch[0].SessionID)
	require.Equal(t, int64(15), batch[0].LastActivityUnix)
	require.Empty(t, reg.DrainTouched(10))
}

func TestRegistryMarkTouchedIgnoresPendingAndMissing(t *testing.T) {
	reg := NewRegistry(RegistryOptions{ShardCount: 1})
	conn := OnlineConn{UID: "u1", HashSlot: 1, OwnerNodeID: 1, OwnerBootID: 1, OwnerSeq: 1, SessionID: 7, ConnectedUnix: 10}
	require.NoError(t, reg.RegisterPending(conn))

	_, ok := reg.MarkTouched(conn.SessionID, 11)
	require.False(t, ok)
	_, ok = reg.MarkTouched(999, 12)
	require.False(t, ok)
	require.Empty(t, reg.DrainTouched(10))
}

func TestRegistryDrainTouchedBatchesAndClearsDirty(t *testing.T) {
	reg := NewRegistry(RegistryOptions{ShardCount: 1})
	for i := uint64(1); i <= 3; i++ {
		conn := OnlineConn{UID: fmt.Sprintf("u%d", i), HashSlot: uint16(i), OwnerNodeID: 1, OwnerBootID: 1, OwnerSeq: i, SessionID: i, ConnectedUnix: 10}
		require.NoError(t, reg.RegisterPending(conn))
		require.NoError(t, reg.MarkActive(i))
		_, ok := reg.MarkTouched(i, int64(20+i))
		require.True(t, ok)
	}

	first := reg.DrainTouched(2)
	require.Len(t, first, 2)
	second := reg.DrainTouched(2)
	require.Len(t, second, 1)
	require.Empty(t, reg.DrainTouched(2))
}

func TestRegistryDrainTouchedRotatesShardStart(t *testing.T) {
	reg := NewRegistry(RegistryOptions{ShardCount: 2})
	shardOne := OnlineConn{UID: "u1", HashSlot: 1, OwnerNodeID: 1, OwnerBootID: 1, OwnerSeq: 1, SessionID: 1, ConnectedUnix: 10}
	shardZero := OnlineConn{UID: "u2", HashSlot: 1, OwnerNodeID: 1, OwnerBootID: 1, OwnerSeq: 2, SessionID: 2, ConnectedUnix: 10}
	require.NoError(t, reg.RegisterPending(shardOne))
	require.NoError(t, reg.RegisterPending(shardZero))
	require.NoError(t, reg.MarkActive(shardOne.SessionID))
	require.NoError(t, reg.MarkActive(shardZero.SessionID))
	_, ok := reg.MarkTouched(shardOne.SessionID, 20)
	require.True(t, ok)
	_, ok = reg.MarkTouched(shardZero.SessionID, 20)
	require.True(t, ok)

	first := reg.DrainTouched(1)
	require.Len(t, first, 1)
	require.Equal(t, shardZero.SessionID, first[0].SessionID)
	_, ok = reg.MarkTouched(shardZero.SessionID, 21)
	require.True(t, ok)

	second := reg.DrainTouched(1)
	require.Len(t, second, 1)
	require.Equal(t, shardOne.SessionID, second[0].SessionID)
}

func TestRegistryRequeueTouchedSkipsRemovedOrSupersededSessions(t *testing.T) {
	reg := NewRegistry(RegistryOptions{ShardCount: 1})
	original := OnlineConn{UID: "u1", HashSlot: 1, OwnerNodeID: 1, OwnerBootID: 1, OwnerSeq: 10, SessionID: 100, ConnectedUnix: 10}
	require.NoError(t, reg.RegisterPending(original))
	require.NoError(t, reg.MarkActive(original.SessionID))
	_, ok := reg.MarkTouched(original.SessionID, 20)
	require.True(t, ok)
	drained := reg.DrainTouched(10)
	require.Len(t, drained, 1)

	removed, ok := reg.MarkClosingAndUnregister(original.SessionID)
	require.True(t, ok)
	reg.RequeueTouched([]OnlineConn{removed})
	require.Empty(t, reg.DrainTouched(10))

	replacement := original
	replacement.OwnerSeq = 11
	replacement.ConnectedUnix = 30
	require.NoError(t, reg.RegisterPending(replacement))
	require.NoError(t, reg.MarkActive(replacement.SessionID))
	reg.RequeueTouched(drained)
	require.Empty(t, reg.DrainTouched(10))

	_, ok = reg.MarkTouched(replacement.SessionID, 40)
	require.True(t, ok)
	batch := reg.DrainTouched(10)
	require.Len(t, batch, 1)
	require.Equal(t, uint64(11), batch[0].OwnerSeq)
}

func TestRegistryRequeueTouchedRestoresCurrentActiveRoute(t *testing.T) {
	reg := NewRegistry(RegistryOptions{ShardCount: 1})
	conn := OnlineConn{UID: "u1", HashSlot: 1, OwnerNodeID: 1, OwnerBootID: 1, OwnerSeq: 10, SessionID: 100, ConnectedUnix: 10}
	require.NoError(t, reg.RegisterPending(conn))
	require.NoError(t, reg.MarkActive(conn.SessionID))
	_, ok := reg.MarkTouched(conn.SessionID, 20)
	require.True(t, ok)
	drained := reg.DrainTouched(10)
	require.Len(t, drained, 1)

	reg.RequeueTouched(drained)

	batch := reg.DrainTouched(10)
	require.Len(t, batch, 1)
	require.Equal(t, conn.SessionID, batch[0].SessionID)
	require.Equal(t, int64(20), batch[0].LastActivityUnix)
}

func TestRegistryRequeueTouchedSkipsDifferentUID(t *testing.T) {
	reg := NewRegistry(RegistryOptions{ShardCount: 1})
	original := OnlineConn{UID: "u1", HashSlot: 1, OwnerNodeID: 1, OwnerBootID: 1, OwnerSeq: 10, SessionID: 100, ConnectedUnix: 10}
	require.NoError(t, reg.RegisterPending(original))
	require.NoError(t, reg.MarkActive(original.SessionID))
	_, ok := reg.MarkTouched(original.SessionID, 20)
	require.True(t, ok)
	drained := reg.DrainTouched(10)
	require.Len(t, drained, 1)

	require.NoError(t, reg.RegisterPending(OnlineConn{UID: "u2", HashSlot: 1, OwnerNodeID: 1, OwnerBootID: 1, OwnerSeq: 10, SessionID: 100, ConnectedUnix: 30}))
	require.NoError(t, reg.MarkActive(100))
	reg.RequeueTouched(drained)

	require.Empty(t, reg.DrainTouched(10))
}
