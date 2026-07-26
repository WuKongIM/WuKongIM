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
	"sync"

	"golang.org/x/sync/semaphore"
)

const (
	segmentWorkingSetMultiplier      int64 = 3
	segmentWorkingSetOverheadBytes         = 16 << 20
	partitionWorkingSetOverheadBytes       = 16 << 20
	partitionStoreMemoryBudgetBytes        = 2*maxSegmentCiphertextBytes + 2*maxObjectPlaintextBytes + partitionWorkingSetOverheadBytes
	segmentStoreMemoryBudgetBytes          = partitionStoreMemoryBudgetBytes
)

// SegmentReference binds a Slot frontier to one exact signed commit record.
type SegmentReference struct {
	// SegmentID is the lowercase SHA-256 of the canonical SegmentHeader.
	SegmentID string `json:"segment_id"`
	// CommitKey is the deterministic immutable commit-record key.
	CommitKey string `json:"commit_key"`
	// CommitSHA256 authenticates the exact signed commit bytes.
	CommitSHA256 string `json:"commit_sha256"`
	// PlaintextBytes is the authenticated decompressed object size. Callers use
	// it to reserve memory before opening an immutable segment.
	PlaintextBytes int64 `json:"plaintext_bytes"`
}

// ReplicatedSegmentStore makes segments visible only after both copies commit.
type ReplicatedSegmentStore struct {
	// primary is the first explicit repository failure domain.
	primary Repository
	// secondary is the second explicit repository failure domain.
	secondary Repository
	// primaryRepair and secondaryRepair are explicit auditor-only writers.
	primaryRepair   RepairRepository
	secondaryRepair RepairRepository
	// codec owns compression and envelope encryption for payloads.
	codec *SegmentCodec
	// objectCodec validates legacy materialized-partition payload objects with
	// the same KMS boundary used by continuous segments.
	objectCodec *ObjectCodec
	// signer authenticates identical commit bytes stored in both repositories.
	signer ManifestSigner
	// signingKeyID is the configured key used for new commit proofs.
	signingKeyID string
	// memoryBudget bounds concurrent seal and open working sets per store.
	memoryBudget *semaphore.Weighted
	// partitionAuditCache holds only authenticated manifests for the active
	// durable audit cycle and is reset when the cycle identity changes.
	partitionAuditCacheMu    sync.Mutex
	partitionAuditCacheCycle string
	partitionAuditCache      []partitionAuditManifestCacheEntry
	partitionAuditCacheBytes int64
}

// NewReplicatedSegmentStore creates one deep segment commit and load boundary.
func NewReplicatedSegmentStore(primary, secondary Repository, codec *SegmentCodec, signer ManifestSigner, signingKeyID string) (*ReplicatedSegmentStore, error) {
	return newReplicatedSegmentStore(
		primary, secondary, nil, nil, codec, signer, signingKeyID,
	)
}

// NewReplicatedSegmentStoreWithRepair creates a store whose auditor receives
// explicit repair capabilities separate from ordinary create-only upload.
func NewReplicatedSegmentStoreWithRepair(
	primary, secondary Repository,
	primaryRepair, secondaryRepair RepairRepository,
	codec *SegmentCodec,
	signer ManifestSigner,
	signingKeyID string,
) (*ReplicatedSegmentStore, error) {
	if primary == nil || secondary == nil ||
		primaryRepair == nil || secondaryRepair == nil ||
		primaryRepair.Name() != primary.Name() ||
		secondaryRepair.Name() != secondary.Name() {
		return nil, fmt.Errorf(
			"%w: explicit segment repair repositories are invalid",
			ErrRepositoryIncomplete,
		)
	}
	return newReplicatedSegmentStore(
		primary, secondary, primaryRepair, secondaryRepair,
		codec, signer, signingKeyID,
	)
}

