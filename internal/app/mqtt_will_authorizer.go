package app

import (
	"context"

	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	"github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
)

// mqttWillAuthorizer translates sibling DTOs only. Message owns publish policy;
// this setup check neither sends a message nor grants future Will execution.
type mqttWillAuthorizer struct{ messages *message.App }

var _ mqttsession.WillAuthorizer = mqttWillAuthorizer{}

func (a mqttWillAuthorizer) AuthorizeWill(ctx context.Context, uid string, target mqttsession.WillTarget) error {
	reason, err := a.messages.CheckPublishPermission(ctx, message.PublishPermissionQuery{FromUID: uid, TargetID: target.TargetID, TargetType: target.TargetType})
	if err != nil {
		return err
	}
	if reason != message.ReasonSuccess {
		return mqttsession.ErrWillDenied
	}
	return nil
}
