package recipient

import (
	"context"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/messageevents"
	conversationusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/conversation"
)

// Processor handles local recipient-authority post-commit work.
type Processor struct {
	localNodeID  uint64
	conversation ConversationUpdater
	delivery     DeliverySubmitter
}

// NewProcessor creates a Processor from entry-agnostic ports.
func NewProcessor(opts ProcessorOptions) *Processor {
	return &Processor{
		localNodeID:  opts.LocalNodeID,
		conversation: opts.Conversation,
		delivery:     opts.Delivery,
	}
}

// Process applies recipient-scoped conversation updates before delivery.
func (p *Processor) Process(ctx context.Context, req ProcessRequest) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if p == nil || !req.Target.IsLocal(p.localNodeID) {
		return ErrNotLeader
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	effectiveRecipients, effectiveUIDs := effectiveRecipientGroup(req.Recipients)
	if len(effectiveUIDs) == 0 {
		return nil
	}
	if p.delivery != nil && p.conversation == nil {
		return ErrConversationRequired
	}

	patches := recipientConversationPatches(req.Event, effectiveRecipients)
	if len(patches) > 0 && p.conversation != nil {
		if err := p.conversation.AdmitPatches(ctx, patches); err != nil {
			return err
		}
	}
	if p.delivery != nil {
		event := req.Event.Clone()
		event.MessageScopedUIDs = effectiveUIDs
		return p.delivery.SubmitDelivery(ctx, event)
	}
	return nil
}

func effectiveRecipientGroup(recipients []Recipient) ([]Recipient, []string) {
	if len(recipients) == 0 {
		return nil, nil
	}
	effectiveRecipients := make([]Recipient, 0, len(recipients))
	uids := make([]string, 0, len(recipients))
	for _, recipient := range recipients {
		if recipient.UID == "" {
			continue
		}
		effectiveRecipients = append(effectiveRecipients, recipient)
		uids = append(uids, recipient.UID)
	}
	return effectiveRecipients, uids
}

func recipientConversationPatches(event messageevents.MessageCommitted, recipients []Recipient) []conversationusecase.ActivePatch {
	if len(recipients) == 0 {
		return nil
	}
	patches := make([]conversationusecase.ActivePatch, 0, len(recipients))
	for _, recipient := range recipients {
		var visibleFloor uint64
		if recipient.JoinSeq > 0 {
			visibleFloor = recipient.JoinSeq - 1
		}
		patches = append(patches, conversationusecase.ActivePatch{
			UID:          recipient.UID,
			ChannelID:    event.ChannelID,
			ChannelType:  int64(event.ChannelType),
			ReadSeq:      visibleFloor,
			DeletedToSeq: visibleFloor,
			ActiveAt:     event.ServerTimestampMS,
			UpdatedAt:    event.ServerTimestampMS,
			MessageSeq:   event.MessageSeq,
		})
	}
	return patches
}
