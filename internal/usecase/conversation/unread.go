package conversation

import (
	"context"
	"errors"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// ClearUnread marks a conversation as read through the latest known message.
// An absent membership or deleted Channel is already read and needs no write.
func (a *App) ClearUnread(ctx context.Context, cmd ClearUnreadCommand) error {
	if a == nil {
		return ErrStoreRequired
	}
	if err := validateUnreadTarget(cmd.UID, cmd.ChannelID, cmd.ChannelType); err != nil {
		return err
	}
	if a.memberships == nil || a.hydrator == nil {
		return ErrStoreRequired
	}
	row, head, found, err := a.membershipMutationHead(ctx, cmd.UID, cmd.ChannelID, cmd.ChannelType)
	if err != nil || !found {
		return err
	}
	if head.LastCommittedSeq <= row.ReadSeq {
		return nil
	}
	return a.memberships.AdvanceUserChannelMembershipReadSeq(ctx, cmd.UID, cmd.ChannelID, int64(cmd.ChannelType), head.LastCommittedSeq, a.now().UnixNano())
}

// SetUnread marks enough messages as read so at most cmd.Unread messages remain unread.
// An absent conversation has zero unread messages; this command never creates one.
func (a *App) SetUnread(ctx context.Context, cmd SetUnreadCommand) error {
	if a == nil {
		return ErrStoreRequired
	}
	if err := validateUnreadTarget(cmd.UID, cmd.ChannelID, cmd.ChannelType); err != nil {
		return err
	}
	if cmd.Unread < 0 {
		return errors.New("unread cannot be negative")
	}
	if a.memberships == nil || a.hydrator == nil {
		return ErrStoreRequired
	}
	row, head, found, err := a.membershipMutationHead(ctx, cmd.UID, cmd.ChannelID, cmd.ChannelType, uint64(cmd.Unread))
	if err != nil || !found {
		return err
	}
	visibilityFloor := maxMembershipFloor(joinVisibilityFloor(row.JoinSeq), row.DeletedToSeq, head.RetentionThroughSeq)
	target := visibilityFloor
	if uint64(cmd.Unread) < head.LastCommittedSeq {
		target = maxMembershipFloor(target, head.LastCommittedSeq-uint64(cmd.Unread))
	}
	if head.BoundaryComputed {
		target = maxMembershipFloor(visibilityFloor, head.UnreadBoundary)
	}
	if target <= row.ReadSeq {
		return nil
	}
	return a.memberships.AdvanceUserChannelMembershipReadSeq(ctx, cmd.UID, cmd.ChannelID, int64(cmd.ChannelType), target, a.now().UnixNano())
}

// DeleteConversation durably hides a conversation through the latest known message.
func (a *App) DeleteConversation(ctx context.Context, cmd DeleteConversationCommand) error {
	if a == nil {
		return ErrStoreRequired
	}
	if err := validateUnreadTarget(cmd.UID, cmd.ChannelID, cmd.ChannelType); err != nil {
		return err
	}
	if a.memberships == nil || a.hydrator == nil {
		return ErrStoreRequired
	}
	_, head, found, err := a.membershipMutationHead(ctx, cmd.UID, cmd.ChannelID, cmd.ChannelType)
	if err != nil {
		return err
	}
	if !found {
		return metadb.ErrNotFound
	}
	return a.memberships.HideUserChannelMembership(ctx, cmd.UID, cmd.ChannelID, int64(cmd.ChannelType), head.LastCommittedSeq, a.now().UnixNano())
}

// ActivateConversation records an explicit user navigation action. Message
// send, receive, delivery, and pull paths do not call this method.
func (a *App) ActivateConversation(ctx context.Context, cmd ActivateConversationCommand) error {
	if a == nil || a.memberships == nil {
		return ErrStoreRequired
	}
	if err := validateUnreadTarget(cmd.UID, cmd.ChannelID, cmd.ChannelType); err != nil {
		return err
	}
	now := a.now().UnixNano()
	return a.memberships.ActivateUserChannelMembership(ctx, cmd.UID, cmd.ChannelID, int64(cmd.ChannelType), now, now)
}

// membershipMutationHead distinguishes authoritative absence from failed reads.
// Callers decide whether a missing conversation is an idempotent success.
func (a *App) membershipMutationHead(ctx context.Context, uid, channelID string, channelType uint8, keepUnread ...uint64) (metadb.UserChannelMembership, HydrationResult, bool, error) {
	row, ok, err := a.memberships.GetUserChannelMembership(ctx, uid, channelID, int64(channelType))
	if err != nil {
		return metadb.UserChannelMembership{}, HydrationResult{}, false, err
	}
	if !ok || row.Tombstone {
		return metadb.UserChannelMembership{}, HydrationResult{}, false, nil
	}
	heads, err := a.hydrator.HydrateConversationHeads(ctx, uid, []metadb.UserChannelMembership{row}, keepUnread...)
	if err != nil {
		return metadb.UserChannelMembership{}, HydrationResult{}, false, err
	}
	if len(heads) != 1 {
		return metadb.UserChannelMembership{}, HydrationResult{}, false, errors.New("conversation: misaligned mutation hydration")
	}
	switch heads[0].Outcome {
	case HydrationOK, HydrationNoVisibleMessage:
		return row, heads[0], true, nil
	case HydrationDelete:
		return metadb.UserChannelMembership{}, HydrationResult{}, false, nil
	case HydrationRetryable:
		return metadb.UserChannelMembership{}, HydrationResult{}, false, ErrRouteNotReady
	default:
		return metadb.UserChannelMembership{}, HydrationResult{}, false, errors.New("conversation: invalid mutation hydration outcome")
	}
}

func validateUnreadTarget(uid, channelID string, channelType uint8) error {
	if uid == "" {
		return errors.New("uid cannot be empty")
	}
	if channelID == "" || channelType == 0 {
		return errors.New("channel_id or channel_type cannot be empty")
	}
	return nil
}
