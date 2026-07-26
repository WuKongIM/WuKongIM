package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash"
	"reflect"
	"sort"
	"strings"

	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
)

// ErasureLedgerCommitLister lists only immutable signed commit-marker keys.
type ErasureLedgerCommitLister interface {
	ListErasureLedgerCommitKeys(context.Context, string) ([]string, error)
}

// PermanentErasureBoundary is the maximum unavailable message prefix for one Channel.
type PermanentErasureBoundary struct {
	// ChannelID identifies the erased durable Channel.
	ChannelID string
	// ChannelType identifies the erased durable Channel kind.
	ChannelType uint8
	// ThroughSeq is the inclusive highest sequence that must remain unavailable.
	ThroughSeq uint64
}

// ErasureLedgerSnapshot is one authenticated exact contiguous ledger prefix.
type ErasureLedgerSnapshot struct {
	// Version identifies the authenticated snapshot schema.
	Version uint32
	// EventCount is the total number of commits across all included streams.
	EventCount uint64
	// Heads authenticates the exact contiguous prefix selected for each Hash Slot.
	Heads []backupartifact.ErasureStreamHead
	// SHA256 authenticates the exact length-delimited artifact prefix.
	SHA256 string
	// Keys contains every immutable object reachable from the snapshot.
	Keys []string
	// events stores collapsed boundaries partitioned by live hash slot.
	events map[uint16][]PermanentErasureBoundary
	// commitSHAByKey authenticates intermediate heads required by older checkpoints.
	commitSHAByKey map[string]string
}

// Boundaries returns detached sorted permanent-erasure boundaries for one hash slot.
func (s ErasureLedgerSnapshot) Boundaries(hashSlot uint16) []PermanentErasureBoundary {
	return append([]PermanentErasureBoundary(nil), s.events[hashSlot]...)
}

// ContainsHeads reports whether every required head is the exact signed commit
// contained at that position in this snapshot, not merely an equal sequence.
func (s ErasureLedgerSnapshot) ContainsHeads(required []backupartifact.ErasureStreamHead) bool {
	for _, head := range required {
		if backupartifact.ValidateErasureStreamHead(head) != nil ||
			s.commitSHAByKey[head.CommitKey] != head.CommitSHA256 {
			return false
		}
	}
	return true
}

// ErasureLedgerLoaderOptions configures authenticated ledger replay.
type ErasureLedgerLoaderOptions struct {
	// Primary and Secondary are the physical repository clients used for reads.
	Primary, Secondary backupartifact.Repository
	// PrimaryRepository and SecondaryRepository are the logical repository
	// identities signed by upload credentials. Empty values use client names.
	PrimaryRepository, SecondaryRepository string
	// Signer verifies record and commit authenticity.
	Signer backupartifact.ManifestSigner
	// Codec decrypts authenticated event objects.
	Codec *backupartifact.ObjectCodec
	// RepositoryID identifies the logical dual-repository backup.
	RepositoryID string
	// SourceClusterID fences events to one source cluster.
	SourceClusterID string
	// SourceGeneration fences events to one source-cluster generation.
	SourceGeneration string
	// HashSlotCount validates live Channel routing.
	HashSlotCount uint16
}

// ErasureLedgerLoader verifies and decrypts one bounded contiguous ledger prefix.
type ErasureLedgerLoader struct {
	primary, secondary  backupartifact.Repository
	signer              backupartifact.ManifestSigner
	codec               *backupartifact.ObjectCodec
	primaryRepository   string
	secondaryRepository string
	repositoryID        string
	sourceClusterID     string
	sourceGeneration    string
	streamNamespace     string
	hashSlotCount       uint16
}

// NewErasureLedgerLoader creates a fail-closed ledger reader.
func NewErasureLedgerLoader(options ErasureLedgerLoaderOptions) (*ErasureLedgerLoader, error) {
	if options.Primary == nil || options.Secondary == nil || options.Signer == nil || options.Codec == nil || options.Primary.Name() == options.Secondary.Name() ||
		strings.TrimSpace(options.RepositoryID) == "" || strings.TrimSpace(options.SourceClusterID) == "" || strings.TrimSpace(options.SourceGeneration) == "" || options.HashSlotCount == 0 {
		return nil, fmt.Errorf("backup erasure ledger loader: invalid options")
	}
	if _, ok := options.Primary.(ErasureLedgerCommitLister); !ok {
		return nil, fmt.Errorf("backup erasure ledger loader: primary repository cannot list commits")
	}
	if _, ok := options.Secondary.(ErasureLedgerCommitLister); !ok {
		return nil, fmt.Errorf("backup erasure ledger loader: secondary repository cannot list commits")
	}
	if strings.TrimSpace(options.PrimaryRepository) == "" {
		options.PrimaryRepository = options.Primary.Name()
	}
	if strings.TrimSpace(options.SecondaryRepository) == "" {
		options.SecondaryRepository = options.Secondary.Name()
	}
	options.PrimaryRepository = strings.TrimSpace(options.PrimaryRepository)
	options.SecondaryRepository = strings.TrimSpace(options.SecondaryRepository)
	if options.PrimaryRepository == options.SecondaryRepository || len(options.PrimaryRepository) > 128 || len(options.SecondaryRepository) > 128 {
		return nil, fmt.Errorf("backup erasure ledger loader: logical repository identities must differ")
	}
	return &ErasureLedgerLoader{primary: options.Primary, secondary: options.Secondary, signer: options.Signer, codec: options.Codec,
		primaryRepository: options.PrimaryRepository, secondaryRepository: options.SecondaryRepository,
		repositoryID: strings.TrimSpace(options.RepositoryID), sourceClusterID: strings.TrimSpace(options.SourceClusterID), sourceGeneration: strings.TrimSpace(options.SourceGeneration),
		streamNamespace: backupartifact.ComputeErasureLedgerStreamNamespace(options.RepositoryID, options.SourceClusterID, options.SourceGeneration),
		hashSlotCount:   options.HashSlotCount}, nil
}

