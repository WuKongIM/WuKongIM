package raft

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"time"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/legacy/controller/meta"
	slotcontroller "github.com/WuKongIM/WuKongIM/pkg/legacy/controller/plane"
)

const (
	commandEnvelopeBinaryVersion byte = 1

	commandFieldReport uint64 = 1 << iota
	commandFieldOp
	commandFieldAdvance
	commandFieldAssignment
	commandFieldTask
	commandFieldMigration
	commandFieldAddSlot
	commandFieldRemoveSlot
	commandFieldNodeStatusUpdate
	commandFieldNodeJoin
	commandFieldNodeJoinActivate
	commandFieldNodeOnboarding
)

var (
	commandEnvelopeBinaryMagic = [...]byte{'W', 'K', 'R', 'C'}
	errCommandEnvelopeCorrupt  = errors.New("controllerraft: corrupt command envelope")
)

const commandKnownFields = commandFieldReport |
	commandFieldOp |
	commandFieldAdvance |
	commandFieldAssignment |
	commandFieldTask |
	commandFieldMigration |
	commandFieldAddSlot |
	commandFieldRemoveSlot |
	commandFieldNodeStatusUpdate |
	commandFieldNodeJoin |
	commandFieldNodeJoinActivate |
	commandFieldNodeOnboarding

func encodeCommandBinary(cmd slotcontroller.Command) ([]byte, error) {
	data := make([]byte, 0, 1024)
	data = append(data, commandEnvelopeBinaryMagic[:]...)
	data = append(data, commandEnvelopeBinaryVersion, byte(cmd.Kind))
	data = binary.AppendUvarint(data, commandFieldMask(cmd))

	if cmd.Report != nil {
		data = appendCommandAgentReport(data, *cmd.Report)
	}
	if cmd.Op != nil {
		data = append(data, byte(cmd.Op.Kind))
		data = binary.BigEndian.AppendUint64(data, cmd.Op.NodeID)
	}
	if cmd.Advance != nil {
		data = binary.BigEndian.AppendUint32(data, cmd.Advance.SlotID)
		data = binary.BigEndian.AppendUint32(data, cmd.Advance.Attempt)
		data = appendCommandTime(data, cmd.Advance.Now)
		if cmd.Advance.Err == nil {
			data = appendCommandString(data, "")
		} else {
			data = appendCommandString(data, cmd.Advance.Err.Error())
		}
	}
	if cmd.Assignment != nil {
		data = appendCommandMetaAssignment(data, *cmd.Assignment)
	}
	if cmd.Task != nil {
		data = appendCommandMetaTask(data, *cmd.Task)
	}
	if cmd.Migration != nil {
		data = binary.BigEndian.AppendUint16(data, cmd.Migration.HashSlot)
		data = binary.BigEndian.AppendUint64(data, cmd.Migration.Source)
		data = binary.BigEndian.AppendUint64(data, cmd.Migration.Target)
		data = append(data, cmd.Migration.Phase)
	}
	if cmd.AddSlot != nil {
		data = binary.BigEndian.AppendUint64(data, cmd.AddSlot.NewSlotID)
		data = appendCommandUint64Slice(data, cmd.AddSlot.Peers)
		data = binary.BigEndian.AppendUint64(data, cmd.AddSlot.PreferredLeader)
	}
	if cmd.RemoveSlot != nil {
		data = binary.BigEndian.AppendUint64(data, cmd.RemoveSlot.SlotID)
	}
	if cmd.NodeStatusUpdate != nil {
		data = appendCommandNodeStatusUpdate(data, *cmd.NodeStatusUpdate)
	}
	if cmd.NodeJoin != nil {
		data = appendCommandNodeJoin(data, *cmd.NodeJoin)
	}
	if cmd.NodeJoinActivate != nil {
		data = binary.BigEndian.AppendUint64(data, cmd.NodeJoinActivate.NodeID)
		data = appendCommandTime(data, cmd.NodeJoinActivate.ActivatedAt)
	}
	if cmd.NodeOnboarding != nil {
		data = appendCommandMetaNodeOnboardingUpdate(data, *cmd.NodeOnboarding)
	}
	return data, nil
}

func encodeCommandEnvelopeBinary(envelope commandEnvelope) ([]byte, error) {
	data := make([]byte, 0, 128)
	data = append(data, commandEnvelopeBinaryMagic[:]...)
	data = append(data, commandEnvelopeBinaryVersion, byte(envelope.Kind))
	data = binary.AppendUvarint(data, commandEnvelopeFieldMask(envelope))

	if envelope.Report != nil {
		data = appendCommandAgentReport(data, *envelope.Report)
	}
	if envelope.Op != nil {
		data = append(data, byte(envelope.Op.Kind))
		data = binary.BigEndian.AppendUint64(data, envelope.Op.NodeID)
	}
	if envelope.Advance != nil {
		data = binary.BigEndian.AppendUint32(data, envelope.Advance.SlotID)
		data = binary.BigEndian.AppendUint32(data, envelope.Advance.Attempt)
		data = appendCommandTime(data, envelope.Advance.Now)
		data = appendCommandString(data, envelope.Advance.Err)
	}
	if envelope.Assignment != nil {
		data = appendCommandAssignment(data, *envelope.Assignment)
	}
	if envelope.Task != nil {
		data = appendCommandTask(data, *envelope.Task)
	}
	if envelope.Migration != nil {
		data = binary.BigEndian.AppendUint16(data, envelope.Migration.HashSlot)
		data = binary.BigEndian.AppendUint64(data, envelope.Migration.Source)
		data = binary.BigEndian.AppendUint64(data, envelope.Migration.Target)
		data = append(data, envelope.Migration.Phase)
	}
	if envelope.AddSlot != nil {
		data = binary.BigEndian.AppendUint64(data, envelope.AddSlot.NewSlotID)
		data = appendCommandUint64Slice(data, envelope.AddSlot.Peers)
		data = binary.BigEndian.AppendUint64(data, envelope.AddSlot.PreferredLeader)
	}
	if envelope.RemoveSlot != nil {
		data = binary.BigEndian.AppendUint64(data, envelope.RemoveSlot.SlotID)
	}
	if envelope.NodeStatusUpdate != nil {
		data = appendCommandNodeStatusUpdate(data, *envelope.NodeStatusUpdate)
	}
	if envelope.NodeJoin != nil {
		data = appendCommandNodeJoin(data, *envelope.NodeJoin)
	}
	if envelope.NodeJoinActivate != nil {
		data = binary.BigEndian.AppendUint64(data, envelope.NodeJoinActivate.NodeID)
		data = appendCommandTime(data, envelope.NodeJoinActivate.ActivatedAt)
	}
	if envelope.NodeOnboarding != nil {
		data = appendCommandNodeOnboardingUpdate(data, *envelope.NodeOnboarding)
	}
	return data, nil
}

func commandFieldMask(cmd slotcontroller.Command) uint64 {
	var mask uint64
	if cmd.Report != nil {
		mask |= commandFieldReport
	}
	if cmd.Op != nil {
		mask |= commandFieldOp
	}
	if cmd.Advance != nil {
		mask |= commandFieldAdvance
	}
	if cmd.Assignment != nil {
		mask |= commandFieldAssignment
	}
	if cmd.Task != nil {
		mask |= commandFieldTask
	}
	if cmd.Migration != nil {
		mask |= commandFieldMigration
	}
	if cmd.AddSlot != nil {
		mask |= commandFieldAddSlot
	}
	if cmd.RemoveSlot != nil {
		mask |= commandFieldRemoveSlot
	}
	if cmd.NodeStatusUpdate != nil {
		mask |= commandFieldNodeStatusUpdate
	}
	if cmd.NodeJoin != nil {
		mask |= commandFieldNodeJoin
	}
	if cmd.NodeJoinActivate != nil {
		mask |= commandFieldNodeJoinActivate
	}
	if cmd.NodeOnboarding != nil {
		mask |= commandFieldNodeOnboarding
	}
	return mask
}

