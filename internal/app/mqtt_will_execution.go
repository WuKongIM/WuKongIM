package app

import (
	"context"
	"fmt"

	clusterinfra "github.com/WuKongIM/WuKongIM/internal/infra/cluster"
	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	"github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
)

type mqttWillMessageSender interface {
	Send(context.Context, message.SendCommand) (message.SendResult, error)
}

// mqttWillPublications maps sibling DTOs to the existing SEND usecase. Retained
// evidence is read separately so SEND errors cannot erase an already committed Will.
type mqttWillPublications struct {
	*clusterinfra.MQTTWillReceipts
	messages mqttWillMessageSender
}

var _ mqttsession.WillPublications = mqttWillPublications{}

func (p mqttWillPublications) PublishWill(ctx context.Context, q mqttsession.WillPublication) error {
	r, err := p.messages.Send(ctx, message.SendCommand{FromUID: q.UID, ClientMsgNo: q.ClientMsgNo, ChannelID: q.Target.TargetID, ChannelType: q.Target.TargetType, Payload: q.Payload, PublicationMetadata: q.PublicationMetadata, NormalizePersonChannel: q.Target.TargetType == 1})
	if err != nil {
		return err
	}
	if r.Reason != message.ReasonSuccess {
		return fmt.Errorf("Will SEND reason %d: %w", r.Reason, mqttsession.ErrWillPending)
	}
	if r.MessageID == 0 || r.MessageSeq == 0 {
		return mqttsession.ErrEvidence
	}
	return nil
}

// newMQTTWillExecutor composes first dispatch and positive-receipt recovery with
// foreground cluster ports. Its caller owns scheduling and lifecycle admission;
// constructing it does not enable the product MQTT listener or uncertain retries.
func newMQTTWillExecutor(node *cluster.Node, messages *message.App, opts mqttsession.WillExecutionOptions) (*mqttsession.WillExecutor, error) {
	if node == nil || messages == nil {
		return nil, mqttsession.ErrInvalid
	}
	receipts, err := clusterinfra.NewMQTTWillReceipts(channels.NewSlotMetaSource(node), node)
	if err != nil {
		return nil, err
	}
	opts.Store = node
	opts.Authorizer = mqttWillAuthorizer{messages: messages}
	opts.Publications = mqttWillPublications{MQTTWillReceipts: receipts, messages: messages}
	return mqttsession.NewWillExecutor(opts)
}
