package migration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"math"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/contracts/channelmembers"
	"github.com/WuKongIM/WuKongIM/internal/contracts/protocolmeta"
	"github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
)

// BusinessDecoder decodes original records without assigning target state.
type BusinessDecoder interface {
	DecodeBusiness(Row, RecordIdentity) (BusinessFacts, error)
}

// TargetRecord is a typed native metadata row with an explicit routing key.
// Value contains a MarshalState-encoded public meta type, never raw engine keys.
type TargetRecord struct {
	Table    string          `json:"table"`
	Owner    string          `json:"owner"`
	HashSlot uint16          `json:"hash_slot"`
	Value    json.RawMessage `json:"value"`
}

// TargetChannel describes a complete source log. PrefixThrough records missing
// historical offsets for diagnostics; the existing v3 importer requires zero.
type TargetChannel struct {
	Channel       ChannelIdentity `json:"channel"`
	PrefixThrough uint64          `json:"prefix_through"`
	LastSeq       uint64          `json:"last_seq"`
	Count         uint64          `json:"count"`
}

// ConversationUnreadArchive binds original ordinary conversation records whose
// independent counters are preserved in the archive rather than native rows.
type ConversationUnreadArchive struct {
	Rows        uint64 `json:"rows"`
	NonzeroRows uint64 `json:"nonzero_rows"`
	SHA256      string `json:"sha256"`
}

type TargetRecordsReport struct {
	ArchivedUnread    *ConversationUnreadArchive `json:"archived_unread,omitempty"`
	HiddenMemberships uint64                     `json:"hidden_memberships,omitempty"`
	Transformation    *MessageTransformReport    `json:"message_transformation,omitempty"`
	SelectionDigest   string                     `json:"selection_digest"`
	Digest            string                     `json:"digest"`
	Metadata          map[string]uint64          `json:"metadata_rows"`
	Messages          uint64                     `json:"messages"`
	MessageChannels   uint64                     `json:"message_channels"`
	MaxMessageID      uint64                     `json:"max_message_id"`
}

