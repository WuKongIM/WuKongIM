package config

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/WuKongIM/WuKongIM/internal/app"
	"github.com/pelletier/go-toml/v2"
)

const initialAdminUsername = "admin"

// InitOptions controls creation of one package-oriented single-node cluster
// configuration. Init never overwrites an existing file.
type InitOptions struct {
	Path          string
	GatewayPublic bool
	AdminPassword string
	RandomReader  func([]byte) (int, error)
}

// InitResult reports the newly created configuration and its one-time
// bootstrap credential.
type InitResult struct {
	Path          string
	AdminUsername string
	AdminPassword string
	ClusterID     string
}

// Init creates and validates a secure single-node cluster configuration using
// package-owned absolute paths. The completed file appears atomically.
func Init(opts InitOptions) (InitResult, error) {
	path := strings.TrimSpace(opts.Path)
	if path == "" {
		return InitResult{}, fmt.Errorf("config init: --config is required")
	}
	resolved, err := filepath.Abs(path)
	if err != nil {
		return InitResult{}, fmt.Errorf("config init: resolve path: %w", err)
	}
	if _, err := os.Lstat(resolved); err == nil {
		return InitResult{}, fmt.Errorf("config init: %s already exists", resolved)
	} else if !errors.Is(err, os.ErrNotExist) {
		return InitResult{}, fmt.Errorf("config init: inspect %s: %w", resolved, err)
	}

	randomReader := opts.RandomReader
	if randomReader == nil {
		randomReader = rand.Read
	}
	clusterSuffix, err := randomHex(randomReader, 8)
	if err != nil {
		return InitResult{}, fmt.Errorf("config init: generate cluster identity: %w", err)
	}
	joinToken, err := randomHex(randomReader, 32)
	if err != nil {
		return InitResult{}, fmt.Errorf("config init: generate join token: %w", err)
	}
	jwtSecret, err := randomHex(randomReader, 32)
	if err != nil {
		return InitResult{}, fmt.Errorf("config init: generate manager jwt secret: %w", err)
	}
	adminPassword := strings.TrimSpace(opts.AdminPassword)
	if adminPassword == "" {
		adminPassword, err = randomPassword(randomReader, 18)
		if err != nil {
			return InitResult{}, fmt.Errorf("config init: generate manager password: %w", err)
		}
	}
	if len(adminPassword) < 12 {
		return InitResult{}, fmt.Errorf("config init: manager password must contain at least 12 characters")
	}

	clusterID := "wukongim-" + clusterSuffix
	body, err := renderInitialConfig(clusterID, joinToken, jwtSecret, adminPassword, opts.GatewayPublic)
	if err != nil {
		return InitResult{}, err
	}
	if err := writeValidatedConfig(resolved, body); err != nil {
		return InitResult{}, err
	}
	return InitResult{
		Path:          resolved,
		AdminUsername: initialAdminUsername,
		AdminPassword: adminPassword,
		ClusterID:     clusterID,
	}, nil
}

type initialConfigDocument struct {
	Node          initialNodeConfig          `toml:"node"`
	Cluster       initialClusterConfig       `toml:"cluster"`
	API           initialAPIConfig           `toml:"api"`
	Manager       initialManagerConfig       `toml:"manager"`
	Bench         initialBenchConfig         `toml:"bench"`
	Observability initialObservabilityConfig `toml:"observability"`
	Prometheus    initialPrometheusConfig    `toml:"prometheus"`
	Top           initialTopConfig           `toml:"top"`
	Diagnostics   initialDiagnosticsConfig   `toml:"diagnostics"`
	Log           initialLogConfig           `toml:"log"`
	Gateway       initialGatewayConfig       `toml:"gateway"`
	Plugin        initialPluginConfig        `toml:"plugin"`
}

type initialNodeConfig struct {
	ID      uint64 `toml:"id"`
	DataDir string `toml:"data_dir"`
}

