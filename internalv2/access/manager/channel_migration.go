package manager

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/gin-gonic/gin"
)

// ChannelMigrationRequest is the common manager JSON body for ChannelV2 migration writes.
type ChannelMigrationRequest struct {
	// ChannelID is the logical channel identifier.
	ChannelID string `json:"channel_id"`
	// ChannelType is the logical channel type.
	ChannelType uint8 `json:"channel_type"`
	// SourceNode is the replica being replaced for replica-replace requests.
	SourceNode uint64 `json:"source_node,omitempty"`
	// TargetNode is the desired leader or replacement node.
	TargetNode uint64 `json:"target_node"`
	// TaskID optionally supplies an idempotency key.
	TaskID string `json:"task_id,omitempty"`
	// Reason is the optional operator abort reason.
	Reason string `json:"reason,omitempty"`
}

// ChannelMigrationResponse is one manager-facing ChannelV2 migration task.
type ChannelMigrationResponse struct {
	// TaskID is the durable migration task identity.
	TaskID string `json:"task_id"`
	// ChannelID is the logical channel identifier.
	ChannelID string `json:"channel_id"`
	// ChannelType is the logical channel type.
	ChannelType int64 `json:"channel_type"`
	// Kind is the stable workflow kind.
	Kind string `json:"kind"`
	// Status is the stable task lifecycle status.
	Status string `json:"status"`
	// Phase is the stable executor phase.
	Phase string `json:"phase"`
	// SourceNode is the source leader or replica, depending on Kind.
	SourceNode uint64 `json:"source_node,omitempty"`
	// TargetNode is the desired leader or replacement node.
	TargetNode uint64 `json:"target_node,omitempty"`
	// DesiredLeader is the desired leader when known.
	DesiredLeader uint64 `json:"desired_leader,omitempty"`
	// BlockerMessage is the bounded blocker detail for blocked tasks.
	BlockerMessage string `json:"blocker_message,omitempty"`
	// LastError is the bounded failure detail for failed tasks.
	LastError string `json:"last_error,omitempty"`
}

// ChannelMigrationListResponse is the manager active migration page body.
type ChannelMigrationListResponse struct {
	// Items contains active migration task rows.
	Items []ChannelMigrationResponse `json:"items"`
}

func (s *Server) handleChannelMigrationLeaderTransfer(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	var body ChannelMigrationRequest
	if err := c.ShouldBindJSON(&body); err != nil || strings.TrimSpace(body.ChannelID) == "" || body.TargetNode == 0 {
		jsonError(c, http.StatusBadRequest, "bad_request", "bad_request")
		return
	}
	result, err := s.management.RequestChannelLeaderTransfer(c.Request.Context(), managementusecase.LeaderTransferInput{
		ChannelID:   body.ChannelID,
		ChannelType: body.ChannelType,
		TargetNode:  body.TargetNode,
		TaskID:      body.TaskID,
	})
	if err != nil {
		writeChannelMigrationError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, channelMigrationResponse(result))
}

func (s *Server) handleChannelMigrationReplicaReplace(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	var body ChannelMigrationRequest
	if err := c.ShouldBindJSON(&body); err != nil || strings.TrimSpace(body.ChannelID) == "" || body.SourceNode == 0 || body.TargetNode == 0 {
		jsonError(c, http.StatusBadRequest, "bad_request", "bad_request")
		return
	}
	result, err := s.management.RequestChannelReplicaReplace(c.Request.Context(), managementusecase.ReplicaReplaceInput{
		ChannelID:   body.ChannelID,
		ChannelType: body.ChannelType,
		SourceNode:  body.SourceNode,
		TargetNode:  body.TargetNode,
		TaskID:      body.TaskID,
	})
	if err != nil {
		writeChannelMigrationError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, channelMigrationResponse(result))
}

func (s *Server) handleActiveChannelMigrations(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	input, err := parseChannelMigrationListInput(c)
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "bad_request")
		return
	}
	result, err := s.management.ListActiveChannelMigrations(c.Request.Context(), input)
	if err != nil {
		writeChannelMigrationError(c, err)
		return
	}
	c.JSON(http.StatusOK, ChannelMigrationListResponse{Items: channelMigrationResponses(result.Items)})
}

