package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestRPCDiagnosticsConfigurationSourcesAndEffectiveValues(t *testing.T) {
	for _, tc := range []struct {
		name, file, env string
		want            uint32
	}{
		{name: "omitted disables"},
		{name: "explicit zero", file: "rpc_sample_every = 0"},
		{name: "file", file: "rpc_sample_every = 32", want: 32},
		{name: "environment overrides", file: "rpc_sample_every = 32", env: "64", want: 64},
		{name: "zero environment overrides", file: "rpc_sample_every = 32", env: "0"},
		{name: "maximum uint32", env: "4294967295", want: ^uint32(0)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "wukongim.toml")
			writeFile(t, path, "[node]\nid = 1\ndata_dir = \""+filepath.ToSlash(dir)+"/node\"\n[cluster]\nlisten_addr = \"127.0.0.1:7001\"\n[diagnostics]\n"+tc.file+"\n")
			env := []string{"PATH=" + os.Getenv("PATH")}
			if tc.env != "" {
				env = append(env, "WK_DIAGNOSTICS_RPC_SAMPLE_EVERY="+tc.env)
			}
			cfg, err := Load(Options{Args: []string{"-config", path}, Environ: env})
			if err != nil {
				t.Fatal(err)
			}
			if got := cfg.Observability.Diagnostics.RPCSampleEvery; got != tc.want {
				t.Fatalf("RPCSampleEvery = %d, want %d", got, tc.want)
			}
			item, ok := snapshotItem(cfg.StartupConfigSnapshot, "WK_DIAGNOSTICS_RPC_SAMPLE_EVERY")
			if !ok || item.Value != strconv.FormatUint(uint64(tc.want), 10) {
				t.Fatalf("effective item = %#v, found=%t", item, ok)
			}
			if !strings.Contains(cfg.StartupConfigDocument.TOML, "rpc_sample_every = "+strconv.FormatUint(uint64(tc.want), 10)) {
				t.Fatal("effective TOML omitted the RPC sampling interval")
			}
		})
	}
}

func TestRPCDiagnosticsRejectInvalidSamplingIntervals(t *testing.T) {
	for _, raw := range []string{"-1", "1.5", "4294967296", "18446744073709551616", "NaN", "not-an-integer"} {
		t.Run(raw, func(t *testing.T) {
			_, err := Load(Options{Environ: []string{
				"WK_NODE_ID=1", "WK_NODE_DATA_DIR=" + t.TempDir(), "WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7001",
				"WK_DIAGNOSTICS_RPC_SAMPLE_EVERY=" + raw,
			}})
			if err == nil || !strings.Contains(err.Error(), "WK_DIAGNOSTICS_RPC_SAMPLE_EVERY") {
				t.Fatalf("invalid sampling interval %q: %v", raw, err)
			}
		})
	}
}

func TestRPCDiagnosticsSchemaAndExampleDeclareDefaultOff(t *testing.T) {
	var found bool
	for _, field := range SchemaFields() {
		if field.TOMLPath != "diagnostics.rpc_sample_every" {
			continue
		}
		found = true
		if field.EnvKey != "WK_DIAGNOSTICS_RPC_SAMPLE_EVERY" || field.Kind != "int" {
			t.Fatalf("unexpected schema field: %#v", field)
		}
	}
	if !found {
		t.Fatal("schema omitted diagnostics.rpc_sample_every")
	}
	data, err := os.ReadFile(filepath.Join("..", "..", "wukongim.toml.example"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "rpc_sample_every = 0") {
		t.Fatal("example must leave RPC tracing disabled")
	}
}
