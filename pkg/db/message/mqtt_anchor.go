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

const mqttReplayAnchorMaxBytes = 4096

// MQTTReplayAnchorProof is a locally committed full-content anchor, verified
// independently of donor pages. It does not prove current membership/readiness.
type MQTTReplayAnchorProof struct {
	Anchor   quorumlog.MQTTReplayAnchor
	Manifest DurableProposalManifest
}

func mqttReplayAnchorPrefix(key ChannelKey) []byte {
	return encodeMessageSystemPrefix(key, messageSystemIDMQTTReplayAnchor)
}
func mqttReplayAnchorKey(key ChannelKey, position uint64) []byte {
	return binary.BigEndian.AppendUint64(mqttReplayAnchorPrefix(key), position)
}
func mqttReplayAnchorPosition(key ChannelKey, k []byte) (uint64, bool) {
	p := mqttReplayAnchorPrefix(key)
	if len(k) != len(p)+8 || !bytes.HasPrefix(k, p) {
		return 0, false
	}
	n := binary.BigEndian.Uint64(k[len(p):])
	return n, n != 0
}

// canonicalMQTTAnchorContent excludes every native business-only field and
// normalizes the replica-local size hint before retaining the control envelope.
func canonicalMQTTAnchorContent(key ChannelKey, row messageRow) ([]byte, error) {
	expected := messageRow{MessageID: row.MessageID, MessageSeq: row.MessageSeq, ChannelID: row.ChannelID, ChannelType: row.ChannelType, ServerTimestampMS: row.ServerTimestampMS, FramerFlags: 4, Payload: row.Payload, PayloadSize: uint64(len(row.Payload))}
	row.PayloadSize = uint64(len(row.Payload))
	k := encodeMessageRowKey(key, row.MessageSeq, 0)
	got, err := encodeMessageHeader(k, row)
	if err != nil {
		return nil, err
	}
	want, err := encodeMessageHeader(k, expected)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(got, want) || len(want) > mqttReplayAnchorMaxBytes {
		return nil, dberrors.ErrInvalidArgument
	}
	return want, nil
}

func decodeMQTTAnchorJournal(key ChannelKey, position uint64, value []byte) (messageRow, quorumlog.MQTTReplayAnchor, error) {
	var a quorumlog.MQTTReplayAnchor
	if len(value) > rowcodec.EnvelopeLen(mqttReplayAnchorMaxBytes) {
		return messageRow{}, a, dberrors.ErrCorruptValue
	}
	env, err := rowcodec.UnwrapBorrowed(mqttReplayAnchorKey(key, position), value)
	if err != nil {
		return messageRow{}, a, err
	}
	if env.Version != 1 || env.Codec != rowcodec.CodecFixed || env.Flags != rowcodec.FlagChecksum {
		return messageRow{}, a, dberrors.ErrCorruptValue
	}
	row, err := mqttReplayOriginalRow(key, position, env.Payload)
	if err != nil {
		return row, a, err
	}
	content, err := canonicalMQTTAnchorContent(key, row)
	if err != nil || !bytes.Equal(content, env.Payload) {
		return row, a, dberrors.ErrCorruptValue
	}
	a, err = quorumlog.DecodeMQTTReplayAnchor(row.Payload)
	if err != nil || a.Through >= position {
		return row, a, dberrors.ErrCorruptValue
	}
	return row, a, nil
}

// stageMQTTReplayAnchors runs inside the same append/checkpoint ownership and
// synchronous batch as the exact proposals. No business append selects this path.
func (e *channelEntry) stageMQTTReplayAnchors(batch *engine.Batch, rows []messageRow, checkpoint *Checkpoint, proposals []durableProposalRecord, keepThrough uint64) error {
	hasAnchor := false
	for _, p := range proposals {
		hasAnchor = hasAnchor || p.manifest.Version == quorumlog.MQTTReplayAnchorProposalManifestVersion
	}
	if !hasAnchor {
		return nil
	}
	evidence, err := readMQTTActivationEvidence(e.db.engine, e.key)
	if err != nil {
		return err
	}
	activation, present := evidence.manifest, evidence.present
	if present && activation.LastOffset > keepThrough {
		if evidence.sourcePresent {
			return dberrors.ErrConflict
		}
		present = false
	}
	if !present {
		for _, p := range proposals {
			if p.manifest.Version == quorumlog.MQTTSourceProposalManifestVersion {
				activation, present = p.manifest, true
				break
			}
		}
	}
	hw := evidence.checkpoint.HW
	if checkpoint != nil {
		hw = checkpoint.HW
	}
	if !present || activation.LastOffset > hw {
		return dberrors.ErrConflict
	}
	for _, p := range proposals {
		if p.manifest.Version != quorumlog.MQTTReplayAnchorProposalManifestVersion {
			continue
		}
		var row messageRow
		found := false
		for _, candidate := range rows {
			if candidate.MessageSeq == p.manifest.LastOffset {
				row, found = candidate, true
				break
			}
		}
		if !found {
			return dberrors.ErrCorruptState
		}
		a, err := quorumlog.DecodeMQTTReplayAnchor(row.Payload)
		if err != nil || a.SourceCommand != activation.CommandID || a.StartAfter != activation.BaseOffset || a.Through > hw || a.Through >= p.manifest.LastOffset {
			return dberrors.ErrConflict
		}
		content, err := canonicalMQTTAnchorContent(e.key, row)
		if err != nil {
			return err
		}
		k := mqttReplayAnchorKey(e.key, row.MessageSeq)
		if err = batch.Set(k, rowcodec.Wrap(k, 1, rowcodec.CodecFixed, rowcodec.FlagChecksum, content)); err != nil {
			return err
		}
	}
	return nil
}

