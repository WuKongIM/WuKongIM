package migration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
)

// MessagePolicy authorizes target-only omissions. Compaction never repairs a
// pre-existing source gap and never changes native v3 storage or replication.
type MessagePolicy struct {
	KeepLatestDuplicates bool `json:"keep_latest_duplicates"`
	// ResolveDuplicateChains follows strictly increasing same-channel replacement
	// edges to one surviving terminal; every original edge remains in the archive.
	ResolveDuplicateChains bool `json:"resolve_duplicate_chains,omitempty"`
	ExcludeCMD             bool `json:"exclude_cmd"`
	// ExcludeStreams omits stream-flagged messages and legacy StreamNo parents,
	// plus their unambiguous event projections; original rows remain archived.
	ExcludeStreams   bool `json:"exclude_streams"`
	CompactSequences bool `json:"compact_sequences"`
}

func validateMessagePolicy(p *MessagePolicy) error {
	if p != nil && p.ResolveDuplicateChains && !p.KeepLatestDuplicates {
		return errors.New("duplicate chain resolution requires keep_latest_duplicates")
	}
	if p != nil && (!p.CompactSequences || (!p.KeepLatestDuplicates && !p.ExcludeCMD && !p.ExcludeStreams)) {
		return errors.New("message omissions require compact_sequences and an explicit duplicate, CMD or stream policy")
	}
	return nil
}

// IsStreamMessage uses persisted protocol identity, never payload heuristics.
// The pinned v2 Setting stream bit is 1<<1; older parents also carry StreamNo.
func IsStreamMessage(m channelcompat.Message) bool {
	return m.Setting&(1<<1) != 0 || m.StreamNo != ""
}

// MessageTransformReport binds all decisions to the selected original rows.
// It does not certify that external clients have reset or remapped old cursors.
type MessageTransformReport struct {
	Policy             MessagePolicy `json:"policy"`
	Original           uint64        `json:"original_messages"`
	Retained           uint64        `json:"retained_messages"`
	DuplicateDrops     uint64        `json:"duplicate_drops"`
	CMDDrops           uint64        `json:"cmd_drops"`
	StreamDrops        uint64        `json:"stream_drops"`
	QuarantineDrops    uint64        `json:"quarantine_drops,omitempty"`
	StreamEventStates  uint64        `json:"omitted_stream_event_states"`
	StreamEventCursors uint64        `json:"omitted_stream_event_cursors"`
	CMDConversations   uint64        `json:"omitted_cmd_conversations"`
	ChangedChannels    uint64        `json:"changed_channels"`
	ChangedSequences   uint64        `json:"changed_surviving_sequences"`
	MaxSourceMessageID uint64        `json:"max_source_message_id,string"`
	ChainRoots         uint64        `json:"duplicate_chain_roots,omitempty"`
	ChainTerminals     uint64        `json:"duplicate_chain_terminals,omitempty"`
	ChainSHA256        string        `json:"duplicate_chains_sha256,omitempty"`
	MappingSHA256      string        `json:"mapping_sha256"`
}

// MessageSequenceMapping distinguishes an omitted row from a position boundary.
// BoundarySeq counts retained messages at or below OriginalSeq, including when
// that exact old row was omitted; it must not jump forward to a duplicate winner.
type MessageSequenceMapping struct {
	SourceNodeID uint64          `json:"source_node_id,string"`
	Channel      ChannelIdentity `json:"channel"`
	OriginalSeq  uint64          `json:"original_seq,string"`
	MessageID    uint64          `json:"message_id,string"`
	TargetSeq    uint64          `json:"target_seq,string"`
	BoundarySeq  uint64          `json:"boundary_seq,string"`
	Omitted      string          `json:"omitted,omitempty"`
	SourceSHA256 string          `json:"source_sha256"`
	Winners      []DedupeMessage `json:"winners,omitempty"`
	// TerminalWinner supplements direct Winners; it never moves BoundarySeq.
	TerminalWinner *DedupeMessage `json:"terminal_winner,omitempty"`
}