// BuildTargetRecords joins selected source business state into bounded native
// records on disk. A completion seal is written only after every join and log
// range check succeeds. Repeating the same conversion must produce exact bytes.
func BuildTargetRecords(ctx context.Context, selection SourceSelection, w Workspace, decoder BusinessDecoder) (report TargetRecordsReport, err error) {
	if ctx == nil || selection.Digest == "" || w == nil || decoder == nil {
		return report, errors.New("conversion requires a complete selected source")
	}
	decisions, err := newMissingConversationDecisions(selection)
	if err != nil {
		return report, err
	}
	transform, err := buildMessageTransform(ctx, selection, w, decoder, "message-transform/convert/")
	if err != nil {
		return report, err
	}
	if transform != nil {
		report.Transformation = transform.report
		report.MaxMessageID = transform.report.MaxSourceMessageID
	}
	report.SelectionDigest = selection.Digest
	report.Metadata = map[string]uint64{}
	b := &captureBatch{ctx: ctx, workspace: w}
	put := func(key string, value any) error {
		data, err := MarshalState(value)
		if err != nil {
			return err
		}
		return b.add(transfer.SpoolRow{Key: []byte(key), Value: data})
	}
	metadata := func(table, owner, key string, value any) error {
		data, err := MarshalState(value)
		if err != nil {
			return err
		}
		record := TargetRecord{Table: table, Owner: owner, HashSlot: targetHashSlot(owner), Value: data}
		return put(fmt.Sprintf("target/meta/%03d/%s/%s", record.HashSlot, table, key), record)
	}
	unreadHash := sha256.New()
	unreadEncoder := json.NewEncoder(unreadHash)
	if selection.Metadata != nil && selection.Metadata.Policy.DeriveUnreadFromBoundaries {
		report.ArchivedUnread = &ConversationUnreadArchive{}
	}
	err = WalkSelectedSources(ctx, w, func(record SelectedRecord) error {
		id := record.Identity
		if id.Channel.ID != "" && (strings.HasPrefix(id.Channel.ID, "__wk_internal_memberlist__/") || id.Channel.ID == "__wk_internal_system_uids__") {
			return errors.New("source channel collides with reserved target namespace")
		}
		switch record.Row.Table {
		case "ChannelClusterConfig":
			return nil // Source ownership is never target placement.
		case "SystemUid":
			const channel = "__wk_internal_system_uids__"
			registry := ChannelIdentity{ID: channel, Type: uint8(protocolmeta.ChannelTypeSystemUIDRegistry)}
			if err := put("convert/native-channel/"+channelTuple(registry), registry); err != nil {
				return err
			}
			if err := put("convert/native-member/"+channelTuple(registry)+"/"+tuple(id.UID), id.UID); err != nil {
				return err
			}
			return metadata("subscriber", channel, tuple(channel, uint8(protocolmeta.ChannelTypeSystemUIDRegistry), id.UID), meta.Subscriber{ChannelID: channel, ChannelType: int64(protocolmeta.ChannelTypeSystemUIDRegistry), UID: id.UID})
		}
		facts, err := decoder.DecodeBusiness(record.Row, id)
		if err != nil {
			return fmt.Errorf("convert %s: %w", record.Row.Table, err)
		}
		if c := facts.Conversation; c != nil && c.Type == 0 && report.ArchivedUnread != nil {
			report.ArchivedUnread.Rows++
			if c.UnreadCount != 0 {
				report.ArchivedUnread.NonzeroRows++
			}
			if err := unreadEncoder.Encode(struct {
				NodeID uint64
				Row    Row
			}{record.NodeID, record.Row}); err != nil {
				return err
			}
		}
		facts, omitted, err := transform.apply(ctx, id, facts)
		if err != nil || omitted {
			return err
		}
		switch {
		case facts.PluginBinding != nil:
			p := facts.PluginBinding
			if err := validatePluginBinding(p); err != nil {
				return err
			}
			// Native bindings are UID-owned. The old physical ID and exact
			// nanoseconds remain in the source archive, not in the v3 schema.
			return metadata("plugin_binding", p.UID, tuple(p.UID, p.PluginNo), meta.PluginUserBinding{UID: p.UID, PluginNo: p.PluginNo, CreatedAtMS: time.Unix(0, p.CreatedAtNS).UnixMilli(), UpdatedAtMS: time.Unix(0, p.UpdatedAtNS).UnixMilli()})
		case facts.User != nil:
			if facts.User.PluginNo != "" {
				return errors.New("legacy user plugin binding requires a verified compatibility mapping")
			}
			return metadata("user", id.UID, tuple(id.UID), meta.User{UID: id.UID})
		case facts.Device != nil:
			d := facts.Device
			if d.Flag > math.MaxInt64 {
				return errors.New("source device flag exceeds target range")
			}
			return metadata("device", d.UID, tuple(d.UID, d.Flag), meta.Device{UID: d.UID, DeviceFlag: int64(d.Flag), Token: d.Token, DeviceLevel: int64(d.Level)})
		case facts.Channel != nil:
			if err := put("convert/native-channel/"+channelTuple(id.Channel), id.Channel); err != nil {
				return err
			}
			return put("convert/channel/"+channelTuple(id.Channel), *facts.Channel)
		case facts.Member != nil:
			m := facts.Member
			channel := m.Channel.ID
			switch record.Row.Table {
			case "Allowlist":
				channel = channelmembers.AllowlistChannelID(channelmembers.ChannelKey{ChannelID: channel, ChannelType: m.Channel.Type})
			case "Denylist":
				channel = channelmembers.DenylistChannelID(channelmembers.ChannelKey{ChannelID: channel, ChannelType: m.Channel.Type})
			case "Subscriber":
				if err := put("convert/subscriber/"+channelTuple(m.Channel)+"/"+tuple(m.UID), m); err != nil {
					return err
				}
			}
			nativeID := ChannelIdentity{ID: channel, Type: m.Channel.Type}
			if err := put("convert/native-channel/"+channelTuple(nativeID), nativeID); err != nil {
				return err
			}
			if err := put("convert/native-member/"+channelTuple(nativeID)+"/"+tuple(m.UID), m.UID); err != nil {
				return err
			}
			return metadata("subscriber", channel, tuple(channel, m.Channel.Type, m.UID), meta.Subscriber{ChannelID: channel, ChannelType: int64(m.Channel.Type), UID: m.UID})
		case facts.Conversation != nil:
			c := facts.Conversation
			if c.UnreadCount != 0 && (selection.Metadata == nil || !selection.Metadata.Policy.DeriveUnreadFromBoundaries || c.Type == 1) {
				return errors.New("independent source unread count requires API equivalence validation")
			}
			if c.Type == 1 {
				if c.DeletedToSeq != 0 {
					return errors.New("source command conversation has an unsupported delete boundary")
				}
				return metadata("cmd_membership", c.UID, tuple(c.UID, c.Channel.ID, c.Channel.Type), meta.UserCMDChannelMembership{UID: c.UID, CommandChannelID: c.Channel.ID, ChannelType: int64(c.Channel.Type), StartSeq: 1, AckSeq: c.ReadSeq, UpdatedAt: c.UpdatedAtNS})
			}
			if err := put("convert/original-conversations/"+tuple(c.UID, c.Channel.ID, c.Channel.Type), true); err != nil {
				return err
			}
			return metadata("membership", c.UID, tuple(c.UID, c.Channel.ID, c.Channel.Type), meta.UserChannelMembership{UID: c.UID, ChannelID: c.Channel.ID, ChannelType: int64(c.Channel.Type), JoinSeq: 1, ReadSeq: c.ReadSeq, DeletedToSeq: c.DeletedToSeq, UpdatedAt: c.UpdatedAtNS, SourceVersion: 1})
		case facts.Message != nil:
			m := facts.Message
			if _, _, err := PrepareMessageRecord(*m); err != nil {
				return err
			}
			if err := put(fmt.Sprintf("target/messages/%s/%020d", channelTuple(id.Channel), m.MessageSeq), m); err != nil {
				return err
			}
			if err := put(fmt.Sprintf("convert/message-id/%020d", m.MessageID), tuple(id.Channel.ID, id.Channel.Type, m.MessageSeq)); err != nil {
				return err
			}
			if m.FromUID != "" && m.ClientMsgNo != "" {
				if err := put("convert/idempotency/"+tuple(id.Channel.ID, id.Channel.Type, m.FromUID, m.ClientMsgNo), m.MessageSeq); err != nil {
					return err
				}
			}
			return put("convert/log/"+channelTuple(id.Channel), id.Channel)
		case facts.Tail != nil:
			if err := put("convert/tail/"+channelTuple(id.Channel), facts.Tail); err != nil {
				return err
			}
			return put("convert/log/"+channelTuple(id.Channel), id.Channel)
		case facts.EventCursor != nil:
			return metadata("event_cursor", id.Channel.ID, tuple(id.Channel.ID, id.Channel.Type, id.ClientMsgNo), facts.EventCursor)
		case facts.EventState != nil:
			if err := put("convert/event-lanes/"+tuple(id.Channel.ID, id.Channel.Type, id.ClientMsgNo), true); err != nil {
				return err
			}
			if err := put("convert/event-id/"+tuple(id.Channel.ID, id.Channel.Type, id.ClientMsgNo, facts.EventState.LastEventID), facts.EventState.EventKey); err != nil {
				return err
			}
			return metadata("event_state", id.Channel.ID, tuple(id.Channel.ID, id.Channel.Type, id.ClientMsgNo, facts.EventState.EventKey), facts.EventState)
		default:
			return fmt.Errorf("unhandled selected business table %s", record.Row.Table)
		}
	})
	if err != nil {
		return report, err
	}
	if err = b.flush(); err != nil {
		return report, err
	}
	err = WalkTargetMetadata(ctx, w, func(row TargetRecord) error {
		switch row.Table {
		case "event_state":
			var state meta.MessageEventState
			if err := UnmarshalState(row.Value, &state); err != nil {
				return err
			}
			cursor, err := ReadTargetEventCursor(ctx, w, state)
			if err != nil {
				return err
			}
			if err := meta.ValidateImportedMessageEvent(state, cursor); err != nil {
				return fmt.Errorf("source event lane cannot be preserved by native import: %w", err)
			}
		case "event_cursor":
			var cursor meta.MessageEventCursor
			if err := UnmarshalState(row.Value, &cursor); err != nil {
				return err
			}
			_, found, err := w.Get(ctx, []byte("convert/event-lanes/"+tuple(cursor.ChannelID, uint8(cursor.ChannelType), cursor.ClientMsgNo)))
			if err != nil {
				return err
			}
			if !found {
				return errors.New("source event cursor has no durable lane projection")
			}
		}
		return nil
	})
	if err != nil {
		return report, err
	}
	if report.ArchivedUnread != nil {
		report.ArchivedUnread.SHA256 = hex.EncodeToString(unreadHash.Sum(nil))
	}
	// A subscriber is a durable member even before the first conversation.
	// With existing history, an absent v2 conversation cannot be inferred as
	// read, deleted or active; require an exact operator decision before import.
	err = w.Walk(ctx, []byte("convert/subscriber/"), func(row transfer.SpoolRow) error {
		var m SourceMember
		if err := UnmarshalState(row.Value, &m); err != nil {
			return err
		}
		key := "convert/original-conversations/" + tuple(m.UID, m.Channel.ID, m.Channel.Type)
		_, found, err := w.Get(ctx, []byte(key))
		if err != nil || found {
			return err
		}
		data, exists, err := w.Get(ctx, []byte("convert/tail/"+channelTuple(m.Channel)))
		if err != nil {
			return err
		}
		var tail SourceMessageTail
		if exists {
			if err := UnmarshalState(data, &tail); err != nil {
				return err
			}
		}
		readSeq, err := decisions.readSeq(m, tail.LastSeq)
		if err != nil {
			return err
		}
		if decisions.hiddenThrough(m) > 0 {
			report.HiddenMemberships++
		}
		return metadata("membership", m.UID, tuple(m.UID, m.Channel.ID, m.Channel.Type), meta.UserChannelMembership{UID: m.UID, ChannelID: m.Channel.ID, ChannelType: int64(m.Channel.Type), JoinSeq: 1, ReadSeq: readSeq, ConversationHiddenThroughSeq: decisions.hiddenThrough(m), SourceVersion: 1})
	})
	if err != nil {
		return report, err
	}
	if err = b.flush(); err != nil {
		return report, err
	}
	if err := decisions.complete(); err != nil {
		return report, err
	}
	// Channel counters are recomputed from the authoritative set, not old stale
	// informational columns. Each scan retains one row, even for very large groups.
	err = w.Walk(ctx, []byte("convert/native-channel/"), func(row transfer.SpoolRow) error {
		var id ChannelIdentity
		if err := UnmarshalState(row.Value, &id); err != nil {
			return err
		}
		source := SourceChannel{ChannelIdentity: id}
		data, found, err := w.Get(ctx, []byte("convert/channel/"+channelTuple(id)))
		if err != nil {
			return err
		}
		if found {
			if err := UnmarshalState(data, &source); err != nil {
				return err
			}
		}
		var count uint64
		if err := w.Walk(ctx, []byte("convert/native-member/"+channelTuple(source.ChannelIdentity)+"/"), func(transfer.SpoolRow) error { count++; return nil }); err != nil {
			return err
		}
		channel := meta.Channel{ChannelID: source.ID, ChannelType: int64(source.Type), Ban: boolInt(source.Ban), Disband: boolInt(source.Disband), SendBan: boolInt(source.SendBan), AllowStranger: boolInt(source.AllowStranger), Large: boolInt(source.Large), SubscriberCount: count, SubscriberMutationVersion: 1}
		return metadata("channel", source.ID, channelTuple(source.ChannelIdentity), channel)
	})
	if err != nil {
		return report, err
	}
	if err = b.flush(); err != nil {
		return report, err
	}
	err = w.Walk(ctx, []byte("convert/log/"), func(row transfer.SpoolRow) error {
		var id ChannelIdentity
		if err := UnmarshalState(row.Value, &id); err != nil {
			return err
		}
		tailBytes, found, err := w.Get(ctx, []byte("convert/tail/"+channelTuple(id)))
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("source channel %q has messages without a durable tail", id.ID)
		}
		var tail SourceMessageTail
		if err := UnmarshalState(tailBytes, &tail); err != nil {
			return err
		}
		channel := TargetChannel{Channel: id, LastSeq: tail.LastSeq, PrefixThrough: tail.LastSeq}
		var previous uint64
		err = WalkTargetMessages(ctx, w, id, func(m channelcompat.Message) error {
			if channel.Count == 0 {
				channel.PrefixThrough = m.MessageSeq - 1
			} else if m.MessageSeq != previous+1 {
				return fmt.Errorf("source channel %q has an interior sequence gap after %d", id.ID, previous)
			}
			if m.MessageSeq == 0 || m.MessageSeq > tail.LastSeq {
				return errors.New("source message exceeds its durable tail")
			}
			previous = m.MessageSeq
			channel.Count++
			report.Messages++
			report.MaxMessageID = max(report.MaxMessageID, m.MessageID)
			return nil
		})
		if err != nil {
			return err
		}
		if channel.Count > 0 && previous != tail.LastSeq {
			return errors.New("source message suffix is missing below its durable tail")
		}
		if channel.PrefixThrough != 0 {
			return fmt.Errorf("source channel %q retained prefix through %d cannot be represented by the existing v3 proposal format", id.ID, channel.PrefixThrough)
		}
		report.MessageChannels++
		return put("target/channels/"+channelTuple(id), channel)
	})
	if err != nil {
		return report, err
	}
	if err = b.flush(); err != nil {
		return report, err
	}
	if err := WalkTargetMetadata(ctx, w, func(row TargetRecord) error { report.Metadata[row.Table]++; return nil }); err != nil {
		return report, err
	}
	h := sha256.New()
	enc := json.NewEncoder(h)
	if err := w.Walk(ctx, []byte("target/"), func(row transfer.SpoolRow) error { return enc.Encode(row) }); err != nil {
		return report, err
	}
	report.Digest = hex.EncodeToString(h.Sum(nil))
	if err := put("conversion/COMPLETE", report); err != nil {
		return report, err
	}
	return report, b.flush()
}

