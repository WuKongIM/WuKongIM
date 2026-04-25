package cluster

import (
	"encoding/binary"
	"time"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	slotcontroller "github.com/WuKongIM/WuKongIM/pkg/controller/plane"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

const (
	rpcServiceController               uint8            = 14
	controllerRPCShardKey              multiraft.SlotID = multiraft.SlotID(^uint32(0))
	controllerRPCHeartbeat             string           = "heartbeat"
	controllerRPCRuntimeReport         string           = "runtime_report"
	controllerRPCListAssignments       string           = "list_assignments"
	controllerRPCListNodes             string           = "list_nodes"
	controllerRPCListRuntimeViews      string           = "list_runtime_views"
	controllerRPCListTasks             string           = "list_tasks"
	controllerRPCFetchObservationDelta string           = "fetch_observation_delta"
	controllerRPCOperator              string           = "operator"
	controllerRPCGetTask               string           = "get_task"
	controllerRPCForceReconcile        string           = "force_reconcile"
	controllerRPCTaskResult            string           = "task_result"
	controllerRPCStartMigration        string           = "start_migration"
	controllerRPCAdvanceMigration      string           = "advance_migration"
	controllerRPCFinalizeMigration     string           = "finalize_migration"
	controllerRPCAbortMigration        string           = "abort_migration"
	controllerRPCAddSlot               string           = "add_slot"
	controllerRPCRemoveSlot            string           = "remove_slot"
)

const (
	controllerCodecVersion byte = 1

	controllerRespFlagNotLeader byte = 1 << iota
	controllerRespFlagNotFound
)

const (
	controllerKindUnknown byte = iota
	controllerKindHeartbeat
	controllerKindRuntimeReport
	controllerKindListAssignments
	controllerKindListNodes
	controllerKindListRuntimeViews
	controllerKindListTasks
	controllerKindFetchObservationDelta
	controllerKindOperator
	controllerKindGetTask
	controllerKindForceReconcile
	controllerKindTaskResult
	controllerKindStartMigration
	controllerKindAdvanceMigration
	controllerKindFinalizeMigration
	controllerKindAbortMigration
	controllerKindAddSlot
	controllerKindRemoveSlot
)

type controllerRPCRequest struct {
	Kind             string
	SlotID           uint32
	Report           *slotcontroller.AgentReport
	RuntimeReport    *runtimeObservationReport
	ObservationDelta *observationDeltaRequest
	Op               *slotcontroller.OperatorRequest
	Advance          *controllerTaskAdvance
	Migration        *slotcontroller.MigrationRequest
	AddSlot          *slotcontroller.AddSlotRequest
	RemoveSlot       *slotcontroller.RemoveSlotRequest
}

type controllerTaskAdvance struct {
	SlotID  uint32
	Attempt uint32
	Now     time.Time
	Err     string
}

type runtimeObservationReport struct {
	NodeID      uint64
	ObservedAt  time.Time
	FullSync    bool
	Views       []controllermeta.SlotRuntimeView
	ClosedSlots []uint32
}

type controllerRPCResponse struct {
	NotLeader            bool
	NotFound             bool
	LeaderID             uint64
	Nodes                []controllermeta.ClusterNode
	Assignments          []controllermeta.SlotAssignment
	RuntimeViews         []controllermeta.SlotRuntimeView
	Tasks                []controllermeta.ReconcileTask
	ObservationDelta     *observationDeltaResponse
	Task                 *controllermeta.ReconcileTask
	HashSlotTableVersion uint64
	HashSlotTable        []byte
}

func encodeControllerRequest(req controllerRPCRequest) ([]byte, error) {
	kind, err := controllerKindCode(req.Kind)
	if err != nil {
		return nil, err
	}

	slotID := req.SlotID
	if slotID == 0 && req.Advance != nil {
		slotID = req.Advance.SlotID
	}

	payload, err := encodeControllerRequestPayload(req)
	if err != nil {
		return nil, err
	}

	body := make([]byte, 0, 1+1+4+binary.MaxVarintLen64+len(payload))
	body = append(body, controllerCodecVersion, kind)
	body = binary.BigEndian.AppendUint32(body, slotID)
	body = binary.AppendUvarint(body, uint64(len(payload)))
	body = append(body, payload...)
	return body, nil
}

func decodeControllerRequest(body []byte) (controllerRPCRequest, error) {
	if len(body) < 1+1+4 {
		return controllerRPCRequest{}, ErrInvalidConfig
	}
	if body[0] != controllerCodecVersion {
		return controllerRPCRequest{}, ErrInvalidConfig
	}

	kind, err := controllerKindName(body[1])
	if err != nil {
		return controllerRPCRequest{}, err
	}
	slotID := binary.BigEndian.Uint32(body[2:6])
	payload, err := readControllerPayload(body[6:])
	if err != nil {
		return controllerRPCRequest{}, err
	}

	req := controllerRPCRequest{
		Kind:   kind,
		SlotID: slotID,
	}
	if err := decodeControllerRequestPayload(&req, payload); err != nil {
		return controllerRPCRequest{}, err
	}
	return req, nil
}

func encodeControllerResponse(kind string, resp controllerRPCResponse) ([]byte, error) {
	flags := byte(0)
	if resp.NotLeader {
		flags |= controllerRespFlagNotLeader
	}
	if resp.NotFound {
		flags |= controllerRespFlagNotFound
	}

	payload, err := encodeControllerResponsePayload(kind, resp)
	if err != nil {
		return nil, err
	}

	body := make([]byte, 0, 1+1+8+binary.MaxVarintLen64+len(payload))
	body = append(body, controllerCodecVersion, flags)
	body = binary.BigEndian.AppendUint64(body, resp.LeaderID)
	body = binary.AppendUvarint(body, uint64(len(payload)))
	body = append(body, payload...)
	return body, nil
}

func decodeControllerResponse(kind string, body []byte) (controllerRPCResponse, error) {
	if len(body) < 1+1+8 {
		return controllerRPCResponse{}, ErrInvalidConfig
	}
	if body[0] != controllerCodecVersion {
		return controllerRPCResponse{}, ErrInvalidConfig
	}

	payload, err := readControllerPayload(body[10:])
	if err != nil {
		return controllerRPCResponse{}, err
	}

	resp := controllerRPCResponse{
		NotLeader: body[1]&controllerRespFlagNotLeader != 0,
		NotFound:  body[1]&controllerRespFlagNotFound != 0,
		LeaderID:  binary.BigEndian.Uint64(body[2:10]),
	}
	if err := decodeControllerResponsePayload(kind, &resp, payload); err != nil {
		return controllerRPCResponse{}, err
	}
	return resp, nil
}

func encodeControllerRequestPayload(req controllerRPCRequest) ([]byte, error) {
	switch req.Kind {
	case controllerRPCHeartbeat:
		if req.Report == nil {
			return nil, ErrInvalidConfig
		}
		return encodeAgentReport(*req.Report), nil
	case controllerRPCRuntimeReport:
		if req.RuntimeReport == nil {
			return nil, ErrInvalidConfig
		}
		return encodeRuntimeObservationReport(*req.RuntimeReport), nil
	case controllerRPCFetchObservationDelta:
		if req.ObservationDelta == nil {
			return nil, ErrInvalidConfig
		}
		return encodeObservationDeltaRequest(*req.ObservationDelta), nil
	case controllerRPCOperator:
		if req.Op == nil {
			return nil, ErrInvalidConfig
		}
		return encodeOperatorRequest(*req.Op), nil
	case controllerRPCTaskResult:
		if req.Advance == nil {
			return nil, ErrInvalidConfig
		}
		return encodeTaskAdvance(*req.Advance), nil
	case controllerRPCStartMigration, controllerRPCAdvanceMigration, controllerRPCFinalizeMigration, controllerRPCAbortMigration:
		if req.Migration == nil {
			return nil, ErrInvalidConfig
		}
		return encodeMigrationRequest(*req.Migration), nil
	case controllerRPCAddSlot:
		if req.AddSlot == nil {
			return nil, ErrInvalidConfig
		}
		return encodeAddSlotRequest(*req.AddSlot), nil
	case controllerRPCRemoveSlot:
		if req.RemoveSlot == nil {
			return nil, ErrInvalidConfig
		}
		return encodeRemoveSlotRequest(*req.RemoveSlot), nil
	case controllerRPCListAssignments, controllerRPCListNodes, controllerRPCListRuntimeViews, controllerRPCListTasks, controllerRPCGetTask, controllerRPCForceReconcile:
		return nil, nil
	default:
		return nil, ErrInvalidConfig
	}
}

func decodeControllerRequestPayload(req *controllerRPCRequest, payload []byte) error {
	switch req.Kind {
	case controllerRPCHeartbeat:
		report, err := decodeAgentReport(payload)
		if err != nil {
			return err
		}
		req.Report = &report
		return nil
	case controllerRPCRuntimeReport:
		report, err := decodeRuntimeObservationReport(payload)
		if err != nil {
			return err
		}
		req.RuntimeReport = &report
		return nil
	case controllerRPCFetchObservationDelta:
		delta, err := decodeObservationDeltaRequest(payload)
		if err != nil {
			return err
		}
		req.ObservationDelta = &delta
		return nil
	case controllerRPCOperator:
		op, err := decodeOperatorRequest(payload)
		if err != nil {
			return err
		}
		req.Op = &op
		return nil
	case controllerRPCTaskResult:
		advance, err := decodeTaskAdvance(req.SlotID, payload)
		if err != nil {
			return err
		}
		req.Advance = &advance
		return nil
	case controllerRPCStartMigration, controllerRPCAdvanceMigration, controllerRPCFinalizeMigration, controllerRPCAbortMigration:
		migration, err := decodeMigrationRequest(payload)
		if err != nil {
			return err
		}
		req.Migration = &migration
		return nil
	case controllerRPCAddSlot:
		addSlot, err := decodeAddSlotRequest(payload)
		if err != nil {
			return err
		}
		req.AddSlot = &addSlot
		return nil
	case controllerRPCRemoveSlot:
		removeSlot, err := decodeRemoveSlotRequest(payload)
		if err != nil {
			return err
		}
		req.RemoveSlot = &removeSlot
		return nil
	case controllerRPCListAssignments, controllerRPCListNodes, controllerRPCListRuntimeViews, controllerRPCListTasks, controllerRPCGetTask, controllerRPCForceReconcile:
		if len(payload) != 0 {
			return ErrInvalidConfig
		}
		return nil
	default:
		return ErrInvalidConfig
	}
}

func encodeControllerResponsePayload(kind string, resp controllerRPCResponse) ([]byte, error) {
	switch kind {
	case controllerRPCHeartbeat:
		return encodeHashSlotTableSync(resp.HashSlotTableVersion, resp.HashSlotTable), nil
	case controllerRPCRuntimeReport, controllerRPCOperator, controllerRPCForceReconcile, controllerRPCTaskResult,
		controllerRPCStartMigration, controllerRPCAdvanceMigration, controllerRPCFinalizeMigration,
		controllerRPCAbortMigration, controllerRPCAddSlot, controllerRPCRemoveSlot:
		return nil, nil
	case controllerRPCFetchObservationDelta:
		if resp.ObservationDelta == nil {
			return nil, nil
		}
		return encodeObservationDeltaResponse(*resp.ObservationDelta), nil
	case controllerRPCListAssignments:
		return encodeAssignmentsWithHashSlotTable(resp.Assignments, resp.HashSlotTableVersion, resp.HashSlotTable), nil
	case controllerRPCListNodes:
		return encodeClusterNodes(resp.Nodes), nil
	case controllerRPCListRuntimeViews:
		return encodeRuntimeViews(resp.RuntimeViews), nil
	case controllerRPCListTasks:
		return encodeReconcileTasks(resp.Tasks), nil
	case controllerRPCGetTask:
		if resp.Task == nil {
			return nil, nil
		}
		return encodeReconcileTask(*resp.Task), nil
	default:
		return nil, ErrInvalidConfig
	}
}

func decodeControllerResponsePayload(kind string, resp *controllerRPCResponse, payload []byte) error {
	switch kind {
	case controllerRPCHeartbeat:
		version, table, err := decodeHashSlotTableSync(payload)
		if err != nil {
			return err
		}
		resp.HashSlotTableVersion = version
		resp.HashSlotTable = table
		return nil
	case controllerRPCRuntimeReport, controllerRPCOperator, controllerRPCForceReconcile, controllerRPCTaskResult,
		controllerRPCStartMigration, controllerRPCAdvanceMigration, controllerRPCFinalizeMigration,
		controllerRPCAbortMigration, controllerRPCAddSlot, controllerRPCRemoveSlot:
		if len(payload) != 0 {
			return ErrInvalidConfig
		}
		return nil
	case controllerRPCFetchObservationDelta:
		if len(payload) == 0 {
			return nil
		}
		delta, err := decodeObservationDeltaResponse(payload)
		if err != nil {
			return err
		}
		resp.ObservationDelta = &delta
		return nil
	case controllerRPCListAssignments:
		assignments, version, table, err := decodeAssignmentsWithHashSlotTable(payload)
		if err != nil {
			return err
		}
		resp.Assignments = assignments
		resp.HashSlotTableVersion = version
		resp.HashSlotTable = table
		return nil
	case controllerRPCListNodes:
		nodes, err := decodeClusterNodes(payload)
		if err != nil {
			return err
		}
		resp.Nodes = nodes
		return nil
	case controllerRPCListRuntimeViews:
		views, err := decodeRuntimeViews(payload)
		if err != nil {
			return err
		}
		resp.RuntimeViews = views
		return nil
	case controllerRPCListTasks:
		tasks, err := decodeReconcileTasks(payload)
		if err != nil {
			return err
		}
		resp.Tasks = tasks
		return nil
	case controllerRPCGetTask:
		if len(payload) == 0 {
			return nil
		}
		task, err := decodeReconcileTask(payload)
		if err != nil {
			return err
		}
		resp.Task = &task
		return nil
	default:
		return ErrInvalidConfig
	}
}

func controllerKindCode(kind string) (byte, error) {
	switch kind {
	case controllerRPCHeartbeat:
		return controllerKindHeartbeat, nil
	case controllerRPCRuntimeReport:
		return controllerKindRuntimeReport, nil
	case controllerRPCListAssignments:
		return controllerKindListAssignments, nil
	case controllerRPCListNodes:
		return controllerKindListNodes, nil
	case controllerRPCListRuntimeViews:
		return controllerKindListRuntimeViews, nil
	case controllerRPCListTasks:
		return controllerKindListTasks, nil
	case controllerRPCFetchObservationDelta:
		return controllerKindFetchObservationDelta, nil
	case controllerRPCOperator:
		return controllerKindOperator, nil
	case controllerRPCGetTask:
		return controllerKindGetTask, nil
	case controllerRPCForceReconcile:
		return controllerKindForceReconcile, nil
	case controllerRPCTaskResult:
		return controllerKindTaskResult, nil
	case controllerRPCStartMigration:
		return controllerKindStartMigration, nil
	case controllerRPCAdvanceMigration:
		return controllerKindAdvanceMigration, nil
	case controllerRPCFinalizeMigration:
		return controllerKindFinalizeMigration, nil
	case controllerRPCAbortMigration:
		return controllerKindAbortMigration, nil
	case controllerRPCAddSlot:
		return controllerKindAddSlot, nil
	case controllerRPCRemoveSlot:
		return controllerKindRemoveSlot, nil
	default:
		return controllerKindUnknown, ErrInvalidConfig
	}
}

func controllerKindName(kind byte) (string, error) {
	switch kind {
	case controllerKindHeartbeat:
		return controllerRPCHeartbeat, nil
	case controllerKindRuntimeReport:
		return controllerRPCRuntimeReport, nil
	case controllerKindListAssignments:
		return controllerRPCListAssignments, nil
	case controllerKindListNodes:
		return controllerRPCListNodes, nil
	case controllerKindListRuntimeViews:
		return controllerRPCListRuntimeViews, nil
	case controllerKindListTasks:
		return controllerRPCListTasks, nil
	case controllerKindFetchObservationDelta:
		return controllerRPCFetchObservationDelta, nil
	case controllerKindOperator:
		return controllerRPCOperator, nil
	case controllerKindGetTask:
		return controllerRPCGetTask, nil
	case controllerKindForceReconcile:
		return controllerRPCForceReconcile, nil
	case controllerKindTaskResult:
		return controllerRPCTaskResult, nil
	case controllerKindStartMigration:
		return controllerRPCStartMigration, nil
	case controllerKindAdvanceMigration:
		return controllerRPCAdvanceMigration, nil
	case controllerKindFinalizeMigration:
		return controllerRPCFinalizeMigration, nil
	case controllerKindAbortMigration:
		return controllerRPCAbortMigration, nil
	case controllerKindAddSlot:
		return controllerRPCAddSlot, nil
	case controllerKindRemoveSlot:
		return controllerRPCRemoveSlot, nil
	default:
		return "", ErrInvalidConfig
	}
}

func readControllerPayload(body []byte) ([]byte, error) {
	payloadLen, n := binary.Uvarint(body)
	if n <= 0 {
		return nil, ErrInvalidConfig
	}
	body = body[n:]
	if len(body) != int(payloadLen) {
		return nil, ErrInvalidConfig
	}
	return body, nil
}

func encodeAgentReport(report slotcontroller.AgentReport) []byte {
	body := make([]byte, 0, 64+len(report.Addr))
	body = binary.BigEndian.AppendUint64(body, report.NodeID)
	body = appendString(body, report.Addr)
	body = appendInt64(body, report.ObservedAt.UnixNano())
	body = appendInt64(body, int64(report.CapacityWeight))
	body = binary.BigEndian.AppendUint64(body, report.HashSlotTableVersion)
	if report.Runtime != nil {
		body = append(body, 1)
		body = appendRuntimeView(body, *report.Runtime)
		return body
	}
	return append(body, 0)
}

func decodeAgentReport(body []byte) (slotcontroller.AgentReport, error) {
	nodeID, rest, err := readUint64(body)
	if err != nil {
		return slotcontroller.AgentReport{}, err
	}
	addr, rest, err := readString(rest)
	if err != nil {
		return slotcontroller.AgentReport{}, err
	}
	observedAtUnix, rest, err := readInt64(rest)
	if err != nil {
		return slotcontroller.AgentReport{}, err
	}
	capacityWeight, rest, err := readInt64(rest)
	if err != nil {
		return slotcontroller.AgentReport{}, err
	}
	hashSlotTableVersion, rest, err := readUint64(rest)
	if err != nil {
		return slotcontroller.AgentReport{}, err
	}
	if len(rest) < 1 {
		return slotcontroller.AgentReport{}, ErrInvalidConfig
	}

	report := slotcontroller.AgentReport{
		NodeID:               nodeID,
		Addr:                 addr,
		ObservedAt:           time.Unix(0, observedAtUnix),
		CapacityWeight:       int(capacityWeight),
		HashSlotTableVersion: hashSlotTableVersion,
	}
	if rest[0] == 0 {
		if len(rest) != 1 {
			return slotcontroller.AgentReport{}, ErrInvalidConfig
		}
		return report, nil
	}
	view, remaining, err := consumeRuntimeView(rest[1:])
	if err != nil || len(remaining) != 0 {
		return slotcontroller.AgentReport{}, ErrInvalidConfig
	}
	report.Runtime = &view
	return report, nil
}

func encodeRuntimeObservationReport(report runtimeObservationReport) []byte {
	body := make([]byte, 0, 32+len(report.Views)*48+len(report.ClosedSlots)*4)
	body = binary.BigEndian.AppendUint64(body, report.NodeID)
	body = appendInt64(body, report.ObservedAt.UnixNano())
	if report.FullSync {
		body = append(body, 1)
	} else {
		body = append(body, 0)
	}
	body = append(body, encodeRuntimeViews(report.Views)...)
	return appendUint32Slice(body, report.ClosedSlots)
}

func decodeRuntimeObservationReport(body []byte) (runtimeObservationReport, error) {
	nodeID, rest, err := readUint64(body)
	if err != nil {
		return runtimeObservationReport{}, err
	}
	observedAtUnix, rest, err := readInt64(rest)
	if err != nil {
		return runtimeObservationReport{}, err
	}
	if len(rest) < 1 {
		return runtimeObservationReport{}, ErrInvalidConfig
	}
	fullSync := rest[0] == 1
	views, rest, err := consumeRuntimeViews(rest[1:])
	if err != nil {
		return runtimeObservationReport{}, err
	}
	closedSlots, rest, err := readUint32Slice(rest)
	if err != nil || len(rest) != 0 {
		return runtimeObservationReport{}, ErrInvalidConfig
	}
	return runtimeObservationReport{
		NodeID:      nodeID,
		ObservedAt:  time.Unix(0, observedAtUnix),
		FullSync:    fullSync,
		Views:       views,
		ClosedSlots: closedSlots,
	}, nil
}

func encodeOperatorRequest(op slotcontroller.OperatorRequest) []byte {
	body := make([]byte, 0, 9)
	body = append(body, byte(op.Kind))
	return binary.BigEndian.AppendUint64(body, op.NodeID)
}

func decodeOperatorRequest(body []byte) (slotcontroller.OperatorRequest, error) {
	if len(body) != 9 {
		return slotcontroller.OperatorRequest{}, ErrInvalidConfig
	}
	return slotcontroller.OperatorRequest{
		Kind:   slotcontroller.OperatorKind(body[0]),
		NodeID: binary.BigEndian.Uint64(body[1:9]),
	}, nil
}

func encodeTaskAdvance(advance controllerTaskAdvance) []byte {
	body := make([]byte, 0, 4+8+binary.MaxVarintLen64+len(advance.Err))
	body = binary.BigEndian.AppendUint32(body, advance.Attempt)
	body = appendInt64(body, advance.Now.UnixNano())
	body = appendString(body, advance.Err)
	return body
}

func decodeTaskAdvance(slotID uint32, body []byte) (controllerTaskAdvance, error) {
	attempt, rest, err := readUint32(body)
	if err != nil {
		return controllerTaskAdvance{}, err
	}
	nowUnix, rest, err := readInt64(rest)
	if err != nil {
		return controllerTaskAdvance{}, err
	}
	taskErr, rest, err := readString(rest)
	if err != nil || len(rest) != 0 {
		return controllerTaskAdvance{}, ErrInvalidConfig
	}
	return controllerTaskAdvance{
		SlotID:  slotID,
		Attempt: attempt,
		Now:     time.Unix(0, nowUnix),
		Err:     taskErr,
	}, nil
}

func encodeObservationDeltaRequest(req observationDeltaRequest) []byte {
	body := make([]byte, 0, 8*6+1+len(req.RequestedSlots)*4)
	body = binary.BigEndian.AppendUint64(body, req.LeaderID)
	body = binary.BigEndian.AppendUint64(body, req.LeaderGeneration)
	body = appendObservationRevisions(body, req.Revisions)
	if req.ForceFullSync {
		body = append(body, 1)
	} else {
		body = append(body, 0)
	}
	return appendUint32Slice(body, req.RequestedSlots)
}

func decodeObservationDeltaRequest(body []byte) (observationDeltaRequest, error) {
	leaderID, rest, err := readUint64(body)
	if err != nil {
		return observationDeltaRequest{}, err
	}
	leaderGeneration, rest, err := readUint64(rest)
	if err != nil {
		return observationDeltaRequest{}, err
	}
	revisions, rest, err := readObservationRevisions(rest)
	if err != nil {
		return observationDeltaRequest{}, err
	}
	if len(rest) < 1 {
		return observationDeltaRequest{}, ErrInvalidConfig
	}
	forceFullSync := rest[0] == 1
	requestedSlots, rest, err := readUint32Slice(rest[1:])
	if err != nil || len(rest) != 0 {
		return observationDeltaRequest{}, ErrInvalidConfig
	}
	return observationDeltaRequest{
		LeaderID:         leaderID,
		LeaderGeneration: leaderGeneration,
		Revisions:        revisions,
		RequestedSlots:   requestedSlots,
		ForceFullSync:    forceFullSync,
	}, nil
}

func encodeObservationDeltaResponse(resp observationDeltaResponse) []byte {
	body := make([]byte, 0, 8*6+1)
	body = binary.BigEndian.AppendUint64(body, resp.LeaderID)
	body = binary.BigEndian.AppendUint64(body, resp.LeaderGeneration)
	body = appendObservationRevisions(body, resp.Revisions)
	if resp.FullSync {
		body = append(body, 1)
	} else {
		body = append(body, 0)
	}
	body = appendBytes(body, encodeAssignments(resp.Assignments))
	body = appendBytes(body, encodeReconcileTasks(resp.Tasks))
	body = appendBytes(body, encodeClusterNodes(resp.Nodes))
	body = appendBytes(body, encodeRuntimeViews(resp.RuntimeViews))
	body = appendUint32Slice(body, resp.DeletedTasks)
	return appendUint32Slice(body, resp.DeletedRuntimeSlots)
}

func decodeObservationDeltaResponse(body []byte) (observationDeltaResponse, error) {
	leaderID, rest, err := readUint64(body)
	if err != nil {
		return observationDeltaResponse{}, err
	}
	leaderGeneration, rest, err := readUint64(rest)
	if err != nil {
		return observationDeltaResponse{}, err
	}
	revisions, rest, err := readObservationRevisions(rest)
	if err != nil {
		return observationDeltaResponse{}, err
	}
	if len(rest) < 1 {
		return observationDeltaResponse{}, ErrInvalidConfig
	}
	fullSync := rest[0] == 1
	rest = rest[1:]

	assignmentsBody, rest, err := readBytes(rest)
	if err != nil {
		return observationDeltaResponse{}, err
	}
	assignments, err := decodeAssignments(assignmentsBody)
	if err != nil {
		return observationDeltaResponse{}, err
	}
	tasksBody, rest, err := readBytes(rest)
	if err != nil {
		return observationDeltaResponse{}, err
	}
	tasks, err := decodeReconcileTasks(tasksBody)
	if err != nil {
		return observationDeltaResponse{}, err
	}
	nodesBody, rest, err := readBytes(rest)
	if err != nil {
		return observationDeltaResponse{}, err
	}
	nodes, err := decodeClusterNodes(nodesBody)
	if err != nil {
		return observationDeltaResponse{}, err
	}
	runtimeViewsBody, rest, err := readBytes(rest)
	if err != nil {
		return observationDeltaResponse{}, err
	}
	runtimeViews, err := decodeRuntimeViews(runtimeViewsBody)
	if err != nil {
		return observationDeltaResponse{}, err
	}
	deletedTasks, rest, err := readUint32Slice(rest)
	if err != nil {
		return observationDeltaResponse{}, err
	}
	deletedRuntimeSlots, rest, err := readUint32Slice(rest)
	if err != nil || len(rest) != 0 {
		return observationDeltaResponse{}, ErrInvalidConfig
	}
	return observationDeltaResponse{
		LeaderID:            leaderID,
		LeaderGeneration:    leaderGeneration,
		Revisions:           revisions,
		FullSync:            fullSync,
		Assignments:         assignments,
		Tasks:               tasks,
		Nodes:               nodes,
		RuntimeViews:        runtimeViews,
		DeletedTasks:        deletedTasks,
		DeletedRuntimeSlots: deletedRuntimeSlots,
	}, nil
}

func encodeMigrationRequest(req slotcontroller.MigrationRequest) []byte {
	body := make([]byte, 0, 2+8+8+1)
	body = binary.BigEndian.AppendUint16(body, req.HashSlot)
	body = binary.BigEndian.AppendUint64(body, req.Source)
	body = binary.BigEndian.AppendUint64(body, req.Target)
	return append(body, req.Phase)
}

func decodeMigrationRequest(body []byte) (slotcontroller.MigrationRequest, error) {
	if len(body) != 19 {
		return slotcontroller.MigrationRequest{}, ErrInvalidConfig
	}
	return slotcontroller.MigrationRequest{
		HashSlot: binary.BigEndian.Uint16(body[0:2]),
		Source:   binary.BigEndian.Uint64(body[2:10]),
		Target:   binary.BigEndian.Uint64(body[10:18]),
		Phase:    body[18],
	}, nil
}

func encodeAddSlotRequest(req slotcontroller.AddSlotRequest) []byte {
	body := make([]byte, 0, 8+len(req.Peers)*8)
	body = binary.BigEndian.AppendUint64(body, req.NewSlotID)
	return appendUint64Slice(body, req.Peers)
}

func decodeAddSlotRequest(body []byte) (slotcontroller.AddSlotRequest, error) {
	newSlotID, rest, err := readUint64(body)
	if err != nil {
		return slotcontroller.AddSlotRequest{}, err
	}
	peers, rest, err := readUint64Slice(rest)
	if err != nil || len(rest) != 0 {
		return slotcontroller.AddSlotRequest{}, ErrInvalidConfig
	}
	return slotcontroller.AddSlotRequest{NewSlotID: newSlotID, Peers: peers}, nil
}

func encodeRemoveSlotRequest(req slotcontroller.RemoveSlotRequest) []byte {
	return binary.BigEndian.AppendUint64(nil, req.SlotID)
}

func decodeRemoveSlotRequest(body []byte) (slotcontroller.RemoveSlotRequest, error) {
	if len(body) != 8 {
		return slotcontroller.RemoveSlotRequest{}, ErrInvalidConfig
	}
	return slotcontroller.RemoveSlotRequest{SlotID: binary.BigEndian.Uint64(body)}, nil
}

func encodeHashSlotTableSync(version uint64, table []byte) []byte {
	body := make([]byte, 0, 8+binary.MaxVarintLen64+len(table))
	body = binary.BigEndian.AppendUint64(body, version)
	return appendBytes(body, table)
}

func decodeHashSlotTableSync(body []byte) (uint64, []byte, error) {
	version, rest, err := readUint64(body)
	if err != nil {
		return 0, nil, err
	}
	table, rest, err := readBytes(rest)
	if err != nil || len(rest) != 0 {
		return 0, nil, ErrInvalidConfig
	}
	return version, table, nil
}

func encodeAssignmentsWithHashSlotTable(assignments []controllermeta.SlotAssignment, version uint64, table []byte) []byte {
	body := encodeAssignments(assignments)
	body = binary.BigEndian.AppendUint64(body, version)
	return appendBytes(body, table)
}

func decodeAssignmentsWithHashSlotTable(body []byte) ([]controllermeta.SlotAssignment, uint64, []byte, error) {
	count, rest, err := readUvarint(body)
	if err != nil {
		return nil, 0, nil, err
	}
	assignments := make([]controllermeta.SlotAssignment, 0, count)
	for i := uint64(0); i < count; i++ {
		assignment, next, err := consumeAssignment(rest)
		if err != nil {
			return nil, 0, nil, err
		}
		assignments = append(assignments, assignment)
		rest = next
	}
	version, rest, err := readUint64(rest)
	if err != nil {
		return nil, 0, nil, err
	}
	table, rest, err := readBytes(rest)
	if err != nil || len(rest) != 0 {
		return nil, 0, nil, ErrInvalidConfig
	}
	return assignments, version, table, nil
}

func encodeClusterNodes(nodes []controllermeta.ClusterNode) []byte {
	body := binary.AppendUvarint(make([]byte, 0, len(nodes)*32), uint64(len(nodes)))
	for _, node := range nodes {
		body = appendClusterNode(body, node)
	}
	return body
}

func decodeClusterNodes(body []byte) ([]controllermeta.ClusterNode, error) {
	count, rest, err := readUvarint(body)
	if err != nil {
		return nil, err
	}
	nodes := make([]controllermeta.ClusterNode, 0, count)
	for i := uint64(0); i < count; i++ {
		node, next, err := consumeClusterNode(rest)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, node)
		rest = next
	}
	if len(rest) != 0 {
		return nil, ErrInvalidConfig
	}
	return nodes, nil
}

