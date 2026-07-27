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

	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
)

// ErasureIntegrityArtifactKind identifies one node in a committed erasure event.
type ErasureIntegrityArtifactKind string

const (
	ErasureIntegrityArtifactCommit  ErasureIntegrityArtifactKind = "commit"
	ErasureIntegrityArtifactReceipt ErasureIntegrityArtifactKind = "receipt"
	ErasureIntegrityArtifactRecord  ErasureIntegrityArtifactKind = "record"
	ErasureIntegrityArtifactEvent   ErasureIntegrityArtifactKind = "event"
)

// ErasureIntegrityAuditTarget binds one durable cursor to an exact ledger node.
type ErasureIntegrityAuditTarget struct {
	Kind                 ErasureIntegrityArtifactKind
	HashSlot             uint16
	Sequence             uint64
	CommitKey            string
	ExpectedCommitSHA256 string
	EventID              string
	RecordKey            string
	RecordSHA256         string
}

// ErasureIntegrityAuditReport contains independently authenticated copies and
// navigation metadata disclosed by a healthy signed node.
type ErasureIntegrityAuditReport struct {
	Copies []backupartifact.SegmentAuditCopy
	Commit backupartifact.ErasureLedgerCommit
	Record backupartifact.ErasureLedgerRecord
	Event  backupartifact.ErasureLedgerEvent
}

// ErasureCopyAuditor validates and repairs one exact permanent-erasure node.
type ErasureCopyAuditor interface {
	InspectErasureArtifactCopies(
		context.Context,
		ErasureIntegrityAuditTarget,
	) (ErasureIntegrityAuditReport, error)
	RepairErasureArtifactCopy(
		context.Context,
		ErasureIntegrityAuditTarget,
		string,
	) (int64, error)
}

// ReplicatedErasureIntegrityAuditor verifies the complete signed and encrypted
// erasure chain without relying on the restore-time whole-ledger loader.
type ReplicatedErasureIntegrityAuditor struct {
	primary, secondary             backupartifact.Repository
	primaryRepair, secondaryRepair backupartifact.RepairRepository
	codec                          *backupartifact.ObjectCodec
	signer                         backupartifact.ManifestSigner
	repositoryID                   string
	sourceClusterID                string
	sourceGeneration               string
	streamNamespace                string
	hashSlotCount                  uint16
}

// ReplicatedErasureIntegrityAuditorOptions configures the portable ledger audit.
type ReplicatedErasureIntegrityAuditorOptions struct {
	Primary, Secondary             backupartifact.Repository
	PrimaryRepair, SecondaryRepair backupartifact.RepairRepository
	Codec                          *backupartifact.ObjectCodec
	Signer                         backupartifact.ManifestSigner
	RepositoryID                   string
	SourceClusterID                string
	SourceGeneration               string
	HashSlotCount                  uint16
}

// NewReplicatedErasureIntegrityAuditor creates an explicit repair-only adapter.
func NewReplicatedErasureIntegrityAuditor(
	options ReplicatedErasureIntegrityAuditorOptions,
) (*ReplicatedErasureIntegrityAuditor, error) {
	options.RepositoryID = strings.TrimSpace(options.RepositoryID)
	options.SourceClusterID = strings.TrimSpace(options.SourceClusterID)
	options.SourceGeneration = strings.TrimSpace(options.SourceGeneration)
	if options.Primary == nil || options.Secondary == nil ||
		options.PrimaryRepair == nil || options.SecondaryRepair == nil ||
		options.Primary.Name() == "" ||
		options.Primary.Name() == options.Secondary.Name() ||
		options.PrimaryRepair.Name() != options.Primary.Name() ||
		options.SecondaryRepair.Name() != options.Secondary.Name() ||
		options.Codec == nil || options.Signer == nil ||
		options.RepositoryID == "" || options.SourceClusterID == "" ||
		options.SourceGeneration == "" || options.HashSlotCount == 0 {
		return nil, fmt.Errorf(
			"backup erasure integrity auditor: dependencies are invalid",
		)
	}
	return &ReplicatedErasureIntegrityAuditor{
		primary: options.Primary, secondary: options.Secondary,
		primaryRepair:   options.PrimaryRepair,
		secondaryRepair: options.SecondaryRepair,
		codec:           options.Codec, signer: options.Signer,
		repositoryID:     options.RepositoryID,
		sourceClusterID:  options.SourceClusterID,
		sourceGeneration: options.SourceGeneration,
		streamNamespace: backupartifact.ComputeErasureLedgerStreamNamespace(
			options.RepositoryID,
			options.SourceClusterID,
			options.SourceGeneration,
		),
		hashSlotCount: options.HashSlotCount,
	}, nil
}