// WalkTargetMetadata streams native rows in hash-slot/table/key order. Callers
// must require BuildTargetRecords success before installing any generated data.
func WalkTargetMetadata(ctx context.Context, w Workspace, visit func(TargetRecord) error) error {
	return w.Walk(ctx, []byte("target/meta/"), func(row transfer.SpoolRow) error {
		var record TargetRecord
		if err := UnmarshalState(row.Value, &record); err != nil {
			return err
		}
		if record.Owner == "" || record.HashSlot != targetHashSlot(record.Owner) {
			return errors.New("invalid target metadata owner")
		}
		return visit(record)
	})
}

func WalkTargetChannels(ctx context.Context, w Workspace, visit func(TargetChannel) error) error {
	return w.Walk(ctx, []byte("target/channels/"), func(row transfer.SpoolRow) error {
		var channel TargetChannel
		if err := UnmarshalState(row.Value, &channel); err != nil {
			return err
		}
		return visit(channel)
	})
}

func WalkTargetMessages(ctx context.Context, w Workspace, id ChannelIdentity, visit func(channelcompat.Message) error) error {
	return w.Walk(ctx, []byte("target/messages/"+channelTuple(id)+"/"), func(row transfer.SpoolRow) error {
		var message channelcompat.Message
		if err := UnmarshalState(row.Value, &message); err != nil {
			return err
		}
		if message.ChannelID != id.ID || message.ChannelType != id.Type {
			return errors.New("target message channel mismatch")
		}
		return visit(message)
	})
}

