package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
)

const maxErasureLedgerRepositoryBytes = 1 << 20

type PermanentMessageErasure = backupcontract.PermanentMessageErasure
type ErasureLedgerReceipt = backupcontract.ErasureLedgerReceipt

// ErasureLedgerCoordinator is the bounded Controller seam used to serialize commits.
type ErasureLedgerCoordinator interface {
	ReserveErasureLedgerCommit(context.Context, backupusecase.ErasureLedgerRecordReference) (backupusecase.ErasureLedgerRecordReference, error)
	CommitErasureLedgerCommit(context.Context, backupartifact.ErasureStreamHead, string) error
	CoordinationState(context.Context) (backupusecase.State, error)
}

// PermanentErasureLedgerOptions configures signed encrypted dual-repository publication.
type PermanentErasureLedgerOptions struct {
	// Primary and Secondary are distinct immutable repository failure domains.
	Primary   backupartifact.Repository
	Secondary backupartifact.Repository
	// Codec envelope-encrypts Channel identity before repository publication.
	Codec *backupartifact.ObjectCodec
	// Coordinator serializes each Hash Slot's one-based contiguous commit head.
	Coordinator ErasureLedgerCoordinator
	// Signer authenticates record and commit metadata.
	Signer backupartifact.ManifestSigner
	// RepositoryID, SourceClusterID, and SourceGeneration fence this ledger namespace.
	RepositoryID     string
	SourceClusterID  string
	SourceGeneration string
	// HashSlotCount must match the source cluster's immutable logical partition count.
	HashSlotCount uint16
	// Now returns UTC artifact creation time.
	Now func() time.Time
	// NewAttemptID returns a safe unique immutable object namespace.
	NewAttemptID func() string
}

// PermanentErasureLedger publishes and repairs per-Slot monotonic encrypted erasure streams.
type PermanentErasureLedger struct {
	primary          backupartifact.Repository
	secondary        backupartifact.Repository
	codec            *backupartifact.ObjectCodec
	publisher        *backupartifact.ReplicatedPublisher
	coordinator      ErasureLedgerCoordinator
	signer           backupartifact.ManifestSigner
	repositoryID     string
	sourceClusterID  string
	sourceGeneration string
	streamNamespace  string
	hashSlotCount    uint16
	now              func() time.Time
	newAttemptID     func() string
}

// NewPermanentErasureLedger creates a permanent-erasure ledger publisher.
func NewPermanentErasureLedger(options PermanentErasureLedgerOptions) (*PermanentErasureLedger, error) {
	options.RepositoryID = strings.TrimSpace(options.RepositoryID)
	options.SourceClusterID = strings.TrimSpace(options.SourceClusterID)
	options.SourceGeneration = strings.TrimSpace(options.SourceGeneration)
	if options.Primary == nil || options.Secondary == nil || options.Primary.Name() == "" || options.Secondary.Name() == "" || options.Primary.Name() == options.Secondary.Name() ||
		options.Codec == nil || options.Coordinator == nil || options.Signer == nil ||
		options.RepositoryID == "" || options.SourceClusterID == "" || options.SourceGeneration == "" || options.HashSlotCount == 0 || options.Now == nil || options.NewAttemptID == nil {
		return nil, fmt.Errorf("backup erasure ledger: invalid options")
	}
	return &PermanentErasureLedger{
		primary: options.Primary, secondary: options.Secondary, codec: options.Codec,
		publisher: backupartifact.NewReplicatedPublisher(options.Primary, options.Secondary), coordinator: options.Coordinator,
		signer:       options.Signer,
		repositoryID: options.RepositoryID, sourceClusterID: options.SourceClusterID, sourceGeneration: options.SourceGeneration,
		streamNamespace: backupartifact.ComputeErasureLedgerStreamNamespace(options.RepositoryID, options.SourceClusterID, options.SourceGeneration),
		hashSlotCount:   options.HashSlotCount, now: options.Now, newAttemptID: options.NewAttemptID,
	}, nil
}

