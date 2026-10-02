package message

import (
	"context"
	"math"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/rowcodec"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
)

func mqttActivationKey(key ChannelKey) []byte {
	return encodeMessageSystemPrefix(key, messageSystemIDMQTTActivation)
}

func decodeMQTTActivation(key, value []byte) (DurableProposalManifest, error) {
	if len(value) != rowcodec.EnvelopeLen(durableProposalRecordSize) {
		return DurableProposalManifest{}, dberrors.ErrCorruptValue
	}
	env, err := rowcodec.UnwrapBorrowed(key, value)
	if err != nil {
		return DurableProposalManifest{}, err
	}
	if env.Version != 1 || env.Codec != rowcodec.CodecFixed || env.Flags != rowcodec.FlagChecksum {
		return DurableProposalManifest{}, dberrors.ErrCorruptValue
	}
	r, err := decodeDurableProposalRecord(env.Payload)
	if err != nil || r.manifest.Version != quorumlog.MQTTSourceProposalManifestVersion {
		return DurableProposalManifest{}, dberrors.ErrCorruptValue
	}
	return r.manifest, nil
}

func loadMQTTActivation(view proposalReadView, key ChannelKey) (DurableProposalManifest, bool, error) {
	k := mqttActivationKey(key)
	b, ok, err := view.Get(k)
	if err != nil || !ok {
		return DurableProposalManifest{}, ok, err
	}
	m, err := decodeMQTTActivation(k, b)
	return m, err == nil, err
}

// mqttActivationEvidence is read under checkpoint ownership or one pinned
// snapshot. Separate uncoordinated reads could straddle materialization.
type mqttActivationEvidence struct {
	// manifest identifies the first control, including while it is uncommitted.
	manifest DurableProposalManifest
	present  bool
	// source is materialized only when the same view's HW covers the control.
	source            MQTTSourceState
	sourcePresent     bool
	checkpoint        Checkpoint
	checkpointPresent bool
}

// readMQTTActivationEvidence rejects missing or contradictory projections; it
// never repairs them from a checkpoint or treats a local marker as authority.
func readMQTTActivationEvidence(view proposalReadView, key ChannelKey) (mqttActivationEvidence, error) {
	var out mqttActivationEvidence
	var err error
	out.manifest, out.present, err = loadMQTTActivation(view, key)
	if err != nil {
		return out, err
	}
	b, ok, err := view.Get(mqttSourceKey(key))
	if err != nil {
		return out, err
	}
	out.sourcePresent = ok
	if ok {
		out.source, err = decodeMQTTSourceState(mqttSourceKey(key), b)
		if err != nil {
			return out, err
		}
	}
	b, ok, err = view.Get(encodeCheckpointKey(key))
	if err != nil {
		return out, err
	}
	out.checkpointPresent = ok
	if ok {
		out.checkpoint, err = decodeCheckpoint(b)
		if err != nil {
			return out, err
		}
	}
	if out.sourcePresent && (!ok || validateCheckpoint(out.checkpoint) != nil || out.source.CopiedThrough > out.checkpoint.HW) {
		return out, dberrors.ErrCorruptState
	}
	if !out.present {
		if strings.HasPrefix(out.source.Generation, "mqtt-log-v1:") {
			return out, dberrors.ErrCorruptState
		}
		return out, nil
	}
	if !ok || validateCheckpoint(out.checkpoint) != nil {
		return out, dberrors.ErrCorruptState
	}
	committed := out.checkpoint.HW >= out.manifest.LastOffset
	if committed != out.sourcePresent {
		return out, dberrors.ErrCorruptState
	}
	if committed && (out.source.Generation != quorumlog.MQTTSourceGeneration(out.manifest.CommandID) || out.source.StartAfter != out.manifest.BaseOffset) {
		return out, dberrors.ErrCorruptState
	}
	return out, nil
}

