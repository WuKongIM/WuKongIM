package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
)

const partitionAuditManifestCacheMaxBytes int64 = 64 << 20

// PartitionArtifactAuditNavigation is the compact authenticated information
// needed to advance through one partition graph.
type PartitionArtifactAuditNavigation struct {
	// Format identifies the authenticated manifest schema.
	Format string
	// HashSlot binds the manifest to the durable audit cursor.
	HashSlot uint16
	// ObjectCount is the number of immutable payloads in this layer.
	ObjectCount uint64
	// SourceHighWatermark and WatermarkAtUnixMillis identify the materialized
	// partition cut authenticated by the manifest reference.
	SourceHighWatermark   uint64
	WatermarkAtUnixMillis int64
}

// PartitionArtifactAuditReport contains the independently verified copies of
// one partition manifest or one encrypted payload selected from that manifest.
type PartitionArtifactAuditReport struct {
	// Navigation is populated from an authenticated healthy manifest copy.
	Navigation PartitionArtifactAuditNavigation
	// Copies contains exactly one result per configured repository.
	Copies []SegmentAuditCopy
}

type auditedPartitionArtifactCopy struct {
	report          SegmentAuditCopy
	manifest        *PartitionManifest
	manifestHealthy bool
	objectHealthy   bool
}

type partitionAuditManifestCacheEntry struct {
	repository string
	reference  PartitionReference
	manifest   *PartitionManifest
	bytes      int64
}

// BeginPartitionAuditCycle keeps authenticated manifest reuse bounded to one
// fixed durable auditor cycle. A process restart naturally begins empty.
func (s *ReplicatedSegmentStore) BeginPartitionAuditCycle(cycleID string) {
	if s == nil {
		return
	}
	s.partitionAuditCacheMu.Lock()
	defer s.partitionAuditCacheMu.Unlock()
	if s.partitionAuditCacheCycle == cycleID {
		return
	}
	s.partitionAuditCacheCycle = cycleID
	s.partitionAuditCache = nil
	s.partitionAuditCacheBytes = 0
}

// InspectPartitionArtifactCopies fully validates one partition manifest when
// objectIndex is -1, or one encrypted object in its graph when non-negative.
func (s *ReplicatedSegmentStore) InspectPartitionArtifactCopies(
	ctx context.Context,
	reference PartitionReference,
	objectIndex int,
) (PartitionArtifactAuditReport, error) {
	if err := s.validatePartitionAuditTarget(reference, objectIndex); err != nil {
		return PartitionArtifactAuditReport{}, err
	}
	report := PartitionArtifactAuditReport{
		Copies: make([]SegmentAuditCopy, 0, 2),
	}
	for _, repository := range []Repository{s.primary, s.secondary} {
		copyResult, err := s.auditPartitionArtifactCopy(
			ctx, repository, reference, objectIndex,
		)
		if err != nil {
			return PartitionArtifactAuditReport{}, err
		}
		report.Copies = append(report.Copies, copyResult.report)
		if copyResult.manifestHealthy && report.Navigation.Format == "" {
			report.Navigation = PartitionArtifactAuditNavigation{
				Format:                copyResult.manifest.Format,
				HashSlot:              copyResult.manifest.Cut.HashSlot,
				ObjectCount:           uint64(len(copyResult.manifest.Objects)),
				SourceHighWatermark:   copyResult.manifest.Cut.RaftIndex,
				WatermarkAtUnixMillis: copyResult.manifest.Cut.CommittedAtMillis,
			}
		}
	}
	return report, nil
}

