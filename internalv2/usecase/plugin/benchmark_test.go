package plugin

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/usecase/plugin/pluginproto"
	pluginevents "github.com/WuKongIM/WuKongIM/internalv2/contracts/pluginevents"
	"github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
	"github.com/stretchr/testify/require"
)

var benchmarkMessageBatchSink *pluginproto.MessageBatch
var benchmarkMessageBatchScalarSink uint64
var benchmarkHTTPResponseSink *pluginproto.HttpResponse

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

func BenchmarkClusterConfigFromSnapshot(b *testing.B) {
	app, err := NewApp(Options{
		Runtime:       &recordingRuntime{},
		Invoker:       &recordingInvoker{},
		ClusterReader: benchmarkClusterReader{snapshot: benchmarkClusterSnapshot(3, 256)},
	})
	require.NoError(b, err)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resp, err := app.ClusterConfig(context.Background(), "bench.plugin")
		if err != nil {
			b.Fatal(err)
		}
		if len(resp.GetSlots()) != 256 {
			b.Fatal("invalid slot count")
		}
	}
}

func BenchmarkClusterChannelsBelongNode(b *testing.B) {
	for _, count := range []int{1, 16, 128} {
		b.Run(fmt.Sprintf("items_%d", count), func(b *testing.B) {
			app, err := NewApp(Options{
				Runtime:       &recordingRuntime{},
				Invoker:       &recordingInvoker{},
				ChannelOwners: benchmarkChannelOwnerReader{},
			})
			require.NoError(b, err)
			req := benchmarkClusterBelongNodeReq(count)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				resp, err := app.ClusterChannelsBelongNode(context.Background(), req, "bench.plugin")
				if err != nil {
					b.Fatal(err)
				}
				if len(resp.GetClusterChannelBelongNodeResps()) == 0 {
					b.Fatal("empty response")
				}
			}
		})
	}
}

func BenchmarkConversationChannels(b *testing.B) {
	for _, count := range []int{1, 16, 128, 1000} {
		b.Run(fmt.Sprintf("items_%d", count), func(b *testing.B) {
			app, err := NewApp(Options{
				Runtime:       &recordingRuntime{},
				Invoker:       &recordingInvoker{},
				Conversations: benchmarkConversationReader{channels: benchmarkConversationChannels(count)},
			})
			require.NoError(b, err)
			req := &pluginproto.ConversationChannelReq{Uid: "bench-user"}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				resp, err := app.ConversationChannels(context.Background(), req, "bench.plugin")
				if err != nil {
					b.Fatal(err)
				}
				if len(resp.GetChannels()) != count {
					b.Fatal("invalid response count")
				}
			}
		})
	}
}

func BenchmarkHTTPForward(b *testing.B) {
	for _, payloadSize := range []int{128, 1024, 16 * 1024} {
		b.Run(fmt.Sprintf("local_payload_%d", payloadSize), func(b *testing.B) {
			payload := make([]byte, payloadSize)
			invoker := &recordingHTTPRouteInvoker{
				response: &pluginproto.HttpResponse{
					Status:  http.StatusOK,
					Headers: map[string]string{"X-Plugin": "ok"},
					Body:    []byte("ok"),
				},
			}
			app, err := NewApp(Options{Runtime: &recordingRuntime{}, Invoker: invoker})
			require.NoError(b, err)
			req := &pluginproto.ForwardHttpReq{
				PluginNo: "bench.plugin",
				Request: &pluginproto.HttpRequest{
					Method:  http.MethodPost,
					Path:    "/echo",
					Headers: map[string]string{"X-Trace": "bench"},
					Query:   map[string]string{"q": "1"},
					Body:    payload,
				},
			}
			b.ReportAllocs()
			b.SetBytes(int64(payloadSize))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				resp, err := app.HTTPForward(context.Background(), req, "bench.plugin")
				if err != nil {
					b.Fatal(err)
				}
				if resp.GetStatus() != http.StatusOK {
					b.Fatal("invalid status")
				}
				benchmarkHTTPResponseSink = resp
			}
		})
		b.Run(fmt.Sprintf("remote_payload_%d", payloadSize), func(b *testing.B) {
			payload := make([]byte, payloadSize)
			forwarder := &recordingHTTPForwarder{resp: &pluginproto.HttpResponse{Status: http.StatusAccepted}}
			app, err := NewApp(Options{Runtime: &recordingRuntime{}, Invoker: &recordingHTTPRouteInvoker{}, HTTPForwarder: forwarder})
			require.NoError(b, err)
			req := &pluginproto.ForwardHttpReq{
				PluginNo: "bench.plugin",
				ToNodeId: 2,
				Request: &pluginproto.HttpRequest{
					Method:  http.MethodPost,
					Path:    "/remote",
					Headers: map[string]string{"X-Trace": "bench"},
					Body:    payload,
				},
			}
			b.ReportAllocs()
			b.SetBytes(int64(payloadSize))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				resp, err := app.HTTPForward(context.Background(), req, "bench.plugin")
				if err != nil {
					b.Fatal(err)
				}
				if resp.GetStatus() != http.StatusAccepted {
					b.Fatal("invalid status")
				}
				benchmarkHTTPResponseSink = resp
			}
		})
	}
}

type benchmarkConversationReader struct {
	channels []message.ChannelID
}

func (r benchmarkConversationReader) ConversationChannels(context.Context, string, int) ([]message.ChannelID, error) {
	return r.channels, nil
}

func benchmarkConversationChannels(count int) []message.ChannelID {
	channels := make([]message.ChannelID, 0, count)
	for i := 0; i < count; i++ {
		channels = append(channels, message.ChannelID{
			ID:   fmt.Sprintf("room-%d", i),
			Type: uint8((i % 3) + 1),
		})
	}
	return channels
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

type benchmarkClusterReader struct {
	snapshot ClusterSnapshot
}

func (r benchmarkClusterReader) ClusterSnapshot(context.Context) (ClusterSnapshot, error) {
	return r.snapshot, nil
}

func benchmarkClusterSnapshot(nodes int, slots int) ClusterSnapshot {
	snapshot := ClusterSnapshot{
		Nodes: make([]ClusterNode, 0, nodes),
		Slots: make([]ClusterSlot, 0, slots),
	}
	for i := 1; i <= nodes; i++ {
		snapshot.Nodes = append(snapshot.Nodes, ClusterNode{
			ID:          uint64(i),
			ClusterAddr: fmt.Sprintf("127.0.0.1:%d", 7000+i),
			Online:      true,
		})
	}
	for i := 1; i <= slots; i++ {
		snapshot.Slots = append(snapshot.Slots, ClusterSlot{
			ID:       uint32(i),
			Leader:   uint64((i % nodes) + 1),
			Term:     uint32(i),
			Replicas: []uint64{1, 2, 3},
		})
	}
	return snapshot
}

type benchmarkChannelOwnerReader struct{}

func (benchmarkChannelOwnerReader) ChannelOwnerNode(_ context.Context, id message.ChannelID) (uint64, error) {
	return uint64(id.Type%3) + 1, nil
}

func benchmarkClusterBelongNodeReq(count int) *pluginproto.ClusterChannelBelongNodeReq {
	req := &pluginproto.ClusterChannelBelongNodeReq{Channels: make([]*pluginproto.Channel, 0, count)}
	for i := 0; i < count; i++ {
		req.Channels = append(req.Channels, &pluginproto.Channel{
			ChannelId:   fmt.Sprintf("room-%d", i),
			ChannelType: uint32((i % 3) + 1),
		})
	}
	return req
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
