package meta

import (
	"encoding/binary"
	"math"
	"sort"
	"time"
)

const (
	recordVersion byte = 1

	recordPrefixNode        byte = 'n'
	recordPrefixMembership  byte = 'm'
	recordPrefixAssignment  byte = 'a'
	recordPrefixRuntimeView byte = 'v'
	recordPrefixTask        byte = 't'
	recordPrefixHashSlot    byte = 'h'
)

func encodeNodeKey(nodeID uint64) []byte {
	key := make([]byte, 1, 1+8)
	key[0] = recordPrefixNode
	return binary.BigEndian.AppendUint64(key, nodeID)
}

func decodeNodeKey(key []byte) (uint64, error) {
	if len(key) != 1+8 || key[0] != recordPrefixNode {
		return 0, ErrCorruptValue
	}
	nodeID := binary.BigEndian.Uint64(key[1:])
	if nodeID == 0 {
		return 0, ErrCorruptValue
	}
	return nodeID, nil
}

func encodeGroupKey(prefix byte, slotID uint32) []byte {
	key := make([]byte, 1, 1+4)
	key[0] = prefix
	return binary.BigEndian.AppendUint32(key, slotID)
}

func decodeGroupKey(key []byte, prefix byte) (uint32, error) {
	if len(key) != 1+4 || key[0] != prefix {
		return 0, ErrCorruptValue
	}
	slotID := binary.BigEndian.Uint32(key[1:])
	if slotID == 0 {
		return 0, ErrCorruptValue
	}
	return slotID, nil
}

func membershipKey() []byte {
	return []byte{recordPrefixMembership}
}

func hashSlotTableKey() []byte {
	return []byte{recordPrefixHashSlot}
}

func prefixBounds(prefix byte) ([]byte, []byte) {
	return []byte{prefix}, []byte{prefix + 1}
}

func validateSnapshotKey(key []byte) error {
	if len(key) == 0 {
		return ErrCorruptValue
	}
	switch key[0] {
	case recordPrefixNode:
		_, err := decodeNodeKey(key)
		return err
	case recordPrefixMembership:
		if len(key) != 1 {
			return ErrCorruptValue
		}
		return nil
	case recordPrefixHashSlot:
		if len(key) != 1 {
			return ErrCorruptValue
		}
		return nil
	case recordPrefixAssignment, recordPrefixRuntimeView, recordPrefixTask:
		_, err := decodeGroupKey(key, key[0])
		return err
	default:
		return ErrCorruptValue
	}
}

func encodeClusterNode(node ClusterNode) []byte {
	node = normalizeClusterNode(node)

	data := make([]byte, 0, 32+len(node.Addr))
	data = append(data, recordVersion)
	data = appendString(data, node.Addr)
	data = append(data, byte(node.Status))
	data = appendInt64(data, node.LastHeartbeatAt.UnixNano())
	data = appendInt64(data, int64(node.CapacityWeight))
	return data
}

func decodeClusterNode(key, data []byte) (ClusterNode, error) {
	nodeID, err := decodeNodeKey(key)
	if err != nil {
		return ClusterNode{}, err
	}
	if len(data) == 0 || data[0] != recordVersion {
		return ClusterNode{}, ErrCorruptValue
	}
	rest := data[1:]

	addr, rest, err := readString(rest)
	if err != nil {
		return ClusterNode{}, err
	}
	if addr == "" {
		return ClusterNode{}, ErrCorruptValue
	}
	if len(rest) < 1 {
		return ClusterNode{}, ErrCorruptValue
	}
	status := NodeStatus(rest[0])
	if !validNodeStatus(status) {
		return ClusterNode{}, ErrCorruptValue
	}
	rest = rest[1:]

	lastHeartbeatAt, rest, err := readInt64(rest)
	if err != nil {
		return ClusterNode{}, err
	}
	capacityWeight, rest, err := readInt64(rest)
	if err != nil {
		return ClusterNode{}, err
	}
	if len(rest) != 0 || capacityWeight <= 0 || capacityWeight > math.MaxInt {
		return ClusterNode{}, ErrCorruptValue
	}

	return ClusterNode{
		NodeID:          nodeID,
		Addr:            addr,
		Status:          status,
		LastHeartbeatAt: time.Unix(0, lastHeartbeatAt),
		CapacityWeight:  int(capacityWeight),
	}, nil
}

