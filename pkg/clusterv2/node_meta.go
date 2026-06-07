package clusterv2

import (
	"context"
	"sort"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
)

// CreateUserMetadata persists durable UID metadata through Slot ownership.
func (n *Node) CreateUserMetadata(ctx context.Context, user metadb.User) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if n == nil {
		return ErrNotStarted
	}
	return n.Propose(ctx, ProposeRequest{
		Key:     user.UID,
		Command: metafsm.EncodeCreateUserCommand(user),
	})
}

// GetUserMetadata reads durable UID metadata from the current Slot route.
func (n *Node) GetUserMetadata(ctx context.Context, uid string) (metadb.User, error) {
	if err := ctxErr(ctx); err != nil {
		return metadb.User{}, err
	}
	if err := n.ensureForeground(); err != nil {
		return metadb.User{}, err
	}
	if n.defaultSlotMetaDB == nil {
		return metadb.User{}, ErrNotStarted
	}
	route, err := n.RouteKey(uid)
	if err != nil {
		return metadb.User{}, err
	}
	return n.defaultSlotMetaDB.ForHashSlot(route.HashSlot).GetUser(ctx, uid)
}

// UpsertDeviceMetadata persists durable per-device token metadata through Slot ownership.
func (n *Node) UpsertDeviceMetadata(ctx context.Context, device metadb.Device) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if n == nil {
		return ErrNotStarted
	}
	return n.Propose(ctx, ProposeRequest{
		Key:     device.UID,
		Command: metafsm.EncodeUpsertDeviceCommand(device),
	})
}

// GetDeviceMetadata reads durable per-device token metadata from the current Slot route.
func (n *Node) GetDeviceMetadata(ctx context.Context, uid string, deviceFlag int64) (metadb.Device, error) {
	if err := ctxErr(ctx); err != nil {
		return metadb.Device{}, err
	}
	if err := n.ensureForeground(); err != nil {
		return metadb.Device{}, err
	}
	if n.defaultSlotMetaDB == nil {
		return metadb.Device{}, ErrNotStarted
	}
	route, err := n.RouteKey(uid)
	if err != nil {
		return metadb.Device{}, err
	}
	return n.defaultSlotMetaDB.ForHashSlot(route.HashSlot).GetDevice(ctx, uid, deviceFlag)
}

// UpsertChannelMetadata persists durable channel metadata through Slot ownership.
func (n *Node) UpsertChannelMetadata(ctx context.Context, channel metadb.Channel) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if n == nil {
		return ErrNotStarted
	}
	return n.Propose(ctx, ProposeRequest{
		Key:     channel.ChannelID,
		Command: metafsm.EncodeUpsertChannelCommand(channel),
	})
}

// GetChannelMetadata reads durable channel metadata from the current Slot route.
func (n *Node) GetChannelMetadata(ctx context.Context, channelID string, channelType int64) (metadb.Channel, error) {
	if err := ctxErr(ctx); err != nil {
		return metadb.Channel{}, err
	}
	if err := n.ensureForeground(); err != nil {
		return metadb.Channel{}, err
	}
	if n.defaultSlotMetaDB == nil {
		return metadb.Channel{}, ErrNotStarted
	}
	route, err := n.RouteKey(channelID)
	if err != nil {
		return metadb.Channel{}, err
	}
	return n.defaultSlotMetaDB.ForHashSlot(route.HashSlot).GetChannel(ctx, channelID, channelType)
}

// DeleteChannelMetadata removes durable channel metadata through Slot ownership.
func (n *Node) DeleteChannelMetadata(ctx context.Context, channelID string, channelType int64) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if n == nil {
		return ErrNotStarted
	}
	return n.Propose(ctx, ProposeRequest{
		Key:     channelID,
		Command: metafsm.EncodeDeleteChannelCommand(channelID, channelType),
	})
}

// AddChannelSubscribers appends durable channel subscribers through Slot ownership.
func (n *Node) AddChannelSubscribers(ctx context.Context, channelID string, channelType int64, uids []string, subscriberMutationVersion uint64) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if n == nil {
		return ErrNotStarted
	}
	command, err := metafsm.EncodeAddSubscribersCommandChecked(channelID, channelType, uids, subscriberMutationVersion)
	if err != nil {
		return err
	}
	return n.Propose(ctx, ProposeRequest{Key: channelID, Command: command})
}

// RemoveChannelSubscribers removes durable channel subscribers through Slot ownership.
func (n *Node) RemoveChannelSubscribers(ctx context.Context, channelID string, channelType int64, uids []string, subscriberMutationVersion uint64) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if n == nil {
		return ErrNotStarted
	}
	command, err := metafsm.EncodeRemoveSubscribersCommandChecked(channelID, channelType, uids, subscriberMutationVersion)
	if err != nil {
		return err
	}
	return n.Propose(ctx, ProposeRequest{Key: channelID, Command: command})
}

