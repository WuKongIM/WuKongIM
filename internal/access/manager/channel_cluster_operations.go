package manager

import (
	"errors"
	"io"
	"net/http"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/gin-gonic/gin"
)

// ChannelClusterReplicaDetailResponse is the manager replica detail response body.
type ChannelClusterReplicaDetailResponse struct {
	// Channel is authoritative channel runtime metadata.
	Channel ChannelRuntimeMetaDetailDTO `json:"channel"`
	// RuntimeReported reports whether live runtime status was proven.
	RuntimeReported bool `json:"runtime_reported"`
	// CommitSeq is the proven committed sequence, when known.
	CommitSeq *uint64 `json:"commit_seq"`
	// MinAvailableSeq is the proven minimum readable sequence, when known.
	MinAvailableSeq *uint64 `json:"min_available_seq"`
	// RetentionThroughSeq is the proven retention boundary, when known.
	RetentionThroughSeq *uint64 `json:"retention_through_seq"`
	// Replicas contains one row for each authoritative replica.
	Replicas []ChannelClusterReplicaStatusDTO `json:"replicas"`
}

// ChannelClusterReplicaStatusDTO is one manager-facing replica status row.
type ChannelClusterReplicaStatusDTO struct {
	// NodeID is the replica node ID.
	NodeID uint64 `json:"node_id"`
	// Role is the authoritative role label.
	Role string `json:"role"`
	// IsLeader reports whether this row is the authoritative leader.
	IsLeader bool `json:"is_leader"`
	// InISR reports whether this row is in the authoritative ISR set.
	InISR bool `json:"in_isr"`
	// Reported reports whether live runtime status was proven.
	Reported bool `json:"reported"`
	// CommitSeq is the proven committed sequence for this replica, when known.
	CommitSeq *uint64 `json:"commit_seq"`
	// LEO is the proven log end offset for this replica, when known.
	LEO *uint64 `json:"leo"`
	// CheckpointHW is the proven durable checkpoint high watermark, when known.
	CheckpointHW *uint64 `json:"checkpoint_hw"`
	// Lag is leader commit minus replica commit, when known.
	Lag *uint64 `json:"lag"`
}

// RepairChannelClusterLeaderRequestDTO is the manager leader repair request body.
type RepairChannelClusterLeaderRequestDTO struct {
	// Reason is the manager-facing repair reason.
	Reason string `json:"reason"`
}

// RepairChannelClusterLeaderResponseDTO is the manager leader repair response body.
type RepairChannelClusterLeaderResponseDTO struct {
	// Changed reports whether authoritative metadata changed.
	Changed bool `json:"changed"`
	// Channel is authoritative channel metadata after repair or validation.
	Channel ChannelRuntimeMetaDetailDTO `json:"channel"`
}

// TransferChannelClusterLeaderRequestDTO is the manager leader transfer request body.
type TransferChannelClusterLeaderRequestDTO struct {
	// TargetNodeID is the requested new leader.
	TargetNodeID uint64 `json:"target_node_id"`
}

// TransferChannelClusterLeaderResponseDTO is the manager leader transfer response body.
type TransferChannelClusterLeaderResponseDTO struct {
	// Changed reports whether authoritative metadata changed.
	Changed bool `json:"changed"`
	// Channel is authoritative channel metadata after transfer or validation.
	Channel ChannelRuntimeMetaDetailDTO `json:"channel"`
}

func (s *Server) handleChannelClusterReplicas(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}

	channelType, err := parseChannelRuntimeMetaChannelTypeParam(c.Param("channel_type"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid channel_type")
		return
	}

	detail, err := s.management.GetChannelClusterReplicaDetail(c.Request.Context(), c.Param("channel_id"), channelType)
	if err != nil {
		handleChannelClusterOperationError(c, err)
		return
	}
	c.JSON(http.StatusOK, channelClusterReplicaDetailDTO(detail))
}

