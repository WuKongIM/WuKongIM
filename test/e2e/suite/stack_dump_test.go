//go:build e2e

package suite

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const fakeGoroutineDump = `SIGQUIT: quit

goroutine 1 [semacquire, 2 minutes]:
sync.runtime_SemacquireMutex(0xc000010000?, 0x0?, 0x1?)
	/usr/local/go/src/runtime/sema.go:95 +0x25
github.com/WuKongIM/WuKongIM/internal/app.(*App).Start(0xc0001a2000)
	/src/internal/app/app.go:120 +0x45

goroutine 7 [semacquire]:
sync.runtime_SemacquireMutex(0xc000010000?, 0x0?, 0x1?)
	/usr/local/go/src/runtime/sema.go:95 +0x25
github.com/WuKongIM/WuKongIM/internal/app.(*App).Start(0xc0001a2000)
	/src/internal/app/app.go:120 +0x45

goroutine 9 [select]:
net/http.(*Server).Serve(0xc000200000, {0x0, 0x0})
	/usr/local/go/src/net/http/server.go:3300 +0x30
`

// Ways stack capture could mislead or hurt a failing run:
//   - the stuck node's stack is not captured, or not summarized in diagnostics;
//   - a node that already exited is signaled or reported as captured;
//   - a node ignoring SIGQUIT blocks the harness beyond the bounded wait;
//   - the summary grows without bound and floods CI logs.
func TestSummarizeGoroutineDumpGroupsByStateAndProductFrame(t *testing.T) {
	summary := summarizeGoroutineDump([]byte(fakeGoroutineDump), 10)

	require.Equal(t, []string{
		"   2  [semacquire]  github.com/WuKongIM/WuKongIM/internal/app.(*App).Start",
		"   1  [select]  net/http.(*Server).Serve",
	}, summary)
	require.Len(t, summarizeGoroutineDump([]byte(fakeGoroutineDump), 1), 2, "one group plus an omitted-groups line")
}

func quitDumpingProcess(t *testing.T, ignoreQuit bool) *NodeProcess {
	t.Helper()
	readyPath := filepath.Join(t.TempDir(), "ready")
	trap := `trap 'cat "$DUMP" >&2; exit 2' QUIT`
	if ignoreQuit {
		trap = `trap '' QUIT`
	}
	dumpPath := filepath.Join(t.TempDir(), "dump.txt")
	require.NoError(t, os.WriteFile(dumpPath, []byte(fakeGoroutineDump), 0o600))
	cmd := exec.Command("/bin/sh", "-c", trap+`; : > "$READY_FILE"; while :; do sleep 0.05; done`)
	cmd.Env = append(os.Environ(), "READY_FILE="+readyPath, "DUMP="+dumpPath)
	process := newCommandNodeProcess(t, cmd)
	process.StopTimeout = 100 * time.Millisecond
	require.NoError(t, process.Start())
	t.Cleanup(func() { _ = process.Stop() })
	require.Eventually(t, func() bool {
		_, err := os.Stat(readyPath)
		return err == nil
	}, 2*time.Second, 10*time.Millisecond)
	return process
}

func TestCaptureGoroutineDumpWritesFileAndDiagnosticsSummary(t *testing.T) {
	process := quitDumpingProcess(t, false)

	path, err := process.CaptureGoroutineDump()

	require.NoError(t, err)
	require.Equal(t, goroutineDumpPath(process.Spec), path)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(data), "goroutine 7 [semacquire]:")
	require.Eventually(t, func() bool { return !process.Running() }, 2*time.Second, 10*time.Millisecond)
	diagnostics := (&NodeProcess{Spec: process.Spec}).DumpDiagnostics()
	require.Contains(t, diagnostics, "goroutine-dump: "+path)
	require.Contains(t, diagnostics, "[semacquire]  github.com/WuKongIM/WuKongIM/internal/app.(*App).Start")
}

func TestCaptureGoroutineDumpRejectsExitedProcess(t *testing.T) {
	process := newCommandNodeProcess(t, exec.Command("/bin/sh", "-c", "exit 0"))
	require.NoError(t, process.Start())
	_ = process.Wait()

	_, err := process.CaptureGoroutineDump()

	require.Error(t, err)
	_, statErr := os.Stat(goroutineDumpPath(process.Spec))
	require.True(t, os.IsNotExist(statErr))
}

func TestCaptureGoroutineDumpWaitIsBounded(t *testing.T) {
	previous := goroutineDumpWait
	goroutineDumpWait = 200 * time.Millisecond
	t.Cleanup(func() { goroutineDumpWait = previous })
	process := quitDumpingProcess(t, true)

	started := time.Now()
	_, err := process.CaptureGoroutineDump()

	require.Error(t, err)
	require.Less(t, time.Since(started), 2*time.Second)
}

func TestClusterHTTPReadinessFailureCapturesStuckNodeStack(t *testing.T) {
	process := quitDumpingProcess(t, false)
	process.Spec.ID = 3
	process.Spec.APIAddr = "127.0.0.1:1"
	cluster := &StartedCluster{Nodes: []StartedNode{{Spec: process.Spec, Process: process}}}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	err := cluster.WaitHTTPReady(ctx)

	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "node 3 http not ready"), err.Error())
	_, statErr := os.Stat(goroutineDumpPath(process.Spec))
	require.NoError(t, statErr, "stuck node stack must be captured on readiness timeout")
}