func newReplicatedSegmentStore(
	primary, secondary Repository,
	primaryRepair, secondaryRepair RepairRepository,
	codec *SegmentCodec,
	signer ManifestSigner,
	signingKeyID string,
) (*ReplicatedSegmentStore, error) {
	store := &ReplicatedSegmentStore{
		primary: primary, secondary: secondary, codec: codec,
		primaryRepair: primaryRepair, secondaryRepair: secondaryRepair,
		signer: signer, signingKeyID: strings.TrimSpace(signingKeyID),
		memoryBudget: semaphore.NewWeighted(segmentStoreMemoryBudgetBytes),
	}
	if codec != nil {
		store.objectCodec = NewObjectCodec(codec.keys, nil)
	}
	if err := store.validate(); err != nil {
		return nil, err
	}
	return store, nil
}

// Commit seals one new segment or repairs and reuses an existing committed copy.
func (s *ReplicatedSegmentStore) Commit(ctx context.Context, descriptor SegmentDescriptor, plaintext []byte) (SegmentReference, error) {
	if err := s.validate(); err != nil {
		return SegmentReference{}, err
	}
	if err := validateSegmentInput(descriptor, plaintext); err != nil {
		return SegmentReference{}, err
	}
	workingSetBytes := estimateSegmentWorkingSet(int64(len(plaintext)))
	if err := s.memoryBudget.Acquire(ctx, workingSetBytes); err != nil {
		return SegmentReference{}, fmt.Errorf("acquire segment commit memory budget: %w", err)
	}
	defer s.memoryBudget.Release(workingSetBytes)

	header, segmentID, canonical, err := buildSegmentIdentity(descriptor, plaintext)
	if err != nil {
		return SegmentReference{}, err
	}
	primaryCopy, err := s.loadCommitCopy(ctx, s.primary, segmentID)
	if err != nil {
		return SegmentReference{}, fmt.Errorf("%w: %s segment commit: %v", ErrRepositoryIncomplete, s.primary.Name(), err)
	}
	secondaryCopy, err := s.loadCommitCopy(ctx, s.secondary, segmentID)
	if err != nil {
		return SegmentReference{}, fmt.Errorf("%w: %s segment commit: %v", ErrRepositoryIncomplete, s.secondary.Name(), err)
	}
	if primaryCopy.found || secondaryCopy.found {
		return s.reuseCommitted(ctx, header, segmentID, primaryCopy, secondaryCopy)
	}

	sealed, err := s.codec.sealPrepared(ctx, descriptor.KMSKeyID, header, segmentID, canonical, plaintext)
	if err != nil {
		return SegmentReference{}, err
	}
	if sealed.ID != segmentID || sealed.Header != header {
		return SegmentReference{}, fmt.Errorf("%w: segment codec identity mismatch", ErrInvalidObject)
	}
	commit, err := SignSegmentCommit(ctx, SegmentCommit{
		Format: SegmentCommitFormat, Version: SegmentCommitVersion,
		SegmentID: segmentID, Header: header, Payload: sealed.Payload,
		PrimaryRepository: s.primary.Name(), SecondaryRepository: s.secondary.Name(),
	}, s.signer, s.signingKeyID)
	if err != nil {
		return SegmentReference{}, err
	}
	body, err := MarshalSegmentCommit(commit)
	if err != nil {
		return SegmentReference{}, err
	}
	bodyHash := sha256.Sum256(body)
	reference := SegmentReference{
		SegmentID: segmentID, CommitKey: segmentCommitKey(segmentID),
		CommitSHA256: hex.EncodeToString(bodyHash[:]), PlaintextBytes: header.PlaintextBytes,
	}
	if err := putAndVerify(ctx, s.primary, commit.Payload.Key, commit.Payload.CiphertextSHA256, sealed.Ciphertext); err != nil {
		return reference, fmt.Errorf("%w: %s segment payload: %v", ErrRepositoryIncomplete, s.primary.Name(), err)
	}
	if err := putAndVerify(ctx, s.secondary, commit.Payload.Key, commit.Payload.CiphertextSHA256, sealed.Ciphertext); err != nil {
		return reference, fmt.Errorf("%w: %s segment payload: %v", ErrRepositoryIncomplete, s.secondary.Name(), err)
	}
	if err := putAndVerify(ctx, s.secondary, reference.CommitKey, reference.CommitSHA256, body); err != nil {
		return reference, fmt.Errorf("%w: %s segment commit: %v", ErrRepositoryIncomplete, s.secondary.Name(), err)
	}
	if err := putAndVerify(ctx, s.primary, reference.CommitKey, reference.CommitSHA256, body); err != nil {
		return reference, fmt.Errorf("%w: %s segment commit: %v", ErrRepositoryIncomplete, s.primary.Name(), err)
	}
	return reference, nil
}