// stageMQTTActivation projects exact controls and HW in their owning physical
// batch. keepThrough is MaxUint64 unless replacing/truncating an uncommitted
// suffix. The caller owns checkpointMu; new controls also own appendMu.
func (e *channelEntry) stageMQTTActivation(batch *engine.Batch, checkpoint *Checkpoint, proposals []durableProposalRecord, keepThrough uint64) error {
	hasControl := false
	for _, p := range proposals {
		hasControl = hasControl || p.manifest.Version == quorumlog.MQTTSourceProposalManifestVersion
	}
	if checkpoint == nil && !hasControl && keepThrough == math.MaxUint64 {
		return nil
	}
	_, exists, err := loadMQTTActivation(e.db.engine, e.key)
	if err != nil {
		return err
	}
	if !exists && !hasControl {
		return nil
	}
	current, err := readMQTTActivationEvidence(e.db.engine, e.key)
	if err != nil {
		return err
	}
	candidate, present := current.manifest, current.present
	if present && candidate.LastOffset > keepThrough {
		if current.sourcePresent || current.checkpoint.HW >= candidate.LastOffset {
			return dberrors.ErrConflict
		}
		present = false
		if err := batch.Delete(mqttActivationKey(e.key)); err != nil {
			return err
		}
	}
	for _, p := range proposals {
		if p.manifest.Version != quorumlog.MQTTSourceProposalManifestVersion {
			continue
		}
		if !present {
			if current.sourcePresent {
				return dberrors.ErrConflict
			}
			candidate, present = p.manifest, true
			key := mqttActivationKey(e.key)
			value := rowcodec.Wrap(key, 1, rowcodec.CodecFixed, rowcodec.FlagChecksum, encodeDurableProposalRecord(p))
			if err := batch.Set(key, value); err != nil {
				return err
			}
		}
	}
	if !present {
		return nil
	}
	next := current.checkpoint
	if checkpoint != nil {
		next = *checkpoint
	}
	if !current.checkpointPresent && checkpoint == nil {
		return dberrors.ErrCorruptState
	}
	if next.HW < current.checkpoint.HW || validateCheckpoint(next) != nil {
		return dberrors.ErrConflict
	}
	limit := candidate.BaseOffset
	if current.sourcePresent {
		limit = current.source.CopiedThrough
	}
	if b, ok, err := e.db.engine.Get(encodeRetentionStateKey(e.key)); err != nil {
		return err
	} else if ok {
		r, err := decodeRetentionState(b)
		if err != nil {
			return err
		}
		if r.PhysicalRetentionThroughSeq > limit {
			return dberrors.ErrCorruptState
		}
	}
	if next.HW >= candidate.LastOffset && !current.sourcePresent {
		source := MQTTSourceState{Generation: quorumlog.MQTTSourceGeneration(candidate.CommandID), Revision: 1, StartAfter: candidate.BaseOffset, CopiedThrough: candidate.BaseOffset}
		key := mqttSourceKey(e.key)
		if err := batch.Set(key, encodeMQTTSourceState(key, source)); err != nil {
			return err
		}
	}
	return nil
}

// mqttActivationCheckpoint pins only activated/pending logs. Native checkpoints
// use one additional bounded marker lookup and do not allocate a snapshot.
func (l *ChannelLog) mqttActivationCheckpoint(ctx context.Context, source MQTTSourceState) (Checkpoint, bool, bool, error) {
	if err := ctx.Err(); err != nil {
		return Checkpoint{}, false, true, err
	}
	_, present, err := loadMQTTActivation(l.db.engine, l.key)
	if err != nil {
		return Checkpoint{}, false, true, err
	}
	if !present && !strings.HasPrefix(source.Generation, "mqtt-log-v1:") {
		return Checkpoint{}, false, false, nil
	}
	view, err := l.db.engine.NewSnapshot()
	if err != nil {
		return Checkpoint{}, false, true, err
	}
	defer view.Close()
	evidence, err := readMQTTActivationEvidence(view, l.key)
	return evidence.checkpoint, evidence.checkpointPresent, true, err
}

func validateMQTTActivationBackup(key ChannelKey, hw uint64, entries []backupRawEntry, proposals map[uint64]durableProposalRecord) error {
	var activation DurableProposalManifest
	var source MQTTSourceState
	for _, entry := range entries {
		switch string(entry.Key) {
		case string(mqttActivationKey(key)):
			m, err := decodeMQTTActivation(entry.Key, entry.Value)
			if err != nil {
				return err
			}
			activation = m
		case string(mqttSourceKey(key)):
			s, err := decodeMQTTSourceState(entry.Key, entry.Value)
			if err != nil {
				return err
			}
			source = s
		}
	}
	var first DurableProposalManifest
	for _, p := range proposals {
		if p.manifest.Version == quorumlog.MQTTSourceProposalManifestVersion && (first.LastOffset == 0 || p.manifest.LastOffset < first.LastOffset) {
			first = p.manifest
		}
	}
	if first.LastOffset == 0 && activation.LastOffset == 0 && !strings.HasPrefix(source.Generation, "mqtt-log-v1:") {
		return nil
	}
	if activation.LastOffset == 0 || activation != first || activation.LastOffset > hw || source.Generation != quorumlog.MQTTSourceGeneration(activation.CommandID) || source.StartAfter != activation.BaseOffset || source.CopiedThrough > hw {
		return dberrors.ErrCorruptState
	}
	return nil
}
