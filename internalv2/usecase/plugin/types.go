package plugin

import (
	"context"
	"errors"
	"time"

	"github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

var (
	// ErrPluginNoRequired reports that a plugin operation omitted the plugin number.
	ErrPluginNoRequired = errors.New("plugin no required")
	// ErrPluginCallerMismatch reports a mismatch between the authenticated caller and plugin number.
	ErrPluginCallerMismatch = errors.New("plugin caller mismatch")
	// ErrPluginNotFound reports that a requested plugin observation does not exist.
	ErrPluginNotFound = errors.New("plugin not found")
	// ErrRuntimeRequired reports that the plugin usecase was built without a runtime port.
	ErrRuntimeRequired = errors.New("plugin runtime required")
	// ErrInvokerRequired reports that the plugin usecase was built without an invocation port.
	ErrInvokerRequired = errors.New("plugin invoker required")
	// ErrMessageSenderRequired reports that host message/send needs the message usecase port.
	ErrMessageSenderRequired = errors.New("plugin message sender required")
	// ErrDefaultSenderUIDRequired reports that plugin-origin sends without fromUid need a default sender.
	ErrDefaultSenderUIDRequired = errors.New("plugin default sender uid required")
	// ErrMessageReaderRequired reports that host channel/messages needs the message reader port.
	ErrMessageReaderRequired = errors.New("plugin message reader required")
)

// Method identifies a plugin hook advertised by a plugin manifest.
type Method string

const (
	// MethodSend is invoked before a message append.
	MethodSend Method = "Send"
	// MethodPersistAfter is invoked after a durable message is committed.
	MethodPersistAfter Method = "PersistAfter"
)

// Status describes the node-local runtime state observed for a plugin.
type Status string

const (
	// StatusRunning means the plugin has an active host RPC connection.
	StatusRunning Status = "running"
	// StatusOffline means the plugin is not connected on this node.
	StatusOffline Status = "offline"
	// StatusDisabled means this node should not invoke the plugin.
	StatusDisabled Status = "disabled"
)

// ObservedPlugin is the node-local runtime view consumed by v2 plugin hooks.
type ObservedPlugin struct {
	// No is the stable plugin number used as the runtime UID.
	No string
	// Name is the human-readable plugin name from the manifest.
	Name string
	// Version is the plugin version from the manifest.
	Version string
	// Methods lists hook methods advertised by the plugin.
	Methods []Method
	// Priority controls hook ordering; larger values run first.
	Priority int
	// PersistAfterSync reports whether PersistAfter waits for plugin completion.
	PersistAfterSync bool
	// ReplySync reports whether reply hooks wait for plugin completion.
	ReplySync bool
	// Status is the current node-local process status.
	Status Status
	// Enabled reports whether this node should invoke the plugin.
	Enabled bool
	// PID is the local operating-system process id when available.
	PID int
	// LastSeenAt records the latest runtime observation time.
	LastSeenAt time.Time
	// LastError stores the latest runtime error message.
	LastError string
}

// Runtime stores node-local observed plugin state for the v2 usecase.
type Runtime interface {
	RegisterObserved(context.Context, ObservedPlugin) error
	MarkClosed(context.Context, string) error
	List() []ObservedPlugin
}

// Invoker calls or sends PDK-compatible plugin hook payloads.
type Invoker interface {
	RequestPlugin(context.Context, string, string, []byte) ([]byte, error)
	SendPlugin(string, uint32, []byte) error
}

// MessageSender submits plugin-origin messages through the v2 message usecase.
type MessageSender interface {
	Send(context.Context, message.SendCommand) (message.SendResult, error)
}

// MessageReader reads authoritative channel message pages for plugin host RPCs.
type MessageReader interface {
	SyncMessages(context.Context, message.ChannelMessageQuery) (message.ChannelMessagePage, error)
}

// Observer records low-cardinality synchronous plugin hook events.
type Observer interface {
	ObserveSendInvoke(result string, d time.Duration)
}

// Options configures the v2 plugin usecase.
type Options struct {
	Runtime Runtime
	Invoker Invoker
	// Messages submits plugin-origin /message/send host RPCs.
	Messages MessageSender
	// DefaultSenderUID is used when legacy plugin send requests omit fromUid.
	DefaultSenderUID string
	// MessageReader reads authoritative channel message pages for plugin host RPCs.
	MessageReader MessageReader
	// FailOpen lets synchronous Send hook infrastructure failures preserve the original send.
	FailOpen bool
	Observer Observer
	Logger   wklog.Logger
}

// App orchestrates v2 plugin lifecycle, selection, and hook invocation usecases.
type App struct {
	runtime          Runtime
	invoker          Invoker
	messages         MessageSender
	messageReader    MessageReader
	defaultSenderUID string
	failOpen         bool
	observer         Observer
	logger           wklog.Logger
}
