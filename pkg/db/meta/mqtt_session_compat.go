package meta

import "context"

// CompareAndSwapMQTTSession exposes the conditional row mutation to Slot FSM.
// Its result is valid only after the enclosing WriteBatch commits successfully.
func (b *WriteBatch) CompareAndSwapMQTTSession(slot uint16, expected uint64, row MQTTSession) (*MQTTSessionCASResult, error) {
	if err := b.ensure(); err != nil {
		return nil, err
	}
	return b.batch.CompareAndSwapMQTTSession(HashSlot(slot), expected, row)
}

// GetMQTTSession reads node storage after the caller establishes Slot authority.
func (s *ShardStore) GetMQTTSession(ctx context.Context, namespace, clientID string) (MQTTSession, bool, error) {
	if err := s.validate(); err != nil {
		return MQTTSession{}, false, err
	}
	return s.shard.GetMQTTSession(ctx, namespace, clientID)
}

// ListMQTTSessionDeadlines returns a bounded storage page; each candidate still
// needs current Slot leadership and revision validation before maintenance.
func (s *ShardStore) ListMQTTSessionDeadlines(ctx context.Context, after MQTTSessionDeadlineCursor, limit int) ([]MQTTSession, MQTTSessionDeadlineCursor, bool, error) {
	if err := s.validate(); err != nil {
		return nil, after, false, err
	}
	return s.shard.ListMQTTSessionDeadlines(ctx, after, limit)
}
