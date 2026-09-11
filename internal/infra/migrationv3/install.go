package migrationv3

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"

	"github.com/WuKongIM/WuKongIM/internal/usecase/migration"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/controller/raft/raftstore"
	"github.com/WuKongIM/WuKongIM/pkg/controller/state"
	"github.com/WuKongIM/WuKongIM/pkg/controller/statefile"
	"github.com/WuKongIM/WuKongIM/pkg/dataformat"
	"github.com/WuKongIM/WuKongIM/pkg/db/message"
	"github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
	"github.com/WuKongIM/WuKongIM/pkg/raftlog"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"go.etcd.io/raft/v3/raftpb"
)

// InstallOptions includes independently prepared node-local plugin state and
// executables. Both participate in the immutable generation identity.
type InstallOptions struct {
	// CreatedBy records the tool that first initializes the target generation.
	CreatedBy       dataformat.Build
	PluginSettings  *migration.PluginSettingsReport
	PluginArtifacts *migration.PluginArtifactsReport
}

// Install writes a new cluster generation into exclusive offline output
// directories. Each node receives native business rows and bootstrap snapshots;
// it must subsequently start the normal Controller, Slot and Channel runtimes.
func Install(ctx context.Context, plan migration.TargetPlan, report migration.TargetRecordsReport, w migration.Workspace, options ...InstallOptions) (err error) {
	if !importMu.TryLock() {
		return errors.New("another offline native import is active")
	}
	defer importMu.Unlock()
	var locks []io.Closer
	defer func() {
		for i := len(locks) - 1; i >= 0; i-- {
			err = errors.Join(err, locks[i].Close())
		}
	}()
	var createdBy dataformat.Build
	var pluginSettings *migration.PluginSettingsReport
	var pluginArtifacts *migration.PluginArtifactsReport
	if len(options) > 1 {
		return errors.New("at most one plugin import option set is allowed")
	}
	if len(options) == 1 {
		createdBy = options[0].CreatedBy
		pluginSettings, pluginArtifacts = options[0].PluginSettings, options[0].PluginArtifacts
	}
	if err := migration.ValidatePluginArtifactsReport(ctx, w, pluginArtifacts); err != nil {
		return err
	}
	if pluginArtifacts != nil {
		if pluginSettings == nil || pluginSettings.PlanDigest != pluginArtifacts.PlanDigest {
			return errors.New("plugin executables require settings from the same plan")
		}
		for _, a := range pluginArtifacts.Targets {
			found := false
			for _, n := range plan.Nodes {
				found = found || n.NodeID == a.TargetNode
			}
			if !found {
				return errors.New("plugin executable refers to an unknown target")
			}
		}
	}
	if pluginSettings != nil {
		if err := migration.WalkPluginSettings(ctx, w, *pluginSettings, func(record migration.MappedPluginSettings) error {
			for _, n := range plan.Nodes {
				if n.NodeID == record.TargetNode {
					return nil
				}
			}
			return errors.New("plugin settings refer to an unknown target")
		}); err != nil {
			return err
		}
	}
	l, err := newLayout(ctx, plan)
	if err != nil {
		return err
	}
	seal, found, err := w.Get(ctx, []byte("conversion/COMPLETE"))
	if err != nil {
		return err
	}
	expected, err := migration.MarshalState(report)
	if err != nil {
		return err
	}
	if !found || !bytes.Equal(seal, expected) {
		return errors.New("target conversion completion does not match import report")
	}
	identity, err := json.Marshal(struct {
		Plan            migration.TargetPlan
		Records         migration.TargetRecordsReport
		PluginSettings  *migration.PluginSettingsReport  `json:",omitempty"`
		PluginArtifacts *migration.PluginArtifactsReport `json:",omitempty"`
	}{plan, report, pluginSettings, pluginArtifacts})
	if err != nil {
		return err
	}
	planSum := sha256.Sum256(identity)
	markerFor := func(n migration.TargetNode) []byte {
		seal := cluster.OfflineImportSeal{Version: 1, ClusterID: plan.ClusterID, NodeID: n.NodeID, SlotCount: plan.SlotCount, HashSlotCount: plan.HashSlotCount, Replicas: plan.Replicas, ChannelReplicas: plan.ChannelReplicas, PlanSHA256: fmt.Sprintf("%x", planSum), SourceSHA256: report.SelectionDigest, MaxMessageID: report.MaxMessageID}
		for _, node := range plan.Nodes {
			seal.Nodes = append(seal.Nodes, cluster.OfflineImportNode{NodeID: node.NodeID, Addr: node.Addr})
		}
		data, _ := json.Marshal(seal)
		return data
	}
	// Admit all nodes before writing one. Existing directories need the exact
	// generation identity; unrelated target data is never adopted.
	existing := make([]bool, len(plan.Nodes))
	for i, n := range plan.Nodes {
		existing[i], err = checkGeneration(n.DataDir, markerFor(n))
		if err != nil {
			return err
		}
		if err := dataformat.Check(n.DataDir); err != nil {
			return fmt.Errorf("target node %d data format: %w", n.NodeID, err)
		}
	}
	for i, n := range plan.Nodes {
		if !existing[i] {
			if err := os.Mkdir(n.DataDir, 0700); err != nil {
				return err
			}
			if err := writeExclusive(filepath.Join(n.DataDir, "MIGRATION-IMPORTING"), markerFor(n)); err != nil {
				return err
			}
			if err := syncDir(filepath.Dir(n.DataDir)); err != nil {
				return err
			}
		}
		lock, err := lockImport(filepath.Join(n.DataDir, "MIGRATION-LOCK"))
		if err != nil {
			return err
		}
		locks = append(locks, lock)
	}
	ready := make([]bool, len(plan.Nodes))
	for i, n := range plan.Nodes {
		ready[i], err = nodeReady(ctx, n.DataDir)
		if err != nil {
			return err
		}
	}
	for i, n := range plan.Nodes {
		if ready[i] {
			if hasSearchProfile(n, pluginArtifacts) {
				if err := migration.VerifySearchCheckpoints(ctx, w, searchCheckpointReader{n}); err != nil {
					return err
				}
			}
			if err := checkPluginArtifacts(ctx, n, pluginArtifacts); err != nil {
				return err
			}
			continue
		}
		if err := dataformat.InitializeOwned(n.DataDir, createdBy); err != nil {
			return fmt.Errorf("target node %d data format: %w", n.NodeID, err)
		}
		if err := installNode(ctx, n, l, report, w); err != nil {
			return fmt.Errorf("target node %d: %w", n.NodeID, err)
		}
		if err := installPluginSettings(ctx, n, w, pluginSettings); err != nil {
			return fmt.Errorf("target node %d plugin settings: %w", n.NodeID, err)
		}
		if err := installPluginArtifacts(ctx, n, w, pluginArtifacts); err != nil {
			return fmt.Errorf("target node %d plugin executables: %w", n.NodeID, err)
		}
		if hasSearchProfile(n, pluginArtifacts) {
			if err := installSearchCheckpoints(ctx, n, w); err != nil {
				return fmt.Errorf("target node %d search rebuild: %w", n.NodeID, err)
			}
		}
		digest, err := generationDigest(ctx, n.DataDir)
		if err != nil {
			return err
		}
		if err := writeExclusive(filepath.Join(n.DataDir, "MIGRATION-READY"), []byte(digest)); err != nil {
			return err
		}
	}
	// Every node is durable before any server can pass the startup seal guard.
	for _, n := range plan.Nodes {
		if err := writeExclusive(filepath.Join(n.DataDir, "MIGRATION-COMPLETE"), markerFor(n)); err != nil {
			return err
		}
	}

	return nil
}

