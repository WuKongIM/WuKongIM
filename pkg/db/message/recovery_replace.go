package message

import (
	"context"
	"errors"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
	channel "github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
)

// RecoveryProposal is one complete exact proposal copied from a quorum-proven
// recovery source.
type RecoveryProposal struct {
	Manifest DurableProposalManifest
	Records  []channel.Record
}

// ReplaceRecoverySuffixRequest fences one atomic suffix replacement to the
// exact local frontier inspected before repair.
type ReplaceRecoverySuffixRequest struct {
	Expected    DurableFrontier
	KeepThrough uint64
	Proposals   []RecoveryProposal
	Committed   uint64
}

// ReplaceRecoverySuffixResult reports the closed physical commit outcome.
type ReplaceRecoverySuffixResult struct {
	LastOffset uint64
	Outcome    quorumlog.AppendOutcome
}

// DurableRecoveryPageRequest bounds one donor read whose result may end before
// Through rather than split a proposal or exceed MaxBytes.
type DurableRecoveryPageRequest struct {
	From     uint64
	Through  uint64
	MaxBytes int
}

// DurableRecoveryPage is one append/checkpoint-consistent donor view.
type DurableRecoveryPage struct {
	DurableFrontier
	Records []channel.Record
	Entries []DurableEntryProbe
}

// ReadDurableRecoveryPage reads records and their exact identities while the
// canonical append and checkpoint locks keep the donor frontier unchanged.
func (s *ChannelStore) ReadDurableRecoveryPage(ctx context.Context, req DurableRecoveryPageRequest) (DurableRecoveryPage, error) {
	if ctx == nil || req.From == 0 || req.Through < req.From || req.Through-req.From >= 256 || req.MaxBytes <= 0 {
		return DurableRecoveryPage{}, channel.ErrInvalidArgument
	}
	if err := s.beginUse(); err != nil {
		return DurableRecoveryPage{}, err
	}
	defer s.endUse()
	if err := ctx.Err(); err != nil {
		return DurableRecoveryPage{}, err
	}
	s.log.appendMu.Lock()
	defer s.log.appendMu.Unlock()
	s.log.checkpointMu.Lock()
	defer s.log.checkpointMu.Unlock()
	frontier, _, err := s.loadDurableFrontierLocked(ctx)
	if err != nil {
		return DurableRecoveryPage{}, toChannelError(err)
	}
	if req.Through > frontier.LEO {
		return DurableRecoveryPage{}, channel.ErrCorruptState
	}
	first, present, err := loadDurableEntryIdentityFrom(s.log.db.engine, s.log.key, req.From)
	if err != nil || !present {
		if err == nil {
			err = dberrors.ErrCorruptState
		}
		return DurableRecoveryPage{}, toChannelError(err)
	}
	firstProposal, present, err := s.loadDurableProposal(encodeProposalByCommandKey(s.log.key, first.CommandID))
	if err != nil || !present || firstProposal.manifest.BaseOffset+1 != req.From {
		if err == nil {
			err = dberrors.ErrConflict
		}
		return DurableRecoveryPage{}, toChannelError(err)
	}
	page := DurableRecoveryPage{DurableFrontier: frontier}
	used := 0
	proposal := firstProposal
	for {
		if proposal.manifest.LastOffset > req.Through {
			if len(page.Records) == 0 {
				return DurableRecoveryPage{}, channel.ErrBackpressured
			}
			break
		}
		count := int(proposal.manifest.LastOffset - proposal.manifest.BaseOffset)
		remaining := req.MaxBytes - used
		if remaining <= 0 {
			break
		}
		rows, readErr := s.log.readRows(ctx, proposal.manifest.BaseOffset+1, proposal.manifest.LastOffset, ReadOptions{
			Limit: count, MaxBytes: remaining,
		})
		if readErr != nil {
			return DurableRecoveryPage{}, toChannelError(readErr)
		}
		if len(rows) != count || len(rows) == 0 || rows[0].MessageSeq != proposal.manifest.BaseOffset+1 ||
			rows[len(rows)-1].MessageSeq != proposal.manifest.LastOffset {
			if len(page.Records) == 0 {
				return DurableRecoveryPage{}, channel.ErrBackpressured
			}
			break
		}
		records, convertErr := recordsFromRows(rows)
		if convertErr != nil {
			return DurableRecoveryPage{}, toChannelError(convertErr)
		}
		proposalBytes := 0
		entries := make([]DurableEntryProbe, len(records))
		for index, record := range records {
			recordBytes := record.SizeBytes
			if proposalBytes > req.MaxBytes-recordBytes {
				return DurableRecoveryPage{}, channel.ErrBackpressured
			}
			proposalBytes += recordBytes
			identity, identityPresent, loadErr := loadDurableEntryIdentityFrom(s.log.db.engine, s.log.key, record.Index)
			if loadErr != nil || !identityPresent || identity.Index != record.Index || identity.CommandID != proposal.manifest.CommandID {
				if loadErr == nil {
					loadErr = dberrors.ErrCorruptState
				}
				return DurableRecoveryPage{}, toChannelError(loadErr)
			}
			record.Epoch = identity.ChannelEpoch
			records[index] = record
			entries[index] = DurableEntryProbe{Index: record.Index, Present: true, Identity: identity}
		}
		if used > req.MaxBytes-proposalBytes {
			if len(page.Records) == 0 {
				return DurableRecoveryPage{}, channel.ErrBackpressured
			}
			break
		}
		used += proposalBytes
		page.Records = append(page.Records, records...)
		page.Entries = append(page.Entries, entries...)
		if proposal.manifest.LastOffset == req.Through {
			break
		}
		next, nextPresent, loadErr := loadDurableEntryIdentityFrom(s.log.db.engine, s.log.key, proposal.manifest.LastOffset+1)
		if loadErr != nil || !nextPresent {
			if loadErr == nil {
				loadErr = dberrors.ErrCorruptState
			}
			return DurableRecoveryPage{}, toChannelError(loadErr)
		}
		nextProposal, nextProposalPresent, loadErr := s.loadDurableProposal(encodeProposalByCommandKey(s.log.key, next.CommandID))
		if loadErr != nil || !nextProposalPresent || nextProposal.manifest.BaseOffset != proposal.manifest.LastOffset {
			if loadErr == nil {
				loadErr = dberrors.ErrConflict
			}
			return DurableRecoveryPage{}, toChannelError(loadErr)
		}
		proposal = nextProposal
	}
	return page, nil
}

