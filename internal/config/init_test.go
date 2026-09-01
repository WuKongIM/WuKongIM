package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/app"
)

func TestInitCreatesValidatedSingleNodeClusterConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "etc", "wukongim.toml")
	result, err := Init(InitOptions{
		Path:          path,
		AdminPassword: "test-admin-password",
		RandomReader:  deterministicRandomReader(),
	})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if result.Path != path || result.AdminUsername != "admin" || result.AdminPassword != "test-admin-password" {
		t.Fatalf("Init() result = %#v", result)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 && mode != 0o640 {
		t.Fatalf("config mode = %#o, want 0600 or 0640", mode)
	}

	cfg, err := Load(Options{Args: []string{"-config", path}, Environ: cleanEnv()})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if _, err := app.NormalizeConfig(cfg); err != nil {
		t.Fatalf("NormalizeConfig() error = %v", err)
	}
	if cfg.NodeID != 1 || cfg.DataDir != "/var/lib/wukongim" {
		t.Fatalf("node config = id %d data %q", cfg.NodeID, cfg.DataDir)
	}
	if cfg.Cluster.Slots.HashSlotCount != 256 || cfg.Cluster.Slots.ReplicaCount != 1 || cfg.Cluster.Channel.ReplicaCount != 1 {
		t.Fatalf("cluster slots/channel = %#v/%#v", cfg.Cluster.Slots, cfg.Cluster.Channel)
	}
	if cfg.Manager.ListenAddr != "127.0.0.1:5301" || !cfg.Manager.AuthOn || len(cfg.Manager.Users) != 1 || cfg.Manager.Users[0].Password != "test-admin-password" {
		t.Fatalf("manager config = %#v", cfg.Manager)
	}
	if len(cfg.Gateway.Listeners) != 2 || cfg.Gateway.Listeners[0].Address != "127.0.0.1:5100" || cfg.Gateway.Listeners[1].Address != "127.0.0.1:5200" {
		t.Fatalf("gateway listeners = %#v", cfg.Gateway.Listeners)
	}
	if cfg.Bench.APIEnabled || cfg.Observability.DebugAPIEnabled || cfg.Observability.Prometheus.Enabled || cfg.Plugin.Enable {
		t.Fatalf("unsafe optional services enabled: bench=%t debug=%t prometheus=%t plugin=%t",
			cfg.Bench.APIEnabled, cfg.Observability.DebugAPIEnabled, cfg.Observability.Prometheus.Enabled, cfg.Plugin.Enable)
	}
	if cfg.Log.Dir != "/var/log/wukongim" || cfg.Log.Console {
		t.Fatalf("log config = %#v", cfg.Log)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	for _, forbidden := range []string{"change-me", "a1234567", "wukongim-dev-manager-secret", "./data/"} {
		if strings.Contains(string(body), forbidden) {
			t.Fatalf("generated config contains unsafe value %q", forbidden)
		}
	}
}

func TestInitGatewayPublicIsExplicit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wukongim.toml")
	_, err := Init(InitOptions{
		Path:          path,
		GatewayPublic: true,
		AdminPassword: "test-admin-password",
		RandomReader:  deterministicRandomReader(),
	})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	cfg, err := Load(Options{Args: []string{"-config", path}, Environ: cleanEnv()})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Gateway.Listeners[0].Address != "0.0.0.0:5100" || cfg.Gateway.Listeners[1].Address != "0.0.0.0:5200" {
		t.Fatalf("gateway listeners = %#v", cfg.Gateway.Listeners)
	}
	if cfg.API.ListenAddr != "127.0.0.1:5001" || cfg.Manager.ListenAddr != "127.0.0.1:5301" || cfg.Cluster.ListenAddr != "127.0.0.1:7001" {
		t.Fatalf("non-gateway listeners escaped loopback: api=%q manager=%q cluster=%q",
			cfg.API.ListenAddr, cfg.Manager.ListenAddr, cfg.Cluster.ListenAddr)
	}
}

func TestInitRefusesOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wukongim.toml")
	if err := os.WriteFile(path, []byte("owned-by-operator\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	_, err := Init(InitOptions{Path: path, AdminPassword: "test-admin-password", RandomReader: deterministicRandomReader()})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("Init() error = %v, want already exists", err)
	}
	body, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("ReadFile() error = %v", readErr)
	}
	if string(body) != "owned-by-operator\n" {
		t.Fatalf("existing config changed to %q", body)
	}
}

func TestInitRandomFailureLeavesNoConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wukongim.toml")
	want := errors.New("random unavailable")
	_, err := Init(InitOptions{
		Path:          path,
		AdminPassword: "test-admin-password",
		RandomReader: func([]byte) (int, error) {
			return 0, want
		},
	})
	if !errors.Is(err, want) {
		t.Fatalf("Init() error = %v, want %v", err, want)
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("Stat() error = %v, want not exist", statErr)
	}
}

func TestInitRejectsShortAdminPassword(t *testing.T) {
	_, err := Init(InitOptions{
		Path:          filepath.Join(t.TempDir(), "wukongim.toml"),
		AdminPassword: "short",
		RandomReader:  deterministicRandomReader(),
	})
	if err == nil || !strings.Contains(err.Error(), "at least 12") {
		t.Fatalf("Init() error = %v, want minimum password length", err)
	}
}

func deterministicRandomReader() func([]byte) (int, error) {
	var next byte = 1
	return func(data []byte) (int, error) {
		for i := range data {
			data[i] = next
			next++
		}
		return len(data), nil
	}
}
