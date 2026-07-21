package state

import "sort"

// Normalize applies deterministic defaults and ordering to a cluster state.
func (s *ClusterState) Normalize() {
	if s == nil {
		return
	}
	s.UpdatedAt = s.UpdatedAt.UTC()
	if s.Controllers == nil {
		s.Controllers = []ControllerVoter{}
	}
	if s.Nodes == nil {
		s.Nodes = []Node{}
	}
	if s.Slots == nil {
		s.Slots = []SlotAssignment{}
	}
	if s.HashSlots.Ranges == nil {
		s.HashSlots.Ranges = []HashSlotRange{}
	}
	if s.Tasks == nil {
		s.Tasks = []ReconcileTask{}
	}
	for i := range s.Nodes {
		if s.Nodes[i].CapacityWeight == 0 {
			s.Nodes[i].CapacityWeight = 1
		}
		sort.Slice(s.Nodes[i].Roles, func(a, b int) bool { return s.Nodes[i].Roles[a] < s.Nodes[i].Roles[b] })
	}
	for i := range s.Slots {
		sort.Slice(s.Slots[i].DesiredPeers, func(a, b int) bool { return s.Slots[i].DesiredPeers[a] < s.Slots[i].DesiredPeers[b] })
	}
	sort.Slice(s.NodeHealthReports, func(i, j int) bool { return s.NodeHealthReports[i].NodeID < s.NodeHealthReports[j].NodeID })
	for i := range s.Tasks {
		sort.Slice(s.Tasks[i].TargetPeers, func(a, b int) bool { return s.Tasks[i].TargetPeers[a] < s.Tasks[i].TargetPeers[b] })
		sort.Slice(s.Tasks[i].ObservedVoters, func(a, b int) bool { return s.Tasks[i].ObservedVoters[a] < s.Tasks[i].ObservedVoters[b] })
		sort.Slice(s.Tasks[i].ObservedLearners, func(a, b int) bool { return s.Tasks[i].ObservedLearners[a] < s.Tasks[i].ObservedLearners[b] })
		normalizeTaskProgress(&s.Tasks[i])
	}
	if s.Backup != nil {
		if s.Backup.RestorePoints == nil {
			s.Backup.RestorePoints = []BackupRestorePoint{}
		}
		if s.Backup.PendingGarbage == nil {
			s.Backup.PendingGarbage = []BackupRestorePoint{}
		}
		if s.Backup.Active != nil {
			if s.Backup.Active.Partitions == nil {
				s.Backup.Active.Partitions = []BackupPartitionReport{}
			}
			sort.Slice(s.Backup.Active.Partitions, func(i, j int) bool {
				return s.Backup.Active.Partitions[i].HashSlot < s.Backup.Active.Partitions[j].HashSlot
			})
		}
		sort.Slice(s.Backup.RestorePoints, func(i, j int) bool {
			if s.Backup.RestorePoints[i].EffectiveAtUnixMillis == s.Backup.RestorePoints[j].EffectiveAtUnixMillis {
				return s.Backup.RestorePoints[i].ID < s.Backup.RestorePoints[j].ID
			}
			return s.Backup.RestorePoints[i].EffectiveAtUnixMillis > s.Backup.RestorePoints[j].EffectiveAtUnixMillis
		})
		sort.Slice(s.Backup.PendingGarbage, func(i, j int) bool {
			if s.Backup.PendingGarbage[i].CreatedAtUnixMillis == s.Backup.PendingGarbage[j].CreatedAtUnixMillis {
				return s.Backup.PendingGarbage[i].ID < s.Backup.PendingGarbage[j].ID
			}
			return s.Backup.PendingGarbage[i].CreatedAtUnixMillis < s.Backup.PendingGarbage[j].CreatedAtUnixMillis
		})
	}
	if s.Restore != nil && s.Restore.Plan != nil {
		if s.Restore.Plan.Partitions == nil {
			s.Restore.Plan.Partitions = []RestorePartition{}
		}
		sort.Slice(s.Restore.Plan.Partitions, func(i, j int) bool {
			return s.Restore.Plan.Partitions[i].HashSlot < s.Restore.Plan.Partitions[j].HashSlot
		})
	}
	sort.Slice(s.Controllers, func(i, j int) bool { return s.Controllers[i].NodeID < s.Controllers[j].NodeID })
	sort.Slice(s.Nodes, func(i, j int) bool { return s.Nodes[i].NodeID < s.Nodes[j].NodeID })
	sort.Slice(s.Slots, func(i, j int) bool { return s.Slots[i].SlotID < s.Slots[j].SlotID })
	sort.Slice(s.HashSlots.Ranges, func(i, j int) bool {
		if s.HashSlots.Ranges[i].From == s.HashSlots.Ranges[j].From {
			return s.HashSlots.Ranges[i].To < s.HashSlots.Ranges[j].To
		}
		return s.HashSlots.Ranges[i].From < s.HashSlots.Ranges[j].From
	})
	sort.Slice(s.Tasks, func(i, j int) bool {
		if s.Tasks[i].SlotID == s.Tasks[j].SlotID {
			return s.Tasks[i].TaskID < s.Tasks[j].TaskID
		}
		return s.Tasks[i].SlotID < s.Tasks[j].SlotID
	})
}

