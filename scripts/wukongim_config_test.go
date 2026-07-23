package scripts_test

import (
	"os"
	"path/filepath"
	"testing"

	productconfig "github.com/WuKongIM/WuKongIM/internal/config"
)

func TestScriptWukongIMTOMLConfigsLoad(t *testing.T) {
	root := repoRoot(t)
	tests := []struct {
		name          string
		file          string
		nodeID        uint64
		hashSlotCount uint16
		apiAddr       string
		tcpAddr       string
	}{
		{
			name:          "single node cluster",
			file:          "wukongim.toml",
			nodeID:        1,
			hashSlotCount: 256,
			apiAddr:       "127.0.0.1:5001",
			tcpAddr:       "127.0.0.1:5100",
		},
		{
			name:          "three node 1",
			file:          "wukongim-node1.toml",
			nodeID:        1,
			hashSlotCount: 256,
			apiAddr:       "127.0.0.1:5011",
			tcpAddr:       "127.0.0.1:5111",
		},
		{
			name:          "three node 2",
			file:          "wukongim-node2.toml",
			nodeID:        2,
			hashSlotCount: 256,
			apiAddr:       "127.0.0.1:5012",
			tcpAddr:       "127.0.0.1:5112",
		},
		{
			name:          "three node 3",
			file:          "wukongim-node3.toml",
			nodeID:        3,
			hashSlotCount: 256,
			apiAddr:       "127.0.0.1:5013",
			tcpAddr:       "127.0.0.1:5113",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := productconfig.Load(productconfig.Options{
				Args:    []string{"-config", filepath.Join(root, "scripts", "wukongim", tt.file)},
				Environ: []string{"PATH=" + os.Getenv("PATH")},
			})
			if err != nil {
				t.Fatalf("Load(%s): %v", tt.file, err)
			}
			if cfg.NodeID != tt.nodeID {
				t.Fatalf("NodeID = %d, want %d", cfg.NodeID, tt.nodeID)
			}
			if cfg.Cluster.Slots.HashSlotCount != tt.hashSlotCount {
				t.Fatalf("HashSlotCount = %d, want %d", cfg.Cluster.Slots.HashSlotCount, tt.hashSlotCount)
			}
			if cfg.API.ListenAddr != tt.apiAddr {
				t.Fatalf("API.ListenAddr = %q, want %q", cfg.API.ListenAddr, tt.apiAddr)
			}
			if cfg.API.ExternalTCPAddr != tt.tcpAddr {
				t.Fatalf("API.ExternalTCPAddr = %q, want %q", cfg.API.ExternalTCPAddr, tt.tcpAddr)
			}
			if !cfg.Observability.MetricsEnabled || !cfg.Bench.APIEnabled {
				t.Fatalf("metrics/bench disabled in %s", tt.file)
			}
			if len(cfg.Gateway.Listeners) != 2 {
				t.Fatalf("Gateway.Listeners len = %d, want 2", len(cfg.Gateway.Listeners))
			}
			if cfg.Conversation.AuthorityFlushBatchRows != 512 {
				t.Fatalf("Conversation.AuthorityFlushBatchRows = %d, want 512", cfg.Conversation.AuthorityFlushBatchRows)
			}
		})
	}
}
