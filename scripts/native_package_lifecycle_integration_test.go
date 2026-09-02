//go:build integration

package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestNativePackageLifecycle(t *testing.T) {
	if os.Getenv("WK_NATIVE_PACKAGE_LIFECYCLE_INTEGRATION") != "1" {
		t.Skip("set WK_NATIVE_PACKAGE_LIFECYCLE_INTEGRATION=1 to run the native package lifecycle validation")
	}

	image := strings.TrimSpace(os.Getenv("WK_NATIVE_PACKAGE_LIFECYCLE_IMAGE"))
	format := strings.TrimSpace(os.Getenv("WK_NATIVE_PACKAGE_LIFECYCLE_FORMAT"))
	if image == "" || format == "" {
		t.Fatal("WK_NATIVE_PACKAGE_LIFECYCLE_IMAGE and WK_NATIVE_PACKAGE_LIFECYCLE_FORMAT are required")
	}

	runHeavyShellScriptTestInParallel(t)
	root := repoRoot(t)
	command := exec.CommandContext(
		t.Context(),
		"bash",
		filepath.Join(root, "scripts", "validate-native-package-lifecycle-container.sh"),
		image,
		format,
	)
	command.Dir = root
	command.Env = os.Environ()
	command.Cancel = func() error {
		return command.Process.Signal(syscall.SIGTERM)
	}
	command.WaitDelay = 3 * time.Minute
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("native package lifecycle validation failed: %v\n%s", err, output)
	}
}
