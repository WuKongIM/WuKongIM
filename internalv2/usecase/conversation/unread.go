package conversation

import (
	"context"
	"errors"
	"fmt"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// ClearUnread marks a conversation as read through the latest known message.
func (a *App) ClearUnread(ctx context.Context, cmd ClearUnreadCommand) error {
	if a == nil {
		return ErrStoreRequired
	}
	if err := validateUnreadTarget(cmd.UID, cmd.ChannelID, cmd.ChannelType); err != nil {
		return err
	}
	key := ConversationKey{ChannelID: cmd.ChannelID, ChannelType: int64(cmd.ChannelType)}
	latestSeq, ok, err := a.latestConversationSeq(ctx, key, cmd.MessageSeq)
	if err != nil {
		return err
	}
	if !ok || latestSeq == 0 {
		return nil
	}
	return a.advanceReadSeq(ctx, cmd.UID, key, latestSeq)
}

// SetUnread marks enough messages as read so at most cmd.Unread messages remain unread.
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
	key := ConversationKey{ChannelID: cmd.ChannelID, ChannelType: int64(cmd.ChannelType)}
	latestSeq, ok, err := a.latestConversationSeq(ctx, key, 0)
	if err != nil {
		return err
	}
	if !ok || latestSeq == 0 {
		return nil
	}
	return a.advanceReadSeq(ctx, cmd.UID, key, readSeqForUnread(latestSeq, cmd.Unread))
}

// DeleteConversation durably hides a conversation through the latest known message.
func (a *App) DeleteConversation(ctx context.Context, cmd DeleteConversationCommand) error {
	if a == nil {
		return ErrStoreRequired
	}
	if err := validateUnreadTarget(cmd.UID, cmd.ChannelID, cmd.ChannelType); err != nil {
		return err
	}
	if a.deleteStore == nil {
		return ErrStoreRequired
	}
	key := ConversationKey{ChannelID: cmd.ChannelID, ChannelType: int64(cmd.ChannelType)}
	deleteSeq := cmd.MessageSeq
	if deleteSeq == 0 {
		latestSeq, ok, err := a.latestConversationSeq(ctx, key, 0)
		if err != nil {
			return err
		}
		if !ok || latestSeq == 0 {
			return errors.New("conversation latest message not found")
		}
		deleteSeq = latestSeq
	}
	req := metadb.UserConversationDelete{
		UID:          cmd.UID,
		ChannelID:    key.ChannelID,
		ChannelType:  key.ChannelType,
		DeletedToSeq: deleteSeq,
		UpdatedAt:    a.now().UnixNano(),
	}
	if err := a.deleteStore.HideUserConversations(ctx, []metadb.UserConversationDelete{req}); err != nil {
		return fmt.Errorf("conversation: hide conversation: %w", err)
	}
	return nil
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

func (a *App) latestConversationSeq(ctx context.Context, key ConversationKey, fallback uint64) (uint64, bool, error) {
	if a.messages == nil {
		return 0, false, ErrStoreRequired
	}
	latestByKey, err := a.messages.GetLastVisibleMessages(ctx, []LastVisibleMessageRequest{{
		ChannelID:   key.ChannelID,
		ChannelType: key.ChannelType,
	}})
	if err != nil {
		return 0, false, err
	}
	if latest, ok := latestByKey[metadb.ConversationKey{ChannelID: key.ChannelID, ChannelType: key.ChannelType}]; ok && latest.MessageSeq > 0 {
		return latest.MessageSeq, true, nil
	}
	if fallback > 0 {
		return fallback, true, nil
	}
	return 0, false, nil
}

func (a *App) advanceReadSeq(ctx context.Context, uid string, key ConversationKey, target uint64) error {
	if a.stateStore == nil || a.stateWriter == nil {
		return ErrStoreRequired
	}
	state, ok, err := a.stateStore.GetUserConversationState(ctx, uid, key.ChannelID, key.ChannelType)
	if err != nil {
		return err
	}
	if !ok {
		state = metadb.UserConversationState{
			UID:         uid,
			ChannelID:   key.ChannelID,
			ChannelType: key.ChannelType,
		}
	}
	if state.ReadSeq >= target {
		return nil
	}
	state.ReadSeq = target
	state.UpdatedAt = a.now().UnixNano()
	if err := a.stateWriter.UpsertUserConversationStates(ctx, []metadb.UserConversationState{state}); err != nil {
		return fmt.Errorf("conversation: upsert unread state: %w", err)
	}
	return nil
}

func readSeqForUnread(latestSeq uint64, unread int) uint64 {
	if latestSeq == 0 {
		return 0
	}
	if unread <= 0 {
		return latestSeq
	}
	if uint64(unread) > latestSeq {
		return latestSeq - 1
	}
	return latestSeq - uint64(unread)
}