type transformedChannel struct {
	SourceNodeID uint64          `json:"source_node_id,string"`
	Channel      ChannelIdentity `json:"channel"`
	OriginalLast uint64          `json:"original_last"`
	Retained     uint64          `json:"retained"`
	Changed      bool            `json:"changed"`
}

type messageTransform struct {
	w      Workspace
	report *MessageTransformReport
}

// scopedWorkspace keeps independently rebuilt verification decisions separate
// from conversion artifacts while preserving ordered keys for disk joins.
type scopedWorkspace struct {
	Workspace
	prefix string
}

func (w scopedWorkspace) Put(ctx context.Context, rows []transfer.SpoolRow) error {
	out := make([]transfer.SpoolRow, len(rows))
	for i, r := range rows {
		out[i] = transfer.SpoolRow{Key: append([]byte(w.prefix), r.Key...), Value: r.Value}
	}
	return w.Workspace.Put(ctx, out)
}
func (w scopedWorkspace) Get(ctx context.Context, key []byte) ([]byte, bool, error) {
	return w.Workspace.Get(ctx, append([]byte(w.prefix), key...))
}
func (w scopedWorkspace) Walk(ctx context.Context, prefix []byte, visit func(transfer.SpoolRow) error) error {
	return w.Workspace.Walk(ctx, append([]byte(w.prefix), prefix...), func(r transfer.SpoolRow) error { r.Key = r.Key[len(w.prefix):]; return visit(r) })
}

