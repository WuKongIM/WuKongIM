package channel

import (
	"context"
	"errors"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// ErrStoreRequired indicates that the channel usecase has no storage backend.
var ErrStoreRequired = errors.New("usecase/channel: store required")

const defaultSubscriberPageLimit = 1000

// Store persists channel metadata and member-like channel lists through the
// cluster-authoritative slot store.
type Store interface {
	GetChannel(ctx context.Context, channelID string, channelType int64) (metadb.Channel, error)
	UpsertChannel(ctx context.Context, ch metadb.Channel) error
	DeleteChannel(ctx context.Context, channelID string, channelType int64) error
	AddChannelSubscribers(ctx context.Context, channelID string, channelType int64, uids []string, subscriberMutationVersion ...uint64) error
	RemoveChannelSubscribers(ctx context.Context, channelID string, channelType int64, uids []string, subscriberMutationVersion ...uint64) error
	ListChannelSubscribers(ctx context.Context, channelID string, channelType int64, afterUID string, limit int) ([]string, string, bool, error)
}

// Options contains dependencies for the channel usecase.
type Options struct {
	Store Store
	// SubscriberPageLimit bounds internal subscriber pages and mutation chunks.
	SubscriberPageLimit int
}

// App coordinates legacy channel management actions without depending on an
// entry protocol.
type App struct {
	store               Store
	subscriberPageLimit int
}

// New creates a channel management usecase.
func New(opts Options) *App {
	limit := opts.SubscriberPageLimit
	if limit <= 0 {
		limit = defaultSubscriberPageLimit
	}
	return &App{
		store:               opts.Store,
		subscriberPageLimit: limit,
	}
}

// Upsert updates channel metadata and optionally replaces subscribers.
func (a *App) Upsert(ctx context.Context, cmd UpsertCommand) error {
	if err := a.requireStore(); err != nil {
		return err
	}
	if err := a.UpdateInfo(ctx, cmd.Info); err != nil {
		return err
	}
	mutationVersion, err := a.subscriberMutationVersionFor(ctx, cmd.Info.ChannelID, int64(cmd.Info.ChannelType))
	if err != nil {
		return err
	}
	if cmd.Reset {
		if err := a.removeAllSubscribersFor(ctx, cmd.Info.ChannelID, int64(cmd.Info.ChannelType), mutationVersion); err != nil {
			return err
		}
	}
	if len(cmd.Subscribers) == 0 {
		return nil
	}
	return a.addSubscribersChunked(ctx, cmd.Info.ChannelID, int64(cmd.Info.ChannelType), cmd.Subscribers, mutationVersion)
}

// UpdateInfo upserts the persisted channel flags supported by the slot store.
func (a *App) UpdateInfo(ctx context.Context, info Info) error {
	if err := a.requireStore(); err != nil {
		return err
	}
	return a.store.UpsertChannel(ctx, metadb.Channel{
		ChannelID:     info.ChannelID,
		ChannelType:   int64(info.ChannelType),
		Ban:           boolToInt64(info.Ban),
		Disband:       boolToInt64(info.Disband),
		SendBan:       boolToInt64(info.SendBan),
		AllowStranger: boolToInt64(info.AllowStranger),
	})
}

// Delete removes channel metadata and the ordinary subscriber list.
func (a *App) Delete(ctx context.Context, key ChannelKey) error {
	if err := a.requireStore(); err != nil {
		return err
	}
	return a.store.DeleteChannel(ctx, key.ChannelID, int64(key.ChannelType))
}

// AddSubscribers appends subscribers to a channel, replacing existing members
// when Reset is set.
func (a *App) AddSubscribers(ctx context.Context, cmd SubscriberCommand) error {
	if err := a.requireStore(); err != nil {
		return err
	}
	if err := a.ensureChannelExists(ctx, cmd.ChannelID, int64(cmd.ChannelType)); err != nil {
		return err
	}
	mutationVersion, err := a.subscriberMutationVersionFor(ctx, cmd.ChannelID, int64(cmd.ChannelType))
	if err != nil {
		return err
	}
	if cmd.Reset {
		if err := a.removeAllSubscribersFor(ctx, cmd.ChannelID, int64(cmd.ChannelType), mutationVersion); err != nil {
			return err
		}
	}
	if len(cmd.Subscribers) == 0 {
		return nil
	}
	return a.addSubscribersChunked(ctx, cmd.ChannelID, int64(cmd.ChannelType), cmd.Subscribers, mutationVersion)
}

// RemoveSubscribers removes selected channel subscribers.
func (a *App) RemoveSubscribers(ctx context.Context, cmd SubscriberCommand) error {
	if err := a.requireStore(); err != nil {
		return err
	}
	if len(cmd.Subscribers) == 0 {
		return nil
	}
	mutationVersion, err := a.subscriberMutationVersionFor(ctx, cmd.ChannelID, int64(cmd.ChannelType))
	if err != nil {
		return err
	}
	return a.removeSubscribersChunked(ctx, cmd.ChannelID, int64(cmd.ChannelType), cmd.Subscribers, mutationVersion)
}

// RemoveAllSubscribers removes every ordinary subscriber for the channel.
func (a *App) RemoveAllSubscribers(ctx context.Context, key ChannelKey) error {
	if err := a.requireStore(); err != nil {
		return err
	}
	mutationVersion, err := a.subscriberMutationVersionFor(ctx, key.ChannelID, int64(key.ChannelType))
	if err != nil {
		return err
	}
	return a.removeAllSubscribersFor(ctx, key.ChannelID, int64(key.ChannelType), mutationVersion)
}

// SetTempSubscribers replaces the internal temporary subscriber list.
func (a *App) SetTempSubscribers(ctx context.Context, cmd TempSubscriberCommand) error {
	return a.setMemberList(ctx, tempListKind, ChannelKey{ChannelID: cmd.ChannelID, ChannelType: tempChannelType}, cmd.UIDs)
}

// AddDenylist appends members to the denylist.
func (a *App) AddDenylist(ctx context.Context, cmd MemberCommand) error {
	return a.addMemberList(ctx, denyListKind, cmd.ChannelKey, cmd.UIDs)
}

// SetDenylist replaces every member in the denylist.
func (a *App) SetDenylist(ctx context.Context, cmd MemberCommand) error {
	return a.setMemberList(ctx, denyListKind, cmd.ChannelKey, cmd.UIDs)
}

// RemoveDenylist removes selected members from the denylist.
func (a *App) RemoveDenylist(ctx context.Context, cmd MemberCommand) error {
	return a.removeMemberList(ctx, denyListKind, cmd.ChannelKey, cmd.UIDs)
}

// RemoveAllDenylist removes every denylist member.
func (a *App) RemoveAllDenylist(ctx context.Context, key ChannelKey) error {
	return a.removeAllMemberList(ctx, denyListKind, key)
}

// AddAllowlist appends members to the allowlist.
func (a *App) AddAllowlist(ctx context.Context, cmd MemberCommand) error {
	return a.addMemberList(ctx, allowListKind, cmd.ChannelKey, cmd.UIDs)
}

// SetAllowlist replaces every member in the allowlist.
func (a *App) SetAllowlist(ctx context.Context, cmd MemberCommand) error {
	return a.setMemberList(ctx, allowListKind, cmd.ChannelKey, cmd.UIDs)
}

// RemoveAllowlist removes selected members from the allowlist.
func (a *App) RemoveAllowlist(ctx context.Context, cmd MemberCommand) error {
	return a.removeMemberList(ctx, allowListKind, cmd.ChannelKey, cmd.UIDs)
}

// RemoveAllAllowlist removes every allowlist member.
func (a *App) RemoveAllAllowlist(ctx context.Context, key ChannelKey) error {
	return a.removeAllMemberList(ctx, allowListKind, key)
}

// ListAllowlist returns allowlist members in the legacy member response shape.
func (a *App) ListAllowlist(ctx context.Context, key ChannelKey) (MemberListResult, error) {
	uids, err := a.listMemberList(ctx, allowListKind, key)
	if err != nil {
		return MemberListResult{}, err
	}
	members := make([]Member, 0, len(uids))
	for _, uid := range uids {
		members = append(members, Member{UID: uid})
	}
	return MemberListResult{Members: members}, nil
}

// ListSubscribersPage returns one ordinary subscriber page.
func (a *App) ListSubscribersPage(ctx context.Context, req MemberListPageRequest) (MemberListPageResult, error) {
	return a.listMemberListPage(ctx, req.ChannelID, int64(req.ChannelType), req.AfterUID, req.Limit)
}

// ListAllowlistPage returns one allowlist page.
func (a *App) ListAllowlistPage(ctx context.Context, req MemberListPageRequest) (MemberListPageResult, error) {
	return a.listMemberListPage(ctx, namespacedListChannelID(allowListKind, req.ChannelKey), int64(req.ChannelType), req.AfterUID, req.Limit)
}

// ListDenylistPage returns one denylist page.
func (a *App) ListDenylistPage(ctx context.Context, req MemberListPageRequest) (MemberListPageResult, error) {
	return a.listMemberListPage(ctx, namespacedListChannelID(denyListKind, req.ChannelKey), int64(req.ChannelType), req.AfterUID, req.Limit)
}

func (a *App) addMemberList(ctx context.Context, kind memberListKind, key ChannelKey, uids []string) error {
	if err := a.requireStore(); err != nil {
		return err
	}
	if len(uids) == 0 {
		return nil
	}
	return a.addSubscribersChunked(ctx, namespacedListChannelID(kind, key), int64(key.ChannelType), uids, 1)
}

func (a *App) setMemberList(ctx context.Context, kind memberListKind, key ChannelKey, uids []string) error {
	if err := a.removeAllMemberList(ctx, kind, key); err != nil {
		return err
	}
	return a.addMemberList(ctx, kind, key, uids)
}

func (a *App) removeMemberList(ctx context.Context, kind memberListKind, key ChannelKey, uids []string) error {
	if err := a.requireStore(); err != nil {
		return err
	}
	if len(uids) == 0 {
		return nil
	}
	return a.removeSubscribersChunked(ctx, namespacedListChannelID(kind, key), int64(key.ChannelType), uids, 1)
}

func (a *App) removeAllMemberList(ctx context.Context, kind memberListKind, key ChannelKey) error {
	return a.removeAllSubscribersFor(ctx, namespacedListChannelID(kind, key), int64(key.ChannelType), 1)
}

func (a *App) listMemberList(ctx context.Context, kind memberListKind, key ChannelKey) ([]string, error) {
	if err := a.requireStore(); err != nil {
		return nil, err
	}
	return a.listSubscribers(ctx, namespacedListChannelID(kind, key), int64(key.ChannelType))
}

func (a *App) listMemberListPage(ctx context.Context, channelID string, channelType int64, afterUID string, limit int) (MemberListPageResult, error) {
	if err := a.requireStore(); err != nil {
		return MemberListPageResult{}, err
	}
	if limit <= 0 {
		return MemberListPageResult{}, metadb.ErrInvalidArgument
	}
	uids, nextCursor, done, err := a.store.ListChannelSubscribers(ctx, channelID, channelType, afterUID, limit)
	if err != nil {
		return MemberListPageResult{}, err
	}
	members := make([]Member, 0, len(uids))
	for _, uid := range uids {
		members = append(members, Member{UID: uid})
	}
	return MemberListPageResult{
		Members:    members,
		NextCursor: nextCursor,
		HasMore:    !done,
	}, nil
}

func (a *App) removeAllSubscribersFor(ctx context.Context, channelID string, channelType int64, subscriberMutationVersion uint64) error {
	if err := a.requireStore(); err != nil {
		return err
	}
	cursor := ""
	for {
		uids, nextCursor, done, err := a.store.ListChannelSubscribers(ctx, channelID, channelType, cursor, a.subscriberPageLimit)
		if err != nil {
			return err
		}
		if len(uids) > 0 {
			if err := a.removeSubscribersChunked(ctx, channelID, channelType, uids, subscriberMutationVersion); err != nil {
				return err
			}
		}
		if done {
			return nil
		}
		if nextCursor == "" || nextCursor == cursor {
			return nil
		}
		cursor = nextCursor
	}
}

func (a *App) addSubscribersChunked(ctx context.Context, channelID string, channelType int64, uids []string, subscriberMutationVersion uint64) error {
	return a.forEachSubscriberChunk(uids, func(chunk []string) error {
		return a.store.AddChannelSubscribers(ctx, channelID, channelType, chunk, subscriberMutationVersion)
	})
}

func (a *App) removeSubscribersChunked(ctx context.Context, channelID string, channelType int64, uids []string, subscriberMutationVersion uint64) error {
	return a.forEachSubscriberChunk(uids, func(chunk []string) error {
		return a.store.RemoveChannelSubscribers(ctx, channelID, channelType, chunk, subscriberMutationVersion)
	})
}

func (a *App) forEachSubscriberChunk(uids []string, fn func([]string) error) error {
	if len(uids) == 0 {
		return nil
	}
	limit := a.subscriberPageLimit
	if limit <= 0 {
		limit = defaultSubscriberPageLimit
	}
	for start := 0; start < len(uids); start += limit {
		end := start + limit
		if end > len(uids) {
			end = len(uids)
		}
		if err := fn(uids[start:end]); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) listSubscribers(ctx context.Context, channelID string, channelType int64) ([]string, error) {
	var out []string
	cursor := ""
	for {
		uids, nextCursor, done, err := a.store.ListChannelSubscribers(ctx, channelID, channelType, cursor, a.subscriberPageLimit)
		if err != nil {
			return nil, err
		}
		out = append(out, uids...)
		if done {
			return out, nil
		}
		if nextCursor == "" || nextCursor == cursor {
			return out, nil
		}
		cursor = nextCursor
	}
}

func (a *App) requireStore() error {
	if a == nil || a.store == nil {
		return ErrStoreRequired
	}
	return nil
}

func (a *App) ensureChannelExists(ctx context.Context, channelID string, channelType int64) error {
	_, err := a.store.GetChannel(ctx, channelID, channelType)
	if err == nil {
		return nil
	}
	if !errors.Is(err, metadb.ErrNotFound) {
		return err
	}
	return a.store.UpsertChannel(ctx, metadb.Channel{ChannelID: channelID, ChannelType: channelType})
}

func (a *App) subscriberMutationVersionFor(ctx context.Context, channelID string, channelType int64) (uint64, error) {
	if err := a.requireStore(); err != nil {
		return 0, err
	}
	channel, err := a.store.GetChannel(ctx, channelID, channelType)
	if err != nil {
		if errors.Is(err, metadb.ErrNotFound) {
			return 1, nil
		}
		return 0, err
	}
	if channel.SubscriberMutationVersion == 0 {
		return 1, nil
	}
	return channel.SubscriberMutationVersion + 1, nil
}

func boolToInt64(value bool) int64 {
	if value {
		return 1
	}
	return 0
}