// RecordPermanentMessageErasure makes the erasure durable in both repositories
// before returning a commit receipt to the caller that will mutate live metadata.
func (l *PermanentErasureLedger) RecordPermanentMessageErasure(ctx context.Context, request PermanentMessageErasure) (ErasureLedgerReceipt, error) {
	request.ChannelID = strings.TrimSpace(request.ChannelID)
	if l == nil || request.ChannelID == "" || len(request.ChannelID) > 4096 || request.ChannelType == 0 || request.ThroughSeq == 0 || request.RequestedAtUnixMillis <= 0 {
		return ErasureLedgerReceipt{}, fmt.Errorf("backup erasure ledger: invalid permanent erasure request")
	}
	eventID := backupartifact.ComputeErasureEventID(l.repositoryID, l.sourceClusterID, l.sourceGeneration, request.ChannelID, request.ChannelType, request.ThroughSeq)
	hashSlot := routing.HashSlotForKey(request.ChannelID, l.hashSlotCount)
	state, err := l.coordinator.CoordinationState(ctx)
	if err != nil {
		return ErasureLedgerReceipt{}, err
	}
	stream := erasureStreamState(state.ErasureStreams, hashSlot)
	if stream != nil && stream.Pending != nil {
		if _, err := l.finalizeReference(ctx, *stream.Pending); err != nil {
			return ErasureLedgerReceipt{}, err
		}
		state, err = l.coordinator.CoordinationState(ctx)
		if err != nil {
			return ErasureLedgerReceipt{}, err
		}
		stream = erasureStreamState(state.ErasureStreams, hashSlot)
	}
	if stream != nil && stream.LastCommitted != nil && stream.LastCommitted.EventID == eventID {
		return l.finalizeReference(ctx, *stream.LastCommitted)
	}
	var head *backupartifact.ErasureStreamHead
	if stream != nil {
		head = stream.Head
	}
	if receipt, found, err := l.loadCommittedReceipt(ctx, eventID, hashSlot, head); err != nil {
		return ErasureLedgerReceipt{}, err
	} else if found {
		return receipt, nil
	}

	recordKey := backupartifact.ErasureLedgerRecordKey(hashSlot, eventID)
	if reference, found, err := l.loadRecordReference(ctx, recordKey, eventID); err != nil {
		return ErasureLedgerReceipt{}, err
	} else if found {
		reserved, err := l.reserveReference(ctx, reference)
		if err != nil {
			return ErasureLedgerReceipt{}, err
		}
		return l.finalizeReference(ctx, reserved)
	}

	event := backupartifact.ErasureLedgerEvent{
		Format: backupartifact.ErasureLedgerEventFormat, Version: backupartifact.ErasureLedgerEventVersion,
		RepositoryID: l.repositoryID, SourceClusterID: l.sourceClusterID, SourceGeneration: l.sourceGeneration,
		EventID: eventID, HashSlot: hashSlot, ChannelID: request.ChannelID, ChannelType: request.ChannelType,
		ThroughSeq: request.ThroughSeq, RequestedAtUnixMillis: request.RequestedAtUnixMillis,
	}
	plaintext, err := backupartifact.MarshalErasureLedgerEvent(event)
	if err != nil {
		return ErasureLedgerReceipt{}, err
	}
	attemptID := strings.TrimSpace(l.newAttemptID())
	if !safeObjectNamespace(attemptID) {
		return ErasureLedgerReceipt{}, fmt.Errorf("backup erasure ledger: invalid attempt id")
	}
	sealed, err := l.codec.Seal(ctx, backupartifact.ObjectDescriptor{
		Key:  "objects/erasure-ledger/" + eventID + "/" + attemptID + ".wkb",
		Kind: backupartifact.ObjectKindErasureLedger, HashSlot: hashSlot,
	}, plaintext)
	if err != nil {
		return ErasureLedgerReceipt{}, err
	}
	if err := l.publisher.ReplicateObject(ctx, sealed); err != nil {
		return ErasureLedgerReceipt{}, err
	}
	record, err := backupartifact.SignErasureLedgerRecord(ctx, backupartifact.ErasureLedgerRecord{
		Format: backupartifact.ErasureLedgerRecordFormat, Version: backupartifact.ErasureLedgerRecordVersion,
		RepositoryID: l.repositoryID, SourceClusterID: l.sourceClusterID, SourceGeneration: l.sourceGeneration,
		EventID: eventID, HashSlot: hashSlot, CreatedAtUnixMillis: l.now().UTC().UnixMilli(), Object: sealed.Entry,
	}, l.signer)
	if err != nil {
		return ErasureLedgerReceipt{}, err
	}
	recordBody, err := backupartifact.MarshalErasureLedgerRecord(record)
	if err != nil {
		return ErasureLedgerReceipt{}, err
	}
	recordBody, record, err = l.replicateOrAdoptRecord(ctx, recordKey, recordBody, eventID, hashSlot)
	if err != nil {
		return ErasureLedgerReceipt{}, err
	}
	recordSHA := sha256Hex(recordBody)
	reference := backupusecase.ErasureLedgerRecordReference{HashSlot: hashSlot, EventID: eventID, RecordKey: recordKey, RecordSHA256: recordSHA}
	reserved, err := l.reserveReference(ctx, reference)
	if err != nil {
		return ErasureLedgerReceipt{}, err
	}
	return l.finalizeReference(ctx, reserved)
}

