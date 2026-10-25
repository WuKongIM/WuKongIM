package scripts_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

var scriptTestGate = func(*testing.T) {}

func TestGateScriptTestSourceOnlyRoutesIntegrationFiles(t *testing.T) {
	original := scriptTestGate
	t.Cleanup(func() { scriptTestGate = original })
	calls := 0
	scriptTestGate = func(*testing.T) { calls++ }

	gateScriptTestSource(t, filepath.Join("scripts", "static_contract_test.go"))
	if calls != 0 {
		t.Fatalf("default test source gate calls = %d, want 0", calls)
	}
	gateScriptTestSource(t, filepath.Join("scripts", "real_process_integration_test.go"))
	if calls != 1 {
		t.Fatalf("integration test source gate calls = %d, want 1", calls)
	}
}

// These process fixtures must enter the bounded scheduler before their child
// cases allocate deadlines or processes; a child repoRoot call cannot admit them.
func TestChildCaseIntegrationEntriesAdmitBeforeSetup(t *testing.T) {
	root := repoRoot(t)
	for _, test := range []struct {
		file, entry string
	}{
		{"native_package_bootstrap_diagnostics_integration_test.go", "TestNativePackageBootstrapFailureDiagnostics"},
		{"cloud_deployment_execution_integration_test.go", "TestCloudDeploymentExecution"},
	} {
		t.Run(test.entry, func(t *testing.T) {
			positions := token.NewFileSet()
			file, err := parser.ParseFile(positions, filepath.Join(root, "scripts", test.file), nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			var entry *ast.FuncDecl
			for _, declaration := range file.Decls {
				if function, ok := declaration.(*ast.FuncDecl); ok && function.Name.Name == test.entry {
					entry = function
					break
				}
			}
			if entry == nil || entry.Body == nil {
				t.Fatal("integration entry is missing")
			}
			var admission token.Pos
			for _, statement := range entry.Body.List {
				assignment, ok := statement.(*ast.AssignStmt)
				if !ok || len(assignment.Rhs) != 1 {
					continue
				}
				call, ok := assignment.Rhs[0].(*ast.CallExpr)
				if !ok || len(call.Args) != 1 {
					continue
				}
				name, named := call.Fun.(*ast.Ident)
				argument, identified := call.Args[0].(*ast.Ident)
				if named && identified && name.Name == "repoRoot" && argument.Name == "t" {
					admission = call.Pos()
					break
				}
			}
			if !admission.IsValid() {
				t.Fatal("integration entry must resolve repoRoot(t) at top level before child-case setup")
			}
			ast.Inspect(entry.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				selector, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				receiver, ok := selector.X.(*ast.Ident)
				if !ok {
					return true
				}
				setup := receiver.Name == "t" && (selector.Sel.Name == "Run" || selector.Sel.Name == "TempDir") ||
					receiver.Name == "context" && strings.HasPrefix(selector.Sel.Name, "With") ||
					receiver.Name == "exec" && strings.HasPrefix(selector.Sel.Name, "Command")
				if setup && call.Pos() < admission {
					t.Errorf("%s: scheduler admission follows %s.%s", positions.Position(call.Pos()), receiver.Name, selector.Sel.Name)
				}
				return true
			})
		})
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func repoRoot(t *testing.T) string {
	t.Helper()
	if !strings.Contains(t.Name(), "/") {
		_, callerFile, _, callerOK := runtime.Caller(1)
		if !callerOK {
			t.Fatal("runtime.Caller failed for test source")
		}
		gateScriptTestSource(t, callerFile)
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), ".."))
}

// gateScriptTestSource reserves the bounded integration scheduler only for
// tests whose source file is explicitly integration-tagged. Default static
// contracts keep their ordinary serial unit-test scheduling under -tags=integration.
func gateScriptTestSource(t *testing.T, sourceFile string) {
	t.Helper()
	if strings.HasSuffix(filepath.Base(sourceFile), "_integration_test.go") {
		scriptTestGate(t)
	}
}

func writeFile(t *testing.T, path string, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}
