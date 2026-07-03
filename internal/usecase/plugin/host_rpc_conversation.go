package plugin

import (
	"context"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/plugin/pluginproto"
)

// ConversationChannels handles PDK-compatible /conversation/channels host RPCs.
func (a *App) ConversationChannels(ctx context.Context, req *pluginproto.ConversationChannelReq, _ string) (*pluginproto.ConversationChannelResp, error) {
	if a == nil || a.conversations == nil {
		return nil, ErrConversationReaderRequired
	}
	if req == nil {
		req = &pluginproto.ConversationChannelReq{}
	}
	uid := strings.TrimSpace(req.GetUid())
	if uid == "" {
		return nil, ErrConversationUIDRequired
	}
	channels, err := a.conversations.ConversationChannels(ctx, uid, defaultHostConversationChannelsLimit)
	if err != nil {
		return nil, err
	}
	return conversationChannelsRespFromChannelIDs(channels), nil
}
