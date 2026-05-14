package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWkbenchDoesNotImportServerInternals(t *testing.T) {
	repoRoot := findRepoRoot(t)
	cmd := exec.Command("go", "list", "-deps", "./cmd/wkbench")
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "GOWORK=off")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	forbidden := []string{"/internal/app", "/internal/access/", "/internal/usecase/", "/internal/runtime/", "/internal/gateway/", "/pkg/slot/", "/pkg/controller/", "/pkg/cluster/"}
	deps := string(out)
	for _, item := range forbidden {
		if strings.Contains(deps, item) {
			t.Fatalf("wkbench imports forbidden dependency %s\n%s", item, deps)
		}
	}
}

func findRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	requireNoError(t, err)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("go.mod not found from %s", dir)
		}
		dir = parent
	}
}

func requireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
