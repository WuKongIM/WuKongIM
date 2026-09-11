package migration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
)

// MetadataPolicy is deliberately narrower than arbitrary duplicate removal.
// Cold-start behavior cannot reconstruct a stopped process's former hot cache.
type MetadataPolicy struct {
	// DeriveUnreadFromBoundaries archives the independent v2 unread scalar and
	// uses native retained-message/read/delete badge math without changing floors.
	DeriveUnreadFromBoundaries bool `json:"derive_unread_from_boundaries,omitempty"`
	// MissingConversations binds explicit fully-read or list-hidden decisions
	// for exact absent conversations.
	MissingConversations []MissingConversationRecovery `json:"missing_conversations,omitempty"`
	// ArchiveUserTimestamps retains original per-node User creation/update
	// times in the archive only. Native v3 users have no equivalent fields.
	ArchiveUserTimestamps bool `json:"archive_user_timestamps,omitempty"`
	// ArchiveEmptyChannels permits only a replica/log-proven pair with no business references.
	ArchiveEmptyChannels bool   `json:"archive_empty_channels,omitempty"`
	DeviceLookup         string `json:"device_lookup"`
	// ConversationLookup retains the active Slot Leader's persisted list view
	// only when exact lookup states agree across every formal Slot replica.
	ConversationLookup string `json:"conversation_lookup,omitempty"`
	// ConversationListLimit is the original deployment's userMaxCount, even
	// when an explicit recovery preserves conversations outside that list.
	ConversationListLimit uint64 `json:"conversation_list_limit,omitempty"`
	// PreserveAllConversations preserves every valid persisted conversation
	// instead of requiring it to fit the original limited list response.
	PreserveAllConversations bool `json:"preserve_all_conversations,omitempty"`
	// ConversationRecoveries authorizes exact original indexed records for
	// conflicting active-Leader list groups; it never synthesizes read state.
	ConversationRecoveries []ConversationStateRecovery `json:"conversation_recoveries,omitempty"`
	// ConversationReplicas binds exact replica disagreements to an operator's
	// original-record choice or archive-only decision; it cannot synthesize state.
	ConversationReplicas []ConversationReplicaRecovery `json:"conversation_replicas,omitempty"`
}

func validateMetadataPolicy(p *MetadataPolicy) error {
	if p != nil && p.DeviceLookup != "v2_cold_start" {
		return errors.New("metadata.device_lookup must be v2_cold_start; omit metadata for strict duplicate rejection")
	}
	if p != nil && ((p.ConversationLookup != "" && p.ConversationLookup != "v2_active_slot") || (p.ConversationLookup == "") != (p.ConversationListLimit == 0) || p.ConversationListLimit > 1000000) {
		return errors.New("conversation metadata requires v2_active_slot and an original conversation_list_limit in 1..1000000")
	}
	if err := validateConversationRecoveryPolicy(p); err != nil {
		return err
	}
	if err := validateConversationReplicaPolicy(p); err != nil {
		return err
	}
	return validateMissingConversations(p)
}

// MetadataSelection binds every original device candidate and chosen lookup.
// Counts include physical source copies; all candidate rows remain archived.
type MetadataSelection struct {
	Policy          MetadataPolicy                `json:"policy"`
	DeviceGroups    uint64                        `json:"device_groups"`
	DuplicateGroups uint64                        `json:"duplicate_device_groups"`
	ShadowedRows    uint64                        `json:"shadowed_device_rows"`
	SHA256          string                        `json:"sha256"`
	Conversations   *ConversationSelection        `json:"conversations,omitempty"`
	ReplicaRecovery *ConversationReplicaSelection `json:"replica_recovery,omitempty"`
}

type deviceLookupRow struct {
	SourceKey []byte `json:"source_key"`
	ID        uint64 `json:"id"`
	SHA256    string `json:"sha256"`
}

func deviceLookupBase(capture string, p *MetadataPolicy) string {
	data, _ := json.Marshal(p)
	return "device-lookup/v1/" + capture + "/" + diagnosticSHA(data) + "/"
}

