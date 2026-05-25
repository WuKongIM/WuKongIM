package conversation

import (
	"context"
	"errors"
	"fmt"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// DeleteConversation durably hides a conversation and then installs a best-effort
// active hint barrier so delayed old hints do not revive it.
func (a *App) DeleteConversation(ctx context.Context, cmd DeleteConversationCommand) error {
	if a == nil {
		return nil
	}
	if err := validateUnreadTarget(cmd.UID, cmd.ChannelID, cmd.ChannelType); err != nil {
		return err
	}
	if a.deletes == nil {
		return errors.New("conversation delete store not configured")
	}

	key := conversationKey(cmd.ChannelID, cmd.ChannelType)
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
		ChannelType:  int64(key.ChannelType),
		DeletedToSeq: deleteSeq,
		UpdatedAt:    a.now().UnixNano(),
	}
	if err := a.deletes.HideUserConversations(ctx, []metadb.UserConversationDelete{req}); err != nil {
		return fmt.Errorf("conversation: hide conversation: %w", err)
	}

	barriers := []metadb.UserConversationDeleteBarrier{{
		UID:          cmd.UID,
		ChannelID:    key.ChannelID,
		ChannelType:  int64(key.ChannelType),
		DeletedToSeq: deleteSeq,
	}}
	a.async(func() {
		removeCtx, cancel := context.WithTimeout(context.Background(), defaultDeleteHintRemovalTimeout)
		defer cancel()
		_ = a.deletes.RemoveUserConversationActiveHints(removeCtx, barriers)
	})
	return nil
}
