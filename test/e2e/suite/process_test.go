//go:build e2e

package suite

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNodeProcessStartStripsHarnessEnvironment(t *testing.T) {
	t.Setenv("WK_E2E_BINARY", "inherited-binary")
	t.Setenv("WK_E2E_GOFAIL_DYNAMIC_NODE", "inherited-gofail")
	t.Setenv("WK_E2E_100K_CONVERSATION", "inherited-conversation")
	t.Setenv("E2E_ENV_PROBE_INHERITED", "inherited-kept")

	envBinary, err := exec.LookPath("env")
	require.NoError(t, err)

	tests := []struct {
		name       string
		commandEnv []string
		ordinary   string
	}{
		{
			name:     "inherited command environment",
			ordinary: "E2E_ENV_PROBE_INHERITED=inherited-kept",
		},
		{
			name: "explicit command environment",
			commandEnv: []string{
				"WK_E2E_BINARY=explicit-binary",
				"WK_E2E_GOFAIL_DYNAMIC_NODE=explicit-gofail",
				"WK_E2E_100K_CONVERSATION=explicit-conversation",
				"E2E_ENV_PROBE_EXPLICIT=explicit-kept",
			},
			ordinary: "E2E_ENV_PROBE_EXPLICIT=explicit-kept",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rootDir := t.TempDir()
			cmd := exec.Command(envBinary)
			cmd.Env = tt.commandEnv
			process := &NodeProcess{
				Spec: NodeSpec{
					RootDir:    rootDir,
					ConfigPath: filepath.Join(rootDir, "wukongim.toml"),
					StdoutPath: filepath.Join(rootDir, "stdout.log"),
					StderrPath: filepath.Join(rootDir, "stderr.log"),
					Env: []string{
						"WK_NODE_ID=42",
						"GOFAIL_HTTP=127.0.0.1:23456",
						"WK_E2E_NODE_OVERRIDE=spec-leak",
					},
				},
				command: cmd,
			}

			require.NoError(t, process.Start())
			require.NoError(t, process.Cmd.Wait())
			process.closeLogs()

			output, err := os.ReadFile(process.Spec.StdoutPath)
			require.NoError(t, err)
			env := parseProbeEnvironment(string(output))
			for _, name := range []string{
				"WK_E2E_BINARY",
				"WK_E2E_GOFAIL_DYNAMIC_NODE",
				"WK_E2E_100K_CONVERSATION",
				"WK_E2E_NODE_OVERRIDE",
			} {
				_, leaked := env[name]
				require.False(t, leaked, "harness variable %s leaked into child process", name)
			}
			ordinaryKey, ordinaryValue, ok := strings.Cut(tt.ordinary, "=")
			require.True(t, ok)
			require.Equal(t, ordinaryValue, env[ordinaryKey])
			require.Equal(t, "42", env["WK_NODE_ID"])
			require.Equal(t, "127.0.0.1:23456", env["GOFAIL_HTTP"])
		})
	}
}

func parseProbeEnvironment(output string) map[string]string {
	env := make(map[string]string)
	for _, entry := range strings.Split(strings.TrimSpace(output), "\n") {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			env[key] = value
		}
	}
	return env
}
