package message

import (
	"context"
	"crypto/sha256"
	"encoding/binary"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
	channel "github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
)

const (
	// DurableProposalManifestVersion is the only on-disk proposal manifest
	// format. Unknown versions fail closed because no released format exists.
	DurableProposalManifestVersion = quorumlog.ProposalManifestVersion
	durableProposalRecordSize      = 154
	durableEntryIdentitySize       = 146
)

// DurableProposalManifest is the shared immutable authority and hash-chain
// identity persisted atomically with one exact Channel proposal.
type DurableProposalManifest = quorumlog.ProposalManifest

type durableProposalRecord struct {
	manifest DurableProposalManifest
}

// durableProposalTail is the last committed exact proposal and its matching
// tail entry identity. Callers guard it with the channel append mutex.
type durableProposalTail struct {
	proposal durableProposalRecord
	entry    quorumlog.EntryIdentity
	loaded   bool
}

func validateDurableProposalManifest(manifest DurableProposalManifest, expectedBase uint64, recordCount int) error {
	if !manifest.ValidFor(expectedBase, recordCount) {
		return channel.ErrInvalidArgument
	}
	return nil
}

func validateDecodedDurableProposalManifest(manifest DurableProposalManifest) error {
	if !manifest.StructurallyValid() {
		return dberrors.ErrCorruptValue
	}
	return nil
}

func deriveDurableProposalEntries(manifest DurableProposalManifest, records []channel.Record, rows []messageRow) ([]quorumlog.EntryIdentity, bool) {
	if len(records) != len(rows) {
		return nil, false
	}
	return quorumlog.DeriveProposalEntries(manifest, len(records), func(index int) quorumlog.Record {
		row := rows[index]
		return quorumlog.Record{
			ID: row.MessageID, Index: row.MessageSeq, Epoch: records[index].Epoch,
			Setting: row.Setting, FromUID: row.FromUID, ClientMsgNo: row.ClientMsgNo,
			ServerTimestampMS: row.ServerTimestampMS, SyncOnce: row.FramerFlags&4 != 0,
			Payload: row.Payload,
		}
	})
}

func encodeDurableProposalRecord(record durableProposalRecord) []byte {
	manifest := record.manifest
	value := make([]byte, 0, durableProposalRecordSize)
	value = binary.BigEndian.AppendUint16(value, manifest.Version)
	value = binary.BigEndian.AppendUint64(value, manifest.ChannelEpoch)
	value = binary.BigEndian.AppendUint64(value, manifest.LeaderTerm)
	value = binary.BigEndian.AppendUint64(value, manifest.FenceVersion)
	value = append(value, manifest.CommandID[:]...)
	value = binary.BigEndian.AppendUint64(value, manifest.BaseOffset)
	value = binary.BigEndian.AppendUint64(value, manifest.LastOffset)
	value = binary.BigEndian.AppendUint64(value, manifest.PreviousTerm)
	value = binary.BigEndian.AppendUint64(value, manifest.PreviousIndex)
	value = append(value, manifest.PreviousDigest[:]...)
	return append(value, manifest.Digest[:]...)
}

func decodeDurableProposalRecord(value []byte) (durableProposalRecord, error) {
	if len(value) != durableProposalRecordSize {
		return durableProposalRecord{}, dberrors.ErrCorruptValue
	}
	record := durableProposalRecord{}
	offset := 0
	record.manifest.Version = binary.BigEndian.Uint16(value[offset:])
	offset += 2
	readUint64 := func() uint64 {
		result := binary.BigEndian.Uint64(value[offset:])
		offset += 8
		return result
	}
	record.manifest.ChannelEpoch = readUint64()
	record.manifest.LeaderTerm = readUint64()
	record.manifest.FenceVersion = readUint64()
	copy(record.manifest.CommandID[:], value[offset:offset+sha256.Size])
	offset += sha256.Size
	record.manifest.BaseOffset = readUint64()
	record.manifest.LastOffset = readUint64()
	record.manifest.PreviousTerm = readUint64()
	record.manifest.PreviousIndex = readUint64()
	copy(record.manifest.PreviousDigest[:], value[offset:offset+sha256.Size])
	offset += sha256.Size
	copy(record.manifest.Digest[:], value[offset:offset+sha256.Size])
	if err := validateDecodedDurableProposalManifest(record.manifest); err != nil {
		return durableProposalRecord{}, dberrors.ErrCorruptValue
	}
	return record, nil
}