// InspectPartitionArtifactEnvelopeCopies authenticates a partition manifest
// or verifies one referenced payload through provider metadata in both
// repositories. It never downloads or decrypts payload bytes, so restore
// admission can prove graph completeness without consuming Slot-Leader key
// and repository work.
func (s *ReplicatedSegmentStore) InspectPartitionArtifactEnvelopeCopies(
	ctx context.Context,
	reference PartitionReference,
	objectIndex int,
) (PartitionArtifactAuditReport, error) {
	if err := s.validatePartitionAuditTarget(reference, objectIndex); err != nil {
		return PartitionArtifactAuditReport{}, err
	}
	report := PartitionArtifactAuditReport{
		Copies: make([]SegmentAuditCopy, 0, 2),
	}
	for _, repository := range []Repository{s.primary, s.secondary} {
		copyReport := SegmentAuditCopy{Repository: repository.Name()}
		manifest, category, bytesRead, err := s.auditPartitionManifestCopy(
			ctx, repository, reference, true,
		)
		copyReport.StoredBytes = bytesRead
		if err != nil {
			return PartitionArtifactAuditReport{}, err
		}
		if category != "" {
			copyReport.Category = category
			report.Copies = append(report.Copies, copyReport)
			continue
		}
		if report.Navigation.Format == "" {
			report.Navigation = PartitionArtifactAuditNavigation{
				Format:                manifest.Format,
				HashSlot:              manifest.Cut.HashSlot,
				ObjectCount:           uint64(len(manifest.Objects)),
				SourceHighWatermark:   manifest.Cut.RaftIndex,
				WatermarkAtUnixMillis: manifest.Cut.CommittedAtMillis,
			}
		}
		if objectIndex >= 0 {
			entry := manifest.Objects[objectIndex]
			object, err := repository.Stat(ctx, entry.Key)
			if errors.Is(err, ErrObjectNotFound) {
				copyReport.Category = SegmentCorruptionMissing
				report.Copies = append(report.Copies, copyReport)
				continue
			}
			if err != nil {
				return PartitionArtifactAuditReport{}, err
			}
			if object.Key != entry.Key ||
				object.Size != entry.CiphertextBytes ||
				object.SHA256 != entry.CiphertextSHA256 {
				copyReport.Category = SegmentCorruptionCiphertext
				report.Copies = append(report.Copies, copyReport)
				continue
			}
		}
		copyReport.Healthy = true
		report.Copies = append(report.Copies, copyReport)
	}
	return report, nil
}

// RepairPartitionArtifactCopy restores one damaged manifest or payload from
// its fully authenticated peer and validates the target again.
func (s *ReplicatedSegmentStore) RepairPartitionArtifactCopy(
	ctx context.Context,
	reference PartitionReference,
	objectIndex int,
	targetRepository string,
) (int64, error) {
	if err := s.validatePartitionAuditTarget(reference, objectIndex); err != nil {
		return 0, err
	}
	target, peer, targetRepair, err := s.partitionAuditRepositories(targetRepository)
	if err != nil {
		return 0, err
	}
	sourceCopy, err := s.auditPartitionArtifactCopy(
		ctx, peer, reference, objectIndex,
	)
	if err != nil {
		return 0, err
	}
	if !sourceCopy.report.Healthy {
		return 0, fmt.Errorf(
			"%w: partition repair source is unhealthy",
			ErrRepositoryIncomplete,
		)
	}
	targetCopy, err := s.auditPartitionArtifactCopy(
		ctx, target, reference, objectIndex,
	)
	if err != nil {
		return 0, err
	}
	if targetCopy.report.Healthy {
		return 0, nil
	}
	var repairedBytes int64
	if !targetCopy.manifestHealthy {
		if err := repairRepositoryObjectFromPeer(
			ctx, peer, targetRepair, reference.Key,
			reference.Bytes, reference.SHA256,
		); err != nil {
			return repairedBytes, err
		}
		repairedBytes += reference.Bytes
		s.invalidatePartitionAuditManifest(target.Name(), reference)
	}
	if objectIndex >= 0 && !targetCopy.objectHealthy {
		entry := sourceCopy.manifest.Objects[objectIndex]
		if err := repairRepositoryObjectFromPeer(
			ctx, peer, targetRepair, entry.Key,
			entry.CiphertextBytes, entry.CiphertextSHA256,
		); err != nil {
			return repairedBytes, err
		}
		repairedBytes += entry.CiphertextBytes
	}
	s.invalidatePartitionAuditManifest(target.Name(), reference)
	revalidated, err := s.auditPartitionArtifactCopy(
		ctx, target, reference, objectIndex,
	)
	if err != nil {
		return repairedBytes, err
	}
	if !revalidated.report.Healthy {
		return repairedBytes, fmt.Errorf(
			"%w: repaired partition artifact failed revalidation",
			ErrRepositoryIncomplete,
		)
	}
	return repairedBytes, nil
}

