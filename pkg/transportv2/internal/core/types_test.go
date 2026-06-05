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

func TestOwnedBufferCopyOwnsIndependentBytes(t *testing.T) {
	src := []byte("payload")
	copied := CopyOwnedBuffer(src)
	defer copied.Release()

	src[0] = 'P'

	if string(copied.Bytes()) != "payload" {
		t.Fatalf("copied bytes = %q, want payload", copied.Bytes())
	}
}