func decodeCommandEnvelopeBinary(data []byte) (commandEnvelope, error) {
	if !hasCommandEnvelopeBinaryMagic(data) {
		return decodeCommandEnvelopeLegacyJSON(data)
	}

	reader := commandEnvelopeReader{rest: data[len(commandEnvelopeBinaryMagic):]}
	version, err := reader.readByte()
	if err != nil {
		return commandEnvelope{}, err
	}
	if version != commandEnvelopeBinaryVersion {
		return commandEnvelope{}, errCommandEnvelopeCorrupt
	}
	kind, err := reader.readByte()
	if err != nil {
		return commandEnvelope{}, err
	}
	mask, err := reader.readUvarint()
	if err != nil {
		return commandEnvelope{}, err
	}
	if mask&^commandKnownFields != 0 {
		return commandEnvelope{}, errCommandEnvelopeCorrupt
	}

	envelope := commandEnvelope{Kind: slotcontroller.CommandKind(kind)}
	if mask&commandFieldReport != 0 {
		envelope.Report, err = reader.readAgentReport()
		if err != nil {
			return commandEnvelope{}, err
		}
	}
	if mask&commandFieldOp != 0 {
		opKind, err := reader.readByte()
		if err != nil {
			return commandEnvelope{}, err
		}
		nodeID, err := reader.readUint64()
		if err != nil {
			return commandEnvelope{}, err
		}
		envelope.Op = &slotcontroller.OperatorRequest{Kind: slotcontroller.OperatorKind(opKind), NodeID: nodeID}
	}
	if mask&commandFieldAdvance != 0 {
		advance, err := reader.readTaskAdvance()
		if err != nil {
			return commandEnvelope{}, err
		}
		envelope.Advance = advance
	}
	if mask&commandFieldAssignment != 0 {
		envelope.Assignment, err = reader.readAssignment()
		if err != nil {
			return commandEnvelope{}, err
		}
	}
	if mask&commandFieldTask != 0 {
		envelope.Task, err = reader.readTask()
		if err != nil {
			return commandEnvelope{}, err
		}
	}
	if mask&commandFieldMigration != 0 {
		envelope.Migration, err = reader.readMigrationRequest()
		if err != nil {
			return commandEnvelope{}, err
		}
	}
	if mask&commandFieldAddSlot != 0 {
		envelope.AddSlot, err = reader.readAddSlotRequest()
		if err != nil {
			return commandEnvelope{}, err
		}
	}
	if mask&commandFieldRemoveSlot != 0 {
		slotID, err := reader.readUint64()
		if err != nil {
			return commandEnvelope{}, err
		}
		envelope.RemoveSlot = &slotcontroller.RemoveSlotRequest{SlotID: slotID}
	}
	if mask&commandFieldNodeStatusUpdate != 0 {
		envelope.NodeStatusUpdate, err = reader.readNodeStatusUpdate()
		if err != nil {
			return commandEnvelope{}, err
		}
	}
	if mask&commandFieldNodeJoin != 0 {
		envelope.NodeJoin, err = reader.readNodeJoinRequest()
		if err != nil {
			return commandEnvelope{}, err
		}
	}
	if mask&commandFieldNodeJoinActivate != 0 {
		nodeID, err := reader.readUint64()
		if err != nil {
			return commandEnvelope{}, err
		}
		activatedAt, err := reader.readTime()
		if err != nil {
			return commandEnvelope{}, err
		}
		envelope.NodeJoinActivate = &slotcontroller.NodeJoinActivateRequest{NodeID: nodeID, ActivatedAt: activatedAt}
	}
	if mask&commandFieldNodeOnboarding != 0 {
		envelope.NodeOnboarding, err = reader.readNodeOnboardingUpdate()
		if err != nil {
			return commandEnvelope{}, err
		}
	}
	if len(reader.rest) != 0 {
		return commandEnvelope{}, errCommandEnvelopeCorrupt
	}
	return envelope, nil
}

func decodeCommandBinary(data []byte) (slotcontroller.Command, error) {
	reader := commandEnvelopeReader{rest: data[len(commandEnvelopeBinaryMagic):]}
	version, err := reader.readByte()
	if err != nil {
		return slotcontroller.Command{}, err
	}
	if version != commandEnvelopeBinaryVersion {
		return slotcontroller.Command{}, errCommandEnvelopeCorrupt
	}
	kind, err := reader.readByte()
	if err != nil {
		return slotcontroller.Command{}, err
	}
	mask, err := reader.readUvarint()
	if err != nil {
		return slotcontroller.Command{}, err
	}
	if mask&^commandKnownFields != 0 {
		return slotcontroller.Command{}, errCommandEnvelopeCorrupt
	}

	cmd := slotcontroller.Command{Kind: slotcontroller.CommandKind(kind)}
	if mask&commandFieldReport != 0 {
		cmd.Report, err = reader.readAgentReport()
		if err != nil {
			return slotcontroller.Command{}, err
		}
	}
	if mask&commandFieldOp != 0 {
		opKind, err := reader.readByte()
		if err != nil {
			return slotcontroller.Command{}, err
		}
		nodeID, err := reader.readUint64()
		if err != nil {
			return slotcontroller.Command{}, err
		}
		cmd.Op = &slotcontroller.OperatorRequest{Kind: slotcontroller.OperatorKind(opKind), NodeID: nodeID}
	}
	if mask&commandFieldAdvance != 0 {
		cmd.Advance, err = reader.readCommandTaskAdvance()
		if err != nil {
			return slotcontroller.Command{}, err
		}
	}
	if mask&commandFieldAssignment != 0 {
		cmd.Assignment, err = reader.readMetaAssignment()
		if err != nil {
			return slotcontroller.Command{}, err
		}
	}
	if mask&commandFieldTask != 0 {
		cmd.Task, err = reader.readMetaTask()
		if err != nil {
			return slotcontroller.Command{}, err
		}
	}
	if mask&commandFieldMigration != 0 {
		cmd.Migration, err = reader.readMigrationRequest()
		if err != nil {
			return slotcontroller.Command{}, err
		}
	}
	if mask&commandFieldAddSlot != 0 {
		cmd.AddSlot, err = reader.readAddSlotRequest()
		if err != nil {
			return slotcontroller.Command{}, err
		}
	}
	if mask&commandFieldRemoveSlot != 0 {
		slotID, err := reader.readUint64()
		if err != nil {
			return slotcontroller.Command{}, err
		}
		cmd.RemoveSlot = &slotcontroller.RemoveSlotRequest{SlotID: slotID}
	}
	if mask&commandFieldNodeStatusUpdate != 0 {
		cmd.NodeStatusUpdate, err = reader.readNodeStatusUpdate()
		if err != nil {
			return slotcontroller.Command{}, err
		}
	}
	if mask&commandFieldNodeJoin != 0 {
		cmd.NodeJoin, err = reader.readNodeJoinRequest()
		if err != nil {
			return slotcontroller.Command{}, err
		}
	}
	if mask&commandFieldNodeJoinActivate != 0 {
		nodeID, err := reader.readUint64()
		if err != nil {
			return slotcontroller.Command{}, err
		}
		activatedAt, err := reader.readTime()
		if err != nil {
			return slotcontroller.Command{}, err
		}
		cmd.NodeJoinActivate = &slotcontroller.NodeJoinActivateRequest{NodeID: nodeID, ActivatedAt: activatedAt}
	}
	if mask&commandFieldNodeOnboarding != 0 {
		cmd.NodeOnboarding, err = reader.readMetaNodeOnboardingUpdate()
		if err != nil {
			return slotcontroller.Command{}, err
		}
	}
	if len(reader.rest) != 0 {
		return slotcontroller.Command{}, errCommandEnvelopeCorrupt
	}
	return cmd, nil
}

func commandEnvelopeFieldMask(envelope commandEnvelope) uint64 {
	var mask uint64
	if envelope.Report != nil {
		mask |= commandFieldReport
	}
	if envelope.Op != nil {
		mask |= commandFieldOp
	}
	if envelope.Advance != nil {
		mask |= commandFieldAdvance
	}
	if envelope.Assignment != nil {
		mask |= commandFieldAssignment
	}
	if envelope.Task != nil {
		mask |= commandFieldTask
	}
	if envelope.Migration != nil {
		mask |= commandFieldMigration
	}
	if envelope.AddSlot != nil {
		mask |= commandFieldAddSlot
	}
	if envelope.RemoveSlot != nil {
		mask |= commandFieldRemoveSlot
	}
	if envelope.NodeStatusUpdate != nil {
		mask |= commandFieldNodeStatusUpdate
	}
	if envelope.NodeJoin != nil {
		mask |= commandFieldNodeJoin
	}
	if envelope.NodeJoinActivate != nil {
		mask |= commandFieldNodeJoinActivate
	}
	if envelope.NodeOnboarding != nil {
		mask |= commandFieldNodeOnboarding
	}
	return mask
}

func hasCommandEnvelopeBinaryMagic(data []byte) bool {
	if len(data) < len(commandEnvelopeBinaryMagic) {
		return false
	}
	for i, value := range commandEnvelopeBinaryMagic {
		if data[i] != value {
			return false
		}
	}
	return true
}

func decodeCommandEnvelopeLegacyJSON(data []byte) (commandEnvelope, error) {
	var envelope commandEnvelope
	if len(data) == 0 || data[0] != '{' {
		return commandEnvelope{}, errCommandEnvelopeCorrupt
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return commandEnvelope{}, err
	}
	return envelope, nil
}

func appendCommandAgentReport(dst []byte, report slotcontroller.AgentReport) []byte {
	dst = binary.BigEndian.AppendUint64(dst, report.NodeID)
	dst = appendCommandString(dst, report.Addr)
	dst = appendCommandTime(dst, report.ObservedAt)
	dst = appendCommandInt64(dst, int64(report.CapacityWeight))
	dst = binary.BigEndian.AppendUint64(dst, report.HashSlotTableVersion)
	dst = appendCommandRuntimeViewPtr(dst, report.Runtime)
	return dst
}

func appendCommandRuntimeViewPtr(dst []byte, view *controllermeta.SlotRuntimeView) []byte {
	if view == nil {
		return appendCommandBool(dst, false)
	}
	dst = appendCommandBool(dst, true)
	return appendCommandRuntimeView(dst, *view)
}

func appendCommandRuntimeView(dst []byte, view controllermeta.SlotRuntimeView) []byte {
	dst = binary.BigEndian.AppendUint32(dst, view.SlotID)
	dst = appendCommandUint64Slice(dst, view.CurrentPeers)
	dst = appendCommandUint64Slice(dst, view.CurrentVoters)
	dst = binary.BigEndian.AppendUint64(dst, view.LeaderID)
	dst = binary.BigEndian.AppendUint32(dst, view.HealthyVoters)
	dst = appendCommandBool(dst, view.HasQuorum)
	dst = binary.BigEndian.AppendUint64(dst, view.ObservedConfigEpoch)
	dst = appendCommandTime(dst, view.LastReportAt)
	return dst
}

func appendCommandAssignmentPtr(dst []byte, assignment *slotcontrollerAssignmentEnvelope) []byte {
	if assignment == nil {
		return appendCommandBool(dst, false)
	}
	dst = appendCommandBool(dst, true)
	return appendCommandAssignment(dst, *assignment)
}

