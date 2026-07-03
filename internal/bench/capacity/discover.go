package capacity

import (
	"context"
	"fmt"
	"strings"

	"github.com/WuKongIM/WuKongIM/internal/bench/target"
	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
)

// DiscoveredTarget contains capacity target discovery output.
type DiscoveredTarget struct {
	// Target is the wkbench target config built from API and gateway discovery.
	Target model.Target `json:"target"`
	// CapacityTargets are the raw per-node capacity target documents.
	CapacityTargets []model.CapacityTarget `json:"capacity_targets"`
}

// DiscoverTarget checks target APIs and builds the wkbench target config.
func DiscoverTarget(ctx context.Context, cfg Config) (DiscoveredTarget, error) {
	if err := cfg.Validate(); err != nil {
		return DiscoveredTarget{}, err
	}
	apiAddrs := nonEmptyStrings(cfg.APIAddrs)
	gatewayAddrs := nonEmptyStrings(cfg.GatewayTCPAddrs)
	manualGatewayAddrs := len(gatewayAddrs) > 0
	capacityTargets := make([]model.CapacityTarget, 0, len(apiAddrs))
	for _, apiAddr := range apiAddrs {
		client := target.NewClient(target.Config{APIAddrs: []string{apiAddr}, Token: cfg.BenchToken})
		if err := client.Healthz(ctx); err != nil {
			return DiscoveredTarget{}, fmt.Errorf("target %s /healthz failed: %w", apiAddr, err)
		}
		if err := client.Readyz(ctx); err != nil {
			return DiscoveredTarget{}, fmt.Errorf("target %s /readyz failed: %w", apiAddr, err)
		}
		caps, err := client.Capabilities(ctx)
		if err != nil {
			return DiscoveredTarget{}, fmt.Errorf("target %s capabilities failed: %w", apiAddr, err)
		}
		if err := validateCapacityCapabilities(caps); err != nil {
			return DiscoveredTarget{}, fmt.Errorf("target %s capabilities invalid: %w", apiAddr, err)
		}
		if manualGatewayAddrs {
			continue
		}
		capacityTarget, err := client.CapacityTarget(ctx)
		if err != nil {
			return DiscoveredTarget{}, fmt.Errorf("target %s capacity target failed: %w", apiAddr, err)
		}
		capacityTargets = append(capacityTargets, capacityTarget)
		if strings.TrimSpace(capacityTarget.Gateway.TCPAddr) != "" {
			gatewayAddrs = append(gatewayAddrs, capacityTarget.Gateway.TCPAddr)
		}
	}
	gatewayAddrs = dedupeStrings(gatewayAddrs)
	if len(gatewayAddrs) == 0 {
		return DiscoveredTarget{}, fmt.Errorf("capacity target returned empty gateway.tcp_addr; set WK_EXTERNAL_TCPADDR on target nodes or pass --gateway")
	}
	return DiscoveredTarget{
		Target: model.Target{
			Name:    "capacity-send",
			API:     model.TargetAPIConfig{Addrs: apiAddrs},
			Gateway: model.TargetGatewayConfig{TCP: model.TargetGatewayTCPConfig{Addrs: gatewayAddrs}},
			BenchAPI: model.BenchAPIConfig{
				Enabled: true,
				Addrs:   apiAddrs,
				Token:   cfg.BenchToken,
			},
		},
		CapacityTargets: capacityTargets,
	}, nil
}

func validateCapacityCapabilities(caps model.BenchCapabilities) error {
	if !caps.Enabled {
		return fmt.Errorf("bench api is not enabled")
	}
	if caps.Version != "bench/v1" {
		return fmt.Errorf("bench api version = %q, want bench/v1", caps.Version)
	}
	return nil
}

func dedupeStrings(items []string) []string {
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}
