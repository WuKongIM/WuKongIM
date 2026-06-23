package manager

import (
	"fmt"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/usecase/plugin/pluginproto"
	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	pluginusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/plugin"
)

var benchmarkPluginDTOsSink []PluginDTO

func BenchmarkPluginDTOs(b *testing.B) {
	for _, count := range []int{1, 16, 256, 1024} {
		b.Run(fmt.Sprintf("plugins_%d", count), func(b *testing.B) {
			plugins := benchmarkManagerPlugins(count)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				items := pluginDTOs(plugins)
				if len(items) != count {
					b.Fatalf("items = %d, want %d", len(items), count)
				}
				benchmarkPluginDTOsSink = items
			}
		})
	}
}

func benchmarkManagerPlugins(count int) []managementusecase.Plugin {
	lastSeenAt := time.Date(2026, 6, 22, 10, 0, 0, 0, time.UTC)
	createdAt := lastSeenAt.Add(-time.Hour)
	updatedAt := lastSeenAt.Add(-time.Minute)
	plugins := make([]managementusecase.Plugin, 0, count)
	for i := 0; i < count; i++ {
		plugins = append(plugins, managementusecase.Plugin{
			NodeID:           1,
			No:               fmt.Sprintf("plugin-%04d", i),
			Name:             fmt.Sprintf("Plugin %04d", i),
			Version:          "v1",
			ConfigTemplate:   &pluginproto.ConfigTemplate{Fields: []*pluginproto.Field{{Name: "mode", Type: "string", Label: "Mode"}}},
			Config:           map[string]any{"mode": "fast", "shard": float64(i % 16)},
			CreatedAt:        &createdAt,
			UpdatedAt:        &updatedAt,
			Methods:          []pluginusecase.Method{pluginusecase.MethodPersistAfter, pluginusecase.MethodSend},
			Priority:         i % 32,
			PersistAfterSync: i%2 == 0,
			ReplySync:        i%3 == 0,
			Status:           "running",
			Enabled:          true,
			IsAI:             uint8(i % 2),
			PID:              1000 + i,
			LastSeenAt:       lastSeenAt,
		})
	}
	return plugins
}
