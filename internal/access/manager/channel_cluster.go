package manager

import (
	"errors"
	"net/http"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/gin-gonic/gin"
)

// ChannelClusterSummaryResponse is the manager channel-cluster health summary body.
type ChannelClusterSummaryResponse struct {
	// Total is the number of channel runtime records scanned across all physical slots.
	Total int `json:"total"`
	// Healthy counts active channels with a leader and enough in-sync replicas.
	Healthy int `json:"healthy"`
	// ISRInsufficient counts channels whose in-sync replica count is below MinISR.
	ISRInsufficient int `json:"isr_insufficient"`
	// NoLeader counts channels with no current leader.
	NoLeader int `json:"no_leader"`
	// AvgReplicas is the average configured replica count across scanned channels.
	AvgReplicas float64 `json:"avg_replicas"`
	// AvgISR is the average in-sync replica count across scanned channels.
	AvgISR float64 `json:"avg_isr"`
	// LeaderDistribution counts channels led by each non-zero node ID.
	LeaderDistribution []ChannelLeaderDistributionDTO `json:"leader_distribution"`
}

// ChannelLeaderDistributionDTO counts channel leaders assigned to one node.
type ChannelLeaderDistributionDTO struct {
	// NodeID is the channel leader node ID.
	NodeID uint64 `json:"node_id"`
	// Count is the number of scanned channels led by NodeID.
	Count int `json:"count"`
}

// ChannelClusterUnhealthyListResponse is one unhealthy channel page.
type ChannelClusterUnhealthyListResponse struct {
	// Items contains unhealthy channel rows.
	Items []ChannelClusterUnhealthyDTO `json:"items"`
	// HasMore reports whether another page exists.
	HasMore bool `json:"has_more"`
	// NextCursor is the opaque cursor for the next page when HasMore is true.
	NextCursor string `json:"next_cursor,omitempty"`
}

// ChannelClusterUnhealthyDTO is a channel runtime row with health reasons.
type ChannelClusterUnhealthyDTO struct {
	ChannelRuntimeMetaDTO
	// Reasons describes why the channel is considered unhealthy.
	Reasons []string `json:"reasons"`
}

func (s *Server) handleChannelClusterSummary(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}

	summary, err := s.management.GetChannelClusterSummary(c.Request.Context())
	if err != nil {
		if slotLeaderAuthoritativeReadUnavailable(err) {
			jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "slot leader authoritative read unavailable")
			return
		}
		jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	c.JSON(http.StatusOK, channelClusterSummaryDTO(summary))
}

func (s *Server) handleChannelClusterUnhealthy(c *gin.Context) {
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

	page, err := s.management.ListChannelClusterUnhealthy(c.Request.Context(), managementusecase.ListChannelClusterUnhealthyRequest{
		Limit:  limit,
		Cursor: cursor,
	})
	if err != nil {
		switch {
		case slotLeaderAuthoritativeReadUnavailable(err):
			jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "slot leader authoritative read unavailable")
		case errors.Is(err, metadb.ErrInvalidArgument):
			jsonError(c, http.StatusBadRequest, "bad_request", "invalid cursor")
		default:
			jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
		}
		return
	}

	nextCursor, err := encodeChannelRuntimeMetaCursor(page.NextCursor)
	if err != nil {
		jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	c.JSON(http.StatusOK, ChannelClusterUnhealthyListResponse{
		Items:      channelClusterUnhealthyDTOs(page.Items),
		HasMore:    page.HasMore,
		NextCursor: nextCursor,
	})
}

func channelClusterSummaryDTO(summary managementusecase.ChannelClusterSummary) ChannelClusterSummaryResponse {
	return ChannelClusterSummaryResponse{
		Total:              summary.Total,
		Healthy:            summary.Healthy,
		ISRInsufficient:    summary.ISRInsufficient,
		NoLeader:           summary.NoLeader,
		AvgReplicas:        summary.AvgReplicas,
		AvgISR:             summary.AvgISR,
		LeaderDistribution: channelLeaderDistributionDTOs(summary.LeaderDistribution),
	}
}

func channelLeaderDistributionDTOs(items []managementusecase.ChannelLeaderDistribution) []ChannelLeaderDistributionDTO {
	out := make([]ChannelLeaderDistributionDTO, 0, len(items))
	for _, item := range items {
		out = append(out, ChannelLeaderDistributionDTO{
			NodeID: item.NodeID,
			Count:  item.Count,
		})
	}
	return out
}

func channelClusterUnhealthyDTOs(items []managementusecase.ChannelClusterUnhealthyItem) []ChannelClusterUnhealthyDTO {
	out := make([]ChannelClusterUnhealthyDTO, 0, len(items))
	for _, item := range items {
		out = append(out, ChannelClusterUnhealthyDTO{
			ChannelRuntimeMetaDTO: channelRuntimeMetaDTO(item.ChannelRuntimeMeta),
			Reasons:               append([]string(nil), item.Reasons...),
		})
	}
	return out
}
