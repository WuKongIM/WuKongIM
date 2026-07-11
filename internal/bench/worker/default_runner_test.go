package worker

import (
	"errors"
	"net"
	"syscall"
	"testing"

	benchworkload "github.com/WuKongIM/WuKongIM/internal/bench/workload"
	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
)

func TestNewDefaultWorkloadRunnerExposesMetricsReporter(t *testing.T) {
	runner := NewDefaultWorkloadRunner(nil)
	if runner == nil {
		t.Fatal("expected default workload runner")
	}
	if _, ok := runner.(MetricsReporter); !ok {
		t.Fatal("expected default workload runner to expose metrics")
	}
	if _, ok := runner.(ConnectionStatusReporter); !ok {
		t.Fatal("expected default workload runner to expose connection status")
	}
}

func TestMarkTargetUnavailablePreservesLocalTCPSourceErrors(t *testing.T) {
	sourceErr := &benchworkload.TCPSourceError{
		Kind: benchworkload.TCPSourceErrorUnavailable,
		Err:  &net.OpError{Op: "dial", Net: "tcp", Err: syscall.EADDRNOTAVAIL},
	}

	got := markTargetUnavailable(sourceErr)

	if got != sourceErr {
		t.Fatalf("markTargetUnavailable() = %T %v, want original local source error", got, got)
	}
	if errors.Is(got, errTargetUnavailable) {
		t.Fatal("local source error classified as target unavailable")
	}
}

func TestConnectionManagerConfigCopiesAssignmentClientProfile(t *testing.T) {
	profile := &model.WorkerClientConfig{
		SendQueueCapacity: 16,
		MaxInflight:       1,
		ReadBufferSize:    1024,
		FrameBufferSize:   4,
	}
	assignment := Assignment{
		Client: profile,
		Target: model.Target{Gateway: model.TargetGatewayConfig{TCP: model.TargetGatewayTCPConfig{Addrs: []string{"127.0.0.1:5100"}}}},
	}

	cfg := connectionManagerConfig(assignment, nil)

	if cfg.Client == nil || *cfg.Client != *profile {
		t.Fatalf("connection manager client profile = %#v, want %#v", cfg.Client, profile)
	}
	if cfg.Client == profile {
		t.Fatal("connection manager client profile aliases assignment profile")
	}
	profile.SendQueueCapacity = 999
	if cfg.Client.SendQueueCapacity != 16 {
		t.Fatalf("copied send queue capacity = %d, want 16 after source mutation", cfg.Client.SendQueueCapacity)
	}
}

func TestConnectionManagerConfigCopiesAssignmentTCPSourcePool(t *testing.T) {
	pool := &model.TCPSourceConfig{
		IPv4Addrs: []string{"127.0.0.1", "192.168.3.57"},
		PortMin:   1024,
		PortMax:   65535,
	}
	assignment := Assignment{
		TCPSource: pool,
		Target:    model.Target{Gateway: model.TargetGatewayConfig{TCP: model.TargetGatewayTCPConfig{Addrs: []string{"127.0.0.1:5100"}}}},
	}

	cfg := connectionManagerConfig(assignment, nil)

	if cfg.TCPSource == nil || cfg.TCPSource.PortMin != 1024 || cfg.TCPSource.PortMax != 65535 {
		t.Fatalf("connection manager tcp source pool = %#v, want %#v", cfg.TCPSource, pool)
	}
	if cfg.TCPSource == pool || &cfg.TCPSource.IPv4Addrs[0] == &pool.IPv4Addrs[0] {
		t.Fatal("connection manager tcp source pool aliases assignment pool")
	}
	pool.IPv4Addrs[0] = "10.0.0.1"
	if cfg.TCPSource.IPv4Addrs[0] != "127.0.0.1" {
		t.Fatalf("copied source address = %q, want 127.0.0.1 after source mutation", cfg.TCPSource.IPv4Addrs[0])
	}
}
