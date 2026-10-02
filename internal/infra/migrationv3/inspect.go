package migrationv3

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"reflect"
	"slices"
	"sync"

	"github.com/WuKongIM/WuKongIM/internal/usecase/migration"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/controller/raft/raftstore"
	"github.com/WuKongIM/WuKongIM/pkg/controller/state"
	"github.com/WuKongIM/WuKongIM/pkg/controller/statefile"
	"github.com/WuKongIM/WuKongIM/pkg/dataformat"
	"github.com/WuKongIM/WuKongIM/pkg/db/inspect"
	"github.com/WuKongIM/WuKongIM/pkg/db/message"
	"github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
	"github.com/WuKongIM/WuKongIM/pkg/raftlog"
	"go.etcd.io/raft/v3/raftpb"
)

// Inspector reads stopped native stores under shared process-exclusion locks.
// It has no access to converted records or importer success counters.
type Inspector struct{}

// POSIX record locks are process-owned: closing a second descriptor can release
// the first reader's lock. Serialize inspection sessions within this process.
var inspectionMu sync.Mutex

type nativeView struct {
	node         migration.TargetNode
	layout       *layout
	source       string
	maxMessageID uint64
	store        *inspect.Store
	locks        []io.Closer
	log          *message.ChannelLog
	logID        migration.ChannelIdentity
	closed       bool
}

func (Inspector) Open(ctx context.Context, plan migration.TargetPlan, node migration.TargetNode) (_ migration.TargetView, err error) {
	if !inspectionMu.TryLock() {
		return nil, errors.New("another offline target inspection is active")
	}
	v := &nativeView{node: node}
	defer func() {
		if err != nil {
			err = errors.Join(err, v.Close())
		}
	}()
	v.layout, err = newLayout(ctx, plan)
	if err != nil {
		return nil, err
	}
	if err := dataformat.Check(node.DataDir); err != nil {
		return nil, err
	}
	seal, found, err := cluster.ReadOfflineImportSeal(node.DataDir)
	if err != nil {
		return nil, err
	}
	if !found || seal.NodeID != node.NodeID || seal.ClusterID != plan.ClusterID || seal.HashSlotCount != plan.HashSlotCount || seal.SlotCount != plan.SlotCount || seal.Replicas != plan.Replicas || seal.ChannelReplicas != plan.ChannelReplicas {
		return nil, errors.New("target generation seal differs from verification plan")
	}
	v.source = seal.SourceSHA256
	v.maxMessageID = seal.MaxMessageID
	for _, dir := range []string{"slotmeta", "messages", "slotraft"} {
		lock, e := lockTarget(filepath.Join(node.DataDir, dir, "LOCK"))
		if e != nil {
			return nil, fmt.Errorf("target must be stopped and readable (%s): %w", dir, e)
		}
		v.locks = append(v.locks, lock)
	}
	// Verification precedes opening the target to traffic. Consensus initialization
	// must still be exactly the planned native generation, not a modified cluster.
	st, err := statefile.New(filepath.Join(node.DataDir, "controller", "cluster-state.json")).Load(ctx)
	if err != nil {
		return nil, err
	}
	actualState, err := state.Encode(st)
	if err != nil {
		return nil, err
	}
	expectedState, err := state.Encode(v.layout.state)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(actualState, expectedState) {
		return nil, errors.New("target Controller state differs from the offline generation; verify before starting v3")
	}
	v.store, err = inspect.OpenStore(inspect.Options{MetaPath: filepath.Join(node.DataDir, "slotmeta"), MessagePath: filepath.Join(node.DataDir, "messages")})
	if err != nil {
		return nil, err
	}
	return v, nil
}

func (v *nativeView) SourceDigest() string { return v.source }
func (v *nativeView) OwnsMetadata(owner string) (bool, error) {
	r, err := v.layout.RouteKey(owner)
	return slices.Contains(r.Peers, v.node.NodeID), err
}
func (v *nativeView) OwnsMessages(id migration.ChannelIdentity) (bool, error) {
	p, err := v.layout.placement.ResolveChannelPlacement(context.Background(), ch.ChannelID{ID: id.ID, Type: id.Type})
	return slices.Contains(p.Replicas, ch.NodeID(v.node.NodeID)), err
}

