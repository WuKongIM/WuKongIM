package plugin

import (
	"context"
	"errors"

	"github.com/WuKongIM/WuKongIM/internal/usecase/plugin/pluginproto"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
)

// SendMessage handles the legacy /message/send host RPC through the message usecase.
func (a *App) SendMessage(ctx context.Context, req *pluginproto.SendReq, _ string) (*pluginproto.SendResp, error) {
	if a == nil || a.messages == nil {
		return nil, ErrMessageSenderRequired
	}
	cmd, err := sendCommandFromPluginReq(req, a.defaultSenderUID)
	if err != nil {
		return nil, err
	}
	result, err := a.messages.Send(ctx, cmd)
	if err != nil {
		return nil, err
	}
	return sendRespFromResult(result), nil
}

// ChannelMessages handles the legacy /channel/messages host RPC through the authoritative reader.
func (a *App) ChannelMessages(ctx context.Context, req *pluginproto.ChannelMessageBatchReq, _ string) (*pluginproto.ChannelMessageBatchResp, error) {
	if a == nil || a.messageReader == nil {
		return nil, ErrMessageReaderRequired
	}
	if req == nil {
		req = &pluginproto.ChannelMessageBatchReq{}
	}
	resp := &pluginproto.ChannelMessageBatchResp{ChannelMessageResps: make([]*pluginproto.ChannelMessageResp, 0, len(req.GetChannelMessageReqs()))}
	for _, item := range req.GetChannelMessageReqs() {
		page, err := a.messageReader.SyncMessages(ctx, channelMessageQueryFromPluginReq(item))
		if errors.Is(err, metadb.ErrNotFound) {
			resp.ChannelMessageResps = append(resp.ChannelMessageResps, channelMessageRespFromPage(item, page))
			continue
		}
		if err != nil {
			return nil, err
		}
		resp.ChannelMessageResps = append(resp.ChannelMessageResps, channelMessageRespFromPage(item, page))
	}
	return resp, nil
}