// LoadDualSnapshot requires identical complete commit-marker sets and artifact bytes in both repositories.
func (l *ErasureLedgerLoader) LoadDualSnapshot(ctx context.Context) (ErasureLedgerSnapshot, error) {
	primaryKeys, err := l.primary.(ErasureLedgerCommitLister).ListErasureLedgerCommitKeys(ctx, l.streamNamespace)
	if err != nil {
		return ErasureLedgerSnapshot{}, err
	}
	secondaryKeys, err := l.secondary.(ErasureLedgerCommitLister).ListErasureLedgerCommitKeys(ctx, l.streamNamespace)
	if err != nil {
		return ErasureLedgerSnapshot{}, err
	}
	if !reflect.DeepEqual(primaryKeys, secondaryKeys) {
		return ErasureLedgerSnapshot{}, fmt.Errorf("%w: erasure-ledger commit sets disagree", backupartifact.ErrRepositoryIncomplete)
	}
	return l.loadSnapshot(ctx, l.primary, l.secondary, primaryKeys, nil)
}

// LoadDualSnapshotProof authenticates the replicated commit graph and exact
// ciphertext digests without opening event plaintext. Restore admission uses
// this metadata-only proof so KMS and Channel boundary materialization happen
// exactly once later on each target Slot Leader.
func (l *ErasureLedgerLoader) LoadDualSnapshotProof(
	ctx context.Context,
	requiredHeads []backupartifact.ErasureStreamHead,
) (ErasureLedgerSnapshot, error) {
	primaryKeys, err := l.primary.(ErasureLedgerCommitLister).
		ListErasureLedgerCommitKeys(ctx, l.streamNamespace)
	if err != nil {
		return ErasureLedgerSnapshot{}, err
	}
	secondaryKeys, err := l.secondary.(ErasureLedgerCommitLister).
		ListErasureLedgerCommitKeys(ctx, l.streamNamespace)
	if err != nil {
		return ErasureLedgerSnapshot{}, err
	}
	if !reflect.DeepEqual(primaryKeys, secondaryKeys) {
		return ErasureLedgerSnapshot{}, fmt.Errorf(
			"%w: erasure-ledger commit sets disagree",
			backupartifact.ErrRepositoryIncomplete,
		)
	}
	return l.loadSnapshotProof(
		ctx, l.primary, l.secondary, primaryKeys, requiredHeads,
	)
}

// LoadPinnedSnapshot loads exactly the plan-pinned per-Slot prefixes from one selected repository.
func (l *ErasureLedgerLoader) LoadPinnedSnapshot(ctx context.Context, repositoryName string, version uint32, eventCount uint64, checksum string, heads []backupartifact.ErasureStreamHead) (ErasureLedgerSnapshot, error) {
	if version != backupartifact.ErasureLedgerSnapshotVersion || !validLowerSHA256(checksum) {
		return ErasureLedgerSnapshot{}, fmt.Errorf("%w: erasure-ledger snapshot fence is invalid", backupartifact.ErrInvalidManifest)
	}
	repository := l.primary
	if repositoryName == "secondary" {
		repository = l.secondary
	} else if repositoryName != "primary" {
		return ErasureLedgerSnapshot{}, fmt.Errorf("%w: erasure-ledger repository selector is invalid", backupartifact.ErrInvalidManifest)
	}
	keys, normalizedHeads, err := erasureCommitKeysForHeads(heads, l.streamNamespace, l.hashSlotCount)
	if err != nil || uint64(len(keys)) != eventCount {
		return ErasureLedgerSnapshot{}, fmt.Errorf("%w: pinned erasure-ledger heads are invalid", backupartifact.ErrInvalidManifest)
	}
	snapshot, err := l.loadSnapshot(ctx, repository, nil, keys, normalizedHeads)
	if err != nil {
		return ErasureLedgerSnapshot{}, err
	}
	if snapshot.SHA256 != checksum {
		return ErasureLedgerSnapshot{}, fmt.Errorf("%w: pinned erasure-ledger digest mismatch", backupartifact.ErrInvalidManifest)
	}
	return snapshot, nil
}

