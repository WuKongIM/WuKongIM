//go:build integration

package scripts_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// A package-test timeout must preserve the last container output, even when
// the shell command has not returned. The fixture never starts real Docker.
func TestNativePackageRepositoryKeepsTimeoutOutput(t *testing.T) {
	root := t.TempDir()
	packages := filepath.Join(root, "packages")
	bin := filepath.Join(root, "bin")
	for _, dir := range []string{packages, bin} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"wukongim-test.deb", "wukongim-test.rpm"} {
		if err := os.WriteFile(filepath.Join(packages, name), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	const marker = "native-repository-fixture-download-started"
	// Sleep only in this integration fixture, long enough for the child test's
	// deadline; the failed fake Docker exits without creating resources.
	fake := "#!/bin/sh\nprintf '%s\\n' '" + marker + "'\nsleep 0.5\nexit 23\n"
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(fake), 0700); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(executable, "-test.run=^TestNativePackageSignedRepository$", "-test.timeout=250ms")
	command.Env = append(os.Environ(), "WK_NATIVE_PACKAGE_REPOSITORY_INTEGRATION=1", "WK_NATIVE_PACKAGE_DIST_DIR="+packages, "TMPDIR="+root, "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	command.WaitDelay = 2 * time.Second
	output, err := command.CombinedOutput()
	if err == nil || !bytes.Contains(output, []byte("panic: test timed out")) {
		t.Fatalf("fixture did not exercise a test deadline: %v\n%s", err, output)
	}
	if !bytes.Contains(output, []byte(marker)) {
		t.Fatalf("test timeout discarded container diagnostics:\n%s", output)
	}
}
