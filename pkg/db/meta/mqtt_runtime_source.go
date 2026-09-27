package meta

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
)

// MQTTRuntimeView pins a physical runtime and its retained deletion floor.
// This is neither business Channel lifecycle identity nor permission to append.
type MQTTRuntimeView struct {
	Channel ChannelKey          `json:"channel"`
	Meta    *ChannelRuntimeMeta `json:"meta,omitempty"`
	// RetiredThrough is zero before the first physical deletion and remains stable
	// across authority changes within a live runtime. Meta may be absent at any floor.
	RetiredThrough uint64 `json:"retired_through"`
}

func validateMQTTRuntimeKey(key ChannelKey) error {
	if validateMQTTIdentity(key.ChannelID, 4096) != nil || (key.ChannelType != 1 && key.ChannelType != 2) {
		return dberrors.ErrInvalidArgument
	}
	return nil
}

// ValidateMQTTRuntimeView rejects cross-Channel or inconsistent snapshot data.
// Consumers still need fresh authority and lifecycle checks before any effect.
func ValidateMQTTRuntimeView(key ChannelKey, v *MQTTRuntimeView) error {
	if validateMQTTRuntimeKey(key) != nil || v == nil || v.Channel != key {
		return dberrors.ErrCorruptValue
	}
	if m := v.Meta; m != nil {
		if m.ChannelID != key.ChannelID || m.ChannelType != key.ChannelType ||
			validateChannelRuntimeMeta(*m) != nil || m.ChannelEpoch <= v.RetiredThrough ||
			m.LeaderEpoch <= v.RetiredThrough || m.RouteGeneration <= v.RetiredThrough {
			return dberrors.ErrCorruptValue
		}
	}
	return nil
}

func (s *Shard) readMQTTRuntime(ctx context.Context, channel ChannelKey) (*MQTTRuntimeView, error) {
	if s.readSnapshot == nil {
		return nil, dberrors.ErrInvalidArgument
	}
	v := &MQTTRuntimeView{Channel: channel}
	m, found, err := channelRuntimeMetaTable.Get(ctx, s, channelRuntimeMetaPrimaryKey(channel.ChannelID, channel.ChannelType))
	if err != nil {
		return nil, err
	}
	if found {
		v.Meta = &m
	}
	key := runtimeRetirementKey(s.hashSlot, channel.ChannelID, channel.ChannelType)
	value, found, err := s.readSnapshot.Get(key)
	if err != nil {
		return nil, err
	}
	if found {
		v.RetiredThrough, err = decodeRuntimeRetirement(key, value)
		if err != nil {
			return nil, err
		}
	}
	if err := ValidateMQTTRuntimeView(channel, v); err != nil {
		return nil, err
	}
	return v, nil
}
