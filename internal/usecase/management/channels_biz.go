package management

import (
	"context"
	"errors"
	"hash/crc32"
	"math"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/WuKongIM/WuKongIM/internal/contracts/protocolmeta"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const (
	defaultBusinessChannelInternalScanLimit = 200
	businessMemberListSubscribers           = "subscribers"
	businessMemberListAllowlist             = "allowlist"
	businessMemberListDenylist              = "denylist"
	maxBusinessChannelMutationUIDs          = 500
	maxBusinessChannelUIDBytes              = 256
	maxNewBusinessChannelIDBytes            = 256
	internalMemberListChannelPrefix         = "__wk_internal_memberlist__/"
	derivedCommandChannelSuffix             = "____cmd"
)

const (
	channelMemberListKindSubscribers uint8 = iota + 1
	channelMemberListKindAllowlist
	channelMemberListKindDenylist
)

// ErrBusinessChannelReaderUnavailable reports that a requested node channel source is not available.
var ErrBusinessChannelReaderUnavailable = errors.New("internal/usecase/management: business channel reader unavailable")

// ErrBusinessChannelOperatorUnavailable reports that channel detail or mutation dependencies are absent.
var ErrBusinessChannelOperatorUnavailable = errors.New("internal/usecase/management: business channel operator unavailable")

// ChannelBusinessReader exposes authoritative channel metadata scans.
type ChannelBusinessReader interface {
	// ScanChannelsSlotPage returns one channel metadata page for a physical Slot.
	ScanChannelsSlotPage(ctx context.Context, slotID uint32, after metadb.ChannelCursor, limit int) ([]metadb.Channel, metadb.ChannelCursor, bool, error)
}

// ChannelBusinessOperator exposes authoritative detail, member, and mutation operations.
type ChannelBusinessOperator interface {
	// GetMetadata returns the authoritative channel metadata row.
	GetMetadata(context.Context, BusinessChannelKey) (metadb.Channel, error)
	// CreateMetadata creates one channel without overwriting an existing row.
	CreateMetadata(context.Context, BusinessChannelInfo) error
	// PatchMetadataFlags atomically changes only Manager-editable flags.
	PatchMetadataFlags(context.Context, BusinessChannelKey, BusinessChannelFlags) error
	// HasSubscribers reports whether the ordinary subscriber set is non-empty.
	HasSubscribers(context.Context, BusinessChannelKey) (bool, error)
	// HasAllowlist reports whether the allowlist is non-empty.
	HasAllowlist(context.Context, BusinessChannelKey) (bool, error)
	// HasDenylist reports whether the denylist is non-empty.
	HasDenylist(context.Context, BusinessChannelKey) (bool, error)
	// ContainsSubscriber performs an exact ordinary subscriber lookup.
	ContainsSubscriber(context.Context, BusinessChannelKey, string) (bool, error)
	// ContainsAllowlistMember performs an exact allowlist lookup.
	ContainsAllowlistMember(context.Context, BusinessChannelKey, string) (bool, error)
	// ContainsDenylistMember performs an exact denylist lookup.
	ContainsDenylistMember(context.Context, BusinessChannelKey, string) (bool, error)
	// ListSubscribersPage returns one bounded ordinary subscriber page.
	ListSubscribersPage(context.Context, BusinessChannelMemberPageRequest) (BusinessChannelMemberPageResult, error)
	// ListAllowlistPage returns one bounded allowlist page.
	ListAllowlistPage(context.Context, BusinessChannelMemberPageRequest) (BusinessChannelMemberPageResult, error)
	// ListDenylistPage returns one bounded denylist page.
	ListDenylistPage(context.Context, BusinessChannelMemberPageRequest) (BusinessChannelMemberPageResult, error)
	// MutateSubscribersCounted changes the ordinary subscriber set and returns durable counts.
	MutateSubscribersCounted(context.Context, BusinessChannelKey, []string, bool) (metadb.SubscriberMutationResult, error)
	// MutateAllowlistCounted changes the allowlist and returns durable counts.
	MutateAllowlistCounted(context.Context, BusinessChannelKey, []string, bool) (metadb.SubscriberMutationResult, error)
	// MutateDenylistCounted changes the denylist and returns durable counts.
	MutateDenylistCounted(context.Context, BusinessChannelKey, []string, bool) (metadb.SubscriberMutationResult, error)
}