func appendClusterNode(dst []byte, node controllermeta.ClusterNode) []byte {
	dst = binary.BigEndian.AppendUint64(dst, node.NodeID)
	dst = appendString(dst, node.Addr)
	dst = append(dst, byte(node.Status))
	dst = appendInt64(dst, node.LastHeartbeatAt.UnixNano())
	return appendInt64(dst, int64(node.CapacityWeight))
}

func consumeClusterNode(body []byte) (controllermeta.ClusterNode, []byte, error) {
	nodeID, rest, err := readUint64(body)
	if err != nil {
		return controllermeta.ClusterNode{}, nil, err
	}
	addr, rest, err := readString(rest)
	if err != nil {
		return controllermeta.ClusterNode{}, nil, err
	}
	if len(rest) < 1 {
		return controllermeta.ClusterNode{}, nil, ErrInvalidConfig
	}
	status := controllermeta.NodeStatus(rest[0])
	lastHeartbeatAtUnix, rest, err := readInt64(rest[1:])
	if err != nil {
		return controllermeta.ClusterNode{}, nil, err
	}
	capacityWeight, rest, err := readInt64(rest)
	if err != nil {
		return controllermeta.ClusterNode{}, nil, err
	}
	return controllermeta.ClusterNode{
		NodeID:          nodeID,
		Addr:            addr,
		Status:          status,
		LastHeartbeatAt: time.Unix(0, lastHeartbeatAtUnix),
		CapacityWeight:  int(capacityWeight),
	}, rest, nil
}

