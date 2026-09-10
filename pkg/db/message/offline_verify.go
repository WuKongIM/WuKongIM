package message

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
)

// VerifyOfflineImportedLog checks every native proposal against its durable
// rows, predecessor and paired indexes. It reads at most 256 records and the
// supplied byte budget at a time in both native and recovery representation.
// The caller must keep the target stopped.
func (l *ChannelLog) VerifyOfflineImportedLog(ctx context.Context, maxBytes int) error {
	if ctx == nil || maxBytes <= 0 {
		return dberrors.ErrInvalidArgument
	}
	if err := l.beginUse(); err != nil {
		return err
	}
	defer l.endUse()
	l.appendMu.Lock()
	defer l.appendMu.Unlock()
	l.checkpointMu.Lock()
	defer l.checkpointMu.Unlock()
	// These shared read helpers do not acquire a compatibility lease or writer.
	reader := &ChannelStore{log: l}
	frontier, _, err := reader.loadDurableFrontierLocked(ctx)
	if err != nil {
		return err
	}
	if frontier.Committed != frontier.LEO {
		return dberrors.ErrCorruptState
	}
	var previous quorumlog.EntryIdentity
	for previous.Index < frontier.LEO {
		if err := ctx.Err(); err != nil {
			return err
		}
		first, found, err := loadDurableEntryIdentityFrom(l.db.engine, l.key, previous.Index+1)
		if err != nil {
			return err
		}
		if !found {
			return dberrors.ErrCorruptState
		}
		proposal, found, err := loadDurableProposalFrom(l.db.engine, encodeProposalByCommandKey(l.key, first.CommandID))
		if err != nil {
			return err
		}
		if !found {
			return dberrors.ErrCorruptState
		}
		m := proposal.manifest
		count := m.LastOffset - m.BaseOffset
		if !quorumlog.SupportedProposalVersion(m.Version) || m.BaseOffset != previous.Index || m.PreviousIndex != previous.Index || m.PreviousTerm != previous.LeaderTerm || m.PreviousDigest != previous.Digest || count == 0 || count > 256 || m.LastOffset > frontier.LEO {
			return dberrors.ErrCorruptState
		}
		paired, found, err := loadDurableProposalPairByLast(l.db.engine, l.key, m.LastOffset)
		if err != nil {
			return err
		}
		if !found || paired.manifest != m {
			return dberrors.ErrCorruptState
		}
		rows, err := l.readRows(ctx, m.BaseOffset+1, m.LastOffset, ReadOptions{Limit: int(count), MaxBytes: maxBytes})
		if err != nil {
			return err
		}
		if uint64(len(rows)) != count {
			return dberrors.ErrCorruptState
		}
		records, err := recordsFromRows(rows)
		if err != nil {
			return err
		}
		// Message rows do not duplicate the proposal's channel epoch. Restore
		// it from the manifest before recomputing the existing version-1 identities;
		// every separately stored entry is compared against that result below.
		used, recoveryUsed := 0, 0
		for i := range records {
			if records[i].SizeBytes > maxBytes-used {
				return dberrors.ErrInvalidArgument
			}
			used += records[i].SizeBytes
			row := rows[i]
			recoveryBytes := quorumlog.RecoveryRecordBytes(row.FromUID, row.ClientMsgNo, len(row.Payload))
			if recoveryBytes > maxBytes-recoveryUsed {
				return dberrors.ErrInvalidArgument
			}
			recoveryUsed += recoveryBytes
			records[i].Epoch = m.ChannelEpoch
		}
		identities, valid := deriveDurableProposalEntries(m, records, rows)
		if !valid {
			return dberrors.ErrCorruptState
		}
		for i, identity := range identities {
			if rows[i].MessageSeq != m.BaseOffset+uint64(i)+1 {
				return dberrors.ErrCorruptState
			}
			actual, found, err := loadDurableEntryIdentityFrom(l.db.engine, l.key, identity.Index)
			if err != nil {
				return err
			}
			if !found || actual != identity {
				return dberrors.ErrCorruptState
			}
		}
		previous = identities[len(identities)-1]
	}
	return l.validateDurableProposalCommandIndex(ctx)
}

// ReadOfflineMessage exposes existing durable columns for independent migration
// verification without changing normal reads, writes, key validation or codecs.
// Empty-sender client indexes are checked by exact key, never by an unbounded
// client-number listing or the sender/client idempotency API.
// The caller must keep the target stopped throughout its verification session.
func (l *ChannelLog) ReadOfflineMessage(ctx context.Context, seq uint64) (channelcompat.Message, bool, error) {
	if ctx == nil {
		return channelcompat.Message{}, false, dberrors.ErrInvalidArgument
	}
	if err := l.beginUse(); err != nil {
		return channelcompat.Message{}, false, err
	}
	defer l.endUse()
	row, found, err := l.getRowBySeq(ctx, seq)
	if err != nil || !found {
		return channelcompat.Message{}, found, err
	}
	if row.FromUID == "" && row.ClientMsgNo != "" {
		value, found, err := l.db.engine.Get(encodeMessageClientMsgNoIndexKey(l.key, row.ClientMsgNo, row.MessageSeq))
		if err != nil {
			return channelcompat.Message{}, false, err
		}
		if !found {
			return channelcompat.Message{}, false, dberrors.ErrCorruptState
		}
		indexedSeq, err := decodeMessageIDIndexValue(value)
		if err != nil {
			return channelcompat.Message{}, false, err
		}
		if indexedSeq != row.MessageSeq {
			return channelcompat.Message{}, false, dberrors.ErrCorruptState
		}
	}
	return channelMessageFromRow(row), true, nil
}
