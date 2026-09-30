// Package sendbanaudit carries verified entry attribution and atomic policy
// mutation facts to a composition-owned audit sink. It never carries secrets.
package sendbanaudit

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// Actor identifies a verified principal when available. Backend APIs without a
// human principal use unknown; Peer is the actual socket peer, never a header.
type Actor struct{ Source, Name, Peer string }

// Event contains one attempted mutation and only apply-confirmed old/new state.
// Nil policy states mean the caller has no proof of the durable outcome.
type Event struct {
	Actor                  Actor
	UID, ChannelID         string
	ChannelType, Requested int64
	Result                 string
	Previous, Current      *metadb.SendBanPolicy
}

// Observer records bounded events synchronously without retaining request data.
type Observer func(Event)
type actorKey struct{}

// WithActor attaches entry-verified attribution without changing authorization.
func WithActor(ctx context.Context, actor Actor) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, actorKey{}, actor)
}

// RecordMutation never reconstructs old state with a racing pre-write read, or
// presents a proposal timeout as a committed change.
func RecordMutation(ctx context.Context, observer Observer, q metadb.SendBanMutation, result metadb.SendBanResult, err error) {
	if observer == nil {
		return
	}
	actor := Actor{Source: "internal", Name: "unknown"}
	if ctx != nil {
		if value, ok := ctx.Value(actorKey{}).(Actor); ok {
			actor = value
		}
	}
	if actor.Name == "" {
		actor.Name = "unknown"
	}
	actor.Name, actor.Source, actor.Peer = bounded(actor.Name, 128), bounded(actor.Source, 32), bounded(actor.Peer, 64)
	event := Event{Actor: actor, UID: bounded(q.UID, 512), ChannelID: bounded(q.ChannelID, 1024), ChannelType: q.ChannelType, Requested: q.SendBan, Result: result.Status}
	switch {
	case errors.Is(err, metadb.ErrInvalidArgument):
		event.Result = "invalid_request"
	case err != nil:
		event.Result = "outcome_unknown"
	default:
		switch result.Status {
		case "ok", "not_found", "version_conflict", "channel_disbanded", "version_exhausted":
			if result.Previous != nil {
				previous := *result.Previous
				event.Previous = &previous
				event.Current = &metadb.SendBanPolicy{SendBan: result.SendBan, Version: result.Version}
			}
		default:
			event.Result = "outcome_unknown"
		}
	}
	observer(event)
}

func bounded(value string, limit int) string {
	value = strings.ToValidUTF8(value, "?")
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}