func encodeDurableEntryIdentity(entry quorumlog.EntryIdentity) []byte {
	value := make([]byte, 0, durableEntryIdentitySize)
	value = binary.BigEndian.AppendUint16(value, entry.Version)
	value = binary.BigEndian.AppendUint64(value, entry.ChannelEpoch)
	value = binary.BigEndian.AppendUint64(value, entry.LeaderTerm)
	value = binary.BigEndian.AppendUint64(value, entry.FenceVersion)
	value = binary.BigEndian.AppendUint64(value, entry.Index)
	value = binary.BigEndian.AppendUint64(value, entry.PreviousTerm)
	value = binary.BigEndian.AppendUint64(value, entry.PreviousIndex)
	value = append(value, entry.CommandID[:]...)
	value = append(value, entry.PreviousDigest[:]...)
	return append(value, entry.Digest[:]...)
}

func decodeDurableEntryIdentity(value []byte) (quorumlog.EntryIdentity, error) {
	if len(value) != durableEntryIdentitySize {
		return quorumlog.EntryIdentity{}, dberrors.ErrCorruptValue
	}
	entry := quorumlog.EntryIdentity{}
	offset := 0
	entry.Version = binary.BigEndian.Uint16(value[offset:])
	offset += 2
	readUint64 := func() uint64 {
		result := binary.BigEndian.Uint64(value[offset:])
		offset += 8
		return result
	}
	entry.ChannelEpoch = readUint64()
	entry.LeaderTerm = readUint64()
	entry.FenceVersion = readUint64()
	entry.Index = readUint64()
	entry.PreviousTerm = readUint64()
	entry.PreviousIndex = readUint64()
	copy(entry.CommandID[:], value[offset:offset+sha256.Size])
	offset += sha256.Size
	copy(entry.PreviousDigest[:], value[offset:offset+sha256.Size])
	offset += sha256.Size
	copy(entry.Digest[:], value[offset:offset+sha256.Size])
	if entry.Version != DurableProposalManifestVersion || entry.ChannelEpoch == 0 || entry.LeaderTerm == 0 || entry.FenceVersion == 0 ||
		entry.Index == 0 || entry.CommandID == (quorumlog.CommandID{}) || entry.Digest == (quorumlog.EntryDigest{}) ||
		entry.PreviousIndex+1 != entry.Index {
		return quorumlog.EntryIdentity{}, dberrors.ErrCorruptValue
	}
	if entry.PreviousIndex == 0 {
		if entry.PreviousTerm != 0 || entry.PreviousDigest != (quorumlog.EntryDigest{}) {
			return quorumlog.EntryIdentity{}, dberrors.ErrCorruptValue
		}
	} else if entry.PreviousTerm == 0 || entry.PreviousDigest == (quorumlog.EntryDigest{}) {
		return quorumlog.EntryIdentity{}, dberrors.ErrCorruptValue
	}
	return entry, nil
}

type proposalReadView interface {
	Get(key []byte) ([]byte, bool, error)
}

type durableProposalDisposition uint8

const (
	durableProposalFresh durableProposalDisposition = iota + 1
	durableProposalAlreadyPresent
)

// inspectDurableProposal distinguishes a fresh exact proposal from an exact
// retry while rejecting incomplete paired indexes and orphaned entry
// identities. The caller must hold appendMu and must already have validated
// the log frontier, durable predecessor, manifest, and derived entry chain;
// this function classifies only the proposal and entry-identity rows. It
// performs the same bounded point reads for production and in-memory contract
// adapters.
func inspectDurableProposal(view proposalReadView, channelKey ChannelKey, proposal durableProposalRecord, entries []quorumlog.EntryIdentity) (durableProposalDisposition, error) {
	manifest := proposal.manifest
	if !manifest.StructurallyValid() || uint64(len(entries)) != manifest.LastOffset-manifest.BaseOffset {
		return 0, dberrors.ErrCorruptState
	}
	byCommand, commandPresent, err := loadDurableProposalFrom(view, encodeProposalByCommandKey(channelKey, proposal.manifest.CommandID))
	if err != nil {
		return 0, err
	}
	byLast, lastPresent, err := loadDurableProposalFrom(view, encodeProposalByLastKey(channelKey, proposal.manifest.LastOffset))
	if err != nil {
		return 0, err
	}
	if commandPresent || lastPresent {
		if !commandPresent || !lastPresent || byCommand != proposal || byLast != proposal {
			return 0, dberrors.ErrCorruptState
		}
	}
	for _, expected := range entries {
		persisted, present, err := loadDurableEntryIdentityFrom(view, channelKey, expected.Index)
		if err != nil {
			return 0, err
		}
		if commandPresent {
			if !present || persisted != expected {
				return 0, dberrors.ErrCorruptState
			}
		} else if present {
			return 0, dberrors.ErrCorruptState
		}
	}
	if commandPresent {
		return durableProposalAlreadyPresent, nil
	}
	return durableProposalFresh, nil
}

