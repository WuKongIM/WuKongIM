package node

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/WuKongIM/WuKongIM/internal/contracts/onlinedelivery"
	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
)

// MessageUpdateDelivery is a bounded versioned owner-node notification request.
type MessageUpdateDelivery struct {
	Format int
	Routes []onlinedelivery.Route
	Hint   message.MessageUpdateHint
}

// MessageUpdateHintWriter checks exact owner-local sessions before writing.
type MessageUpdateHintWriter interface {
	WriteMessageUpdateHints(context.Context, []onlinedelivery.Route, message.MessageUpdateHint) error
}

// ErrMessageUpdateFrameBudget lets senders split oversized route pages.
var ErrMessageUpdateFrameBudget = errors.New("message update frame budget")

func EncodeMessageUpdateDelivery(q MessageUpdateDelivery) ([]byte, error) {
	q.Format = 1
	if len(q.Routes) > 512 {
		return nil, errors.New("message update route budget")
	}
	// Reject a definitely oversized page before JSON allocation. With at most
	// 256 KiB of identity bytes remaining, JSON escaping has a bounded expansion.
	identityBytes := len(q.Hint.ChannelID)
	for _, route := range q.Routes {
		identityBytes += len(route.UID)
		if identityBytes > 256<<10 {
			return nil, ErrMessageUpdateFrameBudget
		}
	}
	body, err := json.Marshal(q)
	if err == nil && len(body) > 256<<10 {
		return nil, ErrMessageUpdateFrameBudget
	}
	return body, err
}

// MessageUpdateRPC maps a bounded peer request to local delivery without routing.
type MessageUpdateRPC struct{ Writer MessageUpdateHintWriter }

func (a MessageUpdateRPC) HandleRPC(ctx context.Context, body []byte) ([]byte, error) {
	if len(body) > 256<<10 {
		return nil, errors.New("message update frame budget")
	}
	var q MessageUpdateDelivery
	if err := json.Unmarshal(body, &q); err != nil {
		return nil, err
	}
	if q.Format != 1 || len(q.Routes) > 512 || q.Hint.ChannelID == "" || q.Hint.ChannelType == 0 || q.Hint.MessageID == 0 || q.Hint.Version == 0 {
		return nil, errors.New("invalid message update frame")
	}
	if a.Writer == nil {
		return nil, errors.New("message update writer unavailable")
	}
	if err := a.Writer.WriteMessageUpdateHints(ctx, q.Routes, q.Hint); err != nil {
		return nil, err
	}
	return []byte{1}, nil
}
