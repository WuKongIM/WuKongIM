package channel

import (
	"context"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"strings"
)

type sendBanStore interface {
	SetChannelSendBan(context.Context, metadb.SendBanMutation) (metadb.SendBanResult, error)
	GetChannelSendBan(context.Context, string, int64) (metadb.SendBanResult, error)
}

func (a *App) validateSendBanKey(k ChannelKey) error {
	if k.ChannelID == "" || strings.TrimSpace(k.ChannelID) != k.ChannelID || len(k.ChannelID) > 1024 || k.ChannelType == 0 || a.commandChannels.IsCommandChannel(k.ChannelID) {
		return metadb.ErrInvalidArgument
	}
	if k.ChannelType == 1 {
		left, right, err := channelid.DecodePersonChannel(k.ChannelID)
		if err != nil || channelid.EncodePersonChannel(left, right) != k.ChannelID {
			return metadb.ErrInvalidArgument
		}
	}
	return nil
}

// SetSendBan changes only the actual source Channel's sending restriction.
func (a *App) SetSendBan(ctx context.Context, k ChannelKey, value int64, expected *uint64) (metadb.SendBanResult, error) {
	if err := a.validateSendBanKey(k); err != nil {
		return metadb.SendBanResult{}, err
	}
	q := metadb.SendBanMutation{ChannelID: k.ChannelID, ChannelType: int64(k.ChannelType), SendBan: value, ExpectedVersion: expected}
	if err := metadb.ValidateSendBanMutation(q); err != nil {
		return metadb.SendBanResult{}, err
	}
	s, ok := a.store.(sendBanStore)
	if !ok {
		return metadb.SendBanResult{}, ErrStoreRequired
	}
	return s.SetChannelSendBan(ctx, q)
}

// GetSendBan queries a source Channel; missing person Channels are unrestricted.
func (a *App) GetSendBan(ctx context.Context, k ChannelKey) (metadb.SendBanResult, error) {
	if err := a.validateSendBanKey(k); err != nil {
		return metadb.SendBanResult{}, err
	}
	s, ok := a.store.(sendBanStore)
	if !ok {
		return metadb.SendBanResult{}, ErrStoreRequired
	}
	return s.GetChannelSendBan(ctx, k.ChannelID, int64(k.ChannelType))
}

type channelInfoStore interface {
	UpdateChannelInfo(context.Context, metadb.ChannelInfoMutation) (metadb.SendBanResult, error)
}

func channelInfoResult(r metadb.SendBanResult, err error) error {
	if err != nil {
		return err
	}
	switch r.Status {
	case "ok":
		return nil
	case "not_found":
		return metadb.ErrNotFound
	case "channel_disbanded", "version_conflict", "version_exhausted":
		return metadb.ErrStaleMeta
	default:
		return ErrStoreRequired
	}
}
