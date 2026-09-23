package message

import (
	"bytes"
	"context"
	"encoding/binary"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/rowcodec"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
)

// MQTTReplayRetirementProof retains the committed decision independently of
// original history. It does not materialize a pruned replay baseline or delete.
type MQTTReplayRetirementProof struct {
	Retirement quorumlog.MQTTReplayRetirement
	Manifest   DurableProposalManifest
}

func mqttReplayRetirementPrefix(key ChannelKey) []byte {
	return encodeMessageSystemPrefix(key, messageSystemIDMQTTReplayRetirement)
}

func mqttReplayRetirementKey(key ChannelKey, position uint64) []byte {
	return binary.BigEndian.AppendUint64(mqttReplayRetirementPrefix(key), position)
}

func mqttReplayRetirementPosition(key ChannelKey, k []byte) (uint64, bool) {
	p := mqttReplayRetirementPrefix(key)
	if len(k) != len(p)+8 || !bytes.HasPrefix(k, p) {
		return 0, false
	}
	n := binary.BigEndian.Uint64(k[len(p):])
	return n, n != 0
}

func decodeMQTTRetirementJournal(key ChannelKey, position uint64, value []byte) (messageRow, quorumlog.MQTTReplayRetirement, error) {
	var r quorumlog.MQTTReplayRetirement
	if len(value) > rowcodec.EnvelopeLen(mqttReplayAnchorMaxBytes) {
		return messageRow{}, r, dberrors.ErrCorruptValue
	}
	env, err := rowcodec.UnwrapBorrowed(mqttReplayRetirementKey(key, position), value)
	if err != nil {
		return messageRow{}, r, err
	}
	if env.Version != 1 || env.Codec != rowcodec.CodecFixed || env.Flags != rowcodec.FlagChecksum {
		return messageRow{}, r, dberrors.ErrCorruptValue
	}
	row, err := mqttReplayOriginalRow(key, position, env.Payload)
	if err != nil {
		return row, r, err
	}
	content, err := canonicalMQTTControlContent(key, row)
	if err != nil || !bytes.Equal(content, env.Payload) {
		return row, r, dberrors.ErrCorruptValue
	}
	r, err = quorumlog.DecodeMQTTReplayRetirement(row.Payload)
	if err != nil || r.AnchorPosition >= position {
		return row, r, dberrors.ErrCorruptValue
	}
	return row, r, nil
}

func validMQTTRetirementAdvance(previous, next quorumlog.MQTTReplayRetirement) bool {
	if previous == (quorumlog.MQTTReplayRetirement{}) {
		return true
	}
	p, n := previous.Anchor, next.Anchor
	if p.SourceCommand != n.SourceCommand || p.StartAfter != n.StartAfter || n.Through < p.Through {
		return false
	}
	if n.Through == p.Through {
		return previous == next
	}
	return n.TotalBytes >= p.TotalBytes && n.TotalStoredBytes > p.TotalStoredBytes
}

// latestMQTTRetirement includes retained pending controls only for staging's
// monotonicity check. Its caller holds append/checkpoint ownership; no GC proof
// is returned from this helper.
func latestMQTTRetirement(view messageBackupReadView, key ChannelKey, through uint64) (quorumlog.MQTTReplayRetirement, error) {
	span := keycodec.NewPrefixSpan(mqttReplayRetirementPrefix(key))
	if through != ^uint64(0) {
		span.End = mqttReplayRetirementKey(key, through+1)
	}
	iter, err := view.NewIter(engine.Span{Start: span.Start, End: span.End}, engine.IterOptions{})
	if err != nil {
		return quorumlog.MQTTReplayRetirement{}, err
	}
	defer iter.Close()
	if !iter.Last() {
		return quorumlog.MQTTReplayRetirement{}, iter.Error()
	}
	position, ok := mqttReplayRetirementPosition(key, iter.Key())
	if !ok {
		return quorumlog.MQTTReplayRetirement{}, dberrors.ErrCorruptState
	}
	value, err := iter.Value()
	if err != nil {
		return quorumlog.MQTTReplayRetirement{}, err
	}
	row, r, err := decodeMQTTRetirementJournal(key, position, value)
	if err != nil {
		return r, err
	}
	entry, manifest, err := mqttReplayCommittedEntry(view, key, position, through)
	if err != nil || manifest.Version != quorumlog.MQTTReplayRetirementProposalManifestVersion || !verifyBackupRowIdentity(entry, row) {
		return quorumlog.MQTTReplayRetirement{}, dberrors.ErrCorruptState
	}
	return r, nil
}