// LoadMQTTReplayAnchor proves one exact committed control using a pinned view.
// Original history is unnecessary; missing or contradictory proof is corruption.
func (l *ChannelLog) LoadMQTTReplayAnchor(ctx context.Context, position uint64) (MQTTReplayAnchorProof, bool, error) {
	if position == 0 {
		return MQTTReplayAnchorProof{}, false, dberrors.ErrInvalidArgument
	}
	if err := l.beginUse(); err != nil {
		return MQTTReplayAnchorProof{}, false, err
	}
	defer l.endUse()
	if err := ctxErr(ctx); err != nil {
		return MQTTReplayAnchorProof{}, false, err
	}
	view, err := l.db.engine.NewSnapshot()
	if err != nil {
		return MQTTReplayAnchorProof{}, false, err
	}
	defer view.Close()
	return loadMQTTReplayAnchorFrom(view, l.key, position)
}

func loadMQTTReplayAnchorFrom(view proposalReadView, key ChannelKey, position uint64) (MQTTReplayAnchorProof, bool, error) {
	value, found, err := view.Get(mqttReplayAnchorKey(key, position))
	if err != nil {
		return MQTTReplayAnchorProof{}, false, err
	}
	if !found {
		entry, present, err := loadDurableEntryIdentityFrom(view, key, position)
		if err != nil {
			return MQTTReplayAnchorProof{}, false, err
		}
		if present && entry.Version == quorumlog.MQTTReplayAnchorProposalManifestVersion {
			return MQTTReplayAnchorProof{}, false, dberrors.ErrCorruptState
		}
		return MQTTReplayAnchorProof{}, false, nil
	}
	row, a, err := decodeMQTTAnchorJournal(key, position, value)
	if err != nil {
		return MQTTReplayAnchorProof{}, false, err
	}
	evidence, err := readMQTTActivationEvidence(view, key)
	if err != nil {
		return MQTTReplayAnchorProof{}, false, err
	}
	if !evidence.present || !evidence.sourcePresent || a.SourceCommand != evidence.manifest.CommandID || a.StartAfter != evidence.source.StartAfter {
		return MQTTReplayAnchorProof{}, false, dberrors.ErrCorruptState
	}
	if position > evidence.checkpoint.HW {
		return MQTTReplayAnchorProof{}, false, nil
	}
	_, activation, err := mqttReplayCommittedEntry(view, key, evidence.manifest.LastOffset, evidence.checkpoint.HW)
	if err != nil || activation != evidence.manifest {
		return MQTTReplayAnchorProof{}, false, dberrors.ErrCorruptState
	}
	entry, manifest, err := mqttReplayCommittedEntry(view, key, position, evidence.checkpoint.HW)
	if err != nil {
		return MQTTReplayAnchorProof{}, false, err
	}
	if manifest.Version != quorumlog.MQTTReplayAnchorProposalManifestVersion || !verifyBackupRowIdentity(entry, row) {
		return MQTTReplayAnchorProof{}, false, dberrors.ErrCorruptState
	}
	return MQTTReplayAnchorProof{Anchor: a, Manifest: manifest}, true, nil
}

func (e *channelEntry) stageTruncateMQTTReplayAnchors(batch *engine.Batch, to uint64) error {
	if to == ^uint64(0) {
		return nil
	}
	span := keycodec.NewPrefixSpan(mqttReplayAnchorPrefix(e.key))
	return batch.DeleteRange(engine.Span{Start: mqttReplayAnchorKey(e.key, to+1), End: span.End})
}

func validateMQTTReplayAnchorBackup(key ChannelKey, hw uint64, entries []backupRawEntry, proposals map[uint64]durableProposalRecord, identities map[uint64]quorumlog.EntryIdentity) error {
	var activation DurableProposalManifest
	for _, p := range proposals {
		if p.manifest.Version == quorumlog.MQTTSourceProposalManifestVersion && (activation.LastOffset == 0 || p.manifest.LastOffset < activation.LastOffset) {
			activation = p.manifest
		}
	}
	seen := make(map[uint64]struct{})
	for _, raw := range entries {
		if !bytes.HasPrefix(raw.Key, mqttReplayAnchorPrefix(key)) {
			continue
		}
		position, ok := mqttReplayAnchorPosition(key, raw.Key)
		if !ok || position > hw {
			return dberrors.ErrCorruptState
		}
		row, a, err := decodeMQTTAnchorJournal(key, position, raw.Value)
		if err != nil {
			return err
		}
		proposal, ok := proposals[position]
		entry, hasEntry := identities[position]
		if !ok || !hasEntry || proposal.manifest.Version != quorumlog.MQTTReplayAnchorProposalManifestVersion || activation.LastOffset == 0 || a.SourceCommand != activation.CommandID || a.StartAfter != activation.BaseOffset || !verifyBackupRowIdentity(entry, row) {
			return dberrors.ErrCorruptState
		}
		seen[position] = struct{}{}
	}
	for position, p := range proposals {
		if p.manifest.Version == quorumlog.MQTTReplayAnchorProposalManifestVersion {
			if _, ok := seen[position]; !ok {
				return dberrors.ErrCorruptState
			}
		}
	}
	return nil
}

// LoadMQTTReplayAnchor exposes the committed point proof through a Channel lease.
func (s *ChannelStore) LoadMQTTReplayAnchor(ctx context.Context, position uint64) (MQTTReplayAnchorProof, bool, error) {
	if err := s.beginUse(); err != nil {
		return MQTTReplayAnchorProof{}, false, err
	}
	defer s.endUse()
	proof, found, err := s.log.LoadMQTTReplayAnchor(ctx, position)
	return proof, found, toChannelError(err)
}
