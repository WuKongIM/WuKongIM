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
	CommitErasureLedgerCommit(context.Context, uint64, string) error
	CoordinationState(context.Context) (backupusecase.State, error)
}

// PermanentErasureLedgerOptions configures signed encrypted dual-repository publication.
type PermanentErasureLedgerOptions struct {
	// Primary and Secondary are distinct immutable repository failure domains.
	Primary   backupartifact.Repository
	Secondary backupartifact.Repository
	// Codec envelope-encrypts Channel identity before repository publication.
	Codec *backupartifact.ObjectCodec
	// Coordinator serializes the one-based contiguous commit boundary.
	Coordinator ErasureLedgerCoordinator
	// Signer and SigningKeyID authenticate record and commit metadata.
	Signer       backupartifact.ManifestSigner
	SigningKeyID string
	// KMSKeyID protects encrypted event payload data keys.
	KMSKeyID string
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

// PermanentErasureLedger publishes and repairs one monotonic encrypted erasure ledger.
type PermanentErasureLedger struct {
	primary          backupartifact.Repository
	secondary        backupartifact.Repository
	codec            *backupartifact.ObjectCodec
	publisher        *backupartifact.ReplicatedPublisher
	coordinator      ErasureLedgerCoordinator
	signer           backupartifact.ManifestSigner
	signingKeyID     string
	kmsKeyID         string
	repositoryID     string
	sourceClusterID  string
	sourceGeneration string
	hashSlotCount    uint16
	now              func() time.Time
	newAttemptID     func() string
}

// NewPermanentErasureLedger creates a permanent-erasure ledger publisher.
func NewPermanentErasureLedger(options PermanentErasureLedgerOptions) (*PermanentErasureLedger, error) {
	options.SigningKeyID = strings.TrimSpace(options.SigningKeyID)
	options.KMSKeyID = strings.TrimSpace(options.KMSKeyID)
	options.RepositoryID = strings.TrimSpace(options.RepositoryID)
	options.SourceClusterID = strings.TrimSpace(options.SourceClusterID)
	options.SourceGeneration = strings.TrimSpace(options.SourceGeneration)
	if options.Primary == nil || options.Secondary == nil || options.Primary.Name() == "" || options.Secondary.Name() == "" || options.Primary.Name() == options.Secondary.Name() ||
		options.Codec == nil || options.Coordinator == nil || options.Signer == nil || options.SigningKeyID == "" || options.KMSKeyID == "" ||
		options.RepositoryID == "" || options.SourceClusterID == "" || options.SourceGeneration == "" || options.HashSlotCount == 0 || options.Now == nil || options.NewAttemptID == nil {
		return nil, fmt.Errorf("backup erasure ledger: invalid options")
	}
	return &PermanentErasureLedger{
		primary: options.Primary, secondary: options.Secondary, codec: options.Codec,
		publisher: backupartifact.NewReplicatedPublisher(options.Primary, options.Secondary), coordinator: options.Coordinator,
		signer: options.Signer, signingKeyID: options.SigningKeyID, kmsKeyID: options.KMSKeyID,
		repositoryID: options.RepositoryID, sourceClusterID: options.SourceClusterID, sourceGeneration: options.SourceGeneration,
		hashSlotCount: options.HashSlotCount, now: options.Now, newAttemptID: options.NewAttemptID,
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
	state, err := l.coordinator.CoordinationState(ctx)
	if err != nil {
		return ErasureLedgerReceipt{}, err
	}
	if state.PendingErasureLedger != nil {
		if _, err := l.finalizeReference(ctx, *state.PendingErasureLedger); err != nil {
			return ErasureLedgerReceipt{}, err
		}
		state, err = l.coordinator.CoordinationState(ctx)
		if err != nil {
			return ErasureLedgerReceipt{}, err
		}
	}
	if state.LastCommittedErasureLedger != nil && state.LastCommittedErasureLedger.EventID == eventID {
		return l.finalizeReference(ctx, *state.LastCommittedErasureLedger)
	}
	if receipt, found, err := l.loadCommittedReceipt(ctx, eventID, state.ErasureLedgerBoundary); err != nil {
		return ErasureLedgerReceipt{}, err
	} else if found {
		return receipt, nil
	}

	hashSlot := routing.HashSlotForKey(request.ChannelID, l.hashSlotCount)
	recordKey := backupartifact.ErasureLedgerRecordKey(hashSlot, eventID)
	if reference, found, err := l.loadRecordReference(ctx, recordKey, eventID); err != nil {
		return ErasureLedgerReceipt{}, err
	} else if found {
		reserved, err := l.coordinator.ReserveErasureLedgerCommit(ctx, reference)
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
	if !safeJobID(attemptID) {
		return ErasureLedgerReceipt{}, fmt.Errorf("backup erasure ledger: invalid attempt id")
	}
	sealed, err := l.codec.Seal(ctx, backupartifact.ObjectDescriptor{
		Key: "objects/erasure-ledger/" + eventID + "/" + attemptID + ".wkb", Kind: backupartifact.ObjectKindErasureLedger, HashSlot: hashSlot, KMSKeyID: l.kmsKeyID,
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
	}, l.signer, l.signingKeyID)
	if err != nil {
		return ErasureLedgerReceipt{}, err
	}
	recordBody, err := backupartifact.MarshalErasureLedgerRecord(record)
	if err != nil {
		return ErasureLedgerReceipt{}, err
	}
	recordSHA := sha256Hex(recordBody)
	if err := l.putReplicatedExact(ctx, recordKey, recordSHA, recordBody); err != nil {
		return ErasureLedgerReceipt{}, err
	}
	reference := backupusecase.ErasureLedgerRecordReference{EventID: eventID, RecordKey: recordKey, RecordSHA256: recordSHA}
	reserved, err := l.coordinator.ReserveErasureLedgerCommit(ctx, reference)
	if err != nil {
		return ErasureLedgerReceipt{}, err
	}
	return l.finalizeReference(ctx, reserved)
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
	commitKey := backupartifact.ErasureLedgerCommitKey(reference.Sequence)
	commitBody, commit, found, err := l.loadReplicatedCommit(ctx, commitKey)
	if err != nil {
		return ErasureLedgerReceipt{}, err
	}
	if found {
		if !l.commitMatchesReference(commit, reference) {
			return ErasureLedgerReceipt{}, fmt.Errorf("%w: erasure ledger commit does not match Controller reference", backupartifact.ErrRepositoryIncomplete)
		}
	} else {
		commit, err = backupartifact.SignErasureLedgerCommit(ctx, backupartifact.ErasureLedgerCommit{
			Format: backupartifact.ErasureLedgerCommitFormat, Version: backupartifact.ErasureLedgerCommitVersion,
			RepositoryID: l.repositoryID, SourceClusterID: l.sourceClusterID, SourceGeneration: l.sourceGeneration,
			Sequence: reference.Sequence, EventID: reference.EventID, RecordKey: reference.RecordKey, RecordSHA256: reference.RecordSHA256,
			CreatedAtUnixMillis: l.now().UTC().UnixMilli(), PrimaryRepository: l.primary.Name(), SecondaryRepository: l.secondary.Name(),
		}, l.signer, l.signingKeyID)
		if err != nil {
			return ErasureLedgerReceipt{}, err
		}
		commitBody, err = backupartifact.MarshalErasureLedgerCommit(commit)
		if err != nil {
			return ErasureLedgerReceipt{}, err
		}
	}
	if err := l.putReplicatedExact(ctx, commitKey, sha256Hex(commitBody), commitBody); err != nil {
		return ErasureLedgerReceipt{}, err
	}
	if err := l.putReplicatedExact(ctx, backupartifact.ErasureLedgerReceiptKey(reference.EventID), sha256Hex(commitBody), commitBody); err != nil {
		return ErasureLedgerReceipt{}, err
	}
	if err := l.coordinator.CommitErasureLedgerCommit(ctx, reference.Sequence, reference.EventID); err != nil {
		return ErasureLedgerReceipt{}, err
	}
	return ErasureLedgerReceipt{Sequence: reference.Sequence, EventID: reference.EventID}, nil
}

func (l *PermanentErasureLedger) loadCommittedReceipt(ctx context.Context, eventID string, boundary uint64) (ErasureLedgerReceipt, bool, error) {
	receiptKey := backupartifact.ErasureLedgerReceiptKey(eventID)
	receiptBody, commit, found, err := l.loadReplicatedCommit(ctx, receiptKey)
	if err != nil || !found {
		return ErasureLedgerReceipt{}, found, err
	}
	reference := backupusecase.ErasureLedgerRecordReference{
		Sequence: commit.Sequence, EventID: commit.EventID, RecordKey: commit.RecordKey, RecordSHA256: commit.RecordSHA256,
	}
	if commit.EventID != eventID || commit.Sequence == 0 || commit.Sequence > boundary || !l.commitMatchesReference(commit, reference) {
		return ErasureLedgerReceipt{}, false, fmt.Errorf("%w: erasure ledger committed-event receipt mismatch", backupartifact.ErrRepositoryIncomplete)
	}
	commitKey := backupartifact.ErasureLedgerCommitKey(commit.Sequence)
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
	return ErasureLedgerReceipt{Sequence: commit.Sequence, EventID: eventID}, true, nil
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
	return backupusecase.ErasureLedgerRecordReference{EventID: eventID, RecordKey: key, RecordSHA256: checksum}, true, nil
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
		commit.Sequence == reference.Sequence && commit.EventID == reference.EventID && commit.RecordKey == reference.RecordKey && commit.RecordSHA256 == reference.RecordSHA256 &&
		commit.PrimaryRepository == l.primary.Name() && commit.SecondaryRepository == l.secondary.Name()
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
