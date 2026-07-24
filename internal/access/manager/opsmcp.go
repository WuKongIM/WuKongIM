package manager

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	"github.com/gin-gonic/gin"
)

const maxOpsMCPManagerBodyBytes = 64 << 10

// OpsMCPManagement exposes embedded operations MCP administration.
type OpsMCPManagement interface {
	OpsMCPStatus(context.Context) (managementusecase.OpsMCPStatus, error)
	CreateOpsMCPToken(context.Context, managementusecase.OpsMCPTokenCreateRequest) (managementusecase.OpsMCPTokenCreateResponse, error)
	RevokeOpsMCPToken(context.Context, managementusecase.OpsMCPTokenRevokeRequest) error
	SetOpsMCPOwner(context.Context, managementusecase.OpsMCPOwnerUpdateRequest) error
	StartOpsMCP(context.Context, managementusecase.OpsMCPStateMutationRequest) error
	StopOpsMCP(context.Context, managementusecase.OpsMCPStateMutationRequest) error
	OpsMCPAudits(context.Context, int) ([]managementusecase.OpsMCPAuditEntry, error)
}

type opsMCPStatusResponse struct {
	ClusterID       string                           `json:"cluster_id"`
	Revision        uint64                           `json:"revision"`
	Enabled         bool                             `json:"enabled"`
	ObservedStatus  string                           `json:"observed_status"`
	OwnerNodeID     uint64                           `json:"owner_node_id"`
	OwnerCandidates []opsMCPOwnerCandidateResponse   `json:"owner_candidates"`
	Credentials     []opsMCPCredentialStatusResponse `json:"credentials"`
	Warnings        []string                         `json:"warnings"`
}

type opsMCPOwnerCandidateResponse struct {
	NodeID uint64 `json:"node_id"`
	Status string `json:"status"`
}

type opsMCPCredentialStatusResponse struct {
	ID                  string `json:"id"`
	CreatedAtUnixMillis int64  `json:"created_at_unix_ms"`
	Old                 bool   `json:"old"`
}

type opsMCPMutationBody struct {
	ExpectedRevision uint64 `json:"expected_revision"`
}

type opsMCPOwnerMutationBody struct {
	ExpectedRevision uint64 `json:"expected_revision"`
	OwnerNodeID      uint64 `json:"owner_node_id"`
}

type opsMCPTokenCreateResponse struct {
	CredentialID        string `json:"credential_id"`
	Token               string `json:"token"`
	CreatedAtUnixMillis int64  `json:"created_at_unix_ms"`
	Revision            uint64 `json:"revision"`
}

type opsMCPMutationResponse struct {
	Accepted bool `json:"accepted"`
}

type opsMCPAuditsResponse struct {
	Items []opsMCPAuditResponse `json:"items"`
}

type opsMCPAuditResponse struct {
	RequestID      string    `json:"request_id"`
	RecorderNodeID uint64    `json:"recorder_node_id,omitempty"`
	Phase          string    `json:"phase"`
	IngressNodeID  uint64    `json:"ingress_node_id,omitempty"`
	OwnerNodeID    uint64    `json:"owner_node_id,omitempty"`
	CredentialID   string    `json:"credential_id"`
	Tool           string    `json:"tool"`
	NodeID         uint64    `json:"node_id,omitempty"`
	SlotID         uint32    `json:"slot_id,omitempty"`
	ChannelType    uint8     `json:"channel_type,omitempty"`
	Result         string    `json:"result"`
	StartedAt      time.Time `json:"started_at"`
	DurationMS     int64     `json:"duration_ms"`
	ResponseBytes  int64     `json:"response_bytes"`
	CacheHit       bool      `json:"cache_hit"`
	PprofKind      string    `json:"pprof_kind,omitempty"`
	PprofSeconds   int       `json:"pprof_seconds,omitempty"`
}

func (s *Server) requireMCPAdministrationReady() gin.HandlerFunc {
	return func(c *gin.Context) {
		if s == nil || !s.auth.enabled() {
			jsonError(c, http.StatusServiceUnavailable, "manager_auth_required", "manager authentication must be enabled before MCP administration")
			c.Abort()
			return
		}
		c.Next()
	}
}

func (s *Server) handleOpsMCPStatus(c *gin.Context) {
	if s.opsMCP == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "operations MCP management is not configured")
		return
	}
	status, err := s.opsMCP.OpsMCPStatus(c.Request.Context())
	if err != nil {
		writeOpsMCPManagementError(c, err)
		return
	}
	response := opsMCPStatusResponse{
		ClusterID: status.ClusterID, Revision: status.Revision, Enabled: status.Enabled,
		ObservedStatus:  status.ObservedStatus,
		OwnerNodeID:     status.OwnerNodeID,
		OwnerCandidates: make([]opsMCPOwnerCandidateResponse, 0, len(status.OwnerCandidates)),
		Credentials:     make([]opsMCPCredentialStatusResponse, 0, len(status.Credentials)),
		Warnings:        cloneStringDTOs(status.Warnings),
	}
	if response.Warnings == nil {
		response.Warnings = []string{}
	}
	for _, credential := range status.Credentials {
		response.Credentials = append(response.Credentials, opsMCPCredentialStatusResponse{
			ID: credential.ID, CreatedAtUnixMillis: credential.CreatedAtUnixMillis, Old: credential.Old,
		})
	}
	for _, candidate := range status.OwnerCandidates {
		response.OwnerCandidates = append(response.OwnerCandidates, opsMCPOwnerCandidateResponse{
			NodeID: candidate.NodeID, Status: candidate.Status,
		})
	}
	c.JSON(http.StatusOK, response)
}

