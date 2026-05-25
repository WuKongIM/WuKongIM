package delivery

import (
	"context"
	"errors"
	"strings"

	"github.com/WuKongIM/WuKongIM/internal/runtime/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

// ChannelSubscriberStore provides subscriber storage access for channel delivery.
type ChannelSubscriberStore interface {
	SnapshotChannelSubscribers(ctx context.Context, channelID string, channelType int64) ([]string, error)
	ListChannelSubscribers(ctx context.Context, channelID string, channelType int64, afterUID string, limit int) ([]string, string, bool, error)
}

// ChannelSubscriberMetadataStore exposes durable channel metadata for subscriber version fences.
type ChannelSubscriberMetadataStore interface {
	GetChannel(ctx context.Context, channelID string, channelType int64) (metadb.Channel, error)
}

// ChannelSubscriberPermissionMetadataStore exposes authoritative channel metadata for permission-sensitive reads.
type ChannelSubscriberPermissionMetadataStore interface {
	GetChannelForPermission(ctx context.Context, channelID string, channelType int64) (metadb.Channel, error)
}

// TemporarySubscriberSource exposes message-scoped temporary subscriber overlays.
type TemporarySubscriberSource interface {
	SnapshotTemporarySubscribers(ctx context.Context, channelID string, channelType int64) ([]string, error)
}

// SnapshotToken carries the resolved subscriber source and paging state.
type SnapshotToken struct {
	id     channel.ChannelID
	source SubscriberSource
	state  *subscriberSourceState
}

// Source returns the resolved subscriber source for this token.
func (t SnapshotToken) Source() SubscriberSource {
	return t.source
}

// SubscriberResolver resolves channel subscribers for delivery routing.
type SubscriberResolver interface {
	BeginSnapshot(ctx context.Context, id channel.ChannelID) (SnapshotToken, error)
	BeginSnapshotWithRequest(ctx context.Context, id channel.ChannelID, req SubscriberSnapshotRequest) (SnapshotToken, error)
	NextPage(ctx context.Context, token SnapshotToken, cursor string, limit int) ([]string, string, bool, error)
}

// SubscriberResolverOptions configures subscriber resolution behavior.
type SubscriberResolverOptions struct {
	Store     ChannelSubscriberStore
	Metadata  ChannelSubscriberMetadataStore
	Temporary TemporarySubscriberSource
}

type subscriberResolver struct {
	store     ChannelSubscriberStore
	metadata  ChannelSubscriberMetadataStore
	temporary TemporarySubscriberSource
}

type subscriberSourceState struct {
	staticSnapshot        []string
	overlayUIDs           []string
	overlayIndex          int
	storeChannelID        string
	storeChannelType      int64
	storeCursor           string
	seen                  map[string]struct{}
	snapshotFallbackTried bool
}

var errInvalidSubscriberPageLimit = errors.New("delivery: invalid subscriber page limit")

// NewSubscriberResolver creates a subscriber resolver for delivery routing.
func NewSubscriberResolver(opts SubscriberResolverOptions) SubscriberResolver {
	metadata := opts.Metadata
	if metadata == nil {
		if candidate, ok := opts.Store.(ChannelSubscriberMetadataStore); ok {
			metadata = candidate
		}
	}
	temporary := opts.Temporary
	if temporary == nil {
		if candidate, ok := opts.Store.(TemporarySubscriberSource); ok {
			temporary = candidate
		}
	}
	return &subscriberResolver{
		store:     opts.Store,
		metadata:  metadata,
		temporary: temporary,
	}
}

// BeginSnapshot resolves the default subscriber source for a channel.
func (r *subscriberResolver) BeginSnapshot(ctx context.Context, id channel.ChannelID) (SnapshotToken, error) {
	return r.BeginSnapshotWithRequest(ctx, id, SubscriberSnapshotRequest{})
}

// BeginSnapshotWithRequest resolves the subscriber source for a channel and request-scoped overlays.
func (r *subscriberResolver) BeginSnapshotWithRequest(ctx context.Context, id channel.ChannelID, req SubscriberSnapshotRequest) (SnapshotToken, error) {
	sourceID, derived := channelid.FromCommandChannel(id.ID)
	resolvedID := id
	if derived {
		resolvedID.ID = sourceID
	}

	token := SnapshotToken{
		id: id,
		state: &subscriberSourceState{
			seen: make(map[string]struct{}),
		},
	}
	token.source.ChannelID = id.ID
	token.source.ChannelType = id.Type
	token.source.ReusableTagState = true

	resolveStoreBackedVersions := func() uint64 {
		currentVersion := r.channelMutationVersion(ctx, id)
		sourceVersion := currentVersion
		if derived && id.Type != frame.ChannelTypeVisitors {
			sourceVersion = r.commandSourceMutationVersion(ctx, resolvedID)
		}
		token.source.SubscriberMutationVersion = currentVersion
		return sourceVersion
	}

	switch id.Type {
	case frame.ChannelTypePerson:
		left, right, err := DecodePersonChannel(resolvedID.ID)
		if err != nil {
			return SnapshotToken{}, err
		}
		token.source.Kind = SubscriberSourceKindDerived
		token.source.SourceChannelID = resolvedID.ID
		token.source.SourceChannelType = resolvedID.Type
		token.state.staticSnapshot = uniqueStrings([]string{left, right})
		return token, nil
	case frame.ChannelTypeAgent:
		left, right, err := decodeAgentChannel(resolvedID.ID)
		if err != nil {
			return SnapshotToken{}, err
		}
		token.source.Kind = SubscriberSourceKindDerived
		token.source.SourceChannelID = resolvedID.ID
		token.source.SourceChannelType = resolvedID.Type
		token.state.staticSnapshot = uniqueStrings([]string{left, right})
		return token, nil
	case frame.ChannelTypeTemp:
		token.source.Kind = SubscriberSourceKindMessageScoped
		token.source.SourceChannelID = resolvedID.ID
		token.source.SourceChannelType = resolvedID.Type
		token.source.ReusableTagState = false
		token.state.staticSnapshot = uniqueStrings(req.MessageScopedUIDs)
		return token, nil
	case frame.ChannelTypeVisitors:
		currentVersion := r.channelMutationVersion(ctx, id)
		token.source.SubscriberMutationVersion = currentVersion
		token.source.Kind = SubscriberSourceKindOverlayStore
		token.source.SourceChannelID = resolvedID.ID
		token.source.SourceChannelType = frame.ChannelTypeCustomerService
		visitorSourceID := channel.ChannelID{
			ID:   resolvedID.ID,
			Type: frame.ChannelTypeCustomerService,
		}
		token.source.SourceSubscriberMutationVersion = r.channelMutationVersion(ctx, visitorSourceID)
		if derived {
			token.source.SourceSubscriberMutationVersion = r.commandSourceMutationVersion(ctx, visitorSourceID)
		}
		token.state.storeChannelID = resolvedID.ID
		token.state.storeChannelType = int64(frame.ChannelTypeCustomerService)
		token.state.overlayUIDs = uniqueStrings([]string{resolvedID.ID})
		return token, nil
	case frame.ChannelTypeCustomerService:
		sourceVersion := resolveStoreBackedVersions()
		token.source.Kind = SubscriberSourceKindOverlayStore
		token.source.SourceChannelID = resolvedID.ID
		token.source.SourceChannelType = resolvedID.Type
		token.source.SourceSubscriberMutationVersion = sourceVersion
		token.state.storeChannelID = resolvedID.ID
		token.state.storeChannelType = int64(resolvedID.Type)
		if visitorUID := customerServiceVisitorUID(resolvedID.ID); visitorUID != "" {
			token.state.overlayUIDs = uniqueStrings([]string{visitorUID})
		}
		return token, nil
	case frame.ChannelTypeInfo:
		sourceVersion := resolveStoreBackedVersions()
		token.source.SourceChannelID = resolvedID.ID
		token.source.SourceChannelType = resolvedID.Type
		token.source.SourceSubscriberMutationVersion = sourceVersion
		token.state.storeChannelID = resolvedID.ID
		token.state.storeChannelType = int64(resolvedID.Type)
		if r.temporary != nil {
			uids, err := r.temporary.SnapshotTemporarySubscribers(ctx, resolvedID.ID, int64(resolvedID.Type))
			if err != nil {
				return SnapshotToken{}, err
			}
			token.state.overlayUIDs = uniqueStrings(uids)
		}
		if len(token.state.overlayUIDs) > 0 {
			token.source.Kind = SubscriberSourceKindOverlayStore
			token.source.ReusableTagState = false
		} else {
			token.source.Kind = SubscriberSourceKindPagedStore
		}
		return token, nil
	case frame.ChannelTypeGroup, frame.ChannelTypeCommunity, frame.ChannelTypeCommunityTopic, frame.ChannelTypeData, frame.ChannelTypeLive, frame.ChannelTypeAgentGroup:
		sourceVersion := resolveStoreBackedVersions()
		token.source.SourceChannelID = resolvedID.ID
		token.source.SourceChannelType = resolvedID.Type
		token.source.SourceSubscriberMutationVersion = sourceVersion
		token.state.storeChannelID = resolvedID.ID
		token.state.storeChannelType = int64(resolvedID.Type)
		if derived {
			token.source.Kind = SubscriberSourceKindDerived
		} else {
			token.source.Kind = SubscriberSourceKindPagedStore
		}
		return token, nil
	default:
		sourceVersion := resolveStoreBackedVersions()
		token.source.SourceChannelID = resolvedID.ID
		token.source.SourceChannelType = resolvedID.Type
		token.source.SourceSubscriberMutationVersion = sourceVersion
		token.state.storeChannelID = resolvedID.ID
		token.state.storeChannelType = int64(resolvedID.Type)
		if derived {
			token.source.Kind = SubscriberSourceKindDerived
		} else {
			token.source.Kind = SubscriberSourceKindPagedStore
		}
		return token, nil
	}
}

// NextPage returns the next subscriber page for the resolved source.
func (r *subscriberResolver) NextPage(ctx context.Context, token SnapshotToken, cursor string, limit int) ([]string, string, bool, error) {
	if token.state == nil {
		return nil, cursor, true, nil
	}
	if token.state.storeChannelID == "" {
		return nextFilteredSnapshotPage(token.state.staticSnapshot, cursor, limit, token.state.seen)
	}
	return r.nextStoreBackedPage(ctx, &token, cursor, limit)
}

func (r *subscriberResolver) nextStoreBackedPage(ctx context.Context, token *SnapshotToken, cursor string, limit int) ([]string, string, bool, error) {
	if limit <= 0 {
		return nil, "", false, errInvalidSubscriberPageLimit
	}
	state := token.state
	if state.seen == nil {
		state.seen = make(map[string]struct{})
	}

	out := make([]string, 0, limit)
	nextCursor := cursor

	if len(state.overlayUIDs) > 0 && state.overlayIndex < len(state.overlayUIDs) {
		for state.overlayIndex < len(state.overlayUIDs) && len(out) < limit {
			uid := state.overlayUIDs[state.overlayIndex]
			state.overlayIndex++
			if _, ok := state.seen[uid]; ok {
				continue
			}
			state.seen[uid] = struct{}{}
			out = append(out, uid)
			nextCursor = uid
		}
		if len(out) == limit {
			return out, nextCursor, false, nil
		}
	}

	for len(out) < limit {
		paged, pageCursor, done, err := r.store.ListChannelSubscribers(ctx, state.storeChannelID, state.storeChannelType, state.storeCursor, limit-len(out))
		if err != nil {
			return nil, "", false, err
		}
		state.storeCursor = pageCursor
		if len(paged) == 0 && done && !state.snapshotFallbackTried && state.storeCursor == "" {
			state.snapshotFallbackTried = true
			snapshot, err := r.store.SnapshotChannelSubscribers(ctx, state.storeChannelID, state.storeChannelType)
			if err != nil {
				return nil, "", false, err
			}
			state.staticSnapshot = uniqueStrings(snapshot)
			state.storeChannelID = ""
			state.storeChannelType = 0
			snapshotPage, snapshotCursor, snapshotDone, err := nextFilteredSnapshotPage(state.staticSnapshot, cursor, limit-len(out), state.seen)
			if err != nil {
				return nil, "", false, err
			}
			if len(snapshotPage) > 0 {
				out = append(out, snapshotPage...)
				nextCursor = snapshotCursor
				return out, nextCursor, snapshotDone, nil
			}
			if len(out) > 0 {
				return out, nextCursor, true, nil
			}
			return snapshotPage, snapshotCursor, snapshotDone, nil
		}
		state.snapshotFallbackTried = true
		for _, uid := range paged {
			if _, ok := state.seen[uid]; ok {
				continue
			}
			state.seen[uid] = struct{}{}
			out = append(out, uid)
			nextCursor = uid
			if len(out) == limit {
				return out, nextCursor, false, nil
			}
		}
		if done {
			return out, nextCursor, true, nil
		}
		if len(paged) == 0 {
			continue
		}
	}
	return out, nextCursor, false, nil
}

func nextFilteredSnapshotPage(uids []string, cursor string, limit int, seen map[string]struct{}) ([]string, string, bool, error) {
	if limit <= 0 {
		return nil, "", false, errInvalidSubscriberPageLimit
	}
	start := 0
	if cursor != "" {
		for i, uid := range uids {
			if uid == cursor {
				start = i + 1
				break
			}
		}
	}
	if len(seen) == 0 {
		page, nextCursor, done := nextUnfilteredSnapshotPage(uids, cursor, start, limit)
		return page, nextCursor, done, nil
	}

	page := make([]string, 0, limit)
	nextCursor := cursor
	for i := start; i < len(uids); i++ {
		uid := uids[i]
		if seen != nil {
			if _, ok := seen[uid]; ok {
				continue
			}
		}
		page = append(page, uid)
		nextCursor = uid
		if len(page) == limit {
			return page, nextCursor, !hasMoreFilteredSnapshotUIDs(uids, i+1, seen), nil
		}
	}
	if len(page) == 0 && start >= len(uids) {
		return nil, cursor, true, nil
	}
	return page, nextCursor, true, nil
}

func nextUnfilteredSnapshotPage(uids []string, cursor string, start int, limit int) ([]string, string, bool) {
	if start >= len(uids) {
		return nil, cursor, true
	}
	end := start + limit
	if end > len(uids) {
		end = len(uids)
	}
	page := uids[start:end]
	nextCursor := page[len(page)-1]
	return page, nextCursor, end >= len(uids)
}

func hasMoreFilteredSnapshotUIDs(uids []string, start int, seen map[string]struct{}) bool {
	for i := start; i < len(uids); i++ {
		if seen == nil {
			return true
		}
		if _, ok := seen[uids[i]]; !ok {
			return true
		}
	}
	return false
}

func decodeAgentChannel(channelID string) (string, string, error) {
	parts := strings.SplitN(channelID, "@", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", errors.New("delivery: invalid agent channel")
	}
	return parts[0], parts[1], nil
}

func customerServiceVisitorUID(channelID string) string {
	if idx := strings.IndexByte(channelID, '|'); idx >= 0 {
		return channelID[:idx]
	}
	return ""
}

func uniqueStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func (r *subscriberResolver) channelMutationVersion(ctx context.Context, id channel.ChannelID) uint64 {
	if r == nil || r.metadata == nil {
		return 0
	}
	ch, err := r.metadata.GetChannel(ctx, id.ID, int64(id.Type))
	if err != nil {
		return 0
	}
	return ch.SubscriberMutationVersion
}

func (r *subscriberResolver) commandSourceMutationVersion(ctx context.Context, id channel.ChannelID) uint64 {
	if r == nil || r.metadata == nil {
		return 0
	}
	if authoritative, ok := r.metadata.(ChannelSubscriberPermissionMetadataStore); ok {
		ch, err := authoritative.GetChannelForPermission(ctx, id.ID, int64(id.Type))
		if err == nil {
			return ch.SubscriberMutationVersion
		}
	}
	return r.channelMutationVersion(ctx, id)
}