func (l *PermanentErasureLedger) replicateOrAdoptRecord(
	ctx context.Context,
	key string,
	body []byte,
	eventID string,
	hashSlot uint16,
) ([]byte, backupartifact.ErasureLedgerRecord, error) {
	if err := l.putReplicatedExact(ctx, key, sha256Hex(body), body); err == nil {
		record, loadErr := backupartifact.LoadErasureLedgerRecord(ctx, body, l.signer)
		return body, record, loadErr
	}
	existingBody, existing, found, err := l.loadReplicatedRecordByKey(ctx, key)
	if err != nil || !found || existing.EventID != eventID || existing.HashSlot != hashSlot {
		if err != nil {
			return nil, backupartifact.ErasureLedgerRecord{}, err
		}
		return nil, backupartifact.ErasureLedgerRecord{}, fmt.Errorf("%w: erasure ledger record race is inconsistent", backupartifact.ErrRepositoryIncomplete)
	}
	if err := l.putReplicatedExact(ctx, key, sha256Hex(existingBody), existingBody); err != nil {
		return nil, backupartifact.ErasureLedgerRecord{}, err
	}
	if err := l.repairReplicatedObject(ctx, existing.Object); err != nil {
		return nil, backupartifact.ErasureLedgerRecord{}, err
	}
	return existingBody, existing, nil
}

func (l *PermanentErasureLedger) reserveReference(ctx context.Context, reference backupusecase.ErasureLedgerRecordReference) (backupusecase.ErasureLedgerRecordReference, error) {
	for attempt := 0; attempt < 8; attempt++ {
		reserved, err := l.coordinator.ReserveErasureLedgerCommit(ctx, reference)
		if err == nil {
			return reserved, nil
		}
		if !errors.Is(err, backupusecase.ErrErasureLedgerPending) {
			return backupusecase.ErasureLedgerRecordReference{}, err
		}
		state, loadErr := l.coordinator.CoordinationState(ctx)
		if loadErr != nil {
			return backupusecase.ErasureLedgerRecordReference{}, loadErr
		}
		stream := erasureStreamState(state.ErasureStreams, reference.HashSlot)
		if stream == nil || stream.Pending == nil {
			continue
		}
		if _, finalizeErr := l.finalizeReference(ctx, *stream.Pending); finalizeErr != nil {
			return backupusecase.ErasureLedgerRecordReference{}, finalizeErr
		}
	}
	return backupusecase.ErasureLedgerRecordReference{}, backupusecase.ErrStateConflict
}

