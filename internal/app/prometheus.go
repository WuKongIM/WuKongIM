package app

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"gopkg.in/yaml.v3"
)

const embeddedPrometheusDir = "prometheus_embedded"

var errEmbeddedPrometheusMissing = errors.New("embedded prometheus binary missing")

// embeddedPrometheusFS carries the optional build-time Prometheus binary assets.
//
//go:embed prometheus_embedded/*
var embeddedPrometheusFS embed.FS

// prometheusRuntimeConfig contains the effective child-process Prometheus settings.
type prometheusRuntimeConfig struct {
	// BinaryPath is an optional external prometheus executable path or PATH-resolved command.
	BinaryPath string
	// ListenAddr is the Prometheus web listen address.
	ListenAddr string
	// DataDir stores the generated config file and Prometheus TSDB data.
	DataDir string
	// RetentionTime controls time-based TSDB retention.
	RetentionTime time.Duration
	// RetentionSize optionally controls size-based TSDB retention.
	RetentionSize string
	// ScrapeInterval controls how often Prometheus scrapes wukongim.
	ScrapeInterval time.Duration
	// ScrapeTargets lists wukongim /metrics host:port endpoints.
	ScrapeTargets []string
	// Logger records child-process lifecycle failures.
	Logger wklog.Logger
}

type prometheusRuntime struct {
	cfg prometheusRuntimeConfig

	mu      sync.Mutex
	cmd     *exec.Cmd
	done    chan error
	started bool
}

type prometheusConfigFile struct {
	Global        prometheusGlobalConfig   `yaml:"global"`
	ScrapeConfigs []prometheusScrapeConfig `yaml:"scrape_configs"`
}

type prometheusGlobalConfig struct {
	ScrapeInterval     string `yaml:"scrape_interval"`
	EvaluationInterval string `yaml:"evaluation_interval"`
}

type prometheusScrapeConfig struct {
	JobName       string                   `yaml:"job_name"`
	MetricsPath   string                   `yaml:"metrics_path"`
	StaticConfigs []prometheusStaticConfig `yaml:"static_configs"`
}

type prometheusStaticConfig struct {
	Targets []string `yaml:"targets"`
}

func (a *App) wirePrometheus() {
	if a.prometheus != nil || !a.cfg.Observability.Prometheus.Enabled {
		return
	}
	a.prometheus = newPrometheusRuntime(prometheusRuntimeConfigFromApp(a.cfg, a.logger.Named("prometheus")))
}

func prometheusRuntimeConfigFromApp(cfg Config, logger wklog.Logger) prometheusRuntimeConfig {
	prom := cfg.Observability.Prometheus
	return prometheusRuntimeConfig{
		BinaryPath:     strings.TrimSpace(prom.BinaryPath),
		ListenAddr:     strings.TrimSpace(prom.ListenAddr),
		DataDir:        strings.TrimSpace(prom.DataDir),
		RetentionTime:  prom.RetentionTime,
		RetentionSize:  strings.TrimSpace(prom.RetentionSize),
		ScrapeInterval: prom.ScrapeInterval,
		ScrapeTargets:  append([]string(nil), prom.ScrapeTargets...),
		Logger:         logger,
	}
}

func newPrometheusRuntime(cfg prometheusRuntimeConfig) *prometheusRuntime {
	if cfg.Logger == nil {
		cfg.Logger = wklog.NewNop()
	}
	return &prometheusRuntime{cfg: cfg}
}

func (r *prometheusRuntime) Start(ctx context.Context) error {
	if r == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.started {
		return nil
	}
	if err := os.MkdirAll(r.cfg.DataDir, 0o755); err != nil {
		return fmt.Errorf("internal/app: create prometheus data dir: %w", err)
	}
	rendered, err := r.renderConfig()
	if err != nil {
		return err
	}
	if err := os.WriteFile(r.configPath(), rendered, 0o644); err != nil {
		return fmt.Errorf("internal/app: write prometheus config: %w", err)
	}
	binaryPath, err := r.resolveBinaryPath()
	if err != nil {
		return err
	}
	cmd := exec.Command(binaryPath, r.commandArgs()...)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("internal/app: start prometheus: %w", err)
	}
	done := make(chan error, 1)
	goruntimeregistry.SafeGo(nil, goruntimeregistry.TaskAppPrometheusWait, func() {
		done <- cmd.Wait()
	})
	r.cmd = cmd
	r.done = done
	r.started = true
	return nil
}