func appendCommandAssignment(dst []byte, assignment slotcontrollerAssignmentEnvelope) []byte {
	dst = binary.BigEndian.AppendUint32(dst, assignment.SlotID)
	dst = appendCommandUint64Slice(dst, assignment.DesiredPeers)
	dst = binary.BigEndian.AppendUint64(dst, assignment.ConfigEpoch)
	dst = binary.BigEndian.AppendUint64(dst, assignment.BalanceVersion)
	dst = binary.BigEndian.AppendUint64(dst, assignment.PreferredLeader)
	dst = appendCommandTime(dst, assignment.LeaderTransferCooldownUntil)
	return dst
}

func appendCommandTaskPtr(dst []byte, task *slotcontrollerReconcileTaskEnvelope) []byte {
	if task == nil {
		return appendCommandBool(dst, false)
	}
	dst = appendCommandBool(dst, true)
	return appendCommandTask(dst, *task)
}

func appendCommandTask(dst []byte, task slotcontrollerReconcileTaskEnvelope) []byte {
	dst = binary.BigEndian.AppendUint32(dst, task.SlotID)
	dst = append(dst, byte(task.Kind), byte(task.Step))
	dst = binary.BigEndian.AppendUint64(dst, task.SourceNode)
	dst = binary.BigEndian.AppendUint64(dst, task.TargetNode)
	dst = binary.BigEndian.AppendUint32(dst, task.Attempt)
	dst = appendCommandTime(dst, task.NextRunAt)
	dst = append(dst, byte(task.Status))
	dst = appendCommandString(dst, task.LastError)
	return dst
}

func appendCommandNodeStatusUpdate(dst []byte, update slotcontroller.NodeStatusUpdate) []byte {
	dst = binary.AppendUvarint(dst, uint64(len(update.Transitions)))
	for _, transition := range update.Transitions {
		dst = binary.BigEndian.AppendUint64(dst, transition.NodeID)
		dst = append(dst, byte(transition.NewStatus))
		if transition.ExpectedStatus == nil {
			dst = appendCommandBool(dst, false)
		} else {
			dst = appendCommandBool(dst, true)
			dst = append(dst, byte(*transition.ExpectedStatus))
		}
		dst = appendCommandTime(dst, transition.EvaluatedAt)
		dst = appendCommandString(dst, transition.Addr)
		dst = appendCommandInt64(dst, int64(transition.CapacityWeight))
	}
	return dst
}

func appendCommandNodeJoin(dst []byte, req slotcontroller.NodeJoinRequest) []byte {
	dst = binary.BigEndian.AppendUint64(dst, req.NodeID)
	dst = appendCommandString(dst, req.Name)
	dst = appendCommandString(dst, req.Addr)
	dst = appendCommandInt64(dst, int64(req.CapacityWeight))
	dst = appendCommandTime(dst, req.JoinedAt)
	return dst
}

func appendCommandMetaAssignment(dst []byte, assignment controllermeta.SlotAssignment) []byte {
	dst = binary.BigEndian.AppendUint32(dst, assignment.SlotID)
	dst = appendCommandUint64Slice(dst, assignment.DesiredPeers)
	dst = binary.BigEndian.AppendUint64(dst, assignment.ConfigEpoch)
	dst = binary.BigEndian.AppendUint64(dst, assignment.BalanceVersion)
	dst = binary.BigEndian.AppendUint64(dst, assignment.PreferredLeader)
	dst = appendCommandTime(dst, assignment.LeaderTransferCooldownUntil)
	return dst
}

func appendCommandMetaAssignmentPtr(dst []byte, assignment *controllermeta.SlotAssignment) []byte {
	if assignment == nil {
		return appendCommandBool(dst, false)
	}
	dst = appendCommandBool(dst, true)
	return appendCommandMetaAssignment(dst, *assignment)
}

func appendCommandMetaTask(dst []byte, task controllermeta.ReconcileTask) []byte {
	dst = binary.BigEndian.AppendUint32(dst, task.SlotID)
	dst = append(dst, byte(task.Kind), byte(task.Step))
	dst = binary.BigEndian.AppendUint64(dst, task.SourceNode)
	dst = binary.BigEndian.AppendUint64(dst, task.TargetNode)
	dst = binary.BigEndian.AppendUint32(dst, task.Attempt)
	dst = appendCommandTime(dst, task.NextRunAt)
	dst = append(dst, byte(task.Status))
	dst = appendCommandString(dst, task.LastError)
	return dst
}

func appendCommandMetaTaskPtr(dst []byte, task *controllermeta.ReconcileTask) []byte {
	if task == nil {
		return appendCommandBool(dst, false)
	}
	dst = appendCommandBool(dst, true)
	return appendCommandMetaTask(dst, *task)
}

func appendCommandMetaNodeOnboardingUpdate(dst []byte, update slotcontroller.NodeOnboardingJobUpdate) []byte {
	dst = appendCommandMetaNodeOnboardingJobPtr(dst, update.Job)
	dst = appendCommandOnboardingJobStatusPtr(dst, update.ExpectedStatus)
	dst = appendCommandMetaAssignmentPtr(dst, update.Assignment)
	dst = appendCommandMetaTaskPtr(dst, update.Task)
	return dst
}

func appendCommandMetaNodeOnboardingJobPtr(dst []byte, job *controllermeta.NodeOnboardingJob) []byte {
	if job == nil {
		return appendCommandBool(dst, false)
	}
	dst = appendCommandBool(dst, true)
	return appendCommandMetaNodeOnboardingJob(dst, *job)
}

func appendCommandMetaNodeOnboardingJob(dst []byte, job controllermeta.NodeOnboardingJob) []byte {
	dst = appendCommandString(dst, job.JobID)
	dst = binary.BigEndian.AppendUint64(dst, job.TargetNodeID)
	dst = appendCommandString(dst, job.RetryOfJobID)
	dst = appendCommandString(dst, string(job.Status))
	dst = appendCommandTime(dst, job.CreatedAt)
	dst = appendCommandTime(dst, job.UpdatedAt)
	dst = appendCommandTime(dst, job.StartedAt)
	dst = appendCommandTime(dst, job.CompletedAt)
	dst = binary.BigEndian.AppendUint32(dst, job.PlanVersion)
	dst = appendCommandString(dst, job.PlanFingerprint)
	dst = appendCommandMetaNodeOnboardingPlan(dst, job.Plan)
	dst = appendCommandMetaNodeOnboardingMoves(dst, job.Moves)
	dst = appendCommandInt64(dst, int64(job.CurrentMoveIndex))
	dst = appendCommandMetaNodeOnboardingResultCounts(dst, job.ResultCounts)
	dst = appendCommandMetaTaskPtr(dst, job.CurrentTask)
	dst = appendCommandString(dst, job.LastError)
	return dst
}

func appendCommandMetaNodeOnboardingPlan(dst []byte, plan controllermeta.NodeOnboardingPlan) []byte {
	dst = binary.BigEndian.AppendUint64(dst, plan.TargetNodeID)
	dst = appendCommandMetaNodeOnboardingPlanSummary(dst, plan.Summary)
	dst = appendCommandMetaNodeOnboardingPlanMoves(dst, plan.Moves)
	dst = appendCommandMetaNodeOnboardingBlockedReasons(dst, plan.BlockedReasons)
	return dst
}

func appendCommandMetaNodeOnboardingPlanSummary(dst []byte, summary controllermeta.NodeOnboardingPlanSummary) []byte {
	dst = appendCommandInt64(dst, int64(summary.CurrentTargetSlotCount))
	dst = appendCommandInt64(dst, int64(summary.PlannedTargetSlotCount))
	dst = appendCommandInt64(dst, int64(summary.CurrentTargetLeaderCount))
	dst = appendCommandInt64(dst, int64(summary.PlannedLeaderGain))
	return dst
}

func appendCommandMetaNodeOnboardingPlanMoves(dst []byte, moves []controllermeta.NodeOnboardingPlanMove) []byte {
	dst = binary.AppendUvarint(dst, uint64(len(moves)))
	for _, move := range moves {
		dst = binary.BigEndian.AppendUint32(dst, move.SlotID)
		dst = binary.BigEndian.AppendUint64(dst, move.SourceNodeID)
		dst = binary.BigEndian.AppendUint64(dst, move.TargetNodeID)
		dst = appendCommandString(dst, move.Reason)
		dst = appendCommandUint64Slice(dst, move.DesiredPeersBefore)
		dst = appendCommandUint64Slice(dst, move.DesiredPeersAfter)
		dst = binary.BigEndian.AppendUint64(dst, move.CurrentLeaderID)
		dst = appendCommandBool(dst, move.LeaderTransferRequired)
	}
	return dst
}

func appendCommandMetaNodeOnboardingBlockedReasons(dst []byte, reasons []controllermeta.NodeOnboardingBlockedReason) []byte {
	dst = binary.AppendUvarint(dst, uint64(len(reasons)))
	for _, reason := range reasons {
		dst = appendCommandString(dst, reason.Code)
		dst = appendCommandString(dst, reason.Scope)
		dst = binary.BigEndian.AppendUint32(dst, reason.SlotID)
		dst = binary.BigEndian.AppendUint64(dst, reason.NodeID)
		dst = appendCommandString(dst, reason.Message)
	}
	return dst
}

