package internal_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

const modulePath = "github.com/WuKongIM/WuKongIM"

func TestInternalImportBoundaries(t *testing.T) {
	packages := listInternalPackages(t)
	forbidden := map[string][]string{
		modulePath + "/internal/runtime/": {
			modulePath + "/internal/access/",
			modulePath + "/internal/gateway/",
			modulePath + "/internal/usecase/",
			modulePath + "/internal/app",
		},
		modulePath + "/internal/usecase/": {
			modulePath + "/internal/access/",
			modulePath + "/internal/app",
		},
	}

	var violations []string
	for _, pkg := range packages {
		for sourcePrefix, forbiddenPrefixes := range forbidden {
			if !matchesImportPrefix(pkg.ImportPath, sourcePrefix) {
				continue
			}
			for _, imported := range pkg.Imports {
				for _, forbiddenPrefix := range forbiddenPrefixes {
					if matchesImportPrefix(imported, forbiddenPrefix) {
						violations = append(violations, fmt.Sprintf("%s imports %s", pkg.ImportPath, imported))
					}
				}
			}
		}
	}
	sort.Strings(violations)
	if len(violations) > 0 {
		t.Fatalf("internal import boundary violations:\n%s", strings.Join(violations, "\n"))
	}
}

type listedPackage struct {
	ImportPath string
	Imports    []string
}

func listInternalPackages(t *testing.T) []listedPackage {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Dir(filepath.Dir(file))
	cmd := exec.Command("go", "list", "-json", "./internal/...")
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "GOWORK=off")
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			t.Fatalf("go list failed: %v\n%s", err, exitErr.Stderr)
		}
		t.Fatalf("go list failed: %v", err)
	}

	decoder := json.NewDecoder(bytes.NewReader(out))
	var packages []listedPackage
	for decoder.More() {
		var pkg listedPackage
		if err := decoder.Decode(&pkg); err != nil {
			t.Fatalf("decode go list output: %v", err)
		}
		packages = append(packages, pkg)
	}
	return packages
}

func matchesImportPrefix(importPath, prefix string) bool {
	prefix = strings.TrimSuffix(prefix, "/")
	return importPath == prefix || strings.HasPrefix(importPath, prefix+"/")
}