func (v *nativeView) Metadata(ctx context.Context, table, owner string, filters map[string]any) (map[string]any, bool, error) {
	route, err := v.layout.RouteKey(owner)
	if err != nil {
		return nil, false, err
	}
	if table == "plugin_binding" {
		uid, uidOK := filters["uid"].(string)
		no, noOK := filters["plugin_no"].(string)
		if !uidOK || !noOK || uid != owner {
			return nil, false, errors.New("invalid plugin binding verification key")
		}
		s := v.store.Meta().HashSlot(route.HashSlot)
		p, found, err := s.GetPluginUserBinding(ctx, uid, no)
		if err != nil || !found {
			return nil, found, err
		}
		exists, err := s.ExistPluginBindingByUID(ctx, uid)
		if err != nil {
			return nil, false, err
		}
		if !exists {
			return nil, false, errors.New("plugin binding UID lookup is missing")
		}
		return map[string]any{"uid": p.UID, "plugin_no": p.PluginNo, "created_at_ms": p.CreatedAtMS, "updated_at_ms": p.UpdatedAtMS}, true, nil
	}
	req := meta.InspectScanRequest{Table: table, HashSlot: route.HashSlot, HashSlotSet: true, Filters: filters, Limit: 2}
	var row map[string]any
	for {
		page, err := meta.InspectScan(ctx, v.store.Meta(), req)
		if err != nil {
			return nil, false, err
		}
		for _, got := range page.Rows {
			if row != nil {
				return nil, false, errors.New("ambiguous target metadata identity")
			}
			if table == "subscriber" {
				// v2 verifies its original business fields; the native-only
				// default must still be exactly legacy, never a rejoined row.
				if got["incarnation"] != uint64(1) {
					return nil, false, errors.New("subscriber incarnation differs from the legacy import")
				}
				delete(got, "incarnation")
			}
			row = got
		}
		if page.Done {
			return row, row != nil, nil
		}
		if page.Next == nil {
			return nil, false, errors.New("metadata scan did not advance")
		}
		req.After = page.Next
	}
}

// WalkPluginBindings exercises the public reverse index without materializing
// an entire plugin's user population in memory.
func (v *nativeView) WalkPluginBindings(ctx context.Context, hashSlot uint16, pluginNo string, visit func(meta.PluginUserBinding) error) error {
	if hashSlot >= v.layout.state.Config.HashSlotCount {
		return errors.New("plugin binding hash slot exceeds target layout")
	}
	s := v.store.Meta().HashSlot(hashSlot)
	var cursor meta.PluginUserBindingCursor
	for {
		rows, next, done, err := s.ScanPluginBindingsByPluginNo(ctx, pluginNo, cursor, 128)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if err := visit(row); err != nil {
				return err
			}
		}
		if done {
			return nil
		}
		if next.PluginNo != pluginNo || len(next.UID) < len(cursor.UID) || (len(next.UID) == len(cursor.UID) && next.UID <= cursor.UID) {
			return errors.New("plugin binding reverse index did not advance")
		}
		cursor = next
	}
}

func (v *nativeView) channel(id migration.ChannelIdentity) (*message.ChannelLog, error) {
	if v.log != nil && v.logID == id {
		return v.log, nil
	}
	if v.log != nil {
		if err := v.log.Close(); err != nil {
			return nil, err
		}
		v.log = nil
	}
	log, err := v.store.Messages().Channel(message.ChannelKey(ch.ChannelKeyForID(ch.ChannelID{ID: id.ID, Type: id.Type})), message.ChannelID{ID: id.ID, Type: id.Type})
	if err != nil {
		return nil, err
	}
	v.log = log
	v.logID = id
	return log, nil
}

