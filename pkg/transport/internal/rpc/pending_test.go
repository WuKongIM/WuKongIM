package rpc

import (
	"context"
	"errors"
	"github.com/WuKongIM/WuKongIM/pkg/transport/internal/core"
	"sync"
	"testing"
)

func newPendingOwner() *Completion {
	owner := NewCompletion(context.Background())
	owner.SendResult(nil)
	return owner
}
func completePendingForTest(table *PendingTable, id uint64, response Response) bool {
	owner := table.Load(id)
	if owner == nil {
		return false
	}
	accepted := owner.Publish(response)
	table.DeleteOwner(id, owner)
	return accepted
}
func TestPendingStoreComplete(t *testing.T) {
	table := NewPendingTable(16)
	owner := newPendingOwner()
	table.Store(42, owner)
	if !completePendingForTest(table, 42, Response{Payload: []byte("ok")}) {
		t.Fatal("existing owner was not completed")
	}
	got, ready := owner.Resolve(nil)
	if !ready || string(got.Response.Payload) != "ok" || table.Len() != 0 || completePendingForTest(table, 42, Response{}) {
		t.Fatal("response/index ownership did not transfer exactly once")
	}
}
func TestPendingStoreRequiresOwner(t *testing.T) {
	table := NewPendingTable(16)
	mustPanic(t, func() { table.Store(1, nil) })
}
func TestPendingFailAllRejectsLaterStore(t *testing.T) {
	table := NewPendingTable(16)
	errBoom := errors.New("first shutdown")
	first := newPendingOwner()
	table.Store(1, first)
	table.FailAll(errBoom)
	table.FailAll(errors.New("later shutdown"))
	later := newPendingOwner()
	table.Store(2, later)
	for _, owner := range []*Completion{first, later} {
		got, ready := owner.Resolve(nil)
		if !ready || got.Response.Err != errBoom || got.Response.ReceiveTiming != nil || got.LocalContext {
			t.Fatal("shutdown error or synthetic receipt ownership changed")
		}
	}
	if table.Len() != 0 || table.Load(1) != nil || table.Load(2) != nil {
		t.Fatal("closed index retained an entry")
	}
}
func TestPendingFailAllConcurrentStoreLeavesNoPending(t *testing.T) {
	table := NewPendingTable(16)
	owners := make([]*Completion, 512)
	start := make(chan struct{})
	var work sync.WaitGroup
	for i := range owners {
		owners[i] = newPendingOwner()
		work.Add(1)
		go func(i int) { defer work.Done(); <-start; table.Store(uint64(i+1), owners[i]) }(i)
	}
	close(start)
	table.FailAll(core.ErrStopped)
	work.Wait()
	if table.Len() != 0 {
		t.Fatal("close/store race left indexed owners")
	}
	for _, owner := range owners {
		got, ready := owner.Resolve(nil)
		if !ready || !errors.Is(got.Response.Err, core.ErrStopped) {
			t.Fatal("close/store race left unsettled owner")
		}
	}
}
func TestPendingCountReplacementAndConditionalRemoval(t *testing.T) {
	table := NewPendingTable(16)
	old, current := newPendingOwner(), newPendingOwner()
	table.Store(1, old)
	table.Store(1, current)
	if table.Len() != 1 || table.DeleteOwner(1, old) || table.Load(1) != current {
		t.Fatal("stale reader removed its replacement")
	}
	got, ready := old.Resolve(nil)
	if !ready || !errors.Is(got.Response.Err, core.ErrStopped) || old.Publish(Response{}) {
		t.Fatal("replaced owner was not settled")
	}
	if !table.DeleteOwner(1, current) || table.DeleteOwner(1, current) || table.Len() != 0 {
		t.Fatal("conditional removal miscounted owners")
	}
	current.Fail(core.ErrStopped)
}
func TestPendingConcurrentStoreDelete(t *testing.T) {
	table := NewPendingTable(16)
	var work sync.WaitGroup
	for i := 0; i < 256; i++ {
		work.Add(1)
		go func(id uint64) {
			defer work.Done()
			owner := newPendingOwner()
			table.Store(id, owner)
			table.Delete(id)
			owner.Fail(core.ErrCanceled)
		}(uint64(i + 1))
	}
	work.Wait()
	if table.Len() != 0 {
		t.Fatal("concurrent removals miscounted")
	}
}
func TestPendingDefaultShardCountHandlesInvalidInputs(t *testing.T) {
	for _, count := range []int{0, 3} {
		table := NewPendingTable(count)
		if len(table.shards) != 16 || table.Load(404) != nil {
			t.Fatal("default shard shape or missing lookup changed")
		}
	}
}
func mustPanic(t *testing.T, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatal("function did not panic")
		}
	}()
	fn()
}
