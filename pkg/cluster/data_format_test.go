package cluster

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/dataformat"
	"github.com/stretchr/testify/require"
)

func TestNodeRegistersFreshSingleNodeClusterAndGuardsBeforeRuntime(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{NodeID: 1, ListenAddr: "127.0.0.1:0", DataDir: dir, CreatedBy: dataformat.Build{Program: "wukongim", Version: "v3.test", Commit: "abc", BuildSource: "release"}}
	node, err := New(cfg)
	require.NoError(t, err)
	report, err := dataformat.Inspect(dir)
	require.NoError(t, err)
	require.Equal(t, cfg.CreatedBy, report.Metadata.CreatedBy)
	marker := filepath.Join(dir, dataformat.FileName)
	data, err := os.ReadFile(marker)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(marker, []byte(strings.Replace(string(data), fmt.Sprintf(`"format_version": %d`, dataformat.CurrentVersion), `"format_version": 999`, 1)), 0600))
	_, err = New(cfg)
	require.ErrorIs(t, err, dataformat.ErrUnsupported)
	// Start must recheck even if the marker changed after construction. It must
	// fail before creating Controller/Slot/Channel stores or opening listeners.
	require.ErrorIs(t, node.Start(context.Background()), dataformat.ErrUnsupported)
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
}

func TestNodeRejectsUnregisteredDataOrExternalController(t *testing.T) {
	for _, external := range []bool{false, true} {
		dir := t.TempDir()
		existing := dir
		cfg := Config{NodeID: 1, ListenAddr: "127.0.0.1:0", DataDir: dir}
		if external {
			existing = t.TempDir()
			cfg.Control.StateDir = existing
		}
		require.NoError(t, os.WriteFile(filepath.Join(existing, "old-record"), []byte("existing"), 0600))
		_, err := New(cfg)
		require.ErrorIs(t, err, dataformat.ErrUnsupported)
		report, err := dataformat.Inspect(dir)
		require.NoError(t, err)
		require.Equal(t, "unregistered", report.Status)
	}
}