// ReadTargetEventCursor joins a projected lane to its original message cursor.
func ReadTargetEventCursor(ctx context.Context, w Workspace, state meta.MessageEventState) (meta.MessageEventCursor, error) {
	key := fmt.Sprintf("target/meta/%03d/event_cursor/%s", targetHashSlot(state.ChannelID), tuple(state.ChannelID, uint8(state.ChannelType), state.ClientMsgNo))
	data, found, err := w.Get(ctx, []byte(key))
	if err != nil {
		return meta.MessageEventCursor{}, err
	}
	if !found {
		return meta.MessageEventCursor{}, errors.New("source event state has no message cursor")
	}
	var row TargetRecord
	if err := UnmarshalState(data, &row); err != nil {
		return meta.MessageEventCursor{}, err
	}
	var cursor meta.MessageEventCursor
	if err := UnmarshalState(row.Value, &cursor); err != nil {
		return cursor, err
	}
	if cursor.LastMsgEventSeq < state.LastMsgEventSeq {
		return cursor, errors.New("source event state exceeds message cursor")
	}
	return cursor, nil
}

func targetHashSlot(owner string) uint16 { return uint16(crc32.ChecksumIEEE([]byte(owner)) % 256) }
func boolInt(v bool) int64 {
	if v {
		return 1
	}
	return 0
}
func tuple(values ...any) string {
	return IdentityKey(values...)
}
func channelTuple(id ChannelIdentity) string { return tuple(id.ID, id.Type) }