func (s *ReplicatedSegmentStore) validatePartitionAuditTarget(
	reference PartitionReference,
	objectIndex int,
) error {
	if err := s.validate(); err != nil {
		return err
	}
	if s.objectCodec == nil || reference.Bytes <= 0 ||
		reference.ObjectCount == 0 || reference.CiphertextBytes == 0 ||
		objectIndex < -1 ||
		(objectIndex >= 0 && uint64(objectIndex) >= reference.ObjectCount) {
		return fmt.Errorf("%w: partition audit target is invalid", ErrInvalidObject)
	}
	return nil
}

func (s *ReplicatedSegmentStore) partitionAuditRepositories(
	targetRepository string,
) (Repository, Repository, RepairRepository, error) {
	switch targetRepository {
	case s.primary.Name():
		if s.primaryRepair == nil {
			return nil, nil, nil, fmt.Errorf(
				"%w: partition repair capability is not configured",
				ErrRepositoryIncomplete,
			)
		}
		return s.primary, s.secondary, s.primaryRepair, nil
	case s.secondary.Name():
		if s.secondaryRepair == nil {
			return nil, nil, nil, fmt.Errorf(
				"%w: partition repair capability is not configured",
				ErrRepositoryIncomplete,
			)
		}
		return s.secondary, s.primary, s.secondaryRepair, nil
	default:
		return nil, nil, nil, fmt.Errorf(
			"%w: unknown partition repair repository",
			ErrInvalidObject,
		)
	}
}

func (s *ReplicatedSegmentStore) auditPartitionArtifactCopy(
	ctx context.Context,
	repository Repository,
	reference PartitionReference,
	objectIndex int,
) (auditedPartitionArtifactCopy, error) {
	result := auditedPartitionArtifactCopy{
		report: SegmentAuditCopy{Repository: repository.Name()},
	}
	manifest, category, bytesRead, err := s.auditPartitionManifestCopy(
		ctx, repository, reference, objectIndex >= 0,
	)
	result.report.StoredBytes = bytesRead
	if err != nil {
		return result, err
	}
	if category != "" {
		result.report.Category = category
		return result, nil
	}
	result.manifest = manifest
	result.manifestHealthy = true
	if objectIndex < 0 {
		result.report.Healthy = true
		return result, nil
	}
	entry := manifest.Objects[objectIndex]
	workingSetBytes, err := estimatePartitionAuditWorkingSet(entry)
	if err != nil {
		result.report.Category = SegmentCorruptionCommitProof
		return result, nil
	}
	if err := s.memoryBudget.Acquire(ctx, workingSetBytes); err != nil {
		return result, err
	}
	defer s.memoryBudget.Release(workingSetBytes)
	healthy, category, objectBytes, err := s.auditPartitionObjectCopy(
		ctx, repository, entry,
	)
	result.report.StoredBytes += objectBytes
	if err != nil {
		return result, err
	}
	if !healthy {
		result.report.Category = category
		return result, nil
	}
	result.objectHealthy = true
	result.report.Healthy = true
	return result, nil
}