func loadDurableProposalFrom(view proposalReadView, key []byte) (durableProposalRecord, bool, error) {
	value, ok, err := view.Get(key)
	if err != nil || !ok {
		return durableProposalRecord{}, ok, err
	}
	record, err := decodeDurableProposalRecord(value)
	return record, err == nil, err
}

func loadDurableProposalPairByLast(view proposalReadView, channelKey ChannelKey, lastOffset uint64) (durableProposalRecord, bool, error) {
	byLast, ok, err := loadDurableProposalFrom(view, encodeProposalByLastKey(channelKey, lastOffset))
	if err != nil || !ok {
		return durableProposalRecord{}, ok, err
	}
	byCommand, commandPresent, err := loadDurableProposalFrom(view, encodeProposalByCommandKey(channelKey, byLast.manifest.CommandID))
	if err != nil {
		return durableProposalRecord{}, false, err
	}
	if !commandPresent || byCommand != byLast {
		return durableProposalRecord{}, false, dberrors.ErrCorruptState
	}
	return byLast, true, nil
}

func (s *ChannelStore) loadDurableProposal(key []byte) (durableProposalRecord, bool, error) {
	return loadDurableProposalFrom(s.log.db.engine, key)
}

func loadDurableEntryIdentityFrom(view proposalReadView, channelKey ChannelKey, index uint64) (quorumlog.EntryIdentity, bool, error) {
	value, ok, err := view.Get(encodeEntryIdentityKey(channelKey, index))
	if err != nil || !ok {
		return quorumlog.EntryIdentity{}, ok, err
	}
	entry, err := decodeDurableEntryIdentity(value)
	return entry, err == nil, err
}

// validateDurableProposalPredecessor proves that the exact previous range and
// its tail entry identity are present before a new proposal can extend it.
func (s *ChannelStore) validateDurableProposalPredecessor(manifest DurableProposalManifest, allowCached bool) error {
	if manifest.BaseOffset == 0 {
		return nil
	}
	if cached := s.log.durableProposalTail; allowCached && cached.loaded && cached.entry.Index == manifest.BaseOffset {
		if !durableProposalTailMatchesPredecessor(cached.proposal, cached.entry, manifest) {
			return dberrors.ErrCorruptState
		}
		s.log.db.durablePredecessorCacheHits.Add(1)
		return nil
	}
	s.log.db.durablePredecessorValidations.Add(1)
	previous, ok, err := loadDurableProposalPairByLast(s.log.db.engine, s.log.key, manifest.BaseOffset)
	if err != nil {
		return err
	}
	if !ok {
		return dberrors.ErrCorruptState
	}
	tail, present, err := loadDurableEntryIdentityFrom(s.log.db.engine, s.log.key, manifest.BaseOffset)
	if err != nil {
		return err
	}
	if !present || !durableProposalTailMatchesPredecessor(previous, tail, manifest) {
		return dberrors.ErrCorruptState
	}
	s.log.durableProposalTail = durableProposalTail{proposal: previous, entry: tail, loaded: true}
	return nil
}

func durableProposalTailMatchesPredecessor(previous durableProposalRecord, tail quorumlog.EntryIdentity, next DurableProposalManifest) bool {
	manifest := previous.manifest
	return manifest.LastOffset == next.BaseOffset && manifest.LeaderTerm == next.PreviousTerm && manifest.Digest == next.PreviousDigest &&
		durableProposalTailConsistent(previous, tail)
}

func durableProposalTailConsistent(proposal durableProposalRecord, tail quorumlog.EntryIdentity) bool {
	manifest := proposal.manifest
	return tail.Index == manifest.LastOffset && tail.ChannelEpoch == manifest.ChannelEpoch &&
		tail.LeaderTerm == manifest.LeaderTerm && tail.FenceVersion == manifest.FenceVersion &&
		tail.CommandID == manifest.CommandID && tail.Digest == manifest.Digest
}

func (e *channelEntry) clearDurableProposalTailLocked() {
	e.durableProposalTail = durableProposalTail{}
}