func (l *PermanentErasureLedger) finalizeReference(ctx context.Context, reference backupusecase.ErasureLedgerRecordReference) (ErasureLedgerReceipt, error) {
	recordBody, record, err := l.loadReplicatedRecord(ctx, reference)
	if err != nil {
		return ErasureLedgerReceipt{}, err
	}
	if err := l.putReplicatedExact(ctx, reference.RecordKey, reference.RecordSHA256, recordBody); err != nil {
		return ErasureLedgerReceipt{}, err
	}
	if err := l.repairReplicatedObject(ctx, record.Object); err != nil {
		return ErasureLedgerReceipt{}, err
	}
	commitKey := backupartifact.ErasureLedgerCommitKey(l.streamNamespace, reference.HashSlot, reference.Sequence)
	commitBody, commit, found, err := l.loadReplicatedCommit(ctx, commitKey)
	if err != nil {
		return ErasureLedgerReceipt{}, err
	}
	if found {
		if !l.commitMatchesReference(commit, reference) {
			return ErasureLedgerReceipt{}, fmt.Errorf("%w: erasure ledger commit does not match Controller reference", backupartifact.ErrRepositoryIncomplete)
		}
	} else {
		previousCommitSHA, err := l.previousCommitSHA(ctx, reference)
		if err != nil {
			return ErasureLedgerReceipt{}, err
		}
		commit, err = backupartifact.SignErasureLedgerCommit(ctx, backupartifact.ErasureLedgerCommit{
			Format: backupartifact.ErasureLedgerCommitFormat, Version: backupartifact.ErasureLedgerCommitVersion,
			RepositoryID: l.repositoryID, SourceClusterID: l.sourceClusterID, SourceGeneration: l.sourceGeneration,
			HashSlot: reference.HashSlot, Sequence: reference.Sequence, PreviousCommitSHA256: previousCommitSHA,
			EventID: reference.EventID, RecordKey: reference.RecordKey, RecordSHA256: reference.RecordSHA256,
			CreatedAtUnixMillis: record.CreatedAtUnixMillis, PrimaryRepository: l.primary.Name(), SecondaryRepository: l.secondary.Name(),
		}, l.signer)
		if err != nil {
			return ErasureLedgerReceipt{}, err
		}
		commitBody, err = backupartifact.MarshalErasureLedgerCommit(commit)
		if err != nil {
			return ErasureLedgerReceipt{}, err
		}
	}
	if err := l.validateCommitPredecessor(ctx, commit); err != nil {
		return ErasureLedgerReceipt{}, err
	}
	commitBody, commit, err = l.replicateOrAdoptCommit(ctx, commitKey, commitBody, reference)
	if err != nil {
		return ErasureLedgerReceipt{}, err
	}
	commitSHA := sha256Hex(commitBody)
	if err := l.putReplicatedExact(ctx, backupartifact.ErasureLedgerReceiptKey(reference.EventID), commitSHA, commitBody); err != nil {
		return ErasureLedgerReceipt{}, err
	}
	head := backupartifact.ErasureStreamHead{
		HashSlot: reference.HashSlot, Sequence: reference.Sequence,
		CommitKey: commitKey, CommitSHA256: commitSHA,
	}
	if err := l.coordinator.CommitErasureLedgerCommit(ctx, head, reference.EventID); err != nil {
		return ErasureLedgerReceipt{}, err
	}
	return ErasureLedgerReceipt{HashSlot: reference.HashSlot, Sequence: reference.Sequence, EventID: reference.EventID}, nil
}

func (l *PermanentErasureLedger) replicateOrAdoptCommit(
	ctx context.Context,
	key string,
	body []byte,
	reference backupusecase.ErasureLedgerRecordReference,
) ([]byte, backupartifact.ErasureLedgerCommit, error) {
	checksum := sha256Hex(body)
	if err := l.putReplicatedExact(ctx, key, checksum, body); err == nil {
		commit, loadErr := backupartifact.LoadErasureLedgerCommit(ctx, body, l.signer)
		return body, commit, loadErr
	}
	existingBody, existing, found, err := l.loadReplicatedCommit(ctx, key)
	if err != nil || !found || !l.commitMatchesReference(existing, reference) {
		if err != nil {
			return nil, backupartifact.ErasureLedgerCommit{}, err
		}
		return nil, backupartifact.ErasureLedgerCommit{}, fmt.Errorf("%w: erasure ledger commit race is inconsistent", backupartifact.ErrRepositoryIncomplete)
	}
	if err := l.validateCommitPredecessor(ctx, existing); err != nil {
		return nil, backupartifact.ErasureLedgerCommit{}, err
	}
	if err := l.putReplicatedExact(ctx, key, sha256Hex(existingBody), existingBody); err != nil {
		return nil, backupartifact.ErasureLedgerCommit{}, err
	}
	return existingBody, existing, nil
}

