package meta

import (
	"encoding/binary"
	"math"
	"sort"
	"time"
)

func encodeOnboardingJobKey(jobID string) []byte {
	key := make([]byte, 1, 1+len(jobID))
	key[0] = recordPrefixOnboardingJob
	return append(key, jobID...)
}

func decodeOnboardingJobKey(key []byte) (string, error) {
	if len(key) <= 1 || key[0] != recordPrefixOnboardingJob {
		return "", ErrCorruptValue
	}
	return string(key[1:]), nil
}

func encodeOnboardingJob(job NodeOnboardingJob) []byte {
	job = normalizeOnboardingJob(job)

	data := make([]byte, 0, 256)
	data = append(data, recordVersion)
	data = appendString(data, job.RetryOfJobID)
	data = appendString(data, string(job.Status))
	data = binary.BigEndian.AppendUint64(data, job.TargetNodeID)
	data = appendTime(data, job.CreatedAt)
	data = appendTime(data, job.UpdatedAt)
	data = appendTime(data, job.StartedAt)
	data = appendTime(data, job.CompletedAt)
	data = binary.BigEndian.AppendUint32(data, job.PlanVersion)
	data = appendString(data, job.PlanFingerprint)
	data = appendOnboardingPlan(data, job.Plan)
	data = appendOnboardingMoves(data, job.Moves)
	data = appendInt64(data, int64(job.CurrentMoveIndex))
	data = appendOnboardingResultCounts(data, job.ResultCounts)
	data = appendReconcileTaskPtr(data, job.CurrentTask)
	data = appendString(data, job.LastError)
	return data
}

func decodeOnboardingJob(key, data []byte) (NodeOnboardingJob, error) {
	jobID, err := decodeOnboardingJobKey(key)
	if err != nil {
		return NodeOnboardingJob{}, err
	}
	if len(data) == 0 || data[0] != recordVersion {
		return NodeOnboardingJob{}, ErrCorruptValue
	}
	rest := data[1:]

	retryOfJobID, rest, err := readString(rest)
	if err != nil {
		return NodeOnboardingJob{}, ErrCorruptValue
	}
	statusValue, rest, err := readString(rest)
	if err != nil {
		return NodeOnboardingJob{}, ErrCorruptValue
	}
	status := OnboardingJobStatus(statusValue)
	if !validOnboardingJobStatus(status) {
		return NodeOnboardingJob{}, ErrCorruptValue
	}
	if len(rest) < 8+8+8+8+8+4 {
		return NodeOnboardingJob{}, ErrCorruptValue
	}
	targetNodeID := binary.BigEndian.Uint64(rest[:8])
	rest = rest[8:]
	createdAt, rest, err := readTime(rest)
	if err != nil {
		return NodeOnboardingJob{}, ErrCorruptValue
	}
	updatedAt, rest, err := readTime(rest)
	if err != nil {
		return NodeOnboardingJob{}, ErrCorruptValue
	}
	startedAt, rest, err := readTime(rest)
	if err != nil {
		return NodeOnboardingJob{}, ErrCorruptValue
	}
	completedAt, rest, err := readTime(rest)
	if err != nil {
		return NodeOnboardingJob{}, ErrCorruptValue
	}
	planVersion := binary.BigEndian.Uint32(rest[:4])
	rest = rest[4:]
	planFingerprint, rest, err := readString(rest)
	if err != nil {
		return NodeOnboardingJob{}, ErrCorruptValue
	}
	plan, rest, err := readOnboardingPlan(rest)
	if err != nil {
		return NodeOnboardingJob{}, err
	}
	moves, rest, err := readOnboardingMoves(rest)
	if err != nil {
		return NodeOnboardingJob{}, err
	}
	currentMoveIndex, rest, err := readInt64(rest)
	if err != nil || currentMoveIndex < int64(math.MinInt) || currentMoveIndex > int64(math.MaxInt) {
		return NodeOnboardingJob{}, ErrCorruptValue
	}
	counts, rest, err := readOnboardingResultCounts(rest)
	if err != nil {
		return NodeOnboardingJob{}, err
	}
	currentTask, rest, err := readReconcileTaskPtr(rest)
	if err != nil {
		return NodeOnboardingJob{}, err
	}
	lastError, rest, err := readString(rest)
	if err != nil || len(rest) != 0 {
		return NodeOnboardingJob{}, ErrCorruptValue
	}

	job := NodeOnboardingJob{
		JobID:            jobID,
		TargetNodeID:     targetNodeID,
		RetryOfJobID:     retryOfJobID,
		Status:           status,
		CreatedAt:        createdAt,
		UpdatedAt:        updatedAt,
		StartedAt:        startedAt,
		CompletedAt:      completedAt,
		PlanVersion:      planVersion,
		PlanFingerprint:  planFingerprint,
		Plan:             plan,
		Moves:            moves,
		CurrentMoveIndex: int(currentMoveIndex),
		ResultCounts:     counts,
		CurrentTask:      currentTask,
		LastError:        lastError,
	}
	if _, err := normalizeAndValidateOnboardingJob(job, ErrCorruptValue); err != nil {
		return NodeOnboardingJob{}, err
	}
	return job, nil
}

