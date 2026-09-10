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
	"slices"

	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
)

type sourceGroup struct {
	Leader   uint64   `json:"leader"`
	Replicas []uint64 `json:"replicas"`
}

// sourceCandidate references the immutable capture instead of duplicating
// message payloads for every join, comparison and selected record.
type sourceCandidate struct {
	NodeID     uint64         `json:"node_id"`
	SourceKey  []byte         `json:"source_key"`
	Table      string         `json:"table"`
	Kind       Kind           `json:"kind"`
	Identity   RecordIdentity `json:"identity"`
	LogicalKey string         `json:"logical_key"`
	Digest     string         `json:"digest"`
	Group      sourceGroup    `json:"group"`
}

// SelectSources compares every configured replica to the persisted authority.
// No union or maximum sequence can create a business record. Source indexes,
// runtime metadata and obsolete management rows remain in the bound capture.
func SelectSources(ctx context.Context, capture SourceCapture, catalog SourceCatalog, workspace Workspace, decoder RecordDecoder, exclusions *Exclusions, policies ...*MessagePolicy) (selection SourceSelection, err error) {
	return selectSources(ctx, capture, catalog, workspace, decoder, exclusions, nil, nil, nil, policies...)
}

func selectSources(ctx context.Context, capture SourceCapture, catalog SourceCatalog, workspace Workspace, decoder RecordDecoder, exclusions *Exclusions, metadata *MetadataPolicy, artifacts *PluginArtifactsReport, history *HistoryPolicy, policies ...*MessagePolicy) (selection SourceSelection, err error) {
	// Retain the original history decoding capability before the later record
	// projection decorators. Empty-channel certification wraps this same reader.
	historyDecoder, _ := decoder.(HistoryPrefixDecoder)
	if empty, ok := decoder.(*emptyChannelDecoder); ok {
		historyDecoder, _ = empty.OriginalDecoder.(HistoryPrefixDecoder)
	}
	if err := validateHistoryPolicy(history); err != nil {
		return selection, err
	}
	var prefixComparison *historyPrefixComparison
	if history != nil && history.LeaderQuorumPrefixes {
		if historyDecoder == nil {
			return selection, errors.New("source history policy requires original history evidence decoder")
		}
		prefixComparison = &historyPrefixComparison{ctx: ctx, capture: capture, w: workspace, decoder: historyDecoder, policy: *history}
	}
	if view, ok := workspace.(*quarantineWorkspace); ok {
		selection.Quarantine = view.report
	}
	selection.PluginArtifacts = artifacts
	if artifacts != nil {
		if artifacts.Compatibility == nil {
			selection.PluginArtifactCompatibilityPending = uint64(len(artifacts.Files))
		}
	}
	// Plugin compatibility does not decide replica authority. Keep its gate
	// closed, but do not let it hide an independent source inconsistency.
	defer func() {
		if selection.PluginArtifactCompatibilityPending > 0 {
			selection.Digest = ""
			err = errors.Join(err, errors.New("plugin executables require a verified business compatibility profile"))
		}
		if selection.PluginBusinessRows > 0 {
			selection.Digest = ""
			err = errors.Join(err, errors.New("source plugin business methods/config require a verified v3 compatibility mapping"))
		}
	}()
	if ctx == nil || workspace == nil || decoder == nil || capture.Digest == "" || catalog.Digest == "" {
		return selection, errors.New("source selection requires completed capture and catalog")
	}
	if err := validateCapturedAuthority(capture.Nodes); err != nil {
		return selection, err
	}
	if proof, ok := decoder.(interface{ EmptyChannelProof() *EmptyChannelProof }); ok {
		selection.EmptyChannels = proof.EmptyChannelProof()
	}
	selection.Metadata, err = reduceDeviceLookups(ctx, capture, workspace, decoder, metadata)
	if err != nil {
		return selection, err
	}
	if selection.Metadata != nil {
		selection.Metadata.Conversations, err = reduceConversationLookups(ctx, capture, workspace, decoder, metadata)
		if err != nil {
			return selection, err
		}
	}
	decoder, selection.AuthorityDigest, err = certifyCapturedTransitions(ctx, capture, workspace, decoder)
	if err != nil {
		return selection, err
	}
	if artifacts != nil && artifacts.Compatibility != nil {
		decoder = withPluginProfile(decoder, artifacts.Compatibility)
	}
	if len(policies) > 1 {
		return selection, errors.New("one message policy is required")
	}
	if len(policies) == 1 {
		selection.Messages = policies[0]
	}
	if err := validateMessagePolicy(selection.Messages); err != nil {
		return selection, err
	}
	selection.Tables = map[string]uint64{}
	selection.Preserved = map[string]uint64{}
	excludedHash := sha256.New()
	excludedEncoder := json.NewEncoder(excludedHash)
	userTimestampHash := sha256.New()
	userTimestampEncoder := json.NewEncoder(userTimestampHash)
	if metadata != nil && metadata.ArchiveUserTimestamps {
		selection.UserTimestamps = &UserTimestampArchive{}
	}
	if exclusions != nil && exclusions.LegacyStreamStorage {
		selection.Excluded = &ExclusionReport{Policy: *exclusions, PhysicalRows: map[string]uint64{}}
	}
	slots := make(map[uint32]SourceSlot, len(capture.Nodes[0].Config.Slots))
	knownNodes := make(map[uint64]bool, len(capture.Nodes))
	for _, slot := range capture.Nodes[0].Config.Slots {
		slots[slot.ID] = slot
	}
	for _, node := range capture.Nodes {
		knownNodes[node.NodeID] = true
	}
	batch := &captureBatch{ctx: ctx, workspace: workspace}
	for _, phase := range []string{"metadata", "messages"} {
		for _, node := range capture.Nodes {
			err := walkSourceRows(ctx, workspace, node.NodeID, func(row Row) error {
				category, reason, err := sourceCategory(row, exclusions)
				if err != nil {
					return err
				}
				if reason != "" {
					if phase == "metadata" {
						if reason == "excluded_legacy_stream_storage" {
							selection.Excluded.PhysicalRows[row.Table]++
							if err := excludedEncoder.Encode(struct {
								NodeID uint64
								Row    Row
							}{node.NodeID, row}); err != nil {
								return err
							}
						}
						if row.Table == "Plugin" && row.Kind == Primary {
							description, err := decoder.Describe(row, RecordIdentity{})
							if err != nil {
								return err
							}
							// Persisted status does not prove that an original global
							// hook was inactive. Check all business copies before
							// returning the still-mandatory compatibility failure.
							if description.Plugin == nil || (description.Plugin.CompatibilityProfile == "" && (len(description.Plugin.Methods) != 0 || description.Plugin.HasConfig)) {
								selection.PluginBusinessRows++
							}
						}
						selection.Preserved[reason]++
					}
					return nil
				}
				if category != phase {
					return nil
				}
				id, err := decoder.Identify(row)
				if err != nil {
					return err
				}
				id, err = resolveIdentity(ctx, workspace, id)
				if err != nil {
					return fmt.Errorf("%s identity: %w", row.Table, err)
				}
				description, err := decoder.Describe(row, id)
				if err != nil {
					return err
				}
				if description.ArchivedEmptyChannel {
					selection.Preserved["unreferenced_empty_channel_administration"]++
					return nil
				}
				if description.DerivedChannelCounters {
					selection.Preserved["derived_channel_counters"]++
					return nil
				}
				if row.Table == "User" && selection.UserTimestamps != nil {
					if len(description.UserWithoutTimestamps) == 0 {
						return errors.New("user timestamp archival requires a proven original field projection")
					}
					// Bind every captured original User row, including copies
					// outside current ownership. No source timestamp is rewritten.
					if err := userTimestampEncoder.Encode(struct {
						NodeID uint64
						Row    Row
					}{node.NodeID, row}); err != nil {
						return err
					}
					selection.UserTimestamps.Rows++
					for _, field := range []string{"CreatedAt", "UpdatedAt"} {
						if _, exists := row.Fields[field]; exists {
							selection.UserTimestamps.Fields++
						}
					}
					description.Comparable = description.UserWithoutTimestamps
				}
				var group sourceGroup
				if phase == "metadata" {
					owner := id.Channel.ID
					switch row.Table {
					case "User", "Device", "Conversation":
						owner = id.UID
					}
					slotID := crc32.ChecksumIEEE([]byte(owner)) % capture.Nodes[0].Config.SlotCount
					if row.Table == "SystemUid" || row.Table == "PluginUser" {
						slotID = 0
					}
					slot, ok := slots[slotID]
					if !ok {
						return errors.New("missing source owner Slot")
					}
					group = sourceGroup{Leader: slot.Leader, Replicas: slot.Replicas}
				} else {
					group, err = messageGroup(ctx, workspace, decoder, id.Channel, knownNodes)
					if err != nil {
						return err
					}
				}
				if !slices.Contains(group.Replicas, node.NodeID) {
					selection.Preserved["outside_current_replica_group"]++
					return nil
				}
				if row.Table == "Device" && selection.Metadata != nil {
					keep, err := keepsColdDevice(ctx, workspace, capture.Digest, metadata, node.NodeID, row, description.Key)
					if err != nil {
						return err
					}
					if !keep {
						selection.Preserved["device_rows_shadowed_on_v2_cold_start"]++
						return nil
					}
				}
				if row.Table == "Conversation" && selection.Metadata != nil && selection.Metadata.Conversations != nil {
					keep, err := keepsConversation(ctx, workspace, capture.Digest, metadata, node.NodeID, row, description.Key)
					if err != nil {
						return err
					}
					if !keep {
						selection.Preserved["conversation_rows_shadowed_by_original_lookups"]++
						return nil
					}
					if description.ConversationLookup == nil {
						return errors.New("selected conversation lacks original state")
					}
					description.Comparable = description.ConversationLookup.State
				}
				sum := sha256.Sum256(description.Comparable)
				candidate := sourceCandidate{
					NodeID: node.NodeID, SourceKey: sourceRowKey(node.NodeID, row), Table: row.Table, Kind: row.Kind,
					Identity: id, LogicalKey: description.Key, Digest: hex.EncodeToString(sum[:]), Group: group,
				}
				value, err := MarshalState(candidate)
				if err != nil {
					return err
				}
				return batch.add(transfer.SpoolRow{Key: candidateKey(phase, node.NodeID, row.Table, description.Key), Value: value})
			})
			if err != nil {
				return selection, fmt.Errorf("source node %d %s selection: %w", node.NodeID, phase, err)
			}
		}
		if err := batch.flush(); err != nil {
			return selection, err
		}
		if phase == "metadata" && selection.UserTimestamps != nil {
			selection.UserTimestamps.SHA256 = hex.EncodeToString(userTimestampHash.Sum(nil))
		}
		var accept func(sourceCandidate) (uint64, error)
		if phase == "messages" && prefixComparison != nil {
			accept = prefixComparison.source
		}
		comparisonErr := compareCandidatesWithHistory(ctx, workspace, phase, batch, accept)
		if phase == "messages" && prefixComparison != nil {
			selection.HistoryPrefixes, err = prefixComparison.report()
			comparisonErr = errors.Join(comparisonErr, err)
		}
		if comparisonErr != nil {
			return selection, comparisonErr
		}
		if err := batch.flush(); err != nil {
			return selection, err
		}
	}
	selection.ReplicaComparisonComplete = true
	if err := recoverSourceConversations(ctx, capture, workspace, decoder, batch, &selection); err != nil {
		return selection, err
	}
	if err := batch.flush(); err != nil {
		return selection, err
	}
	if selection.Metadata != nil {
		if err := includePendingConversationList(ctx, capture, workspace, decoder, metadata, selection.Metadata.Conversations); err != nil {
			return selection, err
		}
	}
	h := sha256.New()
	enc := json.NewEncoder(h)
	if selection.Quarantine != nil {
		if err := enc.Encode(selection.Quarantine); err != nil {
			return selection, err
		}
	}
	if selection.HistoryPrefixes != nil {
		if err := enc.Encode(selection.HistoryPrefixes); err != nil {
			return selection, err
		}
	}
	if selection.PluginArtifacts != nil {
		if err := enc.Encode(selection.PluginArtifacts); err != nil {
			return selection, err
		}
	}
	if selection.UserTimestamps != nil {
		if err := enc.Encode(selection.UserTimestamps); err != nil {
			return selection, err
		}
	}
	if selection.Metadata != nil {
		if err := enc.Encode(selection.Metadata); err != nil {
			return selection, err
		}
	}
	if selection.EmptyChannels != nil {
		if err := enc.Encode(selection.EmptyChannels); err != nil {
			return selection, err
		}
	}
	if selection.AuthorityDigest != "" {
		if err := enc.Encode(selection.AuthorityDigest); err != nil {
			return selection, err
		}
	}
	if selection.Messages != nil {
		if err := enc.Encode(selection.Messages); err != nil {
			return selection, err
		}
	}
	if selection.Excluded != nil {
		selection.Excluded.SHA256 = hex.EncodeToString(excludedHash.Sum(nil))
		if err := enc.Encode(selection.Excluded); err != nil {
			return selection, err
		}
	}
	err = WalkSelectedSources(ctx, workspace, func(record SelectedRecord) error {
		if record.Row.Kind == Primary {
			selection.Tables[record.Row.Table]++
		}
		data, err := MarshalState(record)
		if err != nil {
			return err
		}
		return enc.Encode(json.RawMessage(data))
	})
	if err != nil {
		return selection, err
	}
	if selection.PluginBusinessRows == 0 && selection.PluginArtifactCompatibilityPending == 0 {
		selection.Digest = hex.EncodeToString(h.Sum(nil))
	}
	return selection, nil
}

