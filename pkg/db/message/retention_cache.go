package message

// retentionReadState is immutable storage state retained by the bounded channel
// registry. It is neither a visibility decision nor a distributed read proof.
type retentionReadState struct {
	generation uint64
	state      RetentionState
	present    bool
}

// beginRetentionMutation fences all retained states before a possible durable
// change and again on exit, including ambiguous failures and overlapping imports.
// The conservative database-wide generation avoids retaining per-channel maps.
func (db *MessageDB) beginRetentionMutation() func() {
	db.retentionWriters.Add(1)
	db.retentionGeneration.Add(1)
	return func() {
		db.retentionGeneration.Add(1)
		db.retentionWriters.Add(-1)
	}
}

// retentionReadGeneration rejects an active writer or a generation sampled
// across a completed mutation. Readers never wait for retention maintenance.
func (db *MessageDB) retentionReadGeneration() (uint64, bool) {
	generation := db.retentionGeneration.Load()
	return generation, db.retentionWriters.Load() == 0 && db.retentionGeneration.Load() == generation
}
