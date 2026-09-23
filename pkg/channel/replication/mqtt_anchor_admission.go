package replication

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"slices"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
)

// MQTTReplayAnchorAdmission carries fresh authority plus independently collected
// current-voter copy evidence. The entry must recheck fresh metadata around this
// call. MessageID is server allocated; an uncertain retry retains the first row.
type MQTTReplayAnchorAdmission struct {
	Meta              ch.Meta
	Copy              ch.MQTTReplayCopyReceipt
	MessageID         uint64
	ServerTimestampMS int64
}

// MQTTReplayAnchorCommitter admits one bounded accepted content interval through
// the same sequencer as business writes. The returned proof never authorizes GC.
type MQTTReplayAnchorCommitter interface {
	CommitMQTTReplayAnchor(context.Context, MQTTReplayAnchorAdmission) (ch.MQTTReplayAnchorProof, error)
}

// mqttAnchorStateStore checkpoints only the installed sequencer's HW before
// reading one pinned source/latest/command view. It owns each temporary lease.
type mqttAnchorStateStore interface {
	prepareMQTTReplayAnchors(context.Context, ch.ChannelKey, ch.ChannelID, uint64, ch.CommandID) (ch.MQTTReplayAnchorState, error)
}

func (a *storeAdapter) prepareMQTTReplayAnchors(ctx context.Context, key ch.ChannelKey, id ch.ChannelID, hw uint64, command ch.CommandID) (ch.MQTTReplayAnchorState, error) {
	if !a.supportsMQTTAnchors() {
		return ch.MQTTReplayAnchorState{}, ch.ErrInvalidConfig
	}
	st, err := a.cfg.Factory.ChannelStore(key, id)
	if err != nil {
		return ch.MQTTReplayAnchorState{}, err
	}
	if st == nil {
		return ch.MQTTReplayAnchorState{}, ch.ErrInvalidConfig
	}
	defer st.Close()
	reader, ok := st.(channelstore.MQTTReplayAnchorStateReader)
	if !ok {
		return ch.MQTTReplayAnchorState{}, ch.ErrInvalidConfig
	}
	if err = st.StoreCheckpoint(ctx, ch.Checkpoint{HW: hw}); err != nil {
		return ch.MQTTReplayAnchorState{}, err
	}
	return reader.ReadMQTTReplayAnchors(ctx, hw, command)
}

func mqttAnchorAuthority(m ch.Meta) Authority {
	a := Authority{Key: ch.ChannelKeyForID(m.ID), ChannelID: m.ID, ID: AuthorityID{ChannelEpoch: m.Epoch, LeaderTerm: m.LeaderEpoch, FenceVersion: m.RouteGeneration}, Leader: m.Leader, Voters: m.ISR, WriteQuorum: m.MinISR, WriteFence: m.WriteFence}
	for _, n := range m.Replicas {
		if !slices.Contains(m.ISR, n) {
			a.Learners = append(a.Learners, n)
		}
	}
	return a
}

// mqttAnchorCommand is stable across leadership and caller retry identities.
// Conflicting content at the same source/Through must fail, never get a new ID.
func mqttAnchorCommand(anchor quorumlog.MQTTReplayAnchor) ch.CommandID {
	b := append([]byte("wukongim/channel/mqtt-replay-anchor-command/v1\x00"), anchor.SourceCommand[:]...)
	b = binary.BigEndian.AppendUint64(b, anchor.Through)
	return sha256.Sum256(b)
}

