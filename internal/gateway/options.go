package gateway

import gatewaytypes "github.com/WuKongIM/WuKongIM/internal/gateway/types"

type Options = gatewaytypes.Options
type ListenerOptions = gatewaytypes.ListenerOptions
type SessionOptions = gatewaytypes.SessionOptions

func DefaultSessionOptions() SessionOptions {
	return gatewaytypes.DefaultSessionOptions()
}

func NormalizeSessionOptions(opt SessionOptions) SessionOptions {
	return gatewaytypes.NormalizeSessionOptions(opt)
}