func (v *nativeView) Message(ctx context.Context, id migration.ChannelIdentity, seq uint64) (channelcompat.Message, bool, error) {
	log, err := v.channel(id)
	if err != nil {
		return channelcompat.Message{}, false, err
	}
	m, found, err := log.ReadOfflineMessage(ctx, seq)
	if err != nil || !found {
		return m, found, err
	}
	indexed, found, err := log.GetByMessageID(ctx, m.MessageID)
	if err != nil {
		return m, false, err
	}
	if !found || indexed.MessageSeq != seq {
		return m, false, errors.New("message ID index differs from primary row")
	}
	if m.FromUID != "" && m.ClientMsgNo != "" {
		hit, found, err := log.LookupIdempotency(ctx, message.IdempotencyKey{FromUID: m.FromUID, ClientMsgNo: m.ClientMsgNo})
		if err != nil {
			return m, false, err
		}
		if !found || hit.MessageID != m.MessageID || hit.MessageSeq != m.MessageSeq || hit.Offset != m.MessageSeq-1 {
			return m, false, errors.New("message idempotency index differs from primary row")
		}
	}
	return m, true, nil
}

func (v *nativeView) Progress(ctx context.Context, id migration.ChannelIdentity) (uint64, uint64, error) {
	log, err := v.channel(id)
	if err != nil {
		return 0, 0, err
	}
	if err := log.VerifyOfflineImportedLog(ctx, quorumlog.DefaultRecoveryPageBytes); err != nil {
		return 0, 0, err
	}
	leo, err := log.LEO(ctx)
	if err != nil {
		return 0, 0, err
	}
	cp, found, err := log.LoadCheckpoint(ctx)
	if err != nil {
		return 0, 0, err
	}
	if leo > 0 && !found {
		return 0, 0, errors.New("target message checkpoint missing")
	}
	return leo, cp.HW, nil
}

func (v *nativeView) CheckRuntime(ctx context.Context, id migration.ChannelIdentity, prefix uint64) error {
	route, err := v.layout.RouteKey(id.ID)
	if err != nil {
		return err
	}
	got, found, err := v.store.Meta().HashSlot(route.HashSlot).GetChannelRuntimeMeta(ctx, id.ID, int64(id.Type))
	if err != nil {
		return err
	}
	p, err := v.layout.placement.ResolveChannelPlacement(ctx, ch.ChannelID{ID: id.ID, Type: id.Type})
	if err != nil {
		return err
	}
	replicas := make([]uint64, len(p.Replicas))
	for i, n := range p.Replicas {
		replicas[i] = uint64(n)
	}
	want := meta.NormalizeChannelRuntimeMeta(meta.ChannelRuntimeMeta{ChannelID: id.ID, ChannelType: int64(id.Type), ChannelEpoch: 1, LeaderEpoch: 1, Replicas: replicas, ISR: append([]uint64(nil), replicas...), Leader: uint64(p.Leader), MinISR: int64(p.MinISR), Status: uint8(ch.StatusActive), RetentionThroughSeq: prefix, WriteFenceVersion: 1})
	if !found || !reflect.DeepEqual(got, want) {
		return errors.New("channel runtime authority or retained prefix differs from planned generation")
	}
	return nil
}

// Counts includes every registered primary table and every physical catalog
// channel, so extra rows on non-owner nodes cannot hide behind point checks.
func (v *nativeView) Counts(ctx context.Context) (map[string]uint64, uint64, uint64, error) {
	counts := map[string]uint64{}
	for _, table := range meta.InspectTables() {
		req := meta.InspectScanRequest{Table: table.Name, HashSlotCount: 256, Limit: 8}
		for {
			page, err := meta.InspectScan(ctx, v.store.Meta(), req)
			if err != nil {
				return nil, 0, 0, err
			}
			counts[table.Name] += uint64(len(page.Rows))
			if page.Done {
				break
			}
			if page.Next == nil {
				return nil, 0, 0, errors.New("metadata count cursor missing")
			}
			req.After = page.Next
		}
	}
	var rows, channels uint64
	req := message.InspectMessageRequest{Limit: 64}
	for {
		page, err := message.InspectChannels(ctx, v.store.Messages(), req)
		if err != nil {
			return nil, 0, 0, err
		}
		for _, row := range page.Rows {
			id := migration.ChannelIdentity{ID: row["channel_id"].(string), Type: row["channel_type"].(uint8)}
			if row["channel_key"] != string(ch.ChannelKeyForID(ch.ChannelID{ID: id.ID, Type: id.Type})) {
				return nil, 0, 0, errors.New("message catalog channel identity mismatch")
			}
			log, err := v.channel(id)
			if err != nil {
				return nil, 0, 0, err
			}
			channels++
			from := uint64(1)
			for {
				messages, err := log.Read(ctx, from, message.ReadOptions{Limit: 64, MaxBytes: 4 << 20})
				if err != nil {
					return nil, 0, 0, err
				}
				if len(messages) == 0 {
					break
				}
				rows += uint64(len(messages))
				last := messages[len(messages)-1].MessageSeq
				if last < from || last == ^uint64(0) {
					return nil, 0, 0, errors.New("invalid message sequence scan")
				}
				from = last + 1
			}
		}
		if page.Done {
			break
		}
		if page.Next == nil {
			return nil, 0, 0, errors.New("message catalog cursor missing")
		}
		req.AfterChannelKey = page.Next.AfterChannelKey
	}
	return counts, rows, channels, nil
}