type initialClusterConfig struct {
	ListenAddr      string               `toml:"listen_addr"`
	ID              string               `toml:"id"`
	Nodes           []initialClusterNode `toml:"nodes"`
	JoinToken       string               `toml:"join_token"`
	InitialSlots    uint32               `toml:"initial_slot_count"`
	HashSlots       uint16               `toml:"hash_slot_count"`
	SlotReplicas    uint16               `toml:"slot_replica_n"`
	ChannelReplicas uint16               `toml:"channel_replica_n"`
}

type initialClusterNode struct {
	ID   uint64 `toml:"id"`
	Addr string `toml:"addr"`
}

type initialAPIConfig struct {
	ListenAddr string `toml:"listen_addr"`
}

type initialManagerConfig struct {
	ListenAddr string               `toml:"listen_addr"`
	AuthOn     bool                 `toml:"auth_on"`
	JWTSecret  string               `toml:"jwt_secret"`
	JWTIssuer  string               `toml:"jwt_issuer"`
	JWTExpire  string               `toml:"jwt_expire"`
	Users      []initialManagerUser `toml:"users"`
}

type initialManagerUser struct {
	Username    string                     `toml:"username"`
	Password    string                     `toml:"password"`
	Permissions []initialManagerPermission `toml:"permissions"`
}

type initialManagerPermission struct {
	Resource string   `toml:"resource"`
	Actions  []string `toml:"actions"`
}

type initialBenchConfig struct {
	APIEnable bool `toml:"api_enable"`
}

type initialObservabilityConfig struct {
	MetricsEnable  bool `toml:"metrics_enable"`
	DebugAPIEnable bool `toml:"debug_api_enable"`
}

type initialPrometheusConfig struct {
	Enable bool `toml:"enable"`
}

type initialTopConfig struct {
	APIEnable bool `toml:"api_enable"`
}

type initialDiagnosticsConfig struct {
	Enable bool `toml:"enable"`
}

type initialLogConfig struct {
	Level      string `toml:"level"`
	Dir        string `toml:"dir"`
	MaxSize    int    `toml:"max_size"`
	MaxAge     int    `toml:"max_age"`
	MaxBackups int    `toml:"max_backups"`
	Compress   bool   `toml:"compress"`
	Console    bool   `toml:"console"`
	Format     string `toml:"format"`
}

type initialGatewayConfig struct {
	Listeners []initialGatewayListener `toml:"listeners"`
}

type initialGatewayListener struct {
	Name      string `toml:"name"`
	Network   string `toml:"network"`
	Address   string `toml:"address"`
	Transport string `toml:"transport"`
	Protocol  string `toml:"protocol"`
}

type initialPluginConfig struct {
	Enable     bool   `toml:"enable"`
	Dir        string `toml:"dir"`
	SocketPath string `toml:"socket_path"`
	SandboxDir string `toml:"sandbox_dir"`
	StateDir   string `toml:"state_dir"`
}