func (s *Server) handleOpsMCPTokenCreate(c *gin.Context) {
	var body opsMCPMutationBody
	if !decodeOpsMCPBody(c, &body) || !requireOpsMCPBackend(c, s.opsMCP) {
		return
	}
	response, err := s.opsMCP.CreateOpsMCPToken(c.Request.Context(), managementusecase.OpsMCPTokenCreateRequest{
		ExpectedRevision: body.ExpectedRevision, IdempotencyKey: c.GetHeader("Idempotency-Key"),
	})
	if err != nil {
		writeOpsMCPManagementError(c, err)
		return
	}
	c.JSON(http.StatusCreated, opsMCPTokenCreateResponse{
		CredentialID: response.CredentialID, Token: response.Token,
		CreatedAtUnixMillis: response.CreatedAtUnixMillis, Revision: response.Revision,
	})
}

func (s *Server) handleOpsMCPTokenRevoke(c *gin.Context) {
	var body opsMCPMutationBody
	if !decodeOpsMCPBody(c, &body) || !requireOpsMCPBackend(c, s.opsMCP) {
		return
	}
	err := s.opsMCP.RevokeOpsMCPToken(c.Request.Context(), managementusecase.OpsMCPTokenRevokeRequest{
		ExpectedRevision: body.ExpectedRevision, IdempotencyKey: c.GetHeader("Idempotency-Key"),
		CredentialID: c.Param("credential_id"),
	})
	writeOpsMCPMutationResult(c, err)
}

func (s *Server) handleOpsMCPOwnerUpdate(c *gin.Context) {
	var body opsMCPOwnerMutationBody
	if !decodeOpsMCPBody(c, &body) || !requireOpsMCPBackend(c, s.opsMCP) {
		return
	}
	err := s.opsMCP.SetOpsMCPOwner(c.Request.Context(), managementusecase.OpsMCPOwnerUpdateRequest{
		ExpectedRevision: body.ExpectedRevision, IdempotencyKey: c.GetHeader("Idempotency-Key"),
		OwnerNodeID: body.OwnerNodeID,
	})
	writeOpsMCPMutationResult(c, err)
}

func (s *Server) handleOpsMCPStart(c *gin.Context) {
	s.handleOpsMCPEnabledMutation(c, true)
}

func (s *Server) handleOpsMCPStop(c *gin.Context) {
	s.handleOpsMCPEnabledMutation(c, false)
}

func (s *Server) handleOpsMCPEnabledMutation(c *gin.Context, enabled bool) {
	var body opsMCPMutationBody
	if !decodeOpsMCPBody(c, &body) || !requireOpsMCPBackend(c, s.opsMCP) {
		return
	}
	request := managementusecase.OpsMCPStateMutationRequest{
		ExpectedRevision: body.ExpectedRevision, IdempotencyKey: c.GetHeader("Idempotency-Key"),
	}
	var err error
	if enabled {
		err = s.opsMCP.StartOpsMCP(c.Request.Context(), request)
	} else {
		err = s.opsMCP.StopOpsMCP(c.Request.Context(), request)
	}
	writeOpsMCPMutationResult(c, err)
}

func (s *Server) handleOpsMCPAudits(c *gin.Context) {
	if !requireOpsMCPBackend(c, s.opsMCP) {
		return
	}
	limit := 200
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value <= 0 || value > 200 {
			jsonError(c, http.StatusBadRequest, "bad_request", "limit must be between 1 and 200")
			return
		}
		limit = value
	}
	entries, err := s.opsMCP.OpsMCPAudits(c.Request.Context(), limit)
	if err != nil {
		writeOpsMCPManagementError(c, err)
		return
	}
	response := opsMCPAuditsResponse{Items: make([]opsMCPAuditResponse, 0, len(entries))}
	for _, entry := range entries {
		response.Items = append(response.Items, opsMCPAuditResponse{
			RequestID: entry.RequestID, RecorderNodeID: entry.RecorderNodeID, Phase: entry.Phase,
			IngressNodeID: entry.IngressNodeID, OwnerNodeID: entry.OwnerNodeID,
			CredentialID: entry.CredentialID, Tool: entry.Tool, Result: entry.Result,
			NodeID: entry.NodeID, SlotID: entry.SlotID, ChannelType: entry.ChannelType,
			StartedAt: entry.StartedAt, DurationMS: entry.DurationMS, ResponseBytes: entry.ResponseBytes, CacheHit: entry.CacheHit,
			PprofKind: entry.PprofKind, PprofSeconds: entry.PprofSeconds,
		})
	}
	c.JSON(http.StatusOK, response)
}

func decodeOpsMCPBody(c *gin.Context, out any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxOpsMCPManagerBodyBytes)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid request")
		return false
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid request")
		return false
	}
	return true
}

func requireOpsMCPBackend(c *gin.Context, backend OpsMCPManagement) bool {
	if backend != nil {
		return true
	}
	jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "operations MCP management is not configured")
	return false
}

func writeOpsMCPMutationResult(c *gin.Context, err error) {
	if err != nil {
		writeOpsMCPManagementError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, opsMCPMutationResponse{Accepted: true})
}

func writeOpsMCPManagementError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, managementusecase.ErrOpsMCPInvalidRequest):
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid request")
	case errors.Is(err, managementusecase.ErrOpsMCPTokenNotFound):
		jsonError(c, http.StatusNotFound, "not_found", "token not found")
	case errors.Is(err, managementusecase.ErrOpsMCPConflict):
		jsonError(c, http.StatusConflict, "conflict", err.Error())
	case errors.Is(err, managementusecase.ErrOpsMCPUnavailable):
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "operations MCP unavailable")
	default:
		jsonError(c, http.StatusInternalServerError, "internal_error", "operations MCP request failed")
	}
}
