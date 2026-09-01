//go:build integration

package scripts_test

import (
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
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("signed native package repository validation failed: %v\n%s", err, output)
	}
}
