package migration

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
)

// QuarantineRow authorizes one exact original primary, never a wildcard or a
// best-effort skip. SHA256 covers json.Marshal(Row), including all raw columns.
type QuarantineRow struct {
	NodeID uint64 `json:"node_id"`
	Shard  int    `json:"shard"`
	Key    []byte `json:"key"`
	SHA256 string `json:"sha256"`
	Reason string `json:"reason"`
}

// QuarantineFacts is a fixed-reader classification of an approved source row.
// A message position belongs to its physical owner; no replacement identity or
// payload is synthesized from the malformed channel columns.
type QuarantineFacts struct {
	MessageID         uint64
	Owner             uint64
	Sequence          uint64
	UnresolvedChannel uint64
}

// QuarantineDecoder proves the narrow reason and exact index dependencies.
// It must reject healthy primaries and unknown index shapes.
type QuarantineDecoder interface {
	InspectQuarantine(Row, string) (QuarantineFacts, error)
	QuarantineIndexPrimary(Row) ([]byte, error)
}

type QuarantinePosition struct {
	Entry     QuarantineRow `json:"entry"`
	Owner     uint64        `json:"owner,string"`
	Sequence  uint64        `json:"sequence,string"`
	MessageID uint64        `json:"message_id,string"`
}

// QuarantineReport binds primary and dependent rows while keeping the complete
// raw capture intact. Positions include only owners with surviving source rows.
type QuarantineReport struct {
	Policy             []QuarantineRow      `json:"policy"`
	PhysicalRows       map[string]uint64    `json:"physical_rows_by_reason"`
	SHA256             string               `json:"sha256"`
	Positions          []QuarantinePosition `json:"positions,omitempty"`
	MaxSourceMessageID uint64               `json:"max_source_message_id,string"`
}

// quarantineWorkspace is a business interpretation view. Capture/export always
// retain the underlying original rows; no source or capture bytes are edited.
type quarantineWorkspace struct {
	Workspace
	hidden     map[string]bool
	report     *QuarantineReport
	unresolved []uint64
}

func (w *quarantineWorkspace) Get(ctx context.Context, key []byte) ([]byte, bool, error) {
	if w.hidden[string(key)] {
		return nil, false, nil
	}
	return w.Workspace.Get(ctx, key)
}
func (w *quarantineWorkspace) Walk(ctx context.Context, prefix []byte, visit func(transfer.SpoolRow) error) error {
	return w.Workspace.Walk(ctx, prefix, func(r transfer.SpoolRow) error {
		if w.hidden[string(r.Key)] {
			return nil
		}
		return visit(r)
	})
}
func quarantineRawWorkspace(w Workspace) Workspace {
	if q, ok := w.(*quarantineWorkspace); ok {
		return q.Workspace
	}
	return w
}

func validateQuarantinePolicy(plan Plan) error {
	if len(plan.Quarantine) == 0 {
		return nil
	}
	if len(plan.Quarantine) > 4096 {
		return errors.New("quarantine exceeds bounded exact-row inventory")
	}
	nodes := map[uint64]int{}
	for _, n := range plan.Sources {
		nodes[n.NodeID] = n.ShardCount
	}
	seen := map[string]bool{}
	for _, q := range plan.Quarantine {
		digest, err := hex.DecodeString(q.SHA256)
		if err != nil || len(digest) != 32 || hex.EncodeToString(digest) != q.SHA256 || q.NodeID == 0 || q.Shard < 0 || q.Shard >= nodes[q.NodeID] || len(q.Key) != 20 {
			return errors.New("invalid exact quarantine row binding")
		}
		switch q.Reason {
		case "invalid_message_channel":
			if plan.Messages == nil || !plan.Messages.CompactSequences {
				return errors.New("message quarantine requires explicit sequence compaction")
			}
		case "inconsistent_cmd_conversation":
			if plan.Messages == nil || !plan.Messages.ExcludeCMD {
				return errors.New("CMD quarantine requires explicit CMD exclusion")
			}
		case "unresolved_allowlist_channel":
		default:
			return errors.New("unsupported quarantine reason")
		}
		key := string(sourceRowKey(q.NodeID, Row{Shard: q.Shard, Key: q.Key}))
		if seen[key] {
			return errors.New("duplicate quarantine row binding")
		}
		seen[key] = true
	}
	return nil
}