func appendCommandMetaNodeOnboardingMoves(dst []byte, moves []controllermeta.NodeOnboardingMove) []byte {
	dst = binary.AppendUvarint(dst, uint64(len(moves)))
	for _, move := range moves {
		dst = binary.BigEndian.AppendUint32(dst, move.SlotID)
		dst = binary.BigEndian.AppendUint64(dst, move.SourceNodeID)
		dst = binary.BigEndian.AppendUint64(dst, move.TargetNodeID)
		dst = appendCommandString(dst, string(move.Status))
		dst = append(dst, byte(move.TaskKind))
		dst = binary.BigEndian.AppendUint32(dst, move.TaskSlotID)
		dst = appendCommandTime(dst, move.StartedAt)
		dst = appendCommandTime(dst, move.CompletedAt)
		dst = appendCommandString(dst, move.LastError)
		dst = appendCommandUint64Slice(dst, move.DesiredPeersBefore)
		dst = appendCommandUint64Slice(dst, move.DesiredPeersAfter)
		dst = binary.BigEndian.AppendUint64(dst, move.LeaderBefore)
		dst = binary.BigEndian.AppendUint64(dst, move.LeaderAfter)
		dst = appendCommandBool(dst, move.LeaderTransferRequired)
	}
	return dst
}

func appendCommandMetaNodeOnboardingResultCounts(dst []byte, counts controllermeta.OnboardingResultCounts) []byte {
	dst = appendCommandInt64(dst, int64(counts.Pending))
	dst = appendCommandInt64(dst, int64(counts.Running))
	dst = appendCommandInt64(dst, int64(counts.Completed))
	dst = appendCommandInt64(dst, int64(counts.Failed))
	dst = appendCommandInt64(dst, int64(counts.Skipped))
	return dst
}

func appendCommandNodeOnboardingUpdate(dst []byte, update nodeOnboardingJobUpdateEnvelope) []byte {
	dst = appendCommandNodeOnboardingJobPtr(dst, update.Job)
	dst = appendCommandOnboardingJobStatusPtr(dst, update.ExpectedStatus)
	dst = appendCommandAssignmentPtr(dst, update.Assignment)
	dst = appendCommandTaskPtr(dst, update.Task)
	return dst
}

func appendCommandNodeOnboardingJobPtr(dst []byte, job *nodeOnboardingJobEnvelope) []byte {
	if job == nil {
		return appendCommandBool(dst, false)
	}
	dst = appendCommandBool(dst, true)
	return appendCommandNodeOnboardingJob(dst, *job)
}

func appendCommandNodeOnboardingJob(dst []byte, job nodeOnboardingJobEnvelope) []byte {
	dst = appendCommandString(dst, job.JobID)
	dst = binary.BigEndian.AppendUint64(dst, job.TargetNodeID)
	dst = appendCommandString(dst, job.RetryOfJobID)
	dst = appendCommandString(dst, string(job.Status))
	dst = appendCommandTime(dst, job.CreatedAt)
	dst = appendCommandTime(dst, job.UpdatedAt)
	dst = appendCommandTime(dst, job.StartedAt)
	dst = appendCommandTime(dst, job.CompletedAt)
	dst = binary.BigEndian.AppendUint32(dst, job.PlanVersion)
	dst = appendCommandString(dst, job.PlanFingerprint)
	dst = appendCommandNodeOnboardingPlan(dst, job.Plan)
	dst = appendCommandNodeOnboardingMoves(dst, job.Moves)
	dst = appendCommandInt64(dst, int64(job.CurrentMoveIndex))
	dst = appendCommandNodeOnboardingResultCounts(dst, job.ResultCounts)
	dst = appendCommandTaskPtr(dst, job.CurrentTask)
	dst = appendCommandString(dst, job.LastError)
	return dst
}

func appendCommandOnboardingJobStatusPtr(dst []byte, status *controllermeta.OnboardingJobStatus) []byte {
	if status == nil {
		return appendCommandBool(dst, false)
	}
	dst = appendCommandBool(dst, true)
	return appendCommandString(dst, string(*status))
}

func appendCommandNodeOnboardingPlan(dst []byte, plan nodeOnboardingPlanEnvelope) []byte {
	dst = binary.BigEndian.AppendUint64(dst, plan.TargetNodeID)
	dst = appendCommandNodeOnboardingPlanSummary(dst, plan.Summary)
	dst = appendCommandNodeOnboardingPlanMoves(dst, plan.Moves)
	dst = appendCommandNodeOnboardingBlockedReasons(dst, plan.BlockedReasons)
	return dst
}

func appendCommandNodeOnboardingPlanSummary(dst []byte, summary nodeOnboardingPlanSummaryEnvelope) []byte {
	dst = appendCommandInt64(dst, int64(summary.CurrentTargetSlotCount))
	dst = appendCommandInt64(dst, int64(summary.PlannedTargetSlotCount))
	dst = appendCommandInt64(dst, int64(summary.CurrentTargetLeaderCount))
	dst = appendCommandInt64(dst, int64(summary.PlannedLeaderGain))
	return dst
}

func appendCommandNodeOnboardingPlanMoves(dst []byte, moves []nodeOnboardingPlanMoveEnvelope) []byte {
	dst = binary.AppendUvarint(dst, uint64(len(moves)))
	for _, move := range moves {
		dst = binary.BigEndian.AppendUint32(dst, move.SlotID)
		dst = binary.BigEndian.AppendUint64(dst, move.SourceNodeID)
		dst = binary.BigEndian.AppendUint64(dst, move.TargetNodeID)
		dst = appendCommandString(dst, move.Reason)
		dst = appendCommandUint64Slice(dst, move.DesiredPeersBefore)
		dst = appendCommandUint64Slice(dst, move.DesiredPeersAfter)
		dst = binary.BigEndian.AppendUint64(dst, move.CurrentLeaderID)
		dst = appendCommandBool(dst, move.LeaderTransferRequired)
	}
	return dst
}

func appendCommandNodeOnboardingBlockedReasons(dst []byte, reasons []nodeOnboardingBlockedReasonEnvelope) []byte {
	dst = binary.AppendUvarint(dst, uint64(len(reasons)))
	for _, reason := range reasons {
		dst = appendCommandString(dst, reason.Code)
		dst = appendCommandString(dst, reason.Scope)
		dst = binary.BigEndian.AppendUint32(dst, reason.SlotID)
		dst = binary.BigEndian.AppendUint64(dst, reason.NodeID)
		dst = appendCommandString(dst, reason.Message)
	}
	return dst
}

func appendCommandNodeOnboardingMoves(dst []byte, moves []nodeOnboardingMoveEnvelope) []byte {
	dst = binary.AppendUvarint(dst, uint64(len(moves)))
	for _, move := range moves {
		dst = binary.BigEndian.AppendUint32(dst, move.SlotID)
		dst = binary.BigEndian.AppendUint64(dst, move.SourceNodeID)
		dst = binary.BigEndian.AppendUint64(dst, move.TargetNodeID)
		dst = appendCommandString(dst, string(move.Status))
		dst = append(dst, byte(move.TaskKind))
		dst = binary.BigEndian.AppendUint32(dst, move.TaskSlotID)
		dst = appendCommandTime(dst, move.StartedAt)
		dst = appendCommandTime(dst, move.CompletedAt)
		dst = appendCommandString(dst, move.LastError)
		dst = appendCommandUint64Slice(dst, move.DesiredPeersBefore)
		dst = appendCommandUint64Slice(dst, move.DesiredPeersAfter)
		dst = binary.BigEndian.AppendUint64(dst, move.LeaderBefore)
		dst = binary.BigEndian.AppendUint64(dst, move.LeaderAfter)
		dst = appendCommandBool(dst, move.LeaderTransferRequired)
	}
	return dst
}

func appendCommandNodeOnboardingResultCounts(dst []byte, counts nodeOnboardingResultCountsEnvelope) []byte {
	dst = appendCommandInt64(dst, int64(counts.Pending))
	dst = appendCommandInt64(dst, int64(counts.Running))
	dst = appendCommandInt64(dst, int64(counts.Completed))
	dst = appendCommandInt64(dst, int64(counts.Failed))
	dst = appendCommandInt64(dst, int64(counts.Skipped))
	return dst
}

func appendCommandString(dst []byte, value string) []byte {
	dst = binary.AppendUvarint(dst, uint64(len(value)))
	return append(dst, value...)
}

func appendCommandUint64Slice(dst []byte, values []uint64) []byte {
	dst = binary.AppendUvarint(dst, uint64(len(values)))
	for _, value := range values {
		dst = binary.BigEndian.AppendUint64(dst, value)
	}
	return dst
}

func appendCommandTime(dst []byte, value time.Time) []byte {
	if value.IsZero() {
		dst = appendCommandInt64(dst, 0)
		return appendCommandBool(dst, false)
	}
	dst = appendCommandInt64(dst, value.UnixNano())
	return appendCommandBool(dst, value.Location() == time.UTC)
}

func appendCommandInt64(dst []byte, value int64) []byte {
	return binary.BigEndian.AppendUint64(dst, uint64(value))
}

func appendCommandBool(dst []byte, value bool) []byte {
	if value {
		return append(dst, 1)
	}
	return append(dst, 0)
}

type commandEnvelopeReader struct {
	rest []byte
}

func (r *commandEnvelopeReader) readAgentReport() (*slotcontroller.AgentReport, error) {
	nodeID, err := r.readUint64()
	if err != nil {
		return nil, err
	}
	addr, err := r.readString()
	if err != nil {
		return nil, err
	}
	observedAt, err := r.readTime()
	if err != nil {
		return nil, err
	}
	capacityWeight64, err := r.readInt64()
	if err != nil {
		return nil, err
	}
	capacityWeight, err := commandIntFromInt64(capacityWeight64)
	if err != nil {
		return nil, err
	}
	hashSlotTableVersion, err := r.readUint64()
	if err != nil {
		return nil, err
	}
	runtime, err := r.readRuntimeViewPtr()
	if err != nil {
		return nil, err
	}
	return &slotcontroller.AgentReport{
		NodeID:               nodeID,
		Addr:                 addr,
		ObservedAt:           observedAt,
		CapacityWeight:       capacityWeight,
		HashSlotTableVersion: hashSlotTableVersion,
		Runtime:              runtime,
	}, nil
}

