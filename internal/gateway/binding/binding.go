package binding

import "github.com/WuKongIM/WuKongIM/internal/gateway"

const DefaultWSPath = ""

func listener(name, network, address, transport, protocol string) gateway.ListenerOptions {
	return gateway.ListenerOptions{
		Name:      name,
		Network:   network,
		Address:   address,
		Transport: transport,
		Protocol:  protocol,
	}
}
