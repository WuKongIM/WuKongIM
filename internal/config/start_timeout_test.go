package config

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadClusterStartTimeoutAndEnvironmentOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wukongim.toml")
	writeFile(t, path, "[node]\nid=1\ndata_dir=\""+dir+"/data\"\n[cluster]\nlisten_addr=\"127.0.0.1:7001\"\nstart_timeout=\"90s\"\n")
	for _, tc := range []struct {
		name string
		env  []string
		want time.Duration
	}{{"toml", cleanEnv(), 90 * time.Second}, {"environment", append(cleanEnv(), "WK_CLUSTER_START_TIMEOUT=3m"), 3 * time.Minute}} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := Load(Options{Args: []string{"-config", path}, Environ: tc.env})
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Cluster.Timeouts.Start != tc.want {
				t.Fatalf("startup budget = %s, want %s", cfg.Cluster.Timeouts.Start, tc.want)
			}
		})
	}
}

func TestClusterStartTimeoutValidationAndDocument(t *testing.T) {
	for _, value := range []string{"-1s", "invalid"} {
		values := minimalBuildValues()
		values["WK_CLUSTER_START_TIMEOUT"] = value
		if _, err := buildConfig(values); err == nil || !strings.Contains(err.Error(), "WK_CLUSTER_START_TIMEOUT") {
			t.Fatalf("timeout %q error = %v, want field-specific rejection", value, err)
		}
	}
	for _, value := range []string{"", "0", "3m"} {
		values := minimalBuildValues()
		if value != "" {
			values["WK_CLUSTER_START_TIMEOUT"] = value
		}
		document := documentForTest(t, values)
		want := "30s"
		if value == "3m" {
			want = "3m0s"
		}
		if !strings.Contains(document.TOML, "start_timeout = '"+want+"'") && !strings.Contains(document.TOML, "start_timeout = \""+want+"\"") {
			t.Fatalf("timeout %q missing normalized value %s from document", value, want)
		}
	}
}
