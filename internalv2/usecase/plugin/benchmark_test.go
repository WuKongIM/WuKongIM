package plugin

import (
	"context"
	"fmt"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/usecase/plugin/pluginproto"
	pluginevents "github.com/WuKongIM/WuKongIM/internalv2/contracts/pluginevents"
	"github.com/stretchr/testify/require"
)

var benchmarkMessageBatchSink *pluginproto.MessageBatch
var benchmarkMessageBatchScalarSink uint64

func BenchmarkPersistAfterMessageBatchMapping(b *testing.B) {
	payloadSizes := []int{128, 1024, 16 * 1024}
	for _, payloadSize := range payloadSizes {
		name := fmt.Sprintf("payload_%d", payloadSize)
		b.Run(name, func(b *testing.B) {
			event := benchmarkPersistAfterEvent(payloadSize)
			b.ReportAllocs()
			b.SetBytes(int64(payloadSize))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				batch := messageBatchFromPersistAfter(event)
				if len(batch.Messages) != 1 {
					b.Fatal("empty batch")
				}
				msg := batch.Messages[0]
				if len(msg.Payload) != payloadSize {
					b.Fatal("invalid payload")
				}
				benchmarkMessageBatchScalarSink += msg.MessageSeq + uint64(msg.Payload[0])
				benchmarkMessageBatchSink = batch
			}
		})
	}
}

func BenchmarkPersistAfterCandidates(b *testing.B) {
	for _, count := range []int{1, 16, 128, 1024} {
		b.Run(fmt.Sprintf("plugins_%d", count), func(b *testing.B) {
			app, err := NewApp(Options{Runtime: &recordingRuntime{plugins: benchmarkPlugins(count)}, Invoker: &recordingInvoker{}})
			require.NoError(b, err)
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				candidates, err := app.PersistAfterPluginCandidates(context.Background())
				if err != nil {
					b.Fatal(err)
				}
				if len(candidates) == 0 {
					b.Fatal("empty candidates")
				}
			}
		})
	}
}

func BenchmarkListPlugins(b *testing.B) {
	for _, count := range []int{1, 16, 256, 1024} {
		b.Run(fmt.Sprintf("plugins_%d", count), func(b *testing.B) {
			app, err := NewApp(Options{Runtime: &recordingRuntime{plugins: benchmarkPlugins(count)}, Invoker: &recordingInvoker{}})
			require.NoError(b, err)
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				plugins, err := app.ListPlugins(context.Background())
				if err != nil {
					b.Fatal(err)
				}
				if len(plugins) != count {
					b.Fatalf("plugins = %d, want %d", len(plugins), count)
				}
			}
		})
	}
}

func benchmarkPersistAfterEvent(payloadSize int) pluginevents.PersistAfterCommitted {
	payload := make([]byte, payloadSize)
	for i := range payload {
		payload[i] = byte(i)
	}
	return pluginevents.PersistAfterCommitted{
		MessageID:         11,
		MessageSeq:        5,
		ChannelID:         "room",
		ChannelType:       2,
		FromUID:           "sender",
		SenderNodeID:      1,
		SenderSessionID:   2,
		ClientMsgNo:       "client-1",
		ServerTimestampMS: 1713859200123,
		Payload:           payload,
		RedDot:            true,
	}
}

func benchmarkPlugins(count int) []ObservedPlugin {
	plugins := make([]ObservedPlugin, 0, count)
	for i := 0; i < count; i++ {
		plugins = append(plugins, ObservedPlugin{
			No:       fmt.Sprintf("plugin-%04d", i),
			Methods:  []Method{MethodPersistAfter},
			Priority: i % 32,
			Status:   StatusRunning,
			Enabled:  true,
		})
	}
	return plugins
}
