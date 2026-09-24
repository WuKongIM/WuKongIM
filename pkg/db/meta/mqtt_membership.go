package meta

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
)

// SubscriberKey identifies one ordinary Channel membership, never a SEND grant.
type SubscriberKey struct {
	ChannelID   string `json:"channel_id"`
	ChannelType int64  `json:"channel_type"`
	UID         string `json:"uid"`
}

// MQTTMembershipView holds three point reads from the same metadata snapshot.
// Missing rows mean absence only after current Slot authority has been proven.
type MQTTMembershipView struct {
	Key     SubscriberKey `json:"key"`
	Channel *Channel      `json:"channel,omitempty"`
	Member  *Subscriber   `json:"member,omitempty"`
	// Sequence witnesses the member's incarnation, including the legacy floor 1.
	Sequence uint64 `json:"sequence"`
}

func validateMQTTMembershipKey(k SubscriberKey) error {
	if validateMQTTIdentity(k.ChannelID, 4096) != nil || validateMQTTIdentity(k.UID, 1024) != nil || k.ChannelType < 1 || k.ChannelType > 255 {
		return dberrors.ErrInvalidArgument
	}
	return nil
}

// ValidateMQTTMembershipView checks identity and incarnation evidence, without
// applying receive policy. Orphan members cannot substitute for an absent channel.
func ValidateMQTTMembershipView(k SubscriberKey, v *MQTTMembershipView) error {
	if validateMQTTMembershipKey(k) != nil || v == nil || v.Key != k || v.Sequence == 0 {
		return dberrors.ErrCorruptValue
	}
	if c := v.Channel; c != nil && (c.ChannelID != k.ChannelID || c.ChannelType != k.ChannelType || validateChannel(*c) != nil) {
		return dberrors.ErrCorruptValue
	}
	if m := v.Member; m != nil && (m.ChannelID != k.ChannelID || m.ChannelType != k.ChannelType || m.UID != k.UID || m.Incarnation == 0 || m.Incarnation > v.Sequence) {
		return dberrors.ErrCorruptValue
	}
	return nil
}

// readMQTTMembership bypasses the live channel cache: all three values must
// belong to readMQTTState's pinned snapshot, including authoritative absences.
func (s *Shard) readMQTTMembership(ctx context.Context, k SubscriberKey) (*MQTTMembershipView, error) {
	v := &MQTTMembershipView{Key: k}
	c, found, err := channelTable.Get(ctx, s, KeyParts{String(k.ChannelID), Int64Ordered(k.ChannelType)})
	if err != nil {
		return nil, err
	}
	if found {
		v.Channel = &c
	}
	m, found, err := s.GetSubscriber(ctx, k.ChannelID, k.ChannelType, k.UID)
	if err != nil {
		return nil, err
	}
	if found {
		v.Member = &m
	}
	v.Sequence, err = s.SubscriberSequence(ctx)
	if err != nil {
		return nil, err
	}
	if err = ValidateMQTTMembershipView(k, v); err != nil {
		return nil, err
	}
	return v, nil
}
