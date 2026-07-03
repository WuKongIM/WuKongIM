package management

import (
	"context"
	"strings"

	channelv2 "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const channelRuntimeMetaFilteredMinScanLimit = 64

// ChannelRuntimeMetaReader exposes authoritative channel runtime metadata scans.
type ChannelRuntimeMetaReader interface {
	// ScanChannelRuntimeMetaSlotPage returns one runtime metadata page for a physical Slot.
	ScanChannelRuntimeMetaSlotPage(ctx context.Context, slotID uint32, after metadb.ChannelRuntimeMetaCursor, limit int) ([]metadb.ChannelRuntimeMeta, metadb.ChannelRuntimeMetaCursor, bool, error)
}

// MessageMetaMaxSeqReader reads max sequence from an already-loaded runtime meta row.
type MessageMetaMaxSeqReader interface {
	// MaxMessageSeqForMeta returns the highest committed message sequence for the channel.
	MaxMessageSeqForMeta(ctx context.Context, meta metadb.ChannelRuntimeMeta) (uint64, error)
}

// ChannelRuntimeMeta is the manager-facing channel runtime metadata DTO.
type ChannelRuntimeMeta struct {
	// ChannelID is the logical channel identifier.
	ChannelID string
	// ChannelType is the logical channel type.
	ChannelType int64
	// SlotID is the physical Slot that owns the channel metadata.
	SlotID uint32
	// ChannelEpoch is the runtime channel epoch.
	ChannelEpoch uint64
	// LeaderEpoch is the runtime leader epoch.
	LeaderEpoch uint64
	// Leader is the current leader node ID.
	Leader uint64
	// SlotLeader is the currently observed physical Slot Raft leader.
	SlotLeader uint64
	// PreferredLeader is the control-plane preferred physical Slot leader.
	PreferredLeader uint64
	// Replicas is the configured replica set.
	Replicas []uint64
	// ISR is the current in-sync replica set.
	ISR []uint64
	// MinISR is the configured minimum in-sync replica count.
	MinISR int64
	// MaxMessageSeq is the maximum committed message sequence when requested.
	MaxMessageSeq *uint64
	// Status is the stable manager-facing runtime status string.
	Status string
	// WriteFenceToken identifies the active write-fence owner when present.
	WriteFenceToken string
	// WriteFenceVersion is the active write-fence generation when present.
	WriteFenceVersion uint64
	// WriteFenceReason is the stable manager-facing write-fence reason.
	WriteFenceReason string
	// ActiveTaskID is the active migration task for this channel when available.
	ActiveTaskID string
	// Degraded reports whether the channel runtime metadata has fewer ISR than replicas.
	Degraded bool
	// DegradedReason is a bounded manager-facing degraded explanation.
	DegradedReason string
}

// ChannelRuntimeMetaListCursor identifies the next manager runtime meta list position.
type ChannelRuntimeMetaListCursor struct {
	// SlotID is the current physical Slot scan position.
	SlotID uint32
	// ChannelID is the last emitted channel ID inside SlotID.
	ChannelID string
	// ChannelType is the last emitted channel type inside SlotID.
	ChannelType int64
}

// ChannelRuntimeMetaNodeScope controls how a node_id filter matches runtime metadata.
type ChannelRuntimeMetaNodeScope string

const (
	// ChannelRuntimeMetaNodeScopeAny matches leader, replica, or ISR membership.
	ChannelRuntimeMetaNodeScopeAny ChannelRuntimeMetaNodeScope = "any"
	// ChannelRuntimeMetaNodeScopeLeader matches only channel leaders.
	ChannelRuntimeMetaNodeScopeLeader ChannelRuntimeMetaNodeScope = "leader"
	// ChannelRuntimeMetaNodeScopeReplica matches configured replicas.
	ChannelRuntimeMetaNodeScopeReplica ChannelRuntimeMetaNodeScope = "replica"
	// ChannelRuntimeMetaNodeScopeISR matches in-sync replicas.
	ChannelRuntimeMetaNodeScopeISR ChannelRuntimeMetaNodeScope = "isr"
)

// ListChannelRuntimeMetaRequest configures a manager channel runtime page request.
type ListChannelRuntimeMetaRequest struct {
	// Limit is the maximum number of items to return.
	Limit int
	// Cursor is the optional resume position from the previous page.
	Cursor ChannelRuntimeMetaListCursor
	// ChannelIDQuery optionally limits results to channel IDs containing this substring.
	ChannelIDQuery string
	// NodeID optionally limits results to runtime metadata associated with this node.
	NodeID uint64
	// NodeScope selects how NodeID should match channel runtime metadata.
	NodeScope ChannelRuntimeMetaNodeScope
	// IncludeMaxMessageSeq controls whether rows include per-channel max message sequence.
	IncludeMaxMessageSeq bool
}

// ListChannelRuntimeMetaResponse is the manager channel runtime page result.
type ListChannelRuntimeMetaResponse struct {
	// Items contains the ordered page items.
	Items []ChannelRuntimeMeta
	// HasMore reports whether another page exists after this one.
	HasMore bool
	// NextCursor identifies the next page position when HasMore is true.
	NextCursor ChannelRuntimeMetaListCursor
}

// ListChannelRuntimeMeta returns a manager-facing page ordered by Slot and channel key.
func (a *App) ListChannelRuntimeMeta(ctx context.Context, req ListChannelRuntimeMetaRequest) (ListChannelRuntimeMetaResponse, error) {
	if err := ctxErr(ctx); err != nil {
		return ListChannelRuntimeMetaResponse{}, err
	}
	if a == nil || a.cluster == nil || a.channelRuntimeMeta == nil {
		return ListChannelRuntimeMetaResponse{}, nil
	}
	if req.Limit <= 0 {
		return ListChannelRuntimeMetaResponse{}, metadb.ErrInvalidArgument
	}
	if err := validateChannelRuntimeMetaListCursor(req.Cursor); err != nil {
		return ListChannelRuntimeMetaResponse{}, err
	}
	nodeScope, err := normalizeChannelRuntimeMetaNodeScope(req.NodeScope, req.NodeID)
	if err != nil {
		return ListChannelRuntimeMetaResponse{}, err
	}

	snapshot, err := a.cluster.LocalControlSnapshot(ctx)
	if err != nil {
		return ListChannelRuntimeMetaResponse{}, err
	}
	slotIDs := sortedSnapshotSlotIDs(snapshot.Slots)
	slotAssignments := channelRuntimeSlotAssignmentsByID(snapshot.Slots)
	startIndex, err := channelStartSlotIndex(slotIDs, req.Cursor.SlotID)
	if err != nil {
		return ListChannelRuntimeMetaResponse{}, err
	}

	filter := channelRuntimeMetaListFilter{
		channelIDQuery:       strings.TrimSpace(req.ChannelIDQuery),
		nodeID:               req.NodeID,
		nodeScope:            nodeScope,
		includeMaxMessageSeq: req.IncludeMaxMessageSeq,
	}
	return a.listChannelRuntimeMetaFiltered(ctx, slotIDs, slotAssignments, startIndex, req.Cursor, req.Limit, filter)
}

type channelRuntimeMetaListFilter struct {
	channelIDQuery       string
	nodeID               uint64
	nodeScope            ChannelRuntimeMetaNodeScope
	includeMaxMessageSeq bool
}

func (a *App) listChannelRuntimeMetaFiltered(
	ctx context.Context,
	slotIDs []uint32,
	slotAssignments map[uint32]control.SlotAssignment,
	startIndex int,
	cursor ChannelRuntimeMetaListCursor,
	limit int,
	filter channelRuntimeMetaListFilter,
) (ListChannelRuntimeMetaResponse, error) {
	resp := ListChannelRuntimeMetaResponse{Items: make([]ChannelRuntimeMeta, 0, limit)}
	for i := startIndex; i < len(slotIDs); i++ {
		slotID := slotIDs[i]
		after := metadb.ChannelRuntimeMetaCursor{}
		if i == startIndex {
			after = cursor.shardCursor()
		}
		for {
			page, nextCursor, done, err := a.channelRuntimeMeta.ScanChannelRuntimeMetaSlotPage(ctx, slotID, after, channelRuntimeMetaScanLimit(limit))
			if err != nil {
				return ListChannelRuntimeMetaResponse{}, err
			}
			items, err := a.managerChannelRuntimeMetaItems(ctx, slotID, slotAssignments[slotID], page, filter.includeMaxMessageSeq)
			if err != nil {
				return ListChannelRuntimeMetaResponse{}, err
			}
			for _, item := range items {
				if filter.channelIDQuery != "" && !strings.Contains(item.ChannelID, filter.channelIDQuery) {
					continue
				}
				if filter.nodeID != 0 && !channelRuntimeMetaItemMatchesNode(item, filter.nodeID, filter.nodeScope) {
					continue
				}
				if len(resp.Items) == limit {
					resp.HasMore = true
					resp.NextCursor = channelRuntimeMetaListCursorForItem(resp.Items[len(resp.Items)-1])
					return resp, nil
				}
				resp.Items = append(resp.Items, item)
			}
			if done {
				break
			}
			if nextCursor == after {
				break
			}
			after = nextCursor
		}
	}
	return resp, nil
}

func validateChannelRuntimeMetaListCursor(cursor ChannelRuntimeMetaListCursor) error {
	if cursor.SlotID == 0 {
		if cursor.ChannelID == "" && cursor.ChannelType == 0 {
			return nil
		}
		return metadb.ErrInvalidArgument
	}
	if cursor.ChannelID == "" && cursor.ChannelType != 0 {
		return metadb.ErrInvalidArgument
	}
	return nil
}

func (a *App) managerChannelRuntimeMetaItems(ctx context.Context, slotID uint32, assignment control.SlotAssignment, metas []metadb.ChannelRuntimeMeta, includeMaxMessageSeq bool) ([]ChannelRuntimeMeta, error) {
	out := make([]ChannelRuntimeMeta, 0, len(metas))
	slotLeader := a.actualSlotLeaderID(ctx, assignment)
	for _, meta := range metas {
		var maxMessageSeq *uint64
		if includeMaxMessageSeq {
			value, err := a.channelMaxMessageSeqForMeta(ctx, meta)
			if err != nil {
				return nil, err
			}
			maxMessageSeq = &value
		}
		activeTaskID, err := a.channelRuntimeMetaActiveTaskID(ctx, meta)
		if err != nil {
			return nil, err
		}
		out = append(out, managerChannelRuntimeMetaWithMaxSeq(slotID, slotLeader, assignment.PreferredLeader, meta, maxMessageSeq, activeTaskID))
	}
	return out, nil
}

func managerChannelRuntimeMetaWithMaxSeq(slotID uint32, slotLeader uint64, preferredLeader uint64, meta metadb.ChannelRuntimeMeta, maxMessageSeq *uint64, activeTaskID string) ChannelRuntimeMeta {
	degraded, degradedReason := managerChannelRuntimeDegraded(meta)
	return ChannelRuntimeMeta{
		ChannelID:         meta.ChannelID,
		ChannelType:       meta.ChannelType,
		SlotID:            slotID,
		ChannelEpoch:      meta.ChannelEpoch,
		LeaderEpoch:       meta.LeaderEpoch,
		Leader:            meta.Leader,
		SlotLeader:        slotLeader,
		PreferredLeader:   preferredLeader,
		Replicas:          append([]uint64(nil), meta.Replicas...),
		ISR:               append([]uint64(nil), meta.ISR...),
		MinISR:            meta.MinISR,
		MaxMessageSeq:     maxMessageSeq,
		Status:            managerChannelRuntimeStatus(meta.Status),
		WriteFenceToken:   meta.WriteFenceToken,
		WriteFenceVersion: meta.WriteFenceVersion,
		WriteFenceReason:  managerChannelWriteFenceReason(meta.WriteFenceReason),
		ActiveTaskID:      activeTaskID,
		Degraded:          degraded,
		DegradedReason:    degradedReason,
	}
}

func channelRuntimeSlotAssignmentsByID(items []control.SlotAssignment) map[uint32]control.SlotAssignment {
	out := make(map[uint32]control.SlotAssignment, len(items))
	for _, item := range items {
		out[item.SlotID] = item
	}
	return out
}

func (a *App) channelRuntimeMetaActiveTaskID(ctx context.Context, meta metadb.ChannelRuntimeMeta) (string, error) {
	if a == nil || a.channelMigration == nil || meta.ChannelID == "" || meta.ChannelType < 0 || meta.ChannelType > int64(^uint8(0)) {
		return "", nil
	}
	task, ok, err := a.channelMigration.GetActive(ctx, channelv2.ChannelID{ID: meta.ChannelID, Type: uint8(meta.ChannelType)})
	if err != nil {
		return "", mapChannelMigrationError(err)
	}
	if !ok {
		return "", nil
	}
	return task.TaskID, nil
}

func (a *App) channelMaxMessageSeqForMeta(ctx context.Context, meta metadb.ChannelRuntimeMeta) (uint64, error) {
	if a == nil || a.messages == nil {
		return 0, nil
	}
	reader, ok := a.messages.(MessageMetaMaxSeqReader)
	if !ok {
		return 0, nil
	}
	return reader.MaxMessageSeqForMeta(ctx, meta)
}

func normalizeChannelRuntimeMetaNodeScope(scope ChannelRuntimeMetaNodeScope, nodeID uint64) (ChannelRuntimeMetaNodeScope, error) {
	if nodeID == 0 {
		if scope == "" || scope == ChannelRuntimeMetaNodeScopeAny {
			return ChannelRuntimeMetaNodeScopeAny, nil
		}
		return "", metadb.ErrInvalidArgument
	}
	if scope == "" {
		return ChannelRuntimeMetaNodeScopeAny, nil
	}
	switch scope {
	case ChannelRuntimeMetaNodeScopeAny, ChannelRuntimeMetaNodeScopeLeader, ChannelRuntimeMetaNodeScopeReplica, ChannelRuntimeMetaNodeScopeISR:
		return scope, nil
	default:
		return "", metadb.ErrInvalidArgument
	}
}

func channelRuntimeMetaItemMatchesNode(item ChannelRuntimeMeta, nodeID uint64, scope ChannelRuntimeMetaNodeScope) bool {
	switch scope {
	case ChannelRuntimeMetaNodeScopeLeader:
		return item.Leader == nodeID
	case ChannelRuntimeMetaNodeScopeReplica:
		return containsNodeID(item.Replicas, nodeID)
	case ChannelRuntimeMetaNodeScopeISR:
		return containsNodeID(item.ISR, nodeID)
	case ChannelRuntimeMetaNodeScopeAny:
		return item.Leader == nodeID || containsNodeID(item.Replicas, nodeID) || containsNodeID(item.ISR, nodeID)
	default:
		return false
	}
}

func containsNodeID(values []uint64, nodeID uint64) bool {
	for _, value := range values {
		if value == nodeID {
			return true
		}
	}
	return false
}

func managerChannelRuntimeStatus(status uint8) string {
	switch channelv2.Status(status) {
	case channelv2.StatusCreating:
		return "creating"
	case channelv2.StatusActive:
		return "active"
	case channelv2.StatusDeleting:
		return "deleting"
	case channelv2.StatusDeleted:
		return "deleted"
	default:
		return "unknown"
	}
}

func managerChannelWriteFenceReason(reason uint8) string {
	switch channelv2.WriteFenceReason(reason) {
	case channelv2.WriteFenceReasonLeaderTransfer:
		return "leader_transfer"
	case channelv2.WriteFenceReasonReplicaReplace:
		return "replica_replace"
	case channelv2.WriteFenceReasonFailover:
		return "failover"
	default:
		return ""
	}
}

func managerChannelRuntimeDegraded(meta metadb.ChannelRuntimeMeta) (bool, string) {
	if meta.MinISR > 0 && int64(len(meta.ISR)) < meta.MinISR {
		return true, "min_isr_not_met"
	}
	if len(meta.Replicas) > 0 && len(meta.ISR) < len(meta.Replicas) {
		return true, "isr_below_replicas"
	}
	return false, ""
}

func channelRuntimeMetaListCursorForItem(item ChannelRuntimeMeta) ChannelRuntimeMetaListCursor {
	return ChannelRuntimeMetaListCursor{
		SlotID:      item.SlotID,
		ChannelID:   item.ChannelID,
		ChannelType: item.ChannelType,
	}
}

func (c ChannelRuntimeMetaListCursor) shardCursor() metadb.ChannelRuntimeMetaCursor {
	return metadb.ChannelRuntimeMetaCursor{
		ChannelID:   c.ChannelID,
		ChannelType: c.ChannelType,
	}
}

func channelRuntimeMetaScanLimit(limit int) int {
	if limit > channelRuntimeMetaFilteredMinScanLimit {
		return limit
	}
	return channelRuntimeMetaFilteredMinScanLimit
}