// retirementAnchorForStage supports a committed existing anchor or an earlier
// anchor in this atomic recovery batch covered by its resulting commit frontier.
func (e *channelEntry) retirementAnchorForStage(r quorumlog.MQTTReplayRetirement, rows map[uint64]messageRow, proposals []durableProposalRecord, hw, keepThrough uint64) (MQTTReplayAnchorProof, error) {
	for _, p := range proposals {
		if p.manifest.LastOffset != r.AnchorPosition {
			continue
		}
		row, found := rows[r.AnchorPosition]
		if !found || p.manifest.Version != quorumlog.MQTTReplayAnchorProposalManifestVersion || r.AnchorPosition > hw {
			return MQTTReplayAnchorProof{}, dberrors.ErrConflict
		}
		a, err := quorumlog.DecodeMQTTReplayAnchor(row.Payload)
		return MQTTReplayAnchorProof{Anchor: a, Manifest: p.manifest}, err
	}
	if r.AnchorPosition > hw || r.AnchorPosition > keepThrough {
		return MQTTReplayAnchorProof{}, dberrors.ErrConflict
	}
	value, found, err := e.db.engine.Get(mqttReplayAnchorKey(e.key, r.AnchorPosition))
	if err != nil {
		return MQTTReplayAnchorProof{}, err
	}
	if !found {
		return MQTTReplayAnchorProof{}, dberrors.ErrCorruptState
	}
	row, a, err := decodeMQTTAnchorJournal(e.key, r.AnchorPosition, value)
	if err != nil {
		return MQTTReplayAnchorProof{}, err
	}
	entry, manifest, err := mqttReplayCommittedEntry(e.db.engine, e.key, r.AnchorPosition, hw)
	if err != nil || manifest.Version != quorumlog.MQTTReplayAnchorProposalManifestVersion || !verifyBackupRowIdentity(entry, row) {
		return MQTTReplayAnchorProof{}, dberrors.ErrCorruptState
	}
	return MQTTReplayAnchorProof{Anchor: a, Manifest: manifest}, nil
}

// stageMQTTReplayRetirements shares exact append's synchronous transaction.
// It records a decision only; replay content and source release remain untouched.
func (e *channelEntry) stageMQTTReplayRetirements(batch *engine.Batch, rows []messageRow, checkpoint *Checkpoint, proposals []durableProposalRecord, keepThrough uint64) error {
	hasRetirement := false
	for _, p := range proposals {
		hasRetirement = hasRetirement || p.manifest.Version == quorumlog.MQTTReplayRetirementProposalManifestVersion
	}
	if !hasRetirement {
		return nil
	}
	previous, err := latestMQTTRetirement(e.db.engine, e.key, keepThrough)
	if err != nil {
		return err
	}
	evidence, err := readMQTTActivationEvidence(e.db.engine, e.key)
	if err != nil {
		return err
	}
	cp := evidence.checkpoint
	if checkpoint != nil {
		cp = *checkpoint
	}
	activation := evidence.manifest
	if !evidence.present || activation.LastOffset > keepThrough {
		activation = DurableProposalManifest{}
	}
	if activation.LastOffset == 0 {
		for _, p := range proposals {
			if p.manifest.Version == quorumlog.MQTTSourceProposalManifestVersion {
				activation = p.manifest
				break
			}
		}
	}
	if activation.LastOffset == 0 || activation.LastOffset > cp.HW {
		return dberrors.ErrConflict
	}
	byPosition := make(map[uint64]messageRow, len(rows))
	for _, row := range rows {
		byPosition[row.MessageSeq] = row
	}
	for _, p := range proposals {
		if p.manifest.Version != quorumlog.MQTTReplayRetirementProposalManifestVersion {
			continue
		}
		row, found := byPosition[p.manifest.LastOffset]
		if !found {
			return dberrors.ErrCorruptState
		}
		r, err := quorumlog.DecodeMQTTReplayRetirement(row.Payload)
		if err != nil || r.AnchorPosition >= p.manifest.LastOffset || r.Anchor.SourceCommand != activation.CommandID || r.Anchor.StartAfter != activation.BaseOffset || !validMQTTRetirementAdvance(previous, r) {
			return dberrors.ErrConflict
		}
		anchor, err := e.retirementAnchorForStage(r, byPosition, proposals, cp.HW, keepThrough)
		if err != nil {
			return err
		}
		if anchor.Anchor != r.Anchor || anchor.Manifest.Digest != r.AnchorDigest {
			return dberrors.ErrConflict
		}
		content, err := canonicalMQTTControlContent(e.key, row)
		if err != nil {
			return err
		}
		key := mqttReplayRetirementKey(e.key, row.MessageSeq)
		if err = batch.Set(key, rowcodec.Wrap(key, 1, rowcodec.CodecFixed, rowcodec.FlagChecksum, content)); err != nil {
			return err
		}
		previous = r
	}
	return nil
}

