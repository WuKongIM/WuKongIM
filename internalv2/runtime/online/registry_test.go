package online

import "testing"

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

func TestVisitActiveByHashSlotPagesWithoutSortingWholeBucket(t *testing.T) {
	reg := NewRegistry(RegistryOptions{ShardCount: 4})
	for i := uint64(1); i <= 5; i++ {
		if err := reg.RegisterPending(OnlineConn{UID: "u", HashSlot: 2, OwnerBootID: 1, SessionID: i}); err != nil {
			t.Fatalf("RegisterPending(%d): %v", i, err)
		}
		if err := reg.MarkActive(i); err != nil {
			t.Fatalf("MarkActive(%d): %v", i, err)
		}
	}
	var first []uint64
	cursor, more := reg.VisitActiveByHashSlot(2, RouteCursor{}, 2, func(conn OnlineConn) bool {
		first = append(first, conn.SessionID)
		return true
	})
	if !more || len(first) != 2 {
		t.Fatalf("first page ids=%v more=%v, want len 2 and more", first, more)
	}
	var second []uint64
	_, _ = reg.VisitActiveByHashSlot(2, cursor, 10, func(conn OnlineConn) bool {
		second = append(second, conn.SessionID)
		return true
	})
	if len(second) != 3 {
		t.Fatalf("second page len = %d, want 3", len(second))
	}
}

func TestVisitActiveByHashSlotSkipsPendingAndOtherSlots(t *testing.T) {
	reg := NewRegistry(RegistryOptions{ShardCount: 4})
	if err := reg.RegisterPending(OnlineConn{UID: "u", HashSlot: 2, OwnerBootID: 1, SessionID: 1}); err != nil {
		t.Fatalf("RegisterPending(1): %v", err)
	}
	if err := reg.RegisterPending(OnlineConn{UID: "u", HashSlot: 2, OwnerBootID: 1, SessionID: 2}); err != nil {
		t.Fatalf("RegisterPending(2): %v", err)
	}
	if err := reg.MarkActive(2); err != nil {
		t.Fatalf("MarkActive(2): %v", err)
	}
	if err := reg.RegisterPending(OnlineConn{UID: "u", HashSlot: 3, OwnerBootID: 1, SessionID: 3}); err != nil {
		t.Fatalf("RegisterPending(3): %v", err)
	}
	if err := reg.MarkActive(3); err != nil {
		t.Fatalf("MarkActive(3): %v", err)
	}

	var got []uint64
	cursor, more := reg.VisitActiveByHashSlot(2, RouteCursor{}, 10, func(conn OnlineConn) bool {
		got = append(got, conn.SessionID)
		return true
	})
	if more || cursor.LastSessionID != 2 || len(got) != 1 || got[0] != 2 {
		t.Fatalf("VisitActiveByHashSlot() ids=%v cursor=%#v more=%v, want only active session 2", got, cursor, more)
	}
}
