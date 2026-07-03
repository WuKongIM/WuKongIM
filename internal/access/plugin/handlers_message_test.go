package plugin

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/plugin/pluginproto"
)

func TestMessageSendHandlerDecodesRequestUsesTimeoutContextAndWritesMessageID(t *testing.T) {
	uc := &fakeUsecase{sendResp: &pluginproto.SendResp{MessageId: 987}}
	srv := mustServer(t, uc)
	ctx := newFakeRPCContext(mustMarshal(t, &pluginproto.SendReq{
		Header:      &pluginproto.Header{NoPersist: true, SyncOnce: true, RedDot: true},
		ClientMsgNo: "client-1",
		FromUid:     "",
		ChannelId:   "g1",
		ChannelType: 2,
		Payload:     []byte("hello"),
	}))
	ctx.uid = "plugin.send"

	srv.handlePath("/message/send", ctx)

	if ctx.err != nil {
		t.Fatalf("unexpected WriteErr: %v", ctx.err)
	}
	if uc.sendCalls != 1 {
		t.Fatalf("SendMessage calls = %d, want 1", uc.sendCalls)
	}
	if uc.sendCaller != "plugin.send" {
		t.Fatalf("SendMessage caller = %q", uc.sendCaller)
	}
	if uc.sendReq == nil || uc.sendReq.GetClientMsgNo() != "client-1" || uc.sendReq.GetChannelId() != "g1" || string(uc.sendReq.GetPayload()) != "hello" {
		t.Fatalf("SendMessage request = %#v", uc.sendReq)
	}
	if h := uc.sendReq.GetHeader(); h == nil || !h.GetNoPersist() || !h.GetSyncOnce() || !h.GetRedDot() {
		t.Fatalf("SendMessage header = %#v", h)
	}
	deadline, ok := uc.sendCtx.Deadline()
	if !ok {
		t.Fatal("SendMessage context has no deadline")
	}
	if remaining := time.Until(deadline); remaining <= 0 || remaining > time.Second {
		t.Fatalf("deadline remaining = %v, want within server timeout", remaining)
	}
	var got pluginproto.SendResp
	mustUnmarshal(t, ctx.written, &got)
	if got.GetMessageId() != 987 {
		t.Fatalf("messageId = %d, want 987", got.GetMessageId())
	}
}

func TestChannelMessagesHandlerDecodesBatchUsesTimeoutContextAndWritesResponses(t *testing.T) {
	uc := &fakeUsecase{channelMessagesResp: &pluginproto.ChannelMessageBatchResp{ChannelMessageResps: []*pluginproto.ChannelMessageResp{{
		ChannelId:       "g1",
		ChannelType:     2,
		StartMessageSeq: 7,
		Limit:           1,
		Messages: []*pluginproto.Message{{
			MessageId:   1001,
			MessageSeq:  8,
			ClientMsgNo: "c1",
			ChannelId:   "g1",
			ChannelType: 2,
			Payload:     []byte("stored"),
		}},
	}}}}
	srv := mustServer(t, uc)
	ctx := newFakeRPCContext(mustMarshal(t, &pluginproto.ChannelMessageBatchReq{ChannelMessageReqs: []*pluginproto.ChannelMessageReq{{
		ChannelId:       "g1",
		ChannelType:     2,
		StartMessageSeq: 7,
		Limit:           1,
	}}}))
	ctx.uid = "plugin.messages"

	srv.handlePath("/channel/messages", ctx)

	if ctx.err != nil {
		t.Fatalf("unexpected WriteErr: %v", ctx.err)
	}
	if uc.channelMessagesCalls != 1 {
		t.Fatalf("ChannelMessages calls = %d, want 1", uc.channelMessagesCalls)
	}
	if uc.channelMessagesCaller != "plugin.messages" {
		t.Fatalf("ChannelMessages caller = %q", uc.channelMessagesCaller)
	}
	if got := uc.channelMessagesReq.GetChannelMessageReqs(); len(got) != 1 || got[0].GetChannelId() != "g1" || got[0].GetStartMessageSeq() != 7 || got[0].GetLimit() != 1 {
		t.Fatalf("ChannelMessages request = %#v", uc.channelMessagesReq)
	}
	deadline, ok := uc.channelMessagesCtx.Deadline()
	if !ok {
		t.Fatal("ChannelMessages context has no deadline")
	}
	if remaining := time.Until(deadline); remaining <= 0 || remaining > time.Second {
		t.Fatalf("deadline remaining = %v, want within server timeout", remaining)
	}
	var got pluginproto.ChannelMessageBatchResp
	mustUnmarshal(t, ctx.written, &got)
	if len(got.GetChannelMessageResps()) != 1 || len(got.GetChannelMessageResps()[0].GetMessages()) != 1 {
		t.Fatalf("channel message response = %#v", &got)
	}
	if got.GetChannelMessageResps()[0].GetMessages()[0].GetMessageId() != 1001 {
		t.Fatalf("messageId = %d, want 1001", got.GetChannelMessageResps()[0].GetMessages()[0].GetMessageId())
	}
}

func TestMessageHostRPCHandlersKeepShorterIncomingDeadline(t *testing.T) {
	for _, tc := range []struct {
		name string
		path string
		body []byte
		ctx  func(*fakeUsecase) context.Context
	}{
		{name: "send", path: "/message/send", body: mustMarshal(t, &pluginproto.SendReq{ClientMsgNo: "c1"}), ctx: func(uc *fakeUsecase) context.Context { return uc.sendCtx }},
		{name: "channel messages", path: "/channel/messages", body: mustMarshal(t, &pluginproto.ChannelMessageBatchReq{}), ctx: func(uc *fakeUsecase) context.Context { return uc.channelMessagesCtx }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			uc := &fakeUsecase{}
			srv := mustServer(t, uc)
			incoming, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()
			ctx := newFakeRPCContext(tc.body)
			ctx.uid = "plugin.deadline"
			ctx.ctx = incoming

			srv.handlePath(tc.path, ctx)

			if ctx.err != nil {
				t.Fatalf("unexpected WriteErr: %v", ctx.err)
			}
			deadline, ok := tc.ctx(uc).Deadline()
			if !ok {
				t.Fatal("usecase context has no deadline")
			}
			if remaining := time.Until(deadline); remaining <= 0 || remaining > 250*time.Millisecond {
				t.Fatalf("deadline remaining = %v, want incoming shorter deadline", remaining)
			}
		})
	}
}
