package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Pre-code failures F06/F11: generic diagnostics enables attempt state,
// environment does not override file values, or default-off is undocumented.
func TestAttemptLinkConfigurationSourcesAndExplicitDefaultOff(t *testing.T) {
	for _, tc := range []struct {
		name, file, env string
		want            bool
	}{
		{name: "omitted"}, {name: "generic diagnostics only", file: "enable = true"},
		{name: "file true", file: "enable = true\nattempt_link_enable = true", want: true},
		{name: "environment true", file: "enable = true\nattempt_link_enable = false", env: "true", want: true},
		{name: "environment false", file: "enable = true\nattempt_link_enable = true", env: "false"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "wukongim.toml")
			writeFile(t, path, "[node]\nid = 1\ndata_dir = \""+filepath.ToSlash(dir)+"/node\"\n[cluster]\nlisten_addr = \"127.0.0.1:7001\"\n[diagnostics]\n"+tc.file+"\n")
			env := []string{"PATH=" + os.Getenv("PATH")}
			if tc.env != "" {
				env = append(env, "WK_DIAGNOSTICS_ATTEMPT_LINK_ENABLE="+tc.env)
			}
			cfg, err := Load(Options{Args: []string{"-config", path}, Environ: env})
			if err != nil {
				t.Fatal(err)
			}
			if got := cfg.Observability.Diagnostics.AttemptLinkEnable; got != tc.want {
				t.Fatalf("attempt link=%v want%v", got, tc.want)
			}
			item, ok := snapshotItem(cfg.StartupConfigSnapshot, "WK_DIAGNOSTICS_ATTEMPT_LINK_ENABLE")
			if !ok || item.Value != strconv.FormatBool(tc.want) {
				t.Fatalf("effective field=%+v found=%v", item, ok)
			}
			if !strings.Contains(cfg.StartupConfigDocument.TOML, "attempt_link_enable = "+strconv.FormatBool(tc.want)) {
				t.Fatal("effective TOML omitted opt-in")
			}
		})
	}
	_, err := Load(Options{Environ: []string{"WK_NODE_ID=1", "WK_NODE_DATA_DIR=" + t.TempDir(), "WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7001", "WK_DIAGNOSTICS_ENABLE=false", "WK_DIAGNOSTICS_ATTEMPT_LINK_ENABLE=true"}})
	if err == nil {
		t.Fatal("attempt-link must require general diagnostics enabled")
	}
	found := false
	for _, f := range SchemaFields() {
		if f.TOMLPath == "diagnostics.attempt_link_enable" {
			found = true
			if f.EnvKey != "WK_DIAGNOSTICS_ATTEMPT_LINK_ENABLE" || f.Kind != "bool" {
				t.Fatalf("schema=%+v", f)
			}
		}
	}
	if !found {
		t.Fatal("schema missing")
	}
	example, err := os.ReadFile(filepath.Join("..", "..", "wukongim.toml.example"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(example), "attempt_link_enable = false") {
		t.Fatal("example must leave bounded attempt-link off")
	}
}