func encodeAssignments(assignments []controllermeta.SlotAssignment) []byte {
	body := binary.AppendUvarint(make([]byte, 0, len(assignments)*32), uint64(len(assignments)))
	for _, assignment := range assignments {
		body = appendAssignment(body, assignment)
	}
	return body
}

func decodeAssignments(body []byte) ([]controllermeta.SlotAssignment, error) {
	count, rest, err := readUvarint(body)
	if err != nil {
		return nil, err
	}
	assignments := make([]controllermeta.SlotAssignment, 0, count)
	for i := uint64(0); i < count; i++ {
		assignment, next, err := consumeAssignment(rest)
		if err != nil {
			return nil, err
		}
		assignments = append(assignments, assignment)
		rest = next
	}
	if len(rest) != 0 {
		return nil, ErrInvalidConfig
	}
	return assignments, nil
}

func appendAssignment(dst []byte, assignment controllermeta.SlotAssignment) []byte {
	dst = binary.BigEndian.AppendUint32(dst, assignment.SlotID)
	dst = appendUint64Slice(dst, assignment.DesiredPeers)
	dst = binary.BigEndian.AppendUint64(dst, assignment.ConfigEpoch)
	return binary.BigEndian.AppendUint64(dst, assignment.BalanceVersion)
}

func consumeAssignment(body []byte) (controllermeta.SlotAssignment, []byte, error) {
	slotID, rest, err := readUint32(body)
	if err != nil {
		return controllermeta.SlotAssignment{}, nil, err
	}
	desiredPeers, rest, err := readUint64Slice(rest)
	if err != nil {
		return controllermeta.SlotAssignment{}, nil, err
	}
	configEpoch, rest, err := readUint64(rest)
	if err != nil {
		return controllermeta.SlotAssignment{}, nil, err
	}
	balanceVersion, rest, err := readUint64(rest)
	if err != nil {
		return controllermeta.SlotAssignment{}, nil, err
	}
	return controllermeta.SlotAssignment{
		SlotID:         slotID,
		DesiredPeers:   desiredPeers,
		ConfigEpoch:    configEpoch,
		BalanceVersion: balanceVersion,
	}, rest, nil
}

