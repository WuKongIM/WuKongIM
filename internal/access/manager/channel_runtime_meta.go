package manager

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
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
	// SlotID is the owning physical slot identifier.
	SlotID uint32 `json:"slot_id"`
	// ChannelEpoch is the runtime channel epoch.
	ChannelEpoch uint64 `json:"channel_epoch"`
	// LeaderEpoch is the runtime leader epoch.
	LeaderEpoch uint64 `json:"leader_epoch"`
	// Leader is the current leader node ID.
	Leader uint64 `json:"leader"`
	// Replicas is the configured replica set.
	Replicas []uint64 `json:"replicas"`
	// ISR is the in-sync replica set.
	ISR []uint64 `json:"isr"`
	// MinISR is the configured minimum in-sync replica count.
	MinISR int64 `json:"min_isr"`
	// Status is the stable runtime status string.
	Status string `json:"status"`
}

// ChannelRuntimeMetaDetailDTO is the manager-facing channel runtime detail response body.
type ChannelRuntimeMetaDetailDTO struct {
	ChannelRuntimeMetaDTO
	// HashSlot is the logical hash slot derived from the channel key.
	HashSlot uint16 `json:"hash_slot"`
	// Features is the raw runtime feature bitset.
	Features uint64 `json:"features"`
	// LeaseUntilMS is the leader lease deadline in milliseconds.
	LeaseUntilMS int64 `json:"lease_until_ms"`
}

type channelRuntimeMetaCursorPayload struct {
	Version     int    `json:"v"`
	SlotID      uint32 `json:"slot_id"`
	ChannelID   string `json:"channel_id,omitempty"`
	ChannelType int64  `json:"channel_type,omitempty"`
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

	page, err := s.management.ListChannelRuntimeMeta(c.Request.Context(), managementusecase.ListChannelRuntimeMetaRequest{
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
	c.JSON(http.StatusOK, ChannelRuntimeMetaListResponse{
		Items:      channelRuntimeMetaDTOs(page.Items),
		HasMore:    page.HasMore,
		NextCursor: nextCursor,
	})
}

func (s *Server) handleChannelRuntimeMetaDetail(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}

	channelType, err := parseChannelRuntimeMetaChannelTypeParam(c.Param("channel_type"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid channel_type")
		return
	}

	item, err := s.management.GetChannelRuntimeMeta(c.Request.Context(), c.Param("channel_id"), channelType)
	if err != nil {
		switch {
		case errors.Is(err, metadb.ErrInvalidArgument):
			jsonError(c, http.StatusBadRequest, "bad_request", "invalid channel_type")
		case errors.Is(err, metadb.ErrNotFound):
			jsonError(c, http.StatusNotFound, "not_found", "channel runtime meta not found")
		case slotLeaderAuthoritativeReadUnavailable(err):
			jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "slot leader authoritative read unavailable")
		default:
			jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
		}
		return
	}

	c.JSON(http.StatusOK, channelRuntimeMetaDetailDTO(item))
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

func parseChannelRuntimeMetaChannelTypeParam(raw string) (int64, error) {
	if raw == "" {
		return 0, strconv.ErrSyntax
	}
	channelType, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || channelType <= 0 {
		return 0, strconv.ErrSyntax
	}
	return channelType, nil
}

func encodeChannelRuntimeMetaCursor(cursor managementusecase.ChannelRuntimeMetaListCursor) (string, error) {
	if cursor == (managementusecase.ChannelRuntimeMetaListCursor{}) {
		return "", nil
	}
	payload, err := json.Marshal(channelRuntimeMetaCursorPayload{
		Version:     1,
		SlotID:      cursor.SlotID,
		ChannelID:   cursor.ChannelID,
		ChannelType: cursor.ChannelType,
	})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

func decodeChannelRuntimeMetaCursor(raw string) (managementusecase.ChannelRuntimeMetaListCursor, error) {
	if raw == "" {
		return managementusecase.ChannelRuntimeMetaListCursor{}, nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return managementusecase.ChannelRuntimeMetaListCursor{}, err
	}
	var body channelRuntimeMetaCursorPayload
	if err := json.Unmarshal(payload, &body); err != nil {
		return managementusecase.ChannelRuntimeMetaListCursor{}, err
	}
	if err := validateChannelRuntimeMetaCursorPayload(body); err != nil {
		return managementusecase.ChannelRuntimeMetaListCursor{}, err
	}
	return managementusecase.ChannelRuntimeMetaListCursor{
		SlotID:      body.SlotID,
		ChannelID:   body.ChannelID,
		ChannelType: body.ChannelType,
	}, nil
}

func validateChannelRuntimeMetaCursorPayload(payload channelRuntimeMetaCursorPayload) error {
	if payload.Version != 1 {
		return strconv.ErrSyntax
	}
	if payload.SlotID == 0 {
		if payload.ChannelID == "" && payload.ChannelType == 0 {
			return nil
		}
		return strconv.ErrSyntax
	}
	if payload.ChannelID == "" && payload.ChannelType != 0 {
		return strconv.ErrSyntax
	}
	return nil
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
		ChannelID:    item.ChannelID,
		ChannelType:  item.ChannelType,
		SlotID:       item.SlotID,
		ChannelEpoch: item.ChannelEpoch,
		LeaderEpoch:  item.LeaderEpoch,
		Leader:       item.Leader,
		Replicas:     append([]uint64(nil), item.Replicas...),
		ISR:          append([]uint64(nil), item.ISR...),
		MinISR:       item.MinISR,
		Status:       item.Status,
	}
}

func channelRuntimeMetaDetailDTO(item managementusecase.ChannelRuntimeMetaDetail) ChannelRuntimeMetaDetailDTO {
	return ChannelRuntimeMetaDetailDTO{
		ChannelRuntimeMetaDTO: channelRuntimeMetaDTO(item.ChannelRuntimeMeta),
		HashSlot:              item.HashSlot,
		Features:              item.Features,
		LeaseUntilMS:          item.LeaseUntilMS,
	}
}

func slotLeaderAuthoritativeReadUnavailable(err error) bool {
	return errors.Is(err, raftcluster.ErrNoLeader) ||
		errors.Is(err, raftcluster.ErrNotLeader) ||
		errors.Is(err, raftcluster.ErrSlotNotFound) ||
		errors.Is(err, raftcluster.ErrNotStarted) ||
		errors.Is(err, context.DeadlineExceeded)
}