func prepareQuarantine(ctx context.Context, plan Plan, capture SourceCapture, w Workspace, decoder OriginalDecoder) (Workspace, error) {
	if err := validateQuarantinePolicy(plan); err != nil {
		return w, err
	}
	if len(plan.Quarantine) == 0 {
		return w, nil
	}
	d, ok := decoder.(QuarantineDecoder)
	if !ok {
		return w, errors.New("source reader cannot prove quarantine reasons")
	}
	view := &quarantineWorkspace{Workspace: w, hidden: map[string]bool{}, report: &QuarantineReport{Policy: plan.Quarantine, PhysicalRows: map[string]uint64{}}}
	type boundRow struct {
		Key    string
		SHA256 string
		Reason string
	}
	bound := []boundRow{}
	add := func(node uint64, r Row, reason string) {
		key := string(sourceRowKey(node, r))
		if view.hidden[key] {
			return
		}
		view.hidden[key] = true
		raw, _ := json.Marshal(r)
		bound = append(bound, boundRow{key, diagnosticSHA(raw), reason})
		view.report.PhysicalRows[reason]++
	}
	// Only affected owners are held in memory, bounded by the plan row count.
	ownerKey := func(node, owner uint64) string { return fmt.Sprintf("%020d/%020d", node, owner) }
	live := map[string]bool{}
	positions := []QuarantinePosition{}
	for _, q := range plan.Quarantine {
		key := sourceRowKey(q.NodeID, Row{Shard: q.Shard, Key: q.Key})
		raw, found, err := w.Get(ctx, key)
		if err != nil {
			return w, err
		}
		if !found {
			return w, errors.New("approved quarantine row is absent")
		}
		var r Row
		if err := json.Unmarshal(raw, &r); err != nil {
			return w, err
		}
		exact, _ := json.Marshal(r)
		if string(sourceRowKey(q.NodeID, r)) != string(key) || diagnosticSHA(exact) != q.SHA256 {
			return w, errors.New("approved quarantine row digest or identity changed")
		}
		facts, err := d.InspectQuarantine(r, q.Reason)
		if err != nil {
			return w, err
		}
		add(q.NodeID, r, q.Reason)
		if facts.UnresolvedChannel != 0 {
			view.unresolved = append(view.unresolved, facts.UnresolvedChannel)
		}
		if facts.Sequence != 0 {
			positions = append(positions, QuarantinePosition{q, facts.Owner, facts.Sequence, facts.MessageID})
			live[ownerKey(q.NodeID, facts.Owner)] = false
			view.report.MaxSourceMessageID = max(view.report.MaxSourceMessageID, facts.MessageID)
		}
	}
	// Prove which affected owners still have real messages, and hide only stored
	// indexes that point exactly at an approved primary (not a duplicate winner).
	for _, node := range capture.Nodes {
		err := walkSourceRows(ctx, w, node.NodeID, func(r Row) error {
			if r.Table == "Message" && r.Kind == Primary && !view.hidden[string(sourceRowKey(node.NodeID, r))] {
				k := ownerKey(node.NodeID, r.Owner)
				if _, affected := live[k]; affected {
					live[k] = true
				}
			}
			if r.Kind != Index && r.Kind != SecondaryIndex {
				return nil
			}
			key, err := d.QuarantineIndexPrimary(r)
			if err != nil {
				return err
			}
			if len(key) > 0 && view.hidden[string(sourceRowKey(node.NodeID, Row{Shard: r.Shard, Key: key}))] {
				add(node.NodeID, r, "quarantined_primary_index")
			}
			return nil
		})
		if err != nil {
			return w, err
		}
	}
	for _, p := range positions {
		if live[ownerKey(p.Entry.NodeID, p.Owner)] {
			view.report.Positions = append(view.report.Positions, p)
		}
	}
	// An owner with no valid source messages has no business tail to import.
	// Still require the reader's exact tail shape and retain its original bytes.
	for _, node := range capture.Nodes {
		if err := walkSourceRows(ctx, w, node.NodeID, func(r Row) error {
			if r.Table != "Message" || r.Kind != Other {
				return nil
			}
			id, err := decoder.Identify(r)
			if err != nil {
				return err
			}
			if survives, affected := live[ownerKey(node.NodeID, id.ChannelHash)]; affected && !survives {
				add(node.NodeID, r, "quarantined_owner_tail")
			}
			return nil
		}); err != nil {
			return w, err
		}
	}
	sort.Slice(bound, func(i, j int) bool { return bound[i].Key < bound[j].Key })
	raw, err := json.Marshal(bound)
	if err != nil {
		return w, err
	}
	view.report.SHA256 = diagnosticSHA(raw)
	return view, nil
}

