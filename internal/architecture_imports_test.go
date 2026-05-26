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
	ImportPath   string
	Imports      []string
	TestImports  []string
	XTestImports []string
}

func listInternalPackages(t *testing.T) []listedPackage {
	t.Helper()
	return listPackages(t, "./internal/...")
}

func listPackages(t *testing.T, patterns ...string) []listedPackage {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Dir(filepath.Dir(file))
	args := append([]string{"list", "-json"}, patterns...)
	cmd := exec.Command("go", args...)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "GOWORK=off")
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			t.Fatalf("go %s failed: %v\n%s", strings.Join(args, " "), err, exitErr.Stderr)
		}
		t.Fatalf("go %s failed: %v", strings.Join(args, " "), err)
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

func allImports(pkg listedPackage) []string {
	imports := make([]string, 0, len(pkg.Imports)+len(pkg.TestImports)+len(pkg.XTestImports))
	imports = append(imports, pkg.Imports...)
	imports = append(imports, pkg.TestImports...)
	imports = append(imports, pkg.XTestImports...)
	return imports
}

func TestPkgGatewayDoesNotImportInternalPackages(t *testing.T) {
	packages := listPackages(t, "./pkg/gateway/...")
	var violations []string
	for _, pkg := range packages {
		for _, imported := range allImports(pkg) {
			if matchesImportPrefix(imported, modulePath+"/internal/") {
				violations = append(violations, fmt.Sprintf("%s imports %s", pkg.ImportPath, imported))
			}
		}
	}
	sort.Strings(violations)
	if len(violations) > 0 {
		t.Fatalf("pkg/gateway import boundary violations:\n%s", strings.Join(violations, "\n"))
	}
}

func matchesImportPrefix(importPath, prefix string) bool {
	prefix = strings.TrimSuffix(prefix, "/")
	return importPath == prefix || strings.HasPrefix(importPath, prefix+"/")
}