// ReplaceRecoverySuffix atomically deletes a divergent suffix and installs a
// fully verified exact replacement. No truncated intermediate state is ever
// published or committed.
func (s *ChannelStore) ReplaceRecoverySuffix(ctx context.Context, req ReplaceRecoverySuffixRequest) (ReplaceRecoverySuffixResult, error) {
	if ctx == nil {
		return recoveryReplaceError(channel.ErrInvalidArgument), channel.ErrInvalidArgument
	}
	if err := s.beginUse(); err != nil {
		return recoveryReplaceError(err), err
	}
	defer s.endUse()
	if err := ctx.Err(); err != nil {
		return recoveryReplaceError(err), err
	}
	s.log.appendMu.Lock()
	defer s.log.appendMu.Unlock()
	s.log.checkpointMu.Lock()
	defer s.log.checkpointMu.Unlock()

	current, checkpoint, err := s.loadDurableFrontierLocked(ctx)
	if err != nil {
		err = toChannelError(err)
		return recoveryReplaceError(err), err
	}
	if current != req.Expected || req.KeepThrough > current.LEO || req.KeepThrough < current.Committed ||
		req.Committed < current.Committed {
		return recoveryReplaceError(channel.ErrCorruptState), channel.ErrCorruptState
	}
	retention, present, err := s.log.loadRetentionState(ctx)
	if err != nil {
		err = toChannelError(err)
		return recoveryReplaceError(err), err
	}
	if present && req.KeepThrough < retention.LocalRetentionThroughSeq {
		return recoveryReplaceError(channel.ErrCorruptState), channel.ErrCorruptState
	}
	var retainedProposal durableProposalRecord
	if req.KeepThrough > 0 {
		proposal, present, loadErr := loadDurableProposalPairByLast(s.log.db.engine, s.log.key, req.KeepThrough)
		if loadErr != nil || !present || proposal.manifest.LastOffset != req.KeepThrough {
			if loadErr == nil {
				loadErr = dberrors.ErrConflict
			}
			err = toChannelError(loadErr)
			return recoveryReplaceError(err), err
		}
		retainedProposal = proposal
	}

	prepared, finalOffset, err := s.prepareRecoveryReplacementLocked(ctx, req, retainedProposal)
	if err != nil {
		err = toChannelError(err)
		return recoveryReplaceError(err), err
	}
	if req.Committed > finalOffset {
		return recoveryReplaceError(channel.ErrInvalidArgument), channel.ErrInvalidArgument
	}
	checkpoint.HW = req.Committed
	if err := validateCheckpoint(checkpoint); err != nil {
		err = toChannelError(err)
		return recoveryReplaceError(err), err
	}
	rows, err := s.log.readRows(ctx, req.KeepThrough+1, 0, ReadOptions{})
	if err != nil {
		err = toChannelError(err)
		return recoveryReplaceError(err), err
	}
	nextRetention, writeRetention, err := s.retentionStateAfterTruncate(ctx, finalOffset)
	if err != nil {
		return recoveryReplaceError(err), err
	}

	batch := s.log.db.engine.NewBatch()
	defer batch.Close()
	if err := s.log.channelEntry.stageTruncateDurableProposals(ctx, batch, req.KeepThrough); err != nil {
		err = toChannelError(err)
		return recoveryReplaceError(err), err
	}
	for _, row := range rows {
		if err := s.log.stageDeleteMessage(batch, messageFromRow(row)); err != nil {
			err = toChannelError(err)
			return recoveryReplaceError(err), err
		}
	}
	history := keycodec.NewPrefixSpan(encodeHistoryPrefix(s.log.key))
	if err := batch.DeleteRange(engine.Span{
		Start: encodeHistoryOffsetKey(s.log.key, req.KeepThrough+1), End: history.End,
	}); err != nil {
		err = toChannelError(err)
		return recoveryReplaceError(err), err
	}
	if err := s.log.channelEntry.stageCommitRows(
		batch, prepared.rows, &checkpoint, nil, prepared.proposals, prepared.entries,
	); err != nil {
		return recoveryReplaceError(err), err
	}
	if writeRetention {
		if err := batch.Set(encodeRetentionStateKey(s.log.key), encodeRetentionState(nextRetention)); err != nil {
			err = toChannelError(err)
			return recoveryReplaceError(err), err
		}
	}
	if err := batch.Commit(true); err != nil {
		err = toChannelError(err)
		return ReplaceRecoverySuffixResult{Outcome: quorumlog.AppendOutcomeUnknown}, err
	}
	if len(prepared.rows) == 0 {
		s.log.leo.Store(finalOffset)
		s.log.loaded.Store(true)
		s.log.clearDurableProposalTailLocked()
	} else {
		s.log.publishCommittedRows(prepared.rows, finalOffset, prepared.proposals, prepared.entries)
	}
	if s.log.idempotencyMembershipLoaded {
		cache := s.log.appendKeyCache
		for _, row := range prepared.rows {
			if row.FromUID != "" && row.ClientMsgNo != "" {
				s.log.idempotencyMembership.add(cache.idempotencyIndexKey(row.FromUID, row.ClientMsgNo))
			}
		}
	}
	return ReplaceRecoverySuffixResult{LastOffset: finalOffset, Outcome: quorumlog.AppendOutcomeDurable}, nil
}

