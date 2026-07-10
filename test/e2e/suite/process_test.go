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

func TestRedactTOMLConfigUsesSchemaSensitivity(t *testing.T) {
	config := `
[node]
id = 7
data_dir = "/tmp/e2e-node-7"

[cluster]
listen_addr = "127.0.0.1:7017"
join_token = "join-token-value"

[manager]
jwt_secret = "jwt-secret-value"
users = '[{"username":"admin","password":"manager-user-password"}]'
`
	redacted, err := redactTOMLConfig([]byte(config))
	require.NoError(t, err)
	diagnostics := string(redacted)

	for _, sensitiveValue := range []string{
		"join-token-value",
		"jwt-secret-value",
		"manager-user-password",
		"admin",
	} {
		require.NotContains(t, diagnostics, sensitiveValue)
	}
	require.Equal(t, 3, strings.Count(diagnostics, redactedConfigValue))
	for _, nonSensitiveEvidence := range []string{
		"id = 7",
		"/tmp/e2e-node-7",
		"127.0.0.1:7017",
	} {
		require.Contains(t, diagnostics, nonSensitiveEvidence)
	}
}

func TestRedactTOMLConfigAcceptsProductExample(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "wukongim.toml.example"))
	require.NoError(t, err)

	redacted, err := redactTOMLConfig(data)
	require.NoError(t, err)
	diagnostics := string(redacted)
	require.NotContains(t, diagnostics, "change-me")
	require.NotContains(t, diagnostics, "a1234567")
	require.Contains(t, diagnostics, redactedConfigValue)
}

func TestRedactTOMLConfigRedactsCredentialCapableURLs(t *testing.T) {
	config := `
[node]
id = 7

[cluster]
listen_addr = "127.0.0.1:7017"

[api]
external_ws_addr = "ws://ws-user:ws-password@ws.example.internal/private/socket?token=ws-query#ws-fragment"
external_wss_addr = "wss://wss-user:wss-password@wss.example.internal/private/socket?token=wss-query#wss-fragment"

[webhook]
http_addr = "https://webhook-user:webhook-password@hooks.example.internal/secret/webhook-key?token=webhook-query-token#webhook-fragment"

[prometheus]
query_base_url = "https://prometheus-user:prometheus-password@metrics.example.internal/private/api?access_token=prometheus-query-token#prometheus-fragment"
`
	redacted, err := redactTOMLConfig([]byte(config))
	require.NoError(t, err)
	diagnostics := string(redacted)

	for _, sensitiveFragment := range []string{
		"ws-user",
		"ws-password",
		"ws.example.internal",
		"ws-query",
		"ws-fragment",
		"wss-user",
		"wss-password",
		"wss.example.internal",
		"wss-query",
		"wss-fragment",
		"webhook-user",
		"webhook-password",
		"hooks.example.internal",
		"secret/webhook-key",
		"webhook-query-token",
		"webhook-fragment",
		"prometheus-user",
		"prometheus-password",
		"metrics.example.internal",
		"private/api",
		"prometheus-query-token",
		"prometheus-fragment",
	} {
		require.NotContains(t, diagnostics, sensitiveFragment)
	}
	require.Equal(t, 4, strings.Count(diagnostics, redactedConfigValue))
	require.Contains(t, diagnostics, `external_ws_addr = '[REDACTED]'`)
	require.Contains(t, diagnostics, `external_wss_addr = '[REDACTED]'`)
	require.Contains(t, diagnostics, `http_addr = '[REDACTED]'`)
	require.Contains(t, diagnostics, `query_base_url = '[REDACTED]'`)
	require.Contains(t, diagnostics, "id = 7")
	require.Contains(t, diagnostics, `listen_addr = '127.0.0.1:7017'`)
}

