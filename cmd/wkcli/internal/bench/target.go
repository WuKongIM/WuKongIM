package bench

import (
	"context"
	"fmt"
	"strings"

	contextcmd "github.com/WuKongIM/WuKongIM/cmd/wkcli/internal/context"
	benchtarget "github.com/WuKongIM/WuKongIM/internal/bench/target"
)

type resolvedTarget struct {
	APIAddrs     []string
	GatewayAddrs []string
}

func resolveTarget(ctx context.Context, cfg sendConfig) (resolvedTarget, error) {
	gatewayAddrs := splitValues(cfg.GatewayAddrs)
	if len(gatewayAddrs) > 0 {
		return resolvedTarget{GatewayAddrs: dedupeValues(gatewayAddrs)}, nil
	}

	apiAddrs := splitValues(cfg.ServerAddrs)
	if len(apiAddrs) == 0 {
		contextServers, err := contextServerAddrs(cfg)
		if err != nil {
			return resolvedTarget{}, err
		}
		apiAddrs = contextServers
	}
	if len(apiAddrs) == 0 {
		return resolvedTarget{}, fmt.Errorf("no target configured; pass --gateway, --server, --context, or select a wkcli context")
	}

	for _, apiAddr := range apiAddrs {
		client := benchtarget.NewClient(benchtarget.Config{APIAddrs: []string{apiAddr}, Token: cfg.BenchToken})
		if err := client.Healthz(ctx); err != nil {
			return resolvedTarget{}, fmt.Errorf("target %s /healthz failed: %w", apiAddr, err)
		}
		if err := client.Readyz(ctx); err != nil {
			return resolvedTarget{}, fmt.Errorf("target %s /readyz failed: %w", apiAddr, err)
		}
		capacityTarget, err := client.CapacityTarget(ctx)
		if err != nil {
			return resolvedTarget{}, fmt.Errorf("target %s capacity target failed: %w", apiAddr, err)
		}
		if strings.TrimSpace(capacityTarget.Gateway.TCPAddr) != "" {
			gatewayAddrs = append(gatewayAddrs, capacityTarget.Gateway.TCPAddr)
		}
	}
	gatewayAddrs = dedupeValues(gatewayAddrs)
	if len(gatewayAddrs) == 0 {
		return resolvedTarget{}, fmt.Errorf("capacity target returned empty gateway.tcp_addr; set WK_EXTERNAL_TCPADDR or pass --gateway")
	}
	return resolvedTarget{APIAddrs: dedupeValues(apiAddrs), GatewayAddrs: gatewayAddrs}, nil
}

func contextServerAddrs(cfg sendConfig) ([]string, error) {
	storeDir := strings.TrimSpace(cfg.ContextDir)
	if storeDir == "" {
		storeDir = contextcmd.DefaultStoreDir()
	}
	store := contextcmd.NewStore(storeDir)
	name := strings.TrimSpace(cfg.ContextName)
	if name == "" {
		current, err := store.Current()
		if err != nil {
			return nil, err
		}
		name = current
	}
	if name == "" {
		return nil, fmt.Errorf("no current context selected")
	}
	saved, err := store.Load(name)
	if err != nil {
		return nil, err
	}
	return splitValues(saved.Servers), nil
}

func splitValues(values []string) []string {
	var out []string
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			trimmed := strings.TrimSpace(part)
			if trimmed != "" {
				out = append(out, trimmed)
			}
		}
	}
	return out
}

func dedupeValues(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
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
