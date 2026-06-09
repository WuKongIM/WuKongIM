package channelwrite

import (
	"context"
	"fmt"
)

type cursorPorts struct {
	store          CursorStore
	reader         CommittedReader
	replayPageSize int
}

type cursorEffect struct {
	key       string
	channelID ChannelID
	fromSeq   uint64
	limit     int
	load      bool
}

type cursorCompletedEvent struct {
	key          string
	loadedCursor uint64
	messages     []CommittedMessage
	nextSeq      uint64
	done         bool
	err          error
}

func (p cursorPorts) enabled() bool {
	return p.store != nil
}

func (e cursorEffect) run(runtimeCtx context.Context, ports cursorPorts) cursorCompletedEvent {
	event := cursorCompletedEvent{key: e.key, done: true}
	if ports.store == nil {
		return event
	}

	fromSeq := e.fromSeq
	if e.load {
		cursor, err := ports.store.LoadPostCommitCursor(runtimeCtx, e.channelID)
		if err != nil {
			event.err = fmt.Errorf("%w: load post-commit cursor: %w", ErrCommitEffectFailed, err)
			return event
		}
		event.loadedCursor = cursor
		fromSeq = nextPostCommitSeq(cursor)
	}
	event.nextSeq = fromSeq

	if ports.reader == nil {
		return event
	}

	limit := boundedPositive(e.limit, defaultReplayPageSize)
	messages, err := ports.reader.ReadCommittedFrom(runtimeCtx, e.channelID, fromSeq, limit)
	if err != nil {
		event.err = fmt.Errorf("%w: read post-commit replay: %w", ErrCommitEffectFailed, err)
		return event
	}
	event.messages = cloneCommittedMessages(messages)
	event.nextSeq = nextReplaySeq(fromSeq, event.messages)
	event.done = len(event.messages) < limit
	return event
}

func (e cursorCompletedEvent) apply(r *reactor) {
	r.recordCursorCompletion(e)
}

func nextReplaySeq(fromSeq uint64, messages []CommittedMessage) uint64 {
	next := fromSeq
	for _, msg := range messages {
		if msg.MessageSeq >= next {
			next = nextPostCommitSeq(msg.MessageSeq)
		}
	}
	return next
}

func nextPostCommitSeq(seq uint64) uint64 {
	if seq == ^uint64(0) {
		return seq
	}
	return seq + 1
}

func cloneCommittedMessages(messages []CommittedMessage) []CommittedMessage {
	if len(messages) == 0 {
		return nil
	}
	out := make([]CommittedMessage, len(messages))
	for i := range messages {
		out[i] = messages[i].Clone()
	}
	return out
}