func (r *commandEnvelopeReader) readRuntimeViewPtr() (*controllermeta.SlotRuntimeView, error) {
	present, err := r.readBool()
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, nil
	}
	view, err := r.readRuntimeView()
	if err != nil {
		return nil, err
	}
	return &view, nil
}

func (r *commandEnvelopeReader) readRuntimeView() (controllermeta.SlotRuntimeView, error) {
	slotID, err := r.readUint32()
	if err != nil {
		return controllermeta.SlotRuntimeView{}, err
	}
	currentPeers, err := r.readUint64Slice()
	if err != nil {
		return controllermeta.SlotRuntimeView{}, err
	}
	currentVoters, err := r.readUint64Slice()
	if err != nil {
		return controllermeta.SlotRuntimeView{}, err
	}
	leaderID, err := r.readUint64()
	if err != nil {
		return controllermeta.SlotRuntimeView{}, err
	}
	healthyVoters, err := r.readUint32()
	if err != nil {
		return controllermeta.SlotRuntimeView{}, err
	}
	hasQuorum, err := r.readBool()
	if err != nil {
		return controllermeta.SlotRuntimeView{}, err
	}
	observedConfigEpoch, err := r.readUint64()
	if err != nil {
		return controllermeta.SlotRuntimeView{}, err
	}
	lastReportAt, err := r.readTime()
	if err != nil {
		return controllermeta.SlotRuntimeView{}, err
	}
	return controllermeta.SlotRuntimeView{
		SlotID:              slotID,
		CurrentPeers:        currentPeers,
		CurrentVoters:       currentVoters,
		LeaderID:            leaderID,
		HealthyVoters:       healthyVoters,
		HasQuorum:           hasQuorum,
		ObservedConfigEpoch: observedConfigEpoch,
		LastReportAt:        lastReportAt,
	}, nil
}

func (r *commandEnvelopeReader) readTaskAdvance() (*taskAdvanceEnvelope, error) {
	slotID, err := r.readUint32()
	if err != nil {
		return nil, err
	}
	attempt, err := r.readUint32()
	if err != nil {
		return nil, err
	}
	now, err := r.readTime()
	if err != nil {
		return nil, err
	}
	errText, err := r.readString()
	if err != nil {
		return nil, err
	}
	return &taskAdvanceEnvelope{SlotID: slotID, Attempt: attempt, Now: now, Err: errText}, nil
}

func (r *commandEnvelopeReader) readCommandTaskAdvance() (*slotcontroller.TaskAdvance, error) {
	slotID, err := r.readUint32()
	if err != nil {
		return nil, err
	}
	attempt, err := r.readUint32()
	if err != nil {
		return nil, err
	}
	now, err := r.readTime()
	if err != nil {
		return nil, err
	}
	errText, err := r.readString()
	if err != nil {
		return nil, err
	}
	advance := &slotcontroller.TaskAdvance{
		SlotID:  slotID,
		Attempt: attempt,
		Now:     now,
	}
	if errText != "" {
		advance.Err = errors.New(errText)
	}
	return advance, nil
}

func (r *commandEnvelopeReader) readMetaAssignmentPtr() (*controllermeta.SlotAssignment, error) {
	present, err := r.readBool()
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, nil
	}
	return r.readMetaAssignment()
}

func (r *commandEnvelopeReader) readMetaAssignment() (*controllermeta.SlotAssignment, error) {
	slotID, err := r.readUint32()
	if err != nil {
		return nil, err
	}
	desiredPeers, err := r.readUint64Slice()
	if err != nil {
		return nil, err
	}
	configEpoch, err := r.readUint64()
	if err != nil {
		return nil, err
	}
	balanceVersion, err := r.readUint64()
	if err != nil {
		return nil, err
	}
	preferredLeader, err := r.readUint64()
	if err != nil {
		return nil, err
	}
	cooldown, err := r.readTime()
	if err != nil {
		return nil, err
	}
	return &controllermeta.SlotAssignment{
		SlotID:                      slotID,
		DesiredPeers:                desiredPeers,
		ConfigEpoch:                 configEpoch,
		BalanceVersion:              balanceVersion,
		PreferredLeader:             preferredLeader,
		LeaderTransferCooldownUntil: cooldown,
	}, nil
}

func (r *commandEnvelopeReader) readMetaTaskPtr() (*controllermeta.ReconcileTask, error) {
	present, err := r.readBool()
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, nil
	}
	return r.readMetaTask()
}

func (r *commandEnvelopeReader) readMetaTask() (*controllermeta.ReconcileTask, error) {
	slotID, err := r.readUint32()
	if err != nil {
		return nil, err
	}
	kind, err := r.readByte()
	if err != nil {
		return nil, err
	}
	step, err := r.readByte()
	if err != nil {
		return nil, err
	}
	sourceNode, err := r.readUint64()
	if err != nil {
		return nil, err
	}
	targetNode, err := r.readUint64()
	if err != nil {
		return nil, err
	}
	attempt, err := r.readUint32()
	if err != nil {
		return nil, err
	}
	nextRunAt, err := r.readTime()
	if err != nil {
		return nil, err
	}
	status, err := r.readByte()
	if err != nil {
		return nil, err
	}
	lastError, err := r.readString()
	if err != nil {
		return nil, err
	}
	return &controllermeta.ReconcileTask{
		SlotID:     slotID,
		Kind:       controllermeta.TaskKind(kind),
		Step:       controllermeta.TaskStep(step),
		SourceNode: sourceNode,
		TargetNode: targetNode,
		Attempt:    attempt,
		NextRunAt:  nextRunAt,
		Status:     controllermeta.TaskStatus(status),
		LastError:  lastError,
	}, nil
}

func (r *commandEnvelopeReader) readAssignmentPtr() (*slotcontrollerAssignmentEnvelope, error) {
	present, err := r.readBool()
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, nil
	}
	return r.readAssignment()
}

func (r *commandEnvelopeReader) readAssignment() (*slotcontrollerAssignmentEnvelope, error) {
	slotID, err := r.readUint32()
	if err != nil {
		return nil, err
	}
	desiredPeers, err := r.readUint64Slice()
	if err != nil {
		return nil, err
	}
	configEpoch, err := r.readUint64()
	if err != nil {
		return nil, err
	}
	balanceVersion, err := r.readUint64()
	if err != nil {
		return nil, err
	}
	preferredLeader, err := r.readUint64()
	if err != nil {
		return nil, err
	}
	cooldown, err := r.readTime()
	if err != nil {
		return nil, err
	}
	return &slotcontrollerAssignmentEnvelope{
		SlotID:                      slotID,
		DesiredPeers:                desiredPeers,
		ConfigEpoch:                 configEpoch,
		BalanceVersion:              balanceVersion,
		PreferredLeader:             preferredLeader,
		LeaderTransferCooldownUntil: cooldown,
	}, nil
}

func (r *commandEnvelopeReader) readTaskPtr() (*slotcontrollerReconcileTaskEnvelope, error) {
	present, err := r.readBool()
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, nil
	}
	return r.readTask()
}

func (r *commandEnvelopeReader) readTask() (*slotcontrollerReconcileTaskEnvelope, error) {
	slotID, err := r.readUint32()
	if err != nil {
		return nil, err
	}
	kind, err := r.readByte()
	if err != nil {
		return nil, err
	}
	step, err := r.readByte()
	if err != nil {
		return nil, err
	}
	sourceNode, err := r.readUint64()
	if err != nil {
		return nil, err
	}
	targetNode, err := r.readUint64()
	if err != nil {
		return nil, err
	}
	attempt, err := r.readUint32()
	if err != nil {
		return nil, err
	}
	nextRunAt, err := r.readTime()
	if err != nil {
		return nil, err
	}
	status, err := r.readByte()
	if err != nil {
		return nil, err
	}
	lastError, err := r.readString()
	if err != nil {
		return nil, err
	}
	return &slotcontrollerReconcileTaskEnvelope{
		SlotID:     slotID,
		Kind:       controllermeta.TaskKind(kind),
		Step:       controllermeta.TaskStep(step),
		SourceNode: sourceNode,
		TargetNode: targetNode,
		Attempt:    attempt,
		NextRunAt:  nextRunAt,
		Status:     controllermeta.TaskStatus(status),
		LastError:  lastError,
	}, nil
}

func (r *commandEnvelopeReader) readMigrationRequest() (*slotcontroller.MigrationRequest, error) {
	hashSlot, err := r.readUint16()
	if err != nil {
		return nil, err
	}
	source, err := r.readUint64()
	if err != nil {
		return nil, err
	}
	target, err := r.readUint64()
	if err != nil {
		return nil, err
	}
	phase, err := r.readByte()
	if err != nil {
		return nil, err
	}
	return &slotcontroller.MigrationRequest{HashSlot: hashSlot, Source: source, Target: target, Phase: phase}, nil
}

func (r *commandEnvelopeReader) readAddSlotRequest() (*slotcontroller.AddSlotRequest, error) {
	newSlotID, err := r.readUint64()
	if err != nil {
		return nil, err
	}
	peers, err := r.readUint64Slice()
	if err != nil {
		return nil, err
	}
	preferredLeader, err := r.readUint64()
	if err != nil {
		return nil, err
	}
	return &slotcontroller.AddSlotRequest{NewSlotID: newSlotID, Peers: peers, PreferredLeader: preferredLeader}, nil
}

