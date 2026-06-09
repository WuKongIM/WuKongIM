package channelwrite

import (
	"context"
	"errors"
	"time"

	runtimechannelid "github.com/WuKongIM/WuKongIM/internal/runtime/channelid"
)

const channelTypePerson uint8 = 1

// preparedSend is one validated send item ready to enter the channel pending queue.
type preparedSend struct {
	// Index is the original item index inside the submitted batch.
	Index int
	// Context is the normalized per-item context used by later append attempts.
	Context context.Context
	// Deadline bounds future durable append for this item.
	Deadline time.Time
	// Command is the canonical send command after request/person channel normalization.
	Command SendCommand
	// ServerTimestampMS is the server timestamp assigned exactly once during prepare.
	ServerTimestampMS int64
}

type preparePorts struct {
	messageID   MessageIDAllocator
	authorizer  Authorizer
	idempotency IdempotencyStore
	senderFence SenderFenceValidator
	clock       Clock
}

type prepareOutcome struct {
	results  []SendBatchItemResult
	prepared []preparedSend
}

type allowAllAuthorizer struct{}

func (allowAllAuthorizer) AuthorizeSend(context.Context, SendCommand) (Decision, error) {
	return Decision{Allowed: true, Reason: ReasonSuccess}, nil
}

func prepareBatch(runtimeCtx context.Context, items []SendBatchItem, ports preparePorts) prepareOutcome {
	results := make([]SendBatchItemResult, len(items))
	prepared := make([]preparedSend, 0, len(items))

	for i, item := range items {
		ctx := item.Context
		if ctx == nil {
			ctx = context.Background()
		}
		effectCtx, cancelEffectCtx := prepareItemContext(runtimeCtx, ctx)
		next, done := prepareSend(effectCtx, item.Command, ports)
		cancelEffectCtx()
		next.setItemMetadata(i, ctx, item.Deadline)
		if done {
			results[i] = SendBatchItemResult{Result: next.result, Err: next.err}
			continue
		}
		prepared = append(prepared, next.item)
		results[i] = SendBatchItemResult{Err: ErrNotAppended}
	}

	return prepareOutcome{
		results:  results,
		prepared: prepared,
	}
}

func prepareItemContext(runtimeCtx context.Context, itemCtx context.Context) (context.Context, context.CancelFunc) {
	if itemCtx == nil {
		itemCtx = context.Background()
	}
	if runtimeCtx == nil {
		ctx, cancel := context.WithCancel(itemCtx)
		return ctx, cancel
	}
	ctx, cancel := context.WithCancel(itemCtx)
	done := make(chan struct{})
	go func() {
		select {
		case <-runtimeCtx.Done():
			cancel()
		case <-ctx.Done():
		case <-done:
		}
	}()
	return ctx, func() {
		close(done)
		cancel()
	}
}

type prepareSendResult struct {
	item   preparedSend
	result SendResult
	err    error
}

func (r *prepareSendResult) setItemMetadata(index int, ctx context.Context, deadline time.Time) {
	r.item.Index = index
	r.item.Context = ctx
	r.item.Deadline = deadline
}

func prepareSend(ctx context.Context, cmd SendCommand, ports preparePorts) (prepareSendResult, bool) {
	if cmd.FromUID == "" {
		return prepareSendResult{result: SendResult{Reason: ReasonAuthFail}}, true
	}
	if cmd.RequestScoped || (len(cmd.MessageScopedUIDs) > 0 && cmd.ChannelID == "") {
		return prepareRequestScopedSend(ctx, cmd, ports)
	}
	if cmd.ChannelID == "" || cmd.ChannelType == 0 || len(cmd.Payload) == 0 {
		return prepareSendResult{result: SendResult{Reason: ReasonInvalidRequest}}, true
	}
	if cmd.NoPersist {
		return prepareSendResult{result: SendResult{Reason: ReasonUnsupported}}, true
	}
	return prepareCanonicalSend(ctx, cmd, ports, true)
}

func prepareRequestScopedSend(ctx context.Context, cmd SendCommand, ports preparePorts) (prepareSendResult, bool) {
	if len(cmd.Payload) == 0 {
		return prepareSendResult{result: SendResult{Reason: ReasonInvalidRequest}}, true
	}
	if !cmd.SyncOnce {
		return prepareSendResult{err: ErrRequestSubscribersRequireSyncOnce}, true
	}
	if cmd.ChannelID != "" {
		return prepareSendResult{err: ErrRequestSubscribersConflictChannel}, true
	}
	scoped, err := runtimechannelid.RequestSubscriberChannelFor(cmd.MessageScopedUIDs)
	if err != nil {
		if errors.Is(err, runtimechannelid.ErrRequestSubscribersRequired) {
			return prepareSendResult{err: ErrRequestSubscribersRequired}, true
		}
		return prepareSendResult{err: err}, true
	}
	cmd.ChannelID = scoped.CommandChannelID
	cmd.ChannelType = scoped.ChannelType
	cmd.MessageScopedUIDs = scoped.Subscribers
	cmd.NormalizePersonChannel = false
	if cmd.NoPersist {
		return prepareSendResult{result: SendResult{Reason: ReasonUnsupported}}, true
	}
	return prepareCanonicalSend(ctx, cmd, ports, false)
}

func prepareCanonicalSend(ctx context.Context, cmd SendCommand, ports preparePorts, normalizePerson bool) (prepareSendResult, bool) {
	if err := ctx.Err(); err != nil {
		return prepareSendResult{err: err}, true
	}
	if ports.senderFence != nil {
		if err := ports.senderFence.ValidateSender(ctx, cmd); err != nil {
			return prepareSendResult{err: err}, true
		}
	}
	if ports.authorizer != nil {
		decision, err := ports.authorizer.AuthorizeSend(ctx, cmd)
		if err != nil {
			return prepareSendResult{err: err}, true
		}
		if !decision.Allowed {
			reason := decision.Reason
			if reason == ReasonSuccess {
				reason = ReasonInvalidRequest
			}
			return prepareSendResult{result: SendResult{Reason: reason}}, true
		}
	}
	if normalizePerson && cmd.NormalizePersonChannel && cmd.ChannelType == channelTypePerson {
		channelID, err := runtimechannelid.NormalizePersonChannel(cmd.FromUID, cmd.ChannelID)
		if err != nil {
			return prepareSendResult{err: err}, true
		}
		cmd.ChannelID = channelID
	}
	if existing, ok, err := lookupIdempotentSend(ctx, cmd, ports); err != nil {
		return prepareSendResult{err: err}, true
	} else if ok {
		return prepareSendResult{result: existing}, true
	}
	if cmd.MessageID == 0 {
		if ports.messageID == nil {
			return prepareSendResult{err: ErrMessageIDAllocatorRequired}, true
		}
		cmd.MessageID = ports.messageID.Next()
	}
	clock := ports.clock
	if clock == nil {
		clock = systemClock{}
	}
	return prepareSendResult{
		item: preparedSend{
			Command:           cmd,
			ServerTimestampMS: clock.Now().UnixMilli(),
		},
	}, false
}

func lookupIdempotentSend(ctx context.Context, cmd SendCommand, ports preparePorts) (SendResult, bool, error) {
	if ports.idempotency == nil || cmd.ClientMsgNo == "" {
		return SendResult{}, false, nil
	}
	return ports.idempotency.LookupSend(ctx, IdempotencyQuery{
		FromUID:     cmd.FromUID,
		ClientMsgNo: cmd.ClientMsgNo,
		ChannelID:   cmd.ChannelID,
		ChannelType: cmd.ChannelType,
	})
}
