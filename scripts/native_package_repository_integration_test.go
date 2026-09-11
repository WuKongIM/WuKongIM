//go:build integration

package scripts_test

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestNativePackageSignedRepository(t *testing.T) {
	if os.Getenv("WK_NATIVE_PACKAGE_REPOSITORY_INTEGRATION") != "1" {
		t.Skip("set WK_NATIVE_PACKAGE_REPOSITORY_INTEGRATION=1 to run the signed repository container validation")
	}

	runHeavyShellScriptTestInParallel(t)
	root := repoRoot(t)
	command := exec.Command(
		"bash",
		filepath.Join(root, "scripts", "validate-native-package-repositories-container.sh"),
	)
	command.Dir = root
	command.Env = os.Environ()
	// Forward output to the test process so the go command retains diagnostics
	// even if the package deadline panics before this child command returns.
	// Keep an exec-owned pipe rather than letting descendants inherit the go
	// command's output descriptor and hold it open after the test exits.
	output := io.MultiWriter(os.Stdout)
	command.Stdout = output
	command.Stderr = output
	if err := command.Run(); err != nil {
		t.Fatalf("signed native package repository validation failed: %v", err)
	}
}