func (r *commandEnvelopeReader) readNodeStatusUpdate() (*slotcontroller.NodeStatusUpdate, error) {
	count, err := r.readItemCount(27)
	if err != nil {
		return nil, err
	}
	update := &slotcontroller.NodeStatusUpdate{Transitions: make([]slotcontroller.NodeStatusTransition, 0, int(count))}
	for i := uint64(0); i < count; i++ {
		nodeID, err := r.readUint64()
		if err != nil {
			return nil, err
		}
		newStatus, err := r.readByte()
		if err != nil {
			return nil, err
		}
		expectedPresent, err := r.readBool()
		if err != nil {
			return nil, err
		}
		var expectedStatus *controllermeta.NodeStatus
		if expectedPresent {
			value, err := r.readByte()
			if err != nil {
				return nil, err
			}
			expected := controllermeta.NodeStatus(value)
			expectedStatus = &expected
		}
		evaluatedAt, err := r.readTime()
		if err != nil {
			return nil, err
		}
		addr, err := r.readString()
		if err != nil {
			return nil, err
		}
		capacityWeight64, err := r.readInt64()
		if err != nil {
			return nil, err
		}
		capacityWeight, err := commandIntFromInt64(capacityWeight64)
		if err != nil {
			return nil, err
		}
		update.Transitions = append(update.Transitions, slotcontroller.NodeStatusTransition{
			NodeID:         nodeID,
			NewStatus:      controllermeta.NodeStatus(newStatus),
			ExpectedStatus: expectedStatus,
			EvaluatedAt:    evaluatedAt,
			Addr:           addr,
			CapacityWeight: capacityWeight,
		})
	}
	return update, nil
}

func (r *commandEnvelopeReader) readNodeJoinRequest() (*slotcontroller.NodeJoinRequest, error) {
	nodeID, err := r.readUint64()
	if err != nil {
		return nil, err
	}
	name, err := r.readString()
	if err != nil {
		return nil, err
	}
	addr, err := r.readString()
	if err != nil {
		return nil, err
	}
	capacityWeight64, err := r.readInt64()
	if err != nil {
		return nil, err
	}
	capacityWeight, err := commandIntFromInt64(capacityWeight64)
	if err != nil {
		return nil, err
	}
	joinedAt, err := r.readTime()
	if err != nil {
		return nil, err
	}
	return &slotcontroller.NodeJoinRequest{NodeID: nodeID, Name: name, Addr: addr, CapacityWeight: capacityWeight, JoinedAt: joinedAt}, nil
}

func (r *commandEnvelopeReader) readNodeOnboardingUpdate() (*nodeOnboardingJobUpdateEnvelope, error) {
	job, err := r.readNodeOnboardingJobPtr()
	if err != nil {
		return nil, err
	}
	expectedStatus, err := r.readOnboardingJobStatusPtr()
	if err != nil {
		return nil, err
	}
	assignment, err := r.readAssignmentPtr()
	if err != nil {
		return nil, err
	}
	task, err := r.readTaskPtr()
	if err != nil {
		return nil, err
	}
	return &nodeOnboardingJobUpdateEnvelope{Job: job, ExpectedStatus: expectedStatus, Assignment: assignment, Task: task}, nil
}

func (r *commandEnvelopeReader) readMetaNodeOnboardingUpdate() (*slotcontroller.NodeOnboardingJobUpdate, error) {
	job, err := r.readMetaNodeOnboardingJobPtr()
	if err != nil {
		return nil, err
	}
	expectedStatus, err := r.readOnboardingJobStatusPtr()
	if err != nil {
		return nil, err
	}
	assignment, err := r.readMetaAssignmentPtr()
	if err != nil {
		return nil, err
	}
	task, err := r.readMetaTaskPtr()
	if err != nil {
		return nil, err
	}
	return &slotcontroller.NodeOnboardingJobUpdate{Job: job, ExpectedStatus: expectedStatus, Assignment: assignment, Task: task}, nil
}

func (r *commandEnvelopeReader) readMetaNodeOnboardingJobPtr() (*controllermeta.NodeOnboardingJob, error) {
	present, err := r.readBool()
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, nil
	}
	return r.readMetaNodeOnboardingJob()
}

func (r *commandEnvelopeReader) readMetaNodeOnboardingJob() (*controllermeta.NodeOnboardingJob, error) {
	jobID, err := r.readString()
	if err != nil {
		return nil, err
	}
	targetNodeID, err := r.readUint64()
	if err != nil {
		return nil, err
	}
	retryOfJobID, err := r.readString()
	if err != nil {
		return nil, err
	}
	status, err := r.readString()
	if err != nil {
		return nil, err
	}
	createdAt, err := r.readTime()
	if err != nil {
		return nil, err
	}
	updatedAt, err := r.readTime()
	if err != nil {
		return nil, err
	}
	startedAt, err := r.readTime()
	if err != nil {
		return nil, err
	}
	completedAt, err := r.readTime()
	if err != nil {
		return nil, err
	}
	planVersion, err := r.readUint32()
	if err != nil {
		return nil, err
	}
	planFingerprint, err := r.readString()
	if err != nil {
		return nil, err
	}
	plan, err := r.readMetaNodeOnboardingPlan()
	if err != nil {
		return nil, err
	}
	moves, err := r.readMetaNodeOnboardingMoves()
	if err != nil {
		return nil, err
	}
	currentMoveIndex, err := r.readInt()
	if err != nil {
		return nil, err
	}
	resultCounts, err := r.readMetaNodeOnboardingResultCounts()
	if err != nil {
		return nil, err
	}
	currentTask, err := r.readMetaTaskPtr()
	if err != nil {
		return nil, err
	}
	lastError, err := r.readString()
	if err != nil {
		return nil, err
	}
	return &controllermeta.NodeOnboardingJob{
		JobID:            jobID,
		TargetNodeID:     targetNodeID,
		RetryOfJobID:     retryOfJobID,
		Status:           controllermeta.OnboardingJobStatus(status),
		CreatedAt:        createdAt,
		UpdatedAt:        updatedAt,
		StartedAt:        startedAt,
		CompletedAt:      completedAt,
		PlanVersion:      planVersion,
		PlanFingerprint:  planFingerprint,
		Plan:             plan,
		Moves:            moves,
		CurrentMoveIndex: currentMoveIndex,
		ResultCounts:     resultCounts,
		CurrentTask:      currentTask,
		LastError:        lastError,
	}, nil
}

func (r *commandEnvelopeReader) readMetaNodeOnboardingPlan() (controllermeta.NodeOnboardingPlan, error) {
	targetNodeID, err := r.readUint64()
	if err != nil {
		return controllermeta.NodeOnboardingPlan{}, err
	}
	summary, err := r.readMetaNodeOnboardingPlanSummary()
	if err != nil {
		return controllermeta.NodeOnboardingPlan{}, err
	}
	moves, err := r.readMetaNodeOnboardingPlanMoves()
	if err != nil {
		return controllermeta.NodeOnboardingPlan{}, err
	}
	blockedReasons, err := r.readMetaNodeOnboardingBlockedReasons()
	if err != nil {
		return controllermeta.NodeOnboardingPlan{}, err
	}
	return controllermeta.NodeOnboardingPlan{TargetNodeID: targetNodeID, Summary: summary, Moves: moves, BlockedReasons: blockedReasons}, nil
}

func (r *commandEnvelopeReader) readMetaNodeOnboardingPlanSummary() (controllermeta.NodeOnboardingPlanSummary, error) {
	currentTargetSlotCount, err := r.readInt()
	if err != nil {
		return controllermeta.NodeOnboardingPlanSummary{}, err
	}
	plannedTargetSlotCount, err := r.readInt()
	if err != nil {
		return controllermeta.NodeOnboardingPlanSummary{}, err
	}
	currentTargetLeaderCount, err := r.readInt()
	if err != nil {
		return controllermeta.NodeOnboardingPlanSummary{}, err
	}
	plannedLeaderGain, err := r.readInt()
	if err != nil {
		return controllermeta.NodeOnboardingPlanSummary{}, err
	}
	return controllermeta.NodeOnboardingPlanSummary{
		CurrentTargetSlotCount:   currentTargetSlotCount,
		PlannedTargetSlotCount:   plannedTargetSlotCount,
		CurrentTargetLeaderCount: currentTargetLeaderCount,
		PlannedLeaderGain:        plannedLeaderGain,
	}, nil
}

func (r *commandEnvelopeReader) readMetaNodeOnboardingPlanMoves() ([]controllermeta.NodeOnboardingPlanMove, error) {
	count, err := r.readItemCount(27)
	if err != nil {
		return nil, err
	}
	moves := make([]controllermeta.NodeOnboardingPlanMove, 0, int(count))
	for i := uint64(0); i < count; i++ {
		slotID, err := r.readUint32()
		if err != nil {
			return nil, err
		}
		sourceNodeID, err := r.readUint64()
		if err != nil {
			return nil, err
		}
		targetNodeID, err := r.readUint64()
		if err != nil {
			return nil, err
		}
		reason, err := r.readString()
		if err != nil {
			return nil, err
		}
		desiredPeersBefore, err := r.readUint64Slice()
		if err != nil {
			return nil, err
		}
		desiredPeersAfter, err := r.readUint64Slice()
		if err != nil {
			return nil, err
		}
		currentLeaderID, err := r.readUint64()
		if err != nil {
			return nil, err
		}
		leaderTransferRequired, err := r.readBool()
		if err != nil {
			return nil, err
		}
		moves = append(moves, controllermeta.NodeOnboardingPlanMove{
			SlotID:                 slotID,
			SourceNodeID:           sourceNodeID,
			TargetNodeID:           targetNodeID,
			Reason:                 reason,
			DesiredPeersBefore:     desiredPeersBefore,
			DesiredPeersAfter:      desiredPeersAfter,
			CurrentLeaderID:        currentLeaderID,
			LeaderTransferRequired: leaderTransferRequired,
		})
	}
	return moves, nil
}