// reduceDeviceLookups runs after the complete original index validation. It
// sorts references on disk, retains one group in memory, and independently
// requires each row's original UID-index entry before choosing its first ID.
func reduceDeviceLookups(ctx context.Context, capture SourceCapture, w Workspace, decoder RecordDecoder, policy *MetadataPolicy) (*MetadataSelection, error) {
	if err := validateMetadataPolicy(policy); err != nil {
		return nil, err
	}
	if policy == nil {
		return nil, nil
	}
	pins := map[string]MissingConversationRecovery{}
	archived := map[string]bool{}
	for _, r := range policy.ConversationReplicas {
		if r.ArchiveOnly {
			archived[r.LogicalKey] = true
		}
	}
	for _, r := range policy.MissingConversations {
		if r.CaptureDigest != capture.Digest {
			return nil, errors.New("missing conversation capture differs from approved decision")
		}
		pins[r.UIDSHA256+"/"+r.ChannelSHA256] = r
	}
	report := &MetadataSelection{Policy: *policy}
	base := deviceLookupBase(capture.Digest, policy)
	b := &captureBatch{ctx: ctx, workspace: w}
	for _, node := range capture.Nodes {
		if err := walkSourceRows(ctx, w, node.NodeID, func(row Row) error {
			// Check every physical replica, including shadowed rows and pending intents.
			if len(pins) > 0 && (row.Table == "PendingConversation" || (row.Table == "Conversation" && row.Kind == Primary)) {
				id, err := decoder.Identify(row)
				if err != nil {
					return err
				}
				if pin, ok := pins[missingConversationKey(id.UID, id.Channel)]; ok {
					d, err := decoder.Describe(row, id)
					if err != nil {
						return err
					}
					if row.Table == "PendingConversation" || pin.Visibility != "hidden_until_new_message" || !archived[d.Key] {
						return errors.New("approved missing conversation exists in original rows or pending intents")
					}
				}
			}
			if row.Table != "Device" || row.Kind != Primary {
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
			if len(d.DeviceUIDIndex) == 0 || d.Key == "" || row.ID == 0 {
				return errors.New("device has no proven original cold-login lookup")
			}
			indexKey := sourceRowKey(node.NodeID, Row{Shard: row.Shard, Key: d.DeviceUIDIndex})
			data, found, err := w.Get(ctx, indexKey)
			if err != nil {
				return err
			}
			var index Row
			if !found {
				return errors.New("original device UID index is missing")
			}
			if err := json.Unmarshal(data, &index); err != nil {
				return err
			}
			if index.Table != "Device" || index.Kind != SecondaryIndex || index.Shard != row.Shard || !bytes.Equal(index.Key, d.DeviceUIDIndex) || len(index.Value) != 0 {
				return errors.New("original device UID index disagrees with its primary")
			}
			data, err = json.Marshal(row)
			if err != nil {
				return err
			}
			ref := deviceLookupRow{SourceKey: sourceRowKey(node.NodeID, row), ID: row.ID, SHA256: diagnosticSHA(data)}
			data, err = json.Marshal(ref)
			if err != nil {
				return err
			}
			key := fmt.Sprintf("%srows/%020d/%s/%020d", base, node.NodeID, d.Key, row.ID)
			return b.add(transfer.SpoolRow{Key: []byte(key), Value: data})
		}); err != nil {
			return nil, err
		}
	}
	if err := b.flush(); err != nil {
		return nil, err
	}
	h := sha256.New()
	enc := json.NewEncoder(h)
	prefix := []byte(base + "rows/")
	var group string
	var first deviceLookupRow
	var previousID, count uint64
	flush := func() error {
		if group == "" {
			return nil
		}
		report.DeviceGroups++
		if count > 1 {
			report.DuplicateGroups++
			report.ShadowedRows += count - 1
		}
		data, err := json.Marshal(first)
		if err != nil {
			return err
		}
		return b.add(transfer.SpoolRow{Key: []byte(base + "chosen/" + group), Value: data})
	}
	err := w.Walk(ctx, prefix, func(row transfer.SpoolRow) error {
		suffix := string(row.Key[len(prefix):])
		split := strings.LastIndexByte(suffix, '/')
		if split < 0 {
			return errors.New("invalid device lookup group key")
		}
		var ref deviceLookupRow
		if err := json.Unmarshal(row.Value, &ref); err != nil {
			return err
		}
		if suffix[split+1:] != fmt.Sprintf("%020d", ref.ID) {
			return errors.New("device lookup order differs from primary identity")
		}
		if next := suffix[:split]; next != group {
			if err := flush(); err != nil {
				return err
			}
			group, first, previousID, count = next, ref, 0, 0
		}
		if ref.ID <= previousID {
			return errors.New("device lookup IDs are not strictly ordered")
		}
		previousID = ref.ID
		count++
		return enc.Encode(row)
	})
	if err != nil {
		return nil, err
	}
	if err := flush(); err != nil {
		return nil, err
	}
	if err := b.flush(); err != nil {
		return nil, err
	}
	report.SHA256 = hex.EncodeToString(h.Sum(nil))
	return report, nil
}

func keepsColdDevice(ctx context.Context, w Workspace, base string, node uint64, row Row, logicalKey string) (bool, error) {
	key := fmt.Sprintf("%schosen/%020d/%s", base, node, logicalKey)
	data, found, err := w.Get(ctx, []byte(key))
	if err != nil {
		return false, err
	}
	if !found {
		return false, errors.New("device cold-login selection is missing")
	}
	var ref deviceLookupRow
	if err := json.Unmarshal(data, &ref); err != nil {
		return false, err
	}
	if row.ID != ref.ID {
		return false, nil
	}
	data, err = json.Marshal(row)
	if err != nil {
		return false, err
	}
	if ref.SHA256 != diagnosticSHA(data) || !bytes.Equal(ref.SourceKey, sourceRowKey(node, row)) {
		return false, errors.New("chosen cold-login device differs from its original capture")
	}
	return true, nil
}
