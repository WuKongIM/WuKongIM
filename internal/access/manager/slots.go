package manager

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	"github.com/gin-gonic/gin"
)

// SlotsResponse is the manager slot list response body.
type SlotsResponse struct {
	// Total is the number of returned manager slots.
	Total int `json:"total"`
	// Items contains the ordered manager slot DTO list.
	Items []SlotDTO `json:"items"`
}

// SlotDTO is the manager-facing slot response item.
type SlotDTO struct {
	// SlotID is the physical slot identifier.
	SlotID uint32 `json:"slot_id"`
	// State contains lightweight derived slot summaries.
	State SlotStateDTO `json:"state"`
	// Assignment contains the desired slot placement view.
	Assignment SlotAssignmentDTO `json:"assignment"`
	// Runtime contains the observed slot runtime view.
	Runtime SlotRuntimeDTO `json:"runtime"`
	// NodeLog contains the selected node's local log watermark when requested.
	NodeLog *SlotNodeLogDTO `json:"node_log,omitempty"`
}

// SlotDetailDTO is the manager-facing slot detail response body.
type SlotDetailDTO struct {
	SlotDTO
	// Task contains the current reconcile task summary when one exists.
	Task *SlotTaskDTO `json:"task"`
}

// SlotStateDTO contains derived slot list state fields.
type SlotStateDTO struct {
	// Quorum summarizes whether the slot currently has quorum.
	Quorum string `json:"quorum"`
	// Sync summarizes whether runtime peers/config match the assignment.
	Sync string `json:"sync"`
	// LeaderMatch reports whether the preferred leader is currently leading.
	LeaderMatch bool `json:"leader_match"`
	// LeaderTransferPending reports whether a leader transfer task is current.
	LeaderTransferPending bool `json:"leader_transfer_pending"`
}

// SlotAssignmentDTO contains desired slot placement fields.
type SlotAssignmentDTO struct {
	// DesiredPeers is the desired slot voter set.
	DesiredPeers []uint64 `json:"desired_peers"`
	// PreferredLeaderID is the controller preferred leader; zero means unset.
	PreferredLeaderID uint64 `json:"preferred_leader_id"`
	// ConfigEpoch is the desired slot config epoch.
	ConfigEpoch uint64 `json:"config_epoch"`
	// BalanceVersion is the desired slot balance generation.
	BalanceVersion uint64 `json:"balance_version"`
}

// SlotRuntimeDTO contains observed slot runtime fields.
type SlotRuntimeDTO struct {
	// CurrentPeers is the currently observed peer set.
	CurrentPeers []uint64 `json:"current_peers"`
	// CurrentVoters is the currently observed voter set.
	CurrentVoters []uint64 `json:"current_voters"`
	// LeaderID is the observed slot leader.
	LeaderID uint64 `json:"leader_id"`
	// HealthyVoters is the observed healthy voter count.
	HealthyVoters uint32 `json:"healthy_voters"`
	// HasQuorum reports whether the slot currently has quorum.
	HasQuorum bool `json:"has_quorum"`
	// ObservedConfigEpoch is the observed runtime config epoch.
	ObservedConfigEpoch uint64 `json:"observed_config_epoch"`
	// LastReportAt is the latest runtime observation timestamp.
	LastReportAt time.Time `json:"last_report_at"`
}

// SlotNodeLogDTO contains one selected node's local slot log watermark.
type SlotNodeLogDTO struct {
	// NodeID is the node that reported the local log watermark.
	NodeID uint64 `json:"node_id"`
	// LeaderID is the slot Raft leader known by the reporting node.
	LeaderID uint64 `json:"leader_id"`
	// CommitIndex is the highest committed Raft log index known by the reporting node.
	CommitIndex uint64 `json:"commit_index"`
	// AppliedIndex is the highest Raft log index applied by the reporting node.
	AppliedIndex uint64 `json:"applied_index"`
}

// SlotTaskDTO contains the optional current task summary for slot detail.
type SlotTaskDTO struct {
	// Kind is the stringified task kind.
	Kind string `json:"kind"`
	// Step is the stringified task step.
	Step string `json:"step"`
	// Status is the stringified task status.
	Status string `json:"status"`
	// SourceNode is the source node when the task has one.
	SourceNode uint64 `json:"source_node"`
	// TargetNode is the target node when the task has one.
	TargetNode uint64 `json:"target_node"`
	// Attempt is the current task attempt count.
	Attempt uint32 `json:"attempt"`
	// NextRunAt is the next retry schedule when the task is retrying.
	NextRunAt *time.Time `json:"next_run_at"`
	// LastError is the last recorded task error message.
	LastError string `json:"last_error"`
}