func normalizeOnboardingJob(job NodeOnboardingJob) NodeOnboardingJob {
	job.Plan.Moves = normalizeOnboardingPlanMoves(job.Plan.Moves)
	if len(job.Moves) > 0 {
		job.Moves = append([]NodeOnboardingMove(nil), job.Moves...)
	}
	for i := range job.Moves {
		job.Moves[i].DesiredPeersBefore = normalizeUint64Set(job.Moves[i].DesiredPeersBefore)
		job.Moves[i].DesiredPeersAfter = normalizeUint64Set(job.Moves[i].DesiredPeersAfter)
	}
	return job
}

func normalizeOnboardingPlanMoves(moves []NodeOnboardingPlanMove) []NodeOnboardingPlanMove {
	out := append([]NodeOnboardingPlanMove(nil), moves...)
	for i := range out {
		out[i].DesiredPeersBefore = normalizeUint64Set(out[i].DesiredPeersBefore)
		out[i].DesiredPeersAfter = normalizeUint64Set(out[i].DesiredPeersAfter)
	}
	return out
}

func normalizeAndValidateOnboardingJob(job NodeOnboardingJob, invalid error) (NodeOnboardingJob, error) {
	if job.JobID == "" || job.TargetNodeID == 0 || !validOnboardingJobStatus(job.Status) {
		return NodeOnboardingJob{}, invalid
	}
	job = normalizeOnboardingJob(job)
	if job.Plan.TargetNodeID != job.TargetNodeID {
		return NodeOnboardingJob{}, invalid
	}
	if job.PlanVersion == 0 || job.CreatedAt.IsZero() || job.UpdatedAt.IsZero() {
		return NodeOnboardingJob{}, invalid
	}
	if job.CurrentMoveIndex < -1 || (job.CurrentMoveIndex != -1 && job.CurrentMoveIndex >= len(job.Moves)) {
		return NodeOnboardingJob{}, invalid
	}
	if job.ResultCounts.Pending < 0 || job.ResultCounts.Running < 0 || job.ResultCounts.Completed < 0 || job.ResultCounts.Failed < 0 || job.ResultCounts.Skipped < 0 {
		return NodeOnboardingJob{}, invalid
	}
	if job.Plan.Summary.CurrentTargetSlotCount < 0 || job.Plan.Summary.PlannedTargetSlotCount < 0 || job.Plan.Summary.CurrentTargetLeaderCount < 0 || job.Plan.Summary.PlannedLeaderGain < 0 {
		return NodeOnboardingJob{}, invalid
	}
	for _, move := range job.Plan.Moves {
		if err := validateOnboardingPlanMove(move, invalid); err != nil {
			return NodeOnboardingJob{}, err
		}
	}
	for _, reason := range job.Plan.BlockedReasons {
		if reason.Code == "" || reason.Scope == "" {
			return NodeOnboardingJob{}, invalid
		}
	}
	for _, move := range job.Moves {
		if err := validateOnboardingMove(move, invalid); err != nil {
			return NodeOnboardingJob{}, err
		}
	}
	if job.CurrentTask != nil {
		task := normalizeReconcileTask(*job.CurrentTask)
		if task.SlotID == 0 || !validTaskKind(task.Kind) || !validTaskStep(task.Step) || !validTaskStatus(task.Status) {
			return NodeOnboardingJob{}, invalid
		}
		if err := validateReconcileTaskState(task, invalid); err != nil {
			return NodeOnboardingJob{}, err
		}
		job.CurrentTask = &task
	}
	return job, nil
}

