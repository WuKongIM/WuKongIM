package plugin

import (
	"context"
	"fmt"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/usecase/plugin/pluginproto"
	pluginevents "github.com/WuKongIM/WuKongIM/internalv2/contracts/pluginevents"
	"github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
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

func BenchmarkSendMessageFromPluginReq(b *testing.B) {
	for _, payloadSize := range []int{128, 1024, 16 * 1024} {
		b.Run(fmt.Sprintf("payload_%d", payloadSize), func(b *testing.B) {
			payload := make([]byte, payloadSize)
			for i := range payload {
				payload[i] = byte(i)
			}
			app, err := NewApp(Options{
				Runtime:          &recordingRuntime{},
				Invoker:          &recordingInvoker{},
				Messages:         &recordingMessageSender{result: messageResultForBenchmark()},
				DefaultSenderUID: "____system",
			})
			require.NoError(b, err)
			req := &pluginproto.SendReq{
				Header:      &pluginproto.Header{NoPersist: true, SyncOnce: true, RedDot: true},
				ClientMsgNo: "bench-client",
				ChannelId:   "receiver",
				ChannelType: 1,
				Payload:     payload,
			}
			b.ReportAllocs()
			b.SetBytes(int64(payloadSize))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				resp, err := app.SendMessage(context.Background(), req, "bench.plugin")
				if err != nil {
					b.Fatal(err)
				}
				if resp.GetMessageId() == 0 {
					b.Fatal("empty message id")
				}
			}
		})
	}
}

func BenchmarkChannelMessagesFromPluginReq(b *testing.B) {
	for _, count := range []int{1, 16, 128} {
		b.Run(fmt.Sprintf("items_%d", count), func(b *testing.B) {
			reader := &benchmarkChannelMessageReader{}
			app, err := NewApp(Options{
				Runtime:       &recordingRuntime{},
				Invoker:       &recordingInvoker{},
				MessageReader: reader,
			})
			require.NoError(b, err)
			req := benchmarkChannelMessagesReq(count)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				resp, err := app.ChannelMessages(context.Background(), req, "bench.plugin")
				if err != nil {
					b.Fatal(err)
				}
				if len(resp.GetChannelMessageResps()) != count {
					b.Fatal("invalid response count")
				}
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

func messageResultForBenchmark() message.SendResult {
	return message.SendResult{MessageID: 1, Reason: message.ReasonSuccess}
}

func benchmarkChannelMessagesReq(count int) *pluginproto.ChannelMessageBatchReq {
	req := &pluginproto.ChannelMessageBatchReq{ChannelMessageReqs: make([]*pluginproto.ChannelMessageReq, 0, count)}
	for i := 0; i < count; i++ {
		req.ChannelMessageReqs = append(req.ChannelMessageReqs, &pluginproto.ChannelMessageReq{
			ChannelId:       fmt.Sprintf("room-%d", i),
			ChannelType:     2,
			StartMessageSeq: 1,
			Limit:           1,
		})
	}
	return req
}

type benchmarkChannelMessageReader struct{}

func (benchmarkChannelMessageReader) SyncMessages(context.Context, message.ChannelMessageQuery) (message.ChannelMessagePage, error) {
	return message.ChannelMessagePage{Messages: []message.SyncedMessage{{
		MessageID:   1,
		MessageSeq:  1,
		ClientMsgNo: "bench-client",
		FromUID:     "u1",
		ChannelID:   "room",
		ChannelType: 2,
		Payload:     []byte("payload"),
	}}}, nil
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
