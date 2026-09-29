package meta

import (
	"context"

	dberrors "github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
)

// PermissionFactKind selects a raw metadata fact, never a business decision.
type PermissionFactKind uint8

const (
	PermissionFactChannel PermissionFactKind = iota + 1
	PermissionFactSubscriber
	PermissionFactHasSubscribers
	PermissionFactUser
)

// PermissionFactRead is scoped to a caller-validated logical Hash Slot.
type PermissionFactRead struct {
	Kind        PermissionFactKind
	HashSlot    uint16
	ChannelID   string
	ChannelType int64
	UID         string
}

// PermissionFact returns policy-only user state; credentials never escape.
type PermissionFact struct {
	Channel    Channel
	UserPolicy SendBanResult
	Found      bool
	Value      bool
}

// ReadPermissionSnapshot pins one database view after the distributed caller's
// fresh Slot read/apply barrier. The view is never reused across requests.
func (db *DB) ReadPermissionSnapshot(ctx context.Context, reads []PermissionFactRead) ([]PermissionFact, error) {
	if db == nil || db.engine == nil {
		return nil, dberrors.ErrClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if len(reads) > 4096 {
		return nil, ErrInvalidArgument
	}
	snap, err := db.engine.NewSnapshot()
	if err != nil {
		return nil, err
	}
	defer snap.Close()
	out := make([]PermissionFact, len(reads))
	for i, q := range reads {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		hs := HashSlot(q.HashSlot)
		switch q.Kind {
		case PermissionFactUser:
			var user User
			user, out[i].Found, err = snapshotUpdateRow(snap, userTable, hs, KeyParts{String(q.UID)})
			out[i].UserPolicy = SendBanResult{Status: "ok", SendBan: user.SendBan, Version: user.SendBanVersion}
		case PermissionFactChannel:
			out[i].Channel, out[i].Found, err = snapshotUpdateRow(snap, channelTable, hs, KeyParts{String(q.ChannelID), Int64Ordered(q.ChannelType)})
		case PermissionFactSubscriber:
			_, out[i].Value, err = snapshotUpdateRow(snap, subscriberTable, hs, subscriberPrimaryKey(q.ChannelID, q.ChannelType, q.UID))
		case PermissionFactHasSubscribers:
			span := prefixSpan(encodeSubscriberRowPrefix(hs, q.ChannelID, q.ChannelType))
			var iter *engine.Iter
			iter, err = snap.NewIter(engine.Span{Start: span.Start, End: span.End}, engine.IterOptions{})
			if err == nil {
				out[i].Value = iter.First()
				err = iter.Error()
				closeErr := iter.Close()
				if err == nil {
					err = closeErr
				}
			}
		default:
			return nil, ErrInvalidArgument
		}
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
