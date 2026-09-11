package migration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
)

// ConversationSelection records source-copy reduction, not distinct target
// memberships. SHA256 includes every candidate's original bytes and lookup.
type ConversationSelection struct {
	Groups            uint64 `json:"groups"`
	DuplicateGroups   uint64 `json:"duplicate_groups"`
	ShadowedRows      uint64 `json:"shadowed_rows"`
	MaxLeaderChatRows uint64 `json:"max_leader_chat_rows"`
	// UsersOverOriginalLimit counts preserved lists exceeding the real v2 cap.
	UsersOverOriginalLimit uint64 `json:"users_over_original_limit,omitempty"`
	// RecoveredConflicts counts exact, evidence-bound indexed-state choices.
	RecoveredConflicts uint64 `json:"recovered_conflicts,omitempty"`
	SHA256             string `json:"sha256"`
}

type conversationLookupRow struct {
	deviceLookupRow
	IndexedKey      []byte `json:"indexed_key"`
	StateSHA        string `json:"state_sha256"`
	IndexedStateSHA string `json:"indexed_state_sha256"`
	UpdatedAt       int64  `json:"updated_at"`
	HasUpdatedAt    bool   `json:"has_updated_at"`
	Type            uint8  `json:"type"`
	ActiveLeader    bool   `json:"active_leader"`
}

func conversationLookupBase(capture string, p *MetadataPolicy) string {
	data, _ := json.Marshal(p)
	return "conversation-lookup/v2/" + capture + "/" + diagnosticSHA(data) + "/"
}

// readIndexedSourceRow follows an original index from the immutable capture;
// it never rebuilds a missing index from whichever primary is convenient.
func readIndexedSourceRow(ctx context.Context, w Workspace, node uint64, shard int, key []byte, d SourceIndexDecoder, shards int) (Row, SourceIndexEntry, error) {
	load := func(key []byte) (Row, error) {
		data, ok, err := w.Get(ctx, sourceRowKey(node, Row{Shard: shard, Key: key}))
		if err != nil {
			return Row{}, err
		}
		if !ok {
			return Row{}, errors.New("original source index or its primary is missing")
		}
		var r Row
		if err := json.Unmarshal(data, &r); err != nil {
			return r, err
		}
		if r.Shard != shard || !bytes.Equal(r.Key, key) {
			return r, errors.New("source index/primary placement mismatch")
		}
		return r, nil
	}
	index, err := load(key)
	if err != nil {
		return Row{}, SourceIndexEntry{}, err
	}
	facts, err := d.DescribeIndexes(index, RecordIdentity{}, shards)
	if err != nil {
		return Row{}, SourceIndexEntry{}, err
	}
	if facts.Actual == nil || len(facts.Actual.PrimaryKey) == 0 {
		return Row{}, SourceIndexEntry{}, errors.New("source index has no original primary reference")
	}
	r, err := load(facts.Actual.PrimaryKey)
	return r, *facts.Actual, err
}