func sourceCategory(row Row, exclusions *Exclusions) (category, reason string, err error) {
	// Legacy Stream chunks use the index kind for actual business payloads.
	// Handle them before the generic index preservation rule.
	if row.Table == "LegacyStream" || row.Table == "LegacyStreamMeta" {
		if exclusions == nil || !exclusions.LegacyStreamStorage {
			return "", "", errors.New("legacy stream storage is unsupported; explicit exclusions.legacy_stream_storage is required to archive it without target import")
		}
		return "", "excluded_legacy_stream_storage", nil
	}
	if row.Kind == Index || row.Kind == SecondaryIndex {
		return "", "original_indexes", nil
	}
	switch row.Table {
	case "MessageNotifyQueue":
		return "", "", errors.New("source retains message notification queue records; do not replay automatically")
	case "Total", "Tester", "Plugin":
		return "", "old_management_data", nil
	case "LeaderTermSequence", "ChannelCommon":
		return "", "source_replication_metadata", nil
	case "ConversationLocalUser":
		return "", "derived_conversation_directory", nil
	case "SubscriberChannelRelation":
		return "", "obsolete_subscriber_directory", nil
	case "PendingConversation":
		return "", "", nil
	case "IgnoredConversation":
		return "", "originally_ignored_empty_uid_conversation", nil
	case "Message":
		if row.Kind == Primary || row.Kind == Other {
			return "messages", "", nil
		}
	case "User", "Device", "ChannelInfo", "Subscriber", "Denylist", "Allowlist", "SystemUid", "PluginUser", "Conversation", "ChannelClusterConfig", "MessageEventState":
		if row.Kind == Primary {
			return "metadata", "", nil
		}
	case "MessageEventSeq":
		if row.Kind == Other {
			return "metadata", "", nil
		}
	}
	return "", "", fmt.Errorf("source business table %s kind %d has no supported interpretation", row.Table, row.Kind)
}