// Load requires matching commit and payload copies, then opens the primary copy.
func (s *ReplicatedSegmentStore) Load(ctx context.Context, reference SegmentReference) ([]byte, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	if err := validateSegmentReference(reference); err != nil {
		return nil, err
	}
	primaryCopy, err := s.loadCommitCopy(ctx, s.primary, reference.SegmentID)
	if err != nil {
		return nil, fmt.Errorf("%w: %s segment commit: %v", ErrRepositoryIncomplete, s.primary.Name(), err)
	}
	secondaryCopy, err := s.loadCommitCopy(ctx, s.secondary, reference.SegmentID)
	if err != nil {
		return nil, fmt.Errorf("%w: %s segment commit: %v", ErrRepositoryIncomplete, s.secondary.Name(), err)
	}
	if !primaryCopy.found || !secondaryCopy.found {
		return nil, fmt.Errorf("%w: segment commit is not present in both repositories", ErrRepositoryIncomplete)
	}
	if !bytes.Equal(primaryCopy.body, secondaryCopy.body) || primaryCopy.checksum != reference.CommitSHA256 {
		return nil, fmt.Errorf("%w: replicated segment commits disagree", ErrRepositoryIncomplete)
	}
	commit := primaryCopy.commit
	if commit.Header.PlaintextBytes != reference.PlaintextBytes {
		return nil, fmt.Errorf("%w: segment reference plaintext size mismatch", ErrObjectCorrupt)
	}
	if err := s.validateCommitRepositories(commit); err != nil {
		return nil, err
	}
	workingSetBytes := estimateSegmentWorkingSet(commit.Header.PlaintextBytes)
	if err := s.memoryBudget.Acquire(ctx, workingSetBytes); err != nil {
		return nil, fmt.Errorf("acquire segment load memory budget: %w", err)
	}
	defer s.memoryBudget.Release(workingSetBytes)
	if err := verifySegmentPayloadObject(ctx, s.primary, commit.Payload); err != nil {
		return nil, fmt.Errorf("%w: %s segment payload: %v", ErrRepositoryIncomplete, s.primary.Name(), err)
	}
	if err := verifySegmentPayloadObject(ctx, s.secondary, commit.Payload); err != nil {
		return nil, fmt.Errorf("%w: %s segment payload: %v", ErrRepositoryIncomplete, s.secondary.Name(), err)
	}
	ciphertext, err := readSegmentPayload(ctx, s.primary, commit.Payload)
	if err != nil {
		return nil, fmt.Errorf("%w: %s segment payload: %v", ErrRepositoryIncomplete, s.primary.Name(), err)
	}
	return s.codec.Open(ctx, commit.Header, commit.Payload, ciphertext)
}

// LoadCopy authenticates and opens exactly one selected repository copy. It is
// intended for disaster recovery after the immutable catalog proof has pinned
// the operator-selected failure domain; followers must never call it.
func (s *ReplicatedSegmentStore) LoadCopy(
	ctx context.Context,
	repository Repository,
	reference SegmentReference,
) ([]byte, error) {
	plaintext, _, err := s.loadCopy(ctx, repository, reference)
	return plaintext, err
}

