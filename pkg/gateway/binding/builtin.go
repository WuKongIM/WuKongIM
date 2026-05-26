package binding

import "github.com/WuKongIM/WuKongIM/pkg/gateway"

func TCPWKProto(name, address string) gateway.ListenerOptions {
	return listener(name, "tcp", address, "gnet", "wkproto")
}

func WSJSONRPC(name, address string) gateway.ListenerOptions {
	return listener(name, "websocket", address, "gnet", "jsonrpc")
}

func WSMux(name, address string) gateway.ListenerOptions {
	return listener(name, "websocket", address, "gnet", "wsmux")
}