// InspectErasureArtifactCopies authenticates both repository copies independently.
func (a *ReplicatedErasureIntegrityAuditor) InspectErasureArtifactCopies(
	ctx context.Context,
	target ErasureIntegrityAuditTarget,
) (ErasureIntegrityAuditReport, error) {
	if target.Kind == ErasureIntegrityArtifactEvent {
		return a.inspectErasureEventCopies(ctx, target)
	}
	spec, err := a.artifactSpec(ctx, target)
	if err != nil {
		return ErasureIntegrityAuditReport{}, err
	}
	report := ErasureIntegrityAuditReport{
		Copies: make([]backupartifact.SegmentAuditCopy, 0, 2),
	}
	for _, repository := range []backupartifact.Repository{
		a.primary, a.secondary,
	} {
		copyResult, _, decoded, err := inspectErasureArtifactCopy(
			ctx, repository, spec,
		)
		if err != nil {
			return ErasureIntegrityAuditReport{}, err
		}
		report.Copies = append(report.Copies, copyResult)
		if !copyResult.Healthy {
			continue
		}
		switch target.Kind {
		case ErasureIntegrityArtifactCommit,
			ErasureIntegrityArtifactReceipt:
			report.Commit = decoded.commit
		case ErasureIntegrityArtifactRecord:
			report.Record = decoded.record
		case ErasureIntegrityArtifactEvent:
			report.Event = decoded.event
		}
	}
	return report, nil
}

// RepairErasureArtifactCopy restores one exact unhealthy node and revalidates it.
func (a *ReplicatedErasureIntegrityAuditor) RepairErasureArtifactCopy(
	ctx context.Context,
	target ErasureIntegrityAuditTarget,
	targetRepository string,
) (int64, error) {
	if target.Kind == ErasureIntegrityArtifactEvent {
		return a.repairErasureEventCopy(
			ctx, target, targetRepository,
		)
	}
	spec, err := a.artifactSpec(ctx, target)
	if err != nil {
		return 0, err
	}
	targetCopy, source, repair, err := a.repairRepositories(targetRepository)
	if err != nil {
		return 0, err
	}
	sourceResult, sourceBody, _, err := inspectErasureArtifactCopy(
		ctx, source, spec,
	)
	if err != nil {
		return 0, err
	}
	if !sourceResult.Healthy {
		return 0, fmt.Errorf(
			"%w: erasure repair source is unhealthy",
			backupartifact.ErrRepositoryIncomplete,
		)
	}
	targetResult, _, _, err := inspectErasureArtifactCopy(
		ctx, targetCopy, spec,
	)
	if err != nil {
		return 0, err
	}
	if targetResult.Healthy {
		return 0, nil
	}
	if err := repair.RepairImmutable(
		ctx, spec.key, int64(len(sourceBody)),
		sha256Hex(sourceBody), bytes.NewReader(sourceBody),
	); err != nil {
		return 0, err
	}
	revalidated, repairedBody, _, err := inspectErasureArtifactCopy(
		ctx, targetCopy, spec,
	)
	if err != nil || !revalidated.Healthy ||
		!bytes.Equal(sourceBody, repairedBody) {
		if err != nil {
			return int64(len(sourceBody)), err
		}
		return int64(len(sourceBody)), fmt.Errorf(
			"%w: repaired erasure artifact failed revalidation",
			backupartifact.ErrRepositoryIncomplete,
		)
	}
	return int64(len(sourceBody)), nil
}

type erasureIntegrityArtifactSpec struct {
	key              string
	expectedSize     int64
	expectedChecksum string
	failureCategory  backupartifact.SegmentCorruptionCategory
	validate         func([]byte) (erasureIntegrityDecodedArtifact, error)
}

type erasureIntegrityDecodedArtifact struct {
	commit backupartifact.ErasureLedgerCommit
	record backupartifact.ErasureLedgerRecord
	event  backupartifact.ErasureLedgerEvent
}

