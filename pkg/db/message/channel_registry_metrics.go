package message

// ChannelEntryMetricsSnapshot is a low-cardinality aggregate view of canonical
// channel entry ownership for one physical message database.
type ChannelEntryMetricsSnapshot struct {
	// ActiveEntries is the number of channel keys currently held in the registry.
	ActiveEntries uint64
	// OutstandingLeases is the number of caller-owned channel handles.
	OutstandingLeases uint64
	// BackgroundPins is the number of commit-owned entry references.
	BackgroundPins uint64
	// AcquireTotal is the cumulative number of successful channel acquisitions.
	AcquireTotal uint64
	// ReleaseTotal is the cumulative number of terminal lease releases.
	ReleaseTotal uint64
	// ReclaimTotal is the cumulative number of canonical entries reclaimed at zero references.
	ReclaimTotal uint64
}

// ChannelEntryMetricsSnapshot returns the current aggregate registry ownership state.
func (db *MessageDB) ChannelEntryMetricsSnapshot() ChannelEntryMetricsSnapshot {
	if db == nil || db.registry == nil {
		return ChannelEntryMetricsSnapshot{}
	}
	return publicChannelEntryMetricsSnapshot(db.registry.snapshot())
}

// ChannelEntryMetricsSnapshot returns the compatibility engine's current aggregate registry ownership state.
func (e *Engine) ChannelEntryMetricsSnapshot() ChannelEntryMetricsSnapshot {
	if e == nil {
		return ChannelEntryMetricsSnapshot{}
	}
	e.mu.Lock()
	db := e.db
	e.mu.Unlock()
	if db == nil {
		return ChannelEntryMetricsSnapshot{}
	}
	return db.ChannelEntryMetricsSnapshot()
}

func publicChannelEntryMetricsSnapshot(snapshot channelRegistrySnapshot) ChannelEntryMetricsSnapshot {
	return ChannelEntryMetricsSnapshot{
		ActiveEntries:     uint64(snapshot.activeEntries),
		OutstandingLeases: snapshot.outstandingLeases,
		BackgroundPins:    snapshot.backgroundPins,
		AcquireTotal:      snapshot.acquireTotal,
		ReleaseTotal:      snapshot.releaseTotal,
		ReclaimTotal:      snapshot.reclaimTotal,
	}
}