func encodeRuntimeViews(views []controllermeta.SlotRuntimeView) []byte {
	body := binary.AppendUvarint(make([]byte, 0, len(views)*40), uint64(len(views)))
	for _, view := range views {
		body = appendRuntimeView(body, view)
	}
	return body
}

func decodeRuntimeViews(body []byte) ([]controllermeta.SlotRuntimeView, error) {
	views, rest, err := consumeRuntimeViews(body)
	if err != nil {
		return nil, err
	}
	if len(rest) != 0 {
		return nil, ErrInvalidConfig
	}
	return views, nil
}

func consumeRuntimeViews(body []byte) ([]controllermeta.SlotRuntimeView, []byte, error) {
	count, rest, err := readUvarint(body)
	if err != nil {
		return nil, nil, err
	}
	views := make([]controllermeta.SlotRuntimeView, 0, count)
	for i := uint64(0); i < count; i++ {
		view, next, err := consumeRuntimeView(rest)
		if err != nil {
			return nil, nil, err
		}
		views = append(views, view)
		rest = next
	}
	return views, rest, nil
}

func encodeReconcileTasks(tasks []controllermeta.ReconcileTask) []byte {
	body := binary.AppendUvarint(make([]byte, 0, len(tasks)*40), uint64(len(tasks)))
	for _, task := range tasks {
		body = appendBytes(body, encodeReconcileTask(task))
	}
	return body
}