// BusinessChannelKey identifies one channel without exposing a sibling usecase type.
type BusinessChannelKey struct {
	// ChannelID is the exact persisted channel identifier.
	ChannelID string
	// ChannelType is the legacy WuKong channel type.
	ChannelType uint8
}

// BusinessChannelInfo contains fields accepted for create-only channel metadata.
type BusinessChannelInfo struct {
	// BusinessChannelKey identifies the channel to create.
	BusinessChannelKey
	// Ban blocks all channel messaging.
	Ban bool
	// Disband marks the channel as disbanded.
	Disband bool
	// SendBan blocks sends while retaining receives.
	SendBan bool
}

// BusinessChannelFlags contains the only channel fields Manager may patch.
type BusinessChannelFlags struct {
	// Ban blocks all channel messaging.
	Ban bool
	// Disband marks the channel as disbanded.
	Disband bool
	// SendBan blocks sends while retaining receives.
	SendBan bool
}

// BusinessChannelMemberPageRequest identifies one bounded member-list page.
type BusinessChannelMemberPageRequest struct {
	// BusinessChannelKey identifies the parent business channel.
	BusinessChannelKey
	// AfterUID resumes after this exclusive UID cursor.
	AfterUID string
	// Limit bounds the returned row count.
	Limit int
}