func (s *ReplicatedSegmentStore) auditPartitionManifestCopy(
	ctx context.Context,
	repository Repository,
	reference PartitionReference,
	allowCache bool,
) (*PartitionManifest, SegmentCorruptionCategory, int64, error) {
	if allowCache {
		if manifest, found := s.loadPartitionAuditManifestCache(
			repository.Name(), reference,
		); found {
			return manifest, "", reference.Bytes, nil
		}
	}
	reader, object, err := repository.Open(ctx, reference.Key)
	if errors.Is(err, ErrObjectNotFound) {
		return nil, SegmentCorruptionMissing, 0, nil
	}
	if errors.Is(err, ErrObjectCorrupt) {
		return nil, SegmentCorruptionChecksum, 0, nil
	}
	if err != nil {
		return nil, "", 0, err
	}
	body, readErr := io.ReadAll(io.LimitReader(reader, maxManifestBytes+1))
	closeErr := reader.Close()
	if readErr != nil {
		return nil, "", 0, readErr
	}
	if closeErr != nil {
		return nil, "", 0, closeErr
	}
	sum := sha256.Sum256(body)
	checksum := hex.EncodeToString(sum[:])
	if len(body) == 0 || len(body) > maxManifestBytes ||
		object.Key != reference.Key || object.Size != int64(len(body)) ||
		object.SHA256 != checksum {
		return nil, SegmentCorruptionChecksum, int64(len(body)), nil
	}
	if int64(len(body)) != reference.Bytes || checksum != reference.SHA256 {
		return nil, SegmentCorruptionCommitProof, int64(len(body)), nil
	}
	manifest, err := LoadPartitionManifest(body)
	if err != nil ||
		manifest.Cut.HashSlot != reference.HashSlot ||
		uint64(len(manifest.Objects)) != reference.ObjectCount ||
		manifest.Evidence != reference.Evidence {
		return nil, SegmentCorruptionCommitProof, int64(len(body)), nil
	}
	var ciphertextBytes uint64
	for _, entry := range manifest.Objects {
		ciphertextBytes += uint64(entry.CiphertextBytes)
	}
	if ciphertextBytes != reference.CiphertextBytes {
		return nil, SegmentCorruptionCommitProof, int64(len(body)), nil
	}
	s.storePartitionAuditManifestCache(repository.Name(), reference, manifest)
	return s.loadOrClonePartitionAuditManifest(
		repository.Name(), reference, manifest,
	), "", int64(len(body)), nil
}

func (s *ReplicatedSegmentStore) auditPartitionObjectCopy(
	ctx context.Context,
	repository Repository,
	entry ObjectEntry,
) (bool, SegmentCorruptionCategory, int64, error) {
	reader, object, err := repository.Open(ctx, entry.Key)
	if errors.Is(err, ErrObjectNotFound) {
		return false, SegmentCorruptionMissing, 0, nil
	}
	if errors.Is(err, ErrObjectCorrupt) {
		return false, SegmentCorruptionChecksum, 0, nil
	}
	if err != nil {
		return false, "", 0, err
	}
	body, readErr := io.ReadAll(io.LimitReader(reader, entry.CiphertextBytes+1))
	closeErr := reader.Close()
	if readErr != nil {
		return false, "", 0, readErr
	}
	if closeErr != nil {
		return false, "", 0, closeErr
	}
	sum := sha256.Sum256(body)
	checksum := hex.EncodeToString(sum[:])
	if object.Key != entry.Key || object.Size != int64(len(body)) ||
		object.SHA256 != checksum {
		return false, SegmentCorruptionChecksum, int64(len(body)), nil
	}
	if int64(len(body)) != entry.CiphertextBytes ||
		checksum != entry.CiphertextSHA256 {
		return false, SegmentCorruptionCiphertext, int64(len(body)), nil
	}
	if _, err := s.objectCodec.Open(ctx, entry, body); err != nil {
		if errors.Is(err, ErrObjectCorrupt) || errors.Is(err, ErrInvalidObject) {
			return false, SegmentCorruptionCiphertext, int64(len(body)), nil
		}
		return false, "", int64(len(body)), err
	}
	return true, "", int64(object.Size), nil
}

func estimatePartitionAuditWorkingSet(entry ObjectEntry) (int64, error) {
	if entry.PlaintextBytes < 0 || entry.PlaintextBytes > maxObjectPlaintextBytes ||
		entry.CiphertextBytes <= 0 ||
		entry.CiphertextBytes > maxSegmentCiphertextBytes {
		return 0, ErrInvalidObject
	}
	return 2*entry.CiphertextBytes +
		2*entry.PlaintextBytes +
		partitionWorkingSetOverheadBytes, nil
}

