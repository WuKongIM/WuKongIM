package replication

import (
	"context"
	"crypto/sha256"
	"encoding/binary"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
)

// mqttRetirementCommand is stable across caller identities and leadership.
// A conflicting equal prefix must fail rather than acquire another command ID.
func mqttRetirementCommand(r quorumlog.MQTTReplayRetirement) ch.CommandID {
	b := append([]byte("wukongim/channel/mqtt-replay-retirement-command/v1\x00"), r.Anchor.SourceCommand[:]...)
	return sha256.Sum256(binary.BigEndian.AppendUint64(b, r.Anchor.Through))
}

// CommitMQTTReplayRetirement shares the installed sequencer with business and
// anchor appends. The product caller supplies ordered consumer permission; the
// owner independently proves source, anchors, authority and exact durable retry.
func (l *quorumLog) CommitMQTTReplayRetirement(ctx context.Context, q ch.MQTTReplayRetirementRequest) (ch.MQTTReplayRetirementProof, error) {
	var empty ch.MQTTReplayRetirementProof
	if l == nil || ctx == nil || !q.Valid() {
		return empty, ch.ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	q = q.Clone()
	authority := mqttAnchorAuthority(q.Meta)
	if !validAuthority(authority) {
		return empty, ch.ErrInvalidConfig
	}
	if authority.Leader != l.cfg.Local {
		return empty, ch.ErrNotLeader
	}
	reader, ok := l.cfg.Store.(mqttRetirementAdmissionStore)
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
	if q.Captured.Manifest.LastOffset > state.hw {
		return empty, ch.ErrLogConflict
	}
	decision, err := q.Retirement()
	if err != nil {
		return empty, err
	}
	current, err := reader.prepareMQTTReplayRetirement(ctx, authority.Key, authority.ChannelID, state.hw, q)
	if err != nil {
		return empty, err
	}
	if err = ctx.Err(); err != nil {
		return empty, err
	}
	if current.hasLatest {
		if current.latest.Retirement.Anchor.Through >= decision.Anchor.Through {
			if !q.AcceptsProof(current.latest) {
				return empty, ch.ErrLogConflict
			}
			return current.latest, nil
		}
		// A new decision must extend the already committed immutable prefix.
		p, n := current.latest.Retirement.Anchor, decision.Anchor
		if !current.latest.Retirement.Valid() || !current.latest.Manifest.StructurallyValid() || current.latest.Manifest.Version != quorumlog.MQTTReplayRetirementProposalManifestVersion ||
			p.SourceCommand != n.SourceCommand || p.StartAfter != n.StartAfter || p.TotalBytes > n.TotalBytes || p.TotalStoredBytes >= n.TotalStoredBytes {
			return empty, ch.ErrLogConflict
		}
	}
	command := mqttRetirementCommand(decision)
	if state.pending != nil {
		p := state.pending.proposal
		if p.manifest.CommandID != command {
			return empty, ch.ErrBackpressured
		}
		if p.manifest.Version != quorumlog.MQTTReplayRetirementProposalManifestVersion || len(p.records) != 1 {
			return empty, ch.ErrLogConflict
		}
		retained, e := quorumlog.DecodeMQTTReplayRetirement(p.records[0].Payload)
		if e != nil || retained != decision {
			return empty, ch.ErrLogConflict
		}
		if _, err = l.retryPending(ctx, state, *state.pending); err != nil {
			return empty, err
		}
	} else {
		payload, e := decision.MarshalBinary()
		if e != nil {
			return empty, ch.ErrInvalidConfig
		}
		proposal := Proposal{Key: authority.Key, Expected: authority.ID, CommandID: command, PayloadsImmutable: true, ServerAllocatedMessageIDs: true, MQTTReplayRetirement: true,
			Records: []ch.Record{{ID: q.MessageID, Epoch: authority.ID.ChannelEpoch, ServerTimestampMS: q.ServerTimestampMS, SyncOnce: true, Payload: payload, SizeBytes: len(payload)}}}
		if !validProposalRecords(proposal.Records, l.cfg.MaxProposalBytes) {
			return empty, ch.ErrBackpressured
		}
		if _, err = l.commitLocked(ctx, state, proposal); err != nil {
			return empty, err
		}
	}
	committed, err := reader.prepareMQTTReplayRetirement(ctx, authority.Key, authority.ChannelID, state.hw, q)
	if err != nil {
		return empty, err
	}
	if err = ctx.Err(); err != nil {
		return empty, err
	}
	if !committed.hasLatest || committed.latest.Manifest.CommandID != command || committed.latest.Retirement != decision || !q.AcceptsProof(committed.latest) {
		return empty, ch.ErrLogConflict
	}
	return committed.latest, nil
}

var _ ch.MQTTReplayRetirementCommitter = (*quorumLog)(nil)
