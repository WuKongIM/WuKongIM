package manager

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/legacy/usecase/management"
	"github.com/gin-gonic/gin"
)

// DistributedTasksSummaryResponse is the normalized task counter response.
type DistributedTasksSummaryResponse struct {
	// Total is the number of tasks across available task sources.
	Total int `json:"total"`
	// ByStatus counts tasks by normalized lifecycle state.
	ByStatus map[string]int `json:"by_status"`
	// ByDomain counts tasks by source domain.
	ByDomain map[string]int `json:"by_domain"`
	// Partial reports whether one or more task sources failed.
	Partial bool `json:"partial"`
	// Warnings describes non-fatal source failures.
	Warnings []DistributedTaskWarningDTO `json:"warnings"`
}

// DistributedTasksResponse is one normalized task list page.
type DistributedTasksResponse struct {
	// Total is the number of matching tasks before pagination.
	Total int `json:"total"`
	// Items contains the normalized task page.
	Items []DistributedTaskDTO `json:"items"`
	// NextCursor is the opaque cursor for the next page.
	NextCursor string `json:"next_cursor"`
	// HasMore reports whether another page exists.
	HasMore bool `json:"has_more"`
	// Partial reports whether one or more task sources failed.
	Partial bool `json:"partial"`
	// Warnings describes non-fatal source failures.
	Warnings []DistributedTaskWarningDTO `json:"warnings"`
}

// DistributedTaskDetailResponse wraps one task and source-specific details.
type DistributedTaskDetailResponse struct {
	// Task is the normalized task row.
	Task DistributedTaskDTO `json:"task"`
	// Detail contains source-specific details for the task.
	Detail DistributedTaskDetailPayloadDTO `json:"detail"`
}

// DistributedTaskDTO is a normalized manager-facing task row.
type DistributedTaskDTO struct {
	ID         string                  `json:"id"`
	Domain     string                  `json:"domain"`
	Kind       string                  `json:"kind"`
	Status     string                  `json:"status"`
	Phase      string                  `json:"phase"`
	Scope      DistributedTaskScopeDTO `json:"scope"`
	SourceNode uint64                  `json:"source_node"`
	TargetNode uint64                  `json:"target_node"`
	OwnerNode  uint64                  `json:"owner_node"`
	Attempt    uint32                  `json:"attempt"`
	NextRunAt  *time.Time              `json:"next_run_at"`
	CreatedAt  *time.Time              `json:"created_at"`
	UpdatedAt  *time.Time              `json:"updated_at"`
	LastError  string                  `json:"last_error"`
	Summary    string                  `json:"summary"`
	Links      map[string]string       `json:"links"`
}

// DistributedTaskScopeDTO describes the primary affected task scope.
type DistributedTaskScopeDTO struct {
	Type        string `json:"type"`
	ID          string `json:"id"`
	SlotID      uint32 `json:"slot_id"`
	ChannelID   string `json:"channel_id"`
	ChannelType int64  `json:"channel_type"`
	NodeID      uint64 `json:"node_id"`
}

