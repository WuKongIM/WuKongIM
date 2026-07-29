package issueagentmodel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const validCodexActionProxyConfig = `
# Added by codex-action.
model_provider = "codex-action-responses-proxy"

[model_providers.codex-action-responses-proxy]
name = "Codex Action Responses Proxy"
base_url = "http://127.0.0.1:43123/v1"
wire_api = "responses"
`

func TestLoadCodexActionProxyConfigAcceptsExactLoopbackProvider(t *testing.T) {
	t.Parallel()

	home := writeCodexBootstrapHome(t, validCodexActionProxyConfig, 0o644)
	config, err := loadCodexActionProxyConfig(home)
	require.NoError(t, err)
	require.Equal(t, "http://127.0.0.1:43123/v1", config.baseURL)
}

func TestLoadCodexActionProxyConfigRejectsUnsafeDocuments(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"unknown top-level key": validCodexActionProxyConfig + "\nmodel = \"gpt-5\"\n",
		"second provider": validCodexActionProxyConfig +
			"\n[model_providers.other]\nname=\"x\"\n" +
			"base_url=\"http://127.0.0.1:1/v1\"\nwire_api=\"responses\"\n",
		"secret field": strings.Replace(
			validCodexActionProxyConfig,
			`wire_api = "responses"`,
			"wire_api = \"responses\"\nenv_key = \"CODEX_API_KEY\"",
			1,
		),
		"https": strings.Replace(
			validCodexActionProxyConfig, "http://", "https://", 1,
		),
		"non-loopback": strings.Replace(
			validCodexActionProxyConfig, "127.0.0.1", "localhost", 1,
		),
		"alternate loopback": strings.Replace(
			validCodexActionProxyConfig, "127.0.0.1", "127.000.000.001", 1,
		),
		"wrong path": strings.Replace(
			validCodexActionProxyConfig, "/v1", "/v1/responses", 1,
		),
		"query": strings.Replace(
			validCodexActionProxyConfig, "/v1", "/v1?token=x", 1,
		),
		"fragment": strings.Replace(
			validCodexActionProxyConfig, "/v1", "/v1#x", 1,
		),
		"userinfo": strings.Replace(
			validCodexActionProxyConfig, "127.0.0.1", "user@127.0.0.1", 1,
		),
		"missing port": strings.Replace(
			validCodexActionProxyConfig, ":43123", "", 1,
		),
		"zero port": strings.Replace(
			validCodexActionProxyConfig, "43123", "0", 1,
		),
		"overflow port": strings.Replace(
			validCodexActionProxyConfig, "43123", "65536", 1,
		),
		"wrong provider": strings.Replace(
			validCodexActionProxyConfig,
			`model_provider = "codex-action-responses-proxy"`,
			`model_provider = "openai"`,
			1,
		),
		"wrong display name": strings.Replace(
			validCodexActionProxyConfig,
			`name = "Codex Action Responses Proxy"`,
			`name = "Other"`,
			1,
		),
		"wrong wire API": strings.Replace(
			validCodexActionProxyConfig,
			`wire_api = "responses"`,
			`wire_api = "chat"`,
			1,
		),
	}
	for name, body := range tests {
		name, body := name, body
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := loadCodexActionProxyConfig(
				writeCodexBootstrapHome(t, body, 0o644),
			)
			requireInvalidCodexActionProxy(t, err)
		})
	}
}

func TestLoadCodexActionProxyConfigRejectsUnsafeFiles(t *testing.T) {
	t.Parallel()

	t.Run("empty home", func(t *testing.T) {
		_, err := loadCodexActionProxyConfig("")
		requireInvalidCodexActionProxy(t, err)
	})
	t.Run("relative home", func(t *testing.T) {
		_, err := loadCodexActionProxyConfig("relative")
		requireInvalidCodexActionProxy(t, err)
	})
	t.Run("missing config", func(t *testing.T) {
		_, err := loadCodexActionProxyConfig(t.TempDir())
		requireInvalidCodexActionProxy(t, err)
	})
	t.Run("writable by group", func(t *testing.T) {
		home := writeCodexBootstrapHome(t, validCodexActionProxyConfig, 0o664)
		_, err := loadCodexActionProxyConfig(home)
		requireInvalidCodexActionProxy(t, err)
	})
	t.Run("writable by others", func(t *testing.T) {
		home := writeCodexBootstrapHome(t, validCodexActionProxyConfig, 0o646)
		_, err := loadCodexActionProxyConfig(home)
		requireInvalidCodexActionProxy(t, err)
	})
	t.Run("empty config", func(t *testing.T) {
		home := writeCodexBootstrapHome(t, "", 0o644)
		_, err := loadCodexActionProxyConfig(home)
		requireInvalidCodexActionProxy(t, err)
	})
	t.Run("oversized config", func(t *testing.T) {
		home := writeCodexBootstrapHome(t, strings.Repeat("x", 16<<10+1), 0o644)
		_, err := loadCodexActionProxyConfig(home)
		requireInvalidCodexActionProxy(t, err)
	})
	t.Run("config directory", func(t *testing.T) {
		home := t.TempDir()
		require.NoError(t, os.Mkdir(filepath.Join(home, "config.toml"), 0o700))
		_, err := loadCodexActionProxyConfig(home)
		requireInvalidCodexActionProxy(t, err)
	})
	t.Run("config symlink", func(t *testing.T) {
		home := t.TempDir()
		target := filepath.Join(t.TempDir(), "target.toml")
		require.NoError(t, os.WriteFile(
			target, []byte(validCodexActionProxyConfig), 0o644,
		))
		require.NoError(t, os.Symlink(target, filepath.Join(home, "config.toml")))
		_, err := loadCodexActionProxyConfig(home)
		requireInvalidCodexActionProxy(t, err)
	})
	t.Run("home symlink", func(t *testing.T) {
		home := writeCodexBootstrapHome(t, validCodexActionProxyConfig, 0o644)
		link := filepath.Join(t.TempDir(), "home")
		require.NoError(t, os.Symlink(home, link))
		_, err := loadCodexActionProxyConfig(link)
		requireInvalidCodexActionProxy(t, err)
	})
}

func writeCodexBootstrapHome(
	t *testing.T,
	body string,
	mode os.FileMode,
) string {
	t.Helper()
	home := t.TempDir()
	path := filepath.Join(home, "config.toml")
	require.NoError(t, os.WriteFile(path, []byte(body), mode))
	require.NoError(t, os.Chmod(path, mode))
	return home
}

func requireInvalidCodexActionProxy(t *testing.T, err error) {
	t.Helper()
	require.EqualError(t, err, "Codex Action proxy configuration is invalid")
}