func validateQuarantineCatalog(ctx context.Context, w Workspace) error {
	q, ok := w.(*quarantineWorkspace)
	if !ok {
		return nil
	}
	for _, owner := range q.unresolved {
		_, found, err := w.Get(ctx, []byte(fmt.Sprintf("catalog/channel/%016x", owner)))
		if err != nil {
			return err
		}
		if found {
			return errors.New("quarantined allowlist actually has a resolvable channel")
		}
	}
	return nil
}

// addQuarantinePositions rereads exact raw rows for both conversion and
// verification. Only an already-selected physical owner supplies channel
// identity; an all-quarantined owner cannot create a synthetic target channel.
func addQuarantinePositions(ctx context.Context, selection SourceSelection, source, transformed Workspace, decoder BusinessDecoder, p *dedupePlanner, report *MessageTransformReport) error {
	q := selection.Quarantine
	if q == nil {
		return nil
	}
	if empty, ok := decoder.(*emptyChannelDecoder); ok {
		decoder = empty.OriginalDecoder
	}
	d, ok := decoder.(QuarantineDecoder)
	if !ok {
		return errors.New("message quarantine requires the original evidence decoder")
	}
	rawSource := quarantineRawWorkspace(source)
	for _, entry := range q.Policy {
		raw, found, err := rawSource.Get(ctx, sourceRowKey(entry.NodeID, Row{Shard: entry.Shard, Key: entry.Key}))
		if err != nil {
			return err
		}
		if !found {
			return errors.New("quarantine original missing during transform")
		}
		var r Row
		if err := json.Unmarshal(raw, &r); err != nil {
			return err
		}
		exact, _ := json.Marshal(r)
		if diagnosticSHA(exact) != entry.SHA256 {
			return errors.New("quarantine original changed during transform")
		}
		f, err := d.InspectQuarantine(r, entry.Reason)
		if err != nil {
			return err
		}
		report.MaxSourceMessageID = max(report.MaxSourceMessageID, f.MessageID)
		if f.Sequence == 0 {
			continue
		}
		node, selected, err := transformGet[uint64](ctx, transformed, fmt.Sprintf("input/node/%020d", f.Owner))
		if err != nil {
			return err
		}
		if !selected || node != entry.NodeID {
			continue
		}
		// This source row is an omitted position, never a decoded message. Raw
		// fields are not admitted to dedupe groups or native target records.
		state, err := MarshalState(r)
		if err != nil {
			return err
		}
		ref := DedupeMessage{NodeID: 1, Owner: f.Owner, MessageEvidence: MessageEvidence{ID: f.MessageID, Sequence: f.Sequence, SHA256: diagnosticSHA(state)}}
		if err := p.put(dedupeMessageKey("message", ref), ref); err != nil {
			return err
		}
		if err := p.put(dedupeMessageKey("quarantine", ref), entry.Reason); err != nil {
			return err
		}
		report.Original++
	}
	return p.b.flush()
}