func validateOnboardingPlanMove(move NodeOnboardingPlanMove, invalid error) error {
	if move.SlotID == 0 || move.SourceNodeID == 0 || move.TargetNodeID == 0 || move.SourceNodeID == move.TargetNodeID {
		return invalid
	}
	if err := validateCanonicalPeerSet(move.DesiredPeersBefore, invalid); err != nil {
		return err
	}
	if err := validateCanonicalPeerSet(move.DesiredPeersAfter, invalid); err != nil {
		return err
	}
	return nil
}

func validateOnboardingMove(move NodeOnboardingMove, invalid error) error {
	if move.SlotID == 0 || move.SourceNodeID == 0 || move.TargetNodeID == 0 || move.SourceNodeID == move.TargetNodeID || !validOnboardingMoveStatus(move.Status) {
		return invalid
	}
	if move.TaskKind != TaskKindUnknown && !validTaskKind(move.TaskKind) {
		return invalid
	}
	if move.TaskSlotID != 0 && move.TaskSlotID != move.SlotID {
		return invalid
	}
	if err := validateCanonicalPeerSet(move.DesiredPeersBefore, invalid); err != nil {
		return err
	}
	if err := validateCanonicalPeerSet(move.DesiredPeersAfter, invalid); err != nil {
		return err
	}
	return nil
}

func validOnboardingJobStatus(status OnboardingJobStatus) bool {
	switch status {
	case OnboardingJobStatusPlanned, OnboardingJobStatusRunning, OnboardingJobStatusFailed, OnboardingJobStatusCompleted, OnboardingJobStatusCancelled:
		return true
	default:
		return false
	}
}

func validOnboardingMoveStatus(status OnboardingMoveStatus) bool {
	switch status {
	case OnboardingMoveStatusPending, OnboardingMoveStatusRunning, OnboardingMoveStatusCompleted, OnboardingMoveStatusFailed, OnboardingMoveStatusSkipped:
		return true
	default:
		return false
	}
}

func appendOnboardingPlan(dst []byte, plan NodeOnboardingPlan) []byte {
	dst = binary.BigEndian.AppendUint64(dst, plan.TargetNodeID)
	dst = appendOnboardingPlanSummary(dst, plan.Summary)
	dst = appendOnboardingPlanMoves(dst, plan.Moves)
	dst = appendOnboardingBlockedReasons(dst, plan.BlockedReasons)
	return dst
}

func readOnboardingPlan(src []byte) (NodeOnboardingPlan, []byte, error) {
	if len(src) < 8 {
		return NodeOnboardingPlan{}, nil, ErrCorruptValue
	}
	plan := NodeOnboardingPlan{TargetNodeID: binary.BigEndian.Uint64(src[:8])}
	rest := src[8:]
	var err error
	plan.Summary, rest, err = readOnboardingPlanSummary(rest)
	if err != nil {
		return NodeOnboardingPlan{}, nil, err
	}
	plan.Moves, rest, err = readOnboardingPlanMoves(rest)
	if err != nil {
		return NodeOnboardingPlan{}, nil, err
	}
	plan.BlockedReasons, rest, err = readOnboardingBlockedReasons(rest)
	if err != nil {
		return NodeOnboardingPlan{}, nil, err
	}
	return plan, rest, nil
}

func appendOnboardingPlanSummary(dst []byte, summary NodeOnboardingPlanSummary) []byte {
	dst = appendInt64(dst, int64(summary.CurrentTargetSlotCount))
	dst = appendInt64(dst, int64(summary.PlannedTargetSlotCount))
	dst = appendInt64(dst, int64(summary.CurrentTargetLeaderCount))
	dst = appendInt64(dst, int64(summary.PlannedLeaderGain))
	return dst
}

func readOnboardingPlanSummary(src []byte) (NodeOnboardingPlanSummary, []byte, error) {
	values, rest, err := readInt64Values(src, 4)
	if err != nil {
		return NodeOnboardingPlanSummary{}, nil, err
	}
	return NodeOnboardingPlanSummary{
		CurrentTargetSlotCount:   int(values[0]),
		PlannedTargetSlotCount:   int(values[1]),
		CurrentTargetLeaderCount: int(values[2]),
		PlannedLeaderGain:        int(values[3]),
	}, rest, nil
}

