package app

import (
	"context"
	"errors"
	"fmt"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	clusterinfra "github.com/WuKongIM/WuKongIM/internal/infra/cluster"
	"github.com/WuKongIM/WuKongIM/internal/infra/mqttwill"
	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	"github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
)

type mqttWillMessageSender interface {
	PrepareWill(context.Context, message.WillSendCommand) ([]byte, message.Reason, error)
	SendPreparedWill(context.Context, message.WillSendCommand) (message.SendResult, error)
}

// mqttWillPublications maps sibling DTOs to message-owned preparation and dispatch. Retained
// evidence is read separately so SEND errors cannot erase an already committed Will.
type mqttWillPublications struct {
	*clusterinfra.MQTTWillReceipts
	messages mqttWillMessageSender
}

var _ mqttsession.WillPublications = mqttWillPublications{}

func (p mqttWillPublications) PublishWill(ctx context.Context, q mqttsession.WillPublication) error {
	// gofail: var wkMQTTWillPublicationAttempt bool
	// _ = wkMQTTWillPublicationAttempt
	r, err := p.messages.SendPreparedWill(ctx, mqttWillSendCommand(q))
	if errors.Is(err, message.ErrAppendNotSubmitted) {
		return mqttsession.ErrWillNotSubmitted
	}
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

func (p mqttWillPublications) PrepareWillPublication(ctx context.Context, q mqttsession.WillPublication) ([]byte, error) {
	body, reason, err := p.messages.PrepareWill(ctx, mqttWillSendCommand(q))
	if err != nil {
		return nil, err
	}
	if reason == message.ReasonSuccess {
		return body, nil
	}
	if reason == message.ReasonSystemError || reason == message.ReasonNodeNotMatch {
		return nil, mqttsession.ErrWillPending
	}
	return nil, mqttsession.ErrWillDenied
}

func mqttWillSendCommand(q mqttsession.WillPublication) message.WillSendCommand {
	return message.WillSendCommand{FromUID: q.UID, ClientMsgNo: q.ClientMsgNo, TargetID: q.Target.TargetID, TargetType: q.Target.TargetType, Payload: q.Payload, PublicationMetadata: q.PublicationMetadata, AppendAdmission: q.AppendAdmission}
}

// newMQTTWillExecutor composes frozen preparation, dispatch and positive recovery with
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

// mqttWillDispatches keeps reservation/admission local and sends proof/cleanup
// to the captured owning node, without resolving through Slot routing.
type mqttWillDispatches struct {
	*mqttwill.Attempts
	remote *accessnode.MQTTWillClient
}

func (p mqttWillDispatches) SealUndispatched(ctx context.Context, a contract.WillAttempt) error {
	return p.remote.SealUndispatched(ctx, a)
}
func (p mqttWillDispatches) ReleaseAttempt(ctx context.Context, a contract.WillAttempt) error {
	return p.remote.ReleaseAttempt(ctx, a)
}

// SealUndispatched pins one published generation; a closed journal never grants
// proof during Stop/restore, even while old RPC callbacks still reference it.
func (m *mqttProduct) SealUndispatched(ctx context.Context, a contract.WillAttempt) error {
	if m == nil {
		return mqttwill.ErrUnknown
	}
	g := m.current.Load()
	if g == nil || g.dispatches == nil {
		return mqttwill.ErrUnknown
	}
	return g.dispatches.SealUndispatched(ctx, a)
}

func (m *mqttProduct) ReleaseAttempt(ctx context.Context, a contract.WillAttempt) error {
	if m == nil {
		return mqttwill.ErrUnknown
	}
	g := m.current.Load()
	if g == nil || g.dispatches == nil {
		return mqttwill.ErrUnknown
	}
	return g.dispatches.ReleaseAttempt(ctx, a)
}
