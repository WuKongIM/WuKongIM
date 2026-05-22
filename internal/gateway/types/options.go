package types

import (
	"fmt"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

type Options struct {
	Handler        Handler
	Authenticator  Authenticator
	Observer       Observer
	DefaultSession SessionOptions
	Transport      TransportOptions
	Listeners      []ListenerOptions
	Logger         wklog.Logger
}

// TransportOptions groups transport-specific gateway runtime tuning.
type TransportOptions struct {
	// Gnet configures the gnet transport used by high-throughput TCP and WebSocket listeners.
	Gnet GnetTransportOptions
}

// GnetTransportOptions controls gnet engine tuning while preserving zero-value defaults.
type GnetTransportOptions struct {
	// Multicore enables gnet's CPU-scaled multi event-loop mode when true.
	Multicore bool
	// NumEventLoop sets an explicit gnet event-loop count; zero keeps gnet's default.
	NumEventLoop int
	// ReusePort enables SO_REUSEPORT where the operating system and gnet support it.
	ReusePort bool
	// ReadBufferCap sets gnet's per-event-loop read buffer cap; zero keeps gnet's default.
	ReadBufferCap int
	// WriteBufferCap sets gnet's per-connection static write buffer cap; zero keeps gnet's default.
	WriteBufferCap int
}

type ListenerOptions struct {
	Name      string
	Network   string
	Address   string
	Path      string
	Transport string
	Protocol  string
}

type SessionOptions struct {
	MaxInboundBytes  int
	MaxOutboundBytes int
	IdleTimeout      time.Duration
	// AsyncSendDispatchWorkers sets the SEND worker pool size. Non-positive values use an IO-wait-aware adaptive default.
	AsyncSendDispatchWorkers int
	// AsyncSendBatchMaxWait bounds how long a SEND shard waits to collect adjacent frames.
	AsyncSendBatchMaxWait time.Duration
	// AsyncSendBatchMaxRecords caps SEND frames in one gateway micro-batch.
	AsyncSendBatchMaxRecords int
	// AsyncSendBatchMaxBytes caps payload bytes in one gateway micro-batch.
	AsyncSendBatchMaxBytes int
	CloseOnHandlerError    *bool
}

const (
	defaultAsyncSendBatchMaxWait    = 500 * time.Microsecond
	defaultAsyncSendBatchMaxRecords = 128
	defaultAsyncSendBatchMaxBytes   = 512 * 1024
)

func DefaultSessionOptions() SessionOptions {
	return SessionOptions{
		MaxInboundBytes:          1 << 20,
		MaxOutboundBytes:         1 << 20,
		IdleTimeout:              3 * time.Minute,
		AsyncSendBatchMaxWait:    defaultAsyncSendBatchMaxWait,
		AsyncSendBatchMaxRecords: defaultAsyncSendBatchMaxRecords,
		AsyncSendBatchMaxBytes:   defaultAsyncSendBatchMaxBytes,
		CloseOnHandlerError:      boolPtr(true),
	}
}

func (o *Options) Validate() error {
	if o == nil {
		return fmt.Errorf("gateway: nil options")
	}
	o.DefaultSession = NormalizeSessionOptions(o.DefaultSession)
	seenNames := make(map[string]struct{}, len(o.Listeners))
	seenAddresses := make(map[string]struct{}, len(o.Listeners))
	for i := range o.Listeners {
		o.Listeners[i].Name = strings.TrimSpace(o.Listeners[i].Name)
		o.Listeners[i].Network = strings.TrimSpace(o.Listeners[i].Network)
		o.Listeners[i].Address = strings.TrimSpace(o.Listeners[i].Address)
		o.Listeners[i].Path = strings.TrimSpace(o.Listeners[i].Path)
		o.Listeners[i].Transport = strings.TrimSpace(o.Listeners[i].Transport)
		o.Listeners[i].Protocol = strings.TrimSpace(o.Listeners[i].Protocol)

		name := o.Listeners[i].Name
		network := o.Listeners[i].Network
		address := o.Listeners[i].Address
		transport := o.Listeners[i].Transport
		protocol := o.Listeners[i].Protocol

		if name == "" {
			return ErrListenerNameEmpty
		}
		if _, ok := seenNames[name]; ok {
			return ErrListenerNameDuplicate
		}
		seenNames[name] = struct{}{}
		if address == "" {
			return ErrListenerAddressEmpty
		}
		if _, ok := seenAddresses[address]; ok {
			return ErrListenerAddressDuplicate
		}
		seenAddresses[address] = struct{}{}
		if network == "" {
			return ErrListenerNetworkEmpty
		}
		if transport == "" {
			return ErrListenerTransportEmpty
		}
		if protocol == "" {
			return ErrListenerProtocolEmpty
		}
	}
	if o.Handler == nil {
		return ErrNilHandler
	}
	return nil
}

func NormalizeSessionOptions(opt SessionOptions) SessionOptions {
	def := DefaultSessionOptions()
	if opt == (SessionOptions{}) {
		return def
	}
	if opt.MaxInboundBytes == 0 {
		opt.MaxInboundBytes = def.MaxInboundBytes
	}
	if opt.MaxOutboundBytes == 0 {
		opt.MaxOutboundBytes = def.MaxOutboundBytes
	}
	if opt.IdleTimeout == 0 {
		opt.IdleTimeout = def.IdleTimeout
	}
	if opt.AsyncSendBatchMaxWait == 0 {
		opt.AsyncSendBatchMaxWait = def.AsyncSendBatchMaxWait
	} else if opt.AsyncSendBatchMaxWait < 0 {
		opt.AsyncSendBatchMaxWait = 0
	}
	if opt.AsyncSendBatchMaxRecords <= 0 {
		opt.AsyncSendBatchMaxRecords = def.AsyncSendBatchMaxRecords
	}
	if opt.AsyncSendBatchMaxBytes <= 0 {
		opt.AsyncSendBatchMaxBytes = def.AsyncSendBatchMaxBytes
	}
	if opt.CloseOnHandlerError == nil {
		opt.CloseOnHandlerError = def.CloseOnHandlerError
	}
	return opt
}

func boolPtr(v bool) *bool { return &v }
