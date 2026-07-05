package scripts_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestDevelopmentDocsUsePromotedPackagePaths(t *testing.T) {
	root := repoRoot(t)
	docsDir := filepath.Join(root, "docs", "development")
	forbidden := []string{
		"pkg/channelv2",
		"pkg/transportv2",
		"pkg/clusterv2",
		"pkg/controllerv2",
		"channelv2/",
		"transportv2/",
		"clusterv2/",
		"controllerv2/",
	}

	var violations []string
	err := filepath.WalkDir(docsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "perf-runs" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".md" {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(body)
		for _, needle := range forbidden {
			if strings.Contains(text, needle) {
				rel, _ := filepath.Rel(root, path)
				violations = append(violations, rel+" contains "+needle)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan development docs: %v", err)
	}
	if len(violations) > 0 {
		t.Fatalf("development docs should use promoted package paths:\n%s", strings.Join(violations, "\n"))
	}
}

func TestAgentsDirectoryStructureUsesPromotedPackages(t *testing.T) {
	root := repoRoot(t)
	agents := readFile(t, filepath.Join(root, "AGENTS.md"))

	for _, dir := range []string{
		"pkg/channel",
		"pkg/cluster",
		"pkg/controller",
		"pkg/transport",
		"pkg/legacy/channel",
		"pkg/legacy/cluster",
		"pkg/legacy/controller",
		"pkg/legacy/transport",
	} {
		if _, err := os.Stat(filepath.Join(root, dir)); err != nil {
			t.Fatalf("expected promoted directory %s to exist: %v", dir, err)
		}
	}
	for _, stale := range []string{
		"pkg/channelv2",
		"pkg/clusterv2",
		"pkg/controllerv2",
		"pkg/transportv2",
		"internalv2/",
		"cmd/wukongimv2",
	} {
		if strings.Contains(agents, stale) {
			t.Fatalf("AGENTS.md should not list stale promoted directory %q", stale)
		}
		if _, err := os.Stat(filepath.Join(root, stale)); err == nil {
			t.Fatalf("stale promoted directory %q should not exist", stale)
		}
	}
}

func TestSlotProxyLegacyFallbackIsDocumented(t *testing.T) {
	root := repoRoot(t)
	for _, path := range []string{
		filepath.Join(root, "docs", "development", "PROJECT_KNOWLEDGE.md"),
		filepath.Join(root, "pkg", "slot", "FLOW.md"),
		filepath.Join(root, "internal", "legacy", "FLOW.md"),
	} {
		text := readFile(t, path)
		for _, want := range []string{"internal/legacy/app/slot_proxy_rpc.go", "Store.RegisterRPCHandlers"} {
			if !strings.Contains(text, want) {
				rel, _ := filepath.Rel(root, path)
				t.Fatalf("%s should document %s as the legacy fallback registration point", rel, want)
			}
		}
	}
}

func TestGoImportsUsePromotedPackagePaths(t *testing.T) {
	root := repoRoot(t)
	forbiddenPrefixes := []string{
		"github.com/WuKongIM/WuKongIM/pkg/channelv2",
		"github.com/WuKongIM/WuKongIM/pkg/clusterv2",
		"github.com/WuKongIM/WuKongIM/pkg/controllerv2",
		"github.com/WuKongIM/WuKongIM/pkg/transportv2",
	}

	var violations []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".worktrees", "data", "tmp", "node_modules", "web":
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range file.Imports {
			importPath, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				return err
			}
			for _, prefix := range forbiddenPrefixes {
				if importPath == prefix || strings.HasPrefix(importPath, prefix+"/") {
					rel, _ := filepath.Rel(root, path)
					violations = append(violations, rel+" imports "+importPath)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan go imports: %v", err)
	}
	if len(violations) > 0 {
		t.Fatalf("Go imports should use promoted package paths:\n%s", strings.Join(violations, "\n"))
	}
}

func TestPromotedProductionCodeDoesNotImportLegacyPackages(t *testing.T) {
	root := repoRoot(t)
	scanRoots := []string{
		"cmd/wukongim",
		"internal",
		"pkg",
		"test/e2e",
	}
	forbiddenPrefixes := []string{
		"github.com/WuKongIM/WuKongIM/internal/legacy",
		"github.com/WuKongIM/WuKongIM/pkg/legacy",
	}

	var violations []string
	for _, scanRoot := range scanRoots {
		err := filepath.WalkDir(filepath.Join(root, scanRoot), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			if d.IsDir() {
				switch rel {
				case "internal/legacy", "pkg/legacy":
					return filepath.SkipDir
				}
				return nil
			}
			if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			for _, imp := range file.Imports {
				importPath, err := strconv.Unquote(imp.Path.Value)
				if err != nil {
					return err
				}
				for _, prefix := range forbiddenPrefixes {
					if importPath == prefix || strings.HasPrefix(importPath, prefix+"/") {
						if isDocumentedLegacyBridgeImport(rel, importPath) {
							continue
						}
						violations = append(violations, rel+" imports "+importPath)
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("scan %s: %v", scanRoot, err)
		}
	}
	if len(violations) > 0 {
		t.Fatalf("promoted production code should not import legacy packages outside documented bridges:\n%s", strings.Join(violations, "\n"))
	}
}

func isDocumentedLegacyBridgeImport(rel, importPath string) bool {
	return false
}