func (e *channelEntry) publishDurableProposalTailLocked(proposals []durableProposalRecord, entries []quorumlog.EntryIdentity, nextLEO uint64) {
	if len(proposals) == 0 && len(entries) == 0 {
		e.clearDurableProposalTailLocked()
		return
	}
	if len(proposals) == 0 || len(entries) == 0 {
		e.clearDurableProposalTailLocked()
		return
	}
	proposal := proposals[len(proposals)-1]
	entry := entries[len(entries)-1]
	if proposal.manifest.LastOffset != nextLEO || entry.Index != nextLEO ||
		!durableProposalTailConsistent(proposal, entry) {
		e.clearDurableProposalTailLocked()
		return
	}
	e.durableProposalTail = durableProposalTail{proposal: proposal, entry: entry, loaded: true}
}

func sameDurableProposal(left, right durableProposalRecord) bool {
	return left == right
}

func validateBackupProposalSystemEntries(channelKey ChannelKey, hw uint64, entries []backupRawEntry) error {
	byLast := make(map[uint64]durableProposalRecord)
	byCommand := make(map[quorumlog.CommandID]durableProposalRecord)
	entryIdentities := make(map[uint64]quorumlog.EntryIdentity)
	seenKeys := make(map[string]struct{}, len(entries))
	for _, raw := range entries {
		if _, duplicate := seenKeys[string(raw.Key)]; duplicate {
			return dberrors.ErrCorruptState
		}
		seenKeys[string(raw.Key)] = struct{}{}
		if lastOffset, ok := decodeProposalByLastKey(channelKey, raw.Key); ok {
			record, err := decodeDurableProposalRecord(raw.Value)
			if err != nil || record.manifest.LastOffset != lastOffset || lastOffset > hw {
				return dberrors.ErrCorruptState
			}
			byLast[lastOffset] = record
			continue
		}
		if commandID, ok := decodeProposalByCommandKey(channelKey, raw.Key); ok {
			record, err := decodeDurableProposalRecord(raw.Value)
			if err != nil || record.manifest.CommandID != commandID || record.manifest.LastOffset > hw {
				return dberrors.ErrCorruptState
			}
			byCommand[commandID] = record
			continue
		}
		if index, ok := decodeEntryIdentityKey(channelKey, raw.Key); ok {
			entry, err := decodeDurableEntryIdentity(raw.Value)
			if err != nil || entry.Index != index || index > hw {
				return dberrors.ErrCorruptState
			}
			entryIdentities[index] = entry
		}
	}
	coveredEntries := make(map[uint64]struct{}, len(entryIdentities))
	for lastOffset, record := range byLast {
		if paired, ok := byCommand[record.manifest.CommandID]; !ok || paired != record {
			return dberrors.ErrCorruptState
		}
		if record.manifest.BaseOffset > 0 {
			previousProposal, proposalPresent := byLast[record.manifest.BaseOffset]
			previousEntry, entryPresent := entryIdentities[record.manifest.BaseOffset]
			if !proposalPresent || !entryPresent ||
				previousProposal.manifest.LastOffset != record.manifest.BaseOffset ||
				previousProposal.manifest.LeaderTerm != record.manifest.PreviousTerm ||
				previousProposal.manifest.Digest != record.manifest.PreviousDigest ||
				previousEntry.LeaderTerm != record.manifest.PreviousTerm ||
				previousEntry.Digest != record.manifest.PreviousDigest {
				return dberrors.ErrCorruptState
			}
		}
		previousTerm := record.manifest.PreviousTerm
		previousIndex := record.manifest.PreviousIndex
		previousDigest := record.manifest.PreviousDigest
		for index := record.manifest.BaseOffset + 1; ; index++ {
			entry, ok := entryIdentities[index]
			if !ok || entry.ChannelEpoch != record.manifest.ChannelEpoch || entry.LeaderTerm != record.manifest.LeaderTerm ||
				entry.FenceVersion != record.manifest.FenceVersion || entry.CommandID != record.manifest.CommandID ||
				entry.PreviousTerm != previousTerm || entry.PreviousIndex != previousIndex || entry.PreviousDigest != previousDigest {
				return dberrors.ErrCorruptState
			}
			coveredEntries[index] = struct{}{}
			previousTerm, previousIndex, previousDigest = entry.LeaderTerm, entry.Index, entry.Digest
			if index == lastOffset {
				break
			}
		}
		if previousDigest != record.manifest.Digest {
			return dberrors.ErrCorruptState
		}
	}
	if len(byLast) != len(byCommand) || len(coveredEntries) != len(entryIdentities) {
		return dberrors.ErrCorruptState
	}
	return nil
}