func installNode(ctx context.Context, node migration.TargetNode, l *layout, report migration.TargetRecordsReport, w migration.Workspace) (err error) {
	db, err := meta.Open(filepath.Join(node.DataDir, "slotmeta"))
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, db.Close()) }()
	if err := migration.WalkTargetMetadata(ctx, w, func(row migration.TargetRecord) error {
		route, err := l.RouteKey(row.Owner)
		if err != nil {
			return err
		}
		if !slices.Contains(route.Peers, node.NodeID) {
			return nil
		}
		return installMetadata(ctx, db.MetaDB().HashSlot(row.HashSlot), row, w)
	}); err != nil {
		return err
	}
	// Subscriber insertion maintains native counters. Reapply the canonical
	// ordinary Channel flags/count after all member rows have been installed.
	if err := migration.WalkTargetMetadata(ctx, w, func(row migration.TargetRecord) error {
		if row.Table != "channel" {
			return nil
		}
		route, err := l.RouteKey(row.Owner)
		if err != nil {
			return err
		}
		if !slices.Contains(route.Peers, node.NodeID) {
			return nil
		}
		var value meta.Channel
		if err := migration.UnmarshalState(row.Value, &value); err != nil {
			return err
		}
		return db.MetaDB().HashSlot(row.HashSlot).UpsertChannel(ctx, value)
	}); err != nil {
		return err
	}
	messages, err := message.OpenWithLogger(filepath.Join(node.DataDir, "messages"), nil)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, messages.Close()) }()
	err = migration.WalkTargetChannels(ctx, w, func(channel migration.TargetChannel) error {
		id := ch.ChannelID{ID: channel.Channel.ID, Type: channel.Channel.Type}
		placement, err := l.placement.ResolveChannelPlacement(ctx, id)
		if err != nil {
			return err
		}
		route, err := l.RouteKey(id.ID)
		if err != nil {
			return err
		}
		if slices.Contains(route.Peers, node.NodeID) {
			replicas := make([]uint64, len(placement.Replicas))
			for i, n := range placement.Replicas {
				replicas[i] = uint64(n)
			}
			value := meta.NormalizeChannelRuntimeMeta(meta.ChannelRuntimeMeta{ChannelID: id.ID, ChannelType: int64(id.Type), ChannelEpoch: 1, LeaderEpoch: 1, Leader: uint64(placement.Leader), Replicas: replicas, ISR: append([]uint64(nil), replicas...), MinISR: int64(placement.MinISR), Status: uint8(ch.StatusActive), RetentionThroughSeq: channel.PrefixThrough, WriteFenceVersion: 1})
			if err := db.ForHashSlot(route.HashSlot).UpsertChannelRuntimeMeta(ctx, value); err != nil {
				return err
			}
		}
		if !slices.Contains(placement.Replicas, ch.NodeID(node.NodeID)) {
			return nil
		}
		return installMessages(ctx, messages, channel, report.Digest, w)
	})
	if err != nil {
		return err
	}
	if err := installSlotSnapshots(ctx, node, l, db); err != nil {
		return err
	}
	return installControllerSnapshot(ctx, node, l.state)
}

