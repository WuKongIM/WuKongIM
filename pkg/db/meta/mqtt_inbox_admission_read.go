package meta

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
)

// MQTTInboxAdmissionView pairs directory readiness, runtime and progress in one pinned
// snapshot. A stale checkpoint is visible for reset; it never implies readiness.
type MQTTInboxAdmissionView struct {
	ChannelID  string              `json:"channel_id"`
	Channel    *Channel            `json:"channel,omitempty"`
	Runtime    *ChannelRuntimeMeta `json:"runtime,omitempty"`
	Checkpoint *MQTTInboxAdmission `json:"checkpoint,omitempty"`
}

// ValidateMQTTInboxAdmissionView checks identity and shape only. A consumer must
// require equal positive generations and Participant 2 before using completion.
func ValidateMQTTInboxAdmissionView(id string, v *MQTTInboxAdmissionView) error {
	if _, err := mqttInboxParticipants(id); err != nil || v == nil || v.ChannelID != id {
		return dberrors.ErrCorruptValue
	}
	if c := v.Channel; c != nil && (c.ChannelID != id || c.ChannelType != 1 || validateChannel(*c) != nil) {
		return dberrors.ErrCorruptValue
	}
	if r := v.Runtime; r != nil && (r.ChannelID != id || r.ChannelType != 1 || r.DirectoryGeneration == 0 || validateChannelRuntimeMeta(*r) != nil) {
		return dberrors.ErrCorruptValue
	}
	if c := v.Checkpoint; c != nil && (c.ChannelID != id || ValidateMQTTInboxAdmission(*c) != nil) {
		return dberrors.ErrCorruptValue
	}
	// Runtime absence with live progress indicates a deletion that lost its fence.
	if v.Runtime == nil && v.Checkpoint != nil && v.Checkpoint.DirectoryGeneration != 0 {
		return dberrors.ErrCorruptValue
	}
	return nil
}

func (s *Shard) readMQTTInboxAdmission(ctx context.Context, id string) (*MQTTInboxAdmissionView, error) {
	if s.readSnapshot == nil {
		return nil, dberrors.ErrInvalidArgument
	}
	v := &MQTTInboxAdmissionView{ChannelID: id}
	c, hasChannel, e := channelTable.Get(ctx, s, KeyParts{String(id), Int64Ordered(1)})
	if e != nil {
		return nil, e
	}
	if hasChannel {
		v.Channel = &c
	}
	r, found, err := channelRuntimeMetaTable.Get(ctx, s, channelRuntimeMetaPrimaryKey(id, 1))
	if err != nil {
		return nil, err
	}
	if found {
		v.Runtime = &r
	}
	key := mqttInboxAdmissionKey(s.hashSlot, id)
	value, found, err := s.readSnapshot.Get(key)
	if err != nil {
		return nil, err
	}
	if found {
		c, e := decodeMQTTInboxAdmission(key, id, value)
		if e != nil {
			return nil, e
		}
		v.Checkpoint = &c
	}
	if err = ValidateMQTTInboxAdmissionView(id, v); err != nil {
		return nil, err
	}
	return v, nil
}
