//go:build integration

package scripts_test

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

var heavyShellScriptTestSlots = make(chan struct{}, 2)
var parallelizedShellTests sync.Map
var timingSensitiveShellScriptTests sync.RWMutex

func init() {
	scriptTestGate = runHeavyShellScriptTestInParallel
	cleanupScriptIntegrationBinaries = func() error {
		var result error
		for _, binary := range []*scriptSmokeBinary{&wkcliSmoke, &repairSmoke} {
			if binary.directory != "" {
				result = errors.Join(result, os.RemoveAll(binary.directory))
			}
		}
		return result
	}
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

// scriptSmokeBinary lazily builds one fixed CLI from the suite's immutable
// checkout. Only smoke cases share it; source-rebuild tests keep fresh builds.
// TestMain removes its private directory after every test consumer has joined.
type scriptSmokeBinary struct {
	// The target and flags are fixed for this process-owned fixture.
	packagePath string
	flags       []string
	once        sync.Once
	sourceRoot  string
	directory   string
	path        string
	err         error
}

var (
	wkcliSmoke  = scriptSmokeBinary{packagePath: "./cmd/wkcli"}
	repairSmoke = scriptSmokeBinary{packagePath: "./cmd/wkchatlifecycle", flags: []string{"-trimpath"}}
)

// Path returns the one immutable CLI after its build joins, rejecting callers
// from a different checkout instead of serving a stale source binary.
func (binary *scriptSmokeBinary) Path(t *testing.T, root string) string {
	t.Helper()
	binary.once.Do(func() {
		binary.sourceRoot = root
		binary.directory, binary.err = os.MkdirTemp("", "wukongim-script-smoke-*")
		if binary.err != nil {
			return
		}
		binary.path = filepath.Join(binary.directory, filepath.Base(binary.packagePath))
		args := append([]string{"build"}, binary.flags...)
		args = append(args, "-o", binary.path, binary.packagePath)
		build := exec.Command(goTool(t), args...)
		build.Dir = root
		build.Env = append(os.Environ(), "GOWORK=off")
		if output, err := build.CombinedOutput(); err != nil {
			binary.err = fmt.Errorf("build smoke CLI: %w\n%s", err, output)
			return
		}
		binary.err = os.Chmod(binary.path, 0o555)
	})
	if binary.err != nil {
		t.Fatal(binary.err)
	}
	if binary.sourceRoot != root {
		t.Fatal("smoke CLI cannot be reused across source checkouts")
	}
	return binary.path
}

// goTool selects the toolchain that compiled this test process.
func goTool(t *testing.T) string {
	t.Helper()
	if goroot := runtime.GOROOT(); goroot != "" {
		return filepath.Join(goroot, "bin", "go")
	}
	return "go"
}