// LoadCopyWithHeader returns the authenticated logical header with the
// selected-copy plaintext so restore can fence stream identity and record
// counts without a second repository or KMS call.
func (s *ReplicatedSegmentStore) LoadCopyWithHeader(
	ctx context.Context,
	repository Repository,
	reference SegmentReference,
) ([]byte, SegmentHeader, error) {
	return s.loadCopy(ctx, repository, reference)
}

func (s *ReplicatedSegmentStore) loadCopy(
	ctx context.Context,
	repository Repository,
	reference SegmentReference,
) ([]byte, SegmentHeader, error) {
	if err := s.validate(); err != nil {
		return nil, SegmentHeader{}, err
	}
	if repository == nil ||
		(repository.Name() != s.primary.Name() && repository.Name() != s.secondary.Name()) ||
		validateSegmentReference(reference) != nil {
		return nil, SegmentHeader{}, ErrInvalidObject
	}
	copy, err := s.loadCommitCopy(ctx, repository, reference.SegmentID)
	if err != nil {
		return nil, SegmentHeader{}, fmt.Errorf("%w: %s segment commit: %v", ErrRepositoryIncomplete, repository.Name(), err)
	}
	if !copy.found || copy.checksum != reference.CommitSHA256 ||
		copy.commit.Header.PlaintextBytes != reference.PlaintextBytes {
		return nil, SegmentHeader{}, fmt.Errorf("%w: selected segment commit proof is incomplete", ErrRepositoryIncomplete)
	}
	if err := s.validateCommitRepositories(copy.commit); err != nil {
		return nil, SegmentHeader{}, err
	}
	workingSetBytes := estimateSegmentWorkingSet(copy.commit.Header.PlaintextBytes)
	if err := s.memoryBudget.Acquire(ctx, workingSetBytes); err != nil {
		return nil, SegmentHeader{}, fmt.Errorf("acquire selected segment load memory budget: %w", err)
	}
	defer s.memoryBudget.Release(workingSetBytes)
	if err := verifySegmentPayloadObject(ctx, repository, copy.commit.Payload); err != nil {
		return nil, SegmentHeader{}, fmt.Errorf("%w: %s segment payload: %v", ErrRepositoryIncomplete, repository.Name(), err)
	}
	ciphertext, err := readSegmentPayload(ctx, repository, copy.commit.Payload)
	if err != nil {
		return nil, SegmentHeader{}, fmt.Errorf("%w: %s segment payload: %v", ErrRepositoryIncomplete, repository.Name(), err)
	}
	plaintext, err := s.codec.Open(ctx, copy.commit.Header, copy.commit.Payload, ciphertext)
	if err != nil {
		return nil, SegmentHeader{}, err
	}
	return plaintext, copy.commit.Header, nil
}

// VerifyCommit authenticates the exact commit proof in both repositories
// without opening payload bytes or walking the segment predecessor chain.
func (s *ReplicatedSegmentStore) VerifyCommit(ctx context.Context, reference SegmentReference) (SegmentHeader, error) {
	commit, err := s.verifyReplicatedCommitProof(ctx, reference)
	if err != nil {
		return SegmentHeader{}, err
	}
	return commit.Header, nil
}

// VerifyEnvelopeCopies authenticates the signed commit and immutable payload
// metadata in both repositories without downloading or decrypting payload
// bytes. Restore admission uses the returned predecessor link to walk the
// complete graph while reserving repository and KMS reads for the Slot Leader.
func (s *ReplicatedSegmentStore) VerifyEnvelopeCopies(
	ctx context.Context,
	reference SegmentReference,
) (SegmentHeader, error) {
	commit, err := s.verifyReplicatedCommitProof(ctx, reference)
	if err != nil {
		return SegmentHeader{}, err
	}
	if err := verifySegmentPayloadObject(ctx, s.primary, commit.Payload); err != nil {
		return SegmentHeader{}, fmt.Errorf(
			"%w: %s segment payload: %v",
			ErrRepositoryIncomplete, s.primary.Name(), err,
		)
	}
	if err := verifySegmentPayloadObject(ctx, s.secondary, commit.Payload); err != nil {
		return SegmentHeader{}, fmt.Errorf(
			"%w: %s segment payload: %v",
			ErrRepositoryIncomplete, s.secondary.Name(), err,
		)
	}
	return commit.Header, nil
}

