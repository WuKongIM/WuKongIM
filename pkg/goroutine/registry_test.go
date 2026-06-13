package goroutine

import (
	"sync"
	"testing"
	"time"
)

func TestRegistryGoAndDone(t *testing.T) {
	r := New()
	var wg sync.WaitGroup
	wg.Add(3)
	r.GoN("test", "worker", 3, func(_ int) {
		wg.Done()
	})
	wg.Wait()
	time.Sleep(5 * time.Millisecond)

	snap := r.Snapshot()
	if snap.TotalStarted != 3 {
		t.Fatalf("TotalStarted = %d, want 3", snap.TotalStarted)
	}
	if snap.TotalActive != 0 {
		t.Fatalf("TotalActive = %d, want 0 (all finished)", snap.TotalActive)
	}
}

func TestRegistryPanicRecovery(t *testing.T) {
	var panicComponent, panicName string
	var panicValue any
	r := New(WithPanicHandler(func(c, n string, rec any) {
		panicComponent = c
		panicName = n
		panicValue = rec
	}))

	done := make(chan struct{})
	r.Go("mycomp", "crasher", func() {
		defer close(done)
		panic("test panic")
	})
	<-done
	time.Sleep(5 * time.Millisecond)

	if panicComponent != "mycomp" {
		t.Fatalf("panic component = %q, want %q", panicComponent, "mycomp")
	}
	if panicName != "crasher" {
		t.Fatalf("panic name = %q, want %q", panicName, "crasher")
	}
	if panicValue != "test panic" {
		t.Fatalf("panic value = %v, want %q", panicValue, "test panic")
	}

	snap := r.Snapshot()
	if snap.TotalPanics != 1 {
		t.Fatalf("TotalPanics = %d, want 1", snap.TotalPanics)
	}
}

func TestRegistrySnapshot(t *testing.T) {
	r := New()
	blocker := make(chan struct{})

	r.GoN("gateway", "dispatch", 5, func(_ int) { <-blocker })
	r.GoN("transport", "conn", 3, func(_ int) { <-blocker })

	time.Sleep(10 * time.Millisecond)
	snap := r.Snapshot()

	if snap.TotalActive != 8 {
		t.Fatalf("TotalActive = %d, want 8", snap.TotalActive)
	}
	if snap.TotalStarted != 8 {
		t.Fatalf("TotalStarted = %d, want 8", snap.TotalStarted)
	}

	found := map[string]int64{}
	for _, cs := range snap.Components {
		found[cs.Component] = cs.Active
	}
	if found["gateway"] != 5 {
		t.Fatalf("gateway active = %d, want 5", found["gateway"])
	}
	if found["transport"] != 3 {
		t.Fatalf("transport active = %d, want 3", found["transport"])
	}

	close(blocker)
	time.Sleep(10 * time.Millisecond)

	snap = r.Snapshot()
	if snap.TotalActive != 0 {
		t.Fatalf("after close: TotalActive = %d, want 0", snap.TotalActive)
	}
}

func TestRegistryPeak(t *testing.T) {
	r := New()
	phase1 := make(chan struct{})
	phase2 := make(chan struct{})

	r.GoN("test", "worker", 10, func(_ int) { <-phase1 })
	time.Sleep(5 * time.Millisecond)

	close(phase1)
	time.Sleep(5 * time.Millisecond)

	r.GoN("test", "worker", 3, func(_ int) { <-phase2 })
	time.Sleep(5 * time.Millisecond)

	snap := r.Snapshot()
	for _, cs := range snap.Components {
		for _, gs := range cs.Groups {
			if gs.Name == "worker" && gs.Peak != 10 {
				t.Fatalf("peak = %d, want 10", gs.Peak)
			}
		}
	}
	close(phase2)
}
