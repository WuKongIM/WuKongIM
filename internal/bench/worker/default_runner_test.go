package worker

import (
	"testing"

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
