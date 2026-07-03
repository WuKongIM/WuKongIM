package node

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	pluginusecase "github.com/WuKongIM/WuKongIM/internal/usecase/plugin"
	"github.com/WuKongIM/WuKongIM/pkg/plugin/pluginproto"
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

func BenchmarkManagerPluginRPCHTTPForwardRequestCodec(b *testing.B) {
	for _, payloadSize := range []int{128, 1024, 16 * 1024} {
		b.Run(fmt.Sprintf("payload_%d", payloadSize), func(b *testing.B) {
			req := managerPluginRPCRequest{
				Op:       managerPluginOpHTTPForward,
				NodeID:   2,
				PluginNo: "bench.plugin",
				ForwardReq: &pluginproto.ForwardHttpReq{
					PluginNo: "bench.plugin",
					ToNodeId: 2,
					Request: &pluginproto.HttpRequest{
						Method:  http.MethodPost,
						Path:    "/bench",
						Headers: map[string]string{"X-Trace": "bench"},
						Query:   map[string]string{"q": "1"},
						Body:    make([]byte, payloadSize),
					},
				},
			}
			b.Run("encode", func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(int64(payloadSize))
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					body, err := encodeManagerPluginRequest(req)
					if err != nil {
						b.Fatal(err)
					}
					if len(body) == 0 {
						b.Fatal("empty encoded request")
					}
					benchmarkManagerPluginBytesSink = body
				}
			})
			body, err := encodeManagerPluginRequest(req)
			if err != nil {
				b.Fatal(err)
			}
			b.Run("decode", func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(int64(payloadSize))
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					got, err := decodeManagerPluginRequest(body)
					if err != nil {
						b.Fatal(err)
					}
					if got.ForwardReq.GetRequest().GetPath() != "/bench" {
						b.Fatal("invalid request")
					}
				}
			})
		})
	}
}

func BenchmarkManagerPluginRPCUpdateConfigRequestCodec(b *testing.B) {
	for _, payloadSize := range []int{128, 1024, 16 * 1024, 256 * 1024} {
		b.Run(fmt.Sprintf("payload_%d", payloadSize), func(b *testing.B) {
			config := benchmarkPluginConfig(payloadSize)
			req := managerPluginRPCRequest{
				Op:       managerPluginOpUpdateConfig,
				NodeID:   2,
				PluginNo: "bench.plugin",
				Config:   config,
			}
			b.Run("encode", func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(int64(len(config)))
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					body, err := encodeManagerPluginRequest(req)
					if err != nil {
						b.Fatal(err)
					}
					if len(body) == 0 {
						b.Fatal("empty encoded request")
					}
					benchmarkManagerPluginBytesSink = body
				}
			})
			body, err := encodeManagerPluginRequest(req)
			if err != nil {
				b.Fatal(err)
			}
			b.Run("decode", func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(int64(len(config)))
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					got, err := decodeManagerPluginRequest(body)
					if err != nil {
						b.Fatal(err)
					}
					if got.PluginNo != "bench.plugin" || len(got.Config) != len(config) {
						b.Fatal("invalid request")
					}
				}
			})
		})
	}
}

func BenchmarkManagerPluginRPCHTTPForwardResponseCodec(b *testing.B) {
	for _, payloadSize := range []int{128, 1024, 16 * 1024} {
		b.Run(fmt.Sprintf("payload_%d", payloadSize), func(b *testing.B) {
			resp := managerPluginRPCResponse{
				Status: rpcStatusOK,
				ForwardResp: &pluginproto.HttpResponse{
					Status:  http.StatusOK,
					Headers: map[string]string{"X-Plugin": "ok"},
					Body:    make([]byte, payloadSize),
				},
			}
			b.Run("encode", func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(int64(payloadSize))
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
			body, err := encodeManagerPluginResponse(resp)
			if err != nil {
				b.Fatal(err)
			}
			b.Run("decode", func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(int64(payloadSize))
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					got, err := decodeManagerPluginResponse(body)
					if err != nil {
						b.Fatal(err)
					}
					if got.ForwardResp.GetStatus() != http.StatusOK {
						b.Fatal("invalid response")
					}
					benchmarkManagerPluginRespSink = got
				}
			})
		})
	}
}

func benchmarkPluginConfig(size int) json.RawMessage {
	if size <= 32 {
		return json.RawMessage(`{"mode":"fast"}`)
	}
	value := make([]byte, size-20)
	for i := range value {
		value[i] = 'x'
	}
	return json.RawMessage(fmt.Sprintf(`{"payload":"%s"}`, string(value)))
}

func benchmarkNodeManagerPlugins(count int) []managementusecase.Plugin {
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
			LastError:        "last warning",
		})
	}
	return plugins
}