// ReplayPinnedSlot authenticates and decrypts one Hash Slot's plan-pinned
// erasure prefix one commit at a time. It retains neither the million-entry
// commit-key list nor a Channel map; the target session collapses boundaries in
// its disk-backed evidence index.
func (l *ErasureLedgerLoader) ReplayPinnedSlot(
	ctx context.Context,
	repositoryName string,
	version uint32,
	eventCount uint64,
	checksum string,
	heads []backupartifact.ErasureStreamHead,
	hashSlot uint16,
	visit func(PermanentErasureBoundary) error,
) error {
	if version != backupartifact.ErasureLedgerSnapshotVersion ||
		!validLowerSHA256(checksum) || hashSlot >= l.hashSlotCount ||
		visit == nil {
		return fmt.Errorf(
			"%w: erasure-ledger snapshot fence is invalid",
			backupartifact.ErrInvalidManifest,
		)
	}
	repository := l.primary
	if repositoryName == "secondary" {
		repository = l.secondary
	} else if repositoryName != "primary" {
		return fmt.Errorf(
			"%w: erasure-ledger repository selector is invalid",
			backupartifact.ErrInvalidManifest,
		)
	}
	normalized, total, err := validateErasureHeads(
		heads, l.streamNamespace, l.hashSlotCount,
	)
	if err != nil || total != eventCount {
		return fmt.Errorf(
			"%w: pinned erasure-ledger heads are invalid",
			backupartifact.ErrInvalidManifest,
		)
	}
	for _, head := range normalized {
		if head.HashSlot != hashSlot {
			continue
		}
		previousCommitSHA := ""
		for sequence := uint64(1); sequence <= head.Sequence; sequence++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			key := backupartifact.ErasureLedgerCommitKey(
				l.streamNamespace, hashSlot, sequence,
			)
			commitBody, err := loadLedgerArtifactBytes(
				ctx, repository, nil, key,
				maxErasureLedgerRepositoryBytes,
			)
			if err != nil {
				return err
			}
			commit, err := backupartifact.LoadErasureLedgerCommit(
				ctx, commitBody, l.signer,
			)
			commitSHA := sha256Hex(commitBody)
			if err != nil || commit.HashSlot != hashSlot ||
				commit.Sequence != sequence ||
				commit.PreviousCommitSHA256 != previousCommitSHA ||
				commit.RepositoryID != l.repositoryID ||
				commit.SourceClusterID != l.sourceClusterID ||
				commit.SourceGeneration != l.sourceGeneration ||
				commit.PrimaryRepository != l.primaryRepository ||
				commit.SecondaryRepository != l.secondaryRepository {
				return fmt.Errorf(
					"%w: erasure-ledger commit identity mismatch",
					backupartifact.ErrInvalidManifest,
				)
			}
			receiptBody, err := loadLedgerArtifactBytes(
				ctx, repository, nil,
				backupartifact.ErasureLedgerReceiptKey(commit.EventID),
				maxErasureLedgerRepositoryBytes,
			)
			if err != nil || !bytes.Equal(receiptBody, commitBody) {
				return fmt.Errorf(
					"%w: erasure-ledger committed-event receipt mismatch",
					backupartifact.ErrInvalidManifest,
				)
			}
			recordBody, err := loadLedgerArtifactBytes(
				ctx, repository, nil, commit.RecordKey,
				maxErasureLedgerRepositoryBytes,
			)
			if err != nil || sha256Hex(recordBody) != commit.RecordSHA256 {
				return fmt.Errorf(
					"%w: erasure-ledger record digest mismatch",
					backupartifact.ErrInvalidManifest,
				)
			}
			record, err := backupartifact.LoadErasureLedgerRecord(
				ctx, recordBody, l.signer,
			)
			if err != nil || record.HashSlot != hashSlot ||
				record.EventID != commit.EventID ||
				record.RepositoryID != l.repositoryID ||
				record.SourceClusterID != l.sourceClusterID ||
				record.SourceGeneration != l.sourceGeneration ||
				commit.RecordKey != backupartifact.ErasureLedgerRecordKey(
					record.HashSlot, record.EventID,
				) {
				return fmt.Errorf(
					"%w: erasure-ledger record identity mismatch",
					backupartifact.ErrInvalidManifest,
				)
			}
			ciphertext, err := loadLedgerArtifactBytes(
				ctx, repository, nil, record.Object.Key,
				maxErasureLedgerRepositoryBytes,
			)
			if err != nil {
				return err
			}
			plaintext, err := l.codec.Open(
				ctx, record.Object, ciphertext,
			)
			if err != nil {
				return err
			}
			event, err := backupartifact.LoadErasureLedgerEvent(plaintext)
			if err != nil || event.EventID != record.EventID ||
				event.RepositoryID != l.repositoryID ||
				event.SourceClusterID != l.sourceClusterID ||
				event.SourceGeneration != l.sourceGeneration ||
				event.HashSlot != hashSlot ||
				routing.HashSlotForKey(
					event.ChannelID, l.hashSlotCount,
				) != hashSlot {
				return fmt.Errorf(
					"%w: erasure-ledger event identity mismatch",
					backupartifact.ErrInvalidManifest,
				)
			}
			if err := visit(PermanentErasureBoundary{
				ChannelID:   event.ChannelID,
				ChannelType: event.ChannelType,
				ThroughSeq:  event.ThroughSeq,
			}); err != nil {
				return err
			}
			previousCommitSHA = commitSHA
		}
		if previousCommitSHA != head.CommitSHA256 {
			return fmt.Errorf(
				"%w: pinned erasure-ledger head mismatch",
				backupartifact.ErrInvalidManifest,
			)
		}
		return nil
	}
	return nil
}

