package mqttsession

import (
	"context"
	"errors"
	"math"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
)

// InboxSourceMetadata routes each qualification/source/Session to its current
// Slot authority. Preparation never opens a local replica or mutates intent.
type InboxSourceMetadata interface {
	ReadMQTT(context.Context, meta.MQTTRead) (meta.MQTTReadResult, error)
	CompareAndSwapMQTTSourceBinding(context.Context, uint64, meta.MQTTSourceBinding) (meta.MQTTSourceBindingResult, error)
	MutateMQTTDeliveryCursor(context.Context, meta.MQTTDeliveryCursorMutation) (meta.MQTTDeliveryCursorResult, error)
}

type InboxSourceOptions struct {
	Store   InboxSourceMetadata
	Sources SourceProtector
	// Timeout bounds one pair, with no retry loop; default five seconds, at most a minute.
	Timeout time.Duration
	// Now supplies durable mutation timestamps, never a proof that a Session ended.
	Now func() time.Time
}

// PreparedInboxSource describes one protected source, not complete inbox admission
// or permission for SUBACK. Needed=false is authoritative closed intent only;
// retained bindings/cursors still require their independent cleanup protocol.
type PreparedInboxSource struct {
	Needed  bool
	Binding meta.MQTTSourceBinding
	Cursor  meta.MQTTDeliveryCursor
}

// InboxSources prepares one person source for a durable qualification, including
// offline Sessions. It owns no connections, publication rights or background work.
type InboxSources struct{ options InboxSourceOptions }

func NewInboxSources(o InboxSourceOptions) (*InboxSources, error) {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Timeout == 0 {
		o.Timeout = 5 * time.Second
	}
	if o.Store == nil || o.Sources == nil || o.Timeout <= 0 || o.Timeout > time.Minute {
		return nil, ErrInvalid
	}
	p := &InboxSources{options: o}
	if _, err := p.now(); err != nil {
		return nil, err
	}
	return p, nil
}

var errInboxIntentClosed = errors.New("mqttsession: inbox intent no longer admits sources")

// Prepare fixes one source boundary after unknown responsibility is durable, then
// initializes its cursor using the current stored owner tuple and parent CAS.
// This server-side metadata work needs no local connection execution scope. Its
// caller must order directory registration before qualification discovery and all
// required preparations before the first persistent person append.
func (p *InboxSources) Prepare(parent context.Context, qualification meta.MQTTSourceBindingKey, channel SourceChannel) (out PreparedInboxSource, err error) {
	return p.prepare(parent, qualification, channel, nil)
}

// PrepareIntent pins the initiating Owner and exact child through all nested
// source effects. A takeover must start another turn rather than rebase this one.
func (p *InboxSources) PrepareIntent(parent context.Context, r SubscriptionProjectionRequest, qualification meta.MQTTSourceBindingKey, channel SourceChannel) (PreparedInboxSource, error) {
	sub := r.Subscription
	if r.Owner.Validate() != nil || meta.ValidateMQTTSubscription(sub) != nil || sub.Stage != meta.MQTTSubscriptionPreparing || sub.TargetKind != meta.MQTTSubscriptionUserInbox || sub.TargetID != r.UID || sub.AuthorizationVersion != 0 || r.UID != qualification.Owner.ID || sub.Namespace != qualification.Namespace || sub.ClientID != qualification.ClientID || sub.SessionGeneration != qualification.SessionGeneration || sub.Generation != qualification.SubscriptionGeneration || r.Owner.Key.Namespace != sub.Namespace || r.Owner.Key.ClientID != sub.ClientID || r.Owner.SessionGeneration != sub.SessionGeneration {
		return PreparedInboxSource{}, ErrInvalid
	}
	return p.prepare(parent, qualification, channel, &r)
}

