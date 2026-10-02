package meta

import "context"

// MutateMQTTDeliveryCursor exposes atomic range accounting to Slot FSM callers.
func (b *WriteBatch) MutateMQTTDeliveryCursor(slot uint16, m MQTTDeliveryCursorMutation) (*MQTTDeliveryCursorResult, error) {
	if err := b.ensure(); err != nil {
		return nil, err
	}
	return b.batch.MutateMQTTDeliveryCursor(HashSlot(slot), m)
}

// GetMQTTDeliveryCursor reads node storage after the caller establishes authority.
func (s *ShardStore) GetMQTTDeliveryCursor(ctx context.Context, key MQTTDeliveryCursorKey) (MQTTDeliveryCursor, bool, error) {
	if err := s.validate(); err != nil {
		return MQTTDeliveryCursor{}, false, err
	}
	return s.shard.GetMQTTDeliveryCursor(ctx, key)
}

// ListMQTTDeliveryCursors returns bounded generation-scoped source progress.
func (s *ShardStore) ListMQTTDeliveryCursors(ctx context.Context, namespace, clientID string, sessionGeneration, subscriptionGeneration uint64, after MQTTDeliveryCursorKey, limit int) ([]MQTTDeliveryCursor, MQTTDeliveryCursorKey, bool, error) {
	if err := s.validate(); err != nil {
		return nil, after, false, err
	}
	return s.shard.ListMQTTDeliveryCursors(ctx, namespace, clientID, sessionGeneration, subscriptionGeneration, after, limit)
}
