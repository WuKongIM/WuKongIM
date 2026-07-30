package scripts_test

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

var defaultGoTestRoots = []string{"cmd", "internal", "pkg", "scripts", "docker"}

func assertRepositoryIntegrationTestFilesUseBuildTag(t *testing.T) {
	t.Helper()
	violations, err := findIntegrationTestTagViolations(repoRoot(t), defaultGoTestRoots)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range violations {
		t.Errorf("%s must start with the integration build constraint", path)
	}
}

func findIntegrationTestTagViolations(root string, roots []string) ([]string, error) {
	var violations []string
	for _, name := range roots {
		err := filepath.Walk(filepath.Join(root, name), func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() || !strings.HasSuffix(info.Name(), "_integration_test.go") {
				return nil
			}
			source, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			firstLine, _, _ := strings.Cut(string(source), "\n")
			if !strings.HasPrefix(firstLine, "//go:build ") || !strings.Contains(firstLine, "integration") {
				relativePath, err := filepath.Rel(root, path)
				if err != nil {
					return err
				}
				violations = append(violations, filepath.ToSlash(relativePath))
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(violations)
	return violations, nil
}

func TestFindIntegrationTestTagViolationsRejectsMissingBuildTag(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "pkg", "example", "slow_integration_test.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("package example\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	violations, err := findIntegrationTestTagViolations(root, []string{"pkg"})
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 1 || violations[0] != "pkg/example/slow_integration_test.go" {
		t.Fatalf("violations = %v, want the untagged integration test", violations)
	}
}