func (p *InboxSources) prepare(parent context.Context, qualification meta.MQTTSourceBindingKey, channel SourceChannel, request *SubscriptionProjectionRequest) (out PreparedInboxSource, err error) {
	if p == nil || parent == nil || qualification.Owner.Kind != meta.MQTTBindingUID || meta.ValidateMQTTRead(meta.MQTTRead{Kind: meta.MQTTReadSourceBinding, BindingKey: qualification}) != nil || channel.Type != 1 {
		return out, ErrInvalid
	}
	left, right, e := channelid.DecodePersonChannel(channel.ID)
	if e != nil || !contract.ValidIdentity(left, 1024) || !contract.ValidIdentity(right, 1024) || (left != qualification.Owner.ID && right != qualification.Owner.ID) || channelid.EncodePersonChannel(left, right) != channel.ID {
		return out, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(parent, p.options.Timeout)
	defer cancel()
	defer func() {
		if recover() != nil {
			err = ErrSubscriptionCallback
		}
		if canceled := ctx.Err(); canceled != nil {
			err = canceled
		}
		if err != nil {
			out = PreparedInboxSource{}
			if errors.Is(err, errInboxIntentClosed) {
				err = nil
			}
		}
	}()
	if _, err = p.now(); err != nil {
		return out, err
	}
	t := inboxSourcePreparation{p: p, ctx: ctx, key: qualification, channel: channel, request: request}
	source, err := t.protect()
	if err != nil {
		return out, err
	}
	key := meta.MQTTSourceBindingKey{Owner: meta.MQTTBindingOwner{Kind: meta.MQTTBindingChannel, ID: "1:" + channel.ID, Generation: source.Generation}, Namespace: qualification.Namespace, ClientID: qualification.ClientID, SessionGeneration: qualification.SessionGeneration, SubscriptionGeneration: qualification.SubscriptionGeneration}
	cursorKey := meta.MQTTDeliveryCursorKey{Namespace: key.Namespace, ClientID: key.ClientID, SessionGeneration: key.SessionGeneration, SubscriptionGeneration: key.SubscriptionGeneration, SourceKind: meta.MQTTSourceChannel, SourceID: key.Owner.ID, SourceGeneration: key.Owner.Generation}
	cursor, hasCursor, err := t.cursor(cursorKey)
	if err != nil {
		return out, err
	}
	binding, found, err := t.binding(key)
	if err != nil {
		return out, err
	}
	if !found {
		if hasCursor {
			return out, ErrEvidence
		}
		binding = meta.MQTTSourceBinding{Key: key, UID: qualification.Owner.ID, Topic: t.intent.Topic, OperationID: t.intent.OperationID, Stage: meta.MQTTBindingPreparing}
		binding, err = t.writeBinding(binding)
		if err != nil {
			return out, err
		}
	}
	if !binding.BoundaryKnown {
		if hasCursor || binding.Stage != meta.MQTTBindingPreparing {
			return out, ErrEvidence
		}
		confirmed, e := t.protect()
		if e != nil {
			return out, e
		}
		if confirmed.Generation != source.Generation || confirmed.ProtectedAfter != source.ProtectedAfter || confirmed.CommittedThrough < source.CommittedThrough {
			return out, ErrEvidence
		}
		source = confirmed
		binding.BoundaryKnown = true
		binding.StartAfter, binding.CompletedThrough = source.CommittedThrough, source.CommittedThrough
		if binding.Revision == math.MaxUint64 {
			return out, ErrEvidence
		}
		binding.ProtectionRevision = binding.Revision + 1
		binding, err = t.writeBinding(binding)
		if err != nil {
			return out, err
		}
	}
	if binding.StartAfter < source.ProtectedAfter || binding.StartAfter > source.CommittedThrough || hasCursor && cursor.StartAfter != binding.StartAfter {
		return out, ErrEvidence
	}
	cursor, hasCursor, err = t.cursor(cursorKey)
	if err != nil {
		return out, err
	}
	if !hasCursor {
		if binding.Stage == meta.MQTTBindingActive {
			return out, ErrEvidence
		}
		revision, e := t.initializeCursor(cursorKey, binding.StartAfter)
		if e != nil {
			return out, e
		}
		cursor, hasCursor, err = t.cursor(cursorKey)
		if err != nil {
			return out, err
		}
		if !hasCursor || cursor.Revision < revision {
			return out, ErrEvidence
		}
	}
	if cursor.StartAfter != binding.StartAfter || cursor.CompletedThrough < binding.CompletedThrough || cursor.Revision < binding.ProgressRevision {
		return out, ErrEvidence
	}
	if binding.Stage == meta.MQTTBindingPreparing {
		binding.Stage, binding.ProgressRevision = meta.MQTTBindingActive, cursor.Revision
		binding, err = t.writeBinding(binding)
		if err != nil {
			return out, err
		}
	}
	if _, err = t.current(); err != nil {
		return out, err
	}
	return PreparedInboxSource{Needed: true, Binding: binding, Cursor: cursor}, nil
}

func (p *InboxSources) now() (int64, error) {
	now := p.options.Now()
	if now.IsZero() || now.UnixMilli() <= 0 {
		return 0, ErrClock
	}
	return now.UnixMilli(), nil
}
