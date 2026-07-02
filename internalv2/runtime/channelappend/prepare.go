package channelappend

import (
	"context"
	"errors"
	"time"

	runtimechannelid "github.com/WuKongIM/WuKongIM/internal/runtime/channelid"
)

const channelTypePerson uint8 = 1

const (
	idempotencyFNV64aOffset = 14695981039346656037
	idempotencyFNV64aPrime  = 1099511628211
)

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
	future            *Future
}

type preparePorts struct {
	messageID   MessageIDAllocator
	authorizer  Authorizer
	idempotency IdempotencyStore
	senderFence SenderFenceValidator
	clock       Clock
}

type prepareOutcome struct {
	results          []SendBatchItemResult
	prepared         []preparedSend
	realtime         []preparedSend
	canonicalResults []canonicalTerminalResult
}

type allowAllAuthorizer struct{}

func (allowAllAuthorizer) AuthorizeSend(context.Context, SendCommand) (Decision, error) {
	return Decision{Allowed: true, Reason: ReasonSuccess}, nil
}

func prepareBatch(runtimeCtx context.Context, items []SendBatchItem, ports preparePorts) prepareOutcome {
	results := make([]SendBatchItemResult, len(items))
	prepared := make([]preparedSend, 0, len(items))
	var realtime []preparedSend
	canonicalResults := make([]canonicalTerminalResult, 0, len(items))

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
			if next.canonicalResult {
				canonicalResults = append(canonicalResults, canonicalTerminalResult{
					index:   i,
					command: next.command,
				})
			}
			continue
		}
		if next.realtime {
			realtime = append(realtime, next.item)
			continue
		}
		prepared = append(prepared, next.item)
	}

	return prepareOutcome{
		results:          results,
		prepared:         prepared,
		realtime:         realtime,
		canonicalResults: canonicalResults,
	}
}

type canonicalTerminalResult struct {
	index   int
	command SendCommand
}

func preparedCommandMatchesTarget(target AuthorityTarget, cmd SendCommand) bool {
	return target.ChannelID.ID == cmd.ChannelID && target.ChannelID.Type == cmd.ChannelType
}

func prepareItemContext(runtimeCtx context.Context, itemCtx context.Context) (context.Context, context.CancelFunc) {
	if itemCtx == nil {
		itemCtx = context.Background()
	}
	if runtimeCtx == nil || runtimeCtx.Done() == nil {
		return itemCtx, noopCancel
	}
	ctx, cancel := context.WithCancel(itemCtx)
	stopRuntimeCancel := context.AfterFunc(runtimeCtx, cancel)
	return ctx, func() {
		stopRuntimeCancel()
		cancel()
	}
}

func noopCancel() {}

type prepareSendResult struct {
	item            preparedSend
	result          SendResult
	err             error
	command         SendCommand
	canonicalResult bool
	realtime        bool
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
		return prepareNoPersistSend(ctx, cmd, ports, true)
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
		return prepareNoPersistRealtimeSend(ctx, cmd, ports, false)
	}
	return prepareCanonicalSend(ctx, cmd, ports, false)
}

func prepareCanonicalSend(ctx context.Context, cmd SendCommand, ports preparePorts, normalizePerson bool) (prepareSendResult, bool) {
	nextCmd, result, done := prepareValidatedCommand(ctx, cmd, ports, normalizePerson)
	if done {
		return result, true
	}
	cmd = nextCmd
	if existing, ok, err := lookupIdempotentSend(ctx, cmd, ports); err != nil {
		return prepareSendResult{err: err}, true
	} else if ok {
		return prepareSendResult{result: existing, command: cmd, canonicalResult: true}, true
	}
	return prepareAllocatedSend(cmd, ports, false)
}

func prepareNoPersistSend(ctx context.Context, cmd SendCommand, ports preparePorts, normalizePerson bool) (prepareSendResult, bool) {
	sourceChannelID, alreadyCommandChannel := runtimechannelid.FromCommandChannel(cmd.ChannelID)
	cmd.ChannelID = sourceChannelID
	if !cmd.SyncOnce && !alreadyCommandChannel {
		nextCmd, result, done := prepareValidatedCommand(ctx, cmd, ports, normalizePerson)
		if done {
			return result, true
		}
		return prepareSendResult{result: SendResult{Reason: ReasonSuccess}, command: nextCmd, canonicalResult: true}, true
	}
	cmd.ChannelID = runtimechannelid.ToCommandChannel(cmd.ChannelID)
	return prepareNoPersistRealtimeSend(ctx, cmd, ports, normalizePerson)
}

func prepareNoPersistRealtimeSend(ctx context.Context, cmd SendCommand, ports preparePorts, normalizePerson bool) (prepareSendResult, bool) {
	nextCmd, result, done := prepareValidatedCommand(ctx, cmd, ports, normalizePerson)
	if done {
		return result, true
	}
	cmd = nextCmd
	if !runtimechannelid.IsCommandChannel(cmd.ChannelID) {
		cmd.ChannelID = runtimechannelid.ToCommandChannel(cmd.ChannelID)
	}
	return prepareAllocatedSend(cmd, ports, true)
}

func prepareValidatedCommand(ctx context.Context, cmd SendCommand, ports preparePorts, normalizePerson bool) (SendCommand, prepareSendResult, bool) {
	if err := ctx.Err(); err != nil {
		return cmd, prepareSendResult{err: err}, true
	}
	if ports.senderFence != nil {
		if err := ports.senderFence.ValidateSender(ctx, cmd); err != nil {
			return cmd, prepareSendResult{err: err}, true
		}
	}
	if ports.authorizer != nil {
		decision, err := ports.authorizer.AuthorizeSend(ctx, cmd)
		if err != nil {
			return cmd, prepareSendResult{err: err}, true
		}
		if !decision.Allowed {
			reason := decision.Reason
			if reason == ReasonSuccess {
				reason = ReasonInvalidRequest
			}
			return cmd, prepareSendResult{result: SendResult{Reason: reason}}, true
		}
	}
	if normalizePerson && cmd.NormalizePersonChannel && cmd.ChannelType == channelTypePerson {
		channelID, err := runtimechannelid.NormalizePersonChannel(cmd.FromUID, cmd.ChannelID)
		if err != nil {
			return cmd, prepareSendResult{err: err}, true
		}
		cmd.ChannelID = channelID
	}
	return cmd, prepareSendResult{}, false
}

func prepareAllocatedSend(cmd SendCommand, ports preparePorts, realtime bool) (prepareSendResult, bool) {
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
		realtime: realtime,
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
		PayloadHash: idempotencyPayloadHash(cmd.Payload),
	})
}

func idempotencyPayloadHash(payload []byte) uint64 {
	hash := uint64(idempotencyFNV64aOffset)
	for _, b := range payload {
		hash ^= uint64(b)
		hash *= idempotencyFNV64aPrime
	}
	return hash
}
