package message

import (
	"context"
	"math"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
)

// MQTTReplayPage owns a bounded contiguous range and its ending prefix digest.
type MQTTReplayPage struct {
	// After and Through delimit the returned range (After, Through].
	After   uint64
	Through uint64
	// Digest identifies the complete source prefix ending at Through.
	Digest [32]byte
	// Records own their bytes and remain valid after the Channel lease closes.
	Records []MQTTReplayRecord
}

// MQTTReplayMeasure counts one covered range without scanning its message bodies.
type MQTTReplayMeasure struct {
	Messages uint64
	Bytes    uint64
}

func validateMQTTReplayRead(generation string, from, through uint64, opts ReadOptions) error {
	if !(MQTTSourceState{Generation: generation, Revision: 1}).valid() || from == 0 || through < from || opts.Limit <= 0 || opts.Limit > mqttReplayMaxRows || opts.MaxBytes <= 0 || opts.MaxBytes > mqttReplayMaxBytes {
		return dberrors.ErrInvalidArgument
	}
	return nil
}

// loadMQTTReplayState requires a pinned view or writer exclusion so absence of
// the frontier cannot race publication of its paired retirement marker.
func loadMQTTReplayState(view messageBackupReadView, key ChannelKey) (MQTTReplayState, bool, error) {
	value, ok, err := view.Get(mqttReplayStateKey(key))
	if err != nil {
		return MQTTReplayState{}, ok, err
	}
	if !ok {
		_, retired, err := view.Get(mqttReplayRetiredKey(key))
		if err != nil {
			return MQTTReplayState{}, false, err
		}
		if retired {
			return MQTTReplayState{}, false, dberrors.ErrCorruptState
		}
		return MQTTReplayState{}, false, nil
	}
	s, err := decodeMQTTReplayState(key, value)
	return s, err == nil, err
}

// LoadMQTTReplayState reads local durable coverage. Current distributed source
// authority and quorum durability must be established by the calling runtime.
func (l *ChannelLog) LoadMQTTReplayState(ctx context.Context) (MQTTReplayState, bool, error) {
	if err := l.beginUse(); err != nil {
		return MQTTReplayState{}, false, err
	}
	defer l.endUse()
	if err := ctxErr(ctx); err != nil {
		return MQTTReplayState{}, false, err
	}
	view, err := l.db.engine.NewSnapshot()
	if err != nil {
		return MQTTReplayState{}, false, err
	}
	defer view.Close()
	return loadMQTTReplayState(view, l.key)
}

