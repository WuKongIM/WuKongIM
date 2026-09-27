package message

import (
	"bytes"
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
)

const willReceiptBackupVersion uint16 = 4

// A populated receipt archive requires readers that validate retained proof.
// The existing System section carries receipts; version 4 keeps v3 replay fields.
func hasCommittedWillReceipt(ctx context.Context, view messageBackupReadView, cut BackupChannelCut) (bool, error) {
	span := keycodec.NewPrefixSpan(willReceiptPrefix(cut.Key))
	iter, err := view.NewIter(engine.Span{Start: span.Start, End: span.End}, engine.IterOptions{})
	if err != nil {
		return false, err
	}
	defer iter.Close()
	for ok := iter.First(); ok; ok = iter.Next() {
		if err := ctxErr(ctx); err != nil {
			return false, err
		}
		value, err := iter.Value()
		if err != nil {
			return false, err
		}
		r, err := decodeWillReceipt(cut.Key, iter.Key(), value)
		if err != nil {
			return false, err
		}
		if r.MessageSeq <= cut.Checkpoint.HW {
			return true, nil
		}
	}
	return false, iter.Error()
}
func addWillReceiptBackupStats(stats *BackupSnapshotStats, key ChannelKey, entries []backupRawEntry) error {
	for _, entry := range entries {
		if !bytes.HasPrefix(entry.Key, willReceiptPrefix(key)) {
			continue
		}
		r, err := decodeWillReceipt(key, entry.Key, entry.Value)
		if err != nil {
			return err
		}
		if stats.WillReceiptCount == ^uint64(0) {
			return dberrors.ErrCorruptState
		}
		stats.WillReceiptCount++
		stats.MaxMessageID = max(stats.MaxMessageID, r.MessageID)
	}
	return nil
}

// remaining indexes receipt positions until their exact original is visited.
// Unvisited receipts require an explicit physical-retention witness.
type willBackupReceipts struct {
	entries   map[IdempotencyKey]WillReceipt
	remaining map[uint64]IdempotencyKey
}

func backupWillReceiptMap(key ChannelKey, entries []backupRawEntry) (willBackupReceipts, error) {
	var out willBackupReceipts
	for _, entry := range entries {
		if !bytes.HasPrefix(entry.Key, willReceiptPrefix(key)) {
			continue
		}
		id, ok := willReceiptIdentity(key, entry.Key)
		if !ok {
			return willBackupReceipts{}, dberrors.ErrCorruptState
		}
		r, err := decodeWillReceipt(key, entry.Key, entry.Value)
		if err != nil {
			return willBackupReceipts{}, err
		}
		if out.entries == nil {
			out.entries = make(map[IdempotencyKey]WillReceipt)
			out.remaining = make(map[uint64]IdempotencyKey)
		}
		if _, duplicate := out.entries[id]; duplicate {
			return willBackupReceipts{}, dberrors.ErrCorruptState
		}
		if _, duplicate := out.remaining[r.MessageSeq]; duplicate {
			return willBackupReceipts{}, dberrors.ErrCorruptState
		}
		out.entries[id], out.remaining[r.MessageSeq] = r, id
	}
	return out, nil
}
func validateWillBackupRow(receipts willBackupReceipts, row messageRow) error {
	identity, err := rowIdempotencyKey(row.FromUID, row.ClientMsgNo, row.PublicationMetadata)
	if err != nil {
		return err
	}
	if id, ok := receipts.remaining[row.MessageSeq]; ok && id != identity {
		return dberrors.ErrCorruptState
	}
	if retained, ok := receipts.entries[identity]; ok {
		actual, err := willReceiptFromRow(row)
		if err != nil || actual != retained {
			return dberrors.ErrCorruptState
		}
		delete(receipts.remaining, row.MessageSeq)
	}
	return nil
}
func validateWillBackupCoverage(receipts willBackupReceipts, header messageBackupChannelHeader) error {
	if len(receipts.remaining) == 0 {
		return nil
	}
	var through uint64
	for _, entry := range header.systemEntries {
		if bytes.Equal(entry.Key, encodeRetentionStateKey(header.key)) {
			state, err := decodeRetentionState(entry.Value)
			if err != nil {
				return err
			}
			through = state.PhysicalRetentionThroughSeq
		}
	}
	for seq := range receipts.remaining {
		if seq > through {
			return dberrors.ErrCorruptState
		}
	}
	return nil
}

// Restore cannot replace a receipt merely because the target checkpoint matches.
// The caller fences restore concurrency; preflight runs before any target writes.
func validateWillReceiptTarget(target *MessageDB, key ChannelKey, id IdempotencyKey, incoming WillReceipt) error {
	if target == nil {
		return nil
	}
	old, present, err := loadWillReceipt(target.engine, key, id)
	if err != nil {
		return err
	}
	if present && old != incoming {
		return dberrors.ErrConflict
	}
	return nil
}
func validateWillRestoreRow(target *MessageDB, key ChannelKey, row messageRow) error {
	if target == nil {
		return nil
	}
	id, err := rowIdempotencyKey(row.FromUID, row.ClientMsgNo, row.PublicationMetadata)
	if err != nil {
		return err
	}
	if id.ServerWillKey == "" {
		return nil
	}
	receipt, err := willReceiptFromRow(row)
	if err != nil {
		return err
	}
	return validateWillReceiptTarget(target, key, id, receipt)
}
