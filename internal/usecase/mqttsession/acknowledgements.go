package mqttsession

import (
	"context"
	"math"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	runtime "github.com/WuKongIM/WuKongIM/internal/runtime/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// AcknowledgementMetadata provides one coherent current Session/inflight read
// and an owner/revision-fenced atomic ACK through authoritative Slot routing.
type AcknowledgementMetadata interface {
	ReadMQTT(context.Context, meta.MQTTRead) (meta.MQTTReadResult, error)
	MutateMQTTWindow(context.Context, meta.MQTTWindowMutation) (meta.MQTTWindowResult, error)
}

type AcknowledgementOptions struct {
	Store  AcknowledgementMetadata
	Owners *runtime.Owners
	// Now shares Owners' monotonic clock; Timeout bounds the complete operation.
	Now     func() time.Time
	Timeout time.Duration
}

// AcknowledgementCommand is captured from an admitted outbound exchange before
// deferring ACK work. PacketID alone cannot identify a later reused exchange.
// The current connection Owner may complete an exchange retained across resume.
type AcknowledgementCommand struct {
	Owner         contract.Owner
	Key           meta.MQTTDeliveryCursorKey
	PacketID      uint16
	DeliveryOrder uint64
}

type AcknowledgementResult struct {
	Changed bool
	// Absent means the authoritative read found no exchange at this PacketID.
	// It neither proves delivery nor claims that this invocation committed an ACK.
	Absent bool
}

// Acknowledgements completes existing exchanges. It owns no send admission,
// packet binding, retries, network writes, subscription changes or content GC.
type Acknowledgements struct {
	options AcknowledgementOptions
	guard   *Subscriptions
}

func NewAcknowledgements(o AcknowledgementOptions) (*Acknowledgements, error) {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Timeout == 0 {
		o.Timeout = 5 * time.Second
	}
	if o.Store == nil || o.Owners == nil || o.Timeout <= 0 || o.Timeout > time.Minute {
		return nil, ErrInvalid
	}
	a := &Acknowledgements{options: o, guard: &Subscriptions{options: SubscriptionOptions{Owners: o.Owners, Now: o.Now, Timeout: o.Timeout}}}
	if _, err := a.guard.now(); err != nil {
		return nil, err
	}
	return a, nil
}

// Acknowledge performs one point read and at most one mutation. Current receive
// permission/subscription state is irrelevant to completing an existing exchange;
// active Session ownership and the exact durable order remain mandatory.
func (a *Acknowledgements) Acknowledge(parent context.Context, q AcknowledgementCommand) (out AcknowledgementResult, err error) {
	o := q.Owner
	if a == nil || q.PacketID == 0 || q.DeliveryOrder == 0 || meta.ValidateMQTTRead(meta.MQTTRead{Kind: meta.MQTTReadDeliveryCursor, CursorKey: q.Key}) != nil ||
		q.Key.Namespace != o.Key.Namespace || q.Key.ClientID != o.Key.ClientID || q.Key.SessionGeneration != o.SessionGeneration {
		return out, ErrInvalid
	}
	op, ctx, cancel, err := a.guard.begin(parent, o)
	if err != nil {
		return out, err
	}
	defer finishSubscription(op, cancel, &err)
	if err = checkSubscriptionScope(ctx, op); err != nil {
		return out, err
	}
	r, err := a.options.Store.ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadInflight, Namespace: o.Key.Namespace, ClientID: o.Key.ClientID, SessionGeneration: o.SessionGeneration, PacketID: q.PacketID})
	if err != nil {
		return out, err
	}
	if err = a.guard.checkSession(ctx, op, o, r.Session); err != nil {
		return out, err
	}
	if !r.Done || r.After != (meta.MQTTReadCursor{}) || len(r.Inflight) > 1 || len(r.SourceOwners) != 0 || len(r.Sessions) != 0 || len(r.Subscriptions) != 0 || len(r.DeliveryCursors) != 0 || len(r.Bindings) != 0 || len(r.Wills) != 0 {
		return out, ErrEvidence
	}
	if len(r.Inflight) == 0 {
		return AcknowledgementResult{Absent: true}, nil
	}
	entry := r.Inflight[0]
	if meta.ValidateMQTTInflight(entry) != nil || entry.PacketID != q.PacketID {
		return out, ErrEvidence
	}
	if entry.Key != q.Key || entry.DeliveryOrder != q.DeliveryOrder {
		return out, ErrConflict
	}
	now, err := a.guard.now()
	if err != nil {
		return out, err
	}
	if now.UnixMilli() < entry.UpdatedAtMS || now.UnixMilli() < r.Session.UpdatedAtMS || r.Session.Revision == math.MaxUint64 {
		return out, ErrClock
	}
	mutation := meta.MQTTWindowMutation{Key: q.Key, ExpectedRevision: r.Session.Revision, OwnerGeneration: o.OwnerGeneration, OwnerNodeID: o.NodeID, OwnerBootID: o.BootID, ConnectionID: o.ConnectionID, Op: meta.MQTTWindowAck, PacketID: q.PacketID, DeliveryOrder: q.DeliveryOrder, UpdatedAtMS: now.UnixMilli()}
	if err = checkSubscriptionScope(ctx, op); err != nil {
		return out, err
	}
	receipt, err := a.options.Store.MutateMQTTWindow(ctx, mutation)
	if err != nil {
		return out, err
	}
	if err = checkSubscriptionScope(ctx, op); err != nil {
		return out, err
	}
	if receipt.Status == meta.MQTTWindowConflict {
		return out, ErrConflict
	}
	if (receipt.Status != meta.MQTTWindowApplied && receipt.Status != meta.MQTTWindowUnchanged) || receipt.CurrentRevision != r.Session.Revision+1 || receipt.PacketID != q.PacketID || receipt.DeliveryOrder != q.DeliveryOrder {
		return out, ErrEvidence
	}
	return AcknowledgementResult{Changed: receipt.Status == meta.MQTTWindowApplied}, nil
}
