package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCapacitySendCLIConfigPreservesOperatorIntent(t *testing.T) {
	var stderr bytes.Buffer
	cfg, code := parseCapacitySendConfig([]string{
		"--api", " http://127.0.0.1:5001, ,http://127.0.0.1:5002 ",
		"--gateway", "127.0.0.1:5100,127.0.0.1:5200",
		"--bench-token", "secret",
		"--profile", "group",
		"--start-qps", "250",
		"--max-qps", "1000",
		"--step-factor", "2",
		"--duration", "45s",
		"--warmup", "5s",
		"--cooldown", "2s",
		"--stable-p99", "150ms",
		"--min-actual-ratio", "0.9",
		"--max-sendack-error-rate", "0.01",
		"--max-connect-error-rate", "0.02",
		"--binary-search=false",
		"--binary-search-min-delta-ratio", "0.1",
		"--group-members", "100000",
		"--report-dir", "/tmp/wkbench-send",
	}, &stderr)
	if code != 0 {
		t.Fatalf("parse send config: code=%d stderr=%q", code, stderr.String())
	}
	if got := strings.Join(cfg.APIAddrs, ","); got != "http://127.0.0.1:5001,http://127.0.0.1:5002" {
		t.Fatalf("API addresses = %q", got)
	}
	if got := strings.Join(cfg.GatewayTCPAddrs, ","); got != "127.0.0.1:5100,127.0.0.1:5200" {
		t.Fatalf("gateway addresses = %q", got)
	}
	if cfg.BenchToken != "secret" || cfg.Profile != "group" || cfg.StartQPS != 250 || cfg.MaxQPS != 1000 {
		t.Fatalf("identity/load fields = %+v", cfg)
	}
	if cfg.StepFactor != 2 || cfg.Duration != 45*time.Second || cfg.Warmup != 5*time.Second || cfg.Cooldown != 2*time.Second {
		t.Fatalf("schedule fields = %+v", cfg)
	}
	if cfg.StableP99 != 150*time.Millisecond || cfg.MinActualRatio != 0.9 || cfg.MaxSendackErrorRate != 0.01 || cfg.MaxConnectErrorRate != 0.02 {
		t.Fatalf("threshold fields = %+v", cfg)
	}
	if cfg.BinarySearch || cfg.BinarySearchMinDeltaRatio != 0.1 || cfg.GroupMembers != 100000 || cfg.ReportDir != "/tmp/wkbench-send" {
		t.Fatalf("search/report fields = %+v", cfg)
	}
}

func TestCapacityCLIParsersRejectMalformedOrUnsafeConfigurations(t *testing.T) {
	tests := []struct {
		name string
		call func(*bytes.Buffer) int
		want string
	}{
		{
			name: "send unknown flag",
			call: func(stderr *bytes.Buffer) int {
				_, code := parseCapacitySendConfig([]string{"--unknown"}, stderr)
				return code
			},
		},
		{
			name: "send inverted range",
			call: func(stderr *bytes.Buffer) int {
				_, code := parseCapacitySendConfig([]string{"--api", "http://127.0.0.1:5001", "--start-qps", "200", "--max-qps", "100"}, stderr)
				return code
			},
			want: "max-qps",
		},
		{
			name: "hot channel malformed duration",
			call: func(stderr *bytes.Buffer) int {
				_, code := parseCapacityHotChannelConfig([]string{"--duration", "never"}, stderr)
				return code
			},
		},
		{
			name: "message event unknown flag",
			call: func(stderr *bytes.Buffer) int {
				_, code := parseCapacityMessageEventConfig([]string{"--obsolete"}, stderr)
				return code
			},
		},
		{
			name: "activate malformed count",
			call: func(stderr *bytes.Buffer) int {
				_, code := parseCapacityActivateChannelsConfig([]string{"--channels", "many"}, stderr)
				return code
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stderr bytes.Buffer
			if code := tt.call(&stderr); code != exitConfig {
				t.Fatalf("code = %d, want %d", code, exitConfig)
			}
			if tt.want != "" && !strings.Contains(stderr.String(), tt.want) {
				t.Fatalf("stderr %q does not contain %q", stderr.String(), tt.want)
			}
		})
	}
}

func TestMetricsClassifyCLIRejectsIncompleteAndUnreadableSnapshots(t *testing.T) {
	dir := t.TempDir()
	valid := filepath.Join(dir, "valid.prom")
	writeFileForCLIContract(t, valid, "wukongim_gateway_async_send_queue_depth 0\n")

	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "missing pair", args: []string{"--before", valid}, want: "--before and --after are required"},
		{name: "unknown flag", args: []string{"--unknown"}},
		{name: "missing before", args: []string{"--before", filepath.Join(dir, "missing.prom"), "--after", valid}, want: "read before snapshot failed"},
		{name: "missing after", args: []string{"--before", valid, "--after", filepath.Join(dir, "missing.prom")}, want: "read after snapshot failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stderr bytes.Buffer
			if code := runMetricsClassify(tt.args, &stderr); code != exitConfig {
				t.Fatalf("code = %d, want %d", code, exitConfig)
			}
			if tt.want != "" && !strings.Contains(stderr.String(), tt.want) {
				t.Fatalf("stderr %q does not contain %q", stderr.String(), tt.want)
			}
		})
	}
}

