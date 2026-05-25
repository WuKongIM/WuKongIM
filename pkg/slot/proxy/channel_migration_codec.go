package proxy

import (
	"fmt"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

var (
	channelMigrationRPCRequestMagic  = [...]byte{'W', 'K', 'M', 'Q', 1}
	channelMigrationRPCResponseMagic = [...]byte{'W', 'K', 'M', 'S', 1}
)

const (
	channelMigrationRPCGetActiveID byte = iota + 1
	channelMigrationRPCProposeID
	channelMigrationRPCListActiveForNodeID
)

func encodeChannelMigrationRPCRequestBinary(req channelMigrationRPCRequest) ([]byte, error) {
	opID, err := channelMigrationOpID(req.Op)
	if err != nil {
		return nil, err
	}
	dst := make([]byte, 0, len(channelMigrationRPCRequestMagic)+len(req.ChannelID)+32)
	dst = append(dst, channelMigrationRPCRequestMagic[:]...)
	dst = append(dst, opID)
	dst = runtimeMetaAppendUvarint(dst, req.SlotID)
	dst = runtimeMetaAppendUvarint(dst, uint64(req.HashSlot))
	dst = runtimeMetaAppendString(dst, req.ChannelID)
	dst = runtimeMetaAppendVarint(dst, req.ChannelType)
	dst = channelMigrationAppendBytes(dst, req.Command)
	if req.Op == channelMigrationRPCListActiveForNode {
		dst = runtimeMetaAppendUvarint(dst, req.NodeID)
		dst = runtimeMetaAppendVarint(dst, int64(req.Limit))
	}
	return dst, nil
}

func decodeChannelMigrationRPCRequest(body []byte) (channelMigrationRPCRequest, error) {
	if !runtimeMetaHasMagic(body, channelMigrationRPCRequestMagic[:]) {
		return channelMigrationRPCRequest{}, fmt.Errorf("metastore: invalid channel migration request codec")
	}
	offset := len(channelMigrationRPCRequestMagic)
	if offset >= len(body) {
		return channelMigrationRPCRequest{}, fmt.Errorf("metastore: short channel migration op")
	}
	op, err := channelMigrationOpFromID(body[offset])
	if err != nil {
		return channelMigrationRPCRequest{}, err
	}
	offset++

	var req channelMigrationRPCRequest
	req.Op = op
	if req.SlotID, offset, err = runtimeMetaReadUvarint(body, offset); err != nil {
		return channelMigrationRPCRequest{}, err
	}
	hashSlot, next, err := runtimeMetaReadUvarint(body, offset)
	if err != nil {
		return channelMigrationRPCRequest{}, err
	}
	if hashSlot > uint64(^uint16(0)) {
		return channelMigrationRPCRequest{}, fmt.Errorf("metastore: channel migration hash slot overflows uint16")
	}
	req.HashSlot = uint16(hashSlot)
	offset = next
	if req.ChannelID, offset, err = runtimeMetaReadString(body, offset); err != nil {
		return channelMigrationRPCRequest{}, err
	}
	if req.ChannelType, offset, err = runtimeMetaReadVarint(body, offset); err != nil {
		return channelMigrationRPCRequest{}, err
	}
	if req.Command, offset, err = channelMigrationReadBytes(body, offset); err != nil {
		return channelMigrationRPCRequest{}, err
	}
	if offset == len(body) {
		return req, nil
	}
	if req.NodeID, offset, err = runtimeMetaReadUvarint(body, offset); err != nil {
		return channelMigrationRPCRequest{}, err
	}
	limit, next, err := runtimeMetaReadVarint(body, offset)
	if err != nil {
		return channelMigrationRPCRequest{}, err
	}
	if limit > int64(int(^uint(0)>>1)) || limit < -int64(int(^uint(0)>>1))-1 {
		return channelMigrationRPCRequest{}, fmt.Errorf("metastore: channel migration limit overflows int")
	}
	req.Limit = int(limit)
	offset = next
	if offset != len(body) {
		return channelMigrationRPCRequest{}, fmt.Errorf("metastore: trailing channel migration request bytes")
	}
	return req, nil
}

func encodeChannelMigrationRPCResponse(resp channelMigrationRPCResponse) ([]byte, error) {
	dst := make([]byte, 0, len(channelMigrationRPCResponseMagic)+128)
	dst = append(dst, channelMigrationRPCResponseMagic[:]...)
	dst = runtimeMetaAppendString(dst, resp.Status)
	dst = runtimeMetaAppendUvarint(dst, resp.LeaderID)
	dst = appendChannelMigrationTaskPtr(dst, resp.Task)
	if len(resp.Tasks) > 0 || resp.HasMore {
		dst = appendChannelMigrationTasks(dst, resp.Tasks)
		dst = runtimeMetaAppendBool(dst, resp.HasMore)
	}
	return dst, nil
}

func decodeChannelMigrationRPCResponse(body []byte) (channelMigrationRPCResponse, error) {
	if !runtimeMetaHasMagic(body, channelMigrationRPCResponseMagic[:]) {
		return channelMigrationRPCResponse{}, fmt.Errorf("metastore: invalid channel migration response codec")
	}
	offset := len(channelMigrationRPCResponseMagic)
	var resp channelMigrationRPCResponse
	var err error
	if resp.Status, offset, err = runtimeMetaReadString(body, offset); err != nil {
		return channelMigrationRPCResponse{}, err
	}
	if resp.LeaderID, offset, err = runtimeMetaReadUvarint(body, offset); err != nil {
		return channelMigrationRPCResponse{}, err
	}
	if resp.Task, offset, err = readChannelMigrationTaskPtr(body, offset); err != nil {
		return channelMigrationRPCResponse{}, err
	}
	if offset == len(body) {
		return resp, nil
	}
	if resp.Tasks, offset, err = readChannelMigrationTasks(body, offset); err != nil {
		return channelMigrationRPCResponse{}, err
	}
	if resp.HasMore, offset, err = runtimeMetaReadBool(body, offset); err != nil {
		return channelMigrationRPCResponse{}, err
	}
	if offset != len(body) {
		return channelMigrationRPCResponse{}, fmt.Errorf("metastore: trailing channel migration response bytes")
	}
	return resp, nil
}

func channelMigrationOpID(op string) (byte, error) {
	switch op {
	case channelMigrationRPCGetActive:
		return channelMigrationRPCGetActiveID, nil
	case channelMigrationRPCPropose:
		return channelMigrationRPCProposeID, nil
	case channelMigrationRPCListActiveForNode:
		return channelMigrationRPCListActiveForNodeID, nil
	default:
		return 0, fmt.Errorf("metastore: unknown channel migration rpc op %q", op)
	}
}

func channelMigrationOpFromID(op byte) (string, error) {
	switch op {
	case channelMigrationRPCGetActiveID:
		return channelMigrationRPCGetActive, nil
	case channelMigrationRPCProposeID:
		return channelMigrationRPCPropose, nil
	case channelMigrationRPCListActiveForNodeID:
		return channelMigrationRPCListActiveForNode, nil
	default:
		return "", fmt.Errorf("metastore: unknown channel migration rpc op id %d", op)
	}
}

func channelMigrationAppendBytes(dst []byte, value []byte) []byte {
	dst = runtimeMetaAppendUvarint(dst, uint64(len(value)))
	return append(dst, value...)
}

func channelMigrationReadBytes(body []byte, offset int) ([]byte, int, error) {
	length, next, err := runtimeMetaReadUvarint(body, offset)
	if err != nil {
		return nil, offset, err
	}
	offset = next
	end := offset + int(length)
	if end < offset || end > len(body) {
		return nil, offset, fmt.Errorf("metastore: short bytes")
	}
	return append([]byte(nil), body[offset:end]...), end, nil
}

func appendChannelMigrationTaskPtr(dst []byte, task *metadb.ChannelMigrationTask) []byte {
	if task == nil {
		return append(dst, 0)
	}
	dst = append(dst, 1)
	return appendChannelMigrationTask(dst, *task)
}

func readChannelMigrationTaskPtr(body []byte, offset int) (*metadb.ChannelMigrationTask, int, error) {
	marker, next, err := runtimeMetaReadMarker(body, offset, "channel migration task")
	if err != nil || marker == 0 {
		return nil, next, err
	}
	task, next, err := readChannelMigrationTask(body, next)
	if err != nil {
		return nil, offset, err
	}
	return &task, next, nil
}

func appendChannelMigrationTasks(dst []byte, tasks []metadb.ChannelMigrationTask) []byte {
	dst = runtimeMetaAppendUvarint(dst, uint64(len(tasks)))
	for _, task := range tasks {
		dst = appendChannelMigrationTask(dst, task)
	}
	return dst
}

func readChannelMigrationTasks(body []byte, offset int) ([]metadb.ChannelMigrationTask, int, error) {
	length, next, err := runtimeMetaReadUvarint(body, offset)
	if err != nil {
		return nil, offset, err
	}
	if length > uint64(int(^uint(0)>>1)) {
		return nil, offset, fmt.Errorf("metastore: channel migration task list overflows int")
	}
	offset = next
	var tasks []metadb.ChannelMigrationTask
	for range length {
		task, next, err := readChannelMigrationTask(body, offset)
		if err != nil {
			return nil, offset, err
		}
		tasks = append(tasks, task)
		offset = next
	}
	return tasks, offset, nil
}

func appendChannelMigrationTask(dst []byte, task metadb.ChannelMigrationTask) []byte {
	dst = runtimeMetaAppendString(dst, task.TaskID)
	dst = runtimeMetaAppendUvarint(dst, uint64(task.Kind))
	dst = runtimeMetaAppendUvarint(dst, uint64(task.Status))
	dst = runtimeMetaAppendUvarint(dst, uint64(task.Phase))
	dst = runtimeMetaAppendString(dst, task.ChannelID)
	dst = runtimeMetaAppendVarint(dst, task.ChannelType)
	dst = runtimeMetaAppendUvarint(dst, task.SourceNode)
	dst = runtimeMetaAppendUvarint(dst, task.TargetNode)
	dst = runtimeMetaAppendUvarint(dst, task.DesiredLeader)
	dst = runtimeMetaAppendUvarint(dst, task.BaseChannelEpoch)
	dst = runtimeMetaAppendUvarint(dst, task.BaseLeaderEpoch)
	dst = runtimeMetaAppendString(dst, task.FenceToken)
	dst = runtimeMetaAppendUvarint(dst, task.FenceVersion)
	dst = runtimeMetaAppendVarint(dst, task.FenceUntilMS)
	dst = runtimeMetaAppendBool(dst, task.EmbeddedLeaderTransfer)
	dst = runtimeMetaAppendUvarint(dst, task.EmbeddedDesiredLeader)
	dst = runtimeMetaAppendUvarint(dst, task.OwnerNodeID)
	dst = runtimeMetaAppendVarint(dst, task.OwnerLeaseUntilMS)
	dst = runtimeMetaAppendUvarint(dst, task.CutoverLEO)
	dst = runtimeMetaAppendUvarint(dst, task.CutoverHW)
	dst = runtimeMetaAppendUvarint(dst, task.DrainedLeaderNode)
	dst = runtimeMetaAppendUvarint(dst, task.DrainedRuntimeGeneration)
	dst = runtimeMetaAppendUvarint(dst, task.DrainedChannelEpoch)
	dst = runtimeMetaAppendUvarint(dst, task.DrainedLeaderEpoch)
	dst = runtimeMetaAppendUvarint(dst, task.DrainedFenceVersion)
	dst = runtimeMetaAppendUvarint(dst, uint64(task.Attempt))
	dst = runtimeMetaAppendVarint(dst, task.NextRunAtMS)
	dst = runtimeMetaAppendString(dst, task.BlockerCode)
	dst = runtimeMetaAppendString(dst, task.BlockerMessage)
	dst = runtimeMetaAppendString(dst, task.LastError)
	dst = runtimeMetaAppendVarint(dst, task.CreatedAtMS)
	dst = runtimeMetaAppendVarint(dst, task.UpdatedAtMS)
	dst = runtimeMetaAppendVarint(dst, task.CompletedAtMS)
	return appendChannelMigrationProgress(dst, task.Progress)
}

func readChannelMigrationTask(body []byte, offset int) (metadb.ChannelMigrationTask, int, error) {
	var task metadb.ChannelMigrationTask
	var err error
	if task.TaskID, offset, err = runtimeMetaReadString(body, offset); err != nil {
		return metadb.ChannelMigrationTask{}, offset, err
	}
	kind, next, err := runtimeMetaReadUvarint(body, offset)
	if err != nil {
		return metadb.ChannelMigrationTask{}, offset, err
	}
	task.Kind = metadb.ChannelMigrationKind(kind)
	if !isValidChannelMigrationKindForRPC(task.Kind) {
		return metadb.ChannelMigrationTask{}, offset, fmt.Errorf("metastore: invalid channel migration kind %d", kind)
	}
	offset = next
	status, next, err := runtimeMetaReadUvarint(body, offset)
	if err != nil {
		return metadb.ChannelMigrationTask{}, offset, err
	}
	task.Status = metadb.ChannelMigrationStatus(status)
	if !isValidChannelMigrationStatusForRPC(task.Status) {
		return metadb.ChannelMigrationTask{}, offset, fmt.Errorf("metastore: invalid channel migration status %d", status)
	}
	offset = next
	phase, next, err := runtimeMetaReadUvarint(body, offset)
	if err != nil {
		return metadb.ChannelMigrationTask{}, offset, err
	}
	task.Phase = metadb.ChannelMigrationPhase(phase)
	if !isValidChannelMigrationPhaseForRPC(task.Phase) {
		return metadb.ChannelMigrationTask{}, offset, fmt.Errorf("metastore: invalid channel migration phase %d", phase)
	}
	offset = next
	if task.ChannelID, offset, err = runtimeMetaReadString(body, offset); err != nil {
		return metadb.ChannelMigrationTask{}, offset, err
	}
	if task.ChannelType, offset, err = runtimeMetaReadVarint(body, offset); err != nil {
		return metadb.ChannelMigrationTask{}, offset, err
	}
	if task.SourceNode, offset, err = runtimeMetaReadUvarint(body, offset); err != nil {
		return metadb.ChannelMigrationTask{}, offset, err
	}
	if task.TargetNode, offset, err = runtimeMetaReadUvarint(body, offset); err != nil {
		return metadb.ChannelMigrationTask{}, offset, err
	}
	if task.DesiredLeader, offset, err = runtimeMetaReadUvarint(body, offset); err != nil {
		return metadb.ChannelMigrationTask{}, offset, err
	}
	if task.BaseChannelEpoch, offset, err = runtimeMetaReadUvarint(body, offset); err != nil {
		return metadb.ChannelMigrationTask{}, offset, err
	}
	if task.BaseLeaderEpoch, offset, err = runtimeMetaReadUvarint(body, offset); err != nil {
		return metadb.ChannelMigrationTask{}, offset, err
	}
	if task.FenceToken, offset, err = runtimeMetaReadString(body, offset); err != nil {
		return metadb.ChannelMigrationTask{}, offset, err
	}
	if task.FenceVersion, offset, err = runtimeMetaReadUvarint(body, offset); err != nil {
		return metadb.ChannelMigrationTask{}, offset, err
	}
	if task.FenceUntilMS, offset, err = runtimeMetaReadVarint(body, offset); err != nil {
		return metadb.ChannelMigrationTask{}, offset, err
	}
	if task.EmbeddedLeaderTransfer, offset, err = runtimeMetaReadBool(body, offset); err != nil {
		return metadb.ChannelMigrationTask{}, offset, err
	}
	if task.EmbeddedDesiredLeader, offset, err = runtimeMetaReadUvarint(body, offset); err != nil {
		return metadb.ChannelMigrationTask{}, offset, err
	}
	if task.OwnerNodeID, offset, err = runtimeMetaReadUvarint(body, offset); err != nil {
		return metadb.ChannelMigrationTask{}, offset, err
	}
	if task.OwnerLeaseUntilMS, offset, err = runtimeMetaReadVarint(body, offset); err != nil {
		return metadb.ChannelMigrationTask{}, offset, err
	}
	if task.CutoverLEO, offset, err = runtimeMetaReadUvarint(body, offset); err != nil {
		return metadb.ChannelMigrationTask{}, offset, err
	}
	if task.CutoverHW, offset, err = runtimeMetaReadUvarint(body, offset); err != nil {
		return metadb.ChannelMigrationTask{}, offset, err
	}
	if task.DrainedLeaderNode, offset, err = runtimeMetaReadUvarint(body, offset); err != nil {
		return metadb.ChannelMigrationTask{}, offset, err
	}
	if task.DrainedRuntimeGeneration, offset, err = runtimeMetaReadUvarint(body, offset); err != nil {
		return metadb.ChannelMigrationTask{}, offset, err
	}
	if task.DrainedChannelEpoch, offset, err = runtimeMetaReadUvarint(body, offset); err != nil {
		return metadb.ChannelMigrationTask{}, offset, err
	}
	if task.DrainedLeaderEpoch, offset, err = runtimeMetaReadUvarint(body, offset); err != nil {
		return metadb.ChannelMigrationTask{}, offset, err
	}
	if task.DrainedFenceVersion, offset, err = runtimeMetaReadUvarint(body, offset); err != nil {
		return metadb.ChannelMigrationTask{}, offset, err
	}
	attempt, next, err := runtimeMetaReadUvarint(body, offset)
	if err != nil {
		return metadb.ChannelMigrationTask{}, offset, err
	}
	if attempt > uint64(^uint32(0)) {
		return metadb.ChannelMigrationTask{}, offset, fmt.Errorf("metastore: channel migration attempt overflows uint32")
	}
	task.Attempt = uint32(attempt)
	offset = next
	if task.NextRunAtMS, offset, err = runtimeMetaReadVarint(body, offset); err != nil {
		return metadb.ChannelMigrationTask{}, offset, err
	}
	if task.BlockerCode, offset, err = runtimeMetaReadString(body, offset); err != nil {
		return metadb.ChannelMigrationTask{}, offset, err
	}
	if task.BlockerMessage, offset, err = runtimeMetaReadString(body, offset); err != nil {
		return metadb.ChannelMigrationTask{}, offset, err
	}
	if task.LastError, offset, err = runtimeMetaReadString(body, offset); err != nil {
		return metadb.ChannelMigrationTask{}, offset, err
	}
	if task.CreatedAtMS, offset, err = runtimeMetaReadVarint(body, offset); err != nil {
		return metadb.ChannelMigrationTask{}, offset, err
	}
	if task.UpdatedAtMS, offset, err = runtimeMetaReadVarint(body, offset); err != nil {
		return metadb.ChannelMigrationTask{}, offset, err
	}
	if task.CompletedAtMS, offset, err = runtimeMetaReadVarint(body, offset); err != nil {
		return metadb.ChannelMigrationTask{}, offset, err
	}
	if task.Progress, offset, err = readChannelMigrationProgress(body, offset); err != nil {
		return metadb.ChannelMigrationTask{}, offset, err
	}
	return task, offset, nil
}

func appendChannelMigrationProgress(dst []byte, progress metadb.ChannelMigrationProgress) []byte {
	dst = runtimeMetaAppendUvarint(dst, progress.LeaderLEO)
	dst = runtimeMetaAppendUvarint(dst, progress.LeaderHW)
	dst = runtimeMetaAppendUvarint(dst, progress.TargetLEO)
	dst = runtimeMetaAppendUvarint(dst, progress.TargetCheckpointHW)
	dst = runtimeMetaAppendUvarint(dst, progress.LagRecords)
	return runtimeMetaAppendVarint(dst, progress.StableSinceMS)
}

func readChannelMigrationProgress(body []byte, offset int) (metadb.ChannelMigrationProgress, int, error) {
	var progress metadb.ChannelMigrationProgress
	var err error
	if progress.LeaderLEO, offset, err = runtimeMetaReadUvarint(body, offset); err != nil {
		return metadb.ChannelMigrationProgress{}, offset, err
	}
	if progress.LeaderHW, offset, err = runtimeMetaReadUvarint(body, offset); err != nil {
		return metadb.ChannelMigrationProgress{}, offset, err
	}
	if progress.TargetLEO, offset, err = runtimeMetaReadUvarint(body, offset); err != nil {
		return metadb.ChannelMigrationProgress{}, offset, err
	}
	if progress.TargetCheckpointHW, offset, err = runtimeMetaReadUvarint(body, offset); err != nil {
		return metadb.ChannelMigrationProgress{}, offset, err
	}
	if progress.LagRecords, offset, err = runtimeMetaReadUvarint(body, offset); err != nil {
		return metadb.ChannelMigrationProgress{}, offset, err
	}
	if progress.StableSinceMS, offset, err = runtimeMetaReadVarint(body, offset); err != nil {
		return metadb.ChannelMigrationProgress{}, offset, err
	}
	return progress, offset, nil
}

func isValidChannelMigrationKindForRPC(kind metadb.ChannelMigrationKind) bool {
	switch kind {
	case metadb.ChannelMigrationKindLeaderTransfer, metadb.ChannelMigrationKindReplicaReplace:
		return true
	default:
		return false
	}
}

func isValidChannelMigrationStatusForRPC(status metadb.ChannelMigrationStatus) bool {
	switch status {
	case metadb.ChannelMigrationStatusPending,
		metadb.ChannelMigrationStatusRunning,
		metadb.ChannelMigrationStatusBlocked,
		metadb.ChannelMigrationStatusCompleted,
		metadb.ChannelMigrationStatusFailed,
		metadb.ChannelMigrationStatusAborted:
		return true
	default:
		return false
	}
}

func isValidChannelMigrationPhaseForRPC(phase metadb.ChannelMigrationPhase) bool {
	switch phase {
	case metadb.ChannelMigrationPhaseValidate,
		metadb.ChannelMigrationPhaseProbeTarget,
		metadb.ChannelMigrationPhaseWriteFence,
		metadb.ChannelMigrationPhaseDrainLeader,
		metadb.ChannelMigrationPhaseFinalTargetCatchUp,
		metadb.ChannelMigrationPhaseCommitLeaderMeta,
		metadb.ChannelMigrationPhaseVerifyNewLeader,
		metadb.ChannelMigrationPhaseAddLearner,
		metadb.ChannelMigrationPhaseBootstrapTarget,
		metadb.ChannelMigrationPhaseWarmCatchUp,
		metadb.ChannelMigrationPhaseCutoverFence,
		metadb.ChannelMigrationPhasePromoteAndRemove,
		metadb.ChannelMigrationPhaseVerifyMembership,
		metadb.ChannelMigrationPhaseClearFence:
		return true
	default:
		return false
	}
}
