package message

import (
	"context"
	"errors"

	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

const (
	// DefaultPluginSendMaxHookDepth limits plugin-origin Send hook recursion.
	DefaultPluginSendMaxHookDepth = 1
)

var (
	// ErrSendHookDepthExceeded reports that a plugin-origin send exceeded hook recursion limits.
	ErrSendHookDepthExceeded = errors.New("usecase/message: send hook depth exceeded")
)

// SendOrigin identifies where a SendCommand entered the message usecase.
type SendOrigin string

const (
	// SendOriginClient marks sends that originated from a client or trusted host caller.
	SendOriginClient SendOrigin = "client"
	// SendOriginPlugin marks sends that originated from a plugin host RPC.
	SendOriginPlugin SendOrigin = "plugin"
)

type SendCommand struct {
	// TraceID is the diagnostics trace identifier propagated through the send path.
	TraceID         string
	Framer          frame.Framer
	Setting         frame.Setting
	MsgKey          string
	Expire          uint32
	FromUID         string
	SenderSessionID uint64
	// DeviceID is the trusted gateway session device ID used for legacy system-device permission bypass.
	DeviceID string
	// DeviceFlag is the trusted gateway session device flag. It is pass-through for future device-aware rules.
	DeviceFlag  frame.DeviceFlag
	ClientSeq   uint64
	ClientMsgNo string
	StreamNo    string
	ChannelID   string
	ChannelType uint8
	// RequestSubscribers carries a one-message subscriber snapshot for command-style directed delivery.
	RequestSubscribers   []string
	Topic                string
	Payload              []byte
	CommitMode           channel.CommitMode
	ProtocolVersion      uint8
	ExpectedChannelEpoch uint64
	ExpectedLeaderEpoch  uint64
	// Origin identifies the caller class for plugin hook recursion controls.
	Origin SendOrigin
	// HookDepth records how many plugin Send hook layers have already run.
	HookDepth int
	// SkipPluginHooks bypasses Send hook invocation for trusted internal paths.
	SkipPluginHooks bool
}

// SendBatchItem carries one normalized send command with its own cancellation context.
type SendBatchItem struct {
	// Context is the per-send request context used for cancellation and timeout.
	Context context.Context
	// Command is the normalized send command for this item.
	Command SendCommand
}

// SendBatchItemResult is aligned with one SendBatch item.
type SendBatchItemResult struct {
	// Result contains the client-facing SEND outcome for this item.
	Result SendResult
	// Err contains the infrastructure or business error for this item, if any.
	Err error
}

type CommittedMessageEnvelope struct {
	ChannelID   string
	ChannelType uint8
	MessageID   uint64
	MessageSeq  uint64
	FromUID     string
	ClientMsgNo string
	Topic       string
	Payload     []byte
	Framer      frame.Framer
	Setting     frame.Setting
	MsgKey      string
	Expire      uint32
	StreamNo    string
	ClientSeq   uint64
}

type RecvAckCommand struct {
	UID        string
	SessionID  uint64
	Framer     frame.Framer
	MessageID  int64
	MessageSeq uint64
}

type RouteAckCommand struct {
	UID        string
	SessionID  uint64
	MessageID  uint64
	MessageSeq uint64
}

type SessionClosedCommand struct {
	UID       string
	SessionID uint64
}