func TestDevSimCLIAndEnvironmentContracts(t *testing.T) {
	var stderr bytes.Buffer
	cfg, code := parseDevSimConfig([]string{"--config", "dev.yaml", "--status-listen", "127.0.0.1:6060"}, &stderr)
	if code != 0 {
		t.Fatalf("parse dev-sim: code=%d stderr=%q", code, stderr.String())
	}
	if cfg.configPath != "dev.yaml" || cfg.statusListen != "127.0.0.1:6060" {
		t.Fatalf("config = %+v", cfg)
	}

	stderr.Reset()
	if _, code := parseDevSimConfig([]string{"--status-listen", "127.0.0.1:6060"}, &stderr); code != exitConfig || !strings.Contains(stderr.String(), "--config is required") {
		t.Fatalf("missing config: code=%d stderr=%q", code, stderr.String())
	}
	stderr.Reset()
	if _, code := parseDevSimConfig([]string{"--unknown"}, &stderr); code != exitConfig {
		t.Fatalf("unknown flag: code=%d stderr=%q", code, stderr.String())
	}

	env := envMap([]string{"A=first", "MALFORMED", "EMPTY=", "A=last", "TOKEN=a=b=c"})
	if len(env) != 3 || env["A"] != "last" || env["EMPTY"] != "" || env["TOKEN"] != "a=b=c" {
		t.Fatalf("environment projection = %#v", env)
	}
}

func TestBenchConfigPathParsersSeparateSyntaxFromFileLoading(t *testing.T) {
	var stderr bytes.Buffer
	runCfg, code := parseRunBenchConfig([]string{
		"--target", "target.yaml", "--scenario", "scenario.yaml", "--workers", "workers.yaml",
		"--phase-poll-timeout", "3s",
	}, &stderr)
	if code != 0 {
		t.Fatalf("parse run config: code=%d stderr=%q", code, stderr.String())
	}
	if runCfg.paths.target != "target.yaml" || runCfg.paths.scenario != "scenario.yaml" || runCfg.paths.workers != "workers.yaml" || runCfg.phasePollTimeout != 3*time.Second {
		t.Fatalf("run config = %+v", runCfg)
	}

	stderr.Reset()
	doctor, code := parseBenchConfigPaths("doctor", []string{"--target", "target.yaml", "--workers", "workers.yaml"}, &stderr, false)
	if code != 0 || doctor.scenario != "" {
		t.Fatalf("parse doctor config: cfg=%+v code=%d stderr=%q", doctor, code, stderr.String())
	}

	tests := []struct {
		name string
		call func(*bytes.Buffer) int
		want string
	}{
		{
			name: "run invalid duration",
			call: func(stderr *bytes.Buffer) int {
				_, code := parseRunBenchConfig([]string{"--phase-poll-timeout", "eventually"}, stderr)
				return code
			},
		},
		{
			name: "run missing scenario",
			call: func(stderr *bytes.Buffer) int {
				_, code := parseRunBenchConfig([]string{"--target", "target.yaml", "--workers", "workers.yaml"}, stderr)
				return code
			},
			want: "--scenario is required",
		},
		{
			name: "validate missing target",
			call: func(stderr *bytes.Buffer) int {
				_, code := parseBenchConfigPaths("validate", []string{"--scenario", "scenario.yaml", "--workers", "workers.yaml"}, stderr, true)
				return code
			},
			want: "--target is required",
		},
		{
			name: "doctor missing workers",
			call: func(stderr *bytes.Buffer) int {
				_, code := parseBenchConfigPaths("doctor", []string{"--target", "target.yaml"}, stderr, false)
				return code
			},
			want: "--workers is required",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stderr bytes.Buffer
			if code := tt.call(&stderr); code != exitConfig {
				t.Fatalf("code = %d, want %d", code, exitConfig)
			}
			if tt.want != "" && !strings.Contains(stderr.String(), tt.want) {
				t.Fatalf("stderr %q does not contain %q", stderr.String(), tt.want)
			}
		})
	}
}

func TestConfigLoadingWrappersStopBeforeFilesystemAccessOnMissingFlags(t *testing.T) {
	var stderr bytes.Buffer
	if _, _, _, _, code := loadValidateRunInputs(nil, &stderr); code != exitConfig || !strings.Contains(stderr.String(), "--target is required") {
		t.Fatalf("run wrapper: code=%d stderr=%q", code, stderr.String())
	}
	stderr.Reset()
	if _, _, _, code := loadValidateInputs("validate", nil, &stderr); code != exitConfig || !strings.Contains(stderr.String(), "--target is required") {
		t.Fatalf("validate wrapper: code=%d stderr=%q", code, stderr.String())
	}
	stderr.Reset()
	if _, _, _, _, code := loadDoctorInputs("doctor", nil, &stderr); code != exitConfig || !strings.Contains(stderr.String(), "--target is required") {
		t.Fatalf("doctor wrapper: code=%d stderr=%q", code, stderr.String())
	}
}

func TestCommandExitHelpersPreserveExitClassification(t *testing.T) {
	err := commandExit{code: exitTarget, message: "target unavailable"}
	if err.Error() != "target unavailable" {
		t.Fatalf("error message = %q", err.Error())
	}
	if got := exitCodeError(0); got != nil {
		t.Fatalf("zero exit error = %v", got)
	}
	var classified commandExit
	if got := exitCodeError(exitWorker); !errors.As(got, &classified) || classified.code != exitWorker || classified.message != "" {
		t.Fatalf("classified worker error = %#v", got)
	}
	if got := exitConfigError(nil); got != nil {
		t.Fatalf("nil config error = %v", got)
	}
	if got := exitConfigError(errors.New("bad config")); !errors.As(got, &classified) || classified.code != exitConfig || classified.message != "bad config" {
		t.Fatalf("classified config error = %#v", got)
	}
}

func writeFileForCLIContract(t *testing.T, path string, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", filepath.Base(path), err)
	}
}
