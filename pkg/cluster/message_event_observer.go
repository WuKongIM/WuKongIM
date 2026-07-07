package cluster

import (
	"errors"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const (
	messageEventPathCache       = "cache"
	messageEventPathDurable     = "durable"
	messageEventPathFinishBatch = "finish_batch"
	messageEventPathForward     = "forward"

	messageEventResultOK            = "ok"
	messageEventResultError         = "error"
	messageEventResultBackpressured = "backpressured"
	messageEventResultNotLeader     = "not_leader"
	messageEventResultNotReady      = "not_ready"
	messageEventResultNoSlotLeader  = "no_slot_leader"
	messageEventResultCacheMiss     = "cache_miss"
	messageEventResultInvalid       = "invalid"

	messageEventAppendStageFinishCacheOpen   = "finish_cache_open"
	messageEventAppendStageFinishBatchBuild  = "finish_batch_build"
	messageEventAppendStageFinishCacheRemove = "finish_cache_remove"

	messageEventProposeStageEncode          = "encode"
	messageEventProposeStageSlotProposeWait = "slot_propose_wait"
	messageEventProposeStageSlotSubmit      = "slot_propose_submit"
	messageEventProposeStageSlotFutureWait  = "slot_future_wait"
	messageEventProposeStageSlotControlWait = "slot_control_wait"
	messageEventProposeStageSlotRaftCommit  = "slot_raft_commit_wait"
	messageEventProposeStageSlotFSMApply    = "slot_fsm_apply"
	messageEventProposeStageSlotFSMCommit   = "slot_fsm_commit"
	messageEventProposeStageSlotMarkApplied = "slot_mark_applied"
	messageEventProposeStageDecode          = "decode"
)

func (n *Node) observeMessageEventAppend(path string, event metadb.MessageEventAppend, result string, dur time.Duration) {
	if n == nil || n.cfg.MessageEvent.Observer == nil {
		return
	}
	n.cfg.MessageEvent.Observer.ObserveMessageEventAppend(MessageEventAppendObservation{
		Path:      path,
		EventType: event.EventType,
		Result:    result,
		Duration:  dur,
	})
}

func (n *Node) observeMessageEventPropose(path, result string, batchSize int, dur time.Duration) {
	if n == nil || n.cfg.MessageEvent.Observer == nil {
		return
	}
	n.cfg.MessageEvent.Observer.ObserveMessageEventPropose(MessageEventProposeObservation{
		Path:      path,
		Result:    result,
		BatchSize: batchSize,
		Duration:  dur,
	})
}

func (n *Node) observeMessageEventAppendStage(path, result, stage string, dur time.Duration) {
	if n == nil || n.cfg.MessageEvent.Observer == nil {
		return
	}
	observer, ok := n.cfg.MessageEvent.Observer.(MessageEventAppendStageObserver)
	if !ok {
		return
	}
	observer.ObserveMessageEventAppendStage(MessageEventAppendStageObservation{
		Path:     path,
		Result:   result,
		Stage:    stage,
		Duration: dur,
	})
}

func (n *Node) observeMessageEventProposeStage(path, result, stage string, dur time.Duration) {
	if n == nil || n.cfg.MessageEvent.Observer == nil {
		return
	}
	observer, ok := n.cfg.MessageEvent.Observer.(MessageEventProposeStageObserver)
	if !ok {
		return
	}
	observer.ObserveMessageEventProposeStage(MessageEventProposeStageObservation{
		Path:     path,
		Result:   result,
		Stage:    stage,
		Duration: dur,
	})
}

type messageEventProposeStageAdapter struct {
	node *Node
	path string
	next interface {
		ObserveChannelAppendStage(stage string, result string, d time.Duration)
	}
}

func (o messageEventProposeStageAdapter) ObserveChannelAppendStage(stage string, result string, d time.Duration) {
	if mapped, ok := messageEventSlotProposalStage(stage); ok {
		o.node.observeMessageEventProposeStage(o.path, messageEventProposalStageResult(result), mapped, d)
	}
	if o.next != nil {
		o.next.ObserveChannelAppendStage(stage, result, d)
	}
}

func messageEventSlotProposalStage(stage string) (string, bool) {
	switch stage {
	case defaultSlotStageMetaCreateSubmit:
		return messageEventProposeStageSlotSubmit, true
	case defaultSlotStageMetaCreateWait:
		return messageEventProposeStageSlotFutureWait, true
	case "meta_create_slot_control_wait":
		return messageEventProposeStageSlotControlWait, true
	case "meta_create_slot_raft_commit_wait":
		return messageEventProposeStageSlotRaftCommit, true
	case "meta_create_slot_fsm_apply":
		return messageEventProposeStageSlotFSMApply, true
	case "meta_create_slot_fsm_commit":
		return messageEventProposeStageSlotFSMCommit, true
	case "meta_create_slot_mark_applied":
		return messageEventProposeStageSlotMarkApplied, true
	default:
		return "", false
	}
}

func messageEventProposalStageResult(result string) string {
	switch result {
	case messageEventResultOK:
		return messageEventResultOK
	case "err":
		return messageEventResultError
	default:
		return result
	}
}

func (n *Node) setMessageEventStreamCache(observation MessageEventStreamCacheObservation) {
	if n == nil || n.cfg.MessageEvent.Observer == nil {
		return
	}
	n.cfg.MessageEvent.Observer.SetMessageEventStreamCache(observation)
}

func messageEventResultForError(err error) string {
	switch {
	case err == nil:
		return messageEventResultOK
	case errors.Is(err, ErrBackpressured):
		return messageEventResultBackpressured
	case errors.Is(err, ErrNotLeader):
		return messageEventResultNotLeader
	case errors.Is(err, ErrNotStarted), errors.Is(err, ErrStopping):
		return messageEventResultNotReady
	case errors.Is(err, ErrNoSlotLeader):
		return messageEventResultNoSlotLeader
	case errors.Is(err, ErrMessageEventStreamCacheMiss):
		return messageEventResultCacheMiss
	case errors.Is(err, metadb.ErrInvalidArgument), errors.Is(err, metadb.ErrStaleMeta), errors.Is(err, metadb.ErrCorruptValue):
		return messageEventResultInvalid
	default:
		return messageEventResultError
	}
}