func (v *nativeView) Close() (err error) {
	if v.closed {
		return nil
	}
	v.closed = true
	if v.log != nil {
		err = errors.Join(err, v.log.Close())
	}
	if v.store != nil {
		err = errors.Join(err, v.store.Close())
	}
	for i := len(v.locks) - 1; i >= 0; i-- {
		err = errors.Join(err, v.locks[i].Close())
	}
	inspectionMu.Unlock()
	return err
}

// CheckBootstrap proves native startup snapshots restore the independently
// verified metadata and planned voters. It then checks the complete sealed file
// inventory, including proposal and external snapshot files, without reopening
// any native store for writing.
func (v *nativeView) CheckBootstrap(ctx context.Context, sourceMaxMessageID uint64) error {
	if v.maxMessageID != sourceMaxMessageID {
		return errors.New("native message ID floor differs from original source maximum")
	}
	snap, err := raftstore.InspectOfflineBootstrap(ctx, raftstore.Config{Dir: filepath.Join(v.node.DataDir, "controller", "raft"), NodeID: v.node.NodeID})
	if err != nil {
		return err
	}
	expected, err := state.Encode(v.layout.state)
	if err != nil {
		return err
	}
	if !bytes.Equal(snap.Data, expected) || snap.Metadata.Index != 1 || snap.Metadata.Term != 1 || !slices.Equal(snap.Metadata.ConfState.Voters, v.layout.nodes) || len(snap.Metadata.ConfState.Learners) != 0 || len(snap.Metadata.ConfState.VotersOutgoing) != 0 {
		return errors.New("Controller bootstrap snapshot differs from planned generation")
	}
	var ids []uint64
	for _, slot := range v.layout.state.Slots {
		if slices.Contains(slot.DesiredPeers, v.node.NodeID) {
			ids = append(ids, uint64(slot.SlotID))
		}
	}
	err = raftlog.InspectOfflineSlotBootstrap(ctx, filepath.Join(v.node.DataDir, "slotraft"), ids, 256<<20, func(id uint64, snap raftpb.Snapshot) error {
		slot := v.layout.state.Slots[id-1]
		if snap.Metadata.Index != 1 || snap.Metadata.Term != 1 || !slices.Equal(snap.Metadata.ConfState.Voters, slot.DesiredPeers) || len(snap.Metadata.ConfState.Learners) != 0 || len(snap.Metadata.ConfState.VotersOutgoing) != 0 {
			return errors.New("Slot bootstrap snapshot voters differ from planned generation")
		}
		var hashes []uint16
		for _, span := range v.layout.state.HashSlots.Ranges {
			if uint64(span.SlotID) == id {
				for h := uint32(span.From); h <= uint32(span.To); h++ {
					hashes = append(hashes, uint16(h))
				}
			}
		}
		reader, err := v.store.Meta().OpenHashSlotSnapshot(ctx, hashes)
		if err != nil {
			return err
		}
		h := sha256.New()
		n, readErr := io.Copy(h, reader)
		if err := errors.Join(readErr, reader.Close()); err != nil {
			return err
		}
		expected := sha256.Sum256(snap.Data)
		if n != int64(len(snap.Data)) || !bytes.Equal(h.Sum(nil), expected[:]) {
			return errors.New("Slot startup snapshot differs from independently verified business metadata")
		}
		return nil
	})
	if err != nil {
		return err
	}
	ready, err := nodeReady(ctx, v.node.DataDir)
	if err != nil {
		return err
	}
	if !ready {
		return errors.New("native generation has no durable publication checkpoint")
	}
	return nil
}