func decodeReconcileTasks(body []byte) ([]controllermeta.ReconcileTask, error) {
	count, rest, err := readUvarint(body)
	if err != nil {
		return nil, err
	}
	tasks := make([]controllermeta.ReconcileTask, 0, count)
	for i := uint64(0); i < count; i++ {
		taskBody, next, err := readBytes(rest)
		if err != nil {
			return nil, err
		}
		task, err := decodeReconcileTask(taskBody)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
		rest = next
	}
	if len(rest) != 0 {
		return nil, ErrInvalidConfig
	}
	return tasks, nil
}

func appendRuntimeView(dst []byte, view controllermeta.SlotRuntimeView) []byte {
	dst = binary.BigEndian.AppendUint32(dst, view.SlotID)
	dst = appendUint64Slice(dst, view.CurrentPeers)
	dst = binary.BigEndian.AppendUint64(dst, view.LeaderID)
	dst = binary.BigEndian.AppendUint32(dst, view.HealthyVoters)
	if view.HasQuorum {
		dst = append(dst, 1)
	} else {
		dst = append(dst, 0)
	}
	dst = binary.BigEndian.AppendUint64(dst, view.ObservedConfigEpoch)
	return appendInt64(dst, view.LastReportAt.UnixNano())
}

func consumeRuntimeView(body []byte) (controllermeta.SlotRuntimeView, []byte, error) {
	slotID, rest, err := readUint32(body)
	if err != nil {
		return controllermeta.SlotRuntimeView{}, nil, err
	}
	currentPeers, rest, err := readUint64Slice(rest)
	if err != nil {
		return controllermeta.SlotRuntimeView{}, nil, err
	}
	leaderID, rest, err := readUint64(rest)
	if err != nil {
		return controllermeta.SlotRuntimeView{}, nil, err
	}
	healthyVoters, rest, err := readUint32(rest)
	if err != nil {
		return controllermeta.SlotRuntimeView{}, nil, err
	}
	if len(rest) < 1 {
		return controllermeta.SlotRuntimeView{}, nil, ErrInvalidConfig
	}
	hasQuorum := rest[0] == 1
	observedConfigEpoch, rest, err := readUint64(rest[1:])
	if err != nil {
		return controllermeta.SlotRuntimeView{}, nil, err
	}
	lastReportAtUnix, rest, err := readInt64(rest)
	if err != nil {
		return controllermeta.SlotRuntimeView{}, nil, err
	}
	return controllermeta.SlotRuntimeView{
		SlotID:              slotID,
		CurrentPeers:        currentPeers,
		LeaderID:            leaderID,
		HealthyVoters:       healthyVoters,
		HasQuorum:           hasQuorum,
		ObservedConfigEpoch: observedConfigEpoch,
		LastReportAt:        time.Unix(0, lastReportAtUnix),
	}, rest, nil
}