func (r *commandEnvelopeReader) readMetaNodeOnboardingBlockedReasons() ([]controllermeta.NodeOnboardingBlockedReason, error) {
	count, err := r.readItemCount(15)
	if err != nil {
		return nil, err
	}
	reasons := make([]controllermeta.NodeOnboardingBlockedReason, 0, int(count))
	for i := uint64(0); i < count; i++ {
		code, err := r.readString()
		if err != nil {
			return nil, err
		}
		scope, err := r.readString()
		if err != nil {
			return nil, err
		}
		slotID, err := r.readUint32()
		if err != nil {
			return nil, err
		}
		nodeID, err := r.readUint64()
		if err != nil {
			return nil, err
		}
		message, err := r.readString()
		if err != nil {
			return nil, err
		}
		reasons = append(reasons, controllermeta.NodeOnboardingBlockedReason{
			Code:    code,
			Scope:   scope,
			SlotID:  slotID,
			NodeID:  nodeID,
			Message: message,
		})
	}
	return reasons, nil
}

func (r *commandEnvelopeReader) readMetaNodeOnboardingMoves() ([]controllermeta.NodeOnboardingMove, error) {
	count, err := r.readItemCount(63)
	if err != nil {
		return nil, err
	}
	moves := make([]controllermeta.NodeOnboardingMove, 0, int(count))
	for i := uint64(0); i < count; i++ {
		slotID, err := r.readUint32()
		if err != nil {
			return nil, err
		}
		sourceNodeID, err := r.readUint64()
		if err != nil {
			return nil, err
		}
		targetNodeID, err := r.readUint64()
		if err != nil {
			return nil, err
		}
		status, err := r.readString()
		if err != nil {
			return nil, err
		}
		taskKind, err := r.readByte()
		if err != nil {
			return nil, err
		}
		taskSlotID, err := r.readUint32()
		if err != nil {
			return nil, err
		}
		startedAt, err := r.readTime()
		if err != nil {
			return nil, err
		}
		completedAt, err := r.readTime()
		if err != nil {
			return nil, err
		}
		lastError, err := r.readString()
		if err != nil {
			return nil, err
		}
		desiredPeersBefore, err := r.readUint64Slice()
		if err != nil {
			return nil, err
		}
		desiredPeersAfter, err := r.readUint64Slice()
		if err != nil {
			return nil, err
		}
		leaderBefore, err := r.readUint64()
		if err != nil {
			return nil, err
		}
		leaderAfter, err := r.readUint64()
		if err != nil {
			return nil, err
		}
		leaderTransferRequired, err := r.readBool()
		if err != nil {
			return nil, err
		}
		moves = append(moves, controllermeta.NodeOnboardingMove{
			SlotID:                 slotID,
			SourceNodeID:           sourceNodeID,
			TargetNodeID:           targetNodeID,
			Status:                 controllermeta.OnboardingMoveStatus(status),
			TaskKind:               controllermeta.TaskKind(taskKind),
			TaskSlotID:             taskSlotID,
			StartedAt:              startedAt,
			CompletedAt:            completedAt,
			LastError:              lastError,
			DesiredPeersBefore:     desiredPeersBefore,
			DesiredPeersAfter:      desiredPeersAfter,
			LeaderBefore:           leaderBefore,
			LeaderAfter:            leaderAfter,
			LeaderTransferRequired: leaderTransferRequired,
		})
	}
	return moves, nil
}

func (r *commandEnvelopeReader) readMetaNodeOnboardingResultCounts() (controllermeta.OnboardingResultCounts, error) {
	pending, err := r.readInt()
	if err != nil {
		return controllermeta.OnboardingResultCounts{}, err
	}
	running, err := r.readInt()
	if err != nil {
		return controllermeta.OnboardingResultCounts{}, err
	}
	completed, err := r.readInt()
	if err != nil {
		return controllermeta.OnboardingResultCounts{}, err
	}
	failed, err := r.readInt()
	if err != nil {
		return controllermeta.OnboardingResultCounts{}, err
	}
	skipped, err := r.readInt()
	if err != nil {
		return controllermeta.OnboardingResultCounts{}, err
	}
	return controllermeta.OnboardingResultCounts{
		Pending:   pending,
		Running:   running,
		Completed: completed,
		Failed:    failed,
		Skipped:   skipped,
	}, nil
}

func (r *commandEnvelopeReader) readNodeOnboardingJobPtr() (*nodeOnboardingJobEnvelope, error) {
	present, err := r.readBool()
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, nil
	}
	return r.readNodeOnboardingJob()
}

func (r *commandEnvelopeReader) readNodeOnboardingJob() (*nodeOnboardingJobEnvelope, error) {
	jobID, err := r.readString()
	if err != nil {
		return nil, err
	}
	targetNodeID, err := r.readUint64()
	if err != nil {
		return nil, err
	}
	retryOfJobID, err := r.readString()
	if err != nil {
		return nil, err
	}
	status, err := r.readString()
	if err != nil {
		return nil, err
	}
	createdAt, err := r.readTime()
	if err != nil {
		return nil, err
	}
	updatedAt, err := r.readTime()
	if err != nil {
		return nil, err
	}
	startedAt, err := r.readTime()
	if err != nil {
		return nil, err
	}
	completedAt, err := r.readTime()
	if err != nil {
		return nil, err
	}
	planVersion, err := r.readUint32()
	if err != nil {
		return nil, err
	}
	planFingerprint, err := r.readString()
	if err != nil {
		return nil, err
	}
	plan, err := r.readNodeOnboardingPlan()
	if err != nil {
		return nil, err
	}
	moves, err := r.readNodeOnboardingMoves()
	if err != nil {
		return nil, err
	}
	currentMoveIndex64, err := r.readInt64()
	if err != nil {
		return nil, err
	}
	currentMoveIndex, err := commandIntFromInt64(currentMoveIndex64)
	if err != nil {
		return nil, err
	}
	resultCounts, err := r.readNodeOnboardingResultCounts()
	if err != nil {
		return nil, err
	}
	currentTask, err := r.readTaskPtr()
	if err != nil {
		return nil, err
	}
	lastError, err := r.readString()
	if err != nil {
		return nil, err
	}
	return &nodeOnboardingJobEnvelope{
		JobID:            jobID,
		TargetNodeID:     targetNodeID,
		RetryOfJobID:     retryOfJobID,
		Status:           controllermeta.OnboardingJobStatus(status),
		CreatedAt:        createdAt,
		UpdatedAt:        updatedAt,
		StartedAt:        startedAt,
		CompletedAt:      completedAt,
		PlanVersion:      planVersion,
		PlanFingerprint:  planFingerprint,
		Plan:             plan,
		Moves:            moves,
		CurrentMoveIndex: currentMoveIndex,
		ResultCounts:     resultCounts,
		CurrentTask:      currentTask,
		LastError:        lastError,
	}, nil
}

func (r *commandEnvelopeReader) readOnboardingJobStatusPtr() (*controllermeta.OnboardingJobStatus, error) {
	present, err := r.readBool()
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, nil
	}
	value, err := r.readString()
	if err != nil {
		return nil, err
	}
	status := controllermeta.OnboardingJobStatus(value)
	return &status, nil
}

func (r *commandEnvelopeReader) readNodeOnboardingPlan() (nodeOnboardingPlanEnvelope, error) {
	targetNodeID, err := r.readUint64()
	if err != nil {
		return nodeOnboardingPlanEnvelope{}, err
	}
	summary, err := r.readNodeOnboardingPlanSummary()
	if err != nil {
		return nodeOnboardingPlanEnvelope{}, err
	}
	moves, err := r.readNodeOnboardingPlanMoves()
	if err != nil {
		return nodeOnboardingPlanEnvelope{}, err
	}
	blockedReasons, err := r.readNodeOnboardingBlockedReasons()
	if err != nil {
		return nodeOnboardingPlanEnvelope{}, err
	}
	return nodeOnboardingPlanEnvelope{TargetNodeID: targetNodeID, Summary: summary, Moves: moves, BlockedReasons: blockedReasons}, nil
}

func (r *commandEnvelopeReader) readNodeOnboardingPlanSummary() (nodeOnboardingPlanSummaryEnvelope, error) {
	currentTargetSlotCount, err := r.readInt()
	if err != nil {
		return nodeOnboardingPlanSummaryEnvelope{}, err
	}
	plannedTargetSlotCount, err := r.readInt()
	if err != nil {
		return nodeOnboardingPlanSummaryEnvelope{}, err
	}
	currentTargetLeaderCount, err := r.readInt()
	if err != nil {
		return nodeOnboardingPlanSummaryEnvelope{}, err
	}
	plannedLeaderGain, err := r.readInt()
	if err != nil {
		return nodeOnboardingPlanSummaryEnvelope{}, err
	}
	return nodeOnboardingPlanSummaryEnvelope{
		CurrentTargetSlotCount:   currentTargetSlotCount,
		PlannedTargetSlotCount:   plannedTargetSlotCount,
		CurrentTargetLeaderCount: currentTargetLeaderCount,
		PlannedLeaderGain:        plannedLeaderGain,
	}, nil
}