func (s *ReplicatedSegmentStore) verifyReplicatedCommitProof(
	ctx context.Context,
	reference SegmentReference,
) (SegmentCommit, error) {
	if err := s.validate(); err != nil {
		return SegmentCommit{}, err
	}
	if err := validateSegmentReference(reference); err != nil {
		return SegmentCommit{}, err
	}
	primaryCopy, err := s.loadCommitCopy(ctx, s.primary, reference.SegmentID)
	if err != nil {
		return SegmentCommit{}, fmt.Errorf("%w: %s segment commit: %v", ErrRepositoryIncomplete, s.primary.Name(), err)
	}
	secondaryCopy, err := s.loadCommitCopy(ctx, s.secondary, reference.SegmentID)
	if err != nil {
		return SegmentCommit{}, fmt.Errorf("%w: %s segment commit: %v", ErrRepositoryIncomplete, s.secondary.Name(), err)
	}
	if !primaryCopy.found || !secondaryCopy.found ||
		!bytes.Equal(primaryCopy.body, secondaryCopy.body) ||
		primaryCopy.checksum != reference.CommitSHA256 {
		return SegmentCommit{}, fmt.Errorf("%w: replicated segment commit proof is incomplete", ErrRepositoryIncomplete)
	}
	if primaryCopy.commit.Header.PlaintextBytes != reference.PlaintextBytes {
		return SegmentCommit{}, fmt.Errorf("%w: segment reference plaintext size mismatch", ErrObjectCorrupt)
	}
	if err := s.validateCommitRepositories(primaryCopy.commit); err != nil {
		return SegmentCommit{}, err
	}
	return primaryCopy.commit, nil
}

type loadedSegmentCommit struct {
	body     []byte
	checksum string
	commit   SegmentCommit
	found    bool
}

// loadCommitCopy reads, bounds, authenticates, and binds one repository commit.
func (s *ReplicatedSegmentStore) loadCommitCopy(ctx context.Context, repository Repository, segmentID string) (loadedSegmentCommit, error) {
	key := segmentCommitKey(segmentID)
	reader, object, err := repository.Open(ctx, key)
	if errors.Is(err, ErrObjectNotFound) {
		return loadedSegmentCommit{}, nil
	}
	if err != nil {
		return loadedSegmentCommit{}, err
	}
	body, readErr := io.ReadAll(io.LimitReader(reader, maxSegmentCommitBytes+1))
	closeErr := reader.Close()
	if readErr != nil {
		return loadedSegmentCommit{}, readErr
	}
	if closeErr != nil {
		return loadedSegmentCommit{}, closeErr
	}
	bodyHash := sha256.Sum256(body)
	checksum := hex.EncodeToString(bodyHash[:])
	if len(body) == 0 || len(body) > maxSegmentCommitBytes || object.Key != key || object.Size != int64(len(body)) || object.SHA256 != checksum {
		return loadedSegmentCommit{}, fmt.Errorf("%w: segment commit object metadata mismatch", ErrObjectCorrupt)
	}
	commit, err := LoadSegmentCommit(ctx, body, s.signer)
	if err != nil {
		return loadedSegmentCommit{}, err
	}
	if commit.SegmentID != segmentID {
		return loadedSegmentCommit{}, fmt.Errorf("%w: segment commit identity mismatch", ErrInvalidObject)
	}
	return loadedSegmentCommit{body: body, checksum: checksum, commit: commit, found: true}, nil
}