func encodeGroupAssignment(assignment SlotAssignment) []byte {
	assignment = normalizeGroupAssignment(assignment)

	data := make([]byte, 0, 32)
	data = append(data, recordVersion)
	data = binary.BigEndian.AppendUint64(data, assignment.ConfigEpoch)
	data = binary.BigEndian.AppendUint64(data, assignment.BalanceVersion)
	data = appendUint64Slice(data, assignment.DesiredPeers)
	return data
}

func decodeGroupAssignment(key, data []byte) (SlotAssignment, error) {
	slotID, err := decodeGroupKey(key, recordPrefixAssignment)
	if err != nil {
		return SlotAssignment{}, err
	}
	if len(data) < 1+8+8 || data[0] != recordVersion {
		return SlotAssignment{}, ErrCorruptValue
	}
	rest := data[1:]

	configEpoch := binary.BigEndian.Uint64(rest[:8])
	rest = rest[8:]
	balanceVersion := binary.BigEndian.Uint64(rest[:8])
	rest = rest[8:]
	desiredPeers, rest, err := readUint64Slice(rest)
	if err != nil || len(rest) != 0 {
		return SlotAssignment{}, ErrCorruptValue
	}

	if err := validateCanonicalPeerSet(desiredPeers, ErrCorruptValue); err != nil {
		return SlotAssignment{}, err
	}
	return SlotAssignment{
		SlotID:         slotID,
		DesiredPeers:   desiredPeers,
		ConfigEpoch:    configEpoch,
		BalanceVersion: balanceVersion,
	}, nil
}

func encodeGroupRuntimeView(view SlotRuntimeView) []byte {
	view = normalizeGroupRuntimeView(view)

	data := make([]byte, 0, 48)
	data = append(data, recordVersion)
	data = binary.BigEndian.AppendUint64(data, view.LeaderID)
	data = binary.BigEndian.AppendUint32(data, view.HealthyVoters)
	if view.HasQuorum {
		data = append(data, 1)
	} else {
		data = append(data, 0)
	}
	data = binary.BigEndian.AppendUint64(data, view.ObservedConfigEpoch)
	data = appendInt64(data, view.LastReportAt.UnixNano())
	data = appendUint64Slice(data, view.CurrentPeers)
	return data
}

func decodeGroupRuntimeView(key, data []byte) (SlotRuntimeView, error) {
	slotID, err := decodeGroupKey(key, recordPrefixRuntimeView)
	if err != nil {
		return SlotRuntimeView{}, err
	}
	if len(data) < 1+8+4+1+8+8 || data[0] != recordVersion {
		return SlotRuntimeView{}, ErrCorruptValue
	}
	rest := data[1:]

	leaderID := binary.BigEndian.Uint64(rest[:8])
	rest = rest[8:]
	healthyVoters := binary.BigEndian.Uint32(rest[:4])
	rest = rest[4:]
	if rest[0] != 0 && rest[0] != 1 {
		return SlotRuntimeView{}, ErrCorruptValue
	}
	hasQuorum := rest[0] == 1
	rest = rest[1:]
	observedConfigEpoch := binary.BigEndian.Uint64(rest[:8])
	rest = rest[8:]
	lastReportAt, rest, err := readInt64(rest)
	if err != nil {
		return SlotRuntimeView{}, err
	}
	currentPeers, rest, err := readUint64Slice(rest)
	if err != nil || len(rest) != 0 {
		return SlotRuntimeView{}, ErrCorruptValue
	}

	if err := validateCanonicalPeerSet(currentPeers, ErrCorruptValue); err != nil {
		return SlotRuntimeView{}, err
	}
	view := SlotRuntimeView{
		SlotID:              slotID,
		CurrentPeers:        currentPeers,
		LeaderID:            leaderID,
		HealthyVoters:       healthyVoters,
		HasQuorum:           hasQuorum,
		ObservedConfigEpoch: observedConfigEpoch,
		LastReportAt:        time.Unix(0, lastReportAt),
	}
	if err := validateRuntimeViewState(view, ErrCorruptValue); err != nil {
		return SlotRuntimeView{}, err
	}
	return view, nil
}

