package manager

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/observability/diagnostics"
	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	"github.com/gin-gonic/gin"
)

// DiagnosticsTrackingRuleDTO is the HTTP view of one temporary diagnostics tracking rule.
type DiagnosticsTrackingRuleDTO struct {
	RuleID      string     `json:"rule_id"`
	Target      string     `json:"target"`
	UID         string     `json:"uid,omitempty"`
	ChannelKey  string     `json:"channel_key,omitempty"`
	ChannelID   string     `json:"channel_id,omitempty"`
	ChannelType uint8      `json:"channel_type,omitempty"`
	SampleRate  float64    `json:"sample_rate"`
	CreatedAt   *time.Time `json:"created_at,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
}

// DiagnosticsTrackingNodeDTO reports one node's temporary tracking operation status.
type DiagnosticsTrackingNodeDTO struct {
	NodeID uint64   `json:"node_id"`
	Status string   `json:"status"`
	Notes  []string `json:"notes"`
}

// DiagnosticsTrackingMutationResponse is returned by create operations.
type DiagnosticsTrackingMutationResponse struct {
	Status string                       `json:"status"`
	Rule   DiagnosticsTrackingRuleDTO   `json:"rule"`
	Nodes  []DiagnosticsTrackingNodeDTO `json:"nodes"`
	Notes  []string                     `json:"notes"`
}

// DiagnosticsTrackingListResponse is returned by list operations.
type DiagnosticsTrackingListResponse struct {
	Status string                       `json:"status"`
	Rules  []DiagnosticsTrackingRuleDTO `json:"rules"`
	Nodes  []DiagnosticsTrackingNodeDTO `json:"nodes"`
	Notes  []string                     `json:"notes"`
}

// DiagnosticsTrackingDeleteResponse is returned by delete operations.
type DiagnosticsTrackingDeleteResponse struct {
	Status string                       `json:"status"`
	RuleID string                       `json:"rule_id"`
	Nodes  []DiagnosticsTrackingNodeDTO `json:"nodes"`
	Notes  []string                     `json:"notes"`
}

type diagnosticsTrackingCreateRequest struct {
	NodeID      uint64   `json:"node_id,omitempty"`
	Target      string   `json:"target"`
	UID         string   `json:"uid,omitempty"`
	ChannelID   string   `json:"channel_id,omitempty"`
	ChannelType uint8    `json:"channel_type,omitempty"`
	TTLSeconds  int      `json:"ttl_seconds"`
	SampleRate  *float64 `json:"sample_rate,omitempty"`
}

func (s *Server) handleCreateDiagnosticsTrackingRule(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	var body diagnosticsTrackingCreateRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}
	sampleRate := 1.0
	if body.SampleRate != nil {
		sampleRate = *body.SampleRate
	}
	if !validDiagnosticsTrackingCreateRequest(body, sampleRate) {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid diagnostics tracking rule")
		return
	}
	resp, err := s.management.CreateDiagnosticsTrackingRule(c.Request.Context(), managementusecase.DiagnosticsTrackingCreateRequest{
		NodeID:      body.NodeID,
		Target:      strings.TrimSpace(body.Target),
		UID:         strings.TrimSpace(body.UID),
		ChannelID:   strings.TrimSpace(body.ChannelID),
		ChannelType: body.ChannelType,
		TTLSeconds:  body.TTLSeconds,
		SampleRate:  sampleRate,
	})
	if err != nil {
		s.writeDiagnosticsTrackingError(c, err)
		return
	}
	c.JSON(http.StatusOK, diagnosticsTrackingMutationResponseDTO(resp))
}

func (s *Server) handleDiagnosticsTrackingRules(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	resp, err := s.management.ListDiagnosticsTrackingRules(c.Request.Context())
	if err != nil {
		s.writeDiagnosticsTrackingError(c, err)
		return
	}
	c.JSON(http.StatusOK, diagnosticsTrackingListResponseDTO(resp))
}

func (s *Server) handleDeleteDiagnosticsTrackingRule(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	ruleID := strings.TrimSpace(c.Param("rule_id"))
	if ruleID == "" {
		jsonError(c, http.StatusBadRequest, "bad_request", "rule_id is required")
		return
	}
	resp, err := s.management.DeleteDiagnosticsTrackingRule(c.Request.Context(), ruleID)
	if err != nil {
		s.writeDiagnosticsTrackingError(c, err)
		return
	}
	c.JSON(http.StatusOK, diagnosticsTrackingDeleteResponseDTO(resp))
}

func (s *Server) writeDiagnosticsTrackingError(c *gin.Context, err error) {
	if errors.Is(err, diagnostics.ErrInvalidTrackingRule) {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid diagnostics tracking rule")
		return
	}
	jsonError(c, http.StatusInternalServerError, "internal_error", "diagnostics tracking operation failed")
}

func diagnosticsTrackingMutationResponseDTO(resp managementusecase.DiagnosticsTrackingMutationResponse) DiagnosticsTrackingMutationResponse {
	return DiagnosticsTrackingMutationResponse{
		Status: string(resp.Status),
		Rule:   diagnosticsTrackingRuleDTO(resp.Rule),
		Nodes:  diagnosticsTrackingNodesDTO(resp.Nodes),
		Notes:  diagnosticsStringSliceDTO(resp.Notes),
	}
}

func diagnosticsTrackingListResponseDTO(resp managementusecase.DiagnosticsTrackingListResponse) DiagnosticsTrackingListResponse {
	rules := make([]DiagnosticsTrackingRuleDTO, 0, len(resp.Rules))
	for _, rule := range resp.Rules {
		rules = append(rules, diagnosticsTrackingRuleDTO(rule))
	}
	return DiagnosticsTrackingListResponse{
		Status: string(resp.Status),
		Rules:  rules,
		Nodes:  diagnosticsTrackingNodesDTO(resp.Nodes),
		Notes:  diagnosticsStringSliceDTO(resp.Notes),
	}
}

func diagnosticsTrackingDeleteResponseDTO(resp managementusecase.DiagnosticsTrackingDeleteResponse) DiagnosticsTrackingDeleteResponse {
	return DiagnosticsTrackingDeleteResponse{
		Status: string(resp.Status),
		RuleID: resp.RuleID,
		Nodes:  diagnosticsTrackingNodesDTO(resp.Nodes),
		Notes:  diagnosticsStringSliceDTO(resp.Notes),
	}
}

func diagnosticsTrackingRuleDTO(rule managementusecase.DiagnosticsTrackingRule) DiagnosticsTrackingRuleDTO {
	return DiagnosticsTrackingRuleDTO{
		RuleID:      rule.ID,
		Target:      rule.Target,
		UID:         rule.UID,
		ChannelKey:  rule.ChannelKey,
		ChannelID:   rule.ChannelID,
		ChannelType: rule.ChannelType,
		SampleRate:  rule.SampleRate,
		CreatedAt:   diagnosticsTrackingTimePtr(rule.CreatedAt),
		ExpiresAt:   diagnosticsTrackingTimePtr(rule.ExpiresAt),
	}
}

func diagnosticsTrackingNodesDTO(nodes []managementusecase.DiagnosticsTrackingNodeResult) []DiagnosticsTrackingNodeDTO {
	out := make([]DiagnosticsTrackingNodeDTO, 0, len(nodes))
	for _, node := range nodes {
		out = append(out, DiagnosticsTrackingNodeDTO{
			NodeID: node.NodeID,
			Status: node.Status,
			Notes:  diagnosticsStringSliceDTO(node.Notes),
		})
	}
	return out
}

func validDiagnosticsTrackingCreateRequest(body diagnosticsTrackingCreateRequest, sampleRate float64) bool {
	if body.TTLSeconds <= 0 || sampleRate < 0 || sampleRate > 1 {
		return false
	}
	switch strings.TrimSpace(body.Target) {
	case string(diagnostics.TrackingTargetSenderUID):
		return strings.TrimSpace(body.UID) != ""
	case string(diagnostics.TrackingTargetChannel):
		return strings.TrimSpace(body.ChannelID) != "" && body.ChannelType > 0
	default:
		return false
	}
}

func diagnosticsTrackingTimePtr(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	return &value
}
