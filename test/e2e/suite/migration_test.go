//go:build e2e

package suite

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Prepared nodes without a log directory once logged to the product default
// ./logs, i.e. the test package directory shared by every node, while
// diagnostics read <root>/logs and found nothing.
func TestPreparedNodeSpecDefaultsLogDirUnderNodeRoot(t *testing.T) {
	root := t.TempDir()
	spec := preparedNodeSpec(NodeSpec{ID: 1, RootDir: filepath.Join(root, "1")})

	require.Equal(t, filepath.Join(root, "1", "logs"), spec.LogDir)
	rendered := RenderClusterConfig(spec, []NodeSpec{spec})
	require.True(t, strings.Contains(rendered, filepath.Join(root, "1", "logs")), "rendered config must name the node log dir:\n%s", rendered)
}

func TestPreparedNodeSpecPreservesExplicitLogDir(t *testing.T) {
	spec := preparedNodeSpec(NodeSpec{ID: 1, RootDir: "/node", LogDir: "/kept/logs"})

	require.Equal(t, "/kept/logs", spec.LogDir)
}

func TestPreparedNodeSpecWithoutRootKeepsProductDefault(t *testing.T) {
	require.Empty(t, preparedNodeSpec(NodeSpec{ID: 1}).LogDir)
}