func (s *ChannelStore) loadDurableFrontierLocked(ctx context.Context) (DurableFrontier, Checkpoint, error) {
	leo, err := s.log.loadLEOLocked(ctx)
	if err != nil {
		return DurableFrontier{}, Checkpoint{}, err
	}
	checkpoint, present, err := s.log.loadCheckpoint(ctx)
	if err != nil {
		return DurableFrontier{}, Checkpoint{}, err
	}
	if !present {
		checkpoint = Checkpoint{}
	}
	if checkpoint.HW > leo {
		return DurableFrontier{}, Checkpoint{}, dberrors.ErrCorruptState
	}
	frontier := DurableFrontier{LEO: leo, Committed: checkpoint.HW}
	if leo == 0 {
		return frontier, checkpoint, nil
	}
	proposal, present, err := loadDurableProposalPairByLast(s.log.db.engine, s.log.key, leo)
	if err != nil || !present {
		if err == nil {
			err = dberrors.ErrCorruptState
		}
		return DurableFrontier{}, Checkpoint{}, err
	}
	entry, present, err := loadDurableEntryIdentityFrom(s.log.db.engine, s.log.key, leo)
	if err != nil || !present || proposal.manifest.LastOffset != leo || proposal.manifest.Digest != entry.Digest ||
		proposal.manifest.ChannelEpoch != entry.ChannelEpoch || proposal.manifest.LeaderTerm != entry.LeaderTerm ||
		proposal.manifest.FenceVersion != entry.FenceVersion || proposal.manifest.CommandID != entry.CommandID {
		if err == nil {
			err = dberrors.ErrCorruptState
		}
		return DurableFrontier{}, Checkpoint{}, err
	}
	frontier.Manifest = proposal.manifest
	frontier.TailIdentity = entry
	return frontier, checkpoint, nil
}

