package scripts_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestOfficialServerUsesManagedGoroutines keeps every first-party goroutine in
// the official cmd/wukongim process visible to the process-wide supervisor.
func TestOfficialServerUsesManagedGoroutines(t *testing.T) {
	root := repoRoot(t)
	scanRoots := []string{"cmd/wukongim", "internal", "pkg"}
	excludedPrefixes := []string{
		"internal/access/cloudview/",
		"internal/bench/",
		"internal/infra/cloudanalysis/",
		"internal/runtime/cloudviewstate/",
		"pkg/channel/testkit/",
		"pkg/client/",
	}
	approvedAntsPools := map[string]bool{
		"internal/runtime/channelappend/pool.go": true,
		"pkg/transport/internal/rpc/executor.go": true,
		"pkg/workqueue/bounded_batch_pool.go":    true,
		"pkg/workqueue/bounded_pool.go":          true,
		"pkg/workqueue/sharded_mailbox.go":       true,
	}

	var violations []string
	for _, scanRoot := range scanRoots {
		err := filepath.WalkDir(filepath.Join(root, scanRoot), func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			if hasPathPrefix(rel, excludedPrefixes) {
				return nil
			}

			fileSet := token.NewFileSet()
			file, err := parser.ParseFile(fileSet, path, nil, parser.SkipObjectResolution)
			if err != nil {
				return err
			}
			antsAliases := importAliases(file, "github.com/panjf2000/ants/v2")
			ast.Inspect(file, func(node ast.Node) bool {
				switch expression := node.(type) {
				case *ast.GoStmt:
					if rel != "pkg/goroutine/registry.go" {
						violations = append(violations, sourceViolation(fileSet, expression.Go, rel, "raw go statement"))
					}
				case *ast.CallExpr:
					selector, ok := expression.Fun.(*ast.SelectorExpr)
					if !ok {
						return true
					}
					if selector.Sel.Name == "Go" && rel != "pkg/goroutine/registry.go" {
						violations = append(violations, sourceViolation(fileSet, selector.Sel.Pos(), rel, "unmanaged .Go call"))
					}
					owner, ok := selector.X.(*ast.Ident)
					if ok && antsAliases[owner.Name] && strings.HasPrefix(selector.Sel.Name, "NewPool") && !approvedAntsPools[rel] {
						violations = append(violations, sourceViolation(fileSet, selector.Sel.Pos(), rel, "unregistered ants pool"))
					}
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatalf("scan %s: %v", scanRoot, err)
		}
	}
	if len(violations) > 0 {
		t.Fatalf("official server goroutines must use pkg/goroutine or an approved registered pool:\n%s", strings.Join(violations, "\n"))
	}
}

func hasPathPrefix(path string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

func importAliases(file *ast.File, importPath string) map[string]bool {
	aliases := make(map[string]bool)
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil || path != importPath {
			continue
		}
		name := filepath.Base(path)
		if spec.Name != nil {
			name = spec.Name.Name
		}
		aliases[name] = true
	}
	return aliases
}

func sourceViolation(fileSet *token.FileSet, position token.Pos, path, detail string) string {
	line := fileSet.Position(position).Line
	return fmt.Sprintf("%s:%d: %s", path, line, detail)
}