// LoadPendingReferenceKeys authenticates the Controller's one pending ledger
// reference and returns every immutable key that GC must preserve while commit
// publication is being resumed after a failure or leader change.
func (l *ErasureLedgerLoader) LoadPendingReferenceKeys(ctx context.Context, reference backupusecase.ErasureLedgerRecordReference) ([]string, error) {
	if reference.Sequence == 0 || !validLowerSHA256(reference.EventID) || !validLowerSHA256(reference.RecordSHA256) ||
		backupartifact.ValidateErasureLedgerRecordKey(reference.RecordKey, reference.EventID) != nil ||
		!strings.HasPrefix(reference.RecordKey, fmt.Sprintf("erasure-ledger/events/%04x/", reference.HashSlot)) {
		return nil, fmt.Errorf("%w: pending erasure-ledger reference is invalid", backupartifact.ErrInvalidManifest)
	}
	recordBody, err := l.loadAvailableArtifact(ctx, reference.RecordKey)
	if err != nil || sha256Hex(recordBody) != reference.RecordSHA256 {
		return nil, fmt.Errorf("%w: pending erasure-ledger record digest mismatch", backupartifact.ErrRepositoryIncomplete)
	}
	record, err := backupartifact.LoadErasureLedgerRecord(ctx, recordBody, l.signer)
	if err != nil || record.EventID != reference.EventID || record.RepositoryID != l.repositoryID || record.SourceClusterID != l.sourceClusterID ||
		record.SourceGeneration != l.sourceGeneration || record.HashSlot >= l.hashSlotCount || reference.RecordKey != backupartifact.ErasureLedgerRecordKey(record.HashSlot, record.EventID) {
		return nil, fmt.Errorf("%w: pending erasure-ledger record identity mismatch", backupartifact.ErrInvalidManifest)
	}
	ciphertext, err := l.loadAvailableArtifact(ctx, record.Object.Key)
	if err != nil || int64(len(ciphertext)) != record.Object.CiphertextBytes || sha256Hex(ciphertext) != record.Object.CiphertextSHA256 {
		return nil, fmt.Errorf("%w: pending erasure-ledger event object mismatch", backupartifact.ErrRepositoryIncomplete)
	}
	plaintext, err := l.codec.Open(ctx, record.Object, ciphertext)
	if err != nil {
		return nil, err
	}
	event, err := backupartifact.LoadErasureLedgerEvent(plaintext)
	if err != nil || event.EventID != record.EventID || event.RepositoryID != l.repositoryID || event.SourceClusterID != l.sourceClusterID ||
		event.SourceGeneration != l.sourceGeneration || event.HashSlot != record.HashSlot || routing.HashSlotForKey(event.ChannelID, l.hashSlotCount) != event.HashSlot {
		return nil, fmt.Errorf("%w: pending erasure-ledger event identity mismatch", backupartifact.ErrInvalidManifest)
	}
	return []string{
		backupartifact.ErasureLedgerCommitKey(l.streamNamespace, reference.HashSlot, reference.Sequence),
		backupartifact.ErasureLedgerReceiptKey(reference.EventID),
		reference.RecordKey,
		record.Object.Key,
	}, nil
}

func (l *ErasureLedgerLoader) loadAvailableArtifact(ctx context.Context, key string) ([]byte, error) {
	primaryBody, primaryFound, err := readOptionalRepositoryObject(ctx, l.primary, key, maxErasureLedgerRepositoryBytes)
	if err != nil {
		return nil, err
	}
	secondaryBody, secondaryFound, err := readOptionalRepositoryObject(ctx, l.secondary, key, maxErasureLedgerRepositoryBytes)
	if err != nil {
		return nil, err
	}
	if !primaryFound && !secondaryFound {
		return nil, fmt.Errorf("%w: erasure-ledger object %q is missing", backupartifact.ErrRepositoryIncomplete, key)
	}
	if primaryFound && secondaryFound && !bytes.Equal(primaryBody, secondaryBody) {
		return nil, fmt.Errorf("%w: replicated erasure-ledger object %q disagrees", backupartifact.ErrRepositoryIncomplete, key)
	}
	if primaryFound {
		return primaryBody, nil
	}
	return secondaryBody, nil
}

