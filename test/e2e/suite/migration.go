//go:build e2e

package suite

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// BuildMigrationCLI builds the actual offline operator entrypoint for the test.
func BuildMigrationCLI(t testing.TB) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "wkcli")
	cmd := exec.Command("go", "build", "-o", path, "./cmd/wkcli")
	cmd.Dir = repoRoot()
	cmd.Env = append(os.Environ(), "GOWORK=off")
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", output)
	return path
}

// RunMigrationCLI executes one bounded migration phase and returns combined output for diagnostics.
func RunMigrationCLI(t testing.TB, ctx context.Context, path string, args ...string) []byte {
	t.Helper()
	cmd := exec.CommandContext(ctx, path, append([]string{"migrate"}, args...)...)
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "wkcli migrate %s: %s", args[0], boundedTail(output))
	return output
}

// RunMigrationCLIExpectFailure observes CLI rejection through its real process
// exit and bounded output, without opening any source or target database.
func RunMigrationCLIExpectFailure(t testing.TB, ctx context.Context, path string, args ...string) []byte {
	t.Helper()
	cmd := exec.CommandContext(ctx, path, append([]string{"migrate"}, args...)...)
	output, err := cmd.CombinedOutput()
	require.Error(t, err, "wkcli migrate must reject unsupported input")
	require.NoError(t, ctx.Err(), "a timeout is not a successful refusal")
	return boundedTail(output)
}

func boundedTail(data []byte) []byte {
	if len(data) > 8192 {
		return data[len(data)-8192:]
	}
	return data
}

// UnpackMigrationFixture extracts a checked-in stopped original-release fixture
// into an isolated directory. Source provenance stays beside the fixture.
func UnpackMigrationFixture(t testing.TB, name string) string {
	t.Helper()
	require.Equal(t, filepath.Base(name), name)
	f, err := os.Open(filepath.Join(repoRoot(), "internal/infra/migrationv2/testdata", name))
	require.NoError(t, err)
	defer f.Close()
	gz, err := gzip.NewReader(f)
	require.NoError(t, err)
	defer gz.Close()
	root := t.TempDir()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		clean := filepath.Clean(filepath.FromSlash(h.Name))
		require.False(t, filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)), "unsafe fixture path")
		target := filepath.Join(root, clean)
		switch h.Typeflag {
		case tar.TypeDir:
			require.NoError(t, os.MkdirAll(target, 0700))
		case tar.TypeReg:
			require.LessOrEqual(t, h.Size, int64(64<<20))
			require.NoError(t, os.MkdirAll(filepath.Dir(target), 0700))
			out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			require.NoError(t, err)
			_, err = io.Copy(out, tr)
			closeErr := out.Close()
			require.NoError(t, err)
			require.NoError(t, closeErr)
		default:
			t.Fatalf("unsupported fixture member %s", h.Name)
		}
	}
	return root
}

// StartPreparedCluster starts real product processes with caller-selected
// endpoints and already installed data directories. It does not open storage.
func (s *Suite) StartPreparedCluster(specs []NodeSpec) *StartedCluster {
	s.t.Helper()
	require.NotEmpty(s.t, specs)
	cluster := &StartedCluster{lastReadyz: make(map[uint64]HTTPObservation), binaryPath: s.binaryPath, workspace: s.workspace}
	registerStartedClusterCleanup(s.t, cluster)
	prepared := make([]NodeSpec, len(specs))
	for i, spec := range specs {
		prepared[i] = preparedNodeSpec(spec)
	}
	specs = prepared
	for _, spec := range specs {
		require.NoError(s.t, os.MkdirAll(spec.RootDir, 0700))
		if spec.LogDir != "" {
			require.NoError(s.t, os.MkdirAll(spec.LogDir, 0o755))
		}
		rendered := RenderClusterConfig(spec, specs)
		require.NoError(s.t, os.WriteFile(spec.ConfigPath, []byte(rendered), 0600))
		spec.Env = append(envFromConfig(rendered), spec.Env...)
		process := &NodeProcess{Spec: spec, BinaryPath: s.binaryPath}
		require.NoError(s.t, process.Start())
		cluster.Nodes = append(cluster.Nodes, StartedNode{Spec: spec, Process: process})
	}
	return cluster
}

// preparedNodeSpec gives a caller-built node its own log directory under its
// root when none is set. Otherwise the product default ./logs resolves to the
// test process working directory, shared by every node, and diagnostics,
// which read <root>/logs, find nothing.
func preparedNodeSpec(spec NodeSpec) NodeSpec {
	if spec.LogDir == "" && spec.RootDir != "" {
		spec.LogDir = filepath.Join(spec.RootDir, "logs")
	}
	return spec
}
