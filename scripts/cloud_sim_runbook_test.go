package scripts_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCloudSimulationRunbooksAvoidMaintainerLocalPaths guards the public
// runbooks against author-specific absolute paths such as a maintainer-local
// Go SDK directory, which makes the documented commands non-copyable on other
// machines.
func TestCloudSimulationRunbooksAvoidMaintainerLocalPaths(t *testing.T) {
	runbooksDir := filepath.Join(repoRoot(t), "docs", "superpowers", "runbooks")
	entries, err := os.ReadDir(runbooksDir)
	if err != nil {
		t.Fatalf("read runbooks dir: %v", err)
	}

	forbidden := []string{"/Users/", "sdk/go1.26.4"}
	var violations []string
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
			continue
		}
		path := filepath.Join(runbooksDir, entry.Name())
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read runbook %s: %v", entry.Name(), err)
		}
		text := string(body)
		for _, needle := range forbidden {
			if strings.Contains(text, needle) {
				violations = append(violations, entry.Name()+" contains maintainer-local path "+needle)
			}
		}
	}
	if len(violations) > 0 {
		t.Fatalf("cloud simulation runbooks must use the caller's PATH-configured go executable:\n%s", strings.Join(violations, "\n"))
	}
}
