package proxy

import promotedcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"

type promotedRPCRegistrar interface {
	RegisterRPC(uint8, promotedcluster.NodeRPCHandler)
}

func registerPromotedStoreRPCHandlers(cluster Cluster, handlers []storeRPCRegistration) bool {
	registrar, ok := cluster.(promotedRPCRegistrar)
	if !ok {
		return false
	}
	for _, handler := range handlers {
		registrar.RegisterRPC(handler.serviceID, handler.handler)
	}
	return true
}
