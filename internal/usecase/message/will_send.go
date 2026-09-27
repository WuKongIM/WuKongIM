package message

import (
	"bytes"
	"context"
	"reflect"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/publication"
)

// WillSendCommand carries a server-keyed ordinary publication, without device
// privilege or entry-specific controls. Its target remains the original peer UID.
type WillSendCommand struct {
	FromUID, TargetID, ClientMsgNo string
	TargetType                     uint8
	Payload, PublicationMetadata   []byte
}

// PrepareWill checks current policy and runs transformations without appending.
// The caller must durably freeze the owned result before SendPreparedWill.
func (a *App) PrepareWill(ctx context.Context, q WillSendCommand) ([]byte, Reason, error) {
	cmd, reason, err := a.authorizeWillSend(ctx, q)
	if err != nil || reason != ReasonSuccess {
		return nil, reason, err
	}
	mutated, reason, err := a.beforePluginSendHook(ctx, cmd.Clone())
	if err != nil || reason != ReasonSuccess {
		return nil, reason, err
	}
	if !validWillHookResult(cmd, mutated) {
		return nil, ReasonInvalidRequest, ErrInvalidCommand
	}
	if a.beforeSendWebhook != nil {
		mutated, reason, err = a.beforeSendWebhook.check(ctx, mutated, a.commandChannels)
		if err != nil || reason != ReasonSuccess {
			return nil, reason, err
		}
	}
	if !validWillHookResult(cmd, mutated) {
		return nil, ReasonInvalidRequest, ErrInvalidCommand
	}
	return bytes.Clone(mutated.Payload), ReasonSuccess, nil
}

func validWillHookResult(original, transformed SendCommand) bool {
	if len(transformed.Payload) > 65535 {
		return false
	}
	original.Payload, transformed.Payload = nil, nil
	return reflect.DeepEqual(original, transformed)
}

// SendPreparedWill rechecks current permission and uses ordinary directory and
// append orchestration. It never reruns transformations on durable hook output.
func (a *App) SendPreparedWill(ctx context.Context, q WillSendCommand) (SendResult, error) {
	cmd, reason, err := a.authorizeWillSend(ctx, q)
	if err != nil || reason != ReasonSuccess {
		return SendResult{Reason: reason}, err
	}
	if err := a.ensurePersonDirectory(ctx, cmd); err != nil {
		return SendResult{Reason: ReasonSystemError}, err
	}
	if a.submitter == nil {
		return SendResult{}, ErrRouteNotReady
	}
	return a.submitter.Send(ctx, cmd)
}

func (a *App) authorizeWillSend(ctx context.Context, q WillSendCommand) (SendCommand, Reason, error) {
	md, err := publication.Decode(q.PublicationMetadata)
	if err != nil || md.Source != publication.SourceWill || !publication.ValidServerWillKey(md.ServerWillKey) || !validPublishIdentity(q.ClientMsgNo) || len(q.Payload) > 65535 {
		return SendCommand{}, ReasonInvalidRequest, ErrInvalidCommand
	}
	reason, err := a.CheckPublishPermission(ctx, PublishPermissionQuery{FromUID: q.FromUID, TargetID: q.TargetID, TargetType: q.TargetType})
	if err != nil || reason != ReasonSuccess {
		return SendCommand{}, reason, err
	}
	cmd := SendCommand{FromUID: q.FromUID, ClientMsgNo: q.ClientMsgNo, ChannelID: q.TargetID, ChannelType: q.TargetType, Payload: q.Payload, PublicationMetadata: q.PublicationMetadata, NormalizePersonChannel: q.TargetType == channelTypePerson, Origin: SendOriginClient}
	if cmd.NormalizePersonChannel {
		cmd.ChannelID, err = channelid.NormalizePersonChannel(q.FromUID, q.TargetID)
		if err != nil {
			return SendCommand{}, ReasonInvalidRequest, err
		}
	}
	return cmd, ReasonSuccess, nil
}