// DistributedTaskWarningDTO describes a partial source warning.
type DistributedTaskWarningDTO struct {
	Domain  string `json:"domain"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// DistributedTaskDetailPayloadDTO contains source-specific task details.
type DistributedTaskDetailPayloadDTO struct {
	Domain           string                     `json:"domain"`
	RawStatus        string                     `json:"raw_status"`
	Slot             *TaskDetailDTO             `json:"slot,omitempty"`
	NodeOnboarding   *nodeOnboardingJobDTO      `json:"node_onboarding,omitempty"`
	NodeScaleIn      *nodeScaleInReportDTO      `json:"node_scale_in,omitempty"`
	ChannelMigration *channelMigrationDetailDTO `json:"channel_migration,omitempty"`
}

func (s *Server) handleDistributedTasksSummary(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	summary, err := s.management.GetDistributedTasksSummary(c.Request.Context())
	if err != nil {
		writeDistributedTaskError(c, err, "distributed task sources unavailable")
		return
	}
	c.JSON(http.StatusOK, distributedTasksSummaryResponse(summary))
}

func (s *Server) handleDistributedTasks(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	query, err := parseDistributedTaskQuery(c)
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid distributed task query")
		return
	}
	page, err := s.management.ListDistributedTasks(c.Request.Context(), query)
	if err != nil {
		writeDistributedTaskError(c, err, "distributed task sources unavailable")
		return
	}
	c.JSON(http.StatusOK, distributedTasksResponse(page))
}

func (s *Server) handleDistributedTask(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	domain := managementusecase.DistributedTaskDomain(c.Param("domain"))
	if !validDistributedTaskDomainParam(domain) {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid distributed task domain")
		return
	}
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid distributed task id")
		return
	}
	detail, err := s.management.GetDistributedTask(c.Request.Context(), domain, id)
	if err != nil {
		writeDistributedTaskError(c, err, "distributed task sources unavailable")
		return
	}
	c.JSON(http.StatusOK, DistributedTaskDetailResponse{
		Task:   distributedTaskDTO(detail.Task),
		Detail: distributedTaskDetailPayloadDTO(detail.Detail),
	})
}

func parseDistributedTaskQuery(c *gin.Context) (managementusecase.DistributedTaskQuery, error) {
	var query managementusecase.DistributedTaskQuery
	if raw := strings.TrimSpace(c.Query("domain")); raw != "" {
		query.Domain = managementusecase.DistributedTaskDomain(raw)
		if !validDistributedTaskDomainParam(query.Domain) {
			return query, strconv.ErrSyntax
		}
	}
	if raw := strings.TrimSpace(c.Query("status")); raw != "" {
		query.Status = managementusecase.DistributedTaskStatus(raw)
		if !validDistributedTaskStatusParam(query.Status) {
			return query, strconv.ErrSyntax
		}
	}
	if raw := strings.TrimSpace(c.Query("scope")); raw != "" {
		query.Scope = managementusecase.DistributedTaskScopeType(raw)
		if !validDistributedTaskScopeParam(query.Scope) {
			return query, strconv.ErrSyntax
		}
	}
	if raw := strings.TrimSpace(c.Query("node_id")); raw != "" {
		nodeID, err := strconv.ParseUint(raw, 10, 64)
		if err != nil || nodeID == 0 {
			return query, strconv.ErrSyntax
		}
		query.NodeID = nodeID
	}
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit <= 0 {
			return query, strconv.ErrSyntax
		}
		query.Limit = limit
	}
	if raw := c.Query("keyword"); raw != "" {
		query.Keyword = strings.TrimSpace(raw)
	}
	if raw := strings.TrimSpace(c.Query("cursor")); raw != "" {
		offset, err := decodeDistributedTaskCursor(raw)
		if err != nil {
			return query, err
		}
		query.Offset = offset
	}
	return query, nil
}

func writeDistributedTaskError(c *gin.Context, err error, unavailableMessage string) {
	switch {
	case errors.Is(err, managementusecase.ErrDistributedTaskNotFound):
		jsonError(c, http.StatusNotFound, "not_found", "distributed task not found")
	case errors.Is(err, managementusecase.ErrDistributedTasksUnavailable):
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", unavailableMessage)
	case leaderConsistentReadUnavailable(err):
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "controller leader consistent read unavailable")
	default:
		jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
	}
}

func distributedTasksSummaryResponse(summary managementusecase.DistributedTaskSummary) DistributedTasksSummaryResponse {
	return DistributedTasksSummaryResponse{
		Total:    summary.Total,
		ByStatus: distributedTaskStatusCounts(summary.ByStatus),
		ByDomain: distributedTaskDomainCounts(summary.ByDomain),
		Partial:  summary.Partial,
		Warnings: distributedTaskWarningDTOs(summary.Warnings),
	}
}

func distributedTasksResponse(page managementusecase.DistributedTaskListResult) DistributedTasksResponse {
	items := make([]DistributedTaskDTO, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, distributedTaskDTO(item))
	}
	return DistributedTasksResponse{
		Total:      page.Total,
		Items:      items,
		NextCursor: encodeDistributedTaskCursor(page.NextOffset),
		HasMore:    page.HasMore,
		Partial:    page.Partial,
		Warnings:   distributedTaskWarningDTOs(page.Warnings),
	}
}

func distributedTaskDTO(item managementusecase.DistributedTask) DistributedTaskDTO {
	links := item.Links
	if links == nil {
		links = map[string]string{}
	}
	return DistributedTaskDTO{
		ID:         item.ID,
		Domain:     string(item.Domain),
		Kind:       item.Kind,
		Status:     string(item.Status),
		Phase:      item.Phase,
		Scope:      distributedTaskScopeDTO(item.Scope),
		SourceNode: item.SourceNode,
		TargetNode: item.TargetNode,
		OwnerNode:  item.OwnerNode,
		Attempt:    item.Attempt,
		NextRunAt:  item.NextRunAt,
		CreatedAt:  item.CreatedAt,
		UpdatedAt:  item.UpdatedAt,
		LastError:  item.LastError,
		Summary:    item.Summary,
		Links:      links,
	}
}

func distributedTaskScopeDTO(scope managementusecase.DistributedTaskScope) DistributedTaskScopeDTO {
	return DistributedTaskScopeDTO{
		Type:        string(scope.Type),
		ID:          scope.ID,
		SlotID:      scope.SlotID,
		ChannelID:   scope.ChannelID,
		ChannelType: scope.ChannelType,
		NodeID:      scope.NodeID,
	}
}

func distributedTaskDetailPayloadDTO(detail managementusecase.DistributedTaskDetailPayload) DistributedTaskDetailPayloadDTO {
	out := DistributedTaskDetailPayloadDTO{
		Domain:    string(detail.Domain),
		RawStatus: detail.RawStatus,
	}
	if detail.Slot != nil {
		slot := TaskDetailDTO{TaskDTO: taskDTO(detail.Slot.Task), Slot: taskSlotDTO(detail.Slot.Slot)}
		out.Slot = &slot
	}
	if detail.NodeOnboarding != nil {
		job := nodeOnboardingJobDTOFromUsecase(*detail.NodeOnboarding)
		out.NodeOnboarding = &job
	}
	if detail.NodeScaleIn != nil {
		report := nodeScaleInReportDTOFromUsecase(*detail.NodeScaleIn)
		out.NodeScaleIn = &report
	}
	if detail.ChannelMigration != nil {
		channelMigration := channelMigrationDetailDTOFromUsecase(*detail.ChannelMigration)
		out.ChannelMigration = &channelMigration
	}
	return out
}

func distributedTaskWarningDTOs(items []managementusecase.DistributedTaskWarning) []DistributedTaskWarningDTO {
	out := make([]DistributedTaskWarningDTO, 0, len(items))
	for _, item := range items {
		out = append(out, DistributedTaskWarningDTO{
			Domain:  string(item.Domain),
			Code:    item.Code,
			Message: item.Message,
		})
	}
	return out
}

func distributedTaskStatusCounts(counts map[managementusecase.DistributedTaskStatus]int) map[string]int {
	out := map[string]int{
		"pending":   0,
		"running":   0,
		"retrying":  0,
		"blocked":   0,
		"failed":    0,
		"completed": 0,
		"cancelled": 0,
		"unknown":   0,
	}
	for key, value := range counts {
		if _, ok := out[string(key)]; ok {
			out[string(key)] = value
		}
	}
	return out
}

func distributedTaskDomainCounts(counts map[managementusecase.DistributedTaskDomain]int) map[string]int {
	out := map[string]int{
		"slot_reconcile":    0,
		"node_onboarding":   0,
		"node_scale_in":     0,
		"channel_migration": 0,
	}
	for key, value := range counts {
		if _, ok := out[string(key)]; ok {
			out[string(key)] = value
		}
	}
	return out
}

func validDistributedTaskDomainParam(domain managementusecase.DistributedTaskDomain) bool {
	switch domain {
	case managementusecase.DistributedTaskDomainSlotReconcile,
		managementusecase.DistributedTaskDomainNodeOnboarding,
		managementusecase.DistributedTaskDomainNodeScaleIn,
		managementusecase.DistributedTaskDomainChannelMigration:
		return true
	default:
		return false
	}
}

func validDistributedTaskStatusParam(status managementusecase.DistributedTaskStatus) bool {
	switch status {
	case managementusecase.DistributedTaskStatusPending,
		managementusecase.DistributedTaskStatusRunning,
		managementusecase.DistributedTaskStatusRetrying,
		managementusecase.DistributedTaskStatusBlocked,
		managementusecase.DistributedTaskStatusFailed,
		managementusecase.DistributedTaskStatusCompleted,
		managementusecase.DistributedTaskStatusCancelled,
		managementusecase.DistributedTaskStatusUnknown:
		return true
	default:
		return false
	}
}

func validDistributedTaskScopeParam(scope managementusecase.DistributedTaskScopeType) bool {
	switch scope {
	case managementusecase.DistributedTaskScopeSlot,
		managementusecase.DistributedTaskScopeNode,
		managementusecase.DistributedTaskScopeChannel,
		managementusecase.DistributedTaskScopeJob:
		return true
	default:
		return false
	}
}