// BusinessChannelMemberPageResult is one authoritative member-list page.
type BusinessChannelMemberPageResult struct {
	// UIDs contains the ordered member identifiers.
	UIDs []string
	// NextCursor resumes the next page when HasMore is true.
	NextCursor string
	// HasMore reports whether another page exists.
	HasMore bool
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

// BusinessChannelDetail is one authoritative Manager channel detail.
type BusinessChannelDetail struct {
	// BusinessChannelListItem contains the channel summary fields.
	BusinessChannelListItem
	// HasSubscribers reports whether the ordinary subscriber set is non-empty.
	HasSubscribers bool
	// HasAllowlist reports whether the allowlist is non-empty.
	HasAllowlist bool
	// HasDenylist reports whether the denylist is non-empty.
	HasDenylist bool
}

// CreateBusinessChannelRequest creates one new business channel.
type CreateBusinessChannelRequest struct {
	// ChannelID is the proposed new channel identifier.
	ChannelID string
	// ChannelType is the legacy WuKong channel type.
	ChannelType int64
	// Ban blocks all channel messaging.
	Ban bool
	// Disband marks the channel as disbanded.
	Disband bool
	// SendBan blocks sends while retaining receives.
	SendBan bool
}

// UpdateBusinessChannelRequest patches the Manager-editable flags of one channel.
type UpdateBusinessChannelRequest struct {
	// ChannelID is the exact persisted channel identifier.
	ChannelID string
	// ChannelType is the legacy WuKong channel type.
	ChannelType int64
	// Ban blocks all channel messaging.
	Ban bool
	// Disband marks the channel as disbanded.
	Disband bool
	// SendBan blocks sends while retaining receives.
	SendBan bool
}

// ChannelMemberCursor identifies the next member page and binds it to its list.
type ChannelMemberCursor struct {
	// ChannelIDHash binds the cursor to its parent channel without exposing it.
	ChannelIDHash uint32
	// ChannelType binds the cursor to the parent channel type.
	ChannelType int64
	// ListKind binds the cursor to subscribers, allowlist, or denylist.
	ListKind uint8
	// UID is the exclusive storage cursor for the next page.
	UID string
}

// BusinessChannelMember is one member-list row.
type BusinessChannelMember struct {
	// UID is the exact member identifier.
	UID string
}

// ListBusinessChannelMembersRequest configures a page or exact UID lookup.
type ListBusinessChannelMembersRequest struct {
	// ChannelID is the exact parent channel identifier.
	ChannelID string
	// ChannelType is the legacy WuKong channel type.
	ChannelType int64
	// ListKind identifies subscribers, allowlist, or denylist.
	ListKind string
	// Limit bounds page size.
	Limit int
	// Cursor resumes an ordinary page.
	Cursor ChannelMemberCursor
	// UID requests an exact lookup and is mutually exclusive with Cursor.
	UID string
}

// ListBusinessChannelMembersResponse is one member-list page.
type ListBusinessChannelMembersResponse struct {
	// Items contains the ordered UID-only rows.
	Items []BusinessChannelMember
	// HasMore reports whether another page exists.
	HasMore bool
	// NextCursor resumes the next page when HasMore is true.
	NextCursor ChannelMemberCursor
}

// MutateBusinessChannelMembersRequest configures one bounded set mutation.
type MutateBusinessChannelMembersRequest struct {
	// ChannelID is the exact parent channel identifier.
	ChannelID string
	// ChannelType is the legacy WuKong channel type.
	ChannelType int64
	// ListKind identifies subscribers, allowlist, or denylist.
	ListKind string
	// UIDs contains the proposed member identifiers.
	UIDs []string
	// Add selects add when true and remove when false.
	Add bool
}

// MutateBusinessChannelMembersResponse reports requested and durable changes.
type MutateBusinessChannelMembersResponse struct {
	// ChannelID is the exact parent channel identifier.
	ChannelID string
	// ChannelType is the legacy WuKong channel type.
	ChannelType int64
	// ListKind identifies subscribers, allowlist, or denylist.
	ListKind string
	// RequestedCount is the normalized distinct UID count.
	RequestedCount int
	// ChangedCount is the exact durable set-change count.
	ChangedCount int
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

// GetBusinessChannel returns one authoritative business channel detail.
func (a *App) GetBusinessChannel(ctx context.Context, channelID string, channelType int64) (BusinessChannelDetail, error) {
	if a == nil || a.channelBusinessOperator == nil || a.cluster == nil {
		return BusinessChannelDetail{}, ErrBusinessChannelOperatorUnavailable
	}
	channelID, typed, err := validateExistingBusinessChannelKey(channelID, channelType)
	if err != nil {
		return BusinessChannelDetail{}, err
	}
	key := BusinessChannelKey{ChannelID: channelID, ChannelType: typed}
	ch, err := a.channelBusinessOperator.GetMetadata(ctx, key)
	if err != nil {
		return BusinessChannelDetail{}, err
	}
	snapshot, err := a.cluster.LocalControlSnapshot(ctx)
	if err != nil {
		return BusinessChannelDetail{}, err
	}
	hasSubscribers, err := a.channelBusinessOperator.HasSubscribers(ctx, key)
	if err != nil {
		return BusinessChannelDetail{}, err
	}
	hasAllowlist, err := a.channelBusinessOperator.HasAllowlist(ctx, key)
	if err != nil {
		return BusinessChannelDetail{}, err
	}
	hasDenylist, err := a.channelBusinessOperator.HasDenylist(ctx, key)
	if err != nil {
		return BusinessChannelDetail{}, err
	}
	return BusinessChannelDetail{
		BusinessChannelListItem: businessChannelListItem(snapshot.HashSlots, ch),
		HasSubscribers:          hasSubscribers,
		HasAllowlist:            hasAllowlist,
		HasDenylist:             hasDenylist,
	}, nil
}

// CreateBusinessChannel creates a new channel and returns its fresh detail.
func (a *App) CreateBusinessChannel(ctx context.Context, req CreateBusinessChannelRequest) (BusinessChannelDetail, error) {
	if a == nil || a.channelBusinessOperator == nil {
		return BusinessChannelDetail{}, ErrBusinessChannelOperatorUnavailable
	}
	channelID, channelType, err := validateNewBusinessChannelKey(req.ChannelID, req.ChannelType)
	if err != nil {
		return BusinessChannelDetail{}, err
	}
	err = a.channelBusinessOperator.CreateMetadata(ctx, BusinessChannelInfo{
		BusinessChannelKey: BusinessChannelKey{ChannelID: channelID, ChannelType: channelType},
		Ban:                req.Ban, Disband: req.Disband, SendBan: req.SendBan,
	})
	if err != nil {
		return BusinessChannelDetail{}, err
	}
	return a.GetBusinessChannel(ctx, channelID, int64(channelType))
}

// UpdateBusinessChannel patches only Ban, Disband, and SendBan.
func (a *App) UpdateBusinessChannel(ctx context.Context, req UpdateBusinessChannelRequest) (BusinessChannelDetail, error) {
	if a == nil || a.channelBusinessOperator == nil {
		return BusinessChannelDetail{}, ErrBusinessChannelOperatorUnavailable
	}
	channelID, channelType, err := validateExistingBusinessChannelKey(req.ChannelID, req.ChannelType)
	if err != nil {
		return BusinessChannelDetail{}, err
	}
	err = a.channelBusinessOperator.PatchMetadataFlags(
		ctx,
		BusinessChannelKey{ChannelID: channelID, ChannelType: channelType},
		BusinessChannelFlags{Ban: req.Ban, Disband: req.Disband, SendBan: req.SendBan},
	)
	if err != nil {
		return BusinessChannelDetail{}, err
	}
	return a.GetBusinessChannel(ctx, channelID, int64(channelType))
}

// ListBusinessChannelMembers returns one bounded page or one exact UID hit.
func (a *App) ListBusinessChannelMembers(ctx context.Context, req ListBusinessChannelMembersRequest) (ListBusinessChannelMembersResponse, error) {
	if a == nil || a.channelBusinessOperator == nil {
		return ListBusinessChannelMembersResponse{}, ErrBusinessChannelOperatorUnavailable
	}
	channelID, channelType, err := validateExistingBusinessChannelKey(req.ChannelID, req.ChannelType)
	if err != nil {
		return ListBusinessChannelMembersResponse{}, err
	}
	kind, kindCode, err := parseBusinessMemberListKind(req.ListKind)
	if err != nil || req.Limit <= 0 {
		return ListBusinessChannelMembersResponse{}, metadb.ErrInvalidArgument
	}
	if req.UID != "" && req.Cursor != (ChannelMemberCursor{}) {
		return ListBusinessChannelMembersResponse{}, metadb.ErrInvalidArgument
	}
	if err := validateChannelMemberCursor(req.Cursor, channelID, int64(channelType), kindCode); err != nil {
		return ListBusinessChannelMembersResponse{}, err
	}
	key := BusinessChannelKey{ChannelID: channelID, ChannelType: channelType}
	if _, err := a.channelBusinessOperator.GetMetadata(ctx, key); err != nil {
		return ListBusinessChannelMembersResponse{}, err
	}
	if req.UID != "" {
		uids, err := normalizeBusinessMemberUIDs([]string{req.UID})
		if err != nil {
			return ListBusinessChannelMembersResponse{}, err
		}
		found, err := a.containsBusinessMember(ctx, kind, key, uids[0])
		if err != nil {
			return ListBusinessChannelMembersResponse{}, err
		}
		resp := ListBusinessChannelMembersResponse{Items: make([]BusinessChannelMember, 0, 1)}
		if found {
			resp.Items = append(resp.Items, BusinessChannelMember{UID: uids[0]})
		}
		return resp, nil
	}

	pageReq := BusinessChannelMemberPageRequest{
		BusinessChannelKey: key,
		AfterUID:           req.Cursor.UID,
		Limit:              req.Limit,
	}
	page, err := a.listBusinessMemberPage(ctx, kind, pageReq)
	if err != nil {
		return ListBusinessChannelMembersResponse{}, err
	}
	resp := ListBusinessChannelMembersResponse{
		Items:   make([]BusinessChannelMember, 0, len(page.UIDs)),
		HasMore: page.HasMore,
	}
	for _, uid := range page.UIDs {
		resp.Items = append(resp.Items, BusinessChannelMember{UID: uid})
	}
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

// MutateBusinessChannelMembers applies one idempotent set mutation.
func (a *App) MutateBusinessChannelMembers(ctx context.Context, req MutateBusinessChannelMembersRequest) (MutateBusinessChannelMembersResponse, error) {
	if a == nil || a.channelBusinessOperator == nil {
		return MutateBusinessChannelMembersResponse{}, ErrBusinessChannelOperatorUnavailable
	}
	channelID, channelType, err := validateExistingBusinessChannelKey(req.ChannelID, req.ChannelType)
	if err != nil {
		return MutateBusinessChannelMembersResponse{}, err
	}
	kind, _, err := parseBusinessMemberListKind(req.ListKind)
	if err != nil {
		return MutateBusinessChannelMembersResponse{}, err
	}
	uids, err := normalizeBusinessMemberUIDs(req.UIDs)
	if err != nil || len(uids) == 0 || len(uids) > maxBusinessChannelMutationUIDs {
		return MutateBusinessChannelMembersResponse{}, metadb.ErrInvalidArgument
	}
	if kind == businessMemberListSubscribers && channelType == uint8(protocolmeta.ChannelTypePerson) {
		return MutateBusinessChannelMembersResponse{}, metadb.ErrInvalidArgument
	}
	resp := MutateBusinessChannelMembersResponse{
		ChannelID:      channelID,
		ChannelType:    int64(channelType),
		ListKind:       kind,
		RequestedCount: len(uids),
	}
	key := BusinessChannelKey{ChannelID: channelID, ChannelType: channelType}
	if _, err := a.channelBusinessOperator.GetMetadata(ctx, key); err != nil {
		return resp, err
	}
	var result metadb.SubscriberMutationResult
	switch kind {
	case businessMemberListSubscribers:
		result, err = a.channelBusinessOperator.MutateSubscribersCounted(ctx, key, uids, req.Add)
	case businessMemberListAllowlist:
		result, err = a.channelBusinessOperator.MutateAllowlistCounted(ctx, key, uids, req.Add)
	case businessMemberListDenylist:
		result, err = a.channelBusinessOperator.MutateDenylistCounted(ctx, key, uids, req.Add)
	}
	if err != nil {
		return resp, err
	}
	resp.RequestedCount = result.RequestedCount
	resp.ChangedCount = result.ChangedCount
	return resp, nil
}

func (a *App) containsBusinessMember(ctx context.Context, kind string, key BusinessChannelKey, uid string) (bool, error) {
	switch kind {
	case businessMemberListSubscribers:
		return a.channelBusinessOperator.ContainsSubscriber(ctx, key, uid)
	case businessMemberListAllowlist:
		return a.channelBusinessOperator.ContainsAllowlistMember(ctx, key, uid)
	case businessMemberListDenylist:
		return a.channelBusinessOperator.ContainsDenylistMember(ctx, key, uid)
	default:
		return false, metadb.ErrInvalidArgument
	}
}

func (a *App) listBusinessMemberPage(ctx context.Context, kind string, req BusinessChannelMemberPageRequest) (BusinessChannelMemberPageResult, error) {
	switch kind {
	case businessMemberListSubscribers:
		return a.channelBusinessOperator.ListSubscribersPage(ctx, req)
	case businessMemberListAllowlist:
		return a.channelBusinessOperator.ListAllowlistPage(ctx, req)
	case businessMemberListDenylist:
		return a.channelBusinessOperator.ListDenylistPage(ctx, req)
	default:
		return BusinessChannelMemberPageResult{}, metadb.ErrInvalidArgument
	}
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

func validateExistingBusinessChannelKey(channelID string, channelType int64) (string, uint8, error) {
	if channelID == "" || !utf8.ValidString(channelID) || isInternalBusinessChannelID(channelID) || channelType <= 0 || channelType > math.MaxUint8 {
		return "", 0, metadb.ErrInvalidArgument
	}
	return channelID, uint8(channelType), nil
}

func validateNewBusinessChannelKey(channelID string, channelType int64) (string, uint8, error) {
	channelID = strings.TrimSpace(channelID)
	channelID, typed, err := validateExistingBusinessChannelKey(channelID, channelType)
	if err != nil {
		return "", 0, err
	}
	if len([]byte(channelID)) > maxNewBusinessChannelIDBytes || strings.ContainsAny(channelID, "#@") {
		return "", 0, metadb.ErrInvalidArgument
	}
	return channelID, typed, nil
}

func parseBusinessMemberListKind(raw string) (string, uint8, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case businessMemberListSubscribers:
		return businessMemberListSubscribers, channelMemberListKindSubscribers, nil
	case businessMemberListAllowlist:
		return businessMemberListAllowlist, channelMemberListKindAllowlist, nil
	case businessMemberListDenylist:
		return businessMemberListDenylist, channelMemberListKindDenylist, nil
	default:
		return "", 0, metadb.ErrInvalidArgument
	}
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

func normalizeBusinessMemberUIDs(raw []string) ([]string, error) {
	seen := make(map[string]struct{}, len(raw))
	out := make([]string, 0, len(raw))
	for _, candidate := range raw {
		uid := strings.TrimSpace(candidate)
		if uid == "" || !utf8.ValidString(uid) || len([]byte(uid)) > maxBusinessChannelUIDBytes {
			return nil, metadb.ErrInvalidArgument
		}
		for _, r := range uid {
			if unicode.IsSpace(r) || unicode.IsControl(r) {
				return nil, metadb.ErrInvalidArgument
			}
		}
		if _, ok := seen[uid]; ok {
			continue
		}
		seen[uid] = struct{}{}
		out = append(out, uid)
		if len(out) > maxBusinessChannelMutationUIDs {
			return nil, metadb.ErrInvalidArgument
		}
	}
	return out, nil
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
