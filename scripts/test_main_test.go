package scripts_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestMain provides immutable source Codex credentials for shell-script tests.
// Individual tests still receive isolated runtime homes from analyze.sh, while
// avoiding process-global t.Setenv calls that would serialize otherwise
// independent black-box scenarios.
func TestMain(m *testing.M) {
	sourceCodexHome, err := os.MkdirTemp("", "wukongim-scripts-codex-home-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "create scripts test CODEX_HOME: %v\n", err)
		os.Exit(2)
	}

	files := []struct {
		name    string
		content string
	}{
		{name: "auth.json", content: "{\"auth_mode\":\"chatgpt\"}\n"},
		{name: "models_cache.json", content: "{\"client_version\":\"newer-incompatible\"}\n"},
		{name: "config.toml", content: "model = \"must-not-leak\"\n"},
	}
	for _, file := range files {
		if err := os.WriteFile(filepath.Join(sourceCodexHome, file.name), []byte(file.content), 0o600); err != nil {
			fmt.Fprintf(os.Stderr, "write scripts test CODEX_HOME/%s: %v\n", file.name, err)
			os.Exit(2)
		}
	}
	if err := os.Setenv("CODEX_HOME", sourceCodexHome); err != nil {
		fmt.Fprintf(os.Stderr, "set scripts test CODEX_HOME: %v\n", err)
		os.Exit(2)
	}
	code := m.Run()
	if err := os.RemoveAll(sourceCodexHome); err != nil && code == 0 {
		fmt.Fprintf(os.Stderr, "remove scripts test CODEX_HOME: %v\n", err)
		code = 2
	}
	os.Exit(code)
}
