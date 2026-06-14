package bench

import (
	"reflect"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestNormalizeSendConfigParsesHumanSizeAndDefaults(t *testing.T) {
	cfg, err := normalizeSendConfig(sendConfig{
		GatewayAddrs: []string{"127.0.0.1:5100"},
		Clients:      4,
		Messages:     1000,
		Size:         "1KB",
		Channels:     10,
		ChannelType:  "group",
	})

	if err != nil {
		t.Fatalf("normalizeSendConfig() error = %v", err)
	}
	if cfg.PayloadBytes != 1000 {
		t.Fatalf("PayloadBytes = %d, want 1000", cfg.PayloadBytes)
	}
	if cfg.BatchSize != 1 {
		t.Fatalf("BatchSize = %d, want 1", cfg.BatchSize)
	}
	if cfg.ChannelPick != channelPickRoundRobin {
		t.Fatalf("ChannelPick = %q, want %q", cfg.ChannelPick, channelPickRoundRobin)
	}
	if cfg.AckTimeout != 5*time.Second {
		t.Fatalf("AckTimeout = %s, want 5s", cfg.AckTimeout)
	}
	if cfg.ChannelTypeID != frame.ChannelTypeGroup {
		t.Fatalf("ChannelTypeID = %d, want group", cfg.ChannelTypeID)
	}
}

func TestNormalizeSendConfigRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name string
		cfg  sendConfig
	}{
		{name: "zero clients", cfg: sendConfig{GatewayAddrs: []string{"127.0.0.1:5100"}, Clients: 0, Messages: 1, Channels: 1, ChannelType: "group", Size: "128B"}},
		{name: "zero messages", cfg: sendConfig{GatewayAddrs: []string{"127.0.0.1:5100"}, Clients: 1, Messages: 0, Channels: 1, ChannelType: "group", Size: "128B"}},
		{name: "zero channels", cfg: sendConfig{GatewayAddrs: []string{"127.0.0.1:5100"}, Clients: 1, Messages: 1, Channels: 0, ChannelType: "group", Size: "128B"}},
		{name: "bad channel type", cfg: sendConfig{GatewayAddrs: []string{"127.0.0.1:5100"}, Clients: 1, Messages: 1, Channels: 1, ChannelType: "unknown", Size: "128B"}},
		{name: "bad size", cfg: sendConfig{GatewayAddrs: []string{"127.0.0.1:5100"}, Clients: 1, Messages: 1, Channels: 1, ChannelType: "group", Size: "tiny"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := normalizeSendConfig(tt.cfg); err == nil {
				t.Fatalf("expected validation error")
			}
		})
	}
}

func TestChannelPlannerBuildsGeneratedChannelsAndPicksRoundRobin(t *testing.T) {
	planner, err := newChannelPlanner(sendConfig{
		Channel:       "ignored",
		Channels:      3,
		ChannelPrefix: "bench-g",
		ChannelTypeID: frame.ChannelTypeGroup,
		ChannelPick:   channelPickRoundRobin,
	})
	if err != nil {
		t.Fatalf("newChannelPlanner() error = %v", err)
	}

	if got, want := planner.ChannelIDs(), []string{"bench-g-000001", "bench-g-000002", "bench-g-000003"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ChannelIDs() = %#v, want %#v", got, want)
	}
	var picked []string
	for i := 0; i < 5; i++ {
		picked = append(picked, planner.Pick(i).ID)
	}
	if want := []string{"bench-g-000001", "bench-g-000002", "bench-g-000003", "bench-g-000001", "bench-g-000002"}; !reflect.DeepEqual(picked, want) {
		t.Fatalf("picked = %#v, want %#v", picked, want)
	}
}

func TestChannelPlannerUsesSingleChannelWhenCountIsOne(t *testing.T) {
	planner, err := newChannelPlanner(sendConfig{
		Channel:       "g1",
		Channels:      1,
		ChannelPrefix: "bench-g",
		ChannelTypeID: frame.ChannelTypeGroup,
		ChannelPick:   channelPickRoundRobin,
	})
	if err != nil {
		t.Fatalf("newChannelPlanner() error = %v", err)
	}
	if got := planner.Pick(99).ID; got != "g1" {
		t.Fatalf("Pick() = %q, want g1", got)
	}
}
