package plugin

import (
	"context"
	"fmt"

	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	"github.com/WuKongIM/WuKongIM/pkg/plugin/pluginproto"
)

// SendRejectedError preserves a non-success SEND outcome for entry-specific mapping.
// Its Reason is an internal code, not a WKProto wire reason.
type SendRejectedError struct {
	// Reason identifies the rejected admission or append outcome.
	Reason message.Reason
}

func (e *SendRejectedError) Error() string {
	return fmt.Sprintf("message send rejected: reason=%d", e.Reason)
}

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
	if result.Reason != message.ReasonSuccess {
		return nil, &SendRejectedError{Reason: result.Reason}
	}
	return sendRespFromResult(result), nil
}