func encodeReconcileTask(task controllermeta.ReconcileTask) []byte {
	body := make([]byte, 0, 40+len(task.LastError))
	body = binary.BigEndian.AppendUint32(body, task.SlotID)
	body = append(body, byte(task.Kind), byte(task.Step))
	body = binary.BigEndian.AppendUint64(body, task.SourceNode)
	body = binary.BigEndian.AppendUint64(body, task.TargetNode)
	body = binary.BigEndian.AppendUint32(body, task.Attempt)
	body = appendInt64(body, task.NextRunAt.UnixNano())
	body = append(body, byte(task.Status))
	return appendString(body, task.LastError)
}

func decodeReconcileTask(body []byte) (controllermeta.ReconcileTask, error) {
	slotID, rest, err := readUint32(body)
	if err != nil {
		return controllermeta.ReconcileTask{}, err
	}
	if len(rest) < 2 {
		return controllermeta.ReconcileTask{}, ErrInvalidConfig
	}
	kind := controllermeta.TaskKind(rest[0])
	step := controllermeta.TaskStep(rest[1])
	sourceNode, rest, err := readUint64(rest[2:])
	if err != nil {
		return controllermeta.ReconcileTask{}, err
	}
	targetNode, rest, err := readUint64(rest)
	if err != nil {
		return controllermeta.ReconcileTask{}, err
	}
	attempt, rest, err := readUint32(rest)
	if err != nil {
		return controllermeta.ReconcileTask{}, err
	}
	nextRunAtUnix, rest, err := readInt64(rest)
	if err != nil {
		return controllermeta.ReconcileTask{}, err
	}
	if len(rest) < 1 {
		return controllermeta.ReconcileTask{}, ErrInvalidConfig
	}
	status := controllermeta.TaskStatus(rest[0])
	lastError, rest, err := readString(rest[1:])
	if err != nil || len(rest) != 0 {
		return controllermeta.ReconcileTask{}, ErrInvalidConfig
	}
	return controllermeta.ReconcileTask{
		SlotID:     slotID,
		Kind:       kind,
		Step:       step,
		SourceNode: sourceNode,
		TargetNode: targetNode,
		Attempt:    attempt,
		NextRunAt:  time.Unix(0, nextRunAtUnix),
		Status:     status,
		LastError:  lastError,
	}, nil
}

