package meta

// ApplyMQTTLifecycle exposes atomic Session/Will state changes to the Slot FSM.
// The result is meaningful only after the enclosing WriteBatch commits.
func (b *WriteBatch) ApplyMQTTLifecycle(slot uint16, m MQTTLifecycleMutation) (*MQTTLifecycleResult, error) {
	if err := b.ensure(); err != nil {
		return nil, err
	}
	return b.batch.ApplyMQTTLifecycle(HashSlot(slot), m)
}
