package message

import (
	"bytes"
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
)

// MQTTReplayTransfer owns one bounded contiguous page and both prefix endpoints.
// Neither endpoint is a quorum receipt or permission to release source history.
type MQTTReplayTransfer struct {
	// Before may be the empty activation prefix; After must include every record.
	Before, After MQTTReplayState
	// Records own their content; the page is limited to 256 rows and 16 MiB.
	Records []MQTTReplayRecord
}

// ExportMQTTReplay reads a coherent bounded page, independently of original-body
// retention. The receiver must obtain its accepted prefix through current
// Channel authority, rather than trust the endpoints returned by this method.
func (l *ChannelLog) ExportMQTTReplay(ctx context.Context, generation string, from, through uint64, opts ReadOptions) (MQTTReplayTransfer, error) {
	if err := validateMQTTReplayRead(generation, from, through, opts); err != nil {
		return MQTTReplayTransfer{}, err
	}
	if err := l.beginUse(); err != nil {
		return MQTTReplayTransfer{}, err
	}
	defer l.endUse()
	if err := ctxErr(ctx); err != nil {
		return MQTTReplayTransfer{}, err
	}
	view, err := l.db.engine.NewSnapshot()
	if err != nil {
		return MQTTReplayTransfer{}, err
	}
	defer view.Close()
	return exportMQTTReplayFrom(ctx, view, l.key, generation, from, through, opts)
}

// exportMQTTReplayFrom requires a pinned view or append/checkpoint ownership.
func exportMQTTReplayFrom(ctx context.Context, view messageBackupReadView, key ChannelKey, generation string, from, through uint64, opts ReadOptions) (MQTTReplayTransfer, error) {
	s, ok, err := loadMQTTReplayState(view, key)
	if err != nil {
		return MQTTReplayTransfer{}, err
	}
	if !ok || s.Generation != generation {
		return MQTTReplayTransfer{}, dberrors.ErrConflict
	}
	if _, err := mqttReplayTransferEvidence(view, key, s); err != nil {
		return MQTTReplayTransfer{}, err
	}
	page, err := readMQTTReplayPage(ctx, view, key, s, from, through, opts)
	if err != nil {
		return MQTTReplayTransfer{}, err
	}
	before, err := mqttReplayPrefix(view, key, s, page.After)
	if err != nil {
		return MQTTReplayTransfer{}, err
	}
	after, err := mqttReplayPrefix(view, key, s, page.Through)
	if err != nil {
		return MQTTReplayTransfer{}, err
	}
	tail := page.Records[len(page.Records)-1]
	if after.Digest != page.Digest || after.TotalBytes != tail.TotalBytes || after.TotalStoredBytes != tail.TotalStoredBytes {
		return MQTTReplayTransfer{}, dberrors.ErrCorruptState
	}
	if err := ctxErr(ctx); err != nil {
		return MQTTReplayTransfer{}, err
	}
	return MQTTReplayTransfer{Before: before, After: after, Records: page.Records}, nil
}

// ImportMQTTReplay atomically extends replica-local shared content, or verifies
// an exact retry. Expected MUST come from an independently verified current
// Channel copy/recovery decision, never from the received page itself: existing
// log digests do not bind every native row field. This method proves no quorum
// authority/readiness and never advances source release, HW or ordinary history.
// The caller must keep page contents immutable for the duration of this call.
func (l *ChannelLog) ImportMQTTReplay(ctx context.Context, expected MQTTReplayState, page MQTTReplayTransfer) (MQTTReplayState, error) {
	if err := validateMQTTReplayTransfer(page); err != nil {
		return MQTTReplayState{}, err
	}
	if expected != page.After {
		return MQTTReplayState{}, dberrors.ErrConflict
	}
	if err := l.beginUse(); err != nil {
		return MQTTReplayState{}, err
	}
	defer l.endUse()
	l.appendMu.Lock()
	defer l.appendMu.Unlock()
	l.checkpointMu.Lock()
	defer l.checkpointMu.Unlock()
	if err := ctxErr(ctx); err != nil {
		return MQTTReplayState{}, err
	}
	return l.importMQTTReplayLocked(ctx, expected, page)
}

