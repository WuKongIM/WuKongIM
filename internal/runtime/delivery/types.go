package delivery

import (
	"context"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
)

type ChannelKey struct {
	ChannelID   string
	ChannelType uint8
}

type CommittedEnvelope struct {
	channel.Message
	SenderSessionID uint64
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

type RetryEntry struct {
	When        time.Time
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
	MessageID       uint64
	MessageSeq      uint64
	Envelope        CommittedEnvelope
	ResolveToken    any
	ResolveBegun    bool
	ResolveDone     bool
	ResolveAttempt  int
	ResolveRetryAt  time.Time
	NextCursor      string
	Routes          map[RouteKey]*RouteDeliveryState
	PendingRouteCnt int
}

type Limits struct {
	MaxInflightRoutesPerActor      int
	DedicatedLaneActivityThreshold int
}

type Resolver interface {
	BeginResolve(ctx context.Context, key ChannelKey, env CommittedEnvelope) (any, error)
	ResolvePage(ctx context.Context, token any, cursor string, limit int) ([]RouteKey, string, bool, error)
}

type Pusher interface {
	Push(ctx context.Context, cmd PushCommand) (PushResult, error)
}

type Clock interface {
	Now() time.Time
}

type Config struct {
	Resolver         Resolver
	Push             Pusher
	Clock            Clock
	ShardCount       int
	ResolvePageSize  int
	Limits           Limits
	IdleTimeout      time.Duration
	RetryDelays      []time.Duration
	MaxRetryAttempts int
}

type defaultClock struct{}

func (defaultClock) Now() time.Time {
	return time.Now()
}

type noopResolver struct{}

func (noopResolver) BeginResolve(context.Context, ChannelKey, CommittedEnvelope) (any, error) {
	return nil, nil
}

func (noopResolver) ResolvePage(context.Context, any, string, int) ([]RouteKey, string, bool, error) {
	return nil, "", true, nil
}

type noopPusher struct{}

func (noopPusher) Push(context.Context, PushCommand) (PushResult, error) {
	return PushResult{}, nil
}
