package scripts_test

import (
	"go/build/constraint"
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
		t.Errorf("%s must use both the _integration_test.go suffix and a positive integration build constraint", path)
	}
}

func findIntegrationTestTagViolations(root string, roots []string) ([]string, error) {
	var violations []string
	for _, name := range roots {
		err := filepath.Walk(filepath.Join(root, name), func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() || !strings.HasSuffix(info.Name(), "_test.go") {
				return nil
			}
			source, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			firstLine, _, _ := strings.Cut(string(source), "\n")
			requiresIntegration := false
			if strings.HasPrefix(firstLine, "//go:build ") {
				expr, err := constraint.Parse(firstLine)
				if err != nil {
					return err
				}
				requiresIntegration = buildConstraintRequiresTag(expr, "integration")
			}
			namedIntegration := strings.HasSuffix(info.Name(), "_integration_test.go")
			if namedIntegration != requiresIntegration {
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

func buildConstraintRequiresTag(expr constraint.Expr, tag string) bool {
	switch expr := expr.(type) {
	case *constraint.TagExpr:
		return expr.Tag == tag
	case *constraint.AndExpr:
		return buildConstraintRequiresTag(expr.X, tag) || buildConstraintRequiresTag(expr.Y, tag)
	case *constraint.OrExpr:
		return buildConstraintRequiresTag(expr.X, tag) && buildConstraintRequiresTag(expr.Y, tag)
	case *constraint.NotExpr:
		return false
	default:
		return false
	}
}

func TestFindIntegrationTestTagViolationsRejectsMismatchedNameAndBuildTag(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "pkg", "example")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"missing_integration_test.go":  "package example\n",
		"negative_integration_test.go": "//go:build !integration\n\npackage example\n",
		"typo_integration_test.go":     "//go:build integration_typo\n\npackage example\n",
		"hidden_test.go":               "//go:build integration\n\npackage example\n",
		"valid_integration_test.go":    "//go:build integration && !windows\n\npackage example\n",
	}
	for name, source := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	violations, err := findIntegrationTestTagViolations(root, []string{"pkg"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"pkg/example/hidden_test.go",
		"pkg/example/missing_integration_test.go",
		"pkg/example/negative_integration_test.go",
		"pkg/example/typo_integration_test.go",
	}
	if strings.Join(violations, "\n") != strings.Join(want, "\n") {
		t.Fatalf("violations = %v, want %v", violations, want)
	}
}