func (s *Server) handleChannelClusterRepair(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}

	channelType, err := parseChannelRuntimeMetaChannelTypeParam(c.Param("channel_type"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid channel_type")
		return
	}

	var body RepairChannelClusterLeaderRequestDTO
	if err := c.ShouldBindJSON(&body); err != nil && !errors.Is(err, io.EOF) {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}

	result, err := s.management.RepairChannelClusterLeader(c.Request.Context(), managementusecase.RepairChannelClusterLeaderRequest{
		ChannelID:   c.Param("channel_id"),
		ChannelType: channelType,
		Reason:      body.Reason,
	})
	if err != nil {
		handleChannelClusterOperationError(c, err)
		return
	}
	c.JSON(http.StatusOK, RepairChannelClusterLeaderResponseDTO{
		Changed: result.Changed,
		Channel: channelRuntimeMetaDetailDTO(result.Channel),
	})
}

func (s *Server) handleChannelClusterLeaderTransfer(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}

	channelType, err := parseChannelRuntimeMetaChannelTypeParam(c.Param("channel_type"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid channel_type")
		return
	}

	var body TransferChannelClusterLeaderRequestDTO
	if err := c.ShouldBindJSON(&body); err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}
	if body.TargetNodeID == 0 {
		jsonError(c, http.StatusBadRequest, "bad_request", "target_node_id required")
		return
	}

	result, err := s.management.TransferChannelClusterLeader(c.Request.Context(), managementusecase.TransferChannelClusterLeaderRequest{
		ChannelID:    c.Param("channel_id"),
		ChannelType:  channelType,
		TargetNodeID: body.TargetNodeID,
	})
	if err != nil {
		handleChannelClusterOperationError(c, err)
		return
	}
	c.JSON(http.StatusOK, TransferChannelClusterLeaderResponseDTO{
		Changed: result.Changed,
		Channel: channelRuntimeMetaDetailDTO(result.Channel),
	})
}

func handleChannelClusterOperationError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, metadb.ErrInvalidArgument),
		errors.Is(err, managementusecase.ErrUnsupportedChannelClusterRepairReason),
		errors.Is(err, managementusecase.ErrChannelLeaderTransferTargetNotReplica):
		jsonError(c, http.StatusBadRequest, "bad_request", err.Error())
	case errors.Is(err, metadb.ErrNotFound):
		jsonError(c, http.StatusNotFound, "not_found", "channel runtime meta not found")
	case errors.Is(err, channel.ErrNoSafeChannelLeader),
		errors.Is(err, managementusecase.ErrChannelLeaderTransferTargetNotISR),
		errors.Is(err, managementusecase.ErrChannelLeaderTransferInactiveChannel):
		message := err.Error()
		if errors.Is(err, channel.ErrNoSafeChannelLeader) {
			message = "no safe channel leader candidate"
		}
		jsonError(c, http.StatusConflict, "conflict", message)
	case slotLeaderAuthoritativeReadUnavailable(err), leaderConsistentReadUnavailable(err):
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "slot leader authoritative read unavailable")
	default:
		jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
	}
}

func channelClusterReplicaDetailDTO(item managementusecase.ChannelClusterReplicaDetail) ChannelClusterReplicaDetailResponse {
	return ChannelClusterReplicaDetailResponse{
		Channel:             channelRuntimeMetaDetailDTO(item.Channel),
		RuntimeReported:     item.RuntimeReported,
		CommitSeq:           item.CommitSeq,
		MinAvailableSeq:     item.MinAvailableSeq,
		RetentionThroughSeq: item.RetentionThroughSeq,
		Replicas:            channelClusterReplicaStatusDTOs(item.Replicas),
	}
}

func channelClusterReplicaStatusDTOs(items []managementusecase.ChannelClusterReplicaStatus) []ChannelClusterReplicaStatusDTO {
	out := make([]ChannelClusterReplicaStatusDTO, 0, len(items))
	for _, item := range items {
		out = append(out, ChannelClusterReplicaStatusDTO{
			NodeID:       item.NodeID,
			Role:         item.Role,
			IsLeader:     item.IsLeader,
			InISR:        item.InISR,
			Reported:     item.Reported,
			CommitSeq:    item.CommitSeq,
			LEO:          item.LEO,
			CheckpointHW: item.CheckpointHW,
			Lag:          item.Lag,
		})
	}
	return out
}