// reuseCommitted repairs missing copies from an authenticated healthy payload
// without resealing the caller's matching logical plaintext.
func (s *ReplicatedSegmentStore) reuseCommitted(ctx context.Context, expectedHeader SegmentHeader, segmentID string, primaryCopy, secondaryCopy loadedSegmentCommit) (SegmentReference, error) {
	existing := primaryCopy
	if !existing.found {
		existing = secondaryCopy
	}
	if primaryCopy.found && secondaryCopy.found && !bytes.Equal(primaryCopy.body, secondaryCopy.body) {
		return SegmentReference{}, fmt.Errorf("%w: replicated segment commits disagree", ErrRepositoryIncomplete)
	}
	if !equalSegmentHeader(existing.commit.Header, expectedHeader) ||
		existing.commit.SegmentID != segmentID {
		return SegmentReference{}, fmt.Errorf("%w: committed segment does not match retry", ErrInvalidObject)
	}
	if err := s.validateCommitRepositories(existing.commit); err != nil {
		return SegmentReference{}, err
	}
	reference := SegmentReference{
		SegmentID: segmentID, CommitKey: segmentCommitKey(segmentID),
		CommitSHA256: existing.checksum, PlaintextBytes: existing.commit.Header.PlaintextBytes,
	}
	source, err := s.findHealthyPayload(ctx, existing.commit.Payload)
	if err != nil {
		return reference, err
	}
	if err := copySegmentPayloadIfMissing(ctx, source, s.primary, existing.commit.Payload); err != nil {
		return reference, fmt.Errorf("%w: repair %s segment payload: %v", ErrRepositoryIncomplete, s.primary.Name(), err)
	}
	if err := copySegmentPayloadIfMissing(ctx, source, s.secondary, existing.commit.Payload); err != nil {
		return reference, fmt.Errorf("%w: repair %s segment payload: %v", ErrRepositoryIncomplete, s.secondary.Name(), err)
	}
	if !secondaryCopy.found {
		if err := putAndVerify(ctx, s.secondary, reference.CommitKey, reference.CommitSHA256, existing.body); err != nil {
			return reference, fmt.Errorf("%w: repair %s segment commit: %v", ErrRepositoryIncomplete, s.secondary.Name(), err)
		}
	}
	if !primaryCopy.found {
		if err := putAndVerify(ctx, s.primary, reference.CommitKey, reference.CommitSHA256, existing.body); err != nil {
			return reference, fmt.Errorf("%w: repair %s segment commit: %v", ErrRepositoryIncomplete, s.primary.Name(), err)
		}
	}
	return reference, nil
}

func equalSegmentHeader(left, right SegmentHeader) bool {
	if left.Format != right.Format || left.Version != right.Version ||
		left.Logical != right.Logical || left.Checkpoint != right.Checkpoint ||
		left.SourceHighWatermark != right.SourceHighWatermark ||
		left.WatermarkAtUnixMillis != right.WatermarkAtUnixMillis ||
		left.PlaintextSHA256 != right.PlaintextSHA256 ||
		left.PlaintextBytes != right.PlaintextBytes ||
		(left.Previous == nil) != (right.Previous == nil) {
		return false
	}
	return left.Previous == nil || *left.Previous == *right.Previous
}

// findHealthyPayload returns a repository whose committed payload metadata verifies.
func (s *ReplicatedSegmentStore) findHealthyPayload(ctx context.Context, payload SegmentPayload) (Repository, error) {
	if err := verifySegmentPayloadObject(ctx, s.primary, payload); err == nil {
		return s.primary, nil
	}
	if err := verifySegmentPayloadObject(ctx, s.secondary, payload); err == nil {
		return s.secondary, nil
	}
	return nil, fmt.Errorf("%w: no healthy committed segment payload copy", ErrRepositoryIncomplete)
}

// estimateSegmentWorkingSet conservatively accounts for caller/plaintext,
// codec output, and codec workspace while one segment is hashed or transformed.
func estimateSegmentWorkingSet(plaintextBytes int64) int64 {
	return segmentWorkingSetMultiplier*plaintextBytes + segmentWorkingSetOverheadBytes
}

