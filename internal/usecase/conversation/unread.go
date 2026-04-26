package conversation

import (
	"context"
	"errors"
	"fmt"

	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
)

// ClearUnread marks a conversation as read through the latest known message.
func (a *App) ClearUnread(ctx context.Context, cmd ClearUnreadCommand) error {
	if a == nil {
		return nil
	}
	if err := validateUnreadTarget(cmd.UID, cmd.ChannelID, cmd.ChannelType); err != nil {
		return err
	}

	latestSeq, ok, err := a.latestConversationSeq(ctx, conversationKey(cmd.ChannelID, cmd.ChannelType), cmd.MessageSeq)
	if err != nil {
		return err
	}
	if !ok || latestSeq == 0 {
		return nil
	}
	return a.advanceReadSeq(ctx, cmd.UID, conversationKey(cmd.ChannelID, cmd.ChannelType), latestSeq)
}

// SetUnread marks enough messages as read so at most cmd.Unread messages remain unread.
func (a *App) SetUnread(ctx context.Context, cmd SetUnreadCommand) error {
	if a == nil {
		return nil
	}
	if err := validateUnreadTarget(cmd.UID, cmd.ChannelID, cmd.ChannelType); err != nil {
		return err
	}
	if cmd.Unread < 0 {
		return errors.New("unread cannot be negative")
	}

	latestSeq, ok, err := a.latestConversationSeq(ctx, conversationKey(cmd.ChannelID, cmd.ChannelType), 0)
	if err != nil {
		return err
	}
	if !ok || latestSeq == 0 {
		return nil
	}
	return a.advanceReadSeq(ctx, cmd.UID, conversationKey(cmd.ChannelID, cmd.ChannelType), readSeqForUnread(latestSeq, cmd.Unread))
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
	if a.facts == nil {
		return 0, false, errors.New("message facts store not configured")
	}
	latestByKey, err := a.facts.LoadLatestMessages(ctx, []ConversationKey{key})
	if err != nil {
		return 0, false, err
	}
	if latest, ok := latestByKey[key]; ok && latest.MessageSeq > 0 {
		return latest.MessageSeq, true, nil
	}
	if fallback > 0 {
		return fallback, true, nil
	}
	return 0, false, nil
}

func (a *App) advanceReadSeq(ctx context.Context, uid string, key ConversationKey, target uint64) error {
	if a.states == nil {
		return errors.New("conversation state store not configured")
	}

	state, err := a.states.GetUserConversationState(ctx, uid, key.ChannelID, int64(key.ChannelType))
	switch {
	case err == nil:
	case errors.Is(err, metadb.ErrNotFound):
		state = metadb.UserConversationState{
			UID:         uid,
			ChannelID:   key.ChannelID,
			ChannelType: int64(key.ChannelType),
		}
	default:
		return err
	}

	if state.ReadSeq >= target {
		return nil
	}
	state.ReadSeq = target
	state.UpdatedAt = a.now().UnixNano()
	if err := a.states.UpsertUserConversationStates(ctx, []metadb.UserConversationState{state}); err != nil {
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
