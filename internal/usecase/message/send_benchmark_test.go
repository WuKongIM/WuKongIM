package message

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/contracts/messageevents"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func BenchmarkSend(b *testing.B) {
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		env  func(*testing.B) (*App, SendCommand)
	}{
		{name: "person_durable_channel_appender", env: newBenchPersonDurableChannelAppender},
		{name: "group_durable_permission_cache", env: newBenchGroupDurablePermissionCache},
		{name: "request_scoped_realtime", env: newBenchRequestScopedRealtime},
	} {
		b.Run(tc.name, func(b *testing.B) {
			app, cmd := tc.env(b)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				cmd.ClientSeq = uint64(i + 1)
				cmd.ClientMsgNo = "bench-" + strconv.Itoa(i)
				result, err := app.Send(ctx, cmd)
				if err != nil {
					b.Fatal(err)
				}
				if result.Reason != frame.ReasonSuccess {
					b.Fatalf("Send().Reason = %v, want %v", result.Reason, frame.ReasonSuccess)
				}
			}
		})
	}
}

func newBenchPersonDurableChannelAppender(b *testing.B) (*App, SendCommand) {
	b.Helper()
	cluster := &benchmarkChannelAppender{}
	app := New(Options{
		Now:                 fixedNowFn,
		ChannelAppender:     cluster,
		CommittedDispatcher: benchmarkCommittedDispatcher{},
		LocalNodeID:         1,
	})
	cmd := benchmarkSendCommand("u1", "u2", frame.ChannelTypePerson)
	return app, cmd
}

func newBenchGroupDurablePermissionCache(b *testing.B) (*App, SendCommand) {
	b.Helper()
	permissions := newFakePermissionStore()
	permissions.channels[permissionKey("g1", int64(frame.ChannelTypeGroup))] = metadb.Channel{
		ChannelID:   "g1",
		ChannelType: int64(frame.ChannelTypeGroup),
	}
	permissions.members[permissionKey("g1", int64(frame.ChannelTypeGroup))] = map[string]bool{"u1": true}
	cluster := &benchmarkChannelAppender{}
	app := New(Options{
		Now:                 fixedNowFn,
		ChannelAppender:     cluster,
		CommittedDispatcher: benchmarkCommittedDispatcher{},
		PermissionStore:     permissions,
		PermissionCacheTTL:  benchmarkPermissionCacheTTL,
		LocalNodeID:         1,
	})
	cmd := benchmarkSendCommand("u1", "g1", frame.ChannelTypeGroup)
	return app, cmd
}

func newBenchRequestScopedRealtime(b *testing.B) (*App, SendCommand) {
	b.Helper()
	app := New(Options{
		Now:                fixedNowFn,
		MessageIDs:         &benchmarkMessageIDGenerator{},
		RealtimeDispatcher: benchmarkRealtimeDispatcher{},
	})
	cmd := SendCommand{
		Framer:             frame.Framer{NoPersist: true, SyncOnce: true},
		FromUID:            "system",
		RequestSubscribers: []string{"u1", "u2", "u3", "u2"},
		Payload:            benchmarkSendPayload,
		Topic:              "bench-topic",
	}
	return app, cmd
}

var benchmarkSendPayload = []byte("send benchmark payload")

const benchmarkPermissionCacheTTL = 60 * time.Second

func benchmarkSendCommand(fromUID, channelID string, channelType uint8) SendCommand {
	return SendCommand{
		FromUID:     fromUID,
		ChannelID:   channelID,
		ChannelType: channelType,
		Payload:     benchmarkSendPayload,
		Topic:       "bench-topic",
	}
}

type benchmarkChannelAppender struct {
	messageID  uint64
	messageSeq uint64
}

func (c *benchmarkChannelAppender) Append(_ context.Context, req channel.AppendRequest) (channel.AppendResult, error) {
	c.messageID++
	c.messageSeq++
	msg := req.Message
	msg.MessageID = c.messageID
	msg.MessageSeq = c.messageSeq
	return channel.AppendResult{MessageID: msg.MessageID, MessageSeq: msg.MessageSeq, Message: msg}, nil
}

func (c *benchmarkChannelAppender) AppendBatch(_ context.Context, req channel.AppendBatchRequest) (channel.AppendBatchResult, error) {
	items := make([]channel.AppendBatchItemResult, len(req.Messages))
	for i, msg := range req.Messages {
		c.messageID++
		c.messageSeq++
		msg.MessageID = c.messageID
		msg.MessageSeq = c.messageSeq
		items[i] = channel.AppendBatchItemResult{MessageID: msg.MessageID, MessageSeq: msg.MessageSeq, Message: msg}
	}
	return channel.AppendBatchResult{Items: items}, nil
}

type benchmarkCommittedDispatcher struct{}

func (benchmarkCommittedDispatcher) SubmitCommitted(context.Context, messageevents.MessageCommitted) error {
	return nil
}

type benchmarkRealtimeDispatcher struct{}

func (benchmarkRealtimeDispatcher) SubmitRealtime(context.Context, messageevents.MessageRealtime) error {
	return nil
}

type benchmarkMessageIDGenerator struct {
	next uint64
}

func (g *benchmarkMessageIDGenerator) Next() uint64 {
	g.next++
	return g.next
}
