package meta

import "context"

// MutateMQTTWindow exposes atomic exchange/cursor/accounting changes to Slot FSM.
func (b *WriteBatch) MutateMQTTWindow(slot uint16, m MQTTWindowMutation) (*MQTTWindowResult, error) {
	if err := b.ensure(); err != nil {
		return nil, err
	}
	return b.batch.MutateMQTTWindow(HashSlot(slot), m)
}

// GetMQTTInflight reads one durable exchange after the caller proves authority.
func (s *ShardStore) GetMQTTInflight(ctx context.Context, namespace, clientID string, generation uint64, direction MQTTExchangeDirection, packetID uint16) (MQTTInflight, bool, error) {
	if err := s.validate(); err != nil {
		return MQTTInflight{}, false, err
	}
	return s.shard.GetMQTTInflight(ctx, namespace, clientID, generation, direction, packetID)
}

// ListMQTTInflight returns a bounded page in original send order.
func (s *ShardStore) ListMQTTInflight(ctx context.Context, namespace, clientID string, generation uint64, direction MQTTExchangeDirection, after MQTTInflightCursor, limit int) ([]MQTTInflight, MQTTInflightCursor, bool, error) {
	if err := s.validate(); err != nil {
		return nil, after, false, err
	}
	return s.shard.ListMQTTInflight(ctx, namespace, clientID, generation, direction, after, limit)
}
