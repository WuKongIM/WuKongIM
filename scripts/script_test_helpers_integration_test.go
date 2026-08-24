//go:build integration

package scripts_test

import (
	"sync"
	"testing"
)

var heavyShellScriptTestSlots = make(chan struct{}, 2)
var parallelizedShellTests sync.Map
var timingSensitiveShellScriptTests sync.RWMutex

func init() {
	scriptTestGate = runHeavyShellScriptTestInParallel
}

func runHeavyShellScriptTestInParallel(t *testing.T) {
	t.Helper()
	if _, alreadyParallel := parallelizedShellTests.LoadOrStore(t, struct{}{}); alreadyParallel {
		return
	}
	t.Cleanup(func() { parallelizedShellTests.Delete(t) })
	t.Parallel()
	heavyShellScriptTestSlots <- struct{}{}
	t.Cleanup(func() { <-heavyShellScriptTestSlots })
	timingSensitiveShellScriptTests.RLock()
	t.Cleanup(timingSensitiveShellScriptTests.RUnlock)
}

func runTimingSensitiveShellScriptTestExclusively(t *testing.T) {
	t.Helper()
	if _, alreadyParallel := parallelizedShellTests.LoadOrStore(t, struct{}{}); alreadyParallel {
		return
	}
	t.Cleanup(func() { parallelizedShellTests.Delete(t) })
	t.Parallel()
	timingSensitiveShellScriptTests.Lock()
	t.Cleanup(timingSensitiveShellScriptTests.Unlock)
}

// runTimingSensitiveShellScriptTestBeforeParallelPhase keeps one long real
// workload in Go's serial test phase. Marking it here also prevents repoRoot
// from installing the ordinary t.Parallel gate later in the test body.
func runTimingSensitiveShellScriptTestBeforeParallelPhase(t *testing.T) {
	t.Helper()
	if _, alreadyScheduled := parallelizedShellTests.LoadOrStore(t, struct{}{}); alreadyScheduled {
		t.Fatal("timing-sensitive serial gate must run before repoRoot")
	}
	t.Cleanup(func() { parallelizedShellTests.Delete(t) })
}
