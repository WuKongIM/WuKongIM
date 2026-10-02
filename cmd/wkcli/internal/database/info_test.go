package database

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/dataformat"
	"github.com/stretchr/testify/require"
)

func TestInfoReadsIdentityWithoutOpeningStores(t *testing.T) {
	dir := t.TempDir()
	var out, diagnostics bytes.Buffer
	run := func(args ...string) int {
		out.Reset()
		diagnostics.Reset()
		return runWithStreams(args, nil, &out, &diagnostics)
	}
	require.Zero(t, run("--data-dir", dir, "--format", "json", "info"), diagnostics.String())
	require.JSONEq(t, `{"status":"unregistered"}`, out.String())
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Empty(t, entries)
	require.NoError(t, dataformat.EnsureFresh(dir, dataformat.Build{Program: "test", Version: "v3", Commit: "abc", BuildSource: "source"}))
	marker := filepath.Join(dir, dataformat.FileName)
	before, err := os.ReadFile(marker)
	require.NoError(t, err)
	require.Zero(t, run("--data-dir", dir, "--format", "json", "info"), diagnostics.String())
	var report dataformat.Report
	require.NoError(t, json.Unmarshal(out.Bytes(), &report))
	require.Equal(t, "registered", report.Status)
	after, err := os.ReadFile(marker)
	require.NoError(t, err)
	require.Equal(t, before, after)
	entries, err = os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.NoError(t, os.WriteFile(marker, []byte(strings.Replace(string(before), fmt.Sprintf(`"format_version": %d`, dataformat.CurrentVersion), `"format_version": 999`, 1)), 0600))
	require.Zero(t, run("--data-dir", dir, "info"))
	require.Contains(t, out.String(), "unsupported")
	require.Contains(t, out.String(), "999")
	require.NotZero(t, run("--data-dir", dir, "query", "show tables"))
	require.Contains(t, diagnostics.String(), "unsupported data directory format")
	missing := filepath.Join(t.TempDir(), "missing")
	require.NotZero(t, run("--data-dir", missing, "info"))
	_, err = os.Stat(missing)
	require.True(t, os.IsNotExist(err))
}

func TestInfoResolvesConfigAndEnvironment(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(t.TempDir(), "wukongim.toml")
	require.NoError(t, os.WriteFile(config, []byte("[node]\ndata_dir = "+strconvQuote(dir)+"\n"), 0600))
	var out, diagnostics bytes.Buffer
	require.Zero(t, runWithStreams([]string{"--config", config, "info"}, nil, &out, &diagnostics), diagnostics.String())
	require.Contains(t, out.String(), "unregistered")
	t.Setenv("WK_NODE_DATA_DIR", filepath.Join(dir, "missing"))
	require.NotZero(t, runWithStreams([]string{"--config", config, "info"}, nil, &out, &diagnostics))
	require.Zero(t, runWithStreams([]string{"--config", config, "--data-dir", dir, "info"}, nil, &out, &diagnostics))
}

func strconvQuote(s string) string { b, _ := json.Marshal(s); return string(b) }
