package cmdsync

import (
	"context"
	"errors"
	"strings"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	channelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
)

// MaxBindingRecipients bounds one explicit directory mutation, before normalization.
const MaxBindingRecipients = 1000
const maxBindingIdentityBytes = 256 * 1024

// ErrBindingTargets rejects mixed selector forms, empty targets, or oversized batches.
var ErrBindingTargets = errors.New("internal/usecase/cmdsync: select uid, uids with a source channel, or subscribers only; at most 1000 recipients and 256 KiB of identities")

// Bind captures one committed tail for all recipients before proposing UID-owned
// directory rows. Failed cross-Slot writes may be partial; retrying a live binding
// preserves its original start and acknowledgement positions.
func (a *App) Bind(ctx context.Context, cmd BindCommand) error {
	codec := channelid.CommandCodec{}
	if a != nil {
		codec = a.commandChannels
	}
	uids, key, err := bindingTargets(codec, cmd.UID, cmd.UIDs, cmd.Subscribers, cmd.ChannelID, cmd.ChannelType)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if a == nil || a.states == nil {
		return ErrStateStoreRequired
	}
	if a.messages == nil {
		return ErrMessageStoreRequired
	}
	tail, err := a.messages.CommandChannelTail(ctx, key)
	if err != nil {
		return err
	}
	if tail == ^uint64(0) {
		return ErrSequenceExhausted
	}
	now := a.now().UnixNano()
	rows := make([]metadb.UserCMDChannelMembership, len(uids))
	for i, uid := range uids {
		rows[i] = metadb.UserCMDChannelMembership{UID: uid, CommandChannelID: key.ChannelID, ChannelType: int64(key.ChannelType), StartSeq: tail + 1, UpdatedAt: now}
	}
	return a.states.UpsertUserCMDChannelMemberships(ctx, rows)
}

// Unbind removes the same bounded target forms without reading or deleting messages.
func (a *App) Unbind(ctx context.Context, cmd UnbindCommand) error {
	codec := channelid.CommandCodec{}
	if a != nil {
		codec = a.commandChannels
	}
	uids, key, err := bindingTargets(codec, cmd.UID, cmd.UIDs, cmd.Subscribers, cmd.ChannelID, cmd.ChannelType)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if a == nil || a.states == nil {
		return ErrStateStoreRequired
	}
	now := a.now().UnixNano()
	rows := make([]metadb.UserCMDChannelMembership, len(uids))
	for i, uid := range uids {
		rows[i] = metadb.UserCMDChannelMembership{UID: uid, CommandChannelID: key.ChannelID, ChannelType: int64(key.ChannelType), Tombstone: true, TombstoneAt: now, UpdatedAt: now}
	}
	return a.states.TombstoneUserCMDChannelMemberships(ctx, rows)
}

func bindingTargets(codec channelid.CommandCodec, uid string, uids, subscribers []string, source string, kind uint8) ([]string, CommandChannelKey, error) {
	empty := CommandChannelKey{}
	if len(uids) > MaxBindingRecipients || len(subscribers) > MaxBindingRecipients {
		return nil, empty, ErrBindingTargets
	}
	total := len(uid) + len(source)
	for _, list := range [][]string{uids, subscribers} {
		for _, id := range list {
			total += len(id)
			if total > maxBindingIdentityBytes {
				return nil, empty, ErrBindingTargets
			}
		}
	}
	if total > maxBindingIdentityBytes {
		return nil, empty, ErrBindingTargets
	}
	uid = strings.TrimSpace(uid)
	source = strings.TrimSpace(source)
	if len(subscribers) > 0 {
		if uid != "" || len(uids) > 0 || source != "" || kind != 0 {
			return nil, empty, ErrBindingTargets
		}
		scoped, err := codec.RequestSubscriberChannelFor(subscribers)
		if err != nil {
			return nil, empty, ErrBindingTargets
		}
		return scoped.Subscribers, CommandChannelKey{ChannelID: scoped.CommandChannelID, ChannelType: scoped.ChannelType}, nil
	}
	if len(uids) > 0 {
		if uid != "" {
			return nil, empty, ErrBindingTargets
		}
		normalized := channelid.NormalizeRequestSubscribers(uids)
		if len(normalized) == 0 {
			return nil, empty, ErrBindingTargets
		}
		_, source, err := validateBindingIdentity(normalized[0], source, kind)
		if err != nil {
			return nil, empty, err
		}
		return normalized, CommandChannelKey{ChannelID: codec.ToCommandChannel(source), ChannelType: kind}, nil
	}
	uid, source, err := validateBindingIdentity(uid, source, kind)
	if err != nil {
		return nil, empty, err
	}
	return []string{uid}, CommandChannelKey{ChannelID: codec.ToCommandChannel(source), ChannelType: kind}, nil
}
