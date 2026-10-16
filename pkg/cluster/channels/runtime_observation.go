package channels

import (
	"context"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
)

// MaxRuntimeObservations bounds one observational page independently of storage size.
const MaxRuntimeObservations = 200

// RuntimeObservationTarget pins a read-only observation to the resolved leader.
type RuntimeObservationTarget struct {
	ChannelID                           ch.ChannelID
	LeaderID                            ch.NodeID
	Epoch, LeaderEpoch, RouteGeneration uint64
}

// RuntimeObservation contains residency and an optional proved committed frontier.
// Unknown evidence never implies that the Channel is inactive.
type RuntimeObservation struct {
	Target        RuntimeObservationTarget
	Known, Active bool
	MaxMessageSeq *uint64
}

// ObserveLocalChannels reads runtime residency without loading, recovering or
// refreshing Channel metadata in the reactor. Cold sequence observations use
// only the last durable checkpoint; after a crash this may lag acknowledged data
// until ordinary traffic recovers the leader. It never exposes an uncommitted LEO.
func (s *Service) ObserveLocalChannels(parent context.Context, targets []RuntimeObservationTarget, includeMaxMessageSeq bool) ([]RuntimeObservation, error) {
	if len(targets) > MaxRuntimeObservations {
		return nil, ch.ErrInvalidConfig
	}
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	out := make([]RuntimeObservation, len(targets))
	ids := make([]ch.ChannelID, len(targets))
	for i, target := range targets {
		out[i].Target = target
		ids[i] = target.ChannelID
		if target.ChannelID.ID == "" || target.ChannelID.Type == 0 || target.LeaderID == 0 {
			return nil, ch.ErrInvalidConfig
		}
	}
	if len(targets) == 0 {
		return out, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s == nil || s.runtime == nil {
		return nil, ch.ErrNotReady
	}
	probe, ok := s.runtime.(runtimeHWProbe)
	if !ok {
		return nil, ch.ErrNotReady
	}
	before, err := s.resolveReadMetas(ctx, ids)
	if err != nil {
		return nil, err
	}
	observed, err := probe.RuntimeProbe(ctx, ch.RuntimeSelector{ChannelIDs: ids})
	if err != nil {
		return nil, err
	}
	loaded := make(map[ch.ChannelID]ch.RuntimeProbeChannel, len(observed.Channels))
	coverage := make(map[ch.ChannelID]int, len(targets))
	for _, row := range observed.Channels {
		loaded[row.ChannelID] = row
		coverage[row.ChannelID]++
	}
	for _, id := range observed.Missing {
		coverage[id]++
	}
	for i, target := range targets {
		if !s.observationMetaMatches(target, before[i]) || coverage[target.ChannelID] != 1 {
			continue
		}
		row, active := loaded[target.ChannelID]
		if active {
			if row.Role != ch.RoleLeader || row.Status != ch.StatusActive || row.ChannelEpoch != target.Epoch || row.LeaderEpoch != target.LeaderEpoch || row.RouteGeneration != target.RouteGeneration {
				continue
			}
			out[i].Known, out[i].Active = true, true
			if includeMaxMessageSeq && !row.RecoveryRequired && row.HW <= row.LEO {
				out[i].MaxMessageSeq = s.observeMessageTail(ctx, target.ChannelID, &row.HW, before[i].Meta.RetentionThroughSeq)
			}
			continue
		}
		out[i].Known = true
		if includeMaxMessageSeq {
			out[i].MaxMessageSeq = s.observeMessageTail(ctx, target.ChannelID, nil, before[i].Meta.RetentionThroughSeq)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Recheck placement after storage I/O so a concurrent leader change cannot
	// turn a formerly local observation into current-leader evidence.
	after, err := s.resolveReadMetas(ctx, ids)
	if err != nil {
		return nil, err
	}
	for i, target := range targets {
		if !s.observationMetaMatches(target, after[i]) {
			out[i] = RuntimeObservation{Target: target}
		} else if after[i].Meta.RetentionThroughSeq != before[i].Meta.RetentionThroughSeq {
			out[i].MaxMessageSeq = nil
		}
	}
	return out, nil
}

// observeMessageTail preserves Manager tail-row semantics below a captured
// commit frontier, including command SyncOnce rows. It never classifies controls
// from payloads or flags, and never advances the checkpoint. The 64 KiB store
// budget is soft for the single first row, preserving large-message behavior;
// only its sequence crosses the observation RPC boundary.
func (s *Service) observeMessageTail(ctx context.Context, id ch.ChannelID, liveHW *uint64, retentionFloor uint64) (seq *uint64) {
	if s.store == nil {
		return nil
	}
	handle, err := s.store.ChannelStore(ch.ChannelKeyForID(id), id)
	if err != nil {
		return nil
	}
	defer func() {
		if handle.Close() != nil {
			seq = nil
		}
	}()
	var hw uint64
	if liveHW != nil {
		hw = *liveHW
	} else {
		state, err := handle.Load(ctx)
		if err != nil || state.CheckpointHW > state.LEO {
			return nil
		}
		hw = state.CheckpointHW
	}
	retention, err := handle.LoadRetentionState(ctx)
	if err != nil {
		return nil
	}
	retentionFloor = max(retentionFloor, retention.LocalRetentionThroughSeq)
	if hw <= retentionFloor {
		return new(uint64)
	}
	read, err := handle.ReadCommitted(ctx, channelstore.ReadCommittedRequest{FromSeq: hw, MaxSeq: hw, MinSeq: nextSeq(retentionFloor), Limit: 1, MaxBytes: 64 * 1024, Reverse: true})
	if err != nil {
		return nil
	}
	if len(read.Messages) == 0 {
		return new(uint64)
	}
	message := read.Messages[0]
	if message.MessageSeq == 0 || message.MessageSeq > hw || message.MessageSeq <= retentionFloor {
		return nil
	}
	value := message.MessageSeq
	return &value
}

func (s *Service) observationMetaMatches(target RuntimeObservationTarget, result ChannelMetaResult) bool {
	m := result.Meta
	return result.Err == nil && result.Found && m.ID == target.ChannelID && m.Status == ch.StatusActive && m.Leader == s.localNode && m.Leader == target.LeaderID && m.Epoch == target.Epoch && m.LeaderEpoch == target.LeaderEpoch && m.RouteGeneration == target.RouteGeneration
}
