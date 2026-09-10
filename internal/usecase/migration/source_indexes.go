package migration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
)

// SourceIndexEntry describes an original operational lookup, independent of
// target indexes. PrimaryKey addresses an aggregated captured source row.
type SourceIndexEntry struct {
	// LatestMessageSeq is limited to the original message-ID index, whose
	// last write points to the greatest sequence within the same channel.
	LatestMessageSeq uint64 `json:"latest_message_seq,omitempty"`
	Key              []byte `json:"key"`
	Value            []byte `json:"value"`
	PrimaryKey       []byte `json:"primary_key,omitempty"`
	// AllowAbsentPrimary is restricted to original message lookups that return
	// not found for primary rows removed by the original truncation writer.
	AllowAbsentPrimary bool `json:"allow_absent_primary,omitempty"`
	// NodeUnique rejects duplicate message-ID lookups across a node's shards,
	// including a stale index that would shadow a live row in a later shard.
	NodeUnique bool `json:"node_unique,omitempty"`
	// SenderKey and SenderSeq describe the original unread lookup, which takes
	// the largest indexed sequence even if its primary row was truncated.
	SenderKey []byte `json:"sender_key,omitempty"`
	SenderSeq uint64 `json:"sender_seq,omitempty"`
}

// SourceIndexFacts distinguishes required business lookup entries from
// shape-checked legacy indexes whose absence or stale values are intentional.
type SourceIndexFacts struct {
	Expected []SourceIndexEntry
	Actual   *SourceIndexEntry
}

// SourceIndexDecoder supplies fixed-version index facts without deciding
// target placement or rebuilding missing original business lookup evidence.
type SourceIndexDecoder interface {
	IdentityDecoder
	DescribeIndexes(Row, RecordIdentity, int) (SourceIndexFacts, error)
}

// ValidateSourceIndexes checks the original read paths before conversion, and
// again when rebuilding from an archive. Both directions of the disk-backed
// join must match; target index reconstruction cannot certify a corrupt source.
func ValidateSourceIndexes(ctx context.Context, capture SourceCapture, sources []NodeOptions, w Workspace, decoder SourceIndexDecoder, policies ...*MessagePolicy) error {
	return validateSourceIndexes(ctx, capture, sources, w, decoder, nil, policies...)
}

