package gateway

import gatewaytypes "github.com/WuKongIM/WuKongIM/pkg/gateway/types"

type Options = gatewaytypes.Options
type ListenerOptions = gatewaytypes.ListenerOptions
type SessionOptions = gatewaytypes.SessionOptions
type RuntimeOptions = gatewaytypes.RuntimeOptions
type TransportOptions = gatewaytypes.TransportOptions
type GnetTransportOptions = gatewaytypes.GnetTransportOptions

func DefaultSessionOptions() SessionOptions {
	return gatewaytypes.DefaultSessionOptions()
}

func DefaultRuntimeOptions() RuntimeOptions {
	return gatewaytypes.DefaultRuntimeOptions()
}

func NormalizeSessionOptions(opt SessionOptions) SessionOptions {
	return gatewaytypes.NormalizeSessionOptions(opt)
}

func NormalizeRuntimeOptions(opt RuntimeOptions) RuntimeOptions {
	return gatewaytypes.NormalizeRuntimeOptions(opt)
}