func TestRedactTOMLConfigRedactsSensitiveKeysInsideOrdinaryStructuredLeaf(t *testing.T) {
	config := `
[gateway]
listeners = [
  { name = "tcp", network = "tcp", address = "127.0.0.1:5100", transport = "gnet", protocol = "wkproto", auth = { username = "visible-user", client_secret = "nested-client-secret", API_KEY = "nested-api-key" } },
]
`
	redacted, err := redactTOMLConfig([]byte(config))
	require.NoError(t, err)
	diagnostics := string(redacted)
	require.NotContains(t, diagnostics, "nested-client-secret")
	require.NotContains(t, diagnostics, "nested-api-key")
	require.Equal(t, 2, strings.Count(diagnostics, redactedConfigValue))
	require.Contains(t, diagnostics, "visible-user")
	require.Contains(t, diagnostics, "127.0.0.1:5100")
}

func TestRedactTOMLConfigPreservesEmptyOptionalListLeaf(t *testing.T) {
	redacted, err := redactTOMLConfig([]byte("[webhook]\nfocus_events = \"\"\n"))
	require.NoError(t, err)
	require.Contains(t, string(redacted), "focus_events = ''")
	require.NotContains(t, string(redacted), "focus_events = 'null'")
}

func TestRedactTOMLConfigRejectsUnknownPathsAndScalarGroups(t *testing.T) {
	tests := []struct {
		name   string
		config string
	}{
		{
			name: "case changed known leaf",
			config: `[webhook]
HTTP_ADDR = "https://unknown-case.example/secret"
`,
		},
		{
			name: "unknown group and leaf",
			config: `[database]
dsn = "postgres://user:password@database.example/app"
`,
		},
		{
			name:   "schema group encoded as scalar",
			config: `webhook = "https://scalar-group.example/secret"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := redactTOMLConfig([]byte(tt.config))
			require.Error(t, err)
		})
	}
}

func TestRedactTOMLConfigRejectsKnownLeavesWithWrongSchemaKind(t *testing.T) {
	tests := []struct {
		name   string
		config string
	}{
		{
			name:   "integer leaf encoded as string",
			config: `node = { id = "secret-node-id" }`,
		},
		{
			name:   "string leaf encoded as table",
			config: `api = { listen_addr = { password = "secret-listen-address" } }`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := redactTOMLConfig([]byte(tt.config))
			require.Error(t, err)
		})
	}
}

func TestNodeProcessDumpDiagnosticsFailsClosedForInvalidConfig(t *testing.T) {
	tests := []struct {
		name      string
		config    string
		forbidden []string
	}{
		{
			name: "unknown case-sensitive leaf",
			config: `[webhook]
HTTP_ADDR = "https://unknown-case.example/secret"
`,
			forbidden: []string{"HTTP_ADDR", "unknown-case.example", "secret", "unknown config"},
		},
		{
			name: "unknown group",
			config: `[database]
dsn = "postgres://user:password@database.example/app"
`,
			forbidden: []string{"database", "dsn", "password", "unknown config"},
		},
		{
			name:      "scalar group",
			config:    `webhook = "https://scalar-group.example/secret"`,
			forbidden: []string{"scalar-group.example", "secret", "must be a group"},
		},
		{
			name:      "malformed TOML",
			config:    "[cluster]\njoin_token = \"malformed-secret-value\"\nnodes = [\n",
			forbidden: []string{"malformed-secret-value", "nodes =", "unexpected", "decode"},
		},
		{
			name:      "known integer leaf with string value",
			config:    `node = { id = "secret-node-id" }`,
			forbidden: []string{"secret-node-id", "value must be integer"},
		},
		{
			name:      "known string leaf with table value",
			config:    `api = { listen_addr = { password = "secret-listen-address" } }`,
			forbidden: []string{"secret-listen-address", "value must be string"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rootDir := t.TempDir()
			configPath := filepath.Join(rootDir, "wukongim.toml")
			require.NoError(t, os.WriteFile(configPath, []byte(tt.config), 0o600))

			process := &NodeProcess{Spec: NodeSpec{
				ConfigPath: configPath,
				StdoutPath: filepath.Join(rootDir, "stdout.log"),
				StderrPath: filepath.Join(rootDir, "stderr.log"),
			}}

			diagnostics := process.DumpDiagnostics()

			for _, forbidden := range tt.forbidden {
				require.NotContains(t, diagnostics, forbidden)
			}
			require.Contains(t, diagnostics, "config-content: [invalid or unsupported TOML; content omitted]")
		})
	}
}

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