func (r *commandEnvelopeReader) readNodeOnboardingPlanMoves() ([]nodeOnboardingPlanMoveEnvelope, error) {
	count, err := r.readItemCount(27)
	if err != nil {
		return nil, err
	}
	moves := make([]nodeOnboardingPlanMoveEnvelope, 0, int(count))
	for i := uint64(0); i < count; i++ {
		slotID, err := r.readUint32()
		if err != nil {
			return nil, err
		}
		sourceNodeID, err := r.readUint64()
		if err != nil {
			return nil, err
		}
		targetNodeID, err := r.readUint64()
		if err != nil {
			return nil, err
		}
		reason, err := r.readString()
		if err != nil {
			return nil, err
		}
		desiredPeersBefore, err := r.readUint64Slice()
		if err != nil {
			return nil, err
		}
		desiredPeersAfter, err := r.readUint64Slice()
		if err != nil {
			return nil, err
		}
		currentLeaderID, err := r.readUint64()
		if err != nil {
			return nil, err
		}
		leaderTransferRequired, err := r.readBool()
		if err != nil {
			return nil, err
		}
		moves = append(moves, nodeOnboardingPlanMoveEnvelope{
			SlotID:                 slotID,
			SourceNodeID:           sourceNodeID,
			TargetNodeID:           targetNodeID,
			Reason:                 reason,
			DesiredPeersBefore:     desiredPeersBefore,
			DesiredPeersAfter:      desiredPeersAfter,
			CurrentLeaderID:        currentLeaderID,
			LeaderTransferRequired: leaderTransferRequired,
		})
	}
	return moves, nil
}

func (r *commandEnvelopeReader) readNodeOnboardingBlockedReasons() ([]nodeOnboardingBlockedReasonEnvelope, error) {
	count, err := r.readItemCount(15)
	if err != nil {
		return nil, err
	}
	reasons := make([]nodeOnboardingBlockedReasonEnvelope, 0, int(count))
	for i := uint64(0); i < count; i++ {
		code, err := r.readString()
		if err != nil {
			return nil, err
		}
		scope, err := r.readString()
		if err != nil {
			return nil, err
		}
		slotID, err := r.readUint32()
		if err != nil {
			return nil, err
		}
		nodeID, err := r.readUint64()
		if err != nil {
			return nil, err
		}
		message, err := r.readString()
		if err != nil {
			return nil, err
		}
		reasons = append(reasons, nodeOnboardingBlockedReasonEnvelope{Code: code, Scope: scope, SlotID: slotID, NodeID: nodeID, Message: message})
	}
	return reasons, nil
}

func (r *commandEnvelopeReader) readNodeOnboardingMoves() ([]nodeOnboardingMoveEnvelope, error) {
	count, err := r.readItemCount(63)
	if err != nil {
		return nil, err
	}
	moves := make([]nodeOnboardingMoveEnvelope, 0, int(count))
	for i := uint64(0); i < count; i++ {
		slotID, err := r.readUint32()
		if err != nil {
			return nil, err
		}
		sourceNodeID, err := r.readUint64()
		if err != nil {
			return nil, err
		}
		targetNodeID, err := r.readUint64()
		if err != nil {
			return nil, err
		}
		status, err := r.readString()
		if err != nil {
			return nil, err
		}
		taskKind, err := r.readByte()
		if err != nil {
			return nil, err
		}
		taskSlotID, err := r.readUint32()
		if err != nil {
			return nil, err
		}
		startedAt, err := r.readTime()
		if err != nil {
			return nil, err
		}
		completedAt, err := r.readTime()
		if err != nil {
			return nil, err
		}
		lastError, err := r.readString()
		if err != nil {
			return nil, err
		}
		desiredPeersBefore, err := r.readUint64Slice()
		if err != nil {
			return nil, err
		}
		desiredPeersAfter, err := r.readUint64Slice()
		if err != nil {
			return nil, err
		}
		leaderBefore, err := r.readUint64()
		if err != nil {
			return nil, err
		}
		leaderAfter, err := r.readUint64()
		if err != nil {
			return nil, err
		}
		leaderTransferRequired, err := r.readBool()
		if err != nil {
			return nil, err
		}
		moves = append(moves, nodeOnboardingMoveEnvelope{
			SlotID:                 slotID,
			SourceNodeID:           sourceNodeID,
			TargetNodeID:           targetNodeID,
			Status:                 controllermeta.OnboardingMoveStatus(status),
			TaskKind:               controllermeta.TaskKind(taskKind),
			TaskSlotID:             taskSlotID,
			StartedAt:              startedAt,
			CompletedAt:            completedAt,
			LastError:              lastError,
			DesiredPeersBefore:     desiredPeersBefore,
			DesiredPeersAfter:      desiredPeersAfter,
			LeaderBefore:           leaderBefore,
			LeaderAfter:            leaderAfter,
			LeaderTransferRequired: leaderTransferRequired,
		})
	}
	return moves, nil
}

func (r *commandEnvelopeReader) readNodeOnboardingResultCounts() (nodeOnboardingResultCountsEnvelope, error) {
	pending, err := r.readInt()
	if err != nil {
		return nodeOnboardingResultCountsEnvelope{}, err
	}
	running, err := r.readInt()
	if err != nil {
		return nodeOnboardingResultCountsEnvelope{}, err
	}
	completed, err := r.readInt()
	if err != nil {
		return nodeOnboardingResultCountsEnvelope{}, err
	}
	failed, err := r.readInt()
	if err != nil {
		return nodeOnboardingResultCountsEnvelope{}, err
	}
	skipped, err := r.readInt()
	if err != nil {
		return nodeOnboardingResultCountsEnvelope{}, err
	}
	return nodeOnboardingResultCountsEnvelope{Pending: pending, Running: running, Completed: completed, Failed: failed, Skipped: skipped}, nil
}

func (r *commandEnvelopeReader) readItemCount(minItemSize int) (uint64, error) {
	count, err := r.readUvarint()
	if err != nil {
		return 0, err
	}
	if count == 0 {
		return 0, nil
	}
	if minItemSize <= 0 {
		minItemSize = 1
	}
	if count > uint64(len(r.rest)/minItemSize) {
		return 0, errCommandEnvelopeCorrupt
	}
	return count, nil
}

func (r *commandEnvelopeReader) readUint64Slice() ([]uint64, error) {
	count, err := r.readUvarint()
	if err != nil {
		return nil, err
	}
	if count > uint64(len(r.rest)/8) {
		return nil, errCommandEnvelopeCorrupt
	}
	values := make([]uint64, 0, int(count))
	for i := uint64(0); i < count; i++ {
		value, err := r.readUint64()
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, nil
}

func (r *commandEnvelopeReader) readString() (string, error) {
	length, err := r.readUvarint()
	if err != nil {
		return "", err
	}
	if length > uint64(len(r.rest)) {
		return "", errCommandEnvelopeCorrupt
	}
	value := string(r.rest[:int(length)])
	r.rest = r.rest[int(length):]
	return value, nil
}

func (r *commandEnvelopeReader) readTime() (time.Time, error) {
	unixNano, err := r.readInt64()
	if err != nil {
		return time.Time{}, err
	}
	utc, err := r.readBool()
	if err != nil {
		return time.Time{}, err
	}
	if unixNano == 0 {
		return time.Time{}, nil
	}
	value := time.Unix(0, unixNano)
	if utc {
		return value.UTC(), nil
	}
	return value, nil
}

func (r *commandEnvelopeReader) readInt() (int, error) {
	value, err := r.readInt64()
	if err != nil {
		return 0, err
	}
	return commandIntFromInt64(value)
}

func (r *commandEnvelopeReader) readInt64() (int64, error) {
	value, err := r.readUint64()
	if err != nil {
		return 0, err
	}
	return int64(value), nil
}

func (r *commandEnvelopeReader) readUint64() (uint64, error) {
	if len(r.rest) < 8 {
		return 0, errCommandEnvelopeCorrupt
	}
	value := binary.BigEndian.Uint64(r.rest[:8])
	r.rest = r.rest[8:]
	return value, nil
}

func (r *commandEnvelopeReader) readUint32() (uint32, error) {
	if len(r.rest) < 4 {
		return 0, errCommandEnvelopeCorrupt
	}
	value := binary.BigEndian.Uint32(r.rest[:4])
	r.rest = r.rest[4:]
	return value, nil
}

func (r *commandEnvelopeReader) readUint16() (uint16, error) {
	if len(r.rest) < 2 {
		return 0, errCommandEnvelopeCorrupt
	}
	value := binary.BigEndian.Uint16(r.rest[:2])
	r.rest = r.rest[2:]
	return value, nil
}

func (r *commandEnvelopeReader) readUvarint() (uint64, error) {
	value, n := binary.Uvarint(r.rest)
	if n <= 0 {
		return 0, errCommandEnvelopeCorrupt
	}
	r.rest = r.rest[n:]
	return value, nil
}

func (r *commandEnvelopeReader) readByte() (byte, error) {
	if len(r.rest) < 1 {
		return 0, errCommandEnvelopeCorrupt
	}
	value := r.rest[0]
	r.rest = r.rest[1:]
	return value, nil
}

func (r *commandEnvelopeReader) readBool() (bool, error) {
	value, err := r.readByte()
	if err != nil {
		return false, err
	}
	switch value {
	case 0:
		return false, nil
	case 1:
		return true, nil
	default:
		return false, errCommandEnvelopeCorrupt
	}
}

func commandIntFromInt64(value int64) (int, error) {
	converted := int(value)
	if int64(converted) != value {
		return 0, errCommandEnvelopeCorrupt
	}
	return converted, nil
}
