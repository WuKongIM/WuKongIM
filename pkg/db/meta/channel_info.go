package meta

import (
	"context"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
)

// ChannelInfoMutation updates business flags at apply without copying runtime
// or membership state from an earlier read. Nil SendBan preserves its policy.
type ChannelInfoMutation struct {
	ChannelID     string `json:"channel_id"`
	ChannelType   int64  `json:"channel_type"`
	Ban           int64  `json:"ban"`
	Disband       int64  `json:"disband"`
	SendBan       *int64 `json:"send_ban,omitempty"`
	AllowStranger int64  `json:"allow_stranger"`
	Large         int64  `json:"large"`
	ExistingOnly  bool   `json:"existing_only,omitempty"`
	FlagsOnly     bool   `json:"flags_only,omitempty"`
}

// ValidateChannelInfoMutation bounds identity and all business booleans.
func ValidateChannelInfoMutation(q ChannelInfoMutation) error {
	if err := validateKeyString(q.ChannelID); err != nil {
		return err
	}
	if q.ChannelType < 1 || q.ChannelType > 255 {
		return ErrInvalidArgument
	}
	for _, v := range []int64{q.Ban, q.Disband, q.AllowStranger, q.Large} {
		if v != 0 && v != 1 {
			return ErrInvalidArgument
		}
	}
	if q.SendBan != nil && *q.SendBan != 0 && *q.SendBan != 1 {
		return ErrInvalidArgument
	}
	return nil
}

// ApplyChannelInfo preserves apply-time policy and directory/member fields.
// Conditional failures are item-local even when neighboring commands commit.
func (b *WriteBatch) ApplyChannelInfo(hashSlot uint16, q ChannelInfoMutation) (*SendBanResult, error) {
	if err := b.ensure(); err != nil {
		return nil, err
	}
	if err := ValidateChannelInfoMutation(q); err != nil {
		return nil, err
	}
	if q.SendBan != nil {
		v := *q.SendBan
		q.SendBan = &v
	}
	out := &SendBanResult{}
	hs := HashSlot(hashSlot)
	key := encodeChannelRowKey(hs, q.ChannelID, q.ChannelType, channelPrimaryFamilyID)
	b.batch.addOp(hs, func(ctx context.Context, state *batchCommitState, batch *engine.Batch) error {
		ch, exists, err := state.loadChannel(ctx, key, q.ChannelID, q.ChannelType)
		if err != nil {
			return err
		}
		*out = SendBanResult{Status: "ok", SendBan: ch.SendBan, Version: ch.SendBanVersion}
		if q.SendBan != nil {
			out.Previous = &SendBanPolicy{SendBan: ch.SendBan, Version: ch.SendBanVersion}
		}
		if !exists && q.ExistingOnly {
			out.Status = "not_found"
			return nil
		}
		if !exists {
			ch = Channel{ChannelID: q.ChannelID, ChannelType: q.ChannelType}
		}
		if q.SendBan != nil {
			if ch.Disband != 0 {
				out.Status = "channel_disbanded"
				return nil
			}
			*out = nextSendBan(*out, *q.SendBan, nil)
			if out.Status != "ok" {
				return nil
			}
			ch.SendBan, ch.SendBanVersion = out.SendBan, out.Version
		}
		ch.Ban = q.Ban
		if q.Disband != 0 {
			ch.Disband = 1
		}
		if !q.FlagsOnly {
			ch.AllowStranger = q.AllowStranger
			ch.Large = q.Large
		}
		shard := &Shard{db: state.db, hashSlot: hs}
		if err := shard.stageChannel(batch, key, ch); err != nil {
			return err
		}
		state.channelPublishes[string(key)] = ch
		delete(state.channelDeletes, string(key))
		return nil
	})
	return out, nil
}
