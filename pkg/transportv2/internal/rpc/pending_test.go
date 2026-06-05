package rpc

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestPendingStoreComplete(t *testing.T) {
	table := NewPendingTable(16)
	ch := make(chan Response, 1)

	table.Store(42, ch)

	if !table.Complete(42, Response{Payload: []byte("ok")}) {
		t.Fatal("Complete() = false, want true")
	}
	got := <-ch
	if string(got.Payload) != "ok" {
		t.Fatalf("Payload = %q, want ok", got.Payload)
	}
	if table.Complete(42, Response{Payload: []byte("again")}) {
		t.Fatal("Complete() = true, want false after completion")
	}
}

func TestPendingFailAll(t *testing.T) {
	table := NewPendingTable(16)
	errBoom := errors.New("boom")
	ch1 := make(chan Response, 1)
	ch2 := make(chan Response, 1)

	table.Store(1, ch1)
	table.Store(2, ch2)
	table.FailAll(errBoom)

	if got := table.Len(); got != 0 {
		t.Fatalf("Len() = %d, want 0", got)
	}
	for i, ch := range []chan Response{ch1, ch2} {
		resp := <-ch
		if !errors.Is(resp.Err, errBoom) {
			t.Fatalf("response %d err = %v, want %v", i, resp.Err, errBoom)
		}
	}
}

func TestPendingConcurrentStoreDelete(t *testing.T) {
	table := NewPendingTable(16)
	var wg sync.WaitGroup

	for i := 0; i < 256; i++ {
		id := uint64(i + 1)
		wg.Add(1)
		go func() {
			defer wg.Done()
			table.Store(id, make(chan Response, 1))
			table.Delete(id)
		}()
	}
	wg.Wait()

	if got := table.Len(); got != 0 {
		t.Fatalf("Len() = %d, want 0", got)
	}
}

func TestPendingCompleteMissReturnsFalse(t *testing.T) {
	table := NewPendingTable(16)

	if table.Complete(404, Response{Payload: []byte("missing")}) {
		t.Fatal("Complete() = true, want false for missing id")
	}
}

func TestPendingFailAllDoesNotBlockOnFullChannel(t *testing.T) {
	table := NewPendingTable(16)
	errBoom := errors.New("boom")
	ch := make(chan Response, 1)
	ch <- Response{Payload: []byte("already full")}

	table.Store(1, ch)
	done := make(chan struct{})
	go func() {
		table.FailAll(errBoom)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("FailAll() blocked on a full channel")
	}
	if got := table.Len(); got != 0 {
		t.Fatalf("Len() = %d, want 0", got)
	}
}
