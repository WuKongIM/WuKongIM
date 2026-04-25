package management

import (
	"context"
	"sort"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

// ChannelRuntimeMeta is the manager-facing channel runtime metadata DTO.
type ChannelRuntimeMeta struct {
	// ChannelID is the logical channel identifier.
	ChannelID string
	// ChannelType is the logical channel type.
	ChannelType int64
	// SlotID is the physical slot that owns the channel metadata.
	SlotID uint32
	// ChannelEpoch is the runtime channel epoch.
	ChannelEpoch uint64
	// LeaderEpoch is the runtime leader epoch.
	LeaderEpoch uint64
	// Leader is the current leader node ID.
	Leader uint64
	// Replicas is the configured replica set.
	Replicas []uint64
	// ISR is the current in-sync replica set.
	ISR []uint64
	// MinISR is the configured minimum in-sync replica count.
	MinISR int64
	// Status is the stable manager-facing runtime status string.
	Status string
}

// ChannelRuntimeMetaDetail is the manager-facing channel runtime metadata detail DTO.
type ChannelRuntimeMetaDetail struct {
	ChannelRuntimeMeta
	// HashSlot is the logical hash slot derived from the channel key.
	HashSlot uint16
	// Features is the raw runtime feature bitset.
	Features uint64
	// LeaseUntilMS is the leader lease deadline in milliseconds.
	LeaseUntilMS int64
}

// ChannelRuntimeMetaListCursor identifies the next manager list position.
type ChannelRuntimeMetaListCursor struct {
	// SlotID is the current physical slot position.
	SlotID uint32
	// ChannelID is the last emitted channel identifier within SlotID.
	ChannelID string
	// ChannelType is the last emitted channel type within SlotID.
	ChannelType int64
}

// ListChannelRuntimeMetaRequest configures a manager channel runtime page request.
type ListChannelRuntimeMetaRequest struct {
	// Limit is the maximum number of items to return.
	Limit int
	// Cursor is the optional resume position from the previous page.
	Cursor ChannelRuntimeMetaListCursor
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

// ListChannelRuntimeMeta returns a manager-facing page ordered by slot, channel ID, and channel type.
func (a *App) ListChannelRuntimeMeta(ctx context.Context, req ListChannelRuntimeMetaRequest) (ListChannelRuntimeMetaResponse, error) {
	if a == nil || a.cluster == nil || a.channelRuntimeMeta == nil {
		return ListChannelRuntimeMetaResponse{}, nil
	}
	if req.Limit <= 0 {
		return ListChannelRuntimeMetaResponse{}, metadb.ErrInvalidArgument
	}
	if err := validateChannelRuntimeMetaListCursor(req.Cursor); err != nil {
		return ListChannelRuntimeMetaResponse{}, err
	}

	slotIDs := append([]multiraft.SlotID(nil), a.cluster.SlotIDs()...)
	sort.Slice(slotIDs, func(i, j int) bool { return slotIDs[i] < slotIDs[j] })

	startIndex, err := channelRuntimeMetaStartSlotIndex(slotIDs, req.Cursor.SlotID)
	if err != nil {
		return ListChannelRuntimeMetaResponse{}, err
	}

	resp := ListChannelRuntimeMetaResponse{
		Items: make([]ChannelRuntimeMeta, 0, min(req.Limit, len(slotIDs))),
	}
	for i := startIndex; i < len(slotIDs) && len(resp.Items) < req.Limit; i++ {
		slotID := slotIDs[i]
		after := metadb.ChannelRuntimeMetaCursor{}
		if i == startIndex {
			after = req.Cursor.shardCursor()
		}

		page, cursor, done, err := a.channelRuntimeMeta.ScanChannelRuntimeMetaSlotPage(ctx, slotID, after, req.Limit-len(resp.Items))
		if err != nil {
			return ListChannelRuntimeMetaResponse{}, err
		}
		resp.Items = append(resp.Items, managerChannelRuntimeMetaItems(slotID, page)...)

		if !done {
			resp.HasMore = true
			resp.NextCursor = newChannelRuntimeMetaListCursor(slotID, cursor)
			return resp, nil
		}

		if len(resp.Items) == req.Limit {
			nextSlotID, hasMore, err := a.findNextChannelRuntimeMetaSlotWithData(ctx, slotIDs[i+1:])
			if err != nil {
				return ListChannelRuntimeMetaResponse{}, err
			}
			resp.HasMore = hasMore
			if hasMore {
				resp.NextCursor = ChannelRuntimeMetaListCursor{SlotID: uint32(nextSlotID)}
			}
			return resp, nil
		}
	}

	return resp, nil
}

// GetChannelRuntimeMeta returns one manager-facing authoritative channel runtime detail DTO.
func (a *App) GetChannelRuntimeMeta(ctx context.Context, channelID string, channelType int64) (ChannelRuntimeMetaDetail, error) {
	if a == nil || a.cluster == nil || a.channelRuntimeMeta == nil {
		return ChannelRuntimeMetaDetail{}, nil
	}
	if channelType <= 0 {
		return ChannelRuntimeMetaDetail{}, metadb.ErrInvalidArgument
	}

	meta, err := a.channelRuntimeMeta.GetChannelRuntimeMeta(ctx, channelID, channelType)
	if err != nil {
		return ChannelRuntimeMetaDetail{}, err
	}

	slotID := a.cluster.SlotForKey(channelID)
	return ChannelRuntimeMetaDetail{
		ChannelRuntimeMeta: managerChannelRuntimeMeta(slotID, meta),
		HashSlot:           a.cluster.HashSlotForKey(channelID),
		Features:           meta.Features,
		LeaseUntilMS:       meta.LeaseUntilMS,
	}, nil
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
	if cursor.ChannelID != "" && len(cursor.ChannelID) > 0 {
		return nil
	}
	return nil
}

func channelRuntimeMetaStartSlotIndex(slotIDs []multiraft.SlotID, slotID uint32) (int, error) {
	if slotID == 0 {
		return 0, nil
	}
	for i, current := range slotIDs {
		if current == multiraft.SlotID(slotID) {
			return i, nil
		}
	}
	return 0, metadb.ErrInvalidArgument
}

func (a *App) findNextChannelRuntimeMetaSlotWithData(ctx context.Context, slotIDs []multiraft.SlotID) (multiraft.SlotID, bool, error) {
	for _, slotID := range slotIDs {
		page, _, _, err := a.channelRuntimeMeta.ScanChannelRuntimeMetaSlotPage(ctx, slotID, metadb.ChannelRuntimeMetaCursor{}, 1)
		if err != nil {
			return 0, false, err
		}
		if len(page) > 0 {
			return slotID, true, nil
		}
	}
	return 0, false, nil
}

func managerChannelRuntimeMetaItems(slotID multiraft.SlotID, metas []metadb.ChannelRuntimeMeta) []ChannelRuntimeMeta {
	out := make([]ChannelRuntimeMeta, 0, len(metas))
	for _, meta := range metas {
		out = append(out, managerChannelRuntimeMeta(slotID, meta))
	}
	return out
}

func managerChannelRuntimeMeta(slotID multiraft.SlotID, meta metadb.ChannelRuntimeMeta) ChannelRuntimeMeta {
	return ChannelRuntimeMeta{
		ChannelID:    meta.ChannelID,
		ChannelType:  meta.ChannelType,
		SlotID:       uint32(slotID),
		ChannelEpoch: meta.ChannelEpoch,
		LeaderEpoch:  meta.LeaderEpoch,
		Leader:       meta.Leader,
		Replicas:     append([]uint64(nil), meta.Replicas...),
		ISR:          append([]uint64(nil), meta.ISR...),
		MinISR:       meta.MinISR,
		Status:       managerChannelRuntimeStatus(meta.Status),
	}
}

func managerChannelRuntimeStatus(status uint8) string {
	switch channel.Status(status) {
	case channel.StatusCreating:
		return "creating"
	case channel.StatusActive:
		return "active"
	case channel.StatusDeleting:
		return "deleting"
	case channel.StatusDeleted:
		return "deleted"
	default:
		return "unknown"
	}
}

func newChannelRuntimeMetaListCursor(slotID multiraft.SlotID, cursor metadb.ChannelRuntimeMetaCursor) ChannelRuntimeMetaListCursor {
	return ChannelRuntimeMetaListCursor{
		SlotID:      uint32(slotID),
		ChannelID:   cursor.ChannelID,
		ChannelType: cursor.ChannelType,
	}
}

func (c ChannelRuntimeMetaListCursor) shardCursor() metadb.ChannelRuntimeMetaCursor {
	return metadb.ChannelRuntimeMetaCursor{
		ChannelID:   c.ChannelID,
		ChannelType: c.ChannelType,
	}
}

func min(left, right int) int {
	if left < right {
		return left
	}
	return right
}