// SlotLogEntriesResponse is the manager slot log entries response body.
type SlotLogEntriesResponse struct {
	// NodeID is the node whose local Slot log was read.
	NodeID uint64 `json:"node_id"`
	// SlotID is the physical Slot identifier.
	SlotID uint32 `json:"slot_id"`
	// FirstIndex is the first available local Raft log index.
	FirstIndex uint64 `json:"first_index"`
	// LastIndex is the last available local Raft log index.
	LastIndex uint64 `json:"last_index"`
	// CommitIndex is the queried node's local committed index watermark.
	CommitIndex uint64 `json:"commit_index"`
	// AppliedIndex is the queried node's local applied index watermark.
	AppliedIndex uint64 `json:"applied_index"`
	// NextCursor is the cursor for the next older page. Zero means no more entries.
	NextCursor uint64 `json:"next_cursor,omitempty"`
	// Items contains entries ordered newest first.
	Items []SlotLogEntryDTO `json:"items"`
}

// SlotLogEntryDTO is one manager-facing Slot Raft log entry summary.
type SlotLogEntryDTO struct {
	// Index is the Raft log index.
	Index uint64 `json:"index"`
	// Term is the Raft term stored on the entry.
	Term uint64 `json:"term"`
	// Type is the normalized Raft entry type.
	Type string `json:"type"`
	// DataSize is the payload size in bytes.
	DataSize int `json:"data_size"`
	// DecodeStatus reports whether the entry payload was decoded for inspection.
	DecodeStatus string `json:"decode_status,omitempty"`
	// DecodedType is the stable command or payload type when decoding succeeds.
	DecodedType string `json:"decoded_type,omitempty"`
	// Decoded is a redacted JSON-friendly payload summary for manager inspection.
	Decoded map[string]any `json:"decoded,omitempty"`
}

func (s *Server) handleSlots(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	opts, err := parseListSlotsOptions(c)
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid node_id")
		return
	}
	items, err := s.management.ListSlots(c.Request.Context(), opts)
	if err != nil {
		if leaderConsistentReadUnavailable(err) {
			jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "controller leader consistent read unavailable")
			return
		}
		jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	c.JSON(http.StatusOK, SlotsResponse{
		Total: len(items),
		Items: slotDTOs(items),
	})
}

func parseListSlotsOptions(c *gin.Context) (managementusecase.ListSlotsOptions, error) {
	rawNodeID := c.Query("node_id")
	if rawNodeID == "" {
		return managementusecase.ListSlotsOptions{}, nil
	}
	nodeID, err := parseNodeIDParam(rawNodeID)
	if err != nil {
		return managementusecase.ListSlotsOptions{}, err
	}
	return managementusecase.ListSlotsOptions{NodeID: nodeID}, nil
}