func sourceRowKey(nodeID uint64, row Row) []byte {
	return []byte(fmt.Sprintf("source/%020d/rows/%04d/%x", nodeID, row.Shard, row.Key))
}
func candidateKey(phase string, nodeID uint64, table, key string) []byte {
	return []byte(fmt.Sprintf("candidate/%s/%020d/%s/%s", phase, nodeID, table, key))
}
func selectedKey(table, key string) []byte { return []byte("selected/" + table + "/" + key) }

func compareCandidates(ctx context.Context, workspace Workspace, phase string, batch *captureBatch) error {
	return compareCandidatesWithHistory(ctx, workspace, phase, batch, nil)
}

func compareCandidatesWithHistory(ctx context.Context, workspace Workspace, phase string, batch *captureBatch, accept func(sourceCandidate) (uint64, error)) error {
	return workspace.Walk(ctx, []byte("candidate/"+phase+"/"), func(record transfer.SpoolRow) error {
		var row sourceCandidate
		if err := UnmarshalState(record.Value, &row); err != nil {
			return err
		}
		if !bytes.Equal(record.Key, candidateKey(phase, row.NodeID, row.Table, row.LogicalKey)) {
			return errors.New("source comparison key mismatch")
		}
		selectedNode := row.Group.Leader
		ids := []uint64{row.Group.Leader}
		if row.NodeID == row.Group.Leader {
			ids = row.Group.Replicas
		}
		for _, id := range ids {
			data, ok, err := workspace.Get(ctx, candidateKey(phase, id, row.Table, row.LogicalKey))
			if err != nil {
				return err
			}
			if !ok {
				if phase == "messages" && accept != nil {
					var err error
					selectedNode, err = accept(row)
					if err != nil {
						return err
					}
					break
				}
				return fmt.Errorf("source %s record is missing on configured replica node %d", row.Table, id)
			}
			var other sourceCandidate
			if err := UnmarshalState(data, &other); err != nil {
				return err
			}
			if other.Digest != row.Digest {
				if phase == "messages" && accept != nil {
					var err error
					selectedNode, err = accept(row)
					if err != nil {
						return err
					}
					break
				}
				return fmt.Errorf("source %s record conflicts between replica nodes %d and %d", row.Table, row.NodeID, id)
			}
		}
		if row.NodeID != selectedNode {
			return nil
		}
		return batch.add(transfer.SpoolRow{Key: selectedKey(row.Table, row.LogicalKey), Value: record.Value})
	})
}