func appendOnboardingPlanMoves(dst []byte, moves []NodeOnboardingPlanMove) []byte {
	dst = binary.AppendUvarint(dst, uint64(len(moves)))
	for _, move := range moves {
		move.DesiredPeersBefore = normalizeUint64Set(move.DesiredPeersBefore)
		move.DesiredPeersAfter = normalizeUint64Set(move.DesiredPeersAfter)
		dst = binary.BigEndian.AppendUint32(dst, move.SlotID)
		dst = binary.BigEndian.AppendUint64(dst, move.SourceNodeID)
		dst = binary.BigEndian.AppendUint64(dst, move.TargetNodeID)
		dst = appendString(dst, move.Reason)
		dst = appendUint64Slice(dst, move.DesiredPeersBefore)
		dst = appendUint64Slice(dst, move.DesiredPeersAfter)
		dst = binary.BigEndian.AppendUint64(dst, move.CurrentLeaderID)
		dst = appendBool(dst, move.LeaderTransferRequired)
	}
	return dst
}

func readOnboardingPlanMoves(src []byte) ([]NodeOnboardingPlanMove, []byte, error) {
	count, n := binary.Uvarint(src)
	if n <= 0 {
		return nil, nil, ErrCorruptValue
	}
	rest := src[n:]
	if count > uint64(len(rest))/32 {
		return nil, nil, ErrCorruptValue
	}
	moves := make([]NodeOnboardingPlanMove, 0, int(count))
	for i := uint64(0); i < count; i++ {
		if len(rest) < 4+8+8 {
			return nil, nil, ErrCorruptValue
		}
		move := NodeOnboardingPlanMove{
			SlotID:       binary.BigEndian.Uint32(rest[:4]),
			SourceNodeID: binary.BigEndian.Uint64(rest[4:12]),
			TargetNodeID: binary.BigEndian.Uint64(rest[12:20]),
		}
		rest = rest[20:]
		var err error
		move.Reason, rest, err = readString(rest)
		if err != nil {
			return nil, nil, ErrCorruptValue
		}
		move.DesiredPeersBefore, rest, err = readUint64Slice(rest)
		if err != nil {
			return nil, nil, ErrCorruptValue
		}
		move.DesiredPeersAfter, rest, err = readUint64Slice(rest)
		if err != nil {
			return nil, nil, ErrCorruptValue
		}
		if len(rest) < 8+1 {
			return nil, nil, ErrCorruptValue
		}
		move.CurrentLeaderID = binary.BigEndian.Uint64(rest[:8])
		rest = rest[8:]
		move.LeaderTransferRequired, rest, err = readBool(rest)
		if err != nil {
			return nil, nil, err
		}
		moves = append(moves, move)
	}
	return moves, rest, nil
}

func appendOnboardingBlockedReasons(dst []byte, reasons []NodeOnboardingBlockedReason) []byte {
	dst = binary.AppendUvarint(dst, uint64(len(reasons)))
	for _, reason := range reasons {
		dst = appendString(dst, reason.Code)
		dst = appendString(dst, reason.Scope)
		dst = binary.BigEndian.AppendUint32(dst, reason.SlotID)
		dst = binary.BigEndian.AppendUint64(dst, reason.NodeID)
		dst = appendString(dst, reason.Message)
	}
	return dst
}

func readOnboardingBlockedReasons(src []byte) ([]NodeOnboardingBlockedReason, []byte, error) {
	count, n := binary.Uvarint(src)
	if n <= 0 {
		return nil, nil, ErrCorruptValue
	}
	rest := src[n:]
	if count > uint64(len(rest))/14 {
		return nil, nil, ErrCorruptValue
	}
	reasons := make([]NodeOnboardingBlockedReason, 0, int(count))
	for i := uint64(0); i < count; i++ {
		code, next, err := readString(rest)
		if err != nil {
			return nil, nil, ErrCorruptValue
		}
		rest = next
		scope, next, err := readString(rest)
		if err != nil {
			return nil, nil, ErrCorruptValue
		}
		rest = next
		if len(rest) < 4+8 {
			return nil, nil, ErrCorruptValue
		}
		reason := NodeOnboardingBlockedReason{
			Code:   code,
			Scope:  scope,
			SlotID: binary.BigEndian.Uint32(rest[:4]),
			NodeID: binary.BigEndian.Uint64(rest[4:12]),
		}
		rest = rest[12:]
		reason.Message, rest, err = readString(rest)
		if err != nil {
			return nil, nil, ErrCorruptValue
		}
		reasons = append(reasons, reason)
	}
	return reasons, rest, nil
}

