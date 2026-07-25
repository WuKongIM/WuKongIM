package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
)

// SegmentCorruptionCategory is one bounded full-audit failure class.
type SegmentCorruptionCategory string

const (
	SegmentCorruptionMissing     SegmentCorruptionCategory = "missing"
	SegmentCorruptionChecksum    SegmentCorruptionCategory = "checksum"
	SegmentCorruptionCiphertext  SegmentCorruptionCategory = "ciphertext"
	SegmentCorruptionCommitProof SegmentCorruptionCategory = "commit_proof"
)

// SegmentAuditCopy reports one explicit repository's complete validation.
type SegmentAuditCopy struct {
	// Repository identifies one explicit failure-domain copy.
	Repository string
	// Healthy means commit proof, ciphertext, decrypt, and plaintext digest passed.
	Healthy bool
	// Category classifies the first bounded validation failure.
	Category SegmentCorruptionCategory
	// StoredBytes is the combined commit and payload size observed in this copy.
	StoredBytes int64
}

// SegmentAuditReport contains both independent repository results.
type SegmentAuditReport struct {
	// Header is populated from an authenticated healthy copy.
	Header SegmentHeader
	// Previous is the authenticated predecessor embedded in portable plaintext.
	Previous *SegmentReference
	// Copies contains exactly one result per configured repository.
	Copies []SegmentAuditCopy
}

type auditedSegmentCopy struct {
	report         SegmentAuditCopy
	commit         SegmentCommit
	commitBody     []byte
	ciphertext     []byte
	commitHealthy  bool
	payloadHealthy bool
	previous       *SegmentReference
}

// InspectSegmentCopies fully GETs, authenticates, decrypts, decompresses, and
// verifies the plaintext digest independently in each repository.
func (s *ReplicatedSegmentStore) InspectSegmentCopies(
	ctx context.Context,
	reference SegmentReference,
) (SegmentAuditReport, error) {
	if err := s.validate(); err != nil {
		return SegmentAuditReport{}, err
	}
	if err := validateSegmentReference(reference); err != nil {
		return SegmentAuditReport{}, err
	}
	workingSetBytes := estimateSegmentWorkingSet(reference.PlaintextBytes)
	if err := s.memoryBudget.Acquire(ctx, workingSetBytes); err != nil {
		return SegmentAuditReport{}, err
	}
	defer s.memoryBudget.Release(workingSetBytes)
	repositories := []Repository{s.primary, s.secondary}
	report := SegmentAuditReport{Copies: make([]SegmentAuditCopy, 0, 2)}
	for _, repository := range repositories {
		copyResult, err := s.auditSegmentCopy(ctx, repository, reference, false)
		if err != nil {
			return SegmentAuditReport{}, err
		}
		report.Copies = append(report.Copies, copyResult.report)
		if copyResult.report.Healthy {
			if report.Header == (SegmentHeader{}) {
				report.Header = copyResult.commit.Header
				report.Previous = cloneSegmentReference(copyResult.previous)
			} else if report.Header != copyResult.commit.Header {
				return SegmentAuditReport{}, fmt.Errorf("%w: healthy segment copies disagree", ErrRepositoryIncomplete)
			} else if !segmentReferencesEqual(report.Previous, copyResult.previous) {
				return SegmentAuditReport{}, fmt.Errorf("%w: healthy segment predecessors disagree", ErrRepositoryIncomplete)
			}
		}
	}
	return report, nil
}

