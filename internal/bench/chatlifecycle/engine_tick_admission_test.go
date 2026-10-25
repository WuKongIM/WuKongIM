package chatlifecycle

import (
	"bytes"
	"runtime"
	"strings"
	"testing"
	"time"
)

// engineTestCurrentGoroutineHeader identifies one test goroutine without relying
// on an argument address or a source line in a runtime stack dump.
func engineTestCurrentGoroutineHeader() string {
	var stack [128]byte
	n := runtime.Stack(stack[:], false)
	header, _, _ := strings.Cut(string(stack[:n]), " [")
	return header + " ["
}

// waitEngineTestTickOnStepMu observes real Tick admission without a product hook.
// The caller holds stepMu, so a Tick blocked there has already joined tickOps.
func waitEngineTestTickOnStepMu(t *testing.T, header string) {
	t.Helper()
	stacks := make([]byte, 1<<20)
	deadline := time.Now().Add(time.Second)
	var target string
	for {
		n := runtime.Stack(stacks, true)
		if n == len(stacks) && len(stacks) < 8<<20 {
			stacks = make([]byte, len(stacks)*2)
			continue
		}
		for _, stack := range bytes.Split(stacks[:n], []byte("\n\n")) {
			if !bytes.HasPrefix(stack, []byte(header)) {
				continue
			}
			target = string(stack)
			if bytes.Contains(stack, []byte("(*Engine).Tick(")) &&
				bytes.Contains(stack, []byte("sync.(*Mutex).Lock")) &&
				!bytes.Contains(stack, []byte("(*Engine).beginTickOp(")) {
				return
			}
		}
		if !time.Now().Before(deadline) {
			t.Fatalf("Tick did not reach its admitted stepMu boundary (stack truncated=%t); target stack:\n%s", n == len(stacks), target)
		}
		runtime.Gosched()
	}
}
