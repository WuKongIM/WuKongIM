package meta

import "context"

// CompareAndSwapMQTTSourceBinding exposes source-owned CAS to the Slot FSM.
// The returned result becomes meaningful only after the enclosing commit.
func (b *WriteBatch) CompareAndSwapMQTTSourceBinding(slot uint16, expected uint64, row MQTTSourceBinding) (*MQTTSourceBindingResult, error) {
	if err := b.ensure(); err != nil {
		return nil, err
	}
	return b.batch.CompareAndSwapMQTTSourceBinding(HashSlot(slot), expected, row)
}

// GetMQTTSourceBinding reads node storage after the caller establishes authority.
func (s *ShardStore) GetMQTTSourceBinding(ctx context.Context, key MQTTSourceBindingKey) (MQTTSourceBinding, bool, error) {
	if err := s.validate(); err != nil {
		return MQTTSourceBinding{}, false, err
	}
	return s.shard.GetMQTTSourceBinding(ctx, key)
}

// ListMQTTSourceBindingCandidates returns bounded source/UID-owned candidates.
func (s *ShardStore) ListMQTTSourceBindingCandidates(ctx context.Context, owner MQTTBindingOwner, after MQTTSourceBindingKey, limit int) ([]MQTTSourceBinding, MQTTSourceBindingKey, bool, error) {
	if err := s.validate(); err != nil {
		return nil, after, false, err
	}
	return s.shard.ListMQTTSourceBindingCandidates(ctx, owner, after, limit)
}

// ListMQTTSourceBindingRecovery returns pending work, not authority proof.
func (s *ShardStore) ListMQTTSourceBindingRecovery(ctx context.Context, after MQTTSourceBindingRecoveryCursor, limit int) ([]MQTTSourceBinding, MQTTSourceBindingRecoveryCursor, bool, error) {
	if err := s.validate(); err != nil {
		return nil, after, false, err
	}
	return s.shard.ListMQTTSourceBindingRecovery(ctx, after, limit)
}

// ListMQTTSourceBindingRetention returns conservative source obligations.
func (s *ShardStore) ListMQTTSourceBindingRetention(ctx context.Context, owner MQTTBindingOwner, after MQTTSourceBindingRetentionCursor, limit int) ([]MQTTSourceBinding, MQTTSourceBindingRetentionCursor, bool, error) {
	if err := s.validate(); err != nil {
		return nil, after, false, err
	}
	return s.shard.ListMQTTSourceBindingRetention(ctx, owner, after, limit)
}