func (l *ErasureLedgerLoader) loadSnapshot(ctx context.Context, first, second backupartifact.Repository, keys []string, expectedHeads []backupartifact.ErasureStreamHead) (ErasureLedgerSnapshot, error) {
	if len(keys) > backupartifact.MaxErasureLedgerEvents {
		return ErasureLedgerSnapshot{}, fmt.Errorf("%w: erasure-ledger commit count is invalid", backupartifact.ErrInvalidManifest)
	}
	digest := sha256.New()
	marked := make([]string, 0, len(keys)*3)
	bySlot := make(map[uint16]map[string]PermanentErasureBoundary)
	commitSHAByKey := make(map[string]string, len(keys))
	heads := make([]backupartifact.ErasureStreamHead, 0, l.hashSlotCount)
	var currentSlot uint16
	var currentSequence uint64
	var previousCommitSHA string
	haveStream := false
	for _, key := range keys {
		if err := ctx.Err(); err != nil {
			return ErasureLedgerSnapshot{}, err
		}
		namespace, hashSlot, sequence, parseErr := backupartifact.ParseErasureLedgerCommitKey(key)
		if parseErr != nil || namespace != l.streamNamespace || hashSlot >= l.hashSlotCount {
			return ErasureLedgerSnapshot{}, fmt.Errorf("%w: erasure-ledger commit key is invalid", backupartifact.ErrInvalidManifest)
		}
		if !haveStream || hashSlot != currentSlot {
			if haveStream {
				heads = append(heads, backupartifact.ErasureStreamHead{
					HashSlot: currentSlot, Sequence: currentSequence,
					CommitKey:    backupartifact.ErasureLedgerCommitKey(l.streamNamespace, currentSlot, currentSequence),
					CommitSHA256: previousCommitSHA,
				})
			}
			if haveStream && hashSlot <= currentSlot {
				return ErasureLedgerSnapshot{}, fmt.Errorf("%w: erasure-ledger streams are not sorted", backupartifact.ErrInvalidManifest)
			}
			currentSlot, currentSequence, previousCommitSHA, haveStream = hashSlot, 0, "", true
		}
		if sequence != currentSequence+1 {
			return ErasureLedgerSnapshot{}, fmt.Errorf("%w: erasure-ledger commits are not contiguous", backupartifact.ErrInvalidManifest)
		}
		commitBody, err := loadLedgerArtifactBytes(ctx, first, second, key, maxErasureLedgerRepositoryBytes)
		if err != nil {
			return ErasureLedgerSnapshot{}, err
		}
		commit, err := backupartifact.LoadErasureLedgerCommit(ctx, commitBody, l.signer)
		commitSHA := sha256Hex(commitBody)
		if err != nil || commit.HashSlot != hashSlot || commit.Sequence != sequence ||
			commit.PreviousCommitSHA256 != previousCommitSHA ||
			commit.RepositoryID != l.repositoryID || commit.SourceClusterID != l.sourceClusterID || commit.SourceGeneration != l.sourceGeneration ||
			commit.PrimaryRepository != l.primaryRepository || commit.SecondaryRepository != l.secondaryRepository {
			return ErasureLedgerSnapshot{}, fmt.Errorf("%w: erasure-ledger commit identity mismatch", backupartifact.ErrInvalidManifest)
		}
		receiptKey := backupartifact.ErasureLedgerReceiptKey(commit.EventID)
		receiptBody, err := loadLedgerArtifactBytes(ctx, first, second, receiptKey, maxErasureLedgerRepositoryBytes)
		if err != nil || !bytes.Equal(receiptBody, commitBody) {
			return ErasureLedgerSnapshot{}, fmt.Errorf("%w: erasure-ledger committed-event receipt mismatch", backupartifact.ErrInvalidManifest)
		}
		recordBody, err := loadLedgerArtifactBytes(ctx, first, second, commit.RecordKey, maxErasureLedgerRepositoryBytes)
		if err != nil || sha256Hex(recordBody) != commit.RecordSHA256 {
			return ErasureLedgerSnapshot{}, fmt.Errorf("%w: erasure-ledger record digest mismatch", backupartifact.ErrInvalidManifest)
		}
		record, err := backupartifact.LoadErasureLedgerRecord(ctx, recordBody, l.signer)
		if err != nil || record.HashSlot != hashSlot || record.EventID != commit.EventID || record.RepositoryID != l.repositoryID || record.SourceClusterID != l.sourceClusterID || record.SourceGeneration != l.sourceGeneration ||
			commit.RecordKey != backupartifact.ErasureLedgerRecordKey(record.HashSlot, record.EventID) || record.HashSlot >= l.hashSlotCount {
			return ErasureLedgerSnapshot{}, fmt.Errorf("%w: erasure-ledger record identity mismatch", backupartifact.ErrInvalidManifest)
		}
		ciphertext, err := loadLedgerArtifactBytes(ctx, first, second, record.Object.Key, maxErasureLedgerRepositoryBytes)
		if err != nil {
			return ErasureLedgerSnapshot{}, err
		}
		plaintext, err := l.codec.Open(ctx, record.Object, ciphertext)
		if err != nil {
			return ErasureLedgerSnapshot{}, err
		}
		event, err := backupartifact.LoadErasureLedgerEvent(plaintext)
		if err != nil || event.EventID != record.EventID || event.RepositoryID != l.repositoryID || event.SourceClusterID != l.sourceClusterID || event.SourceGeneration != l.sourceGeneration ||
			event.HashSlot != record.HashSlot || routing.HashSlotForKey(event.ChannelID, l.hashSlotCount) != event.HashSlot {
			return ErasureLedgerSnapshot{}, fmt.Errorf("%w: erasure-ledger event identity mismatch", backupartifact.ErrInvalidManifest)
		}
		appendLedgerDigest(
			digest, commitBody, recordBody,
			erasureObjectDigestEnvelope(record.Object),
		)
		marked = append(marked, key, receiptKey, commit.RecordKey, record.Object.Key)
		identity := fmt.Sprintf("%d:%s", event.ChannelType, event.ChannelID)
		if bySlot[event.HashSlot] == nil {
			bySlot[event.HashSlot] = make(map[string]PermanentErasureBoundary)
		}
		current := bySlot[event.HashSlot][identity]
		if event.ThroughSeq > current.ThroughSeq {
			bySlot[event.HashSlot][identity] = PermanentErasureBoundary{ChannelID: event.ChannelID, ChannelType: event.ChannelType, ThroughSeq: event.ThroughSeq}
		}
		currentSequence = sequence
		previousCommitSHA = commitSHA
		commitSHAByKey[key] = commitSHA
	}
	if haveStream {
		heads = append(heads, backupartifact.ErasureStreamHead{
			HashSlot: currentSlot, Sequence: currentSequence,
			CommitKey:    backupartifact.ErasureLedgerCommitKey(l.streamNamespace, currentSlot, currentSequence),
			CommitSHA256: previousCommitSHA,
		})
	}
	if expectedHeads != nil && !reflect.DeepEqual(heads, expectedHeads) {
		return ErasureLedgerSnapshot{}, fmt.Errorf("%w: erasure-ledger stream heads mismatch", backupartifact.ErrInvalidManifest)
	}
	events := make(map[uint16][]PermanentErasureBoundary, len(bySlot))
	for hashSlot, boundaries := range bySlot {
		items := make([]PermanentErasureBoundary, 0, len(boundaries))
		for _, boundary := range boundaries {
			items = append(items, boundary)
		}
		sort.Slice(items, func(i, j int) bool {
			if items[i].ChannelID == items[j].ChannelID {
				return items[i].ChannelType < items[j].ChannelType
			}
			return items[i].ChannelID < items[j].ChannelID
		})
		events[hashSlot] = items
	}
	return ErasureLedgerSnapshot{
		Version: backupartifact.ErasureLedgerSnapshotVersion, EventCount: uint64(len(keys)),
		Heads: heads, SHA256: hex.EncodeToString(digest.Sum(nil)), Keys: marked,
		events: events, commitSHAByKey: commitSHAByKey,
	}, nil
}

