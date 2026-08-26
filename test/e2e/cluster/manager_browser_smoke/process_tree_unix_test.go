//go:build e2e && (aix || android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris)

package manager_browser_smoke

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRunBrowserCommandReapsTermIgnoringDescendantAfterTimeout(t *testing.T) {
	rootDir := t.TempDir()
	descendantPIDPath := filepath.Join(rootDir, "descendant.pid")
	readyPath := filepath.Join(rootDir, "descendant.ready")
	cmd := exec.Command("/bin/sh", "-c", `
/bin/sh -c 'trap "" TERM; echo $$ > "$DESCENDANT_PID_PATH"; : > "$DESCENDANT_READY_PATH"; exec sleep 30' &
while [ ! -f "$DESCENDANT_READY_PATH" ]; do :; done
wait
`)
	cmd.Env = append(os.Environ(),
		"DESCENDANT_PID_PATH="+descendantPIDPath,
		"DESCENDANT_READY_PATH="+readyPath,
	)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	err := runBrowserCommand(ctx, cmd, 100*time.Millisecond)
	require.ErrorIs(t, err, context.DeadlineExceeded)

	descendantPIDData, readErr := os.ReadFile(descendantPIDPath)
	require.NoError(t, readErr)
	descendantPID, parseErr := strconv.Atoi(strings.TrimSpace(string(descendantPIDData)))
	require.NoError(t, parseErr)
	t.Cleanup(func() { _ = syscall.Kill(descendantPID, syscall.SIGKILL) })
	require.Eventually(t, func() bool {
		return errors.Is(syscall.Kill(descendantPID, 0), syscall.ESRCH)
	}, time.Second, 10*time.Millisecond, "browser descendant pid %d survived timeout cleanup", descendantPID)
}
