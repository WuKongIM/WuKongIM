package mqttsession

import (
	"context"
	"errors"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	runtime "github.com/WuKongIM/WuKongIM/internal/runtime/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

var (
	ErrSubscriptionDenied   = errors.New("mqttsession: subscription not authorized")
	ErrSubscriptionRevoked  = errors.New("mqttsession: subscription authorization incarnation changed")
	ErrSubscriptionLimit    = errors.New("mqttsession: subscription capacity or scan budget exhausted")
	ErrSubscriptionCallback = errors.New("mqttsession: subscription dependency failed")
	// ErrSubscriptionUnconfirmed accompanies errors after a possible intent write
	// or observation of pending intent. Entry must not send a definitive negative
	// reply that would hide later recovery of that same durable subscription.
	ErrSubscriptionUnconfirmed = errors.New("mqttsession: subscription intent remains unconfirmed")
)

// SubscriptionRequest contains an entry-mapped exact topic and receive options.
// Entries validate canonical topic/target correspondence; no packet is retained.
type SubscriptionRequest struct {
	Topic, TargetID            string
	TargetKind                 meta.MQTTSubscriptionTargetKind
	RequestedQoS               uint8
	NoLocal, RetainAsPublished bool
	RetainHandling             uint8
	SubscriptionIdentifier     uint32
}

// SubscriptionMetadata provides coherent current-owner reads and committed
// Session/child CAS results, implemented by the foreground-gated cluster Node.
type SubscriptionMetadata interface {
	ReadMQTT(context.Context, meta.MQTTRead) (meta.MQTTReadResult, error)
	MutateMQTTSubscription(context.Context, meta.MQTTSubscriptionMutation) (meta.MQTTSessionCASResult, error)
}

// SubscriptionAuthorizer checks current receive permission, never SEND privilege
// or membership mutation. Version identifies the permission incarnation, not a
// cached grant. Zero is reserved for authorities with no versioned membership.
type SubscriptionAuthorizer interface {
	AuthorizeSubscription(context.Context, string, SubscriptionRequest) (uint64, error)
}

// SubscriptionProjectionRequest binds monotonic cross-Slot work to one persisted
// intent and one captured owner. UID comes from admitted Owners state for live
// projection or a freshly validated Session for closed-intent maintenance.
type SubscriptionProjectionRequest struct {
	Owner        contract.Owner
	UID          string
	Subscription meta.MQTTSubscription
}

// SubscriptionProjectionReceipt identifies exactly the intent proven by the
// trusted projection port. Matching fields alone are not source durability proof.
type SubscriptionProjectionReceipt struct {
	Namespace, ClientID, Topic                                string
	SessionGeneration, SubscriptionGeneration, IntentRevision uint64
	OperationID                                               string
}

// SubscriptionProjection must establish replicated source protection, initialized
// cursors and recoverable bindings before success; UID inboxes also require future
// source admission. Remove stops new matching but preserves outstanding exchanges
// and their content. Every effect is generation/operation fenced and monotonic;
// errors/late work may retain obligations, never release a successor's content.
// A replica-local write or best-effort callback cannot implement this contract.
type SubscriptionProjection interface {
	Establish(context.Context, SubscriptionProjectionRequest) (SubscriptionProjectionReceipt, error)
	Remove(context.Context, SubscriptionProjectionRequest) (SubscriptionProjectionReceipt, error)
}

type SubscriptionOptions struct {
	Store         SubscriptionMetadata
	Owners        *runtime.Owners
	Authorization SubscriptionAuthorizer
	Projection    SubscriptionProjection
	// MaxSubscriptions includes preparing, active and removing rows; default 128,
	// maximum 1024. Parent revision CAS makes concurrent admissions fail closed.
	MaxSubscriptions int
	// MaxScanPages defaults to 16, at most 64; each page contains at most 64 rows.
	// Tombstones consume scan budget until their independent safe cleanup finishes.
	MaxScanPages int
	// Timeout defaults to five seconds; callbacks must obey its cancellation.
	Timeout time.Duration
	// Now shares the current owner's monotonic clock and cannot regress.
	Now func() time.Time
}

// Subscriptions owns live-owner intent orchestration, not wire replies, workers,
// replicated projection, offline cleanup, revocation ordering or delivery state.
type Subscriptions struct{ options SubscriptionOptions }

func NewSubscriptions(o SubscriptionOptions) (*Subscriptions, error) {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Timeout == 0 {
		o.Timeout = 5 * time.Second
	}
	if o.MaxSubscriptions == 0 {
		o.MaxSubscriptions = 128
	}
	if o.MaxScanPages == 0 {
		o.MaxScanPages = 16
	}
	if o.Store == nil || o.Owners == nil || o.Authorization == nil || o.Projection == nil || o.MaxSubscriptions < 1 || o.MaxSubscriptions > 1024 || o.MaxScanPages < 1 || o.MaxScanPages > 64 || o.Timeout <= 0 || o.Timeout > time.Minute {
		return nil, ErrInvalid
	}
	s := &Subscriptions{options: o}
	if _, err := s.now(); err != nil {
		return nil, err
	}
	return s, nil
}