func (l *ErasureLedgerLoader) loadSnapshotProof(
	ctx context.Context,
	first, second backupartifact.Repository,
	keys []string,
	requiredHeads []backupartifact.ErasureStreamHead,
) (ErasureLedgerSnapshot, error) {
	if len(keys) > backupartifact.MaxErasureLedgerEvents {
		return ErasureLedgerSnapshot{}, fmt.Errorf(
			"%w: erasure-ledger commit count is invalid",
			backupartifact.ErrInvalidManifest,
		)
	}
	required := make(map[string]string, len(requiredHeads))
	for _, head := range requiredHeads {
		namespace, _, _, err := backupartifact.ParseErasureLedgerCommitKey(
			head.CommitKey,
		)
		if err != nil || namespace != l.streamNamespace ||
			head.HashSlot >= l.hashSlotCount ||
			backupartifact.ValidateErasureStreamHead(head) != nil {
			return ErasureLedgerSnapshot{}, fmt.Errorf(
				"%w: required erasure head is invalid",
				backupartifact.ErrInvalidManifest,
			)
		}
		required[head.CommitKey] = head.CommitSHA256
	}
	digest := sha256.New()
	heads := make([]backupartifact.ErasureStreamHead, 0, l.hashSlotCount)
	matched := make(map[string]string, len(required))
	var currentSlot uint16
	var currentSequence uint64
	var previousCommitSHA string
	haveStream := false
	for _, key := range keys {
		if err := ctx.Err(); err != nil {
			return ErasureLedgerSnapshot{}, err
		}
		namespace, hashSlot, sequence, parseErr :=
			backupartifact.ParseErasureLedgerCommitKey(key)
		if parseErr != nil || namespace != l.streamNamespace ||
			hashSlot >= l.hashSlotCount {
			return ErasureLedgerSnapshot{}, fmt.Errorf(
				"%w: erasure-ledger commit key is invalid",
				backupartifact.ErrInvalidManifest,
			)
		}
		if !haveStream || hashSlot != currentSlot {
			if haveStream {
				heads = append(heads, backupartifact.ErasureStreamHead{
					HashSlot: currentSlot, Sequence: currentSequence,
					CommitKey: backupartifact.ErasureLedgerCommitKey(
						l.streamNamespace, currentSlot, currentSequence,
					),
					CommitSHA256: previousCommitSHA,
				})
			}
			if haveStream && hashSlot <= currentSlot {
				return ErasureLedgerSnapshot{}, fmt.Errorf(
					"%w: erasure-ledger streams are not sorted",
					backupartifact.ErrInvalidManifest,
				)
			}
			currentSlot, currentSequence, previousCommitSHA, haveStream =
				hashSlot, 0, "", true
		}
		if sequence != currentSequence+1 {
			return ErasureLedgerSnapshot{}, fmt.Errorf(
				"%w: erasure-ledger commits are not contiguous",
				backupartifact.ErrInvalidManifest,
			)
		}
		commitBody, err := loadLedgerArtifactBytes(
			ctx, first, second, key, maxErasureLedgerRepositoryBytes,
		)
		if err != nil {
			return ErasureLedgerSnapshot{}, err
		}
		commit, err := backupartifact.LoadErasureLedgerCommit(
			ctx, commitBody, l.signer,
		)
		commitSHA := sha256Hex(commitBody)
		if err != nil || commit.HashSlot != hashSlot ||
			commit.Sequence != sequence ||
			commit.PreviousCommitSHA256 != previousCommitSHA ||
			commit.RepositoryID != l.repositoryID ||
			commit.SourceClusterID != l.sourceClusterID ||
			commit.SourceGeneration != l.sourceGeneration ||
			commit.PrimaryRepository != l.primaryRepository ||
			commit.SecondaryRepository != l.secondaryRepository {
			return ErasureLedgerSnapshot{}, fmt.Errorf(
				"%w: erasure-ledger commit identity mismatch",
				backupartifact.ErrInvalidManifest,
			)
		}
		receiptKey := backupartifact.ErasureLedgerReceiptKey(commit.EventID)
		receiptBody, err := loadLedgerArtifactBytes(
			ctx, first, second, receiptKey,
			maxErasureLedgerRepositoryBytes,
		)
		if err != nil || !bytes.Equal(receiptBody, commitBody) {
			return ErasureLedgerSnapshot{}, fmt.Errorf(
				"%w: erasure-ledger committed-event receipt mismatch",
				backupartifact.ErrInvalidManifest,
			)
		}
		recordBody, err := loadLedgerArtifactBytes(
			ctx, first, second, commit.RecordKey,
			maxErasureLedgerRepositoryBytes,
		)
		if err != nil || sha256Hex(recordBody) != commit.RecordSHA256 {
			return ErasureLedgerSnapshot{}, fmt.Errorf(
				"%w: erasure-ledger record digest mismatch",
				backupartifact.ErrInvalidManifest,
			)
		}
		record, err := backupartifact.LoadErasureLedgerRecord(
			ctx, recordBody, l.signer,
		)
		if err != nil || record.HashSlot != hashSlot ||
			record.EventID != commit.EventID ||
			record.RepositoryID != l.repositoryID ||
			record.SourceClusterID != l.sourceClusterID ||
			record.SourceGeneration != l.sourceGeneration ||
			commit.RecordKey != backupartifact.ErasureLedgerRecordKey(
				record.HashSlot, record.EventID,
			) ||
			record.HashSlot >= l.hashSlotCount {
			return ErasureLedgerSnapshot{}, fmt.Errorf(
				"%w: erasure-ledger record identity mismatch",
				backupartifact.ErrInvalidManifest,
			)
		}
		if err := verifyLedgerArtifactMetadata(
			ctx, first, second, record.Object.Key,
			record.Object.CiphertextBytes,
			record.Object.CiphertextSHA256,
		); err != nil {
			return ErasureLedgerSnapshot{}, fmt.Errorf(
				"%w: erasure-ledger ciphertext metadata mismatch: %v",
				backupartifact.ErrInvalidManifest, err,
			)
		}
		appendLedgerDigest(
			digest, commitBody, recordBody,
			erasureObjectDigestEnvelope(record.Object),
		)
		if expected, ok := required[key]; ok {
			if expected != commitSHA {
				return ErasureLedgerSnapshot{}, fmt.Errorf(
					"%w: required erasure head digest mismatch",
					backupartifact.ErrInvalidManifest,
				)
			}
			matched[key] = commitSHA
		}
		currentSequence = sequence
		previousCommitSHA = commitSHA
	}
	if haveStream {
		heads = append(heads, backupartifact.ErasureStreamHead{
			HashSlot: currentSlot, Sequence: currentSequence,
			CommitKey: backupartifact.ErasureLedgerCommitKey(
				l.streamNamespace, currentSlot, currentSequence,
			),
			CommitSHA256: previousCommitSHA,
		})
	}
	if len(matched) != len(required) {
		return ErasureLedgerSnapshot{}, fmt.Errorf(
			"%w: required erasure head is outside current prefix",
			backupartifact.ErrRepositoryIncomplete,
		)
	}
	return ErasureLedgerSnapshot{
		Version:    backupartifact.ErasureLedgerSnapshotVersion,
		EventCount: uint64(len(keys)), Heads: heads,
		SHA256:         hex.EncodeToString(digest.Sum(nil)),
		commitSHAByKey: matched,
	}, nil
}

