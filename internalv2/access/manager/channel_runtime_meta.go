package manager

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/gin-gonic/gin"
)

const (
	defaultChannelRuntimeMetaLimit = 50
	maxChannelRuntimeMetaLimit     = 200
)

// ChannelRuntimeMetaListResponse is the manager channel runtime page body.
type ChannelRuntimeMetaListResponse struct {
	// Items contains the ordered page items.
	Items []ChannelRuntimeMetaDTO `json:"items"`
	// HasMore reports whether another page exists.
	HasMore bool `json:"has_more"`
	// NextCursor is the opaque cursor for the next page when HasMore is true.
	NextCursor string `json:"next_cursor,omitempty"`
}

// ChannelRuntimeMetaDTO is the manager-facing channel runtime response item.
type ChannelRuntimeMetaDTO struct {
	// ChannelID is the logical channel identifier.
	ChannelID string `json:"channel_id"`
	// ChannelType is the logical channel type.
	ChannelType int64 `json:"channel_type"`
	// SlotID is the owning physical Slot identifier.
	SlotID uint32 `json:"slot_id"`
	// ChannelEpoch is the runtime channel epoch.
	ChannelEpoch uint64 `json:"channel_epoch"`
	// LeaderEpoch is the runtime leader epoch.
	LeaderEpoch uint64 `json:"leader_epoch"`
	// Leader is the current leader node ID.
	Leader uint64 `json:"leader"`
	// SlotLeader is the currently observed physical Slot Raft leader.
	SlotLeader uint64 `json:"slot_leader,omitempty"`
	// PreferredLeader is the control-plane preferred physical Slot leader.
	PreferredLeader uint64 `json:"preferred_leader,omitempty"`
	// Replicas is the configured replica set.
	Replicas []uint64 `json:"replicas"`
	// ISR is the in-sync replica set.
	ISR []uint64 `json:"isr"`
	// MinISR is the configured minimum in-sync replica count.
	MinISR int64 `json:"min_isr"`
	// MaxMessageSeq is the maximum committed message sequence when requested.
	MaxMessageSeq *uint64 `json:"max_message_seq,omitempty"`
	// Status is the stable runtime status string.
	Status string `json:"status"`
	// WriteFenceToken identifies the active migration write fence when present.
	WriteFenceToken string `json:"write_fence_token,omitempty"`
	// WriteFenceVersion is the active migration write-fence generation.
	WriteFenceVersion uint64 `json:"write_fence_version,omitempty"`
	// WriteFenceReason is the stable write-fence reason.
	WriteFenceReason string `json:"write_fence_reason,omitempty"`
	// ActiveTaskID is the active ChannelV2 migration task when present.
	ActiveTaskID string `json:"active_task_id,omitempty"`
	// Degraded reports whether the channel has fewer ISR than replicas.
	Degraded bool `json:"degraded"`
	// DegradedReason is a bounded explanation for degraded channels.
	DegradedReason string `json:"degraded_reason,omitempty"`
}

func (s *Server) handleChannelRuntimeMeta(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	limit, err := parseChannelRuntimeMetaLimit(c.Query("limit"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid limit")
		return
	}
	cursor, err := decodeChannelRuntimeMetaCursor(c.Query("cursor"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid cursor")
		return
	}
	nodeID, err := parseOptionalConnectionNodeID(c.Query("node_id"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid node_id")
		return
	}
	nodeScope, err := parseChannelRuntimeMetaNodeScope(c.Query("node_scope"), nodeID)
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid node_scope")
		return
	}
	includeMaxMessageSeq, err := parseOptionalBool(c.Query("include_max_message_seq"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid include_max_message_seq")
		return
	}

	page, err := s.management.ListChannelRuntimeMeta(c.Request.Context(), managementusecase.ListChannelRuntimeMetaRequest{
		Limit:                limit,
		Cursor:               cursor,
		ChannelIDQuery:       strings.TrimSpace(c.Query("channel_id")),
		NodeID:               nodeID,
		NodeScope:            nodeScope,
		IncludeMaxMessageSeq: includeMaxMessageSeq,
	})
	if err != nil {
		writeChannelRuntimeMetaError(c, err)
		return
	}
	nextCursor, err := encodeChannelRuntimeMetaCursor(page.NextCursor)
	if err != nil {
		jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	c.JSON(http.StatusOK, ChannelRuntimeMetaListResponse{
		Items:      channelRuntimeMetaDTOs(page.Items),
		HasMore:    page.HasMore,
		NextCursor: nextCursor,
	})
}

func parseChannelRuntimeMetaLimit(raw string) (int, error) {
	if raw == "" {
		return defaultChannelRuntimeMetaLimit, nil
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit <= 0 || limit > maxChannelRuntimeMetaLimit {
		return 0, strconv.ErrSyntax
	}
	return limit, nil
}

func parseChannelRuntimeMetaNodeScope(raw string, nodeID uint64) (managementusecase.ChannelRuntimeMetaNodeScope, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		if nodeID == 0 {
			return "", nil
		}
		return managementusecase.ChannelRuntimeMetaNodeScopeAny, nil
	}
	scope := managementusecase.ChannelRuntimeMetaNodeScope(value)
	switch scope {
	case managementusecase.ChannelRuntimeMetaNodeScopeAny,
		managementusecase.ChannelRuntimeMetaNodeScopeLeader,
		managementusecase.ChannelRuntimeMetaNodeScopeReplica,
		managementusecase.ChannelRuntimeMetaNodeScopeISR:
		if nodeID == 0 {
			return "", strconv.ErrSyntax
		}
		return scope, nil
	default:
		return "", strconv.ErrSyntax
	}
}

func writeChannelRuntimeMetaError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, metadb.ErrInvalidArgument):
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid cursor")
	case controlSnapshotUnavailable(err), errors.Is(err, cluster.ErrSlotNotFound), errors.Is(err, cluster.ErrNotStarted):
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "channel runtime metadata unavailable")
	default:
		jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
	}
}

func channelRuntimeMetaDTOs(items []managementusecase.ChannelRuntimeMeta) []ChannelRuntimeMetaDTO {
	out := make([]ChannelRuntimeMetaDTO, 0, len(items))
	for _, item := range items {
		out = append(out, channelRuntimeMetaDTO(item))
	}
	return out
}

func channelRuntimeMetaDTO(item managementusecase.ChannelRuntimeMeta) ChannelRuntimeMetaDTO {
	return ChannelRuntimeMetaDTO{
		ChannelID:         item.ChannelID,
		ChannelType:       item.ChannelType,
		SlotID:            item.SlotID,
		ChannelEpoch:      item.ChannelEpoch,
		LeaderEpoch:       item.LeaderEpoch,
		Leader:            item.Leader,
		SlotLeader:        item.SlotLeader,
		PreferredLeader:   item.PreferredLeader,
		Replicas:          append([]uint64(nil), item.Replicas...),
		ISR:               append([]uint64(nil), item.ISR...),
		MinISR:            item.MinISR,
		MaxMessageSeq:     item.MaxMessageSeq,
		Status:            item.Status,
		WriteFenceToken:   item.WriteFenceToken,
		WriteFenceVersion: item.WriteFenceVersion,
		WriteFenceReason:  item.WriteFenceReason,
		ActiveTaskID:      item.ActiveTaskID,
		Degraded:          item.Degraded,
		DegradedReason:    item.DegradedReason,
	}
}