// LoadMQTTReplayRetirement proves a covered decision and its exact accepted
// anchor in one snapshot, independently of original message retention.
func (l *ChannelLog) LoadMQTTReplayRetirement(ctx context.Context, position uint64) (MQTTReplayRetirementProof, bool, error) {
	if position == 0 {
		return MQTTReplayRetirementProof{}, false, dberrors.ErrInvalidArgument
	}
	if err := l.beginUse(); err != nil {
		return MQTTReplayRetirementProof{}, false, err
	}
	defer l.endUse()
	if err := ctxErr(ctx); err != nil {
		return MQTTReplayRetirementProof{}, false, err
	}
	view, err := l.db.engine.NewSnapshot()
	if err != nil {
		return MQTTReplayRetirementProof{}, false, err
	}
	defer view.Close()
	return loadMQTTReplayRetirementFrom(view, l.key, position)
}

func loadMQTTReplayRetirementFrom(view proposalReadView, key ChannelKey, position uint64) (MQTTReplayRetirementProof, bool, error) {
	var empty MQTTReplayRetirementProof
	value, found, err := view.Get(mqttReplayRetirementKey(key, position))
	if err != nil {
		return empty, false, err
	}
	if !found {
		entry, present, err := loadDurableEntryIdentityFrom(view, key, position)
		if err != nil {
			return empty, false, err
		}
		if present && entry.Version == quorumlog.MQTTReplayRetirementProposalManifestVersion {
			return empty, false, dberrors.ErrCorruptState
		}
		return empty, false, nil
	}
	row, r, err := decodeMQTTRetirementJournal(key, position, value)
	if err != nil {
		return empty, false, err
	}
	evidence, err := readMQTTActivationEvidence(view, key)
	if err != nil {
		return empty, false, err
	}
	if !evidence.present || !evidence.sourcePresent {
		return empty, false, dberrors.ErrCorruptState
	}
	if position > evidence.checkpoint.HW {
		return empty, false, nil
	}
	anchor, found, err := loadMQTTReplayAnchorFrom(view, key, r.AnchorPosition)
	if err != nil {
		return empty, false, err
	}
	if !found || anchor.Anchor != r.Anchor || anchor.Manifest.Digest != r.AnchorDigest {
		return empty, false, dberrors.ErrCorruptState
	}
	entry, manifest, err := mqttReplayCommittedEntry(view, key, position, evidence.checkpoint.HW)
	if err != nil {
		return empty, false, err
	}
	if manifest.Version != quorumlog.MQTTReplayRetirementProposalManifestVersion || !verifyBackupRowIdentity(entry, row) {
		return empty, false, dberrors.ErrCorruptState
	}
	return MQTTReplayRetirementProof{Retirement: r, Manifest: manifest}, true, nil
}

func (s *ChannelStore) LoadMQTTReplayRetirement(ctx context.Context, position uint64) (MQTTReplayRetirementProof, bool, error) {
	if err := s.beginUse(); err != nil {
		return MQTTReplayRetirementProof{}, false, err
	}
	defer s.endUse()
	proof, found, err := s.log.LoadMQTTReplayRetirement(ctx, position)
	return proof, found, toChannelError(err)
}

func (e *channelEntry) stageTruncateMQTTReplayRetirements(batch *engine.Batch, to uint64) error {
	if to == ^uint64(0) {
		return nil
	}
	span := keycodec.NewPrefixSpan(mqttReplayRetirementPrefix(e.key))
	return batch.DeleteRange(engine.Span{Start: mqttReplayRetirementKey(e.key, to+1), End: span.End})
}