func appendOnboardingMoves(dst []byte, moves []NodeOnboardingMove) []byte {
	dst = binary.AppendUvarint(dst, uint64(len(moves)))
	for _, move := range moves {
		move.DesiredPeersBefore = normalizeUint64Set(move.DesiredPeersBefore)
		move.DesiredPeersAfter = normalizeUint64Set(move.DesiredPeersAfter)
		dst = binary.BigEndian.AppendUint32(dst, move.SlotID)
		dst = binary.BigEndian.AppendUint64(dst, move.SourceNodeID)
		dst = binary.BigEndian.AppendUint64(dst, move.TargetNodeID)
		dst = appendString(dst, string(move.Status))
		dst = append(dst, byte(move.TaskKind))
		dst = binary.BigEndian.AppendUint32(dst, move.TaskSlotID)
		dst = appendTime(dst, move.StartedAt)
		dst = appendTime(dst, move.CompletedAt)
		dst = appendString(dst, move.LastError)
		dst = appendUint64Slice(dst, move.DesiredPeersBefore)
		dst = appendUint64Slice(dst, move.DesiredPeersAfter)
		dst = binary.BigEndian.AppendUint64(dst, move.LeaderBefore)
		dst = binary.BigEndian.AppendUint64(dst, move.LeaderAfter)
		dst = appendBool(dst, move.LeaderTransferRequired)
	}
	return dst
}

func readOnboardingMoves(src []byte) ([]NodeOnboardingMove, []byte, error) {
	count, n := binary.Uvarint(src)
	if n <= 0 {
		return nil, nil, ErrCorruptValue
	}
	rest := src[n:]
	if count > uint64(len(rest))/62 {
		return nil, nil, ErrCorruptValue
	}
	moves := make([]NodeOnboardingMove, 0, int(count))
	for i := uint64(0); i < count; i++ {
		if len(rest) < 4+8+8 {
			return nil, nil, ErrCorruptValue
		}
		move := NodeOnboardingMove{
			SlotID:       binary.BigEndian.Uint32(rest[:4]),
			SourceNodeID: binary.BigEndian.Uint64(rest[4:12]),
			TargetNodeID: binary.BigEndian.Uint64(rest[12:20]),
		}
		rest = rest[20:]
		status, next, err := readString(rest)
		if err != nil {
			return nil, nil, ErrCorruptValue
		}
		move.Status = OnboardingMoveStatus(status)
		rest = next
		if len(rest) < 1+4 {
			return nil, nil, ErrCorruptValue
		}
		move.TaskKind = TaskKind(rest[0])
		move.TaskSlotID = binary.BigEndian.Uint32(rest[1:5])
		rest = rest[5:]
		move.StartedAt, rest, err = readTime(rest)
		if err != nil {
			return nil, nil, ErrCorruptValue
		}
		move.CompletedAt, rest, err = readTime(rest)
		if err != nil {
			return nil, nil, ErrCorruptValue
		}
		move.LastError, rest, err = readString(rest)
		if err != nil {
			return nil, nil, ErrCorruptValue
		}
		move.DesiredPeersBefore, rest, err = readUint64Slice(rest)
		if err != nil {
			return nil, nil, ErrCorruptValue
		}
		move.DesiredPeersAfter, rest, err = readUint64Slice(rest)
		if err != nil {
			return nil, nil, ErrCorruptValue
		}
		if len(rest) < 8+8+1 {
			return nil, nil, ErrCorruptValue
		}
		move.LeaderBefore = binary.BigEndian.Uint64(rest[:8])
		move.LeaderAfter = binary.BigEndian.Uint64(rest[8:16])
		rest = rest[16:]
		move.LeaderTransferRequired, rest, err = readBool(rest)
		if err != nil {
			return nil, nil, err
		}
		moves = append(moves, move)
	}
	return moves, rest, nil
}

