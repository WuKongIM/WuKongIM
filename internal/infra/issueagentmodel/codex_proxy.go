package issueagentmodel

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"

	"github.com/pelletier/go-toml/v2"
	"golang.org/x/sys/unix"
)

const (
	codexActionProviderName = "codex-action-responses-proxy"
	codexActionDisplayName  = "Codex Action Responses Proxy"
	codexActionWireAPI      = "responses"
	maxCodexProxyConfigSize = 16 << 10
)

type codexActionProxyConfig struct {
	baseURL string
}

// loadCodexActionProxyConfig accepts only the Action's closed loopback provider.
func loadCodexActionProxyConfig(home string) (codexActionProxyConfig, error) {
	if home == "" || !filepath.IsAbs(home) || filepath.Clean(home) != home {
		return invalidCodexActionProxyConfig()
	}
	file, err := openCodexActionProxyConfig(home)
	if err != nil {
		return invalidCodexActionProxyConfig()
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() ||
		info.Mode().Perm()&0o022 != 0 ||
		info.Size() <= 0 || info.Size() > maxCodexProxyConfigSize {
		return invalidCodexActionProxyConfig()
	}
	body, err := io.ReadAll(io.LimitReader(file, maxCodexProxyConfigSize+1))
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
		provider["wire_api"] != codexActionWireAPI {
		return invalidCodexActionProxyConfig()
	}
	baseURL, ok := provider["base_url"].(string)
	if !ok || !validCodexProxyURL(baseURL) {
		return invalidCodexActionProxyConfig()
	}
	return codexActionProxyConfig{baseURL: baseURL}, nil
}

// openCodexActionProxyConfig prevents path and symlink substitution with openat.
func openCodexActionProxyConfig(home string) (*os.File, error) {
	directoryFD, err := unix.Open(
		home,
		unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW,
		0,
	)
	if err != nil {
		return nil, err
	}
	defer unix.Close(directoryFD)
	configFD, err := unix.Openat(
		directoryFD,
		"config.toml",
		unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW,
		0,
	)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(configFD), "config.toml")
	if file == nil {
		_ = unix.Close(configFD)
		return nil, errors.New("open Codex Action proxy configuration")
	}
	return file, nil
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
