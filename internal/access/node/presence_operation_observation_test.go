package node

import (
	"bytes"
	"testing"
)

func TestPresenceOwnerTraceOperationIsClosedHeaderDeclaration(t *testing.T) {
	for op := 0; op < 256; op++ {
		body := []byte{'W', 'K', 'V', 'P', 2, byte(op)}
		want := uint8(0)
		if op == 7 || op == 9 || op == 10 || op == 11 {
			want = uint8(op)
		}
		before := bytes.Clone(body)
		if got := PresenceOwnerTraceOperation(body); got != want {
			t.Fatalf("op%d=%d want%d", op, got, want)
		}
		if !bytes.Equal(body, before) {
			t.Fatal("classifier changed payload")
		}
		if PresenceOwnerReadOnly(body) != (op == 9 || op == 10 || op == 11) {
			t.Fatal("operation declaration changed running cancellation")
		}
		if PresenceOwnerTraceOperation(append(body, 255)) != want {
			t.Fatal("header declaration pretended to validate body")
		}
	}
	valid := []byte{'W', 'K', 'V', 'P', 2, 11}
	for n := 0; n < len(valid); n++ {
		if PresenceOwnerTraceOperation(valid[:n]) != 0 {
			t.Fatal("truncated header classified")
		}
	}
	for _, bad := range [][]byte{{'X', 'K', 'V', 'P', 2, 11}, {'W', 'K', 'V', 'P', 1, 11}, {'W', 'K', 'V', 'P', 3, 11}} {
		if PresenceOwnerTraceOperation(bad) != 0 {
			t.Fatal("unknown header classified")
		}
	}
}