func (s *Server) handleSlot(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	slotID, err := parseSlotIDParam(c.Param("slot_id"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid slot_id")
		return
	}
	item, err := s.management.GetSlot(c.Request.Context(), slotID)
	if err != nil {
		if errors.Is(err, controllermeta.ErrNotFound) {
			jsonError(c, http.StatusNotFound, "not_found", "slot not found")
			return
		}
		if leaderConsistentReadUnavailable(err) {
			jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "controller leader consistent read unavailable")
			return
		}
		jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	c.JSON(http.StatusOK, slotDetailDTO(item))
}

func (s *Server) handleSlotLogs(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	req, err := parseSlotLogEntriesRequest(c)
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	page, err := s.management.ListSlotLogEntries(c.Request.Context(), req)
	if err != nil {
		if errors.Is(err, raftcluster.ErrSlotNotFound) || errors.Is(err, controllermeta.ErrNotFound) {
			jsonError(c, http.StatusNotFound, "not_found", "slot log not found")
			return
		}
		if leaderConsistentReadUnavailable(err) {
			jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "slot log read unavailable")
			return
		}
		jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	c.JSON(http.StatusOK, slotLogEntriesDTO(page))
}

func parseSlotLogEntriesRequest(c *gin.Context) (managementusecase.ListSlotLogEntriesRequest, error) {
	slotID, err := parseSlotIDParam(c.Param("slot_id"))
	if err != nil {
		return managementusecase.ListSlotLogEntriesRequest{}, errors.New("invalid slot_id")
	}
	nodeID, err := parseNodeIDParam(c.Query("node_id"))
	if err != nil {
		return managementusecase.ListSlotLogEntriesRequest{}, errors.New("invalid node_id")
	}
	limit, err := parseSlotLogLimit(c.Query("limit"))
	if err != nil {
		return managementusecase.ListSlotLogEntriesRequest{}, errors.New("invalid limit")
	}
	cursor, err := parseSlotLogCursor(c.Query("cursor"))
	if err != nil {
		return managementusecase.ListSlotLogEntriesRequest{}, errors.New("invalid cursor")
	}
	return managementusecase.ListSlotLogEntriesRequest{
		NodeID: nodeID,
		SlotID: slotID,
		Limit:  limit,
		Cursor: cursor,
	}, nil
}

func slotDTOs(items []managementusecase.Slot) []SlotDTO {
	out := make([]SlotDTO, 0, len(items))
	for _, item := range items {
		out = append(out, slotDTO(item))
	}
	return out
}

func slotDTO(item managementusecase.Slot) SlotDTO {
	return SlotDTO{
		SlotID: item.SlotID,
		State: SlotStateDTO{
			Quorum:                item.State.Quorum,
			Sync:                  item.State.Sync,
			LeaderMatch:           item.State.LeaderMatch,
			LeaderTransferPending: item.State.LeaderTransferPending,
		},
		Assignment: SlotAssignmentDTO{
			DesiredPeers:      append([]uint64(nil), item.Assignment.DesiredPeers...),
			PreferredLeaderID: item.Assignment.PreferredLeader,
			ConfigEpoch:       item.Assignment.ConfigEpoch,
			BalanceVersion:    item.Assignment.BalanceVersion,
		},
		Runtime: SlotRuntimeDTO{
			CurrentPeers:        append([]uint64(nil), item.Runtime.CurrentPeers...),
			CurrentVoters:       append([]uint64(nil), item.Runtime.CurrentVoters...),
			LeaderID:            item.Runtime.LeaderID,
			HealthyVoters:       item.Runtime.HealthyVoters,
			HasQuorum:           item.Runtime.HasQuorum,
			ObservedConfigEpoch: item.Runtime.ObservedConfigEpoch,
			LastReportAt:        item.Runtime.LastReportAt,
		},
		NodeLog: slotNodeLogDTO(item.NodeLog),
	}
}

func slotNodeLogDTO(item *managementusecase.SlotNodeLogStatus) *SlotNodeLogDTO {
	if item == nil {
		return nil
	}
	return &SlotNodeLogDTO{
		NodeID:       item.NodeID,
		LeaderID:     item.LeaderID,
		CommitIndex:  item.CommitIndex,
		AppliedIndex: item.AppliedIndex,
	}
}

func slotLogEntriesDTO(page managementusecase.SlotLogEntriesResponse) SlotLogEntriesResponse {
	return SlotLogEntriesResponse{
		NodeID:       page.NodeID,
		SlotID:       page.SlotID,
		FirstIndex:   page.FirstIndex,
		LastIndex:    page.LastIndex,
		CommitIndex:  page.CommitIndex,
		AppliedIndex: page.AppliedIndex,
		NextCursor:   page.NextCursor,
		Items:        slotLogEntryDTOs(page.Items),
	}
}

func slotLogEntryDTOs(items []managementusecase.SlotLogEntry) []SlotLogEntryDTO {
	out := make([]SlotLogEntryDTO, 0, len(items))
	for _, item := range items {
		out = append(out, SlotLogEntryDTO{
			Index:        item.Index,
			Term:         item.Term,
			Type:         item.Type,
			DataSize:     item.DataSize,
			DecodeStatus: item.DecodeStatus,
			DecodedType:  item.DecodedType,
			Decoded:      item.Decoded,
		})
	}
	return out
}

func slotDetailDTO(item managementusecase.SlotDetail) SlotDetailDTO {
	return SlotDetailDTO{
		SlotDTO: slotDTO(item.Slot),
		Task:    slotTaskDTO(item.Task),
	}
}

func parseSlotLogLimit(raw string) (int, error) {
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0, strconv.ErrSyntax
	}
	return value, nil
}

func parseSlotLogCursor(raw string) (uint64, error) {
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, strconv.ErrSyntax
	}
	return value, nil
}

func slotTaskDTO(item *managementusecase.Task) *SlotTaskDTO {
	if item == nil {
		return nil
	}
	return &SlotTaskDTO{
		Kind:       item.Kind,
		Step:       item.Step,
		Status:     item.Status,
		SourceNode: item.SourceNode,
		TargetNode: item.TargetNode,
		Attempt:    item.Attempt,
		NextRunAt:  item.NextRunAt,
		LastError:  item.LastError,
	}
}
