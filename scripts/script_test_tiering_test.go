package scripts_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func assertScriptsIntegrationTestFilesUseBuildTag(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join(repoRoot(t), "scripts", "*_integration_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("scripts integration test inventory is empty")
	}
	for _, path := range paths {
		name := filepath.Base(path)
		source, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		firstLine, _, _ := strings.Cut(string(source), "\n")
		if !strings.HasPrefix(firstLine, "//go:build ") || !strings.Contains(firstLine, "integration") {
			t.Errorf("%s must start with the integration build constraint", name)
		}
	}
}
