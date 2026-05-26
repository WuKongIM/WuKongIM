package plugin

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	"github.com/WuKongIM/WuKongIM/internal/usecase/plugin/pluginproto"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestHostRPCMessageSendMapsPluginRequestThroughMessageApp(t *testing.T) {
	sender := &recordingMessageSender{result: message.SendResult{MessageID: 4321, MessageSeq: 77, Reason: frame.ReasonSuccess}}
	app := mustNewTestApp(t, Options{
		Runtime:          newFakeRuntime(t.TempDir()),
		DesiredStore:     newFakeDesiredStore(),
		Messages:         sender,
		DefaultSenderUID: "____system",
	})

	resp, err := app.SendMessage(context.Background(), &pluginproto.SendReq{
		Header:      &pluginproto.Header{NoPersist: true, SyncOnce: true, RedDot: true},
		ClientMsgNo: "client-1",
		ChannelId:   "g1",
		ChannelType: uint32(frame.ChannelTypeGroup),
		Payload:     []byte("hello"),
	}, "plugin.send")

	if err != nil {
		t.Fatalf("SendMessage returned error: %v", err)
	}
	if resp.GetMessageId() != 4321 {
		t.Fatalf("messageId = %d, want 4321", resp.GetMessageId())
	}
	if sender.calls != 1 {
		t.Fatalf("message sender calls = %d, want 1", sender.calls)
	}
	cmd := sender.last
	if cmd.Origin != message.SendOriginPlugin {
		t.Fatalf("Origin = %q, want plugin", cmd.Origin)
	}
	if cmd.HookDepth != 0 || cmd.SkipPluginHooks {
		t.Fatalf("hook controls = depth %d skip %v", cmd.HookDepth, cmd.SkipPluginHooks)
	}
	if cmd.FromUID != "____system" {
		t.Fatalf("FromUID = %q, want default sender", cmd.FromUID)
	}
	if cmd.ClientMsgNo != "client-1" || cmd.ChannelID != "g1" || cmd.ChannelType != frame.ChannelTypeGroup || string(cmd.Payload) != "hello" {
		t.Fatalf("mapped command = %#v", cmd)
	}
	if cmd.Framer != (frame.Framer{NoPersist: true, SyncOnce: true, RedDot: true}) {
		t.Fatalf("Framer = %#v", cmd.Framer)
	}
}

func TestHostRPCMessageSendUsesRequestFromUIDWhenProvided(t *testing.T) {
	sender := &recordingMessageSender{result: message.SendResult{MessageID: 100, Reason: frame.ReasonSuccess}}
	app := mustNewTestApp(t, Options{Runtime: newFakeRuntime(t.TempDir()), DesiredStore: newFakeDesiredStore(), Messages: sender, DefaultSenderUID: "____system"})

	_, err := app.SendMessage(context.Background(), &pluginproto.SendReq{FromUid: "alice", ChannelId: "bob", ChannelType: uint32(frame.ChannelTypePerson), Payload: []byte("hi")}, "plugin.send")

	if err != nil {
		t.Fatalf("SendMessage returned error: %v", err)
	}
	if sender.last.FromUID != "alice" {
		t.Fatalf("FromUID = %q, want request sender", sender.last.FromUID)
	}
}

func TestHostRPCMessageSendRequiresMessageSenderAndDefaultSender(t *testing.T) {
	app := mustNewTestApp(t, Options{Runtime: newFakeRuntime(t.TempDir()), DesiredStore: newFakeDesiredStore(), DefaultSenderUID: "____system"})
	_, err := app.SendMessage(context.Background(), &pluginproto.SendReq{ChannelId: "g1", ChannelType: uint32(frame.ChannelTypeGroup), Payload: []byte("hi")}, "plugin.send")
	assertErrorIs(t, err, ErrMessageSenderRequired)

	app = mustNewTestApp(t, Options{Runtime: newFakeRuntime(t.TempDir()), DesiredStore: newFakeDesiredStore(), Messages: &recordingMessageSender{}})
	_, err = app.SendMessage(context.Background(), &pluginproto.SendReq{ChannelId: "g1", ChannelType: uint32(frame.ChannelTypeGroup), Payload: []byte("hi")}, "plugin.send")
	assertErrorIs(t, err, ErrDefaultSenderUIDRequired)
}

