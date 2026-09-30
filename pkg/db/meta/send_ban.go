package meta

import (
	"context"
	"math"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
)

// SendBanMutation changes exactly one entity's sending restriction at its Slot.
// UID and ChannelID are mutually exclusive; no credentials cross this port.
type SendBanMutation struct {
	UID             string  `json:"uid,omitempty"`
	ChannelID       string  `json:"channel_id,omitempty"`
	ChannelType     int64   `json:"channel_type,omitempty"`
	SendBan         int64   `json:"send_ban"`
	ExpectedVersion *uint64 `json:"expected_version,omitempty"`
}

// SendBanResult is populated at atomic apply, including logical CAS rejection.
type SendBanResult struct {
	Status  string `json:"status"`
	SendBan int64  `json:"send_ban"`
	Version uint64 `json:"send_ban_version"`
	// Previous is captured inside atomic mutation apply for audit only. Reads
	// omit it; transport or commit errors do not prove either state is durable.
	Previous *SendBanPolicy `json:"previous_policy,omitempty"`
}

// SendBanPolicy is a credential-free policy value and its independent version.
type SendBanPolicy struct {
	SendBan int64  `json:"send_ban"`
	Version uint64 `json:"send_ban_version"`
}

// ValidateSendBanMutation rejects ambiguous routing and nonboolean flags.
func ValidateSendBanMutation(q SendBanMutation) error {
	if q.SendBan != 0 && q.SendBan != 1 {
		return ErrInvalidArgument
	}
	if q.UID != "" {
		if q.ChannelID != "" || q.ChannelType != 0 {
			return ErrInvalidArgument
		}
		return validateKeyString(q.UID)
	}
	if q.ChannelType <= 0 || q.ChannelType > 255 {
		return ErrInvalidArgument
	}
	return validateKeyString(q.ChannelID)
}

func (state *batchCommitState) loadUser(key []byte, uid string) (User, bool, error) {
	overlay, ok := state.tableRows[string(key)]
	if !ok {
		value, exists, err := state.db.get(key)
		if err != nil {
			return User{}, false, err
		}
		overlay = tableRowOverlay{value: value, exists: exists}
	}
	if !overlay.exists {
		return User{}, false, nil
	}
	user, err := decodeUserValue(uid, overlay.value)
	return user, true, err
}

// ApplySendBan performs the conditional field update inside the shared apply
// batch. Logical failures remain item-local and never discard adjacent writes.
func (b *WriteBatch) ApplySendBan(hashSlot uint16, q SendBanMutation) (*SendBanResult, error) {
	if err := b.ensure(); err != nil {
		return nil, err
	}
	if err := ValidateSendBanMutation(q); err != nil {
		return nil, err
	}
	if q.ExpectedVersion != nil {
		v := *q.ExpectedVersion
		q.ExpectedVersion = &v
	}
	result := &SendBanResult{}
	hs := HashSlot(hashSlot)
	b.batch.addOp(hs, func(ctx context.Context, state *batchCommitState, batch *engine.Batch) error {
		var key []byte
		var user User
		var channel Channel
		var exists bool
		var err error
		*result = SendBanResult{Status: "ok"}
		if q.UID != "" {
			key = encodeUserRowKey(hs, q.UID, userPrimaryFamilyID)
			user, exists, err = state.loadUser(key, q.UID)
			if !exists {
				user.UID = q.UID
			}
			result.SendBan, result.Version = user.SendBan, user.SendBanVersion
		} else {
			key = encodeChannelRowKey(hs, q.ChannelID, q.ChannelType, channelPrimaryFamilyID)
			channel, exists, err = state.loadChannel(ctx, key, q.ChannelID, q.ChannelType)
			if !exists {
				channel = Channel{ChannelID: q.ChannelID, ChannelType: q.ChannelType}
			}
			result.SendBan, result.Version = channel.SendBan, channel.SendBanVersion
		}
		if err != nil {
			return err
		}
		result.Previous = &SendBanPolicy{SendBan: result.SendBan, Version: result.Version}
		if q.UID == "" && !exists && q.ChannelType != 1 {
			result.Status = "not_found"
			return nil
		}
		if channel.Disband != 0 {
			result.Status = "channel_disbanded"
			return nil
		}
		*result = nextSendBan(*result, q.SendBan, q.ExpectedVersion)
		if result.Status != "ok" || (q.UID != "" && user.SendBan == result.SendBan) || (q.UID == "" && channel.SendBan == result.SendBan) {
			return nil
		}

		if q.UID != "" {
			user.SendBan, user.SendBanVersion = result.SendBan, result.Version
			value := encodeUserValue(user)
			if err := batch.Set(key, value); err != nil {
				return err
			}
			state.tableRows[string(key)] = tableRowOverlay{value: value, exists: true}
		} else {
			channel.SendBan, channel.SendBanVersion = result.SendBan, result.Version
			shard := &Shard{db: state.db, hashSlot: hs}
			if err := shard.stageChannel(batch, key, channel); err != nil {
				return err
			}
			state.channelPublishes[string(key)] = channel
			delete(state.channelDeletes, string(key))
		}
		return nil
	})
	return result, nil
}

// nextSendBan is the shared CAS/version rule for dedicated and compound writes.
func nextSendBan(current SendBanResult, value int64, expected *uint64) SendBanResult {
	if expected != nil && *expected != current.Version {
		current.Status = "version_conflict"
		return current
	}
	if current.SendBan == value {
		return current
	}
	if current.Version == math.MaxUint64 {
		current.Status = "version_exhausted"
		return current
	}
	current.SendBan = value
	current.Version++
	return current
}