// importMQTTReplayLocked keeps proof verification and the content commit inside
// one append/checkpoint ownership interval. The caller validates page bounds.
func (l *ChannelLog) importMQTTReplayLocked(ctx context.Context, expected MQTTReplayState, page MQTTReplayTransfer) (MQTTReplayState, error) {
	view := l.db.engine
	evidence, err := mqttReplayTransferEvidence(view, l.key, expected)
	if err != nil {
		return MQTTReplayState{}, err
	}
	current, present, err := loadMQTTReplayState(view, l.key)
	if err != nil {
		return MQTTReplayState{}, err
	}
	if present {
		if current.Generation != expected.Generation || current.StartAfter != expected.StartAfter || current.Through > evidence.checkpoint.HW {
			return MQTTReplayState{}, dberrors.ErrCorruptState
		}
		if err := validateMQTTReplayTail(view, l.key, current); err != nil {
			return MQTTReplayState{}, err
		}
	} else {
		current = MQTTReplayState{Generation: expected.Generation, StartAfter: expected.StartAfter, Through: expected.StartAfter}
	}
	retry := page.After.Through <= current.Through
	if retry {
		before, err := mqttReplayPrefix(view, l.key, current, page.Before.Through)
		if err != nil {
			return MQTTReplayState{}, err
		}
		after, err := mqttReplayPrefix(view, l.key, current, page.After.Through)
		if err != nil {
			return MQTTReplayState{}, err
		}
		if before != page.Before || after != page.After {
			return MQTTReplayState{}, dberrors.ErrConflict
		}
	} else if current != page.Before {
		return MQTTReplayState{}, dberrors.ErrConflict
	}
	batch := view.NewBatch()
	defer batch.Close()
	prefix := page.Before
	var capacityRows []messageRow
	for _, r := range page.Records {
		if err := ctxErr(ctx); err != nil {
			return MQTTReplayState{}, err
		}
		row, err := validateMQTTReplayTransferRecord(l.key, expected.Generation, r)
		if err != nil {
			return MQTTReplayState{}, err
		}
		entry, _, err := mqttReplayCommittedEntry(view, l.key, r.Position, evidence.checkpoint.HW)
		if err != nil {
			return MQTTReplayState{}, err
		}
		if l.db.mqttStorage != nil {
			capacityRows = append(capacityRows, row)
		}
		if !verifyBackupRowIdentity(entry, row) {
			return MQTTReplayState{}, dberrors.ErrCorruptState
		}
		prefix, err = extendMQTTReplayState(prefix, r)
		if err != nil {
			return MQTTReplayState{}, err
		}
		encoded := encodeMQTTReplayRecord(l.key, expected.Generation, r)
		rowKey := mqttReplayRowKey(l.key, expected.Generation, r.Position)
		stored, exists, err := view.Get(rowKey)
		if err != nil {
			return MQTTReplayState{}, err
		}
		if retry {
			meter, err := loadMQTTReplayMeter(view, l.key, current, r.Position)
			if err != nil {
				return MQTTReplayState{}, err
			}
			if !exists || !bytes.Equal(stored, encoded) || meter != prefix {
				return MQTTReplayState{}, dberrors.ErrCorruptState
			}
			continue
		}
		_, meterExists, err := view.Get(mqttReplayMeterKey(l.key, expected.Generation, r.Position))
		if err != nil {
			return MQTTReplayState{}, err
		}
		if exists || meterExists {
			return MQTTReplayState{}, dberrors.ErrCorruptState
		}
		if err := stageMQTTReplayRecord(batch, l.key, expected.Generation, r, encoded); err != nil {
			return MQTTReplayState{}, err
		}
	}
	if prefix != expected {
		return MQTTReplayState{}, dberrors.ErrCorruptState
	}
	if err := ctxErr(ctx); err != nil {
		return MQTTReplayState{}, err
	}
	if retry {
		return expected, nil
	}
	if err := batch.Set(mqttReplayStateKey(l.key), encodeMQTTReplayState(l.key, expected)); err != nil {
		return MQTTReplayState{}, err
	}
	if err := l.stageCatalog(batch); err != nil {
		return MQTTReplayState{}, err
	}
	charges, reserved, err := l.prepareMQTTStorageBooking(ctx, capacityRows, nil)
	if err != nil {
		return MQTTReplayState{}, err
	}
	submitted := false
	defer func() {
		if !submitted {
			l.db.mqttStorage.cancelReservation(reserved)
		}
	}()
	if err = stageMQTTStorageCharges(batch, l.key, charges); err != nil {
		return MQTTReplayState{}, err
	}
	submitted = true
	if err := batch.Commit(true); err != nil {
		return MQTTReplayState{}, err
	}
	return expected, nil
}

func validateMQTTReplayTransfer(page MQTTReplayTransfer) error {
	b, a := page.Before, page.After
	empty := MQTTReplayState{Generation: a.Generation, StartAfter: a.StartAfter, Through: a.StartAfter}
	if len(page.Records) == 0 || len(page.Records) > mqttReplayMaxRows || !mqttReplayStateValid(a) ||
		(b != empty && !mqttReplayStateValid(b)) || b.Generation != a.Generation || b.StartAfter != a.StartAfter ||
		a.Through <= b.Through || a.Through-b.Through != uint64(len(page.Records)) {
		return dberrors.ErrInvalidArgument
	}
	remaining := mqttReplayMaxBytes
	for i, r := range page.Records {
		if r.Position != b.Through+uint64(i)+1 || r.ContentVersion != mqttReplayContentVersion || len(r.Content) == 0 || len(r.Content) > remaining {
			return dberrors.ErrInvalidArgument
		}
		remaining -= len(r.Content)
	}
	return nil
}