func (l *quorumLog) CommitMQTTReplayAnchor(ctx context.Context, q MQTTReplayAnchorAdmission) (ch.MQTTReplayAnchorProof, error) {
	var empty ch.MQTTReplayAnchorProof
	if l == nil || ctx == nil || q.MessageID == 0 || q.ServerTimestampMS <= 0 || !q.Copy.ValidFor(q.Meta) {
		return empty, ch.ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	authority := mqttAnchorAuthority(q.Meta)
	if authority.Leader != l.cfg.Local {
		return empty, ch.ErrNotLeader
	}
	reader, ok := l.cfg.Store.(mqttAnchorStateStore)
	if !ok {
		return empty, ch.ErrInvalidConfig
	}
	state := l.existingChannel(authority.Key)
	if state == nil {
		return empty, ch.ErrNotReady
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	if state.released || !state.ready {
		return empty, ch.ErrNotReady
	}
	if !sameAuthority(state.authority, authority) {
		return empty, ch.ErrStaleMeta
	}
	if state.authority.WriteFence.Set() {
		return empty, ch.ErrWriteFenced
	}
	if q.Copy.After.Through > state.hw {
		return empty, ch.ErrLogConflict
	}
	after := q.Copy.After
	anchor := quorumlog.MQTTReplayAnchor{StartAfter: after.StartAfter, Through: after.Through, TotalBytes: after.TotalBytes, TotalStoredBytes: after.TotalStoredBytes, Digest: after.Digest}
	// ValidFor already requires the exact canonical generation spelling.
	if _, err := hex.Decode(anchor.SourceCommand[:], []byte(after.Generation[len("mqtt-log-v1:"):])); err != nil {
		return empty, ch.ErrInvalidConfig
	}
	command := mqttAnchorCommand(anchor)
	current, err := reader.prepareMQTTReplayAnchors(ctx, authority.Key, authority.ChannelID, state.hw, command)
	if err != nil {
		return empty, err
	}
	if err = ctx.Err(); err != nil {
		return empty, err
	}
	if current.Source.Generation != after.Generation || current.Source.StartAfter != after.StartAfter || current.Source.CommittedThrough != state.hw {
		return empty, ch.ErrLogConflict
	}
	if current.HasRequested {
		if current.Requested.Anchor != anchor || current.Requested.Manifest.CommandID != command {
			return empty, ch.ErrLogConflict
		}
		return current.Requested, nil
	}
	before := ch.MQTTReplayPrefix{Generation: current.Source.Generation, StartAfter: current.Source.StartAfter, Through: current.Source.StartAfter}
	if current.HasLatest {
		before = current.Latest.Prefix()
	}
	if q.Copy.Before != before {
		return empty, ch.ErrLogConflict
	}
	if current.HasLatest && current.Latest.Manifest.LastOffset == state.hw && before.Through+1 == state.hw && after.Through == state.hw {
		return current.Latest, nil
	}
	if state.pending != nil {
		p := state.pending.proposal
		if p.manifest.CommandID != command {
			return empty, ch.ErrBackpressured
		}
		if p.manifest.Version != quorumlog.MQTTReplayAnchorProposalManifestVersion || len(p.records) != 1 {
			return empty, ch.ErrLogConflict
		}
		recorded, e := quorumlog.DecodeMQTTReplayAnchor(p.records[0].Payload)
		if e != nil || recorded != anchor {
			return empty, ch.ErrLogConflict
		}
		if _, err = l.retryPending(ctx, state, *state.pending); err != nil {
			return empty, err
		}
	} else {
		payload, e := anchor.MarshalBinary()
		if e != nil {
			return empty, ch.ErrInvalidConfig
		}
		proposal := Proposal{Key: authority.Key, Expected: authority.ID, CommandID: command, PayloadsImmutable: true, ServerAllocatedMessageIDs: true, MQTTReplayAnchor: true,
			Records: []ch.Record{{ID: q.MessageID, Epoch: authority.ID.ChannelEpoch, ServerTimestampMS: q.ServerTimestampMS, SyncOnce: true, Payload: payload, SizeBytes: len(payload)}}}
		if !validProposalRecords(proposal.Records, l.cfg.MaxProposalBytes) {
			return empty, ch.ErrBackpressured
		}
		if _, err = l.commitLocked(ctx, state, proposal); err != nil {
			return empty, err
		}
	}
	committed, err := reader.prepareMQTTReplayAnchors(ctx, authority.Key, authority.ChannelID, state.hw, command)
	if err != nil {
		return empty, err
	}
	if err = ctx.Err(); err != nil {
		return empty, err
	}
	if !committed.HasRequested || committed.Requested.Anchor != anchor || committed.Requested.Manifest.CommandID != command {
		return empty, ch.ErrLogConflict
	}
	return committed.Requested, nil
}

var _ MQTTReplayAnchorCommitter = (*quorumLog)(nil)
