package delivery

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"testing"
)

// TestLegacyFanoutSurfaceRemoved keeps the canonical Runtime as the only
// exported online-delivery execution module.
func TestLegacyFanoutSurfaceRemoved(t *testing.T) {
	t.Parallel()

	forbidden := map[string]struct{}{
		"Manager":                         {},
		"ManagerOptions":                  {},
		"NewManager":                      {},
		"Planner":                         {},
		"PlannerOptions":                  {},
		"NewPlanner":                      {},
		"FanoutWorker":                    {},
		"FanoutWorkerOptions":             {},
		"NewFanoutWorker":                 {},
		"FanoutTaskRouter":                {},
		"FanoutTaskRouterOptions":         {},
		"NewFanoutTaskRouter":             {},
		"RetryScheduler":                  {},
		"RetrySchedulerOptions":           {},
		"NewRetryScheduler":               {},
		"ChannelSubscriberPlanner":        {},
		"ChannelSubscriberPlannerOptions": {},
		"NewChannelSubscriberPlanner":     {},
		"Partition":                       {},
		"FanoutTask":                      {},
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	files := token.NewFileSet()
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" || filepath.Base(entry.Name()) == "legacy_surface_test.go" {
			continue
		}
		parsed, err := parser.ParseFile(files, entry.Name(), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", entry.Name(), err)
		}
		for _, declaration := range parsed.Decls {
			switch declaration := declaration.(type) {
			case *ast.FuncDecl:
				if _, found := forbidden[declaration.Name.Name]; found {
					t.Errorf("legacy delivery declaration %s remains in %s", declaration.Name.Name, entry.Name())
				}
			case *ast.GenDecl:
				for _, spec := range declaration.Specs {
					typeSpec, ok := spec.(*ast.TypeSpec)
					if !ok {
						continue
					}
					if _, found := forbidden[typeSpec.Name.Name]; found {
						t.Errorf("legacy delivery declaration %s remains in %s", typeSpec.Name.Name, entry.Name())
					}
				}
			}
		}
	}
}