// recoverySuffixChain owns the retained-boundary proof and the topology of one
// replacement suffix. It admits each proposal once, in order, so recovery
// cannot skip an offset, forge a predecessor, or reuse a proposal identity.
type recoverySuffixChain struct {
	lastOffset     uint64
	previousTerm   uint64
	previousDigest quorumlog.EntryDigest
	seenCommands   map[quorumlog.CommandID]struct{}
}

func newRecoverySuffixChain(keepThrough uint64, retainedProposal durableProposalRecord, retainedTail quorumlog.EntryIdentity, proposalCount int) (recoverySuffixChain, error) {
	chain := recoverySuffixChain{
		lastOffset:   keepThrough,
		seenCommands: make(map[quorumlog.CommandID]struct{}, proposalCount),
	}
	if keepThrough == 0 {
		return chain, nil
	}
	if retainedProposal.manifest.LastOffset != keepThrough || !durableProposalTailConsistent(retainedProposal, retainedTail) {
		return recoverySuffixChain{}, dberrors.ErrCorruptState
	}
	chain.previousTerm = retainedTail.LeaderTerm
	chain.previousDigest = retainedTail.Digest
	return chain, nil
}

func (c *recoverySuffixChain) admit(manifest DurableProposalManifest, recordCount int) error {
	if err := validateDurableProposalManifest(manifest, c.lastOffset, recordCount); err != nil {
		return err
	}
	if manifest.PreviousIndex != c.lastOffset || manifest.PreviousTerm != c.previousTerm ||
		manifest.PreviousDigest != c.previousDigest {
		return dberrors.ErrConflict
	}
	if _, duplicate := c.seenCommands[manifest.CommandID]; duplicate {
		return dberrors.ErrConflict
	}
	c.seenCommands[manifest.CommandID] = struct{}{}
	c.lastOffset = manifest.LastOffset
	c.previousTerm = manifest.LeaderTerm
	c.previousDigest = manifest.Digest
	return nil
}

