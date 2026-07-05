package proxy

import "context"

// RegisterRPCHandlerFunc registers one Slot proxy RPC handler.
type RegisterRPCHandlerFunc func(serviceID uint8, handler func(context.Context, []byte) ([]byte, error))

type storeRPCRegistration struct {
	serviceID uint8
	handler   storeRPCHandlerFunc
}

type storeRPCHandlerFunc func(context.Context, []byte) ([]byte, error)

func (f storeRPCHandlerFunc) HandleRPC(ctx context.Context, payload []byte) ([]byte, error) {
	return f(ctx, payload)
}

func registerStoreRPCHandlers(cluster Cluster, store *Store) {
	if cluster == nil || store == nil {
		return
	}
	handlers := storeRPCHandlers(store)
	if registerPromotedStoreRPCHandlers(cluster, handlers) {
		return
	}
}

// RegisterRPCHandlers exposes Store-owned RPC handlers to composition roots.
func (s *Store) RegisterRPCHandlers(register RegisterRPCHandlerFunc) {
	if s == nil || register == nil {
		return
	}
	for _, handler := range storeRPCHandlers(s) {
		register(handler.serviceID, handler.handler)
	}
}

func storeRPCHandlers(store *Store) []storeRPCRegistration {
	return []storeRPCRegistration{
		{serviceID: runtimeMetaRPCServiceID, handler: store.handleRuntimeMetaRPC},
		{serviceID: identityRPCServiceID, handler: store.handleIdentityRPC},
		{serviceID: subscriberRPCServiceID, handler: store.handleSubscriberRPC},
		{serviceID: channelRPCServiceID, handler: store.handleChannelRPC},
		{serviceID: userConversationStateRPCServiceID, handler: store.handleUserConversationStateRPC},
		{serviceID: channelMigrationRPCServiceID, handler: store.handleChannelMigrationRPC},
		{serviceID: cmdConversationStateRPCServiceID, handler: store.handleCMDConversationStateRPC},
		{serviceID: pluginBindingRPCServiceID, handler: store.handlePluginBindingRPC},
	}
}
