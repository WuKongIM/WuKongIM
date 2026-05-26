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
	deps := listDeps(t, repoRoot, "./cmd/wkbench", "./internal/bench/...")
	for _, dep := range deps {
		if forbidden, ok := forbiddenBenchImport(dep); ok {
			t.Fatalf("wkbench imports forbidden dependency %s via %s\n%s", forbidden, dep, strings.Join(deps, "\n"))
		}
	}
}

func TestForbiddenBenchImportCatchesExactRootAndChildren(t *testing.T) {
	for _, dep := range []string{
		"github.com/WuKongIM/WuKongIM/internal/app",
		"github.com/WuKongIM/WuKongIM/internal/app/lifecycle",
	} {
		forbidden, ok := forbiddenBenchImport(dep)
		if !ok {
			t.Fatalf("expected %s to be forbidden", dep)
		}
		if forbidden != "github.com/WuKongIM/WuKongIM/internal/app" {
			t.Fatalf("unexpected forbidden root %s", forbidden)
		}
	}
}

func TestForbiddenBenchImportAllowsPrefixLookalikes(t *testing.T) {
	for _, dep := range []string{
		"github.com/WuKongIM/WuKongIM/internal/application",
		"github.com/WuKongIM/WuKongIM/pkg/gateway",
		"github.com/WuKongIM/WuKongIM/pkg/gateway/testkit",
		"github.com/WuKongIM/WuKongIM/pkg/clustering",
	} {
		if forbidden, ok := forbiddenBenchImport(dep); ok {
			t.Fatalf("expected %s to be allowed, matched %s", dep, forbidden)
		}
	}
}

func listDeps(t *testing.T, repoRoot string, patterns ...string) []string {
	t.Helper()
	args := append([]string{"list", "-deps"}, patterns...)
	cmd := exec.Command("go", args...)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "GOWORK=off")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.Fields(string(out))
}

func forbiddenBenchImport(dep string) (string, bool) {
	forbidden := []string{
		"github.com/WuKongIM/WuKongIM/internal/app",
		"github.com/WuKongIM/WuKongIM/internal/access",
		"github.com/WuKongIM/WuKongIM/internal/usecase",
		"github.com/WuKongIM/WuKongIM/internal/runtime",
		"github.com/WuKongIM/WuKongIM/pkg/slot",
		"github.com/WuKongIM/WuKongIM/pkg/controller",
		"github.com/WuKongIM/WuKongIM/pkg/cluster",
	}
	for _, prefix := range forbidden {
		if dep == prefix || strings.HasPrefix(dep, prefix+"/") {
			return prefix, true
		}
	}
	return "", false
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
