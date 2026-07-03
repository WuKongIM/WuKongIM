package management

import (
	"context"
	"errors"
	"hash/crc32"
	"math"
	"sort"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const (
	defaultBusinessChannelInternalScanLimit = 200
	internalMemberListChannelPrefix         = "__wk_internal_memberlist__/"
	derivedCommandChannelSuffix             = "____cmd"
)

// ErrBusinessChannelReaderUnavailable reports that a requested node channel source is not available.
var ErrBusinessChannelReaderUnavailable = errors.New("internalv2/usecase/management: business channel reader unavailable")

// ChannelBusinessReader exposes authoritative channel metadata scans.
type ChannelBusinessReader interface {
	// ScanChannelsSlotPage returns one channel metadata page for a physical Slot.
	ScanChannelsSlotPage(ctx context.Context, slotID uint32, after metadb.ChannelCursor, limit int) ([]metadb.Channel, metadb.ChannelCursor, bool, error)
}

// RemoteBusinessChannelReader reads manager channel pages from another node.
type RemoteBusinessChannelReader interface {
	// NodeBusinessChannels returns one manager channel page from a selected cluster node.
	NodeBusinessChannels(ctx context.Context, req ListBusinessChannelsRequest) (ListBusinessChannelsResponse, error)
}

// ChannelListCursor identifies the next manager channel list position.
type ChannelListCursor struct {
	// SlotID is the current physical Slot scan position.
	SlotID uint32
	// ChannelID is the last emitted channel ID inside SlotID.
	ChannelID string
	// ChannelType is the last emitted channel type inside SlotID.
	ChannelType int64
	// TypeFilter binds the cursor to the requested type filter. Zero means all types.
	TypeFilter int64
	// KeywordHash binds the opaque cursor to the keyword used to create it.
	KeywordHash uint32
}

// ListBusinessChannelsRequest configures a manager business channel page.
type ListBusinessChannelsRequest struct {
	// NodeID optionally filters to one cluster node. Zero means the local node.
	NodeID uint64
	// Limit is the maximum number of items to return.
	Limit int
	// Cursor resumes a previous business channel list request.
	Cursor ChannelListCursor
	// TypeFilter optionally limits rows to one channel type. Zero means all types.
	TypeFilter int64
	// Keyword optionally limits rows to channel IDs containing this substring.
	Keyword string
}

// ListBusinessChannelsResponse is the manager business channel page result.
type ListBusinessChannelsResponse struct {
	// Items contains the ordered page items.
	Items []BusinessChannelListItem
	// HasMore reports whether another page exists after this one.
	HasMore bool
	// NextCursor identifies the next page position when HasMore is true.
	NextCursor ChannelListCursor
}

// BusinessChannelListItem is the manager-facing business channel summary.
type BusinessChannelListItem struct {
	// ChannelID is the logical channel identifier.
	ChannelID string
	// ChannelType is the legacy WuKong channel type.
	ChannelType int64
	// SlotID is the physical Slot that owns the channel metadata.
	SlotID uint32
	// HashSlot is the logical hash slot derived from the channel ID.
	HashSlot uint16
	// Ban reports whether the channel is banned.
	Ban bool
	// Disband reports whether the channel is disbanded.
	Disband bool
	// SendBan reports whether sending is blocked for the channel.
	SendBan bool
	// SubscriberMutationVersion is the durable subscriber mutation fence.
	SubscriberMutationVersion uint64
}

// ListBusinessChannels returns a manager-facing page ordered by Slot and channel key.
func (a *App) ListBusinessChannels(ctx context.Context, req ListBusinessChannelsRequest) (ListBusinessChannelsResponse, error) {
	if err := ctxErr(ctx); err != nil {
		return ListBusinessChannelsResponse{}, err
	}
	localNodeID := a.localNodeID()
	if !a.businessChannelRequestTargetsLocal(req.NodeID, localNodeID) {
		if a == nil || a.remoteBusinessChannels == nil {
			return ListBusinessChannelsResponse{}, ErrBusinessChannelReaderUnavailable
		}
		return a.remoteBusinessChannels.NodeBusinessChannels(ctx, req)
	}
	if a == nil || a.cluster == nil || a.channelBusinessReader == nil {
		return ListBusinessChannelsResponse{}, nil
	}
	if req.Limit <= 0 || req.TypeFilter < 0 || req.TypeFilter > math.MaxUint8 {
		return ListBusinessChannelsResponse{}, metadb.ErrInvalidArgument
	}
	keyword := strings.TrimSpace(req.Keyword)
	keywordHash := channelBusinessKeywordHash(keyword)
	if err := validateChannelListCursor(req.Cursor, req.TypeFilter, keywordHash); err != nil {
		return ListBusinessChannelsResponse{}, err
	}
	snapshot, err := a.cluster.LocalControlSnapshot(ctx)
	if err != nil {
		return ListBusinessChannelsResponse{}, err
	}
	slotIDs := sortedSnapshotSlotIDs(snapshot.Slots)
	startIndex, err := channelStartSlotIndex(slotIDs, req.Cursor.SlotID)
	if err != nil {
		return ListBusinessChannelsResponse{}, err
	}

	resp := ListBusinessChannelsResponse{Items: make([]BusinessChannelListItem, 0, req.Limit)}
	for i := startIndex; i < len(slotIDs); i++ {
		slotID := slotIDs[i]
		after := metadb.ChannelCursor{}
		if i == startIndex {
			after = req.Cursor.shardCursor()
		}
		for {
			page, nextCursor, done, err := a.channelBusinessReader.ScanChannelsSlotPage(ctx, slotID, after, businessChannelScanLimit(req.Limit))
			if err != nil {
				return ListBusinessChannelsResponse{}, err
			}
			for _, ch := range page {
				if !businessChannelMatches(ch, req.TypeFilter, keyword) {
					continue
				}
				item := businessChannelListItem(snapshot.HashSlots, ch)
				if len(resp.Items) == req.Limit {
					resp.HasMore = true
					resp.NextCursor = channelListCursorForItem(resp.Items[len(resp.Items)-1], req.TypeFilter, keyword)
					return resp, nil
				}
				resp.Items = append(resp.Items, item)
			}
			if done {
				break
			}
			if nextCursor == after {
				break
			}
			after = nextCursor
		}
	}
	return resp, nil
}

func (a *App) businessChannelRequestTargetsLocal(nodeID, localNodeID uint64) bool {
	return nodeID == 0 || localNodeID == 0 || nodeID == localNodeID
}

func sortedSnapshotSlotIDs(assignments []control.SlotAssignment) []uint32 {
	out := make([]uint32, 0, len(assignments))
	for _, assignment := range assignments {
		out = append(out, assignment.SlotID)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func channelStartSlotIndex(slotIDs []uint32, slotID uint32) (int, error) {
	if slotID == 0 {
		return 0, nil
	}
	for i, current := range slotIDs {
		if current == slotID {
			return i, nil
		}
	}
	return 0, metadb.ErrInvalidArgument
}

func businessChannelListItem(table control.HashSlotTable, ch metadb.Channel) BusinessChannelListItem {
	hashSlot := routing.HashSlotForKey(ch.ChannelID, table.Count)
	return BusinessChannelListItem{
		ChannelID:                 ch.ChannelID,
		ChannelType:               ch.ChannelType,
		SlotID:                    slotIDForHashSlot(table, hashSlot),
		HashSlot:                  hashSlot,
		Ban:                       ch.Ban != 0,
		Disband:                   ch.Disband != 0,
		SendBan:                   ch.SendBan != 0,
		SubscriberMutationVersion: ch.SubscriberMutationVersion,
	}
}

func slotIDForHashSlot(table control.HashSlotTable, hashSlot uint16) uint32 {
	for _, item := range table.Ranges {
		if hashSlot >= item.From && hashSlot <= item.To {
			return item.SlotID
		}
	}
	return 0
}

func businessChannelMatches(ch metadb.Channel, typeFilter int64, keyword string) bool {
	if isInternalBusinessChannelID(ch.ChannelID) {
		return false
	}
	if typeFilter != 0 && ch.ChannelType != typeFilter {
		return false
	}
	if keyword != "" && !strings.Contains(ch.ChannelID, keyword) {
		return false
	}
	return true
}

func validateChannelListCursor(cursor ChannelListCursor, typeFilter int64, keywordHash uint32) error {
	if cursor == (ChannelListCursor{}) {
		return nil
	}
	if cursor.SlotID == 0 || cursor.ChannelID == "" || cursor.ChannelType <= 0 {
		return metadb.ErrInvalidArgument
	}
	if cursor.TypeFilter != typeFilter || cursor.KeywordHash != keywordHash {
		return metadb.ErrInvalidArgument
	}
	return nil
}

func channelListCursorForItem(item BusinessChannelListItem, typeFilter int64, keyword string) ChannelListCursor {
	return ChannelListCursor{
		SlotID:      item.SlotID,
		ChannelID:   item.ChannelID,
		ChannelType: item.ChannelType,
		TypeFilter:  typeFilter,
		KeywordHash: channelBusinessKeywordHash(keyword),
	}
}

func (c ChannelListCursor) shardCursor() metadb.ChannelCursor {
	return metadb.ChannelCursor{ChannelID: c.ChannelID, ChannelType: c.ChannelType}
}

func isInternalBusinessChannelID(channelID string) bool {
	return strings.HasPrefix(channelID, internalMemberListChannelPrefix) || strings.HasSuffix(channelID, derivedCommandChannelSuffix)
}

func businessChannelScanLimit(limit int) int {
	if limit > defaultBusinessChannelInternalScanLimit {
		return limit
	}
	return defaultBusinessChannelInternalScanLimit
}

func channelBusinessKeywordHash(keyword string) uint32 {
	return crc32.ChecksumIEEE([]byte(keyword))
}
