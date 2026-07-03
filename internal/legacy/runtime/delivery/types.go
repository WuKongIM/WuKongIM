package delivery

import (
	"context"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
)

type ChannelKey struct {
	ChannelID   string
	ChannelType uint8
}

type CommittedEnvelope struct {
	channel.Message
	SenderSessionID uint64
	// MessageScopedUIDs contains request-scoped subscribers used only for this delivery envelope.
	MessageScopedUIDs []string
	// CMDConversationIntentSubmitted reports that request-scoped CMD sync intent was fully accepted.
	CMDConversationIntentSubmitted bool
}

type RouteKey struct {
	UID       string
	NodeID    uint64
	BootID    uint64
	SessionID uint64
}

type RouteAck struct {
	UID        string
	SessionID  uint64
	MessageID  uint64
	MessageSeq uint64
}

type SessionClosed struct {
	UID       string
	SessionID uint64
}

type AckBinding struct {
	SessionID   uint64
	MessageID   uint64
	ChannelID   string
	ChannelType uint8
	OwnerNodeID uint64
	Route       RouteKey
}

type PushCommand struct {
	Envelope CommittedEnvelope
	Routes   []RouteKey
	Attempt  int
}

type PushResult struct {
	Accepted  []RouteKey
	Retryable []RouteKey
	Dropped   []RouteKey
}

// ResolvePageResult contains one resolver page after subscriber expansion and presence classification.
type ResolvePageResult struct {
	// Routes contains currently online routes that should receive the message.
	Routes []RouteKey
	// OfflineUIDs contains subscribers that had no authoritative online route in this page.
	OfflineUIDs []string
	// NextCursor is the opaque cursor for the next resolver page.
	NextCursor string
	// Done reports whether the resolver has exhausted all subscribers for the message.
	Done bool
}

// RouteExpiredEvent describes a realtime route removed after exhausting retry budget.
type RouteExpiredEvent struct {
	ChannelID   string
	ChannelType uint8
	MessageID   uint64
	MessageSeq  uint64
	Route       RouteKey
	Attempt     int
}

// MaintenanceSnapshot captures delivery runtime gauge values after maintenance passes.
type MaintenanceSnapshot struct {
	InflightRoutes int
	AckBindings    int
}

// Observer receives delivery runtime lifecycle and route expiry notifications.
type Observer interface {
	OnRouteExpired(RouteExpiredEvent)
	OnMaintenanceSnapshot(MaintenanceSnapshot)
}

// OfflineResolvedEvent describes one offline recipient identified after resolver presence classification.
type OfflineResolvedEvent struct {
	// Envelope is the committed message envelope that produced the offline recipient.
	Envelope CommittedEnvelope
	// UID is the offline recipient UID.
	UID string
	// Attempt is the delivery resolve attempt that observed the offline recipient.
	Attempt int
	// PageCursor is the cursor used to request the resolver page.
	PageCursor string
	// NextCursor is the cursor returned by the resolver page.
	NextCursor string
	// PageLimit is the route budget requested for the resolver page.
	PageLimit int
	// Done reports whether this page completed subscriber resolution.
	Done bool
}

// OfflineResolvedObserver receives offline recipient notifications after presence classification.
type OfflineResolvedObserver interface {
	OfflineResolved(context.Context, OfflineResolvedEvent)
}

// RetryEntryKind identifies the actor retry work carried by a retry-wheel entry.
type RetryEntryKind uint8

const (
	// RetryEntryRoute retries a realtime route push.
	RetryEntryRoute RetryEntryKind = iota
	// RetryEntryResolve retries subscriber/page resolution for an inflight message.
	RetryEntryResolve
)

type RetryEntry struct {
	When        time.Time
	Kind        RetryEntryKind
	ChannelID   string
	ChannelType uint8
	MessageID   uint64
	Route       RouteKey
	Attempt     int
}

type StartDispatch struct {
	Envelope CommittedEnvelope
}

type RouteAcked struct {
	MessageID uint64
	Route     RouteKey
}

type RouteOffline struct {
	MessageID uint64
	Route     RouteKey
}

type RetryTick struct {
	Entry RetryEntry
}

type RouteDeliveryState struct {
	Attempt  int
	Accepted bool
}

type ActorLane uint8

const (
	LaneShared ActorLane = iota
	LaneDedicated
)

type InflightMessage struct {
	MessageID    uint64
	MessageSeq   uint64
	Envelope     CommittedEnvelope
	ResolveToken any
	ResolveBegun bool
	ResolveDone  bool
	// ResolveInProgress prevents duplicate resolver calls while I/O runs outside the actor lock.
	ResolveInProgress bool
	ResolveAttempt    int
	ResolveRetryAt    time.Time
	NextCursor        string
	Routes            map[RouteKey]RouteDeliveryState
	PendingRouteCnt   int
}

type Limits struct {
	MaxInflightRoutesPerActor      int
	DedicatedLaneActivityThreshold int
}

type Resolver interface {
	BeginResolve(ctx context.Context, key ChannelKey, env CommittedEnvelope) (any, error)
	ResolvePage(ctx context.Context, token any, cursor string, limit int) (ResolvePageResult, error)
}

type Pusher interface {
	Push(ctx context.Context, cmd PushCommand) (PushResult, error)
}

type Clock interface {
	Now() time.Time
}

type Config struct {
	Resolver Resolver
	Push     Pusher
	Clock    Clock
	// Observer receives runtime maintenance snapshots and route expiry events.
	Observer Observer
	// OfflineResolvedObserver receives offline UIDs after resolver presence classification.
	OfflineResolvedObserver OfflineResolvedObserver
	ShardCount              int
	ResolvePageSize         int
	Limits                  Limits
	IdleTimeout             time.Duration
	RetryDelays             []time.Duration
	MaxRetryAttempts        int
}

type defaultClock struct{}

func (defaultClock) Now() time.Time {
	return time.Now()
}

type noopResolver struct{}

func (noopResolver) BeginResolve(context.Context, ChannelKey, CommittedEnvelope) (any, error) {
	return nil, nil
}

func (noopResolver) ResolvePage(context.Context, any, string, int) (ResolvePageResult, error) {
	return ResolvePageResult{Done: true}, nil
}

type noopPusher struct{}

func (noopPusher) Push(context.Context, PushCommand) (PushResult, error) {
	return PushResult{}, nil
}
