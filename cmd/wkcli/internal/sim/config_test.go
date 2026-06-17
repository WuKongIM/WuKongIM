package sim

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestNormalizeConfigAppliesDefaultsAndDedupeTargets(t *testing.T) {
	cfg, err := normalizeConfig(Config{
		Servers:  []string{" http://127.0.0.1:5001, http://127.0.0.1:5002 ", "http://127.0.0.1:5001"},
		Gateways: []string{"127.0.0.1:5100", "127.0.0.1:5100,127.0.0.1:5101"},
	})

	if err != nil {
		t.Fatalf("normalizeConfig() error = %v", err)
	}
	if !reflect.DeepEqual(cfg.Servers, []string{"http://127.0.0.1:5001", "http://127.0.0.1:5002"}) {
		t.Fatalf("Servers = %#v", cfg.Servers)
	}
	if !reflect.DeepEqual(cfg.Gateways, []string{"127.0.0.1:5100", "127.0.0.1:5101"}) {
		t.Fatalf("Gateways = %#v", cfg.Gateways)
	}
	if cfg.Users != defaultUsers || cfg.Groups != defaultGroups || cfg.GroupMembers != defaultGroupMembers {
		t.Fatalf("topology defaults = users %d groups %d members %d", cfg.Users, cfg.Groups, cfg.GroupMembers)
	}
	if cfg.Rate.PerSecond != 0.2 || cfg.RatePerGroup != defaultRate || cfg.PayloadSize != defaultPayloadSize || cfg.PayloadBytes != 128 {
		t.Fatalf("rate/payload defaults = %#v size %q bytes %d", cfg.Rate, cfg.PayloadSize, cfg.PayloadBytes)
	}
	if cfg.ConnectRate != defaultConnectRate || cfg.Concurrency != defaultConcurrency {
		t.Fatalf("runtime defaults = connect-rate %d concurrency %d", cfg.ConnectRate, cfg.Concurrency)
	}
	if cfg.AckTimeout != defaultAckTimeout || cfg.OperationTimeout != defaultOpTimeout || cfg.StatusInterval != defaultStatusInterval || cfg.RetryBackoff != defaultRetryBackoff {
		t.Fatalf("duration defaults = %#v", cfg)
	}
	if cfg.StatusListen != defaultStatusListen || cfg.UIDPrefix != defaultUIDPrefix || cfg.DevicePrefix != defaultDevicePrefix || cfg.ChannelPrefix != defaultChannelPrefix {
		t.Fatalf("string defaults = %#v", cfg)
	}
	if strings.TrimSpace(cfg.RunID) == "" {
		t.Fatalf("RunID was not generated")
	}
}

func TestNormalizeConfigRejectsInvalidTopologyAndMissingTarget(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		want string
	}{
		{name: "missing target", cfg: Config{}, want: "target"},
		{name: "users", cfg: Config{ContextName: "dev", Users: -1}, want: "--users"},
		{name: "groups", cfg: Config{ContextName: "dev", Groups: -1}, want: "--groups"},
		{name: "group members", cfg: Config{ContextName: "dev", GroupMembers: -1}, want: "--group-members"},
		{name: "max runtime", cfg: Config{ContextName: "dev", MaxRuntime: -1 * time.Second}, want: "--max-runtime"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := normalizeConfig(tt.cfg)

			if err == nil {
				t.Fatalf("expected error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected error to contain %q, got %v", tt.want, err)
			}
		})
	}
}

func TestParseRate(t *testing.T) {
	tests := []struct {
		value string
		want  float64
	}{
		{value: "1.5/s", want: 1.5},
		{value: "1.5", want: 1.5},
	}

	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			got, err := parseRate(tt.value)
			if err != nil {
				t.Fatalf("parseRate() error = %v", err)
			}
			if got.PerSecond != tt.want {
				t.Fatalf("PerSecond = %v, want %v", got.PerSecond, tt.want)
			}
		})
	}

	for _, value := range []string{"0/s", "-1", "bad"} {
		t.Run("invalid "+value, func(t *testing.T) {
			if _, err := parseRate(value); err == nil {
				t.Fatalf("expected error")
			}
		})
	}
}

func TestParseByteSize(t *testing.T) {
	tests := []struct {
		value string
		want  int
	}{
		{value: "128B", want: 128},
		{value: "1KB", want: 1000},
		{value: "1MB", want: 1000 * 1000},
		{value: "1GB", want: 1000 * 1000 * 1000},
		{value: "1KiB", want: 1024},
		{value: "1MiB", want: 1024 * 1024},
		{value: "1GiB", want: 1024 * 1024 * 1024},
	}

	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			got, err := parseByteSize(tt.value)
			if err != nil {
				t.Fatalf("parseByteSize() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("parseByteSize() = %d, want %d", got, tt.want)
			}
		})
	}

	for _, value := range []string{"0B", "-1B", "bad"} {
		t.Run("invalid "+value, func(t *testing.T) {
			if _, err := parseByteSize(value); err == nil {
				t.Fatalf("expected error")
			}
		})
	}
}
