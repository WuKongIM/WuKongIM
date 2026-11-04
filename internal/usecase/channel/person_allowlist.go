package channel

import (
	"context"
	"strings"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
)

// validatePersonalAllowlist rejects identities that cannot form a canonical
// pair before granting permission or submitting any directory work.
func (a *App) validatePersonalAllowlist(ctx context.Context, cmd MemberCommand) error {
	if cmd.ChannelType != 1 || len(cmd.UIDs) == 0 {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if a.personDirectory == nil || a.membershipActivation == nil {
		return ErrStoreRequired
	}
	if strings.TrimSpace(cmd.ChannelID) == "" || strings.Contains(cmd.ChannelID, "@") {
		return metadb.ErrInvalidArgument
	}
	for _, uid := range cmd.UIDs {
		if strings.TrimSpace(uid) == "" || strings.Contains(uid, "@") {
			return metadb.ErrInvalidArgument
		}
	}
	return nil
}

// preparePersonalAllowlist reuses durable person-directory preparation rather
// than ordinary subscriber upserts, preserving existing read and visibility
// floors. Each distinct peer is processed synchronously; failures leave a
// durable prefix and retry must repeat preparation and owner activation.
func (a *App) preparePersonalAllowlist(ctx context.Context, cmd MemberCommand) error {
	if cmd.ChannelType != 1 || len(cmd.UIDs) == 0 {
		return nil
	}
	activatedAt := a.now().UnixNano()
	seen := make(map[string]struct{}, len(cmd.UIDs))
	for _, uid := range cmd.UIDs {
		if _, exists := seen[uid]; exists {
			continue
		}
		seen[uid] = struct{}{}
		if err := ctx.Err(); err != nil {
			return err
		}
		pair := channelid.EncodePersonChannel(cmd.ChannelID, uid)
		if err := a.personDirectory.AdmitPersonChannelDirectory(ctx, pair, 1); err != nil {
			return err
		}
		if err := a.membershipActivation.ActivateUserChannelMembership(ctx, cmd.ChannelID, pair, 1, activatedAt, activatedAt); err != nil {
			return err
		}
	}
	return nil
}