func loadLedgerArtifactBytes(ctx context.Context, first, second backupartifact.Repository, key string, limit int64) ([]byte, error) {
	firstBody, found, err := readOptionalRepositoryObject(ctx, first, key, limit)
	if err != nil || !found {
		return nil, fmt.Errorf("%w: erasure-ledger object %q missing: %v", backupartifact.ErrRepositoryIncomplete, key, err)
	}
	if second == nil {
		return firstBody, nil
	}
	secondBody, found, err := readOptionalRepositoryObject(ctx, second, key, limit)
	if err != nil || !found || !bytes.Equal(firstBody, secondBody) {
		return nil, fmt.Errorf("%w: replicated erasure-ledger object %q disagrees", backupartifact.ErrRepositoryIncomplete, key)
	}
	return firstBody, nil
}

func appendLedgerDigest(digest hash.Hash, bodies ...[]byte) {
	var size [8]byte
	for _, body := range bodies {
		binary.BigEndian.PutUint64(size[:], uint64(len(body)))
		_, _ = digest.Write(size[:])
		_, _ = digest.Write(body)
	}
}

func erasureObjectDigestEnvelope(entry backupartifact.ObjectEntry) []byte {
	return []byte(fmt.Sprintf(
		"%s\n%d\n%s",
		entry.Key, entry.CiphertextBytes, entry.CiphertextSHA256,
	))
}