// RepairSegmentCopy rebuilds one damaged repository from the authenticated
// healthy peer and then repeats the complete validation.
func (s *ReplicatedSegmentStore) RepairSegmentCopy(
	ctx context.Context,
	reference SegmentReference,
	targetRepository string,
) (int64, error) {
	if err := s.validate(); err != nil {
		return 0, err
	}
	if err := validateSegmentReference(reference); err != nil {
		return 0, err
	}
	workingSetBytes := estimateSegmentWorkingSet(reference.PlaintextBytes)
	if err := s.memoryBudget.Acquire(ctx, workingSetBytes); err != nil {
		return 0, err
	}
	defer s.memoryBudget.Release(workingSetBytes)
	var target, peer Repository
	var targetRepair RepairRepository
	switch targetRepository {
	case s.primary.Name():
		target, peer = s.primary, s.secondary
		targetRepair = s.primaryRepair
	case s.secondary.Name():
		target, peer = s.secondary, s.primary
		targetRepair = s.secondaryRepair
	default:
		return 0, fmt.Errorf("%w: unknown segment repair repository", ErrInvalidObject)
	}
	if targetRepair == nil {
		return 0, fmt.Errorf(
			"%w: segment repair capability is not configured",
			ErrRepositoryIncomplete,
		)
	}
	sourceCopy, err := s.auditSegmentCopy(ctx, peer, reference, true)
	if err != nil {
		return 0, err
	}
	if !sourceCopy.report.Healthy {
		return 0, fmt.Errorf("%w: segment repair source is unhealthy", ErrRepositoryIncomplete)
	}
	targetCopy, err := s.auditSegmentCopy(ctx, target, reference, false)
	if err != nil {
		return 0, err
	}
	if targetCopy.report.Healthy {
		return 0, nil
	}
	var repairedBytes int64
	targetPayloadHealthy := targetCopy.payloadHealthy
	if !targetCopy.commitHealthy {
		targetPayloadHealthy, err = auditPayloadAgainstCommit(
			ctx, s.codec, target, sourceCopy.commit,
		)
		if err != nil {
			return 0, err
		}
	}
	if !targetPayloadHealthy {
		if err := repairRepositoryObject(
			ctx, targetRepair, sourceCopy.commit.Payload.Key,
			sourceCopy.commit.Payload.CiphertextBytes,
			sourceCopy.commit.Payload.CiphertextSHA256,
			sourceCopy.ciphertext,
		); err != nil {
			return repairedBytes, err
		}
		repairedBytes += sourceCopy.commit.Payload.CiphertextBytes
	}
	if !targetCopy.commitHealthy {
		if err := repairRepositoryObject(
			ctx, targetRepair, reference.CommitKey, int64(len(sourceCopy.commitBody)),
			reference.CommitSHA256, sourceCopy.commitBody,
		); err != nil {
			return repairedBytes, err
		}
		repairedBytes += int64(len(sourceCopy.commitBody))
	}
	revalidated, err := s.auditSegmentCopy(ctx, target, reference, false)
	if err != nil {
		return repairedBytes, err
	}
	if !revalidated.report.Healthy {
		return repairedBytes, fmt.Errorf("%w: repaired segment copy failed revalidation", ErrRepositoryIncomplete)
	}
	return repairedBytes, nil
}

func (s *ReplicatedSegmentStore) auditSegmentCopy(
	ctx context.Context,
	repository Repository,
	reference SegmentReference,
	retainCiphertext bool,
) (auditedSegmentCopy, error) {
	result := auditedSegmentCopy{
		report: SegmentAuditCopy{Repository: repository.Name()},
	}
	reader, object, err := repository.Open(ctx, reference.CommitKey)
	if errors.Is(err, ErrObjectNotFound) {
		result.report.Category = SegmentCorruptionMissing
		return result, nil
	}
	if err != nil {
		if errors.Is(err, ErrObjectCorrupt) {
			result.report.Category = SegmentCorruptionChecksum
			return result, nil
		}
		return result, err
	}
	body, readErr := io.ReadAll(io.LimitReader(reader, maxSegmentCommitBytes+1))
	closeErr := reader.Close()
	if readErr != nil {
		return result, readErr
	}
	if closeErr != nil {
		return result, closeErr
	}
	bodyHash := sha256.Sum256(body)
	checksum := hex.EncodeToString(bodyHash[:])
	result.report.StoredBytes = int64(len(body))
	if len(body) == 0 || len(body) > maxSegmentCommitBytes ||
		object.Key != reference.CommitKey || object.Size != int64(len(body)) ||
		object.SHA256 != checksum {
		result.report.Category = SegmentCorruptionChecksum
		return result, nil
	}
	if checksum != reference.CommitSHA256 {
		result.report.Category = SegmentCorruptionCommitProof
		return result, nil
	}
	commit, err := LoadSegmentCommit(ctx, body, s.signer)
	if err != nil || commit.SegmentID != reference.SegmentID ||
		commit.Header.PlaintextBytes != reference.PlaintextBytes ||
		s.validateCommitRepositories(commit) != nil {
		result.report.Category = SegmentCorruptionCommitProof
		return result, nil
	}
	result.commit = commit
	result.commitBody = body
	result.commitHealthy = true
	payloadHealthy, ciphertext, previous, category, bytes, err := s.auditPayloadCopy(
		ctx, repository, commit, retainCiphertext,
	)
	result.report.StoredBytes += bytes
	if err != nil {
		return result, err
	}
	if !payloadHealthy {
		result.report.Category = category
		return result, nil
	}
	result.ciphertext = ciphertext
	result.payloadHealthy = true
	result.previous = previous
	result.report.Healthy = true
	return result, nil
}