func appendString(dst []byte, value string) []byte {
	dst = binary.AppendUvarint(dst, uint64(len(value)))
	return append(dst, value...)
}

func appendBytes(dst []byte, value []byte) []byte {
	dst = binary.AppendUvarint(dst, uint64(len(value)))
	return append(dst, value...)
}

func readString(src []byte) (string, []byte, error) {
	length, rest, err := readUvarint(src)
	if err != nil {
		return "", nil, err
	}
	if len(rest) < int(length) {
		return "", nil, ErrInvalidConfig
	}
	return string(rest[:length]), rest[length:], nil
}

func readBytes(src []byte) ([]byte, []byte, error) {
	length, rest, err := readUvarint(src)
	if err != nil {
		return nil, nil, err
	}
	if len(rest) < int(length) {
		return nil, nil, ErrInvalidConfig
	}
	return append([]byte(nil), rest[:length]...), rest[length:], nil
}

func appendUint64Slice(dst []byte, values []uint64) []byte {
	dst = binary.AppendUvarint(dst, uint64(len(values)))
	for _, value := range values {
		dst = binary.BigEndian.AppendUint64(dst, value)
	}
	return dst
}

func readUint64Slice(src []byte) ([]uint64, []byte, error) {
	count, rest, err := readUvarint(src)
	if err != nil {
		return nil, nil, err
	}
	values := make([]uint64, 0, count)
	for i := uint64(0); i < count; i++ {
		value, next, err := readUint64(rest)
		if err != nil {
			return nil, nil, err
		}
		values = append(values, value)
		rest = next
	}
	return values, rest, nil
}