// CopyMQTTReplaySource copies an exact committed source prefix into shared local
// content. It does not update source protection: replication must first prove
// that the returned digest is recoverable under the current Channel authority.
func (l *ChannelLog) CopyMQTTReplaySource(ctx context.Context, generation string, from, through uint64, opts ReadOptions) (MQTTReplayPage, error) {
	if err := validateMQTTReplayRead(generation, from, through, opts); err != nil {
		return MQTTReplayPage{}, err
	}
	if err := l.beginUse(); err != nil {
		return MQTTReplayPage{}, err
	}
	defer l.endUse()
	l.appendMu.Lock()
	defer l.appendMu.Unlock()
	source, ok, err := l.channelEntry.loadMQTTSourceState(ctx)
	if err != nil {
		return MQTTReplayPage{}, err
	}
	if !ok || source.Generation != generation || from <= source.StartAfter {
		return MQTTReplayPage{}, dberrors.ErrConflict
	}
	cp, ok, err := l.loadCheckpoint(ctx)
	if err != nil {
		return MQTTReplayPage{}, err
	}
	if !ok {
		return MQTTReplayPage{}, dberrors.ErrCorruptState
	}
	if through > cp.HW {
		return MQTTReplayPage{}, dberrors.ErrConflict
	}
	s, present, err := loadMQTTReplayState(l.db.engine, l.key)
	if err != nil {
		return MQTTReplayPage{}, err
	}
	if present {
		if s.Generation != generation || s.StartAfter != source.StartAfter || s.Through > cp.HW || s.Through < source.CopiedThrough {
			return MQTTReplayPage{}, dberrors.ErrCorruptState
		}
		if through <= s.Through {
			return readMQTTReplayPage(ctx, l.db.engine, l.key, s, from, through, opts)
		}
		if err := validateMQTTReplayTail(l.db.engine, l.key, s); err != nil {
			return MQTTReplayPage{}, err
		}
	} else {
		if source.CopiedThrough != source.StartAfter {
			return MQTTReplayPage{}, dberrors.ErrCorruptState
		}
		s = MQTTReplayState{Generation: generation, StartAfter: source.StartAfter, Through: source.StartAfter}
	}
	if from-1 != s.Through {
		return MQTTReplayPage{}, dberrors.ErrConflict
	}
	page := MQTTReplayPage{After: from - 1, Records: make([]MQTTReplayRecord, 0, opts.Limit)}
	batch := l.db.engine.NewBatch()
	defer batch.Close()
	used := 0
	for position := from; ; position++ {
		if err := ctxErr(ctx); err != nil {
			return MQTTReplayPage{}, err
		}
		content, ok, err := l.db.engine.Get(encodeMessageRowKey(l.key, position, 0))
		if err != nil {
			return MQTTReplayPage{}, err
		}
		if !ok {
			return MQTTReplayPage{}, dberrors.ErrCorruptState
		}
		if len(content) > mqttReplayMaxBytes+10 {
			return MQTTReplayPage{}, dberrors.ErrInvalidArgument
		}
		row, err := mqttReplayOriginalRow(l.key, position, content)
		if err != nil {
			return MQTTReplayPage{}, err
		}
		// SizeBytes is an append/fetch accounting hint and may differ between
		// replicas. Canonical content binds the actual payload size instead.
		row.PayloadSize = uint64(len(row.Payload))
		if encodedMessageHeaderLen(row) > opts.MaxBytes-used {
			if len(page.Records) == 0 {
				return MQTTReplayPage{}, dberrors.ErrInvalidArgument
			}
			break
		}
		content, err = encodeMessageHeader(encodeMessageRowKey(l.key, position, 0), row)
		if err != nil {
			return MQTTReplayPage{}, err
		}
		accounted := uint64(len(row.Payload) + len(row.PublicationMetadata))
		if math.MaxUint64-s.TotalBytes < accounted || math.MaxUint64-s.TotalStoredBytes < uint64(len(content)) {
			return MQTTReplayPage{}, dberrors.ErrCorruptState
		}
		r := MQTTReplayRecord{Position: position, ContentVersion: mqttReplayContentVersion, MessageID: row.MessageID, AccountedBytes: accounted, TotalBytes: s.TotalBytes + accounted, TotalStoredBytes: s.TotalStoredBytes + uint64(len(content)), Content: content}
		key := mqttReplayRowKey(l.key, generation, position)
		r.ContentHash = mqttReplayContentHash(key, content)
		r.Digest = mqttReplayNextDigest(s.Digest, r)
		if _, exists, err := l.db.engine.Get(key); err != nil {
			return MQTTReplayPage{}, err
		} else if exists {
			return MQTTReplayPage{}, dberrors.ErrCorruptState
		}
		if err := stageMQTTReplayRecord(batch, l.key, generation, r, encodeMQTTReplayRecord(l.key, generation, r)); err != nil {
			return MQTTReplayPage{}, err
		}
		s, err = extendMQTTReplayState(s, r)
		if err != nil {
			return MQTTReplayPage{}, err
		}
		page.Records = append(page.Records, r)
		used += len(content)
		if position == through || len(page.Records) == opts.Limit || used == opts.MaxBytes {
			break
		}
	}
	if err := batch.Set(mqttReplayStateKey(l.key), encodeMQTTReplayState(l.key, s)); err != nil {
		return MQTTReplayPage{}, err
	}
	if err := l.stageCatalog(batch); err != nil {
		return MQTTReplayPage{}, err
	}
	if err := ctxErr(ctx); err != nil {
		return MQTTReplayPage{}, err
	}
	if err := batch.Commit(true); err != nil {
		return MQTTReplayPage{}, err
	}
	page.Through, page.Digest = s.Through, s.Digest
	return page, nil
}

func loadMQTTReplayRecord(view messageBackupReadView, key ChannelKey, generation string, position uint64) (MQTTReplayRecord, error) {
	v, ok, err := view.Get(mqttReplayRowKey(key, generation, position))
	if err != nil {
		return MQTTReplayRecord{}, err
	}
	if !ok {
		return MQTTReplayRecord{}, dberrors.ErrCorruptState
	}
	return decodeMQTTReplayRecord(key, generation, position, v)
}