func TestHostRPCChannelMessagesReadsAuthoritativePagesAndMapsMessages(t *testing.T) {
	reader := &recordingMessageReader{pages: []message.ChannelMessagePage{{
		Messages: []channel.Message{{
			MessageID:   1001,
			MessageSeq:  8,
			ClientMsgNo: "client-8",
			StreamNo:    "stream-1",
			StreamID:    2,
			Timestamp:   1234,
			FromUID:     "alice",
			ChannelID:   "g1",
			ChannelType: frame.ChannelTypeGroup,
			Topic:       "topic-a",
			Payload:     []byte("stored"),
		}},
		HasMore: false,
	}, {
		Messages: []channel.Message{{MessageID: 1002, MessageSeq: 12, ChannelID: "p1", ChannelType: frame.ChannelTypePerson, FromUID: "bob", Payload: []byte("person")}},
	}}}
	app := mustNewTestApp(t, Options{Runtime: newFakeRuntime(t.TempDir()), DesiredStore: newFakeDesiredStore(), MessageReader: reader})

	resp, err := app.ChannelMessages(context.Background(), &pluginproto.ChannelMessageBatchReq{ChannelMessageReqs: []*pluginproto.ChannelMessageReq{{
		ChannelId:       "g1",
		ChannelType:     uint32(frame.ChannelTypeGroup),
		StartMessageSeq: 7,
		Limit:           1,
	}, {
		ChannelId:       "p1",
		ChannelType:     uint32(frame.ChannelTypePerson),
		StartMessageSeq: 11,
		Limit:           2,
	}}}, "plugin.reader")

	if err != nil {
		t.Fatalf("ChannelMessages returned error: %v", err)
	}
	if reader.calls != 2 {
		t.Fatalf("reader calls = %d, want 2", reader.calls)
	}
	if got := reader.queries[0]; got.ChannelID.ID != "g1" || got.ChannelID.Type != frame.ChannelTypeGroup || got.StartSeq != 7 || got.Limit != 1 || got.PullMode != message.PullModeUp {
		t.Fatalf("first query = %#v", got)
	}
	if got := reader.queries[1]; got.ChannelID.ID != "p1" || got.ChannelID.Type != frame.ChannelTypePerson || got.StartSeq != 11 || got.Limit != 2 || got.PullMode != message.PullModeUp {
		t.Fatalf("second query = %#v", got)
	}
	resps := resp.GetChannelMessageResps()
	if len(resps) != 2 {
		t.Fatalf("responses = %d, want 2", len(resps))
	}
	if resps[0].GetChannelId() != "g1" || resps[0].GetChannelType() != uint32(frame.ChannelTypeGroup) || resps[0].GetStartMessageSeq() != 7 || resps[0].GetLimit() != 1 {
		t.Fatalf("first response metadata = %#v", resps[0])
	}
	msgs := resps[0].GetMessages()
	if len(msgs) != 1 {
		t.Fatalf("first response messages = %d, want 1", len(msgs))
	}
	msg := msgs[0]
	if msg.GetMessageId() != 1001 || msg.GetMessageSeq() != 8 || msg.GetClientMsgNo() != "client-8" || msg.GetStreamNo() != "stream-1" || msg.GetStreamId() != 2 || msg.GetTimestamp() != 1234 || msg.GetFrom() != "alice" || msg.GetChannelId() != "g1" || msg.GetChannelType() != uint32(frame.ChannelTypeGroup) || msg.GetTopic() != "topic-a" || string(msg.GetPayload()) != "stored" {
		t.Fatalf("mapped message = %#v", msg)
	}
}

func TestHostRPCChannelMessagesDefaultsLegacyLimit(t *testing.T) {
	reader := &recordingMessageReader{}
	app := mustNewTestApp(t, Options{Runtime: newFakeRuntime(t.TempDir()), DesiredStore: newFakeDesiredStore(), MessageReader: reader})

	_, err := app.ChannelMessages(context.Background(), &pluginproto.ChannelMessageBatchReq{ChannelMessageReqs: []*pluginproto.ChannelMessageReq{{
		ChannelId:       "g1",
		ChannelType:     uint32(frame.ChannelTypeGroup),
		StartMessageSeq: 7,
	}}}, "plugin.reader")

	if err != nil {
		t.Fatalf("ChannelMessages returned error: %v", err)
	}
	if got := reader.queries[0].Limit; got != 100 {
		t.Fatalf("Limit = %d, want legacy default 100", got)
	}
}

