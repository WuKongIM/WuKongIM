package core

import (
	"sync"
	"sync/atomic"
	"testing"
)

var ownedBufferSink OwnedBuffer

func TestPriorityValidation(t *testing.T) {
	for _, pri := range []Priority{PriorityRaft, PriorityControl, PriorityRPC, PriorityBulk} {
		if !pri.Valid() {
			t.Fatalf("priority %d valid = false, want true", pri)
		}
	}
	if Priority(0).Valid() {
		t.Fatal("priority 0 valid = true, want false")
	}
	if Priority(99).Valid() {
		t.Fatal("priority 99 valid = true, want false")
	}
}

func TestOwnedBufferReleaseOnce(t *testing.T) {
	releases := 0
	buf := NewOwnedBuffer([]byte("abc"), func([]byte) { releases++ })
	if string(buf.Bytes()) != "abc" {
		t.Fatalf("Bytes() = %q, want abc", buf.Bytes())
	}
	buf.Release()
	buf.Release()
	if releases != 1 {
		t.Fatalf("releases = %d, want 1", releases)
	}
}

func TestOwnedBufferBytesAndLenAreEmptyAfterRelease(t *testing.T) {
	original := []byte("abc")
	var released []byte
	buf := NewOwnedBuffer(original, func(data []byte) {
		released = data
	})

	buf.Release()

	if string(released) != "abc" {
		t.Fatalf("released bytes = %q, want abc", released)
	}
	if got := buf.Bytes(); got != nil {
		t.Fatalf("Bytes() after Release() = %q, want nil", got)
	}
	if got := buf.Len(); got != 0 {
		t.Fatalf("Len() after Release() = %d, want 0", got)
	}
}

func TestOwnedBufferConcurrentReleaseReleasesOnce(t *testing.T) {
	var releases int32
	buf := NewOwnedBuffer([]byte("abc"), func([]byte) {
		atomic.AddInt32(&releases, 1)
	})

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			buf.Release()
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt32(&releases); got != 1 {
		t.Fatalf("releases = %d, want 1", got)
	}
	if buf.Bytes() != nil || buf.Len() != 0 {
		t.Fatalf("after release Bytes()/Len() = %v/%d, want nil/0", buf.Bytes(), buf.Len())
	}
}

func TestOwnedBufferCopyOwnsIndependentBytes(t *testing.T) {
	src := []byte("payload")
	copied := CopyOwnedBuffer(src)
	defer copied.Release()

	src[0] = 'P'

	if string(copied.Bytes()) != "payload" {
		t.Fatalf("copied bytes = %q, want payload", copied.Bytes())
	}
}

func TestCopyOwnedBufferAllocatesOnlyCopiedPayload(t *testing.T) {
	src := []byte("payload")
	allocs := testing.AllocsPerRun(1000, func() {
		ownedBufferSink = CopyOwnedBuffer(src)
		ownedBufferSink.Release()
	})
	if allocs > 1 {
		t.Fatalf("CopyOwnedBuffer allocs = %.2f, want <= 1", allocs)
	}
}