func (l *PermanentErasureLedger) loadCommittedReceipt(ctx context.Context, eventID string, hashSlot uint16, head *backupartifact.ErasureStreamHead) (ErasureLedgerReceipt, bool, error) {
	receiptKey := backupartifact.ErasureLedgerReceiptKey(eventID)
	receiptBody, commit, found, err := l.loadReplicatedCommit(ctx, receiptKey)
	if err != nil || !found {
		return ErasureLedgerReceipt{}, found, err
	}
	reference := backupusecase.ErasureLedgerRecordReference{
		HashSlot: commit.HashSlot, Sequence: commit.Sequence, EventID: commit.EventID, RecordKey: commit.RecordKey, RecordSHA256: commit.RecordSHA256,
	}
	if head == nil || head.HashSlot != hashSlot || commit.HashSlot != hashSlot ||
		commit.EventID != eventID || commit.Sequence == 0 || commit.Sequence > head.Sequence ||
		!l.commitMatchesReference(commit, reference) {
		return ErasureLedgerReceipt{}, false, fmt.Errorf("%w: erasure ledger committed-event receipt mismatch", backupartifact.ErrRepositoryIncomplete)
	}
	commitKey := backupartifact.ErasureLedgerCommitKey(l.streamNamespace, commit.HashSlot, commit.Sequence)
	commitBody, sequenceCommit, found, err := l.loadReplicatedCommit(ctx, commitKey)
	if err != nil || !found || !bytes.Equal(receiptBody, commitBody) || !l.commitMatchesReference(sequenceCommit, reference) {
		return ErasureLedgerReceipt{}, false, fmt.Errorf("%w: erasure ledger committed-event sequence mismatch", backupartifact.ErrRepositoryIncomplete)
	}
	recordBody, record, err := l.loadReplicatedRecord(ctx, reference)
	if err != nil {
		return ErasureLedgerReceipt{}, false, err
	}
	if err := l.putReplicatedExact(ctx, reference.RecordKey, reference.RecordSHA256, recordBody); err != nil {
		return ErasureLedgerReceipt{}, false, err
	}
	if err := l.repairReplicatedObject(ctx, record.Object); err != nil {
		return ErasureLedgerReceipt{}, false, err
	}
	checksum := sha256Hex(commitBody)
	if err := l.putReplicatedExact(ctx, commitKey, checksum, commitBody); err != nil {
		return ErasureLedgerReceipt{}, false, err
	}
	if err := l.putReplicatedExact(ctx, receiptKey, checksum, receiptBody); err != nil {
		return ErasureLedgerReceipt{}, false, err
	}
	return ErasureLedgerReceipt{HashSlot: commit.HashSlot, Sequence: commit.Sequence, EventID: eventID}, true, nil
}

func (l *PermanentErasureLedger) loadRecordReference(ctx context.Context, key, eventID string) (backupusecase.ErasureLedgerRecordReference, bool, error) {
	body, record, found, err := l.loadReplicatedRecordByKey(ctx, key)
	if err != nil || !found {
		return backupusecase.ErasureLedgerRecordReference{}, found, err
	}
	if record.EventID != eventID {
		return backupusecase.ErasureLedgerRecordReference{}, false, fmt.Errorf("%w: erasure ledger record event mismatch", backupartifact.ErrRepositoryIncomplete)
	}
	checksum := sha256Hex(body)
	if err := l.putReplicatedExact(ctx, key, checksum, body); err != nil {
		return backupusecase.ErasureLedgerRecordReference{}, false, err
	}
	if err := l.repairReplicatedObject(ctx, record.Object); err != nil {
		return backupusecase.ErasureLedgerRecordReference{}, false, err
	}
	return backupusecase.ErasureLedgerRecordReference{HashSlot: record.HashSlot, EventID: eventID, RecordKey: key, RecordSHA256: checksum}, true, nil
}