func (s *ChannelStore) prepareRecoveryReplacementLocked(ctx context.Context, req ReplaceRecoverySuffixRequest, retainedProposal durableProposalRecord) (preparedCommitRows, uint64, error) {
	prepared := preparedCommitRows{store: s, baseOffset: req.KeepThrough, nextLEO: req.KeepThrough}
	var retainedTail quorumlog.EntryIdentity
	if req.KeepThrough > 0 {
		identity, present, err := loadDurableEntryIdentityFrom(s.log.db.engine, s.log.key, req.KeepThrough)
		if err != nil || !present {
			if err == nil {
				err = dberrors.ErrCorruptState
			}
			return preparedCommitRows{}, 0, err
		}
		retainedTail = identity
	}
	chain, err := newRecoverySuffixChain(req.KeepThrough, retainedProposal, retainedTail, len(req.Proposals))
	if err != nil {
		return preparedCommitRows{}, 0, err
	}
	seen := newAppendValidationSeen(recoveryRecordCount(req.Proposals))
	for _, proposal := range req.Proposals {
		manifest := proposal.Manifest
		if err := chain.admit(manifest, len(proposal.Records)); err != nil {
			return preparedCommitRows{}, 0, err
		}
		if err := s.validateRecoveryProposalKeyReuse(manifest, req.KeepThrough); err != nil {
			return preparedCommitRows{}, 0, err
		}
		rows, err := compatibilityRowsFromRecords(manifest.BaseOffset+1, proposal.Records)
		if err != nil {
			return preparedCommitRows{}, 0, err
		}
		entries, ok := deriveDurableProposalEntries(manifest, proposal.Records, rows)
		if !ok || len(entries) == 0 || entries[len(entries)-1].Digest != manifest.Digest {
			return preparedCommitRows{}, 0, dberrors.ErrConflict
		}
		if err := s.validateRecoveryRows(ctx, rows, req.KeepThrough, &seen); err != nil {
			return preparedCommitRows{}, 0, err
		}
		prepared.rows = append(prepared.rows, rows...)
		prepared.proposals = append(prepared.proposals, durableProposalRecord{manifest: manifest})
		prepared.entries = append(prepared.entries, entries...)
	}
	prepared.nextLEO = chain.lastOffset
	return prepared, chain.lastOffset, nil
}

func (s *ChannelStore) validateRecoveryProposalKeyReuse(manifest DurableProposalManifest, keepThrough uint64) error {
	byCommand, commandPresent, err := s.loadDurableProposal(encodeProposalByCommandKey(s.log.key, manifest.CommandID))
	if err != nil {
		return err
	}
	if commandPresent && byCommand.manifest.LastOffset <= keepThrough {
		return dberrors.ErrConflict
	}
	byLast, lastPresent, err := s.loadDurableProposal(encodeProposalByLastKey(s.log.key, manifest.LastOffset))
	if err != nil {
		return err
	}
	if lastPresent && byLast.manifest.LastOffset <= keepThrough {
		return dberrors.ErrConflict
	}
	return nil
}

func (s *ChannelStore) validateRecoveryRows(ctx context.Context, rows []messageRow, keepThrough uint64, seen *appendValidationSeen) error {
	cache := s.log.appendKeyCache
	for _, row := range rows {
		if err := row.validate(); err != nil {
			return err
		}
		if row.ChannelID != s.id.ID || row.ChannelType != s.id.Type || seen.rememberMessageID(row.MessageID) {
			return dberrors.ErrConflict
		}
		channelKey, seq, present, err := s.log.lookupGlobalMessageIDByKey(ctx, encodeGlobalMessageIDIndexKey(row.MessageID))
		if err != nil {
			return err
		}
		if present && (channelKey != s.log.key || seq <= keepThrough) {
			return dberrors.ErrConflict
		}
		if row.FromUID == "" || row.ClientMsgNo == "" {
			continue
		}
		key := IdempotencyKey{FromUID: row.FromUID, ClientMsgNo: row.ClientMsgNo}
		if seen.rememberIdempotencyKey(key) {
			return dberrors.ErrConflict
		}
		hit, present, err := s.log.lookupIdempotencyByKey(ctx, key, cache.idempotencyIndexKey(key.FromUID, key.ClientMsgNo))
		if err != nil {
			return err
		}
		if present && hit.MessageSeq <= keepThrough {
			return dberrors.ErrConflict
		}
	}
	return nil
}

func recoveryRecordCount(proposals []RecoveryProposal) int {
	total := 0
	for _, proposal := range proposals {
		total += len(proposal.Records)
	}
	return total
}

func recoveryReplaceError(err error) ReplaceRecoverySuffixResult {
	outcome := quorumlog.AppendOutcomeDefinitelyNotWritten
	if errors.Is(err, channel.ErrCorruptState) || errors.Is(err, dberrors.ErrCorruptState) || errors.Is(err, dberrors.ErrConflict) {
		outcome = quorumlog.AppendOutcomeConflict
	}
	return ReplaceRecoverySuffixResult{Outcome: outcome}
}
