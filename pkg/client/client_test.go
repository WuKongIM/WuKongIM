package client

import (
	"errors"
	"testing"
	"time"
)

func TestNormalizeConfigAppliesToolingDefaults(t *testing.T) {
	cfg, err := normalizeConfig(Config{Addr: "127.0.0.1:5100"})
	if err != nil {
		t.Fatalf("normalizeConfig() error = %v", err)
	}
	if cfg.OperationTimeout != 5*time.Second {
		t.Fatalf("OperationTimeout = %s, want 5s", cfg.OperationTimeout)
	}
	if cfg.AckTimeout != 5*time.Second {
		t.Fatalf("AckTimeout = %s, want 5s", cfg.AckTimeout)
	}
	if cfg.SendQueueCapacity != 8192 {
		t.Fatalf("SendQueueCapacity = %d, want 8192", cfg.SendQueueCapacity)
	}
	if cfg.MaxInflight != 8192 {
		t.Fatalf("MaxInflight = %d, want 8192", cfg.MaxInflight)
	}
	if cfg.BatchMaxRecords != 512 {
		t.Fatalf("BatchMaxRecords = %d, want 512", cfg.BatchMaxRecords)
	}
	if cfg.BatchMaxBytes != 512*1024 {
		t.Fatalf("BatchMaxBytes = %d, want %d", cfg.BatchMaxBytes, 512*1024)
	}
	if cfg.BatchMaxWait != time.Millisecond {
		t.Fatalf("BatchMaxWait = %s, want 1ms", cfg.BatchMaxWait)
	}
	if cfg.ReadBufferSize != 4096 {
		t.Fatalf("ReadBufferSize = %d, want 4096", cfg.ReadBufferSize)
	}
	if cfg.InboundFrameBufferSize != 1024 {
		t.Fatalf("InboundFrameBufferSize = %d, want 1024", cfg.InboundFrameBufferSize)
	}
	if cfg.GenerateClientMsgNo {
		t.Fatal("GenerateClientMsgNo default = true, want false")
	}
}

func TestNormalizeConfigRequiresAddr(t *testing.T) {
	_, err := normalizeConfig(Config{})
	if !errors.Is(err, ErrMissingAddr) {
		t.Fatalf("normalizeConfig() error = %v, want %v", err, ErrMissingAddr)
	}
}