func verifyLedgerArtifactMetadata(
	ctx context.Context,
	first, second backupartifact.Repository,
	key string,
	size int64,
	checksum string,
) error {
	for _, repository := range []backupartifact.Repository{first, second} {
		if repository == nil {
			continue
		}
		object, err := repository.Stat(ctx, key)
		if err != nil {
			return err
		}
		if object.Key != key || object.Size != size ||
			object.SHA256 != checksum {
			return backupartifact.ErrObjectCorrupt
		}
	}
	return nil
}

func validErasureLedgerCommitKey(key, namespace string) bool {
	parsedNamespace, _, _, err := backupartifact.ParseErasureLedgerCommitKey(key)
	return err == nil && parsedNamespace == namespace
}

func erasureCommitKeysForHeads(heads []backupartifact.ErasureStreamHead, namespace string, hashSlotCount uint16) ([]string, []backupartifact.ErasureStreamHead, error) {
	normalized, total, err := validateErasureHeads(
		heads, namespace, hashSlotCount,
	)
	if err != nil {
		return nil, nil, err
	}
	keys := make([]string, 0, int(total))
	for _, head := range normalized {
		for sequence := uint64(1); sequence <= head.Sequence; sequence++ {
			keys = append(keys, backupartifact.ErasureLedgerCommitKey(namespace, head.HashSlot, sequence))
		}
	}
	return keys, normalized, nil
}

func validateErasureHeads(
	heads []backupartifact.ErasureStreamHead,
	namespace string,
	hashSlotCount uint16,
) ([]backupartifact.ErasureStreamHead, uint64, error) {
	normalized := append([]backupartifact.ErasureStreamHead(nil), heads...)
	var total uint64
	for index, head := range normalized {
		headNamespace, _, _, err := backupartifact.ParseErasureLedgerCommitKey(
			head.CommitKey,
		)
		if err != nil || headNamespace != namespace ||
			head.HashSlot >= hashSlotCount ||
			backupartifact.ValidateErasureStreamHead(head) != nil ||
			(index > 0 && normalized[index-1].HashSlot >= head.HashSlot) ||
			head.Sequence > uint64(backupartifact.MaxErasureLedgerEvents)-total {
			return nil, 0, backupartifact.ErrInvalidManifest
		}
		total += head.Sequence
	}
	return normalized, total, nil
}
