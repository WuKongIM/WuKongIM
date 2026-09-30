package user

import (
	"context"
	"github.com/WuKongIM/WuKongIM/internal/contracts/sendbanaudit"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"strings"
)

type sendBanStore interface {
	SetUserSendBan(context.Context, metadb.SendBanMutation) (metadb.SendBanResult, error)
	GetUserSendBan(context.Context, string) (metadb.SendBanResult, error)
}

// SetSendBan changes only the user's global application-send restriction.
func (a *App) SetSendBan(ctx context.Context, uid string, value int64, expected *uint64) (result metadb.SendBanResult, err error) {
	q := metadb.SendBanMutation{UID: uid, SendBan: value, ExpectedVersion: expected}
	defer func() { sendbanaudit.RecordMutation(ctx, a.sendBanAudit, q, result, err) }()
	if strings.TrimSpace(uid) != uid || len(uid) > 512 {
		return metadb.SendBanResult{}, metadb.ErrInvalidArgument
	}
	if err := metadb.ValidateSendBanMutation(q); err != nil {
		return metadb.SendBanResult{}, err
	}
	s, ok := a.users.(sendBanStore)
	if !ok {
		return metadb.SendBanResult{}, ErrUserStoreRequired
	}
	return s.SetUserSendBan(ctx, q)
}

// GetSendBan queries current authoritative state without credentials.
func (a *App) GetSendBan(ctx context.Context, uid string) (metadb.SendBanResult, error) {
	if uid == "" || strings.TrimSpace(uid) != uid || len(uid) > 512 {
		return metadb.SendBanResult{}, metadb.ErrInvalidArgument
	}
	s, ok := a.users.(sendBanStore)
	if !ok {
		return metadb.SendBanResult{}, ErrUserStoreRequired
	}
	return s.GetUserSendBan(ctx, uid)
}
