package messageevent

import (
	"strings"
	"testing"
	"time"
)

func TestDefaultConfigIsSmallAndComputesExpectedCounts(t *testing.T) {
	cfg := DefaultConfig()
	cfg.APIAddrs = []string{"http://127.0.0.1:5001"}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("default config should validate with api addrs: %v", err)
	}
	if cfg.Channels > 16 || cfg.StreamsPerChannel > 16 || cfg.LanesPerStream > 8 || cfg.DeltasPerLane > 8 {
		t.Fatalf("default config should stay small for a fast smoke run: %+v", cfg)
	}

	shape := cfg.Shape()
	wantStreams := cfg.Channels * cfg.StreamsPerChannel
	if shape.Streams != wantStreams {
		t.Fatalf("streams = %d, want %d", shape.Streams, wantStreams)
	}
	if shape.DeltaEvents != wantStreams*cfg.LanesPerStream*cfg.DeltasPerLane {
		t.Fatalf("delta events = %d, want %d", shape.DeltaEvents, wantStreams*cfg.LanesPerStream*cfg.DeltasPerLane)
	}
	if shape.FinishEvents != wantStreams {
		t.Fatalf("finish events = %d, want %d", shape.FinishEvents, wantStreams)
	}
	if shape.ExpectedDurableEvents != wantStreams*(cfg.LanesPerStream+1) {
		t.Fatalf("durable events = %d, want one compact lane event plus finish per stream", shape.ExpectedDurableEvents)
	}
	if shape.ExpectedFinishProposals != wantStreams {
		t.Fatalf("finish proposals = %d, want one proposal per completed stream", shape.ExpectedFinishProposals)
	}
}

func TestValidateRejectsInvalidMessageEventConfig(t *testing.T) {
	base := DefaultConfig()
	base.APIAddrs = []string{"http://127.0.0.1:5001"}

	tests := []struct {
		name string
		edit func(*Config)
		want string
	}{
		{name: "api", edit: func(c *Config) { c.APIAddrs = nil }, want: "api"},
		{name: "channels", edit: func(c *Config) { c.Channels = 0 }, want: "channels"},
		{name: "streams", edit: func(c *Config) { c.StreamsPerChannel = 0 }, want: "streams-per-channel"},
		{name: "lanes", edit: func(c *Config) { c.LanesPerStream = 0 }, want: "lanes-per-stream"},
		{name: "deltas", edit: func(c *Config) { c.DeltasPerLane = 0 }, want: "deltas-per-lane"},
		{name: "payload", edit: func(c *Config) { c.PayloadBytes = -1 }, want: "payload-bytes"},
		{name: "concurrency", edit: func(c *Config) { c.Concurrency = 0 }, want: "concurrency"},
		{name: "timeout", edit: func(c *Config) { c.RequestTimeout = 0 }, want: "request-timeout"},
		{name: "report", edit: func(c *Config) { c.ReportDir = "" }, want: "report-dir"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := base
			tt.edit(&cfg)
			err := cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Validate() = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestConfigAllowsLargeManualPressureShape(t *testing.T) {
	cfg := DefaultConfig()
	cfg.APIAddrs = []string{"http://127.0.0.1:5001", "http://127.0.0.1:5002"}
	cfg.Channels = 100000
	cfg.StreamsPerChannel = 2
	cfg.LanesPerStream = 3
	cfg.DeltasPerLane = 4
	cfg.PayloadBytes = 128
	cfg.Concurrency = 4096
	cfg.RequestTimeout = 15 * time.Second

	if err := cfg.Validate(); err != nil {
		t.Fatalf("large manual pressure config should validate: %v", err)
	}
	shape := cfg.Shape()
	if shape.Streams != 200000 || shape.DeltaEvents != 2400000 || shape.ExpectedDurableEvents != 800000 {
		t.Fatalf("unexpected large shape: %+v", shape)
	}
}