func validateMQTTReplayTransferRecord(key ChannelKey, generation string, r MQTTReplayRecord) (messageRow, error) {
	row, err := mqttReplayOriginalRow(key, r.Position, r.Content)
	if err != nil {
		return row, err
	}
	if row.MessageID != r.MessageID || row.PayloadSize != uint64(len(row.Payload)) ||
		r.AccountedBytes != uint64(len(row.Payload)+len(row.PublicationMetadata)) ||
		r.ContentHash != mqttReplayContentHash(mqttReplayRowKey(key, generation, r.Position), r.Content) {
		return row, dberrors.ErrCorruptValue
	}
	canonical, err := encodeMessageHeader(encodeMessageRowKey(key, r.Position, 0), row)
	if err != nil {
		return row, err
	}
	if !bytes.Equal(canonical, r.Content) {
		return row, dberrors.ErrCorruptValue
	}
	return row, nil
}

// mqttReplayTransferEvidence proves only already installed committed storage
// evidence. It must not manufacture activation, checkpoints or log identities.
func mqttReplayTransferEvidence(view proposalReadView, key ChannelKey, end MQTTReplayState) (mqttActivationEvidence, error) {
	e, err := readMQTTActivationEvidence(view, key)
	if err != nil {
		return e, err
	}
	if !e.present || !e.sourcePresent || e.source.Generation != end.Generation || e.source.StartAfter != end.StartAfter || end.Through > e.checkpoint.HW {
		return e, dberrors.ErrConflict
	}
	_, activation, err := mqttReplayCommittedEntry(view, key, e.manifest.LastOffset, e.checkpoint.HW)
	if err != nil {
		return e, err
	}
	if activation != e.manifest {
		return e, dberrors.ErrCorruptState
	}
	return e, nil
}

// mqttReplayCommittedEntry uses bounded point reads, including paired proposal
// indexes, a committed proposal tail and the immediate predecessor. Identities
// survive original-body prefix retention; this method does not scan history.
func mqttReplayCommittedEntry(view proposalReadView, key ChannelKey, position, hw uint64) (quorumlog.EntryIdentity, DurableProposalManifest, error) {
	entry, ok, err := loadDurableEntryIdentityFrom(view, key, position)
	if err != nil {
		return entry, DurableProposalManifest{}, err
	}
	if !ok || entry.Index != position {
		return entry, DurableProposalManifest{}, dberrors.ErrCorruptState
	}
	proposal, ok, err := loadDurableProposalFrom(view, encodeProposalByCommandKey(key, entry.CommandID))
	if err != nil {
		return entry, DurableProposalManifest{}, err
	}
	m := proposal.manifest
	if !ok || position <= m.BaseOffset || position > m.LastOffset || m.LastOffset > hw || entry.Version != m.Version ||
		entry.CommandID != m.CommandID || entry.ChannelEpoch != m.ChannelEpoch || entry.LeaderTerm != m.LeaderTerm || entry.FenceVersion != m.FenceVersion {
		return entry, m, dberrors.ErrCorruptState
	}
	paired, ok, err := loadDurableProposalPairByLast(view, key, m.LastOffset)
	if err != nil {
		return entry, m, err
	}
	if !ok || paired != proposal {
		return entry, m, dberrors.ErrCorruptState
	}
	tail, ok, err := loadDurableEntryIdentityFrom(view, key, m.LastOffset)
	if err != nil {
		return entry, m, err
	}
	if !ok || tail.Version != m.Version || !durableProposalTailConsistent(proposal, tail) {
		return entry, m, dberrors.ErrCorruptState
	}
	if position == m.BaseOffset+1 && (entry.PreviousIndex != m.PreviousIndex || entry.PreviousTerm != m.PreviousTerm || entry.PreviousDigest != m.PreviousDigest) {
		return entry, m, dberrors.ErrCorruptState
	}
	if position > 1 {
		previous, ok, err := loadDurableEntryIdentityFrom(view, key, position-1)
		if err != nil {
			return entry, m, err
		}
		if !ok || previous.Index != entry.PreviousIndex || previous.LeaderTerm != entry.PreviousTerm || previous.Digest != entry.PreviousDigest {
			return entry, m, dberrors.ErrCorruptState
		}
		if position > m.BaseOffset+1 && (previous.Version != m.Version || previous.CommandID != m.CommandID || previous.ChannelEpoch != m.ChannelEpoch || previous.FenceVersion != m.FenceVersion) {
			return entry, m, dberrors.ErrCorruptState
		}
	}
	return entry, m, nil
}
