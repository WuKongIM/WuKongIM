package plugin

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/plugin/pluginproto"
)

func TestConversationChannelsHandlerDecodesRequestUsesTimeoutAndWritesChannels(t *testing.T) {
	uc := &fakeUsecase{conversationChannelsResp: &pluginproto.ConversationChannelResp{
		Channels: []*pluginproto.Channel{
			{ChannelId: "g1", ChannelType: 2},
			{ChannelId: "u2", ChannelType: 1},
		},
	}}
	srv := mustServer(t, uc)
	ctx := newFakeRPCContext(mustMarshal(t, &pluginproto.ConversationChannelReq{Uid: "u1"}))
	ctx.uid = "plugin.conversation"

	srv.handlePath("/conversation/channels", ctx)

	if ctx.err != nil {
		t.Fatalf("unexpected WriteErr: %v", ctx.err)
	}
	if uc.conversationChannelsCalls != 1 {
		t.Fatalf("ConversationChannels calls = %d, want 1", uc.conversationChannelsCalls)
	}
	if uc.conversationChannelsCaller != "plugin.conversation" {
		t.Fatalf("ConversationChannels caller = %q", uc.conversationChannelsCaller)
	}
	if uc.conversationChannelsReq.GetUid() != "u1" {
		t.Fatalf("ConversationChannels request = %#v", uc.conversationChannelsReq)
	}
	deadline, ok := uc.conversationChannelsCtx.Deadline()
	if !ok {
		t.Fatal("ConversationChannels context has no deadline")
	}
	if remaining := time.Until(deadline); remaining <= 0 || remaining > time.Second {
		t.Fatalf("deadline remaining = %v, want within server timeout", remaining)
	}
	var got pluginproto.ConversationChannelResp
	mustUnmarshal(t, ctx.written, &got)
	if len(got.GetChannels()) != 2 || got.GetChannels()[0].GetChannelId() != "g1" || got.GetChannels()[1].GetChannelType() != 1 {
		t.Fatalf("conversation response = %#v", &got)
	}
}

func TestConversationChannelsHandlerKeepsShorterIncomingDeadline(t *testing.T) {
	uc := &fakeUsecase{}
	srv := mustServer(t, uc)
	incoming, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	ctx := newFakeRPCContext(mustMarshal(t, &pluginproto.ConversationChannelReq{Uid: "u1"}))
	ctx.uid = "plugin.deadline"
	ctx.ctx = incoming

	srv.handlePath("/conversation/channels", ctx)

	if ctx.err != nil {
		t.Fatalf("unexpected WriteErr: %v", ctx.err)
	}
	deadline, ok := uc.conversationChannelsCtx.Deadline()
	if !ok {
		t.Fatal("ConversationChannels context has no deadline")
	}
	if remaining := time.Until(deadline); remaining <= 0 || remaining > 250*time.Millisecond {
		t.Fatalf("deadline remaining = %v, want incoming shorter deadline", remaining)
	}
}