func renderInitialConfig(clusterID, joinToken, jwtSecret, adminPassword string, gatewayPublic bool) ([]byte, error) {
	gatewayHost := "127.0.0.1"
	if gatewayPublic {
		gatewayHost = "0.0.0.0"
	}
	document := initialConfigDocument{
		Node: initialNodeConfig{ID: 1, DataDir: "/var/lib/wukongim"},
		Cluster: initialClusterConfig{
			ListenAddr:      "127.0.0.1:7001",
			ID:              clusterID,
			Nodes:           []initialClusterNode{{ID: 1, Addr: "127.0.0.1:7001"}},
			JoinToken:       joinToken,
			InitialSlots:    10,
			HashSlots:       256,
			SlotReplicas:    1,
			ChannelReplicas: 1,
		},
		API: initialAPIConfig{ListenAddr: "127.0.0.1:5001"},
		Manager: initialManagerConfig{
			ListenAddr: "127.0.0.1:5301",
			AuthOn:     true,
			JWTSecret:  jwtSecret,
			JWTIssuer:  "wukongim-manager",
			JWTExpire:  "24h",
			Users: []initialManagerUser{{
				Username: initialAdminUsername,
				Password: adminPassword,
				Permissions: []initialManagerPermission{{
					Resource: "*",
					Actions:  []string{"*"},
				}},
			}},
		},
		Bench:         initialBenchConfig{APIEnable: false},
		Observability: initialObservabilityConfig{MetricsEnable: true, DebugAPIEnable: false},
		Prometheus:    initialPrometheusConfig{Enable: false},
		Top:           initialTopConfig{APIEnable: false},
		Diagnostics:   initialDiagnosticsConfig{Enable: false},
		Log: initialLogConfig{
			Level:      "info",
			Dir:        "/var/log/wukongim",
			MaxSize:    100,
			MaxAge:     30,
			MaxBackups: 10,
			Compress:   true,
			Console:    false,
			Format:     "json",
		},
		Gateway: initialGatewayConfig{Listeners: []initialGatewayListener{
			{Name: "tcp-wkproto", Network: "tcp", Address: gatewayHost + ":5100", Transport: "gnet", Protocol: "wkproto"},
			{Name: "ws-gateway", Network: "websocket", Address: gatewayHost + ":5200", Transport: "gnet", Protocol: "wsmux"},
		}},
		Plugin: initialPluginConfig{
			Enable:     false,
			Dir:        "/var/lib/wukongim/plugins",
			SocketPath: "/run/wukongim/plugin.sock",
			SandboxDir: "/var/lib/wukongim/plugin-sandbox",
			StateDir:   "/var/lib/wukongim/plugin-state",
		},
	}
	body, err := toml.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("config init: render TOML: %w", err)
	}
	header := "# Generated by WuKongIM configuration initialization.\n" +
		"# Review advertised addresses and network policy before enabling the service.\n\n"
	return append([]byte(header), body...), nil
}

func writeValidatedConfig(path string, body []byte) error {
	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0o750); err != nil {
		return fmt.Errorf("config init: create %s: %w", parent, err)
	}
	temporary, err := os.CreateTemp(parent, ".wukongim.toml.*")
	if err != nil {
		return fmt.Errorf("config init: create temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)

	mode, gid := initialConfigOwnership()
	if err := temporary.Chmod(mode); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("config init: set temporary permissions: %w", err)
	}
	if gid >= 0 {
		if err := temporary.Chown(-1, gid); err != nil {
			_ = temporary.Close()
			return fmt.Errorf("config init: set wukongim group: %w", err)
		}
	}
	if _, err := temporary.Write(body); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("config init: write temporary file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("config init: sync temporary file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("config init: close temporary file: %w", err)
	}

	cfg, err := Load(Options{Args: []string{"-config", temporaryPath}, Environ: []string{}})
	if err != nil {
		return fmt.Errorf("config init: validate generated config: %w", err)
	}
	if _, err := app.NormalizeConfig(cfg); err != nil {
		return fmt.Errorf("config init: validate generated config: %w", err)
	}
	if err := os.Link(temporaryPath, path); err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("config init: %s already exists", path)
		}
		return fmt.Errorf("config init: publish %s atomically: %w", path, err)
	}
	return nil
}

func initialConfigOwnership() (os.FileMode, int) {
	if os.Geteuid() != 0 {
		return 0o600, -1
	}
	group, err := user.LookupGroup("wukongim")
	if err != nil {
		return 0o600, -1
	}
	gid, err := strconv.Atoi(group.Gid)
	if err != nil {
		return 0o600, -1
	}
	return 0o640, gid
}

func randomHex(read func([]byte) (int, error), size int) (string, error) {
	data := make([]byte, size)
	if err := readFull(read, data); err != nil {
		return "", err
	}
	return hex.EncodeToString(data), nil
}

func randomPassword(read func([]byte) (int, error), size int) (string, error) {
	data := make([]byte, size)
	if err := readFull(read, data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func readFull(read func([]byte) (int, error), data []byte) error {
	for len(data) > 0 {
		n, err := read(data)
		if err != nil {
			return err
		}
		if n <= 0 || n > len(data) {
			return fmt.Errorf("random source returned invalid byte count %d", n)
		}
		data = data[n:]
	}
	return nil
}
