package core

import "testing"

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

func TestOwnedBufferCopyOwnsIndependentBytes(t *testing.T) {
	src := []byte("payload")
	copied := CopyOwnedBuffer(src)
	defer copied.Release()

	src[0] = 'P'

	if string(copied.Bytes()) != "payload" {
		t.Fatalf("copied bytes = %q, want payload", copied.Bytes())
	}
}
