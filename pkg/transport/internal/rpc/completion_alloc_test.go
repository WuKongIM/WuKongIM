//go:build !race

package rpc

import (
	"context"
	"testing"
	"unsafe"
)

func TestCompletionTerminalTransitionsDoNotAllocate(t *testing.T) {
	owners := make([]*Completion, 1002)
	for i := range owners {
		owners[i] = NewCompletion(context.Background())
	}
	response := Response{Payload: []byte("already copied")}
	index := 0
	allocs := testing.AllocsPerRun(1000, func() {
		owner := owners[index]
		index++
		owner.SendResult(nil)
		owner.Publish(response)
		owner.Resolve(nil)
	})
	if allocs != 0 {
		t.Fatalf("terminal transitions allocated %.2f objects", allocs)
	}
	t.Logf("completion_layout owner=%d response=%d result=%d; terminal_allocs=%.2f", unsafe.Sizeof(Completion{}), unsafe.Sizeof(Response{}), unsafe.Sizeof(CompletionResult{}), allocs)
}