func (l *PermanentErasureLedger) loadReplicatedRecord(ctx context.Context, reference backupusecase.ErasureLedgerRecordReference) ([]byte, backupartifact.ErasureLedgerRecord, error) {
	body, record, found, err := l.loadReplicatedRecordByKey(ctx, reference.RecordKey)
	if err != nil {
		return nil, backupartifact.ErasureLedgerRecord{}, err
	}
	if !found || sha256Hex(body) != reference.RecordSHA256 || record.EventID != reference.EventID {
		return nil, backupartifact.ErasureLedgerRecord{}, fmt.Errorf("%w: pending erasure ledger record is missing or mismatched", backupartifact.ErrRepositoryIncomplete)
	}
	return body, record, nil
}

func (l *PermanentErasureLedger) loadReplicatedRecordByKey(ctx context.Context, key string) ([]byte, backupartifact.ErasureLedgerRecord, bool, error) {
	body, found, err := l.loadMatchingReplicatedBytes(ctx, key, "record")
	if err != nil || !found {
		return nil, backupartifact.ErasureLedgerRecord{}, found, err
	}
	record, err := backupartifact.LoadErasureLedgerRecord(ctx, body, l.signer)
	if err != nil {
		return nil, backupartifact.ErasureLedgerRecord{}, false, err
	}
	if record.RepositoryID != l.repositoryID || record.SourceClusterID != l.sourceClusterID || record.SourceGeneration != l.sourceGeneration {
		return nil, backupartifact.ErasureLedgerRecord{}, false, fmt.Errorf("%w: erasure ledger record identity mismatch", backupartifact.ErrRepositoryIncomplete)
	}
	return body, record, true, nil
}

func (l *PermanentErasureLedger) loadReplicatedCommit(ctx context.Context, key string) ([]byte, backupartifact.ErasureLedgerCommit, bool, error) {
	body, found, err := l.loadMatchingReplicatedBytes(ctx, key, "commit")
	if err != nil || !found {
		return nil, backupartifact.ErasureLedgerCommit{}, found, err
	}
	commit, err := backupartifact.LoadErasureLedgerCommit(ctx, body, l.signer)
	if err != nil {
		return nil, backupartifact.ErasureLedgerCommit{}, false, err
	}
	return body, commit, true, nil
}

func (l *PermanentErasureLedger) loadMatchingReplicatedBytes(ctx context.Context, key, kind string) ([]byte, bool, error) {
	primaryBody, primaryFound, err := readOptionalRepositoryObject(ctx, l.primary, key, maxErasureLedgerRepositoryBytes)
	if err != nil {
		return nil, false, err
	}
	secondaryBody, secondaryFound, err := readOptionalRepositoryObject(ctx, l.secondary, key, maxErasureLedgerRepositoryBytes)
	if err != nil {
		return nil, false, err
	}
	if !primaryFound && !secondaryFound {
		return nil, false, nil
	}
	if primaryFound && secondaryFound && !bytes.Equal(primaryBody, secondaryBody) {
		return nil, false, fmt.Errorf("%w: replicated erasure ledger %s bytes disagree", backupartifact.ErrRepositoryIncomplete, kind)
	}
	if primaryFound {
		return primaryBody, true, nil
	}
	return secondaryBody, true, nil
}

func (l *PermanentErasureLedger) repairReplicatedObject(ctx context.Context, entry backupartifact.ObjectEntry) error {
	body, found, err := l.loadMatchingReplicatedBytes(ctx, entry.Key, "event object")
	if err != nil {
		return err
	}
	if !found || int64(len(body)) != entry.CiphertextBytes || sha256Hex(body) != entry.CiphertextSHA256 {
		return fmt.Errorf("%w: erasure ledger event object is missing or corrupt", backupartifact.ErrRepositoryIncomplete)
	}
	return l.putReplicatedExact(ctx, entry.Key, entry.CiphertextSHA256, body)
}

func (l *PermanentErasureLedger) putReplicatedExact(ctx context.Context, key, checksum string, body []byte) error {
	for _, repository := range []backupartifact.Repository{l.primary, l.secondary} {
		object, err := repository.Stat(ctx, key)
		if err == nil {
			if object.Size != int64(len(body)) || object.SHA256 != checksum {
				return fmt.Errorf("%w: %s object %q differs", backupartifact.ErrRepositoryIncomplete, repository.Name(), key)
			}
			continue
		}
		if !errors.Is(err, backupartifact.ErrObjectNotFound) {
			return err
		}
		if err := repository.PutImmutable(ctx, key, int64(len(body)), checksum, bytes.NewReader(body)); err != nil && !errors.Is(err, backupartifact.ErrObjectExists) {
			return err
		}
		object, err = repository.Stat(ctx, key)
		if err != nil || object.Size != int64(len(body)) || object.SHA256 != checksum {
			return fmt.Errorf("%w: %s object %q did not verify", backupartifact.ErrRepositoryIncomplete, repository.Name(), key)
		}
	}
	return nil
}