func installMetadata(ctx context.Context, s *meta.Shard, row migration.TargetRecord, w migration.Workspace) error {
	switch row.Table {
	case "plugin_binding":
		var v meta.PluginUserBinding
		if err := migration.UnmarshalState(row.Value, &v); err != nil {
			return err
		}
		existing, found, err := s.GetPluginUserBinding(ctx, v.UID, v.PluginNo)
		if err != nil {
			return err
		}
		if found && existing != v {
			return errors.New("target plugin binding differs from the planned import")
		}
		return s.BindPluginUser(ctx, v)
	case "user":
		var v meta.User
		if err := migration.UnmarshalState(row.Value, &v); err != nil {
			return err
		}
		return s.UpsertUser(ctx, v)
	case "device":
		var v meta.Device
		if err := migration.UnmarshalState(row.Value, &v); err != nil {
			return err
		}
		return s.UpsertDevice(ctx, v)
	case "channel":
		var v meta.Channel
		if err := migration.UnmarshalState(row.Value, &v); err != nil {
			return err
		}
		v.SubscriberCount = 0
		return s.UpsertChannel(ctx, v)
	case "subscriber":
		var v meta.Subscriber
		if err := migration.UnmarshalState(row.Value, &v); err != nil {
			return err
		}
		_, exists, err := s.GetChannel(ctx, v.ChannelID, v.ChannelType)
		if err != nil {
			return err
		}
		if !exists {
			return errors.New("subscriber has no explicitly planned native channel")
		}
		return s.AddSubscribers(ctx, v.ChannelID, v.ChannelType, []string{v.UID}, 1)
	case "membership":
		var v meta.UserChannelMembership
		if err := migration.UnmarshalState(row.Value, &v); err != nil {
			return err
		}
		return s.UpsertUserChannelMembership(ctx, v)
	case "cmd_membership":
		var v meta.UserCMDChannelMembership
		if err := migration.UnmarshalState(row.Value, &v); err != nil {
			return err
		}
		return s.UpsertUserCMDChannelMembership(ctx, v)
	case "event_cursor":
		return nil // Imported atomically with every matching lane.
	case "event_state":
		var v meta.MessageEventState
		if err := migration.UnmarshalState(row.Value, &v); err != nil {
			return err
		}
		cursor, err := migration.ReadTargetEventCursor(ctx, w, v)
		if err != nil {
			return err
		}
		return s.ImportMessageEventProjection(ctx, v, cursor)
	default:
		return fmt.Errorf("unsupported native metadata table %s", row.Table)
	}
}