func repairRepositoryObjectFromPeer(
	ctx context.Context,
	source Repository,
	target RepairRepository,
	key string,
	size int64,
	checksum string,
) error {
	reader, object, err := source.Open(ctx, key)
	if err != nil {
		return err
	}
	if object.Key != key || object.Size != size || object.SHA256 != checksum {
		_ = reader.Close()
		return ErrObjectCorrupt
	}
	repairErr := target.RepairImmutable(
		ctx, key, size, checksum, io.LimitReader(reader, size+1),
	)
	closeErr := reader.Close()
	if repairErr != nil {
		return repairErr
	}
	return closeErr
}

func (s *ReplicatedSegmentStore) loadPartitionAuditManifestCache(
	repository string,
	reference PartitionReference,
) (*PartitionManifest, bool) {
	s.partitionAuditCacheMu.Lock()
	defer s.partitionAuditCacheMu.Unlock()
	for _, entry := range s.partitionAuditCache {
		if entry.repository == repository && entry.reference == reference {
			return entry.manifest, true
		}
	}
	return nil, false
}

func (s *ReplicatedSegmentStore) storePartitionAuditManifestCache(
	repository string,
	reference PartitionReference,
	manifest PartitionManifest,
) {
	estimatedBytes := reference.Bytes * 2
	if estimatedBytes <= 0 ||
		estimatedBytes > partitionAuditManifestCacheMaxBytes {
		return
	}
	s.partitionAuditCacheMu.Lock()
	defer s.partitionAuditCacheMu.Unlock()
	for _, entry := range s.partitionAuditCache {
		if entry.repository == repository && entry.reference == reference {
			return
		}
	}
	for len(s.partitionAuditCache) > 0 &&
		s.partitionAuditCacheBytes+estimatedBytes >
			partitionAuditManifestCacheMaxBytes {
		s.partitionAuditCacheBytes -= s.partitionAuditCache[0].bytes
		copy(s.partitionAuditCache, s.partitionAuditCache[1:])
		s.partitionAuditCache = s.partitionAuditCache[:len(s.partitionAuditCache)-1]
	}
	s.partitionAuditCache = append(
		s.partitionAuditCache,
		partitionAuditManifestCacheEntry{
			repository: repository, reference: reference,
			manifest: clonePartitionAuditManifest(manifest),
			bytes:    estimatedBytes,
		},
	)
	s.partitionAuditCacheBytes += estimatedBytes
}

func (s *ReplicatedSegmentStore) loadOrClonePartitionAuditManifest(
	repository string,
	reference PartitionReference,
	manifest PartitionManifest,
) *PartitionManifest {
	if cached, found := s.loadPartitionAuditManifestCache(
		repository, reference,
	); found {
		return cached
	}
	return clonePartitionAuditManifest(manifest)
}

func (s *ReplicatedSegmentStore) invalidatePartitionAuditManifest(
	repository string,
	reference PartitionReference,
) {
	s.partitionAuditCacheMu.Lock()
	defer s.partitionAuditCacheMu.Unlock()
	for index, entry := range s.partitionAuditCache {
		if entry.repository != repository || entry.reference != reference {
			continue
		}
		s.partitionAuditCacheBytes -= entry.bytes
		copy(s.partitionAuditCache[index:], s.partitionAuditCache[index+1:])
		s.partitionAuditCache = s.partitionAuditCache[:len(s.partitionAuditCache)-1]
		return
	}
}

func clonePartitionAuditManifest(manifest PartitionManifest) *PartitionManifest {
	out := manifest
	if manifest.BaselineCursor != nil {
		cursor := *manifest.BaselineCursor
		out.BaselineCursor = &cursor
	}
	out.Objects = append([]ObjectEntry(nil), manifest.Objects...)
	return &out
}
