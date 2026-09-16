package cluster

import "time"

// ConversationReadStageObserver is an optional Channel observer facet for
// origin-side conversation-head stages, shared by list and sync callers.
type ConversationReadStageObserver interface {
	// ConversationReadStageObservationEnabled reports whether any consumer needs timing.
	ConversationReadStageObservationEnabled() bool
	ObserveConversationReadStage(scope, stage, result string, duration time.Duration)
}

type conversationReadTimer struct {
	observer ConversationReadStageObserver
	scope    string
}

func (n *Node) conversationReadTimer(persisted bool) conversationReadTimer {
	observer, _ := n.cfg.Channel.Observer.(ConversationReadStageObserver)
	scope := "committed_heads"
	if persisted {
		scope = "persisted_heads"
	}
	if observer != nil && !observer.ConversationReadStageObservationEnabled() {
		observer = nil
	}
	return conversationReadTimer{observer: observer, scope: scope}
}
func (t conversationReadTimer) start() time.Time {
	if t.observer == nil {
		return time.Time{}
	}
	return time.Now()
}
func (t conversationReadTimer) finish(stage string, start time.Time, failed bool) {
	if t.observer == nil {
		return
	}
	result := "ok"
	if failed {
		result = "error"
	}
	t.observer.ObserveConversationReadStage(t.scope, stage, result, time.Since(start))
}