func appendOnboardingResultCounts(dst []byte, counts OnboardingResultCounts) []byte {
	dst = appendInt64(dst, int64(counts.Pending))
	dst = appendInt64(dst, int64(counts.Running))
	dst = appendInt64(dst, int64(counts.Completed))
	dst = appendInt64(dst, int64(counts.Failed))
	dst = appendInt64(dst, int64(counts.Skipped))
	return dst
}

func readOnboardingResultCounts(src []byte) (OnboardingResultCounts, []byte, error) {
	values, rest, err := readInt64Values(src, 5)
	if err != nil {
		return OnboardingResultCounts{}, nil, err
	}
	return OnboardingResultCounts{
		Pending:   int(values[0]),
		Running:   int(values[1]),
		Completed: int(values[2]),
		Failed:    int(values[3]),
		Skipped:   int(values[4]),
	}, rest, nil
}

func appendReconcileTaskPtr(dst []byte, task *ReconcileTask) []byte {
	if task == nil {
		return appendBool(dst, false)
	}
	normalized := normalizeReconcileTask(*task)
	dst = appendBool(dst, true)
	return appendInlineReconcileTask(dst, normalized)
}

func readReconcileTaskPtr(src []byte) (*ReconcileTask, []byte, error) {
	present, rest, err := readBool(src)
	if err != nil {
		return nil, nil, err
	}
	if !present {
		return nil, rest, nil
	}
	value, rest, err := readBytes(rest)
	if err != nil {
		return nil, nil, ErrCorruptValue
	}
	return decodeInlineReconcileTask(value, rest)
}

func decodeInlineReconcileTask(value, rest []byte) (*ReconcileTask, []byte, error) {
	// Current tasks are encoded with the regular task codec payload, which omits SlotID from the value.
	// Store the SlotID in a short wrapper before the payload to preserve the full task identity.
	if len(value) < 4 {
		return nil, nil, ErrCorruptValue
	}
	slotID := binary.BigEndian.Uint32(value[:4])
	task, err := decodeReconcileTask(encodeGroupKey(recordPrefixTask, slotID), value[4:])
	if err != nil {
		return nil, nil, err
	}
	return &task, rest, nil
}

func appendInlineReconcileTask(dst []byte, task ReconcileTask) []byte {
	value := make([]byte, 0, 4+64)
	value = binary.BigEndian.AppendUint32(value, task.SlotID)
	value = append(value, encodeReconcileTask(task)...)
	return appendBytes(dst, value)
}

func appendTime(dst []byte, value time.Time) []byte {
	if value.IsZero() {
		return appendInt64(dst, 0)
	}
	return appendInt64(dst, value.UnixNano())
}

func readTime(src []byte) (time.Time, []byte, error) {
	value, rest, err := readInt64(src)
	if err != nil {
		return time.Time{}, nil, err
	}
	if value == 0 {
		return time.Time{}, rest, nil
	}
	return time.Unix(0, value).UTC(), rest, nil
}

func appendBool(dst []byte, value bool) []byte {
	if value {
		return append(dst, 1)
	}
	return append(dst, 0)
}

func readBool(src []byte) (bool, []byte, error) {
	if len(src) < 1 {
		return false, nil, ErrCorruptValue
	}
	switch src[0] {
	case 0:
		return false, src[1:], nil
	case 1:
		return true, src[1:], nil
	default:
		return false, nil, ErrCorruptValue
	}
}

func appendBytes(dst, value []byte) []byte {
	dst = binary.AppendUvarint(dst, uint64(len(value)))
	return append(dst, value...)
}

func readInt64Values(src []byte, count int) ([]int64, []byte, error) {
	if len(src) < count*8 {
		return nil, nil, ErrCorruptValue
	}
	values := make([]int64, count)
	rest := src
	for i := 0; i < count; i++ {
		values[i] = int64(binary.BigEndian.Uint64(rest[:8]))
		rest = rest[8:]
	}
	return values, rest, nil
}

func sortOnboardingJobsByID(jobs []NodeOnboardingJob) {
	sort.Slice(jobs, func(i, j int) bool {
		return jobs[i].JobID < jobs[j].JobID
	})
}