// ListChannelSubscribersPage reads durable channel subscribers from Slot metadata storage.
func (n *Node) ListChannelSubscribersPage(ctx context.Context, channelID string, channelType int64, afterUID string, limit int) ([]string, string, bool, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, "", false, err
	}
	if err := n.ensureForeground(); err != nil {
		return nil, "", false, err
	}
	if n.defaultSlotMetaDB == nil {
		return nil, "", false, ErrNotStarted
	}
	route, err := n.RouteKey(channelID)
	if err != nil {
		return nil, "", false, err
	}
	return n.defaultSlotMetaDB.ForHashSlot(route.HashSlot).ListSubscribersPage(ctx, channelID, channelType, afterUID, limit)
}

// UpsertUserChannelMemberships persists UID-owned channel memberships through hash-slot ownership.
func (n *Node) UpsertUserChannelMemberships(ctx context.Context, channelID string, channelType int64, uids []string, joinSeq uint64, updatedAt int64) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if n == nil {
		return ErrNotStarted
	}
	groups, err := n.groupUserChannelMembershipsByHashSlot(channelID, channelType, uids, joinSeq, updatedAt)
	if err != nil {
		return err
	}
	for _, hashSlot := range sortedMembershipHashSlots(groups) {
		command, err := metafsm.EncodeUpsertUserChannelMembershipsCommandChecked(groups[hashSlot])
		if err != nil {
			return err
		}
		if err := n.Propose(ctx, ProposeRequest{
			Command: command,
			Target:  ProposeTarget{HashSlot: hashSlot, HasHashSlot: true},
		}); err != nil {
			return err
		}
	}
	return nil
}

// DeleteUserChannelMemberships removes UID-owned channel memberships through hash-slot ownership.
func (n *Node) DeleteUserChannelMemberships(ctx context.Context, channelID string, channelType int64, uids []string, updatedAt int64) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if n == nil {
		return ErrNotStarted
	}
	groups, err := n.groupUserChannelMembershipsByHashSlot(channelID, channelType, uids, 0, updatedAt)
	if err != nil {
		return err
	}
	for _, hashSlot := range sortedMembershipHashSlots(groups) {
		command, err := metafsm.EncodeDeleteUserChannelMembershipsCommandChecked(groups[hashSlot])
		if err != nil {
			return err
		}
		if err := n.Propose(ctx, ProposeRequest{
			Command: command,
			Target:  ProposeTarget{HashSlot: hashSlot, HasHashSlot: true},
		}); err != nil {
			return err
		}
	}
	return nil
}

// ListUserChannelMembershipPage reads UID-owned memberships from Slot metadata storage.
func (n *Node) ListUserChannelMembershipPage(ctx context.Context, uid string, after metadb.UserChannelMembershipCursor, limit int) ([]metadb.UserChannelMembership, metadb.UserChannelMembershipCursor, bool, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, metadb.UserChannelMembershipCursor{}, false, err
	}
	if err := n.ensureForeground(); err != nil {
		return nil, metadb.UserChannelMembershipCursor{}, false, err
	}
	if n.defaultSlotMetaDB == nil {
		return nil, metadb.UserChannelMembershipCursor{}, false, ErrNotStarted
	}
	route, err := n.RouteKey(uid)
	if err != nil {
		return nil, metadb.UserChannelMembershipCursor{}, false, err
	}
	return n.defaultSlotMetaDB.ForHashSlot(route.HashSlot).ListUserChannelMembershipPage(ctx, uid, after, limit)
}

func (n *Node) groupUserChannelMembershipsByHashSlot(channelID string, channelType int64, uids []string, joinSeq uint64, updatedAt int64) (map[uint16][]metadb.UserChannelMembership, error) {
	groups := make(map[uint16][]metadb.UserChannelMembership)
	for _, uid := range uids {
		route, err := n.RouteKey(uid)
		if err != nil {
			return nil, err
		}
		groups[route.HashSlot] = append(groups[route.HashSlot], metadb.UserChannelMembership{
			UID:         uid,
			ChannelID:   channelID,
			ChannelType: channelType,
			JoinSeq:     joinSeq,
			UpdatedAt:   updatedAt,
		})
	}
	return groups, nil
}

func sortedMembershipHashSlots(groups map[uint16][]metadb.UserChannelMembership) []uint16 {
	hashSlots := make([]uint16, 0, len(groups))
	for hashSlot := range groups {
		hashSlots = append(hashSlots, hashSlot)
	}
	sort.Slice(hashSlots, func(i, j int) bool { return hashSlots[i] < hashSlots[j] })
	return hashSlots
}