func encodeReconcileTask(task ReconcileTask) []byte {
	task = normalizeReconcileTask(task)

	data := make([]byte, 0, 56+len(task.LastError))
	data = append(data, recordVersion)
	data = append(data, byte(task.Kind))
	data = append(data, byte(task.Step))
	data = binary.BigEndian.AppendUint64(data, task.SourceNode)
	data = binary.BigEndian.AppendUint64(data, task.TargetNode)
	data = binary.BigEndian.AppendUint32(data, task.Attempt)
	data = appendString(data, task.LastError)
	data = append(data, byte(task.Status))
	data = appendInt64(data, task.NextRunAt.UnixNano())
	return data
}

func decodeReconcileTask(key, data []byte) (ReconcileTask, error) {
	slotID, err := decodeGroupKey(key, recordPrefixTask)
	if err != nil {
		return ReconcileTask{}, err
	}
	if len(data) < 1+1+1+8+8+4 || data[0] != recordVersion {
		return ReconcileTask{}, ErrCorruptValue
	}
	rest := data[1:]

	kind := TaskKind(rest[0])
	step := TaskStep(rest[1])
	if !validTaskKind(kind) || !validTaskStep(step) {
		return ReconcileTask{}, ErrCorruptValue
	}
	rest = rest[2:]
	sourceNode := binary.BigEndian.Uint64(rest[:8])
	rest = rest[8:]
	targetNode := binary.BigEndian.Uint64(rest[:8])
	rest = rest[8:]
	attempt := binary.BigEndian.Uint32(rest[:4])
	rest = rest[4:]
	lastError, rest, err := readString(rest)
	if err != nil {
		return ReconcileTask{}, ErrCorruptValue
	}
	status := TaskStatusPending
	var nextRunAt time.Time
	switch len(rest) {
	case 0:
	case 9:
		status = TaskStatus(rest[0])
		if !validTaskStatus(status) {
			return ReconcileTask{}, ErrCorruptValue
		}
		nextRunAtUnix, remaining, err := readInt64(rest[1:])
		if err != nil || len(remaining) != 0 {
			return ReconcileTask{}, ErrCorruptValue
		}
		nextRunAt = time.Unix(0, nextRunAtUnix)
	default:
		return ReconcileTask{}, ErrCorruptValue
	}

	task := ReconcileTask{
		SlotID:     slotID,
		Kind:       kind,
		Step:       step,
		SourceNode: sourceNode,
		TargetNode: targetNode,
		Attempt:    attempt,
		NextRunAt:  nextRunAt,
		Status:     status,
		LastError:  lastError,
	}
	if err := validateReconcileTaskState(task, ErrCorruptValue); err != nil {
		return ReconcileTask{}, err
	}
	return task, nil
}

func encodeControllerMembership(membership ControllerMembership) []byte {
	membership.Peers = normalizeUint64Set(membership.Peers)

	data := make([]byte, 0, 16)
	data = append(data, recordVersion)
	data = appendUint64Slice(data, membership.Peers)
	return data
}

func decodeControllerMembership(data []byte) (ControllerMembership, error) {
	if len(data) == 0 || data[0] != recordVersion {
		return ControllerMembership{}, ErrCorruptValue
	}
	peers, rest, err := readUint64Slice(data[1:])
	if err != nil || len(rest) != 0 {
		return ControllerMembership{}, ErrCorruptValue
	}
	if err := validateCanonicalPeerSet(peers, ErrCorruptValue); err != nil {
		return ControllerMembership{}, err
	}
	return ControllerMembership{Peers: peers}, nil
}

func normalizeClusterNode(node ClusterNode) ClusterNode {
	if node.CapacityWeight == 0 {
		node.CapacityWeight = 1
	}
	return node
}

func normalizeGroupAssignment(assignment SlotAssignment) SlotAssignment {
	assignment.DesiredPeers = normalizeUint64Set(assignment.DesiredPeers)
	return assignment
}

func normalizeGroupRuntimeView(view SlotRuntimeView) SlotRuntimeView {
	view.CurrentPeers = normalizeUint64Set(view.CurrentPeers)
	return view
}

func normalizeReconcileTask(task ReconcileTask) ReconcileTask {
	if task.Status == TaskStatusUnknown {
		task.Status = TaskStatusPending
	}
	return task
}