func resolveIdentity(ctx context.Context, workspace Workspace, id RecordIdentity) (RecordIdentity, error) {
	get := func(key string) ([]byte, error) {
		data, ok, err := workspace.Get(ctx, []byte(key))
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, errors.New("unresolved source hash reference")
		}
		return data, nil
	}
	if id.UID == "" && id.UIDHash != 0 {
		data, err := get(fmt.Sprintf("catalog/uid/%016x", id.UIDHash))
		if err != nil {
			return id, err
		}
		id.UID = string(data)
	}
	if id.Channel.ID == "" && (id.ChannelHash != 0 || id.EventChannelHash != 0) {
		key := fmt.Sprintf("catalog/channel/%016x", id.ChannelHash)
		if id.EventChannelHash != 0 {
			key = fmt.Sprintf("catalog/event-channel/%016x", id.EventChannelHash)
		}
		data, err := get(key)
		if err != nil {
			return id, err
		}
		if err := UnmarshalState(data, &id.Channel); err != nil {
			return id, err
		}
	}
	if id.ClientMsgNo == "" && id.ClientMsgHash != 0 {
		data, err := get(fmt.Sprintf("catalog/event-message/%016x/%016x", id.EventChannelHash, id.ClientMsgHash))
		if err != nil {
			return id, err
		}
		id.ClientMsgNo = string(data)
	}
	return id, nil
}