func (a *ReplicatedErasureIntegrityAuditor) artifactSpec(
	ctx context.Context,
	target ErasureIntegrityAuditTarget,
) (erasureIntegrityArtifactSpec, error) {
	if target.HashSlot >= a.hashSlotCount || target.Sequence == 0 {
		return erasureIntegrityArtifactSpec{}, backupartifact.ErrInvalidObject
	}
	validateCommit := func(
		body []byte,
	) (erasureIntegrityDecodedArtifact, error) {
		commit, err := backupartifact.LoadErasureLedgerCommit(
			ctx, body, a.signer,
		)
		if err != nil {
			return erasureIntegrityDecodedArtifact{}, err
		}
		expectedKey := backupartifact.ErasureLedgerCommitKey(
			a.streamNamespace, target.HashSlot, target.Sequence,
		)
		if target.CommitKey != expectedKey ||
			commit.RepositoryID != a.repositoryID ||
			commit.SourceClusterID != a.sourceClusterID ||
			commit.SourceGeneration != a.sourceGeneration ||
			commit.PrimaryRepository != a.primary.Name() ||
			commit.SecondaryRepository != a.secondary.Name() ||
			commit.HashSlot != target.HashSlot ||
			commit.Sequence != target.Sequence {
			return erasureIntegrityDecodedArtifact{},
				backupartifact.ErrObjectCorrupt
		}
		return erasureIntegrityDecodedArtifact{commit: commit}, nil
	}
	switch target.Kind {
	case ErasureIntegrityArtifactCommit:
		if target.CommitKey == "" ||
			len(target.ExpectedCommitSHA256) != sha256.Size*2 {
			return erasureIntegrityArtifactSpec{}, backupartifact.ErrInvalidObject
		}
		return erasureIntegrityArtifactSpec{
			key:              target.CommitKey,
			expectedChecksum: target.ExpectedCommitSHA256,
			failureCategory:  backupartifact.SegmentCorruptionCommitProof,
			validate:         validateCommit,
		}, nil
	case ErasureIntegrityArtifactReceipt:
		if target.EventID == "" ||
			len(target.ExpectedCommitSHA256) != sha256.Size*2 {
			return erasureIntegrityArtifactSpec{}, backupartifact.ErrInvalidObject
		}
		return erasureIntegrityArtifactSpec{
			key:              backupartifact.ErasureLedgerReceiptKey(target.EventID),
			expectedChecksum: target.ExpectedCommitSHA256,
			failureCategory:  backupartifact.SegmentCorruptionCommitProof,
			validate: func(
				body []byte,
			) (erasureIntegrityDecodedArtifact, error) {
				decoded, err := validateCommit(body)
				if err != nil {
					return erasureIntegrityDecodedArtifact{}, err
				}
				if decoded.commit.EventID != target.EventID {
					return erasureIntegrityDecodedArtifact{},
						backupartifact.ErrObjectCorrupt
				}
				return decoded, nil
			},
		}, nil
	case ErasureIntegrityArtifactRecord:
		if target.EventID == "" || target.RecordKey == "" ||
			len(target.RecordSHA256) != sha256.Size*2 {
			return erasureIntegrityArtifactSpec{}, backupartifact.ErrInvalidObject
		}
		return erasureIntegrityArtifactSpec{
			key: target.RecordKey, expectedChecksum: target.RecordSHA256,
			failureCategory: backupartifact.SegmentCorruptionCommitProof,
			validate: func(
				body []byte,
			) (erasureIntegrityDecodedArtifact, error) {
				record, err := backupartifact.LoadErasureLedgerRecord(
					ctx, body, a.signer,
				)
				if err != nil {
					return erasureIntegrityDecodedArtifact{}, err
				}
				if record.RepositoryID != a.repositoryID ||
					record.SourceClusterID != a.sourceClusterID ||
					record.SourceGeneration != a.sourceGeneration ||
					record.HashSlot != target.HashSlot ||
					record.EventID != target.EventID ||
					target.RecordKey != backupartifact.ErasureLedgerRecordKey(
						target.HashSlot, target.EventID,
					) {
					return erasureIntegrityDecodedArtifact{},
						backupartifact.ErrObjectCorrupt
				}
				return erasureIntegrityDecodedArtifact{
					record: record,
				}, nil
			},
		}, nil
	case ErasureIntegrityArtifactEvent:
		return erasureIntegrityArtifactSpec{}, backupartifact.ErrInvalidObject
	default:
		return erasureIntegrityArtifactSpec{}, backupartifact.ErrInvalidObject
	}
}

type inspectedErasureEventCopy struct {
	report     backupartifact.SegmentAuditCopy
	recordBody []byte
	eventBody  []byte
	record     backupartifact.ErasureLedgerRecord
	event      backupartifact.ErasureLedgerEvent
}

func (a *ReplicatedErasureIntegrityAuditor) inspectErasureEventCopies(
	ctx context.Context,
	target ErasureIntegrityAuditTarget,
) (ErasureIntegrityAuditReport, error) {
	report := ErasureIntegrityAuditReport{
		Copies: make([]backupartifact.SegmentAuditCopy, 0, 2),
	}
	for _, repository := range []backupartifact.Repository{
		a.primary, a.secondary,
	} {
		copyResult, err := a.inspectErasureEventCopy(
			ctx, repository, target,
		)
		if err != nil {
			return ErasureIntegrityAuditReport{}, err
		}
		report.Copies = append(report.Copies, copyResult.report)
		if copyResult.report.Healthy {
			report.Event = copyResult.event
		}
	}
	return report, nil
}

