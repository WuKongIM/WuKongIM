package plugin

import (
	"context"
	"errors"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/plugin/pluginproto"
)

// ChannelMessages handles PDK-compatible /channel/messages host RPCs through the v2 message reader.
func (a *App) ChannelMessages(ctx context.Context, req *pluginproto.ChannelMessageBatchReq, _ string) (*pluginproto.ChannelMessageBatchResp, error) {
	if a == nil || a.messageReader == nil {
		return nil, ErrMessageReaderRequired
	}
	if req == nil {
		req = &pluginproto.ChannelMessageBatchReq{}
	}
	resp := &pluginproto.ChannelMessageBatchResp{
		ChannelMessageResps: make([]*pluginproto.ChannelMessageResp, 0, len(req.GetChannelMessageReqs())),
	}
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