func (s *Server) handleChannelMigration(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	input, err := parseChannelMigrationLookupInput(c)
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "bad_request")
		return
	}
	result, err := s.management.ChannelMigration(c.Request.Context(), input)
	if err != nil {
		writeChannelMigrationError(c, err)
		return
	}
	c.JSON(http.StatusOK, channelMigrationResponse(result))
}

func (s *Server) handleChannelMigrationAbort(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	var body ChannelMigrationRequest
	if err := c.ShouldBindJSON(&body); err != nil || strings.TrimSpace(body.ChannelID) == "" {
		jsonError(c, http.StatusBadRequest, "bad_request", "bad_request")
		return
	}
	result, err := s.management.AbortChannelMigration(c.Request.Context(), managementusecase.ChannelMigrationAbortInput{
		ChannelID:   body.ChannelID,
		ChannelType: body.ChannelType,
		TaskID:      c.Param("task_id"),
		Reason:      body.Reason,
	})
	if err != nil {
		writeChannelMigrationError(c, err)
		return
	}
	c.JSON(http.StatusOK, channelMigrationResponse(result))
}

func parseChannelMigrationListInput(c *gin.Context) (managementusecase.ChannelMigrationListInput, error) {
	channelType, err := parseChannelMigrationQueryType(c.Query("channel_type"))
	if err != nil {
		return managementusecase.ChannelMigrationListInput{}, err
	}
	limit := 10
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 || parsed > 100 {
			return managementusecase.ChannelMigrationListInput{}, strconv.ErrSyntax
		}
		limit = parsed
	}
	return managementusecase.ChannelMigrationListInput{
		ChannelID:   c.Query("channel_id"),
		ChannelType: channelType,
		Limit:       limit,
	}, nil
}

func parseChannelMigrationLookupInput(c *gin.Context) (managementusecase.ChannelMigrationLookupInput, error) {
	channelType, err := parseChannelMigrationQueryType(c.Query("channel_type"))
	if err != nil {
		return managementusecase.ChannelMigrationLookupInput{}, err
	}
	return managementusecase.ChannelMigrationLookupInput{
		ChannelID:   c.Query("channel_id"),
		ChannelType: channelType,
		TaskID:      c.Param("task_id"),
	}, nil
}

func parseChannelMigrationQueryType(raw string) (uint8, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return 0, nil
	}
	parsed, err := strconv.ParseUint(value, 10, 8)
	if err != nil {
		return 0, err
	}
	return uint8(parsed), nil
}

func channelMigrationResponses(items []managementusecase.ChannelMigrationSummary) []ChannelMigrationResponse {
	out := make([]ChannelMigrationResponse, 0, len(items))
	for _, item := range items {
		out = append(out, channelMigrationResponse(item))
	}
	return out
}

func channelMigrationResponse(item managementusecase.ChannelMigrationSummary) ChannelMigrationResponse {
	return ChannelMigrationResponse{
		TaskID:         item.TaskID,
		ChannelID:      item.ChannelID,
		ChannelType:    item.ChannelType,
		Kind:           item.Kind,
		Status:         item.Status,
		Phase:          item.Phase,
		SourceNode:     item.SourceNode,
		TargetNode:     item.TargetNode,
		DesiredLeader:  item.DesiredLeader,
		BlockerMessage: item.BlockerMessage,
		LastError:      item.LastError,
	}
}

func writeChannelMigrationError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, metadb.ErrInvalidArgument):
		jsonError(c, http.StatusBadRequest, "bad_request", "bad_request")
	case errors.Is(err, managementusecase.ErrChannelMigrationNotFound):
		jsonError(c, http.StatusNotFound, "not_found", "not_found")
	case errors.Is(err, managementusecase.ErrChannelMigrationConflict):
		jsonError(c, http.StatusConflict, "conflict", "conflict")
	case errors.Is(err, managementusecase.ErrChannelMigrationUnavailable),
		errors.Is(err, clusterv2.ErrSlotNotFound),
		errors.Is(err, clusterv2.ErrNotStarted),
		errors.Is(err, clusterv2.ErrNotLeader),
		errors.Is(err, clusterv2.ErrStopping):
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "service_unavailable")
	default:
		jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
	}
}