// Clone returns a deep copy of the cluster state.
func (s ClusterState) Clone() ClusterState {
	out := s
	out.Controllers = cloneSlice(s.Controllers)
	out.Nodes = cloneSlice(s.Nodes)
	for i := range out.Nodes {
		out.Nodes[i].Roles = cloneSlice(s.Nodes[i].Roles)
	}
	out.Slots = cloneSlice(s.Slots)
	for i := range out.Slots {
		out.Slots[i].DesiredPeers = cloneUint64s(s.Slots[i].DesiredPeers)
	}
	out.NodeHealthReports = cloneNodeHealthReports(s.NodeHealthReports)
	out.HashSlots.Ranges = cloneSlice(s.HashSlots.Ranges)
	out.Tasks = cloneSlice(s.Tasks)
	for i := range out.Tasks {
		out.Tasks[i].TargetPeers = cloneUint64s(s.Tasks[i].TargetPeers)
		out.Tasks[i].ParticipantProgress = cloneSlice(s.Tasks[i].ParticipantProgress)
		out.Tasks[i].ObservedVoters = cloneUint64s(s.Tasks[i].ObservedVoters)
		out.Tasks[i].ObservedLearners = cloneUint64s(s.Tasks[i].ObservedLearners)
	}
	if s.Backup != nil {
		backup := s.Backup.Clone()
		out.Backup = &backup
	}
	if s.Restore != nil {
		restore := s.Restore.Clone()
		out.Restore = &restore
	}
	return out
}

func normalizeTaskProgress(task *ReconcileTask) {
	if task == nil {
		return
	}
	if task.Kind == TaskKindBootstrap && task.CompletionPolicy == "" {
		task.CompletionPolicy = TaskCompletionPolicyAllTargetPeers
	}
	if task.Kind == TaskKindLeaderTransfer && task.CompletionPolicy == "" {
		task.CompletionPolicy = TaskCompletionPolicySingleObserver
	}
	if task.Kind == TaskKindSlotReplicaMove && task.CompletionPolicy == "" {
		task.CompletionPolicy = TaskCompletionPolicySingleObserver
	}
	if task.CompletionPolicy == TaskCompletionPolicyAllTargetPeers && len(task.ParticipantProgress) == 0 {
		task.ParticipantProgress = make([]TaskParticipantProgress, 0, len(task.TargetPeers))
		for _, peerID := range task.TargetPeers {
			task.ParticipantProgress = append(task.ParticipantProgress, TaskParticipantProgress{NodeID: peerID, Status: TaskParticipantStatusPending})
		}
	}
	sort.Slice(task.ParticipantProgress, func(i, j int) bool {
		return task.ParticipantProgress[i].NodeID < task.ParticipantProgress[j].NodeID
	})
}

// HasRole reports whether the node has the requested durable capability.
func (n Node) HasRole(role NodeRole) bool {
	for _, candidate := range n.Roles {
		if candidate == role {
			return true
		}
	}
	return false
}

func cloneUint64s(in []uint64) []uint64 {
	return cloneSlice(in)
}

func cloneNodeHealthReports(in []NodeHealthReport) []NodeHealthReport {
	if len(in) == 0 {
		return nil
	}
	out := make([]NodeHealthReport, len(in))
	copy(out, in)
	return out
}

func cloneSlice[T any](in []T) []T {
	if in == nil {
		return nil
	}
	out := make([]T, len(in))
	copy(out, in)
	return out
}
