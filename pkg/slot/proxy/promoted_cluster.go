package proxy

import "context"

type promotedRPCRegistrar interface {
	RegisterSlotProxyRPC(uint8, func(context.Context, []byte) ([]byte, error))
}

func registerPromotedStoreRPCHandlers(cluster Cluster, handlers []storeRPCRegistration) bool {
	registrar, ok := cluster.(promotedRPCRegistrar)
	if !ok {
		return false
	}
	for _, handler := range handlers {
		registrar.RegisterSlotProxyRPC(handler.serviceID, handler.handler)
	}
	return true
}
