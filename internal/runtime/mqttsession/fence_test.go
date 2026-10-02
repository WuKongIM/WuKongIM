package mqttsession

import (
	"context"
	"errors"
	"testing"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
)

func TestOwnerFenceDoesNotWaitOrForgetAdmittedWork(t *testing.T) {
	now := time.Now()
	m, e := NewOwners(OwnerOptions{NodeID: 1, BootID: "boot", Capacity: 2, MaxOperations: 2, PendingTimeout: time.Minute, MaxLease: time.Minute, CloseRetry: time.Second, Now: func() time.Time { return now }})
	if e != nil {
		t.Fatal(e)
	}
	closes := 0
	o, e := m.Reserve(Claim{Key: contract.Key{Namespace: "n", ClientID: "c"}, UID: "u", SessionGeneration: 1, OwnerGeneration: 1}, func(context.Context) error { closes++; return nil })
	if e != nil {
		t.Fatal(e)
	}
	if e = m.Activate(o, 1, now.Add(time.Minute)); e != nil {
		t.Fatal(e)
	}
	op, e := m.Begin(context.Background(), o)
	if e != nil {
		t.Fatal(e)
	}
	if e = m.Fence(o); e != nil {
		t.Fatal(e)
	}
	if closes != 0 {
		t.Fatal("Fence waited for transport cleanup")
	}
	if op.Context().Err() == nil {
		t.Fatal("admitted scope not canceled")
	}
	if _, e = m.Begin(context.Background(), o); !errors.Is(e, ErrOwnerFenced) {
		t.Fatal(e)
	}
	if m.Snapshot().Operations != 1 {
		t.Fatal("cancellation erased admitted work")
	}
	op.Done()
	if e = m.Quiesce(context.Background(), o); e != nil {
		t.Fatal(e)
	}
	if closes != 1 {
		t.Fatal("missing physical close")
	}
	if e = m.Fence(o); e != nil {
		t.Fatal(e)
	}
	o.ConnectionID++
	if e = m.Fence(o); !errors.Is(e, ErrOwnerUnknown) {
		t.Fatal(e)
	}
}