func TestHostRPCChannelMessagesReturnsEmptyResponseForMissingChannel(t *testing.T) {
	reader := &recordingMessageReader{err: metadb.ErrNotFound}
	app := mustNewTestApp(t, Options{Runtime: newFakeRuntime(t.TempDir()), DesiredStore: newFakeDesiredStore(), MessageReader: reader})

	resp, err := app.ChannelMessages(context.Background(), &pluginproto.ChannelMessageBatchReq{ChannelMessageReqs: []*pluginproto.ChannelMessageReq{{
		ChannelId:       "missing",
		ChannelType:     uint32(frame.ChannelTypeGroup),
		StartMessageSeq: 7,
		Limit:           1,
	}}}, "plugin.reader")

	if err != nil {
		t.Fatalf("ChannelMessages returned error: %v", err)
	}
	resps := resp.GetChannelMessageResps()
	if len(resps) != 1 {
		t.Fatalf("responses = %d, want 1", len(resps))
	}
	if resps[0].GetChannelId() != "missing" || len(resps[0].GetMessages()) != 0 {
		t.Fatalf("missing-channel response = %#v", resps[0])
	}
}

func TestHostRPCChannelMessagesCapsLegacyLimit(t *testing.T) {
	reader := &recordingMessageReader{}
	app := mustNewTestApp(t, Options{Runtime: newFakeRuntime(t.TempDir()), DesiredStore: newFakeDesiredStore(), MessageReader: reader})

	_, err := app.ChannelMessages(context.Background(), &pluginproto.ChannelMessageBatchReq{ChannelMessageReqs: []*pluginproto.ChannelMessageReq{{
		ChannelId:       "g1",
		ChannelType:     uint32(frame.ChannelTypeGroup),
		StartMessageSeq: 7,
		Limit:           1<<32 - 1,
	}}}, "plugin.reader")

	if err != nil {
		t.Fatalf("ChannelMessages returned error: %v", err)
	}
	if got := reader.queries[0].Limit; got != 10000 {
		t.Fatalf("Limit = %d, want cap 10000", got)
	}
}

func TestHostRPCChannelMessagesResponseReflectsEffectiveLimit(t *testing.T) {
	reader := &recordingMessageReader{}
	app := mustNewTestApp(t, Options{Runtime: newFakeRuntime(t.TempDir()), DesiredStore: newFakeDesiredStore(), MessageReader: reader})

	resp, err := app.ChannelMessages(context.Background(), &pluginproto.ChannelMessageBatchReq{ChannelMessageReqs: []*pluginproto.ChannelMessageReq{{
		ChannelId:       "g1",
		ChannelType:     uint32(frame.ChannelTypeGroup),
		StartMessageSeq: 7,
	}}}, "plugin.reader")

	if err != nil {
		t.Fatalf("ChannelMessages returned error: %v", err)
	}
	if got := resp.GetChannelMessageResps()[0].GetLimit(); got != 100 {
		t.Fatalf("response limit = %d, want effective limit 100", got)
	}
}

func TestHostRPCChannelMessagesRequiresReader(t *testing.T) {
	app := mustNewTestApp(t, Options{Runtime: newFakeRuntime(t.TempDir()), DesiredStore: newFakeDesiredStore()})

	_, err := app.ChannelMessages(context.Background(), &pluginproto.ChannelMessageBatchReq{ChannelMessageReqs: []*pluginproto.ChannelMessageReq{{ChannelId: "g1", ChannelType: uint32(frame.ChannelTypeGroup)}}}, "plugin.reader")

	assertErrorIs(t, err, ErrMessageReaderRequired)
}

type recordingMessageSender struct {
	calls  int
	last   message.SendCommand
	result message.SendResult
	err    error
}

func (r *recordingMessageSender) Send(_ context.Context, cmd message.SendCommand) (message.SendResult, error) {
	r.calls++
	r.last = cmd
	r.last.Payload = append([]byte(nil), cmd.Payload...)
	return r.result, r.err
}

type recordingMessageReader struct {
	calls   int
	queries []message.ChannelMessageQuery
	pages   []message.ChannelMessagePage
	err     error
}

func (r *recordingMessageReader) SyncMessages(_ context.Context, query message.ChannelMessageQuery) (message.ChannelMessagePage, error) {
	r.calls++
	r.queries = append(r.queries, query)
	if r.err != nil {
		return message.ChannelMessagePage{}, r.err
	}
	if len(r.pages) == 0 {
		return message.ChannelMessagePage{}, nil
	}
	page := r.pages[0]
	r.pages = r.pages[1:]
	page.Messages = append([]channel.Message(nil), page.Messages...)
	return page, nil
}
