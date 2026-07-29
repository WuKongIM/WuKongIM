package issueagentmodel

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"

	"github.com/pelletier/go-toml/v2"
)

const (
	codexActionProviderName = "codex-action-responses-proxy"
	codexActionDisplayName  = "Codex Action Responses Proxy"
	maxCodexProxyConfigSize = 16 << 10
)

type codexActionProxyConfig struct {
	baseURL string
}

func loadCodexActionProxyConfig(home string) (codexActionProxyConfig, error) {
	if home == "" || !filepath.IsAbs(home) || filepath.Clean(home) != home {
		return invalidCodexActionProxyConfig()
	}
	homeInfo, err := os.Lstat(home)
	if err != nil || !homeInfo.IsDir() || homeInfo.Mode()&os.ModeSymlink != 0 {
		return invalidCodexActionProxyConfig()
	}
	path := filepath.Join(home, "config.toml")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() ||
		info.Mode().Perm()&0o022 != 0 ||
		info.Size() <= 0 || info.Size() > maxCodexProxyConfigSize {
		return invalidCodexActionProxyConfig()
	}
	body, err := os.ReadFile(path)
	if err != nil || int64(len(body)) != info.Size() {
		return invalidCodexActionProxyConfig()
	}
	var document map[string]any
	if toml.Unmarshal(body, &document) != nil ||
		!hasExactKeys(document, "model_provider", "model_providers") ||
		document["model_provider"] != codexActionProviderName {
		return invalidCodexActionProxyConfig()
	}
	providers, ok := document["model_providers"].(map[string]any)
	if !ok || !hasExactKeys(providers, codexActionProviderName) {
		return invalidCodexActionProxyConfig()
	}
	provider, ok := providers[codexActionProviderName].(map[string]any)
	if !ok || !hasExactKeys(provider, "name", "base_url", "wire_api") ||
		provider["name"] != codexActionDisplayName ||
		provider["wire_api"] != "responses" {
		return invalidCodexActionProxyConfig()
	}
	baseURL, ok := provider["base_url"].(string)
	if !ok || !validCodexProxyURL(baseURL) {
		return invalidCodexActionProxyConfig()
	}
	return codexActionProxyConfig{baseURL: baseURL}, nil
}

func invalidCodexActionProxyConfig() (codexActionProxyConfig, error) {
	return codexActionProxyConfig{},
		errors.New("Codex Action proxy configuration is invalid")
}

func hasExactKeys(value map[string]any, expected ...string) bool {
	if len(value) != len(expected) {
		return false
	}
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	slices.Sort(expected)
	return slices.Equal(keys, expected)
}

func validCodexProxyURL(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "http" ||
		parsed.Hostname() != "127.0.0.1" || parsed.User != nil ||
		parsed.Path != "/v1" || parsed.RawPath != "" ||
		parsed.RawQuery != "" || parsed.Fragment != "" ||
		parsed.ForceQuery || parsed.Opaque != "" {
		return false
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil || port < 1 || port > 65535 {
		return false
	}
	return value == fmt.Sprintf("http://127.0.0.1:%d/v1", port)
}