func (s *ReplicatedSegmentStore) validateCommitRepositories(commit SegmentCommit) error {
	if commit.PrimaryRepository != s.primary.Name() || commit.SecondaryRepository != s.secondary.Name() {
		return fmt.Errorf("%w: segment commit repository identity mismatch", ErrInvalidObject)
	}
	return nil
}

func (s *ReplicatedSegmentStore) validate() error {
	if s == nil || s.primary == nil || s.secondary == nil || s.codec == nil || s.codec.keys == nil || s.signer == nil || s.signingKeyID == "" || s.memoryBudget == nil {
		return fmt.Errorf("%w: replicated segment store dependencies are required", ErrRepositoryIncomplete)
	}
	if s.primary.Name() == "" || s.secondary.Name() == "" || s.primary.Name() == s.secondary.Name() {
		return fmt.Errorf("%w: segment repositories must have distinct names", ErrRepositoryIncomplete)
	}
	return nil
}

func validateSegmentReference(reference SegmentReference) error {
	if err := validateSHA256(reference.SegmentID); err != nil {
		return fmt.Errorf("%w: segment reference id: %v", ErrInvalidObject, err)
	}
	if reference.CommitKey != segmentCommitKey(reference.SegmentID) {
		return fmt.Errorf("%w: segment reference commit key mismatch", ErrInvalidObject)
	}
	if err := validateSHA256(reference.CommitSHA256); err != nil {
		return fmt.Errorf("%w: segment reference commit checksum: %v", ErrInvalidObject, err)
	}
	if reference.PlaintextBytes <= 0 || reference.PlaintextBytes > maxObjectPlaintextBytes {
		return fmt.Errorf("%w: segment reference plaintext size is invalid", ErrInvalidObject)
	}
	return nil
}

func verifySegmentPayloadObject(ctx context.Context, repository Repository, payload SegmentPayload) error {
	object, err := repository.Stat(ctx, payload.Key)
	if err != nil {
		return err
	}
	if object.Key != payload.Key || object.Size != payload.CiphertextBytes || object.SHA256 != payload.CiphertextSHA256 {
		return fmt.Errorf("%w: segment payload object metadata mismatch", ErrObjectCorrupt)
	}
	return nil
}

func copySegmentPayloadIfMissing(ctx context.Context, source, target Repository, payload SegmentPayload) error {
	if err := verifySegmentPayloadObject(ctx, target, payload); err == nil {
		return nil
	} else if !errors.Is(err, ErrObjectNotFound) {
		return err
	}
	reader, object, err := source.Open(ctx, payload.Key)
	if err != nil {
		return err
	}
	if object.Key != payload.Key || object.Size != payload.CiphertextBytes || object.SHA256 != payload.CiphertextSHA256 {
		_ = reader.Close()
		return fmt.Errorf("%w: source segment payload object metadata mismatch", ErrObjectCorrupt)
	}
	putErr := target.PutImmutable(ctx, payload.Key, payload.CiphertextBytes, payload.CiphertextSHA256, reader)
	closeErr := reader.Close()
	if putErr != nil && !errors.Is(putErr, ErrObjectExists) {
		return putErr
	}
	if closeErr != nil {
		return closeErr
	}
	return verifySegmentPayloadObject(ctx, target, payload)
}

func readSegmentPayload(ctx context.Context, repository Repository, payload SegmentPayload) ([]byte, error) {
	reader, object, err := repository.Open(ctx, payload.Key)
	if err != nil {
		return nil, err
	}
	body, readErr := io.ReadAll(io.LimitReader(reader, payload.CiphertextBytes+1))
	closeErr := reader.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if int64(len(body)) != payload.CiphertextBytes || object.Key != payload.Key || object.Size != payload.CiphertextBytes || object.SHA256 != payload.CiphertextSHA256 {
		return nil, fmt.Errorf("%w: segment payload object metadata mismatch", ErrObjectCorrupt)
	}
	return body, nil
}
