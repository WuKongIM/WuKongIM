package main

import (
	"path/filepath"
	"testing"
)

func TestRPCDiagnosticsMainLoaderUsesProductConfiguration(t *testing.T) {
	unsetLoadConfigEnv(t)
	chdir(t, t.TempDir())
	t.Setenv("WK_NODE_ID", "1")
	t.Setenv("WK_NODE_DATA_DIR", filepath.Join(t.TempDir(), "node"))
	t.Setenv("WK_CLUSTER_LISTEN_ADDR", "127.0.0.1:7001")
	for _, interval := range []string{"0", "32"} {
		t.Setenv("WK_DIAGNOSTICS_RPC_SAMPLE_EVERY", interval)
		cfg, err := loadConfig(nil)
		if err != nil {
			t.Fatal(err)
		}
		want := uint32(0)
		if interval == "32" {
			want = 32
		}
		if got := cfg.Observability.Diagnostics.RPCSampleEvery; got != want {
			t.Fatalf("main RPCSampleEvery = %d, want %d", got, want)
		}
	}
}
