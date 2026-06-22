package node

import (
	"fmt"
	"testing"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	pluginusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/plugin"
)

var (
	benchmarkManagerPluginBytesSink []byte
	benchmarkManagerPluginRespSink  managerPluginRPCResponse
)

func BenchmarkManagerPluginRPCResponseEncode(b *testing.B) {
	for _, count := range []int{1, 16, 256, 1024} {
		b.Run(fmt.Sprintf("plugins_%d", count), func(b *testing.B) {
			resp := managerPluginRPCResponse{
				Status:  rpcStatusOK,
				Plugins: benchmarkNodeManagerPlugins(count),
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				body, err := encodeManagerPluginResponse(resp)
				if err != nil {
					b.Fatal(err)
				}
				if len(body) == 0 {
					b.Fatal("empty encoded response")
				}
				benchmarkManagerPluginBytesSink = body
			}
		})
	}
}

func BenchmarkManagerPluginRPCResponseDecode(b *testing.B) {
	for _, count := range []int{1, 16, 256, 1024} {
		b.Run(fmt.Sprintf("plugins_%d", count), func(b *testing.B) {
			body, err := encodeManagerPluginResponse(managerPluginRPCResponse{
				Status:  rpcStatusOK,
				Plugins: benchmarkNodeManagerPlugins(count),
			})
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				resp, err := decodeManagerPluginResponse(body)
				if err != nil {
					b.Fatal(err)
				}
				if len(resp.Plugins) != count {
					b.Fatalf("plugins = %d, want %d", len(resp.Plugins), count)
				}
				benchmarkManagerPluginRespSink = resp
			}
		})
	}
}

func benchmarkNodeManagerPlugins(count int) []managementusecase.Plugin {
	lastSeenAt := time.Date(2026, 6, 22, 10, 0, 0, 0, time.UTC)
	plugins := make([]managementusecase.Plugin, 0, count)
	for i := 0; i < count; i++ {
		plugins = append(plugins, managementusecase.Plugin{
			NodeID:           1,
			No:               fmt.Sprintf("plugin-%04d", i),
			Name:             fmt.Sprintf("Plugin %04d", i),
			Version:          "v1",
			Methods:          []pluginusecase.Method{pluginusecase.MethodPersistAfter, pluginusecase.MethodSend},
			Priority:         i % 32,
			PersistAfterSync: i%2 == 0,
			ReplySync:        i%3 == 0,
			Status:           "running",
			Enabled:          true,
			IsAI:             uint8(i % 2),
			PID:              1000 + i,
			LastSeenAt:       lastSeenAt,
			LastError:        "last warning",
		})
	}
	return plugins
}