func (a *ReplicatedErasureIntegrityAuditor) inspectErasureEventCopy(
	ctx context.Context,
	repository backupartifact.Repository,
	target ErasureIntegrityAuditTarget,
) (inspectedErasureEventCopy, error) {
	recordTarget := target
	recordTarget.Kind = ErasureIntegrityArtifactRecord
	recordSpec, err := a.artifactSpec(ctx, recordTarget)
	if err != nil {
		return inspectedErasureEventCopy{}, err
	}
	recordResult, recordBody, decodedRecord, err :=
		inspectErasureArtifactCopy(ctx, repository, recordSpec)
	if err != nil {
		return inspectedErasureEventCopy{}, err
	}
	result := inspectedErasureEventCopy{
		report: recordResult, recordBody: recordBody,
		record: decodedRecord.record,
	}
	if !recordResult.Healthy {
		return result, nil
	}
	eventSpec, err := a.eventArtifactSpec(
		ctx, target, decodedRecord.record.Object,
	)
	if err != nil {
		result.report.Healthy = false
		result.report.Category =
			backupartifact.SegmentCorruptionCommitProof
		return result, nil
	}
	eventResult, eventBody, decodedEvent, err :=
		inspectErasureArtifactCopy(ctx, repository, eventSpec)
	if err != nil {
		return inspectedErasureEventCopy{}, err
	}
	eventResult.StoredBytes += recordResult.StoredBytes
	result.report = eventResult
	result.eventBody = eventBody
	result.event = decodedEvent.event
	return result, nil
}

func (a *ReplicatedErasureIntegrityAuditor) eventArtifactSpec(
	ctx context.Context,
	target ErasureIntegrityAuditTarget,
	object backupartifact.ObjectEntry,
) (erasureIntegrityArtifactSpec, error) {
	if target.HashSlot >= a.hashSlotCount || target.Sequence == 0 ||
		target.EventID == "" || target.RecordKey == "" ||
		len(target.RecordSHA256) != sha256.Size*2 ||
		object.Key == "" || object.HashSlot != target.HashSlot ||
		object.CiphertextBytes <= 0 ||
		object.CiphertextBytes > maxErasureLedgerRepositoryBytes {
		return erasureIntegrityArtifactSpec{}, backupartifact.ErrInvalidObject
	}
	return erasureIntegrityArtifactSpec{
		key: object.Key, expectedSize: object.CiphertextBytes,
		expectedChecksum: object.CiphertextSHA256,
		failureCategory:  backupartifact.SegmentCorruptionCiphertext,
		validate: func(
			body []byte,
		) (erasureIntegrityDecodedArtifact, error) {
			plaintext, err := a.codec.Open(ctx, object, body)
			if err != nil {
				return erasureIntegrityDecodedArtifact{}, err
			}
			event, err := backupartifact.LoadErasureLedgerEvent(plaintext)
			if err != nil ||
				event.RepositoryID != a.repositoryID ||
				event.SourceClusterID != a.sourceClusterID ||
				event.SourceGeneration != a.sourceGeneration ||
				event.HashSlot != target.HashSlot ||
				event.EventID != target.EventID ||
				routing.HashSlotForKey(
					event.ChannelID, a.hashSlotCount,
				) != target.HashSlot {
				return erasureIntegrityDecodedArtifact{},
					backupartifact.ErrObjectCorrupt
			}
			return erasureIntegrityDecodedArtifact{event: event}, nil
		},
	}, nil
}

