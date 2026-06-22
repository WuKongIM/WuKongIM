package plugin

import (
	"context"

	"github.com/WuKongIM/WuKongIM/internal/usecase/plugin/pluginproto"
)

// SendMessage handles PDK-compatible /message/send host RPCs through the v2 message usecase.
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
