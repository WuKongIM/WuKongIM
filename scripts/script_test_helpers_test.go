package scripts_test

import (
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