func validateSourceIndexes(ctx context.Context, capture SourceCapture, sources []NodeOptions, w Workspace, decoder SourceIndexDecoder, metadata *MetadataPolicy, policies ...*MessagePolicy) error {
	if err := validateMetadataPolicy(metadata); err != nil {
		return err
	}
	if ctx == nil || capture.Digest == "" || w == nil || decoder == nil {
		return errors.New("source index validation requires a complete capture")
	}
	if len(policies) > 1 {
		return errors.New("one message index policy is required")
	}
	var policy *MessagePolicy
	if len(policies) == 1 {
		policy = policies[0]
	}
	if err := validateMessagePolicy(policy); err != nil {
		return err
	}
	shards := make(map[uint64]int, len(sources))
	for _, source := range sources {
		if source.ShardCount < 1 || source.ShardCount > 1024 || shards[source.NodeID] != 0 {
			return errors.New("invalid source index shard inventory")
		}
		shards[source.NodeID] = source.ShardCount
	}
	base := "source-index/" + capture.Digest + "/"
	if metadata != nil {
		data, _ := json.Marshal(metadata)
		base += diagnosticSHA(data) + "/"
	}
	if policy != nil {
		data, _ := json.Marshal(policy)
		base += diagnosticSHA(data) + "/"
	}
	b := &captureBatch{ctx: ctx, workspace: w}
	indexKey := func(phase string, node uint64, shard int, key []byte) []byte {
		return []byte(fmt.Sprintf("%s%s/%020d/%04d/%x", base, phase, node, shard, key))
	}
	for _, node := range capture.Nodes {
		count := shards[node.NodeID]
		if count == 0 {
			return errors.New("source index node is missing from plan")
		}
		err := walkSourceRows(ctx, w, node.NodeID, func(row Row) error {
			id, err := decoder.Identify(row)
			if err != nil {
				return err
			}
			id, err = resolveIdentity(ctx, w, id)
			if err != nil {
				return err
			}
			facts, err := decoder.DescribeIndexes(row, id, count)
			if err != nil {
				return err
			}
			for _, entry := range facts.Expected {
				if metadata != nil && metadata.ConversationLookup != "" && row.Table == "Conversation" {
					pointed, actual, err := readIndexedSourceRow(ctx, w, node.NodeID, row.Shard, entry.Key, decoder, count)
					if err != nil {
						return err
					}
					if pointed.Table != "Conversation" || pointed.Kind != Primary {
						return errors.New("conversation index points outside its table")
					}
					targetID, err := decoder.Identify(pointed)
					if err != nil {
						return err
					}
					expected, err := decoder.DescribeIndexes(pointed, targetID, count)
					if err != nil {
						return err
					}
					if len(expected.Expected) != 1 || !bytes.Equal(expected.Expected[0].Key, entry.Key) || !bytes.Equal(expected.Expected[0].Value, actual.Value) {
						return errors.New("conversation index points to a different logical conversation")
					}
					entry.Value = bytes.Clone(actual.Value)
				}
				if policy == nil || !policy.KeepLatestDuplicates {
					entry.LatestMessageSeq = 0
				}
				data, err := json.Marshal(entry)
				if err != nil {
					return err
				}
				expectedKey := indexKey("expected", node.NodeID, row.Shard, entry.Key)
				if policy != nil && policy.KeepLatestDuplicates && entry.LatestMessageSeq != 0 {
					if row.Table != "Message" || row.Kind != Primary || len(entry.Value) != 16 || len(entry.Key) != 14 {
						return errors.New("invalid latest-message source index")
					}
					expectedKey = append(indexKey("message-latest", node.NodeID, row.Shard, entry.Key), []byte(fmt.Sprintf("/%020d", entry.LatestMessageSeq))...)
				}
				if err := b.add(transfer.SpoolRow{Key: expectedKey, Value: data}); err != nil {
					return err
				}
				if len(entry.SenderKey) > 0 {
					key := append(indexKey("sender-seq", node.NodeID, row.Shard, entry.SenderKey), []byte(fmt.Sprintf("/%020d", entry.SenderSeq))...)
					data, err := json.Marshal(entry.SenderSeq)
					if err != nil {
						return err
					}
					if err := b.add(transfer.SpoolRow{Key: key, Value: data}); err != nil {
						return err
					}
				}
			}
			if entry := facts.Actual; entry != nil {
				actual := sourceIndexRecord{NodeID: node.NodeID, Shard: row.Shard, Table: row.Table, Entry: *entry}
				data, err := json.Marshal(actual)
				if err != nil {
					return err
				}
				if err := b.add(transfer.SpoolRow{Key: indexKey("actual", node.NodeID, row.Shard, entry.Key), Value: data}); err != nil {
					return err
				}
				if entry.NodeUnique {
					identity, err := json.Marshal(struct {
						Shard   int
						Primary []byte
					}{row.Shard, entry.PrimaryKey})
					if err != nil {
						return err
					}
					if err := b.add(transfer.SpoolRow{Key: []byte(fmt.Sprintf("%sunique/%020d/%x", base, node.NodeID, entry.Key)), Value: identity}); err != nil {
						return err
					}
				}
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("source node %d index validation: %w", node.NodeID, err)
		}
	}
	if err := b.flush(); err != nil {
		return fmt.Errorf("source index conflict: %w", err)
	}
	if err := reduceLatestMessageIndexes(ctx, w, b, base); err != nil {
		return err
	}
	// Reduce sorted original live sender positions with one group in memory.
	senderPrefix := []byte(base + "sender-seq/")
	var lastGroup string
	var lastPosition []byte
	flushSender := func() error {
		if lastGroup == "" {
			return nil
		}
		return b.add(transfer.SpoolRow{Key: []byte(base + "sender-max/" + lastGroup), Value: lastPosition})
	}
	if err := w.Walk(ctx, senderPrefix, func(row transfer.SpoolRow) error {
		suffix := string(row.Key[len(senderPrefix):])
		split := strings.LastIndexByte(suffix, '/')
		if split < 0 {
			return errors.New("invalid source sender index group")
		}
		group := suffix[:split]
		if group != lastGroup {
			if err := flushSender(); err != nil {
				return err
			}
			lastGroup = group
		}
		lastPosition = bytes.Clone(row.Value)
		return nil
	}); err != nil {
		return err
	}
	if err := flushSender(); err != nil {
		return err
	}
	if err := b.flush(); err != nil {
		return err
	}
	return validateCapturedIndexJoin(ctx, w, base)
}

// validateCapturedIndexJoin compares both directions of the original index
// contract. Native spools merge sorted prefixes; other workspaces retain the
// point-lookup path. Unmatched rows still use the interpreted quarantine view.
func validateCapturedIndexJoin(ctx context.Context, w Workspace, base string) error {
	indexKey := func(phase string, node uint64, shard int, key []byte) []byte {
		return []byte(fmt.Sprintf("%s%s/%020d/%04d/%x", base, phase, node, shard, key))
	}
	expectedPrefix, actualPrefix := []byte(base+"expected/"), []byte(base+"actual/")
	checkExpected := func(expectedData, actualData []byte) error {
		var actual sourceIndexRecord
		if err := json.Unmarshal(actualData, &actual); err != nil {
			return err
		}
		var expected SourceIndexEntry
		if err := json.Unmarshal(expectedData, &expected); err != nil {
			return err
		}
		if !bytes.Equal(expected.Value, actual.Entry.Value) {
			return fmt.Errorf("source %s index points to a different primary row", actual.Table)
		}
		return nil
	}
	checkUnexpected := func(row transfer.SpoolRow) error {
		var actual sourceIndexRecord
		if err := json.Unmarshal(row.Value, &actual); err != nil {
			return err
		}
		if len(actual.Entry.SenderKey) > 0 {
			data, found, err := w.Get(ctx, indexKey("sender-max", actual.NodeID, actual.Shard, actual.Entry.SenderKey))
			if err != nil {
				return err
			}
			if found {
				var last uint64
				if err := json.Unmarshal(data, &last); err != nil {
					return err
				}
				if last > actual.Entry.SenderSeq {
					return nil
				}
			}
		}
		if actual.Entry.AllowAbsentPrimary {
			_, exists, err := w.Get(ctx, sourceRowKey(actual.NodeID, Row{Shard: actual.Shard, Key: actual.Entry.PrimaryKey}))
			if err != nil {
				return err
			}
			if !exists {
				return nil
			}
		}
		return fmt.Errorf("source %s index is orphaned or disagrees with its primary fields", actual.Table)
	}
	// These two derived prefixes already incorporate quarantine exclusions.
	// Only their ordered scan bypasses the wrapper; all business Get checks above
	// keep using w so hidden primaries never reappear during orphan validation.
	if merged, ok := quarantineRawWorkspace(w).(interface {
		WalkMerge(context.Context, []byte, []byte, func(transfer.SpoolRow, transfer.SpoolRow) error) error
	}); ok {
		var unexpectedErr error
		err := merged.WalkMerge(ctx, expectedPrefix, actualPrefix, func(expected, actual transfer.SpoolRow) error {
			switch {
			case len(expected.Key) == 0:
				// Preserve the old expected-pass error precedence without retaining
				// orphan rows or repeating point lookups for matched indexes.
				if unexpectedErr == nil {
					unexpectedErr = checkUnexpected(actual)
				}
				return nil
			case len(actual.Key) == 0:
				return errors.New("source business index is missing")
			default:
				return checkExpected(expected.Value, actual.Value)
			}
		})
		if err != nil {
			return err
		}
		return unexpectedErr
	}
	if err := w.Walk(ctx, expectedPrefix, func(row transfer.SpoolRow) error {
		key := append([]byte(base+"actual/"), row.Key[len(expectedPrefix):]...)
		data, found, err := w.Get(ctx, key)
		if err != nil {
			return err
		}
		if !found {
			return errors.New("source business index is missing")
		}
		return checkExpected(row.Value, data)
	}); err != nil {
		return err
	}
	return w.Walk(ctx, actualPrefix, func(row transfer.SpoolRow) error {
		var actual sourceIndexRecord
		if err := json.Unmarshal(row.Value, &actual); err != nil {
			return err
		}
		_, found, err := w.Get(ctx, indexKey("expected", actual.NodeID, actual.Shard, actual.Entry.Key))
		if err != nil || found {
			return err
		}
		return checkUnexpected(row)
	})
}

type sourceIndexRecord struct {
	NodeID uint64           `json:"node_id"`
	Shard  int              `json:"shard"`
	Table  string           `json:"table"`
	Entry  SourceIndexEntry `json:"entry"`
}

// reduceLatestMessageIndexes verifies original last-write lookup behavior only.
// It does not remove source rows, choose a replica, or ignore stale wrong indexes.
func reduceLatestMessageIndexes(ctx context.Context, w Workspace, b *captureBatch, base string) error {
	prefix := []byte(base + "message-latest/")
	var group string
	var latest SourceIndexEntry
	flush := func() error {
		if group == "" {
			return nil
		}
		data, err := json.Marshal(latest)
		if err != nil {
			return err
		}
		return b.add(transfer.SpoolRow{Key: []byte(base + "expected/" + group), Value: data})
	}
	err := w.Walk(ctx, prefix, func(row transfer.SpoolRow) error {
		suffix := string(row.Key[len(prefix):])
		split := strings.LastIndexByte(suffix, '/')
		if split < 0 {
			return errors.New("invalid latest message index group")
		}
		key := suffix[:split]
		var entry SourceIndexEntry
		if err := json.Unmarshal(row.Value, &entry); err != nil {
			return err
		}
		if key != group {
			if err := flush(); err != nil {
				return err
			}
			group = key
			latest = entry
		} else {
			if len(entry.Value) != 16 || len(latest.Value) != 16 || !bytes.Equal(entry.Value[:8], latest.Value[:8]) || entry.LatestMessageSeq <= latest.LatestMessageSeq {
				return errors.New("duplicate message-ID source index has incomparable primary rows")
			}
			latest = entry
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := flush(); err != nil {
		return err
	}
	return b.flush()
}