func (l *PermanentErasureLedger) commitMatchesReference(commit backupartifact.ErasureLedgerCommit, reference backupusecase.ErasureLedgerRecordReference) bool {
	return commit.RepositoryID == l.repositoryID && commit.SourceClusterID == l.sourceClusterID && commit.SourceGeneration == l.sourceGeneration &&
		commit.HashSlot == reference.HashSlot && commit.Sequence == reference.Sequence && commit.EventID == reference.EventID &&
		commit.RecordKey == reference.RecordKey && commit.RecordSHA256 == reference.RecordSHA256 &&
		commit.PrimaryRepository == l.primary.Name() && commit.SecondaryRepository == l.secondary.Name()
}

func (l *PermanentErasureLedger) previousCommitSHA(ctx context.Context, reference backupusecase.ErasureLedgerRecordReference) (string, error) {
	if reference.Sequence == 1 {
		return "", nil
	}
	state, err := l.coordinator.CoordinationState(ctx)
	if err != nil {
		return "", err
	}
	stream := erasureStreamState(state.ErasureStreams, reference.HashSlot)
	if stream == nil || stream.Head == nil || stream.Head.Sequence+1 != reference.Sequence {
		return "", fmt.Errorf("%w: erasure stream predecessor is unavailable", backupartifact.ErrRepositoryIncomplete)
	}
	return stream.Head.CommitSHA256, nil
}

func (l *PermanentErasureLedger) validateCommitPredecessor(ctx context.Context, commit backupartifact.ErasureLedgerCommit) error {
	if commit.Sequence == 1 {
		if commit.PreviousCommitSHA256 != "" {
			return fmt.Errorf("%w: first erasure stream commit has a predecessor", backupartifact.ErrRepositoryIncomplete)
		}
		return nil
	}
	key := backupartifact.ErasureLedgerCommitKey(l.streamNamespace, commit.HashSlot, commit.Sequence-1)
	body, previous, found, err := l.loadReplicatedCommit(ctx, key)
	if err != nil || !found {
		if err != nil {
			return err
		}
		return fmt.Errorf("%w: erasure stream predecessor is missing", backupartifact.ErrRepositoryIncomplete)
	}
	if previous.HashSlot != commit.HashSlot || previous.Sequence+1 != commit.Sequence ||
		sha256Hex(body) != commit.PreviousCommitSHA256 {
		return fmt.Errorf("%w: erasure stream predecessor mismatch", backupartifact.ErrRepositoryIncomplete)
	}
	return nil
}

func erasureStreamState(streams []backupusecase.ErasureStreamState, hashSlot uint16) *backupusecase.ErasureStreamState {
	for index := range streams {
		if streams[index].HashSlot == hashSlot {
			return &streams[index]
		}
	}
	return nil
}

func readOptionalRepositoryObject(ctx context.Context, repository backupartifact.Repository, key string, limit int64) ([]byte, bool, error) {
	reader, object, err := repository.Open(ctx, key)
	if errors.Is(err, backupartifact.ErrObjectNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	defer reader.Close()
	if object.Size < 0 || object.Size > limit {
		return nil, false, fmt.Errorf("%w: repository object %q exceeds read bound", backupartifact.ErrObjectCorrupt, key)
	}
	body, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, false, err
	}
	if int64(len(body)) != object.Size || sha256Hex(body) != object.SHA256 {
		return nil, false, fmt.Errorf("%w: repository object %q metadata mismatch", backupartifact.ErrObjectCorrupt, key)
	}
	return body, true, nil
}

func sha256Hex(body []byte) string {
	hash := sha256.Sum256(body)
	return hex.EncodeToString(hash[:])
}