func backupEntryIdentityMap(channelKey ChannelKey, entries []backupRawEntry) (map[uint64]quorumlog.EntryIdentity, error) {
	identities := make(map[uint64]quorumlog.EntryIdentity)
	for _, raw := range entries {
		index, ok := decodeEntryIdentityKey(channelKey, raw.Key)
		if !ok {
			continue
		}
		entry, err := decodeDurableEntryIdentity(raw.Value)
		if err != nil || entry.Index != index {
			return nil, dberrors.ErrCorruptState
		}
		identities[index] = entry
	}
	return identities, nil
}

func verifyBackupRowIdentity(entry quorumlog.EntryIdentity, row messageRow) bool {
	return quorumlog.VerifyEntry(entry, quorumlog.Record{
		ID: row.MessageID, Index: row.MessageSeq, Epoch: entry.ChannelEpoch,
		Setting: row.Setting, FromUID: row.FromUID, ClientMsgNo: row.ClientMsgNo,
		ServerTimestampMS: row.ServerTimestampMS, SyncOnce: row.FramerFlags&4 != 0,
		Payload: row.Payload,
	})
}

func (e *channelEntry) validateDurableProposalCommandIndex(ctx context.Context) error {
	prefix := encodeProposalByCommandPrefix(e.key)
	span := keycodec.NewPrefixSpan(prefix)
	iter, err := e.db.engine.NewIter(engine.Span{Start: span.Start, End: span.End}, engine.IterOptions{})
	if err != nil {
		return err
	}
	defer iter.Close()
	for ok := iter.First(); ok; ok = iter.Next() {
		if err := ctxErr(ctx); err != nil {
			return err
		}
		commandID, ok := decodeProposalByCommandKey(e.key, iter.Key())
		if !ok {
			return dberrors.ErrCorruptState
		}
		value, err := iter.Value()
		if err != nil {
			return err
		}
		record, err := decodeDurableProposalRecord(value)
		if err != nil || record.manifest.CommandID != commandID {
			return dberrors.ErrCorruptState
		}
		paired, present, err := loadDurableProposalFrom(e.db.engine, encodeProposalByLastKey(e.key, record.manifest.LastOffset))
		if err != nil {
			return err
		}
		if !present || paired != record {
			return dberrors.ErrCorruptState
		}
	}
	return iter.Error()
}

// stageTruncateDurableProposals removes only complete suffix proposals and
// their entry identities in the caller's synchronous message mutation batch.
func (e *channelEntry) stageTruncateDurableProposals(ctx context.Context, batch *engine.Batch, to uint64) error {
	if err := e.validateDurableProposalCommandIndex(ctx); err != nil {
		return err
	}
	prefix := encodeProposalByLastPrefix(e.key)
	span := keycodec.NewPrefixSpan(prefix)
	iter, err := e.db.engine.NewIter(engine.Span{Start: span.Start, End: span.End}, engine.IterOptions{})
	if err != nil {
		return err
	}
	defer iter.Close()
	for ok := iter.First(); ok; ok = iter.Next() {
		if err := ctxErr(ctx); err != nil {
			return err
		}
		lastOffset, ok := decodeProposalByLastKey(e.key, iter.Key())
		if !ok {
			return dberrors.ErrCorruptState
		}
		value, err := iter.Value()
		if err != nil {
			return err
		}
		record, err := decodeDurableProposalRecord(value)
		if err != nil || record.manifest.LastOffset != lastOffset {
			return dberrors.ErrCorruptState
		}
		paired, present, err := loadDurableProposalFrom(e.db.engine, encodeProposalByCommandKey(e.key, record.manifest.CommandID))
		if err != nil {
			return err
		}
		if !present || paired != record {
			return dberrors.ErrCorruptState
		}
		if record.manifest.LastOffset <= to {
			continue
		}
		if record.manifest.BaseOffset < to {
			return dberrors.ErrConflict
		}
		if err := batch.Delete(encodeProposalByLastKey(e.key, record.manifest.LastOffset)); err != nil {
			return err
		}
		if err := batch.Delete(encodeProposalByCommandKey(e.key, record.manifest.CommandID)); err != nil {
			return err
		}
	}
	if err := iter.Error(); err != nil {
		return err
	}
	if to == ^uint64(0) {
		return nil
	}
	entrySpan := keycodec.NewPrefixSpan(encodeEntryIdentityPrefix(e.key))
	return batch.DeleteRange(engine.Span{Start: encodeEntryIdentityKey(e.key, to+1), End: entrySpan.End})
}