func (r *prometheusRuntime) Stop(ctx context.Context) error {
	if r == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	r.mu.Lock()
	if !r.started {
		r.mu.Unlock()
		return nil
	}
	cmd := r.cmd
	done := r.done
	r.cmd = nil
	r.done = nil
	r.started = false
	r.mu.Unlock()

	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Signal(syscall.SIGTERM)
	}
	select {
	case <-ctx.Done():
		if cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		if done != nil {
			<-done
		}
		return ctx.Err()
	case err := <-done:
		if err != nil && r.cfg.Logger != nil {
			r.cfg.Logger.Warn("prometheus process exited during stop", wklog.Error(err))
		}
		return nil
	}
}

func (r *prometheusRuntime) configPath() string {
	return filepath.Join(r.cfg.DataDir, "prometheus.yml")
}

func (r *prometheusRuntime) resolveBinaryPath() (string, error) {
	if path := strings.TrimSpace(r.cfg.BinaryPath); path != "" {
		return path, nil
	}
	path, err := extractEmbeddedPrometheusBinary(embeddedPrometheusFS, runtime.GOOS, runtime.GOARCH, filepath.Join(r.cfg.DataDir, "bin"))
	if err != nil {
		return "", err
	}
	return path, nil
}

func embeddedPrometheusAssetName(goos, goarch string) string {
	name := "prometheus-" + goos + "-" + goarch
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

func extractEmbeddedPrometheusBinary(fsys fs.FS, goos, goarch, dir string) (string, error) {
	assetName := embeddedPrometheusAssetName(goos, goarch)
	data, err := fs.ReadFile(fsys, filepath.ToSlash(filepath.Join(embeddedPrometheusDir, assetName)))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("%w: %s; rebuild wukongim with scripts/start-wukongim-single-node.sh or set WK_PROMETHEUS_BINARY_PATH", errEmbeddedPrometheusMissing, assetName)
		}
		return "", fmt.Errorf("internal/app: read embedded prometheus binary: %w", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("internal/app: create prometheus binary dir: %w", err)
	}
	path := filepath.Join(dir, assetName)
	if err := os.WriteFile(path, data, 0o755); err != nil {
		return "", fmt.Errorf("internal/app: write embedded prometheus binary: %w", err)
	}
	return path, nil
}

func (r *prometheusRuntime) commandArgs() []string {
	args := []string{
		"--config.file=" + r.configPath(),
		"--storage.tsdb.path=" + r.cfg.DataDir,
		"--web.listen-address=" + r.cfg.ListenAddr,
		"--web.enable-lifecycle",
		"--storage.tsdb.retention.time=" + prometheusDuration(r.cfg.RetentionTime),
	}
	if strings.TrimSpace(r.cfg.RetentionSize) != "" {
		args = append(args, "--storage.tsdb.retention.size="+strings.TrimSpace(r.cfg.RetentionSize))
	}
	return args
}

func (r *prometheusRuntime) renderConfig() ([]byte, error) {
	config := prometheusConfigFile{
		Global: prometheusGlobalConfig{
			ScrapeInterval:     prometheusDuration(r.cfg.ScrapeInterval),
			EvaluationInterval: prometheusDuration(r.cfg.ScrapeInterval),
		},
		ScrapeConfigs: []prometheusScrapeConfig{
			{
				JobName:     "wukongim",
				MetricsPath: "/metrics",
				StaticConfigs: []prometheusStaticConfig{
					{
						Targets: append([]string(nil), r.cfg.ScrapeTargets...),
					},
				},
			},
		},
	}
	rendered, err := yaml.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("internal/app: render prometheus config: %w", err)
	}
	return rendered, nil
}

func prometheusDuration(d time.Duration) string {
	if d == 0 {
		return "0s"
	}
	if d%time.Hour == 0 {
		return fmt.Sprintf("%dh", d/time.Hour)
	}
	if d%time.Minute == 0 {
		return fmt.Sprintf("%dm", d/time.Minute)
	}
	if d%time.Second == 0 {
		return fmt.Sprintf("%ds", d/time.Second)
	}
	if d%time.Millisecond == 0 {
		return fmt.Sprintf("%dms", d/time.Millisecond)
	}
	return d.String()
}