func appendUint32Slice(dst []byte, values []uint32) []byte {
	dst = binary.AppendUvarint(dst, uint64(len(values)))
	for _, value := range values {
		dst = binary.BigEndian.AppendUint32(dst, value)
	}
	return dst
}

func readUint32Slice(src []byte) ([]uint32, []byte, error) {
	count, rest, err := readUvarint(src)
	if err != nil {
		return nil, nil, err
	}
	values := make([]uint32, 0, count)
	for i := uint64(0); i < count; i++ {
		value, next, err := readUint32(rest)
		if err != nil {
			return nil, nil, err
		}
		values = append(values, value)
		rest = next
	}
	return values, rest, nil
}

func appendObservationRevisions(dst []byte, revisions observationRevisions) []byte {
	dst = binary.BigEndian.AppendUint64(dst, revisions.Assignments)
	dst = binary.BigEndian.AppendUint64(dst, revisions.Tasks)
	dst = binary.BigEndian.AppendUint64(dst, revisions.Nodes)
	return binary.BigEndian.AppendUint64(dst, revisions.Runtime)
}

func readObservationRevisions(src []byte) (observationRevisions, []byte, error) {
	assignments, rest, err := readUint64(src)
	if err != nil {
		return observationRevisions{}, nil, err
	}
	tasks, rest, err := readUint64(rest)
	if err != nil {
		return observationRevisions{}, nil, err
	}
	nodes, rest, err := readUint64(rest)
	if err != nil {
		return observationRevisions{}, nil, err
	}
	runtime, rest, err := readUint64(rest)
	if err != nil {
		return observationRevisions{}, nil, err
	}
	return observationRevisions{
		Assignments: assignments,
		Tasks:       tasks,
		Nodes:       nodes,
		Runtime:     runtime,
	}, rest, nil
}

func appendInt64(dst []byte, value int64) []byte {
	return binary.BigEndian.AppendUint64(dst, uint64(value))
}

func readInt64(src []byte) (int64, []byte, error) {
	value, rest, err := readUint64(src)
	return int64(value), rest, err
}

func readUvarint(src []byte) (uint64, []byte, error) {
	value, n := binary.Uvarint(src)
	if n <= 0 {
		return 0, nil, ErrInvalidConfig
	}
	return value, src[n:], nil
}

func readUint64(src []byte) (uint64, []byte, error) {
	if len(src) < 8 {
		return 0, nil, ErrInvalidConfig
	}
	return binary.BigEndian.Uint64(src[:8]), src[8:], nil
}

func readUint32(src []byte) (uint32, []byte, error) {
	if len(src) < 4 {
		return 0, nil, ErrInvalidConfig
	}
	return binary.BigEndian.Uint32(src[:4]), src[4:], nil
}