// reduceConversationLookups preserves the active Leader's list version only
// when its state equals exact lookup. Replica comparison later checks exact
// states, while source physical IDs and times remain fully archived.
func reduceConversationLookups(ctx context.Context, capture SourceCapture, w Workspace, decoder RecordDecoder, p *MetadataPolicy) (*ConversationSelection, error) {
	if p == nil || p.ConversationLookup == "" {
		return nil, nil
	}
	indexDecoder, ok := decoder.(SourceIndexDecoder)
	if !ok {
		return nil, errors.New("conversation selection requires original index decoding")
	}
	report := &ConversationSelection{}
	base := conversationLookupBase(capture.Digest, p)
	b := &captureBatch{ctx: ctx, workspace: w}
	shards := map[uint64]int{}
	for _, a := range capture.Authority {
		shards[a.NodeID] = a.ShardCount
	}
	slots := map[uint32]SourceSlot{}
	for _, s := range capture.Nodes[0].Config.Slots {
		slots[s.ID] = s
	}
	for _, node := range capture.Nodes {
		if shards[node.NodeID] < 1 {
			return nil, errors.New("conversation lookup requires captured shard inventory")
		}
		err := walkSourceRows(ctx, w, node.NodeID, func(row Row) error {
			if row.Table != "Conversation" || row.Kind != Primary {
				return nil
			}
			id, err := decoder.Identify(row)
			if err != nil {
				return err
			}
			d, err := decoder.Describe(row, id)
			if err != nil {
				return err
			}
			if d.ConversationLookup == nil {
				return errors.New("conversation has no original lookup facts")
			}
			// Placement was checked by full source-index validation before selection.
			pointed, _, err := readIndexedSourceRow(ctx, w, node.NodeID, row.Shard, d.ConversationLookup.IndexKey, indexDecoder, shards[node.NodeID])
			if err != nil {
				return err
			}
			pid, err := decoder.Identify(pointed)
			if err != nil {
				return err
			}
			pd, err := decoder.Describe(pointed, pid)
			if err != nil {
				return err
			}
			if pointed.Table != "Conversation" || pointed.Kind != Primary || pd.Key != d.Key || pd.ConversationLookup == nil {
				return errors.New("conversation index changes logical identity")
			}
			slot := slots[crc32.ChecksumIEEE([]byte(id.UID))%capture.Nodes[0].Config.SlotCount]
			data, err := json.Marshal(row)
			if err != nil {
				return err
			}
			ref := conversationLookupRow{deviceLookupRow: deviceLookupRow{SourceKey: sourceRowKey(node.NodeID, row), ID: row.ID, SHA256: diagnosticSHA(data)}, IndexedKey: sourceRowKey(node.NodeID, pointed), StateSHA: diagnosticSHA(d.ConversationLookup.State), IndexedStateSHA: diagnosticSHA(pd.ConversationLookup.State), UpdatedAt: d.ConversationLookup.UpdatedAt, HasUpdatedAt: d.ConversationLookup.HasUpdatedAt, Type: d.ConversationLookup.Type, ActiveLeader: node.NodeID == slot.Leader}
			data, err = json.Marshal(ref)
			if err != nil {
				return err
			}
			if err := b.add(transfer.SpoolRow{Key: []byte(fmt.Sprintf("%srows/%020d/%s/%020d", base, node.NodeID, d.Key, row.ID)), Value: data}); err != nil {
				return err
			}
			if ref.ActiveLeader && d.ConversationLookup.Type == 0 {
				return b.add(transfer.SpoolRow{Key: []byte(fmt.Sprintf("%schat-list/%020d/%s/%020d", base, node.NodeID, IdentityKey(id.UID), row.ID)), Value: []byte{1}})
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	if err := b.flush(); err != nil {
		return nil, err
	}
	if err := checkConversationListLimit(ctx, w, base, p, report); err != nil {
		return nil, err
	}
	prefix := []byte(base + "rows/")
	var group string
	var first, indexed, recent, highest conversationLookupRow
	var groupCount uint64
	var ambiguous bool
	groupHash := sha256.New()
	h := sha256.New()
	enc := json.NewEncoder(h)
	flush := func() error {
		if group == "" {
			return nil
		}
		if indexed.ID == 0 {
			return errors.New("conversation exact lookup is absent from its primary group")
		}
		chosen := indexed
		if first.ActiveLeader {
			if first.Type == 1 {
				recent, ambiguous = highest, false
			} // original CMD sync uses GetConversationsByType
			if ambiguous || recent.StateSHA != indexed.StateSHA {
				accepted, err := recoverIndexedConversation(p, group, indexed.SHA256, hex.EncodeToString(groupHash.Sum(nil)))
				if err != nil {
					return err
				}
				if !accepted {
					return errors.New("active Leader conversation list and exact lookup disagree")
				}
				report.RecoveredConflicts++
			} else {
				chosen = recent
			}
		}
		report.Groups++
		if groupCount > 1 {
			report.DuplicateGroups++
			report.ShadowedRows += groupCount - 1
		}
		data, err := json.Marshal(chosen.deviceLookupRow)
		if err != nil {
			return err
		}
		return b.add(transfer.SpoolRow{Key: []byte(base + "chosen/" + group), Value: data})
	}
	err := w.Walk(ctx, prefix, func(row transfer.SpoolRow) error {
		suffix := string(row.Key[len(prefix):])
		split := strings.LastIndexByte(suffix, '/')
		if split < 0 {
			return errors.New("invalid conversation lookup group")
		}
		var ref conversationLookupRow
		if err := json.Unmarshal(row.Value, &ref); err != nil {
			return err
		}
		if suffix[split+1:] != fmt.Sprintf("%020d", ref.ID) {
			return errors.New("conversation lookup order differs from primary ID")
		}
		next := suffix[:split]
		if next != group {
			if err := flush(); err != nil {
				return err
			}
			group, first, recent, indexed, groupCount, ambiguous = next, ref, ref, conversationLookupRow{}, 0, false
			groupHash.Reset()
		}
		if ref.Type != first.Type || ref.ActiveLeader != first.ActiveLeader || ref.IndexedStateSHA != first.IndexedStateSHA || !bytes.Equal(ref.IndexedKey, first.IndexedKey) {
			return errors.New("conversation group has conflicting lookup evidence")
		}
		if bytes.Equal(ref.SourceKey, ref.IndexedKey) {
			indexed = ref
		}
		highest = ref // disk order is the original ascending physical ID
		if (ref.HasUpdatedAt && !recent.HasUpdatedAt) || (ref.HasUpdatedAt == recent.HasUpdatedAt && ref.UpdatedAt > recent.UpdatedAt) {
			recent = ref
			ambiguous = false
		} else if ref.HasUpdatedAt == recent.HasUpdatedAt && ref.UpdatedAt == recent.UpdatedAt && ref.StateSHA != recent.StateSHA {
			ambiguous = true
		}
		groupCount++
		fmt.Fprintln(groupHash, ref.SHA256)
		return enc.Encode(row)
	})
	if err != nil {
		return nil, err
	}
	if err := flush(); err != nil {
		return nil, err
	}
	if report.RecoveredConflicts != uint64(len(p.ConversationRecoveries)) {
		return nil, errors.New("approved conversation recovery was not applied")
	}
	if err := b.flush(); err != nil {
		return nil, err
	}
	report.SHA256 = hex.EncodeToString(h.Sum(nil))
	return report, nil
}

func keepsConversation(ctx context.Context, w Workspace, base string, node uint64, row Row, key string) (bool, error) {
	data, found, err := w.Get(ctx, []byte(fmt.Sprintf("%schosen/%020d/%s", base, node, key)))
	if err != nil {
		return false, err
	}
	if !found {
		return false, errors.New("conversation selection is missing")
	}
	var ref deviceLookupRow
	if err := json.Unmarshal(data, &ref); err != nil {
		return false, err
	}
	if ref.ID != row.ID {
		return false, nil
	}
	data, err = json.Marshal(row)
	if err != nil {
		return false, err
	}
	if ref.SHA256 != diagnosticSHA(data) || !bytes.Equal(ref.SourceKey, sourceRowKey(node, row)) {
		return false, errors.New("chosen conversation differs from its original capture")
	}
	return true, nil
}

// checkConversationListLimit counts before original list deduplication. Pending
// intents are added after exact persisted-row recovery and count only once.
func checkConversationListLimit(ctx context.Context, w Workspace, base string, p *MetadataPolicy, report *ConversationSelection) error {
	var user string
	var count uint64
	report.UsersOverOriginalLimit = 0
	return w.Walk(ctx, []byte(base+"chat-list/"), func(row transfer.SpoolRow) error {
		key := string(row.Key)
		next := key[:strings.LastIndexByte(key, '/')]
		if next != user {
			user, count = next, 0
		}
		count++
		if count > p.ConversationListLimit && !p.PreserveAllConversations {
			return errors.New("original conversation list exceeds its configured pre-deduplication limit")
		}
		if count == p.ConversationListLimit+1 {
			report.UsersOverOriginalLimit++
		}
		if count > report.MaxLeaderChatRows {
			report.MaxLeaderChatRows = count
		}
		return nil
	})
}

func includePendingConversationList(ctx context.Context, capture SourceCapture, w Workspace, decoder RecordDecoder, p *MetadataPolicy, report *ConversationSelection) error {
	if report == nil {
		return nil
	}
	base := conversationLookupBase(capture.Digest, p)
	leaders := map[uint32]uint64{}
	for _, s := range capture.Nodes[0].Config.Slots {
		leaders[s.ID] = s.Leader
	}
	b := &captureBatch{ctx: ctx, workspace: w}
	if err := WalkSelectedSources(ctx, w, func(rec SelectedRecord) error {
		if rec.Row.Table != "PendingConversation" {
			return nil
		}
		d, err := decoder.Describe(rec.Row, rec.Identity)
		if err != nil {
			return err
		}
		if d.ConversationLookup == nil {
			return errors.New("pending conversation has no original type")
		}
		if d.ConversationLookup.Type != 0 {
			return nil
		}
		leader := leaders[crc32.ChecksumIEEE([]byte(rec.Identity.UID))%capture.Nodes[0].Config.SlotCount]
		return b.add(transfer.SpoolRow{Key: []byte(fmt.Sprintf("%schat-list/%020d/%s/pending-%s", base, leader, IdentityKey(rec.Identity.UID), d.Key)), Value: []byte{1}})
	}); err != nil {
		return err
	}
	if err := b.flush(); err != nil {
		return err
	}
	return checkConversationListLimit(ctx, w, base, p, report)
}