func messageGroup(ctx context.Context, workspace Workspace, decoder RecordDecoder, channel ChannelIdentity, nodes map[uint64]bool) (group sourceGroup, err error) {
	encoded, ok, err := workspace.Get(ctx, selectedKey("ChannelClusterConfig", IdentityKey(channel.ID, channel.Type)))
	if err != nil {
		return group, err
	}
	if !ok {
		return group, errors.New("message channel lacks authoritative source configuration")
	}
	record, err := loadSelectedRecord(ctx, workspace, encoded)
	if err != nil {
		return group, err
	}
	description, err := decoder.Describe(record.Row, record.Identity)
	if err != nil {
		return group, err
	}
	if description.Authority == nil {
		return group, errors.New("source channel lacks decoded authority")
	}
	for _, node := range description.Authority.Replicas {
		if !nodes[node] {
			return group, fmt.Errorf("message channel replica node %d is missing", node)
		}
	}
	return sourceGroup{Leader: description.Authority.Leader, Replicas: description.Authority.Replicas}, nil
}

func walkSelected(ctx context.Context, workspace Workspace, prefix []byte, visit func(SelectedRecord) error) error {
	if ctx == nil || workspace == nil || visit == nil {
		return errors.New("selected source walk requires context, workspace and visitor")
	}
	return workspace.Walk(ctx, prefix, func(row transfer.SpoolRow) error {
		record, err := loadSelectedRecord(ctx, workspace, row.Value)
		if err != nil {
			return err
		}
		if !bytes.Equal(row.Key, selectedKey(record.Row.Table, record.LogicalKey)) {
			return errors.New("selected source key mismatch")
		}
		return visit(record)
	})
}
func loadSelectedRecord(ctx context.Context, workspace Workspace, data []byte) (record SelectedRecord, err error) {
	var reference sourceCandidate
	if err := UnmarshalState(data, &reference); err != nil {
		return record, err
	}
	value, ok, err := workspace.Get(ctx, reference.SourceKey)
	if err != nil {
		return record, err
	}
	if !ok {
		return record, errors.New("selected source record is absent from its capture")
	}
	if err := json.Unmarshal(value, &record.Row); err != nil {
		return record, err
	}
	if !bytes.Equal(reference.SourceKey, sourceRowKey(reference.NodeID, record.Row)) || record.Row.Table != reference.Table || record.Row.Kind != reference.Kind {
		return record, errors.New("selected source reference mismatch")
	}
	record.NodeID, record.Identity, record.LogicalKey = reference.NodeID, reference.Identity, reference.LogicalKey
	return record, nil
}