func (s *ReplicatedSegmentStore) auditPayloadCopy(
	ctx context.Context,
	repository Repository,
	commit SegmentCommit,
	retainCiphertext bool,
) (bool, []byte, *SegmentReference, SegmentCorruptionCategory, int64, error) {
	reader, object, err := repository.Open(ctx, commit.Payload.Key)
	if errors.Is(err, ErrObjectNotFound) {
		return false, nil, nil, SegmentCorruptionMissing, 0, nil
	}
	if err != nil {
		if errors.Is(err, ErrObjectCorrupt) {
			return false, nil, nil, SegmentCorruptionChecksum, 0, nil
		}
		return false, nil, nil, "", 0, err
	}
	body, readErr := io.ReadAll(io.LimitReader(reader, commit.Payload.CiphertextBytes+1))
	closeErr := reader.Close()
	if readErr != nil {
		return false, nil, nil, "", 0, readErr
	}
	if closeErr != nil {
		return false, nil, nil, "", 0, closeErr
	}
	bodyHash := sha256.Sum256(body)
	checksum := hex.EncodeToString(bodyHash[:])
	if object.Key != commit.Payload.Key || object.Size != int64(len(body)) ||
		object.SHA256 != checksum {
		return false, nil, nil, SegmentCorruptionChecksum, int64(len(body)), nil
	}
	if int64(len(body)) != commit.Payload.CiphertextBytes ||
		checksum != commit.Payload.CiphertextSHA256 {
		return false, nil, nil, SegmentCorruptionCiphertext, int64(len(body)), nil
	}
	var stored []byte
	if retainCiphertext {
		stored = append([]byte(nil), body...)
	}
	plaintext, err := s.codec.Open(ctx, commit.Header, commit.Payload, body)
	if err != nil {
		if !errors.Is(err, ErrObjectCorrupt) && !errors.Is(err, ErrInvalidObject) {
			return false, nil, nil, "", int64(len(body)), err
		}
		return false, nil, nil, SegmentCorruptionCiphertext, int64(len(body)), nil
	}
	if int64(len(plaintext)) != commit.Header.PlaintextBytes {
		return false, nil, nil, SegmentCorruptionCiphertext, int64(len(body)), nil
	}
	previous, err := segmentPlaintextPrevious(commit.Header.Logical, plaintext)
	if err != nil {
		return false, nil, nil, SegmentCorruptionCiphertext, int64(len(body)), nil
	}
	return true, stored, previous, "", int64(len(body)), nil
}

func segmentPlaintextPrevious(
	logical SegmentLogicalDescriptor,
	plaintext []byte,
) (*SegmentReference, error) {
	switch logical.Stream {
	case SegmentStreamMetadata, SegmentStreamMessages:
		batch, err := LoadSegmentBatch(plaintext)
		if err != nil {
			return nil, nil
		}
		if batch.HashSlot != logical.HashSlot ||
			batch.Generation != logical.Generation ||
			batch.Stream != logical.Stream ||
			batch.Sequence != logical.Sequence {
			return nil, ErrObjectCorrupt
		}
		return cloneSegmentReference(batch.Previous), nil
	case SegmentStreamMessageCursor, SegmentStreamMessageBaselineCursor:
		batch, err := LoadMessageCursorBatch(plaintext)
		if err != nil {
			return nil, nil
		}
		if batch.HashSlot != logical.HashSlot ||
			batch.Generation != logical.Generation ||
			batch.Sequence != logical.Sequence {
			return nil, ErrObjectCorrupt
		}
		return cloneSegmentReference(batch.Previous), nil
	default:
		return nil, nil
	}
}

func segmentReferencesEqual(left, right *SegmentReference) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func auditPayloadAgainstCommit(
	ctx context.Context,
	codec *SegmentCodec,
	repository Repository,
	commit SegmentCommit,
) (bool, error) {
	reader, object, err := repository.Open(ctx, commit.Payload.Key)
	if errors.Is(err, ErrObjectNotFound) || errors.Is(err, ErrObjectCorrupt) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	body, readErr := io.ReadAll(io.LimitReader(reader, commit.Payload.CiphertextBytes+1))
	closeErr := reader.Close()
	if readErr != nil {
		return false, readErr
	}
	if closeErr != nil {
		return false, closeErr
	}
	hash := sha256.Sum256(body)
	checksum := hex.EncodeToString(hash[:])
	if object.Key != commit.Payload.Key || object.Size != int64(len(body)) ||
		object.SHA256 != checksum ||
		int64(len(body)) != commit.Payload.CiphertextBytes ||
		checksum != commit.Payload.CiphertextSHA256 {
		return false, nil
	}
	plaintext, err := codec.Open(ctx, commit.Header, commit.Payload, body)
	if err != nil {
		if !errors.Is(err, ErrObjectCorrupt) && !errors.Is(err, ErrInvalidObject) {
			return false, err
		}
		return false, nil
	}
	return int64(len(plaintext)) == commit.Header.PlaintextBytes, nil
}

func repairRepositoryObject(
	ctx context.Context,
	repository RepairRepository,
	key string,
	size int64,
	checksum string,
	body []byte,
) error {
	return repository.RepairImmutable(
		ctx, key, size, checksum, bytes.NewReader(body),
	)
}
