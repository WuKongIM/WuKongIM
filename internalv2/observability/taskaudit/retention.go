package taskaudit

import "sort"

func (s *Store) enforceRetentionLocked() {
	s.enforceEventRetentionLocked()
	s.enforceTaskRetentionLocked()
}

func (s *Store) enforceEventRetentionLocked() {
	for taskID, events := range s.events {
		if len(events) <= s.opts.MaxEventsPerTask {
			continue
		}
		sortEventsAsc(events)
		drop := len(events) - s.opts.MaxEventsPerTask
		for _, event := range events[:drop] {
			delete(s.eventIDs, event.EventID)
		}
		retained := append([]Event(nil), events[drop:]...)
		s.events[taskID] = retained
		snapshot := rebuildSnapshot(taskID, retained)
		snapshot.Truncated = true
		s.snapshots[taskID] = snapshot
	}
}

func (s *Store) enforceTaskRetentionLocked() {
	if len(s.snapshots) <= s.opts.MaxTasks {
		return
	}
	items := make([]Snapshot, 0, len(s.snapshots))
	for _, snapshot := range s.snapshots {
		items = append(items, snapshot)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].LastAppliedRaftIndex == items[j].LastAppliedRaftIndex {
			return items[i].TaskID < items[j].TaskID
		}
		return items[i].LastAppliedRaftIndex < items[j].LastAppliedRaftIndex
	})
	drop := len(items) - s.opts.MaxTasks
	for _, snapshot := range items[:drop] {
		for _, event := range s.events[snapshot.TaskID] {
			delete(s.eventIDs, event.EventID)
		}
		delete(s.events, snapshot.TaskID)
		delete(s.snapshots, snapshot.TaskID)
	}
}

func rebuildSnapshot(taskID string, events []Event) Snapshot {
	snapshot := Snapshot{TaskID: taskID}
	for _, event := range events {
		if snapshot.StartedAt.IsZero() || event.OccurredAt.Before(snapshot.StartedAt) {
			snapshot.StartedAt = event.OccurredAt
		}
		if snapshot.FirstAppliedRaftIndex == 0 || event.AppliedRaftIndex < snapshot.FirstAppliedRaftIndex {
			snapshot.FirstAppliedRaftIndex = event.AppliedRaftIndex
		}
		if event.AppliedRaftIndex >= snapshot.LastAppliedRaftIndex {
			updateSnapshotFromEvent(&snapshot, event)
		}
	}
	snapshot.EventCount = len(events)
	return snapshot
}

func (s *Store) retainedEventsLocked() []Event {
	var out []Event
	for _, events := range s.events {
		out = append(out, cloneEvents(events)...)
	}
	sortEventsAsc(out)
	return out
}

func sortEventsAsc(events []Event) {
	sort.Slice(events, func(i, j int) bool {
		if events[i].AppliedRaftIndex == events[j].AppliedRaftIndex {
			if events[i].OccurredAt.Equal(events[j].OccurredAt) {
				return events[i].EventID < events[j].EventID
			}
			return events[i].OccurredAt.Before(events[j].OccurredAt)
		}
		return events[i].AppliedRaftIndex < events[j].AppliedRaftIndex
	})
}

func sortSnapshotsDesc(items []Snapshot) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].LastAppliedRaftIndex == items[j].LastAppliedRaftIndex {
			return items[i].TaskID < items[j].TaskID
		}
		return items[i].LastAppliedRaftIndex > items[j].LastAppliedRaftIndex
	})
}