func normalizeUint64Set(values []uint64) []uint64 {
	if len(values) == 0 {
		return nil
	}

	sorted := append([]uint64(nil), values...)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i] < sorted[j]
	})

	n := 1
	for i := 1; i < len(sorted); i++ {
		if sorted[i] == sorted[n-1] {
			continue
		}
		sorted[n] = sorted[i]
		n++
	}
	return sorted[:n]
}

func validNodeStatus(status NodeStatus) bool {
	return status >= NodeStatusAlive && status <= NodeStatusDraining
}

func validTaskKind(kind TaskKind) bool {
	return kind >= TaskKindBootstrap && kind <= TaskKindRebalance
}

func validTaskStep(step TaskStep) bool {
	return step >= TaskStepAddLearner && step <= TaskStepRemoveOld
}

func validTaskStatus(status TaskStatus) bool {
	return status >= TaskStatusPending && status <= TaskStatusFailed
}

func validateReconcileTaskState(task ReconcileTask, invalid error) error {
	switch task.Status {
	case TaskStatusPending:
		return nil
	case TaskStatusRetrying:
		if task.NextRunAt.IsZero() {
			return invalid
		}
		return nil
	case TaskStatusFailed:
		if task.LastError == "" {
			return invalid
		}
		return nil
	default:
		return invalid
	}
}

func validateRequiredPeerSet(values []uint64, invalid error) error {
	if len(values) == 0 {
		return invalid
	}
	for _, value := range values {
		if value == 0 {
			return invalid
		}
	}
	return nil
}

func validateCanonicalPeerSet(values []uint64, invalid error) error {
	if err := validateRequiredPeerSet(values, invalid); err != nil {
		return err
	}
	for i := 1; i < len(values); i++ {
		if values[i-1] >= values[i] {
			return invalid
		}
	}
	return nil
}

func validateRuntimeViewState(view SlotRuntimeView, invalid error) error {
	if err := validateRequiredPeerSet(view.CurrentPeers, invalid); err != nil {
		return err
	}
	if view.LeaderID != 0 {
		idx := sort.Search(len(view.CurrentPeers), func(i int) bool {
			return view.CurrentPeers[i] >= view.LeaderID
		})
		if idx == len(view.CurrentPeers) || view.CurrentPeers[idx] != view.LeaderID {
			return invalid
		}
	}
	if view.HealthyVoters > uint32(len(view.CurrentPeers)) {
		return invalid
	}
	return nil
}

func appendString(dst []byte, value string) []byte {
	dst = binary.AppendUvarint(dst, uint64(len(value)))
	return append(dst, value...)
}

func readString(src []byte) (string, []byte, error) {
	value, rest, err := readBytes(src)
	if err != nil {
		return "", nil, err
	}
	return string(value), rest, nil
}

func appendUint64Slice(dst []byte, values []uint64) []byte {
	dst = binary.AppendUvarint(dst, uint64(len(values)))
	for _, value := range values {
		dst = binary.BigEndian.AppendUint64(dst, value)
	}
	return dst
}

func readUint64Slice(src []byte) ([]uint64, []byte, error) {
	count, n := binary.Uvarint(src)
	if n <= 0 {
		return nil, nil, ErrCorruptValue
	}
	rest := src[n:]
	if count > uint64(len(rest))/8 {
		return nil, nil, ErrCorruptValue
	}

	values := make([]uint64, 0, int(count))
	for i := uint64(0); i < count; i++ {
		values = append(values, binary.BigEndian.Uint64(rest[:8]))
		rest = rest[8:]
	}
	return values, rest, nil
}

func appendInt64(dst []byte, value int64) []byte {
	return binary.BigEndian.AppendUint64(dst, uint64(value))
}

func readInt64(src []byte) (int64, []byte, error) {
	if len(src) < 8 {
		return 0, nil, ErrCorruptValue
	}
	return int64(binary.BigEndian.Uint64(src[:8])), src[8:], nil
}

func readBytes(src []byte) ([]byte, []byte, error) {
	length, n := binary.Uvarint(src)
	if n <= 0 {
		return nil, nil, ErrCorruptValue
	}
	rest := src[n:]
	if length > uint64(len(rest)) {
		return nil, nil, ErrCorruptValue
	}
	size := int(length)
	value := append([]byte(nil), rest[:size]...)
	return value, rest[size:], nil
}
