package meta

import "context"

// CompareAndSwapMQTTWill exposes durable Will CAS to the Slot FSM. The result is
// valid only after commit; it does not replace an atomic Session lifecycle command.
func (b *WriteBatch) CompareAndSwapMQTTWill(slot uint16, expected uint64, row MQTTWill) (*MQTTWillResult, error) {
	if err := b.ensure(); err != nil {
		return nil, err
	}
	return b.batch.CompareAndSwapMQTTWill(HashSlot(slot), expected, row)
}

// GetMQTTWill returns owned content after the caller establishes Slot authority.
func (s *ShardStore) GetMQTTWill(ctx context.Context, key MQTTWillKey) (MQTTWill, bool, error) {
	if err := s.validate(); err != nil {
		return MQTTWill{}, false, err
	}
	return s.shard.GetMQTTWill(ctx, key)
}

// ListMQTTWillRecovery returns bounded candidates, not publication permission.
func (s *ShardStore) ListMQTTWillRecovery(ctx context.Context, after MQTTWillRecoveryCursor, limit int) ([]MQTTWill, MQTTWillRecoveryCursor, bool, error) {
	if err := s.validate(); err != nil {
		return nil, after, false, err
	}
	return s.shard.ListMQTTWillRecovery(ctx, after, limit)
}