// mqttReplayPrefix loads at most one immutable endpoint, including the empty
// activation prefix, without accepting positions outside the durable frontier.
func mqttReplayPrefix(view messageBackupReadView, key ChannelKey, s MQTTReplayState, position uint64) (MQTTReplayState, error) {
	if position < s.StartAfter || position > s.Through {
		return s, dberrors.ErrConflict
	}
	base, _, err := mqttReplayBaseline(view, key, s)
	if err != nil {
		return s, err
	}
	if position < base.Through {
		return s, dberrors.ErrConflict
	}
	if position == base.Through {
		return base, nil
	}
	return loadMQTTReplayMeter(view, key, s, position)
}

func readMQTTReplayPage(ctx context.Context, view messageBackupReadView, key ChannelKey, s MQTTReplayState, from, through uint64, opts ReadOptions) (MQTTReplayPage, error) {
	if from <= s.StartAfter || through > s.Through {
		return MQTTReplayPage{}, dberrors.ErrConflict
	}
	previous, err := mqttReplayPrefix(view, key, s, from-1)
	if err != nil {
		return MQTTReplayPage{}, err
	}
	page := MQTTReplayPage{After: from - 1, Records: make([]MQTTReplayRecord, 0, opts.Limit)}
	used := 0
	for pos := from; ; pos++ {
		if err := ctxErr(ctx); err != nil {
			return MQTTReplayPage{}, err
		}
		r, err := loadMQTTReplayRecord(view, key, s.Generation, pos)
		if err != nil {
			return MQTTReplayPage{}, err
		}
		if len(r.Content) > opts.MaxBytes-used {
			if len(page.Records) == 0 {
				return MQTTReplayPage{}, dberrors.ErrInvalidArgument
			}
			break
		}
		previous, err = extendMQTTReplayState(previous, r)
		if err != nil {
			return MQTTReplayPage{}, err
		}
		if pos == s.Through && previous != s {
			return MQTTReplayPage{}, dberrors.ErrCorruptState
		}
		page.Records = append(page.Records, r)
		used += len(r.Content)
		if pos == through || len(page.Records) == opts.Limit || used == opts.MaxBytes {
			break
		}
	}
	page.Through, page.Digest = previous.Through, previous.Digest
	return page, nil
}

// ReadMQTTReplay returns original content independently of ordinary history's
// logical and physical floors. Missing covered content is corruption, not EOF.
func (l *ChannelLog) ReadMQTTReplay(ctx context.Context, generation string, from, through uint64, opts ReadOptions) (MQTTReplayPage, error) {
	if err := validateMQTTReplayRead(generation, from, through, opts); err != nil {
		return MQTTReplayPage{}, err
	}
	if err := l.beginUse(); err != nil {
		return MQTTReplayPage{}, err
	}
	defer l.endUse()
	l.appendMu.Lock()
	defer l.appendMu.Unlock()
	s, ok, err := loadMQTTReplayState(l.db.engine, l.key)
	if err != nil {
		return MQTTReplayPage{}, err
	}
	if !ok || s.Generation != generation {
		return MQTTReplayPage{}, dberrors.ErrConflict
	}
	return readMQTTReplayPage(ctx, l.db.engine, l.key, s, from, through, opts)
}

// MeasureMQTTReplayRange counts (after, through] from two durable endpoints.
// Qualification and per-session authorization remain responsibilities of callers.
func (l *ChannelLog) MeasureMQTTReplayRange(ctx context.Context, generation string, after, through uint64) (MQTTReplayMeasure, error) {
	if after > through {
		return MQTTReplayMeasure{}, dberrors.ErrInvalidArgument
	}
	if err := l.beginUse(); err != nil {
		return MQTTReplayMeasure{}, err
	}
	defer l.endUse()
	if err := ctxErr(ctx); err != nil {
		return MQTTReplayMeasure{}, err
	}
	l.appendMu.Lock()
	defer l.appendMu.Unlock()
	s, ok, err := loadMQTTReplayState(l.db.engine, l.key)
	if err != nil {
		return MQTTReplayMeasure{}, err
	}
	if !ok || s.Generation != generation {
		return MQTTReplayMeasure{}, dberrors.ErrConflict
	}
	start, err := mqttReplayPrefix(l.db.engine, l.key, s, after)
	if err != nil {
		return MQTTReplayMeasure{}, err
	}
	end, err := mqttReplayPrefix(l.db.engine, l.key, s, through)
	if err != nil {
		return MQTTReplayMeasure{}, err
	}
	if end.TotalBytes < start.TotalBytes || end.TotalStoredBytes < start.TotalStoredBytes {
		return MQTTReplayMeasure{}, dberrors.ErrCorruptState
	}
	return MQTTReplayMeasure{Messages: through - after, Bytes: end.TotalBytes - start.TotalBytes}, nil
}