// buildMessageTransform reconstructs decisions from selected original rows only.
// Verify calls it with a distinct namespace and never consumes conversion maps.
func buildMessageTransform(ctx context.Context, selection SourceSelection, w Workspace, decoder BusinessDecoder, namespace string) (*messageTransform, error) {
	if err := validateMessagePolicy(selection.Messages); err != nil {
		return nil, err
	}
	if selection.Messages == nil {
		return nil, nil
	}
	policy := *selection.Messages
	sourceWorkspace := w
	w = scopedWorkspace{Workspace: w, prefix: namespace}
	report := &MessageTransformReport{Policy: policy}
	p := &dedupePlanner{ctx: ctx, w: w, b: &captureBatch{ctx: ctx, workspace: w}, out: json.NewEncoder(io.Discard)}
	put := func(key string, v any) error {
		data, err := MarshalState(v)
		if err != nil {
			return err
		}
		return p.b.add(transfer.SpoolRow{Key: []byte(key), Value: data})
	}
	err := WalkSelectedSources(ctx, sourceWorkspace, func(r SelectedRecord) error {
		if r.Row.Table != "Message" && r.Row.Table != "Conversation" && r.Row.Table != "PendingConversation" {
			return nil
		}
		f, err := decoder.DecodeBusiness(r.Row, r.Identity)
		if err != nil {
			return err
		}
		if f.Conversation != nil {
			if policy.ExcludeCMD && f.Conversation.Type == 1 {
				report.CMDConversations++
			}
			return nil
		}
		if f.Tail != nil {
			return put("input/tail/"+channelTuple(r.Identity.Channel), f.Tail)
		}
		m := f.Message
		if m == nil {
			return errors.New("missing source message facts")
		}
		data, err := MarshalState(r.Row)
		if err != nil {
			return err
		}
		ref := DedupeMessage{NodeID: 1, Owner: r.Row.Owner, CMD: f.CMDMessage, Stream: IsStreamMessage(*m), ChannelSHA256: diagnosticSHA([]byte(channelTuple(r.Identity.Channel))), StreamParent: m.StreamNo != "", MessageEvidence: MessageEvidence{ID: m.MessageID, Sequence: m.MessageSeq, SHA256: diagnosticSHA(data)}}
		if policy.ExcludeStreams && m.ClientMsgNo != "" {
			key := tuple(m.ChannelID, m.ChannelType, m.ClientMsgNo)
			if err := put(dedupeMessageKey("event-key", ref), key); err != nil {
				return err
			}
			if ref.Stream {
				if err := put("stream-event/"+key, true); err != nil {
					return err
				}
			}
		}
		if m.FromUID != "" && m.ClientMsgNo != "" {
			ref.ClientKeySHA256 = diagnosticSHA([]byte(tuple(m.ChannelID, m.ChannelType, m.FromUID, m.ClientMsgNo)))
		}
		report.Original++
		report.MaxSourceMessageID = max(report.MaxSourceMessageID, m.MessageID)
		if err := put(fmt.Sprintf("input/node/%020d", ref.Owner), r.NodeID); err != nil {
			return err
		}
		if err := put(fmt.Sprintf("input/channel/%020d", ref.Owner), r.Identity.Channel); err != nil {
			return err
		}
		if err := p.put(dedupeMessageKey("message", ref), ref); err != nil {
			return err
		}
		if !policy.KeepLatestDuplicates || (policy.ExcludeCMD && ref.CMD) || (policy.ExcludeStreams && ref.Stream) {
			return nil
		}
		for _, kind := range []string{"id", "client"} {
			if kind == "client" && ref.ClientKeySHA256 == "" {
				continue
			}
			if err := p.put(dedupeGroupKey(kind, ref)+fmt.Sprintf("/%020d/%020d", ref.Owner, ref.Sequence), ref); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := p.b.flush(); err != nil {
		return nil, err
	}
	if err := addQuarantinePositions(ctx, selection, sourceWorkspace, w, decoder, p, report); err != nil {
		return nil, err
	}
	if policy.KeepLatestDuplicates {
		for _, kind := range []string{"id", "client"} {
			if err := p.groups(1, kind, &DedupeNode{}); err != nil {
				return nil, err
			}
		}
		if p.report.Unresolved != 0 {
			return nil, errors.New("duplicate MessageID spans incomparable source channels")
		}
		if err := p.b.flush(); err != nil {
			return nil, err
		}
	}
	var current transformedChannel
	var owner uint64
	flush := func() error {
		if current.OriginalLast == 0 {
			return nil
		}
		tail, found, err := transformGet[SourceMessageTail](ctx, w, "input/tail/"+channelTuple(current.Channel))
		if err != nil {
			return err
		}
		if !found || tail.LastSeq != current.OriginalLast {
			return errors.New("original message history differs from durable tail before transformation")
		}
		if current.Changed {
			report.ChangedChannels++
		}
		return put("channel/"+channelTuple(current.Channel), current)
	}
	err = w.Walk(ctx, []byte("dedupe/message/"), func(row transfer.SpoolRow) error {
		var m DedupeMessage
		if err := json.Unmarshal(row.Value, &m); err != nil {
			return err
		}
		if current.OriginalLast == 0 || owner != m.Owner {
			if err := flush(); err != nil {
				return err
			}
			owner = m.Owner
			ch, found, err := transformGet[ChannelIdentity](ctx, w, fmt.Sprintf("input/channel/%020d", owner))
			if err != nil {
				return err
			}
			if !found {
				return errors.New("missing transform channel identity")
			}
			node, found, err := transformGet[uint64](ctx, w, fmt.Sprintf("input/node/%020d", owner))
			if err != nil {
				return err
			}
			if !found || node == 0 {
				return errors.New("missing original source node")
			}
			current = transformedChannel{Channel: ch, SourceNodeID: node}
		}
		if m.Sequence != current.OriginalLast+1 {
			return errors.New("pre-existing source sequence gap cannot be repaired by message omission")
		}
		current.OriginalLast = m.Sequence
		mapping := MessageSequenceMapping{SourceNodeID: current.SourceNodeID, Channel: current.Channel, OriginalSeq: m.Sequence, MessageID: m.ID, SourceSHA256: m.SHA256}
		_, quarantined, err := w.Get(ctx, []byte(dedupeMessageKey("quarantine", m)))
		if err != nil {
			return err
		}
		if quarantined {
			mapping.Omitted = "quarantined_invalid_message_channel"
			report.QuarantineDrops++
		} else if policy.ExcludeCMD && m.CMD {
			mapping.Omitted = "cmd"
			report.CMDDrops++
		} else if policy.ExcludeStreams && m.Stream {
			mapping.Omitted = "stream"
			report.StreamDrops++
		} else if policy.KeepLatestDuplicates {
			d, err := p.decision(m)
			if err != nil {
				return err
			}
			if d.Unresolved {
				return errors.New("ambiguous duplicate decision")
			}
			if len(d.Winners) > 0 {
				mapping.Omitted = "duplicate"
				mapping.Winners = d.Winners
				for i := range mapping.Winners {
					mapping.Winners[i].NodeID = current.SourceNodeID
				}
				report.DuplicateDrops++
			}
		}
		if mapping.Omitted == "" {
			if policy.ExcludeStreams {
				key, found, err := transformGet[string](ctx, w, dedupeMessageKey("event-key", m))
				if err != nil {
					return err
				}
				if found {
					_, shared, err := w.Get(ctx, []byte("stream-event/"+key))
					if err != nil {
						return err
					}
					if shared {
						if err := put("retained-event/"+key, true); err != nil {
							return err
						}
					}
				}
			}
			current.Retained++
			report.Retained++
			mapping.TargetSeq = current.Retained
			if mapping.TargetSeq != mapping.OriginalSeq {
				report.ChangedSequences++
			}
		} else {
			current.Changed = true
			if err := put(dedupeMessageKey("drop", m), mapping); err != nil {
				return err
			}
		}
		mapping.BoundarySeq = current.Retained
		return put(fmt.Sprintf("mapping/%s/%020d", channelTuple(current.Channel), m.Sequence), mapping)
	})
	if err != nil {
		return nil, err
	}
	if err := flush(); err != nil {
		return nil, err
	}
	if err := p.b.flush(); err != nil {
		return nil, err
	}
	// Preserve direct provenance and certify approved transitive replacements.
	resolver, err := newDuplicateResolver(w)
	if err != nil {
		return nil, err
	}
	err = w.Walk(ctx, []byte("dedupe/drop/"), func(row transfer.SpoolRow) error {
		var m MessageSequenceMapping
		if err := UnmarshalState(row.Value, &m); err != nil {
			return err
		}
		chained := false
		for _, winner := range m.Winners {
			winner.NodeID = 1
			_, dropped, err := w.Get(ctx, []byte(dedupeMessageKey("drop", winner)))
			if err != nil {
				return err
			}
			chained = chained || dropped
		}
		if !chained {
			return nil
		}
		if !policy.ResolveDuplicateChains {
			return errors.New("duplicate winner is superseded by another uniqueness rule")
		}
		terminal, err := resolver.resolveDuplicateTerminal(ctx, m)
		if err != nil {
			return err
		}
		// Load the canonical mapping, whose boundary was populated after the drop row.
		key := fmt.Sprintf("mapping/%s/%020d", channelTuple(m.Channel), m.OriginalSeq)
		canonical, found, err := transformGet[MessageSequenceMapping](ctx, w, key)
		if err != nil {
			return err
		}
		if !found {
			return errors.New("missing duplicate chain source mapping")
		}
		canonical.TerminalWinner = &terminal
		report.ChainRoots++
		rootKey := "chains/" + channelTuple(m.Channel) + fmt.Sprintf("/%020d", m.OriginalSeq)
		if err := put("chain-terminals/"+channelTuple(m.Channel)+fmt.Sprintf("/%020d/%020d", terminal.Sequence, m.OriginalSeq), rootKey); err != nil {
			return err
		}
		return put(rootKey, canonical)
	})
	if err != nil {
		return nil, err
	}
	if err := p.b.flush(); err != nil {
		return nil, err
	}
	lastTerminal := ""
	if err := w.Walk(ctx, []byte("chain-terminals/"), func(row transfer.SpoolRow) error {
		if len(row.Key) < 21 || row.Key[len(row.Key)-21] != '/' {
			return errors.New("invalid duplicate terminal index key")
		}
		key := string(row.Key[:len(row.Key)-21])
		if key != lastTerminal {
			report.ChainTerminals++
			lastTerminal = key
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if report.ChainRoots > 0 {
		h := sha256.New()
		enc := json.NewEncoder(h)
		if err := w.Walk(ctx, []byte("chains/"), func(row transfer.SpoolRow) error { return enc.Encode(row) }); err != nil {
			return nil, err
		}
		report.ChainSHA256 = hex.EncodeToString(h.Sum(nil))
	}
	h := sha256.New()
	enc := json.NewEncoder(h)
	if err := w.Walk(ctx, []byte("mapping/"), func(row transfer.SpoolRow) error { return enc.Encode(row) }); err != nil {
		return nil, err
	}
	report.MappingSHA256 = hex.EncodeToString(h.Sum(nil))
	t := &messageTransform{w: w, report: report}
	if policy.ExcludeStreams {
		if err := WalkSelectedSources(ctx, sourceWorkspace, func(r SelectedRecord) error {
			if r.Row.Table != "MessageEventState" && r.Row.Table != "MessageEventSeq" {
				return nil
			}
			omit, err := t.omitStreamEvent(ctx, r.Identity)
			if err != nil || !omit {
				return err
			}
			if r.Row.Table == "MessageEventState" {
				report.StreamEventStates++
			} else {
				report.StreamEventCursors++
			}
			return nil
		}); err != nil {
			return nil, err
		}
	}
	return t, nil
}

// omitStreamEvent joins the original channel/client identity. A shared key
// cannot establish which parent owns the projection and must block conversion.
func (t *messageTransform) omitStreamEvent(ctx context.Context, id RecordIdentity) (bool, error) {
	if !t.report.Policy.ExcludeStreams {
		return false, nil
	}
	key := tuple(id.Channel.ID, id.Channel.Type, id.ClientMsgNo)
	_, omitted, err := t.w.Get(ctx, []byte("stream-event/"+key))
	if err != nil || !omitted {
		return false, err
	}
	_, retained, err := t.w.Get(ctx, []byte("retained-event/"+key))
	if err == nil && retained {
		err = errors.New("stream event identity is shared with a retained message")
	}
	return true, err
}

func transformGet[T any](ctx context.Context, w Workspace, key string) (v T, found bool, err error) {
	data, found, err := w.Get(ctx, []byte(key))
	if err != nil || !found {
		return v, found, err
	}
	err = UnmarshalState(data, &v)
	return
}

func (t *messageTransform) boundary(ctx context.Context, ch ChannelIdentity, old uint64) (uint64, error) {
	if t == nil || old == 0 {
		return old, nil
	}
	c, found, err := transformGet[transformedChannel](ctx, t.w, "channel/"+channelTuple(ch))
	if err != nil {
		return 0, err
	}
	if !found || !c.Changed {
		return old, nil
	}
	if old >= c.OriginalLast {
		return c.Retained, nil
	}
	m, found, err := transformGet[MessageSequenceMapping](ctx, t.w, fmt.Sprintf("mapping/%s/%020d", channelTuple(ch), old))
	if err != nil {
		return 0, err
	}
	if !found {
		return 0, errors.New("old cursor has no validated source position")
	}
	return m.BoundarySeq, nil
}

// apply changes decoded copies only. Read/delete boundaries retain the old
// ordering of surviving messages, and CMD memberships are omitted with history.
func (t *messageTransform) apply(ctx context.Context, id RecordIdentity, f BusinessFacts) (BusinessFacts, bool, error) {
	if t == nil {
		return f, false, nil
	}
	if f.EventState != nil || f.EventCursor != nil {
		omitted, err := t.omitStreamEvent(ctx, id)
		return f, omitted, err
	}
	if m := f.Message; m != nil {
		mapped, found, err := transformGet[MessageSequenceMapping](ctx, t.w, fmt.Sprintf("mapping/%s/%020d", channelTuple(id.Channel), m.MessageSeq))
		if err != nil {
			return f, false, err
		}
		if !found || mapped.MessageID != m.MessageID {
			return f, false, errors.New("message transform identity mismatch")
		}
		if mapped.Omitted != "" {
			return f, true, nil
		}
		m.MessageSeq = mapped.TargetSeq
	}
	if c := f.Conversation; c != nil {
		if t.report.Policy.ExcludeCMD && c.Type == 1 {
			return f, true, nil
		}
		var err error
		c.ReadSeq, err = t.boundary(ctx, c.Channel, c.ReadSeq)
		if err != nil {
			return f, false, err
		}
		c.DeletedToSeq, err = t.boundary(ctx, c.Channel, c.DeletedToSeq)
		if err != nil {
			return f, false, err
		}
	}
	if tail := f.Tail; tail != nil {
		ch, found, err := transformGet[transformedChannel](ctx, t.w, "channel/"+channelTuple(tail.Channel))
		if err != nil {
			return f, false, err
		}
		if found {
			if ch.OriginalLast != tail.LastSeq {
				return f, false, errors.New("transformed source tail mismatch")
			}
			if ch.Retained == 0 {
				return f, true, nil
			}
			tail.LastSeq = ch.Retained
		}
	}
	return f, false, nil
}

// WalkMessageSequenceMappings exports the conversion map as a sidecar for
// client cache/cursor tooling. Require successful BuildTargetRecords first.
func WalkMessageSequenceMappings(ctx context.Context, w Workspace, visit func(MessageSequenceMapping) error) error {
	return w.Walk(ctx, []byte("message-transform/convert/mapping/"), func(row transfer.SpoolRow) error {
		var m MessageSequenceMapping
		if err := UnmarshalState(row.Value, &m); err != nil {
			return err
		}
		return visit(m)
	})
}

// WalkDuplicateChainMappings exports exact source, direct edges and certified
// terminal identities. Source payloads remain in the complete original archive.
func WalkDuplicateChainMappings(ctx context.Context, w Workspace, expected *MessageTransformReport, visit func(MessageSequenceMapping) error) error {
	if expected == nil {
		return errors.New("duplicate chain export requires its prepared report")
	}
	h := sha256.New()
	enc := json.NewEncoder(h)
	var rows uint64
	var terminals uint64
	prefix := "message-transform/convert/"
	err := w.Walk(ctx, []byte(prefix+"chains/"), func(row transfer.SpoolRow) error {
		row.Key = row.Key[len(prefix):]
		if err := enc.Encode(row); err != nil {
			return err
		}
		var m MessageSequenceMapping
		if err := UnmarshalState(row.Value, &m); err != nil {
			return err
		}
		if m.TerminalWinner == nil {
			return errors.New("duplicate chain export has no terminal proof")
		}
		rows++
		key := prefix + "chain-terminals/" + channelTuple(m.Channel) + fmt.Sprintf("/%020d/%020d", m.TerminalWinner.Sequence, m.OriginalSeq)
		if _, found, err := w.Get(ctx, []byte(key)); err != nil {
			return err
		} else if !found {
			return errors.New("duplicate chain terminal index is incomplete")
		}
		return visit(m)
	})
	if err != nil {
		return err
	}
	// The disk-sorted references count distinct terminals without retaining the
	// whole archive in memory. Every reference must match a digest-bound root.
	lastTerminal := ""
	var terminalReferences uint64
	if err := w.Walk(ctx, []byte(prefix+"chain-terminals/"), func(row transfer.SpoolRow) error {
		var key string
		if err := UnmarshalState(row.Value, &key); err != nil {
			return err
		}
		m, found, err := transformGet[MessageSequenceMapping](ctx, w, prefix+key)
		if err != nil {
			return err
		}
		if !found || m.TerminalWinner == nil || key != "chains/"+channelTuple(m.Channel)+fmt.Sprintf("/%020d", m.OriginalSeq) || string(row.Key) != prefix+"chain-terminals/"+channelTuple(m.Channel)+fmt.Sprintf("/%020d/%020d", m.TerminalWinner.Sequence, m.OriginalSeq) {
			return errors.New("duplicate chain terminal index differs from proof")
		}
		terminalKey := string(row.Key[:len(row.Key)-21])
		if terminalKey != lastTerminal {
			terminals++
			lastTerminal = terminalKey
		}
		terminalReferences++
		return nil
	}); err != nil {
		return err
	}
	if rows != expected.ChainRoots || terminalReferences != rows || terminals != expected.ChainTerminals || (rows > 0 && hex.EncodeToString(h.Sum(nil)) != expected.ChainSHA256) {
		return errors.New("duplicate chain export differs from prepared proof")
	}
	return nil
}
