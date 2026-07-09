package docker_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/app"
	productconfig "github.com/WuKongIM/WuKongIM/internal/config"
)

func TestDockerComposeNodeConfigsLoadWithCurrentConfigSurface(t *testing.T) {
	for _, node := range []string{"node1.toml", "node2.toml", "node3.toml"} {
		t.Run(node, func(t *testing.T) {
			_ = loadDockerNodeConfig(t, node)
		})
	}
}

func TestComposeNodeConfigsUseHotPathTuning(t *testing.T) {
	for _, node := range []string{"node1.toml", "node2.toml", "node3.toml"} {
		t.Run(node, func(t *testing.T) {
			cfg := loadDockerNodeConfig(t, node)
			if cfg.Cluster.Slots.HashSlotCount != 256 {
				t.Fatalf("%s HashSlotCount = %d, want 256", node, cfg.Cluster.Slots.HashSlotCount)
			}
			if cfg.Cluster.Channel.AppendBatchMaxRecords != 128 {
				t.Fatalf("%s AppendBatchMaxRecords = %d, want 128", node, cfg.Cluster.Channel.AppendBatchMaxRecords)
			}
			if cfg.Cluster.Channel.AppendBatchMaxWait != 250*time.Microsecond {
				t.Fatalf("%s AppendBatchMaxWait = %s, want 250us", node, cfg.Cluster.Channel.AppendBatchMaxWait)
			}
			if cfg.Cluster.Storage.CommitFlushWindow != time.Millisecond {
				t.Fatalf("%s CommitFlushWindow = %s, want 1ms", node, cfg.Cluster.Storage.CommitFlushWindow)
			}
			if cfg.Cluster.Storage.CommitMaxBytes != 131072 {
				t.Fatalf("%s CommitMaxBytes = %d, want 131072", node, cfg.Cluster.Storage.CommitMaxBytes)
			}
			if cfg.Cluster.Storage.CommitShards != 8 {
				t.Fatalf("%s CommitShards = %d, want 8", node, cfg.Cluster.Storage.CommitShards)
			}
			if cfg.Gateway.Runtime.AsyncSendWorkers != 128 {
				t.Fatalf("%s AsyncSendWorkers = %d, want 128", node, cfg.Gateway.Runtime.AsyncSendWorkers)
			}
			if cfg.Gateway.Transport.Gnet.NumEventLoop != 4 || !cfg.Gateway.Transport.Gnet.Multicore {
				t.Fatalf("%s gnet = %#v, want multicore with 4 event loops", node, cfg.Gateway.Transport.Gnet)
			}
			if !cfg.Observability.MetricsEnabled || !cfg.Observability.DebugAPIEnabled || !cfg.Observability.Diagnostics.Enabled {
				t.Fatalf("%s observability = %#v", node, cfg.Observability)
			}
		})
	}
}

func loadDockerNodeConfig(t *testing.T, node string) app.Config {
	t.Helper()
	repoRoot := dockerRepoRoot(t)
	cfg, err := productconfig.Load(productconfig.Options{
		Args:    []string{"-config", filepath.Join(repoRoot, "docker", "conf", node)},
		Environ: []string{"PATH=" + os.Getenv("PATH")},
	})
	if err != nil {
		t.Fatalf("load %s: %v", node, err)
	}
	return cfg
}

func dockerRepoRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test file path")
	}
	return filepath.Dir(filepath.Dir(filename))
}
