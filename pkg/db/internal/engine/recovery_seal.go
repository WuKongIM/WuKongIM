package engine

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/cockroachdb/pebble/v2"
	"github.com/cockroachdb/pebble/v2/batchrepr"
)

var errRecoverySealBroken = errors.New("recovery sequence seal requires engine reopen after ambiguous commit")

// recoverySeal certifies the visible engine sequence of an entire physical
// commit. Older writers cannot refresh it, so their mutations reject reuse.
type recoverySeal struct {
	key          []byte
	certificates Span
	next         uint64
	// epoch fences live proofs across any uncertified physical commit.
	epoch  uint64
	broken bool
}

// ConfigureRecoverySeal must run once under exclusive startup admission before
// any batches or snapshots are created. The returned bool proves only that no
// unaware writer changed the database since its last sealed commit; callers
// must independently validate each logical recovery certificate.
func (e *DB) ConfigureRecoverySeal(key []byte, certificates Span) (bool, error) {
	if e.IsClosed() {
		return false, dberrors.ErrClosed
	}
	if len(key) == 0 || len(certificates.Start) == 0 || bytes.Compare(certificates.Start, certificates.End) >= 0 || (bytes.Compare(key, certificates.Start) >= 0 && bytes.Compare(key, certificates.End) < 0) {
		return false, dberrors.ErrInvalidArgument
	}
	e.sealMu.Lock()
	defer e.sealMu.Unlock()
	if e.seal != nil {
		return false, fmt.Errorf("recovery sequence seal already configured")
	}
	// Pebble exposes the pinned visible boundary through its snapshot metrics.
	// Exactly one snapshot makes that boundary unambiguous; no private fields or
	// inference from file names, wall time, or applied indexes is involved.
	snapshot := e.pdb.NewSnapshot()
	metrics := e.pdb.Metrics()
	if err := snapshot.Close(); err != nil {
		return false, err
	}
	if metrics.Snapshots.Count != 1 {
		return false, fmt.Errorf("recovery seal requires exclusive startup snapshot")
	}
	next := uint64(metrics.Snapshots.EarliestSeqNum)
	value, found, err := e.Get(key)
	if err != nil {
		return false, err
	}
	e.seal = &recoverySeal{key: bytes.Clone(key), certificates: Span{Start: bytes.Clone(certificates.Start), End: bytes.Clone(certificates.End)}, next: next, epoch: 1}
	valid := found && len(value) == 16 && string(value[:8]) == "WKSEAL01" && binary.BigEndian.Uint64(value[8:]) == next
	return valid, nil
}

// RecoveryEpoch identifies live proof continuity within this engine open.
// It is not persisted: startup independently validates the physical seal.
func (e *DB) RecoveryEpoch() uint64 {
	if e == nil {
		return 0
	}
	e.sealMu.Lock()
	defer e.sealMu.Unlock()
	if e.seal == nil {
		return 0
	}
	return e.seal.epoch
}

// PreserveRecoveryCertificateAt asserts that this request mutates only the
// state owned by key. The caller validates ownership. A stale epoch (or zero)
// deletes this certificate at commit without invalidating disjoint owners.
// Mixed unclassified requests still veto preservation for the entire batch.
func (b *Batch) PreserveRecoveryCertificateAt(key []byte, epoch uint64) {
	if b.certificateEpochs == nil {
		b.certificateEpochs = make(map[string]uint64)
	}
	b.certificateEpochs[string(key)] = epoch
	b.preserveCertificates = true
}

// InvalidateRecoveryCertificates vetoes preservation for a grouped physical
// batch containing an offline import or another uncertified mutation.
func (b *Batch) InvalidateRecoveryCertificates() { b.invalidateCertificates = true }

func (b *Batch) commitSealed(opts *pebble.WriteOptions) error {
	e := b.db
	e.sealMu.Lock()
	defer e.sealMu.Unlock()
	s := e.seal
	if s.broken {
		return errRecoverySealBroken
	}
	if b.batch.Count() == 0 {
		return b.batch.Commit(opts)
	}
	invalidates := !b.preserveCertificates || b.invalidateCertificates
	if invalidates {
		if err := b.batch.DeleteRange(s.certificates.Start, s.certificates.End, nil); err != nil {
			return err
		}
	} else {
		for key, epoch := range b.certificateEpochs {
			if bytes.Compare([]byte(key), s.certificates.Start) < 0 || bytes.Compare([]byte(key), s.certificates.End) >= 0 {
				return dberrors.ErrInvalidArgument
			}
			if epoch != s.epoch {
				if err := b.batch.Delete([]byte(key), nil); err != nil {
					return err
				}
			}
		}
	}
	var value [16]byte
	copy(value[:8], "WKSEAL01")
	next := s.next + uint64(b.batch.Count()) + 1
	binary.BigEndian.PutUint64(value[8:], next)
	if err := b.batch.Set(s.key, value[:], nil); err != nil {
		return err
	}
	// Apply may detach a large batch into a flushable, clearing Batch.data.
	// Retain its public representation before Commit; Pebble assigns this
	// header during commit and keeps these bytes immutable afterwards.
	repr := b.batch.Repr()
	if err := b.batch.Commit(opts); err != nil {
		s.broken = true
		return err
	}
	// Check the engine's actual assignment, so an unexpected sequence reservation
	// cannot silently produce a reusable proof. A mismatch requires full recovery.
	header, ok := batchrepr.ReadHeader(repr)
	actual := uint64(header.SeqNum) + uint64(header.Count)
	if !ok || actual != next {
		s.broken = true
		return fmt.Errorf("recovery sequence assignment changed: %w", errRecoverySealBroken)
	}
	s.next = actual
	if invalidates {
		s.epoch++
	}
	return nil
}