func installMessages(ctx context.Context, db *message.Engine, channel migration.TargetChannel, digest string, w migration.Workspace) (err error) {
	id := channelcompat.ChannelID{ID: channel.Channel.ID, Type: channel.Channel.Type}
	key := channelcompat.ChannelKey(ch.ChannelKeyForID(ch.ChannelID{ID: id.ID, Type: id.Type}))
	log, err := db.ForChannel(key, id)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, log.Close()) }()
	var previous quorumlog.EntryIdentity
	if channel.PrefixThrough > 0 {
		return errors.New("source retained prefix cannot be represented by the existing v3 proposal format")
	}
	var rows []channelcompat.Record
	var records []quorumlog.Record
	nativeBytes, recoveryBytes := 0, 0
	flush := func() error {
		if len(records) == 0 {
			return nil
		}
		first, last := records[0].Index, records[len(records)-1].Index
		command := sha256.Sum256([]byte(fmt.Sprintf("wkmigrate/%s/%s/%d/%d", digest, key, first, last)))
		proposal, _, ok := quorumlog.SealProposalManifest(quorumlog.ProposalManifest{Version: quorumlog.VersionForRecords(records), ChannelEpoch: 1, LeaderTerm: 1, FenceVersion: 1, CommandID: command, BaseOffset: first - 1, LastOffset: last, PreviousIndex: previous.Index, PreviousTerm: previous.LeaderTerm, PreviousDigest: previous.Digest}, records)
		if !ok {
			return errors.New("cannot seal imported native proposal")
		}
		result := message.StoreAppendBatch(ctx, []message.AppendBatchItem{{Store: log, Records: rows, ExactBaseOffset: true, ExpectedBaseOffset: first - 1, Proposal: proposal, Committed: last}})[0]
		if result.Err != nil {
			return result.Err
		}
		if !result.Outcome.Durable() {
			return errors.New("native import proposal was not durable")
		}
		previous = quorumlog.EntryIdentity{Index: last, LeaderTerm: 1, Digest: proposal.Digest}
		rows = nil
		records = nil
		nativeBytes, recoveryBytes = 0, 0
		return nil
	}
	err = migration.WalkTargetMessages(ctx, w, channel.Channel, func(m channelcompat.Message) error {
		row, record, err := migration.PrepareMessageRecord(m)
		if err != nil {
			return err
		}
		recordBytes := quorumlog.RecoveryRecordBytes(record.FromUID, record.ClientMsgNo, len(record.Payload))
		if len(records) > 0 && (len(records) >= 64 || nativeBytes+len(row.Payload) > quorumlog.DefaultRecoveryPageBytes || recoveryBytes+recordBytes > quorumlog.DefaultRecoveryPageBytes) {
			if err := flush(); err != nil {
				return err
			}
		}
		records = append(records, record)
		rows = append(rows, row)
		nativeBytes += len(row.Payload)
		recoveryBytes += recordBytes
		return nil
	})
	if err != nil {
		return err
	}
	return flush()
}

func installSlotSnapshots(ctx context.Context, node migration.TargetNode, l *layout, db *meta.DB) (err error) {
	raft, err := raftlog.Open(filepath.Join(node.DataDir, "slotraft"), raftlog.Options{})
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, raft.Close()) }()
	for _, slot := range l.state.Slots {
		if !slices.Contains(slot.DesiredPeers, node.NodeID) {
			continue
		}
		var hashes []uint16
		for _, span := range l.state.HashSlots.Ranges {
			if span.SlotID == slot.SlotID {
				for h := uint32(span.From); h <= uint32(span.To); h++ {
					hashes = append(hashes, uint16(h))
				}
			}
		}
		reader, err := db.OpenHashSlotSnapshot(ctx, hashes)
		if err != nil {
			return err
		}
		// Native Raft's snapshot API currently owns a byte slice. Bound that
		// bridge explicitly; a larger metadata partition must fail before publish.
		data, readErr := io.ReadAll(io.LimitReader(reader, (256<<20)+1))
		closeErr := reader.Close()
		if err := errors.Join(readErr, closeErr); err != nil {
			return err
		}
		if len(data) > 256<<20 {
			return errors.New("target physical Slot metadata snapshot exceeds 256 MiB bounded bridge")
		}
		snap := raftpb.Snapshot{Data: data, Metadata: raftpb.SnapshotMetadata{Index: 1, Term: 1, ConfState: raftpb.ConfState{Voters: slot.DesiredPeers}}}
		hs := raftpb.HardState{Term: 1, Commit: 1}
		if err := raft.ForSlot(uint64(slot.SlotID)).Save(ctx, multiraft.PersistentState{HardState: &hs, Snapshot: &snap}); err != nil {
			return err
		}
	}
	return nil
}

func installControllerSnapshot(ctx context.Context, node migration.TargetNode, st state.ClusterState) (err error) {
	data, err := state.Encode(st)
	if err != nil {
		return err
	}
	dir := filepath.Join(node.DataDir, "controller")
	if err := os.Mkdir(dir, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	if err := statefile.New(filepath.Join(dir, "cluster-state.json")).Save(ctx, st); err != nil {
		return err
	}
	raft, err := raftstore.Open(ctx, raftstore.Config{Dir: filepath.Join(dir, "raft"), NodeID: node.NodeID})
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, raft.Close()) }()
	voters := make([]uint64, len(st.Controllers))
	for i, n := range st.Controllers {
		voters[i] = n.NodeID
	}
	snapshot := raftpb.Snapshot{Data: data, Metadata: raftpb.SnapshotMetadata{Index: 1, Term: 1, ConfState: raftpb.ConfState{Voters: voters}}}
	if err := raft.SaveReady(ctx, raftpb.HardState{Term: 1, Commit: 1}, nil, snapshot); err != nil {
		return err
	}
	return raft.MarkAppliedBatch(ctx, 1)
}