func (a *ReplicatedErasureIntegrityAuditor) repairErasureEventCopy(
	ctx context.Context,
	target ErasureIntegrityAuditTarget,
	targetRepository string,
) (int64, error) {
	targetCopy, source, repair, err := a.repairRepositories(
		targetRepository,
	)
	if err != nil {
		return 0, err
	}
	sourceResult, err := a.inspectErasureEventCopy(ctx, source, target)
	if err != nil {
		return 0, err
	}
	if !sourceResult.report.Healthy {
		return 0, fmt.Errorf(
			"%w: erasure event repair source is unhealthy",
			backupartifact.ErrRepositoryIncomplete,
		)
	}
	targetResult, err := a.inspectErasureEventCopy(
		ctx, targetCopy, target,
	)
	if err != nil {
		return 0, err
	}
	if targetResult.report.Healthy {
		return 0, nil
	}
	var repairedBytes int64
	if len(targetResult.recordBody) == 0 ||
		targetResult.record.EventID == "" {
		if err := repair.RepairImmutable(
			ctx, target.RecordKey, int64(len(sourceResult.recordBody)),
			target.RecordSHA256, bytes.NewReader(sourceResult.recordBody),
		); err != nil {
			return repairedBytes, err
		}
		repairedBytes += int64(len(sourceResult.recordBody))
	}
	targetResult, err = a.inspectErasureEventCopy(ctx, targetCopy, target)
	if err != nil {
		return repairedBytes, err
	}
	if !targetResult.report.Healthy {
		object := sourceResult.record.Object
		if err := repair.RepairImmutable(
			ctx, object.Key, int64(len(sourceResult.eventBody)),
			object.CiphertextSHA256,
			bytes.NewReader(sourceResult.eventBody),
		); err != nil {
			return repairedBytes, err
		}
		repairedBytes += int64(len(sourceResult.eventBody))
	}
	revalidated, err := a.inspectErasureEventCopy(
		ctx, targetCopy, target,
	)
	if err != nil || !revalidated.report.Healthy ||
		!bytes.Equal(sourceResult.recordBody, revalidated.recordBody) ||
		!bytes.Equal(sourceResult.eventBody, revalidated.eventBody) {
		if err != nil {
			return repairedBytes, err
		}
		return repairedBytes, fmt.Errorf(
			"%w: repaired erasure event failed revalidation",
			backupartifact.ErrRepositoryIncomplete,
		)
	}
	return repairedBytes, nil
}

func inspectErasureArtifactCopy(
	ctx context.Context,
	repository backupartifact.Repository,
	spec erasureIntegrityArtifactSpec,
) (
	backupartifact.SegmentAuditCopy,
	[]byte,
	erasureIntegrityDecodedArtifact,
	error,
) {
	result := backupartifact.SegmentAuditCopy{Repository: repository.Name()}
	reader, object, err := repository.Open(ctx, spec.key)
	if errors.Is(err, backupartifact.ErrObjectNotFound) {
		result.Category = backupartifact.SegmentCorruptionMissing
		return result, nil, erasureIntegrityDecodedArtifact{}, nil
	}
	if errors.Is(err, backupartifact.ErrObjectCorrupt) {
		result.Category = backupartifact.SegmentCorruptionChecksum
		return result, nil, erasureIntegrityDecodedArtifact{}, nil
	}
	if err != nil {
		return result, nil, erasureIntegrityDecodedArtifact{}, err
	}
	body, readErr := io.ReadAll(io.LimitReader(
		reader, maxErasureLedgerRepositoryBytes+1,
	))
	closeErr := reader.Close()
	if readErr != nil {
		return result, nil, erasureIntegrityDecodedArtifact{}, readErr
	}
	if closeErr != nil {
		return result, nil, erasureIntegrityDecodedArtifact{}, closeErr
	}
	result.StoredBytes = int64(len(body))
	digest := sha256.Sum256(body)
	checksum := hex.EncodeToString(digest[:])
	if len(body) == 0 || len(body) > maxErasureLedgerRepositoryBytes ||
		object.Key != spec.key || object.Size != int64(len(body)) ||
		object.SHA256 != checksum {
		result.Category = backupartifact.SegmentCorruptionChecksum
		return result, nil, erasureIntegrityDecodedArtifact{}, nil
	}
	if (spec.expectedSize > 0 &&
		int64(len(body)) != spec.expectedSize) ||
		(spec.expectedChecksum != "" &&
			checksum != spec.expectedChecksum) {
		result.Category = spec.failureCategory
		return result, nil, erasureIntegrityDecodedArtifact{}, nil
	}
	decoded, err := spec.validate(body)
	if err != nil {
		result.Category = spec.failureCategory
		return result, nil, erasureIntegrityDecodedArtifact{}, nil
	}
	result.Healthy = true
	return result, body, decoded, nil
}

func (a *ReplicatedErasureIntegrityAuditor) repairRepositories(
	target string,
) (
	backupartifact.Repository,
	backupartifact.Repository,
	backupartifact.RepairRepository,
	error,
) {
	switch target {
	case a.primary.Name():
		return a.primary, a.secondary, a.primaryRepair, nil
	case a.secondary.Name():
		return a.secondary, a.primary, a.secondaryRepair, nil
	default:
		return nil, nil, nil, fmt.Errorf(
			"%w: unknown erasure repair repository",
			backupartifact.ErrInvalidObject,
		)
	}
}
