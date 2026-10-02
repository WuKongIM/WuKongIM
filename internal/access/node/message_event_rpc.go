package node

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/WuKongIM/WuKongIM/internal/contracts/onlinedelivery"
	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
)

// MessageEventDelivery is the independent version-1 stream EVENT RPC envelope.
type MessageEventDelivery struct {
	Format int
	Routes []onlinedelivery.Route
	Event  message.MessageEventNotification
}

// MessageEventWriter performs only exact owner-local writes, without rerouting.
type MessageEventWriter interface {
	WriteMessageEvents(context.Context, []onlinedelivery.Route, message.MessageEventNotification) error
}

var ErrMessageEventFrameBudget = errors.New("stream event frame budget")

func EncodeMessageEventDelivery(q MessageEventDelivery) ([]byte, error) {
	q.Format = 1
	if len(q.Routes) > 512 {
		return nil, ErrMessageEventFrameBudget
	}
	size := len(q.Event.Payload) + len(q.Event.ChannelID) + len(q.Event.ClientMsgNo) + len(q.Event.EventID)
	for _, r := range q.Routes {
		size += len(r.UID) + len(r.DeviceID)
		if size > 256<<10 {
			return nil, ErrMessageEventFrameBudget
		}
	}
	body, err := json.Marshal(q)
	if len(body) > 256<<10 {
		return nil, ErrMessageEventFrameBudget
	}
	return body, err
}

// MessageEventRPC bounds versioned frames before delegating session fencing.
type MessageEventRPC struct{ Writer MessageEventWriter }

func (a MessageEventRPC) HandleRPC(ctx context.Context, body []byte) ([]byte, error) {
	if len(body) > 256<<10 {
		return nil, ErrMessageEventFrameBudget
	}
	var q MessageEventDelivery
	if err := json.Unmarshal(body, &q); err != nil {
		return nil, err
	}
	if q.Format != 1 || len(q.Routes) > 512 || q.Event.ChannelID == "" || q.Event.ChannelType == 0 || q.Event.MessageID == 0 || q.Event.EventID == "" || q.Event.ClientMsgNo == "" || a.Writer == nil {
		return nil, errors.New("invalid stream event frame")
	}
	if err := a.Writer.WriteMessageEvents(ctx, q.Routes, q.Event); err != nil {
		return nil, err
	}
	return []byte{1}, nil
}
