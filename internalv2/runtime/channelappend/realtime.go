package channelappend

import (
	"context"
	"time"
)

type realtimeEffect struct {
	target AuthorityTarget
	items  []preparedSend
}

type realtimeItemCompletion struct {
	item   preparedSend
	result SendBatchItemResult
}

func (e realtimeEffect) run(runtimeCtx context.Context, ports commitPorts) []realtimeItemCompletion {
	startedAt := time.Now()
	effectResult := channelAppendResultOK
	defer func() {
		observeEffect(ports.observer, EffectObservation{Stage: effectStageRealtime, Result: effectResult, Items: len(e.items), Duration: elapsedSince(startedAt)})
	}()
	completions := make([]realtimeItemCompletion, 0, len(e.items))
	for _, item := range e.items {
		completion := e.runItem(runtimeCtx, item, ports)
		itemResult := resultClass(completion.result)
		if effectResult == channelAppendResultOK {
			effectResult = itemResult
		} else if effectResult != itemResult {
			effectResult = channelAppendResultMixed
		}
		completions = append(completions, completion)
	}
	return completions
}

func (e realtimeEffect) runItem(runtimeCtx context.Context, item preparedSend, ports commitPorts) realtimeItemCompletion {
	if err := appendItemError(item); err != nil {
		return realtimeItemCompletion{item: item, result: SendBatchItemResult{Err: err}}
	}
	if effectiveRecipientDeliveryEnqueuer(ports) == nil {
		return realtimeItemCompletion{item: item, result: SendBatchItemResult{Err: ErrRealtimeDeliveryRequired}}
	}
	ctx, cancel := prepareItemContext(runtimeCtx, item.Context)
	defer cancel()
	realtimePorts := ports
	realtimePorts.activeAdmitter = nil
	event := committedEnvelopeForRealtime(item)
	if _, err := dispatchCommittedRecipientsForTarget(ctx, e.target, event, subscriberCache{}, realtimePorts); err != nil {
		return realtimeItemCompletion{item: item, result: SendBatchItemResult{Err: err}}
	}
	return realtimeItemCompletion{item: item, result: SendBatchItemResult{Result: SendResult{
		MessageID:  event.MessageID,
		MessageSeq: 0,
		Reason:     ReasonSuccess,
	}}}
}

func committedEnvelopeForRealtime(item preparedSend) CommittedEnvelope {
	cmd := item.Command
	return CommittedEnvelope{
		MessageID:         cmd.MessageID,
		MessageSeq:        0,
		ChannelID:         cmd.ChannelID,
		ChannelType:       cmd.ChannelType,
		FromUID:           cmd.FromUID,
		SenderNodeID:      cmd.SenderNodeID,
		SenderSessionID:   cmd.SenderSessionID,
		ClientMsgNo:       cmd.ClientMsgNo,
		ServerTimestampMS: item.ServerTimestampMS,
		Payload:           cloneBytes(cmd.Payload),
		RedDot:            cmd.RedDot,
		SyncOnce:          cmd.SyncOnce,
		MessageScopedUIDs: append([]string(nil), cmd.MessageScopedUIDs...),
	}
}
