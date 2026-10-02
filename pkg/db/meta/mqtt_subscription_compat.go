package meta

import "context"

// MutateMQTTSubscription exposes owner-fenced intent writes to the Slot FSM.
// The result becomes meaningful after the enclosing WriteBatch commits.
func (b *WriteBatch) MutateMQTTSubscription(slot uint16, m MQTTSubscriptionMutation) (*MQTTSessionCASResult, error) {
	if err := b.ensure(); err != nil {
		return nil, err
	}
	return b.batch.MutateMQTTSubscription(HashSlot(slot), m)
}

// GetMQTTSubscription reads node storage after the caller establishes authority.
func (s *ShardStore) GetMQTTSubscription(ctx context.Context, namespace, clientID string, generation uint64, topic string) (MQTTSubscription, bool, error) {
	if err := s.validate(); err != nil {
		return MQTTSubscription{}, false, err
	}
	return s.shard.GetMQTTSubscription(ctx, namespace, clientID, generation, topic)
}

// ListMQTTSubscriptions returns a bounded generation-scoped storage page.
func (s *ShardStore) ListMQTTSubscriptions(ctx context.Context, namespace, clientID string, generation uint64, after string, limit int) ([]MQTTSubscription, string, bool, error) {
	if err := s.validate(); err != nil {
		return nil, after, false, err
	}
	return s.shard.ListMQTTSubscriptions(ctx, namespace, clientID, generation, after, limit)
}

// ListMQTTSubscriptionRecovery returns candidates, not current owner authority.
func (s *ShardStore) ListMQTTSubscriptionRecovery(ctx context.Context, after MQTTSubscriptionRecoveryCursor, limit int) ([]MQTTSubscription, MQTTSubscriptionRecoveryCursor, bool, error) {
	if err := s.validate(); err != nil {
		return nil, after, false, err
	}
	return s.shard.ListMQTTSubscriptionRecovery(ctx, after, limit)
}
