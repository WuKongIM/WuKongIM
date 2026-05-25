package management

import (
	"context"
	"fmt"
	"hash/crc32"
	"math"
	"sort"
	"strings"

	channelmembers "github.com/WuKongIM/WuKongIM/internal/contracts/channelmembers"
	channelusecase "github.com/WuKongIM/WuKongIM/internal/usecase/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

const (
	defaultBusinessChannelInternalScanLimit = 200
	businessMemberListSubscribers           = "subscribers"
	businessMemberListAllowlist             = "allowlist"
	businessMemberListDenylist              = "denylist"
	internalMemberListChannelPrefix         = "__wk_internal_memberlist__/"
	derivedCommandChannelSuffix             = "____cmd"
)

const (
	channelMemberListKindSubscribers uint8 = iota + 1
	channelMemberListKindAllowlist
	channelMemberListKindDenylist
)

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

// BusinessChannelDetail is the manager-facing business channel detail.
type BusinessChannelDetail struct {
	BusinessChannelListItem
	// HasSubscribers reports whether the ordinary subscriber list is non-empty.
	HasSubscribers bool
	// HasAllowlist reports whether the allowlist is non-empty.
	HasAllowlist bool
	// HasDenylist reports whether the denylist is non-empty.
	HasDenylist bool
}

// UpsertBusinessChannelRequest configures a manager channel metadata upsert.
type UpsertBusinessChannelRequest struct {
	// ChannelID is the logical channel identifier.
	ChannelID string
	// ChannelType is the legacy WuKong channel type.
	ChannelType int64
	// Ban blocks channel messaging when true.
	Ban bool
	// Disband marks the channel as disbanded.
	Disband bool
	// SendBan blocks sends while preserving receive semantics.
	SendBan bool
}

// ChannelMemberCursor identifies the next manager channel member-list position.
type ChannelMemberCursor struct {
	// ChannelIDHash binds the cursor to the requested channel ID.
	ChannelIDHash uint32
	// ChannelType binds the cursor to the requested channel type.
	ChannelType int64
	// ListKind identifies subscribers, allowlist, or denylist.
	ListKind uint8
	// UID is the last emitted UID.
	UID string
}

// BusinessChannelMember is one manager-facing channel member.
type BusinessChannelMember struct {
	// UID is the member user identifier.
	UID string
}

// ListBusinessChannelMembersRequest configures one manager member-list page.
type ListBusinessChannelMembersRequest struct {
	// ChannelID is the logical channel identifier.
	ChannelID string
	// ChannelType is the legacy WuKong channel type.
	ChannelType int64
	// ListKind is subscribers, allowlist, or denylist.
	ListKind string
	// Limit is the maximum number of members to return.
	Limit int
	// Cursor resumes a previous member-list request.
	Cursor ChannelMemberCursor
}

// ListBusinessChannelMembersResponse is one manager member-list page.
type ListBusinessChannelMembersResponse struct {
	// Items contains the ordered page members.
	Items []BusinessChannelMember
	// HasMore reports whether another page exists after this one.
	HasMore bool
	// NextCursor identifies the next page position when HasMore is true.
	NextCursor ChannelMemberCursor
}

// MutateBusinessChannelMembersRequest configures a member-list mutation.
type MutateBusinessChannelMembersRequest struct {
	// ChannelID is the logical channel identifier.
	ChannelID string
	// ChannelType is the legacy WuKong channel type.
	ChannelType int64
	// ListKind is subscribers, allowlist, or denylist.
	ListKind string
	// UIDs contains candidate member user identifiers.
	UIDs []string
	// Add selects add when true and remove when false.
	Add bool
}

// MutateBusinessChannelMembersResponse reports a member-list mutation result.
type MutateBusinessChannelMembersResponse struct {
	// ChannelID is the logical channel identifier.
	ChannelID string
	// ChannelType is the legacy WuKong channel type.
	ChannelType int64
	// ListKind is subscribers, allowlist, or denylist.
	ListKind string
	// Changed reports whether the mutation was accepted.
	Changed bool
}

// ListBusinessChannels returns a manager-facing page ordered by Slot and channel key.
func (a *App) ListBusinessChannels(ctx context.Context, req ListBusinessChannelsRequest) (ListBusinessChannelsResponse, error) {
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

	slotIDs := append([]multiraft.SlotID(nil), a.cluster.SlotIDs()...)
	sort.Slice(slotIDs, func(i, j int) bool { return slotIDs[i] < slotIDs[j] })
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
				item := a.businessChannelListItem(ch)
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

// GetBusinessChannel returns one manager-facing authoritative channel detail.
func (a *App) GetBusinessChannel(ctx context.Context, channelID string, channelType int64) (BusinessChannelDetail, error) {
	if a == nil || a.cluster == nil || a.channelBusinessReader == nil {
		return BusinessChannelDetail{}, nil
	}
	channelID, typed, err := validateBusinessChannelKey(channelID, channelType)
	if err != nil {
		return BusinessChannelDetail{}, err
	}
	ch, err := a.channelBusinessReader.GetChannelForPermission(ctx, channelID, int64(typed))
	if err != nil {
		return BusinessChannelDetail{}, err
	}
	return a.businessChannelDetail(ctx, ch)
}

// UpsertBusinessChannel updates channel flags and returns the fresh detail.
func (a *App) UpsertBusinessChannel(ctx context.Context, req UpsertBusinessChannelRequest) (BusinessChannelDetail, error) {
	if a == nil || a.channelBusinessOperator == nil {
		return BusinessChannelDetail{}, fmt.Errorf("management: channel business operator not configured")
	}
	channelID, channelType, err := validateBusinessChannelKey(req.ChannelID, req.ChannelType)
	if err != nil {
		return BusinessChannelDetail{}, err
	}
	if err := a.channelBusinessOperator.UpdateInfo(ctx, channelusecase.Info{
		ChannelID:   channelID,
		ChannelType: channelType,
		Ban:         req.Ban,
		Disband:     req.Disband,
		SendBan:     req.SendBan,
	}); err != nil {
		return BusinessChannelDetail{}, err
	}
	return a.GetBusinessChannel(ctx, channelID, int64(channelType))
}

// ListBusinessChannelMembers returns one manager-facing member-list page.
func (a *App) ListBusinessChannelMembers(ctx context.Context, req ListBusinessChannelMembersRequest) (ListBusinessChannelMembersResponse, error) {
	if a == nil || a.channelBusinessOperator == nil {
		return ListBusinessChannelMembersResponse{}, fmt.Errorf("management: channel business operator not configured")
	}
	channelID, channelType, err := validateBusinessChannelKey(req.ChannelID, req.ChannelType)
	if err != nil {
		return ListBusinessChannelMembersResponse{}, err
	}
	kind, kindCode, err := parseBusinessMemberListKind(req.ListKind)
	if err != nil {
		return ListBusinessChannelMembersResponse{}, err
	}
	if req.Limit <= 0 {
		return ListBusinessChannelMembersResponse{}, metadb.ErrInvalidArgument
	}
	if err := validateChannelMemberCursor(req.Cursor, channelID, int64(channelType), kindCode); err != nil {
		return ListBusinessChannelMembersResponse{}, err
	}

	pageReq := channelusecase.MemberListPageRequest{
		ChannelKey: channelusecase.ChannelKey{ChannelID: channelID, ChannelType: channelType},
		AfterUID:   req.Cursor.UID,
		Limit:      req.Limit,
	}
	page, err := a.listBusinessMemberPage(ctx, kind, pageReq)
	if err != nil {
		return ListBusinessChannelMembersResponse{}, err
	}
	items := make([]BusinessChannelMember, 0, len(page.Members))
	for _, member := range page.Members {
		items = append(items, BusinessChannelMember{UID: member.UID})
	}
	resp := ListBusinessChannelMembersResponse{Items: items, HasMore: page.HasMore}
	if page.HasMore {
		resp.NextCursor = ChannelMemberCursor{
			ChannelIDHash: crc32.ChecksumIEEE([]byte(channelID)),
			ChannelType:   int64(channelType),
			ListKind:      kindCode,
			UID:           page.NextCursor,
		}
	}
	return resp, nil
}

// MutateBusinessChannelMembers adds or removes manager-facing member-list entries.
func (a *App) MutateBusinessChannelMembers(ctx context.Context, req MutateBusinessChannelMembersRequest) (MutateBusinessChannelMembersResponse, error) {
	if a == nil || a.channelBusinessOperator == nil {
		return MutateBusinessChannelMembersResponse{}, fmt.Errorf("management: channel business operator not configured")
	}
	channelID, channelType, err := validateBusinessChannelKey(req.ChannelID, req.ChannelType)
	if err != nil {
		return MutateBusinessChannelMembersResponse{}, err
	}
	kind, _, err := parseBusinessMemberListKind(req.ListKind)
	if err != nil {
		return MutateBusinessChannelMembersResponse{}, err
	}
	uids := normalizeBusinessMemberUIDs(req.UIDs)
	if len(uids) == 0 {
		return MutateBusinessChannelMembersResponse{}, metadb.ErrInvalidArgument
	}
	if kind == businessMemberListSubscribers && channelType == frame.ChannelTypePerson {
		return MutateBusinessChannelMembersResponse{}, metadb.ErrInvalidArgument
	}

	key := channelusecase.ChannelKey{ChannelID: channelID, ChannelType: channelType}
	switch kind {
	case businessMemberListSubscribers:
		cmd := channelusecase.SubscriberCommand{ChannelID: channelID, ChannelType: channelType, Subscribers: uids}
		if req.Add {
			err = a.channelBusinessOperator.AddSubscribers(ctx, cmd)
		} else {
			err = a.channelBusinessOperator.RemoveSubscribers(ctx, cmd)
		}
	case businessMemberListAllowlist:
		cmd := channelusecase.MemberCommand{ChannelKey: key, UIDs: uids}
		if req.Add {
			err = a.channelBusinessOperator.AddAllowlist(ctx, cmd)
		} else {
			err = a.channelBusinessOperator.RemoveAllowlist(ctx, cmd)
		}
	case businessMemberListDenylist:
		cmd := channelusecase.MemberCommand{ChannelKey: key, UIDs: uids}
		if req.Add {
			err = a.channelBusinessOperator.AddDenylist(ctx, cmd)
		} else {
			err = a.channelBusinessOperator.RemoveDenylist(ctx, cmd)
		}
	}
	if err != nil {
		return MutateBusinessChannelMembersResponse{}, err
	}
	return MutateBusinessChannelMembersResponse{ChannelID: channelID, ChannelType: int64(channelType), ListKind: kind, Changed: true}, nil
}

func (a *App) businessChannelDetail(ctx context.Context, ch metadb.Channel) (BusinessChannelDetail, error) {
	item := a.businessChannelListItem(ch)
	hasSubscribers, err := a.channelBusinessReader.HasChannelSubscribers(ctx, ch.ChannelID, ch.ChannelType)
	if err != nil {
		return BusinessChannelDetail{}, err
	}
	allowID := businessMemberListChannelID(businessMemberListAllowlist, ch.ChannelID, ch.ChannelType)
	hasAllowlist, err := a.channelBusinessReader.HasChannelSubscribers(ctx, allowID, ch.ChannelType)
	if err != nil {
		return BusinessChannelDetail{}, err
	}
	denyID := businessMemberListChannelID(businessMemberListDenylist, ch.ChannelID, ch.ChannelType)
	hasDenylist, err := a.channelBusinessReader.HasChannelSubscribers(ctx, denyID, ch.ChannelType)
	if err != nil {
		return BusinessChannelDetail{}, err
	}
	return BusinessChannelDetail{
		BusinessChannelListItem: item,
		HasSubscribers:          hasSubscribers,
		HasAllowlist:            hasAllowlist,
		HasDenylist:             hasDenylist,
	}, nil
}

func (a *App) businessChannelListItem(ch metadb.Channel) BusinessChannelListItem {
	return BusinessChannelListItem{
		ChannelID:                 ch.ChannelID,
		ChannelType:               ch.ChannelType,
		SlotID:                    uint32(a.cluster.SlotForKey(ch.ChannelID)),
		HashSlot:                  a.cluster.HashSlotForKey(ch.ChannelID),
		Ban:                       ch.Ban != 0,
		Disband:                   ch.Disband != 0,
		SendBan:                   ch.SendBan != 0,
		SubscriberMutationVersion: ch.SubscriberMutationVersion,
	}
}

func (a *App) listBusinessMemberPage(ctx context.Context, kind string, req channelusecase.MemberListPageRequest) (channelusecase.MemberListPageResult, error) {
	switch kind {
	case businessMemberListSubscribers:
		return a.channelBusinessOperator.ListSubscribersPage(ctx, req)
	case businessMemberListAllowlist:
		return a.channelBusinessOperator.ListAllowlistPage(ctx, req)
	case businessMemberListDenylist:
		return a.channelBusinessOperator.ListDenylistPage(ctx, req)
	default:
		return channelusecase.MemberListPageResult{}, metadb.ErrInvalidArgument
	}
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

func validateBusinessChannelKey(channelID string, channelType int64) (string, uint8, error) {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" || isInternalBusinessChannelID(channelID) || channelType <= 0 || channelType > math.MaxUint8 {
		return "", 0, metadb.ErrInvalidArgument
	}
	return channelID, uint8(channelType), nil
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

func validateChannelMemberCursor(cursor ChannelMemberCursor, channelID string, channelType int64, listKind uint8) error {
	if cursor == (ChannelMemberCursor{}) {
		return nil
	}
	if cursor.UID == "" || cursor.ChannelIDHash != crc32.ChecksumIEEE([]byte(channelID)) || cursor.ChannelType != channelType || cursor.ListKind != listKind {
		return metadb.ErrInvalidArgument
	}
	return nil
}

func channelStartSlotIndex(slotIDs []multiraft.SlotID, slotID uint32) (int, error) {
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

func parseBusinessMemberListKind(raw string) (string, uint8, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case businessMemberListSubscribers, "":
		return businessMemberListSubscribers, channelMemberListKindSubscribers, nil
	case businessMemberListAllowlist:
		return businessMemberListAllowlist, channelMemberListKindAllowlist, nil
	case businessMemberListDenylist:
		return businessMemberListDenylist, channelMemberListKindDenylist, nil
	default:
		return "", 0, metadb.ErrInvalidArgument
	}
}

func businessMemberListChannelID(kind string, channelID string, channelType int64) string {
	key := channelmembers.ChannelKey{ChannelID: channelID, ChannelType: uint8(channelType)}
	switch kind {
	case businessMemberListAllowlist:
		return channelmembers.AllowlistChannelID(key)
	case businessMemberListDenylist:
		return channelmembers.DenylistChannelID(key)
	default:
		return channelID
	}
}

func isInternalBusinessChannelID(channelID string) bool {
	return strings.HasPrefix(channelID, internalMemberListChannelPrefix) || strings.HasSuffix(channelID, derivedCommandChannelSuffix)
}

func normalizeBusinessMemberUIDs(raw []string) []string {
	seen := make(map[string]struct{}, len(raw))
	out := make([]string, 0, len(raw))
	for _, uid := range raw {
		uid = strings.TrimSpace(uid)
		if uid == "" {
			continue
		}
		if _, ok := seen[uid]; ok {
			continue
		}
		seen[uid] = struct{}{}
		out = append(out, uid)
	}
	return out
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
