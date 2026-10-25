//go:build integration

package rpc

import (
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/transport/internal/core"
)

func TestCompletionFailAllNeverWaitsForOwnerUnderIndexLocks(t *testing.T) {
	table := NewPendingTable(16)
	owner := newPendingOwner()
	table.Store(1, owner)
	owner.mu.Lock()
	var once sync.Once
	release := func() { once.Do(owner.mu.Unlock) }
	defer release()
	failed := make(chan struct{})
	go func() { table.FailAll(core.ErrStopped); close(failed) }()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for table.Len() != 0 {
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatal("shutdown did not detach the indexed owner")
		}
	}
	later := newPendingOwner()
	stored := make(chan struct{})
	go func() { table.Store(2, later); close(stored) }()
	select {
	case <-stored:
	case <-time.After(time.Second):
		t.Error("closed registration waited behind another owner's mutex")
	}
	release()
	select {
	case <-failed:
	case <-time.After(time.Second):
		t.Fatal("shutdown failed to join terminal transitions")
	}
}

func TestCompletionClosedStoreSettlesWithoutHoldingIndexGate(t *testing.T) {
	table := NewPendingTable(16)
	table.FailAll(core.ErrStopped)
	held, free := newPendingOwner(), newPendingOwner()
	held.mu.Lock()
	var once sync.Once
	release := func() { once.Do(held.mu.Unlock) }
	defer release()
	entered, stored := make(chan struct{}), make(chan struct{})
	go func() { close(entered); table.Store(1, held); close(stored) }()
	<-entered
	for i := 0; i < 100; i++ {
		runtime.Gosched()
	}
	other := make(chan struct{})
	go func() { table.Store(2, free); table.FailAll(core.ErrStopped); close(other) }()
	select {
	case <-other:
	case <-time.After(time.Second):
		t.Error("closed Store held index gate while waiting for its owner")
	}
	release()
	select {
	case <-stored:
	case <-time.After(time.Second):
		t.Fatal("rejected registration did not finish")
	}
}
