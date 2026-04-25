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
	Listeners      []ListenerOptions
	Logger         wklog.Logger
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
	ReadBufferSize      int
	WriteQueueSize      int
	MaxInboundBytes     int
	MaxOutboundBytes    int
	IdleTimeout         time.Duration
	WriteTimeout        time.Duration
	AsyncSendDispatch   bool
	CloseOnHandlerError *bool
}

func DefaultSessionOptions() SessionOptions {
	return SessionOptions{
		ReadBufferSize:      4 << 10,
		WriteQueueSize:      64,
		MaxInboundBytes:     1 << 20,
		MaxOutboundBytes:    1 << 20,
		IdleTimeout:         3 * time.Minute,
		WriteTimeout:        10 * time.Second,
		CloseOnHandlerError: boolPtr(true),
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
	if opt.ReadBufferSize == 0 {
		opt.ReadBufferSize = def.ReadBufferSize
	}
	if opt.WriteQueueSize == 0 {
		opt.WriteQueueSize = def.WriteQueueSize
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
	if opt.WriteTimeout == 0 {
		opt.WriteTimeout = def.WriteTimeout
	}
	if opt.CloseOnHandlerError == nil {
		opt.CloseOnHandlerError = def.CloseOnHandlerError
	}
	return opt
}

func boolPtr(v bool) *bool { return &v }
