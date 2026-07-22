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

const maxErasureLedgerCommits = 1_000_000

// ErasureLedgerCommitLister lists only immutable signed commit-marker keys.
type ErasureLedgerCommitLister interface {
	ListErasureLedgerCommitKeys(context.Context) ([]string, error)
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
	// Boundary is the highest contiguous commit included in the snapshot.
	Boundary uint64
	// SHA256 authenticates the exact length-delimited artifact prefix.
	SHA256 string
	// Keys contains every immutable object reachable from the snapshot.
	Keys []string
	// events stores collapsed boundaries partitioned by live hash slot.
	events map[uint16][]PermanentErasureBoundary
}

// Boundaries returns detached sorted permanent-erasure boundaries for one hash slot.
func (s ErasureLedgerSnapshot) Boundaries(hashSlot uint16) []PermanentErasureBoundary {
	return append([]PermanentErasureBoundary(nil), s.events[hashSlot]...)
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
		repositoryID: strings.TrimSpace(options.RepositoryID), sourceClusterID: strings.TrimSpace(options.SourceClusterID), sourceGeneration: strings.TrimSpace(options.SourceGeneration), hashSlotCount: options.HashSlotCount}, nil
}

// LoadDualSnapshot requires identical complete commit-marker sets and artifact bytes in both repositories.
func (l *ErasureLedgerLoader) LoadDualSnapshot(ctx context.Context) (ErasureLedgerSnapshot, error) {
	primaryKeys, err := l.primary.(ErasureLedgerCommitLister).ListErasureLedgerCommitKeys(ctx)
	if err != nil {
		return ErasureLedgerSnapshot{}, err
	}
	secondaryKeys, err := l.secondary.(ErasureLedgerCommitLister).ListErasureLedgerCommitKeys(ctx)
	if err != nil {
		return ErasureLedgerSnapshot{}, err
	}
	if !reflect.DeepEqual(primaryKeys, secondaryKeys) {
		return ErasureLedgerSnapshot{}, fmt.Errorf("%w: erasure-ledger commit sets disagree", backupartifact.ErrRepositoryIncomplete)
	}
	return l.loadSnapshot(ctx, l.primary, l.secondary, primaryKeys, uint64(len(primaryKeys)))
}

// LoadPinnedSnapshot loads exactly the plan-pinned prefix from one selected repository.
func (l *ErasureLedgerLoader) LoadPinnedSnapshot(ctx context.Context, repositoryName string, version uint32, boundary uint64, checksum string) (ErasureLedgerSnapshot, error) {
	if version != backupartifact.ErasureLedgerSnapshotVersion || !validLowerSHA256(checksum) {
		return ErasureLedgerSnapshot{}, fmt.Errorf("%w: erasure-ledger snapshot fence is invalid", backupartifact.ErrInvalidManifest)
	}
	repository := l.primary
	if repositoryName == "secondary" {
		repository = l.secondary
	} else if repositoryName != "primary" {
		return ErasureLedgerSnapshot{}, fmt.Errorf("%w: erasure-ledger repository selector is invalid", backupartifact.ErrInvalidManifest)
	}
	keys, err := repository.(ErasureLedgerCommitLister).ListErasureLedgerCommitKeys(ctx)
	if err != nil {
		return ErasureLedgerSnapshot{}, err
	}
	if boundary > uint64(len(keys)) {
		return ErasureLedgerSnapshot{}, fmt.Errorf("%w: pinned erasure-ledger prefix is incomplete", backupartifact.ErrRepositoryIncomplete)
	}
	snapshot, err := l.loadSnapshot(ctx, repository, nil, keys[:int(boundary)], boundary)
	if err != nil {
		return ErasureLedgerSnapshot{}, err
	}
	if snapshot.SHA256 != checksum {
		return ErasureLedgerSnapshot{}, fmt.Errorf("%w: pinned erasure-ledger digest mismatch", backupartifact.ErrInvalidManifest)
	}
	return snapshot, nil
}

// LoadPendingReferenceKeys authenticates the Controller's one pending ledger
// reference and returns every immutable key that GC must preserve while commit
// publication is being resumed after a failure or leader change.
func (l *ErasureLedgerLoader) LoadPendingReferenceKeys(ctx context.Context, reference backupusecase.ErasureLedgerRecordReference) ([]string, error) {
	if reference.Sequence == 0 || !validLowerSHA256(reference.EventID) || !validLowerSHA256(reference.RecordSHA256) ||
		backupartifact.ValidateErasureLedgerRecordKey(reference.RecordKey, reference.EventID) != nil {
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
		backupartifact.ErasureLedgerCommitKey(reference.Sequence),
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

func (l *ErasureLedgerLoader) loadSnapshot(ctx context.Context, first, second backupartifact.Repository, keys []string, boundary uint64) (ErasureLedgerSnapshot, error) {
	if len(keys) > maxErasureLedgerCommits || boundary != uint64(len(keys)) {
		return ErasureLedgerSnapshot{}, fmt.Errorf("%w: erasure-ledger commit count is invalid", backupartifact.ErrInvalidManifest)
	}
	digest := sha256.New()
	marked := make([]string, 0, len(keys)*3)
	bySlot := make(map[uint16]map[string]PermanentErasureBoundary)
	for index, key := range keys {
		sequence := uint64(index + 1)
		if err := ctx.Err(); err != nil {
			return ErasureLedgerSnapshot{}, err
		}
		if key != backupartifact.ErasureLedgerCommitKey(sequence) {
			return ErasureLedgerSnapshot{}, fmt.Errorf("%w: erasure-ledger commits are not contiguous", backupartifact.ErrInvalidManifest)
		}
		commitBody, err := loadLedgerArtifactBytes(ctx, first, second, key, maxErasureLedgerRepositoryBytes)
		if err != nil {
			return ErasureLedgerSnapshot{}, err
		}
		commit, err := backupartifact.LoadErasureLedgerCommit(ctx, commitBody, l.signer)
		if err != nil || commit.Sequence != sequence || commit.RepositoryID != l.repositoryID || commit.SourceClusterID != l.sourceClusterID || commit.SourceGeneration != l.sourceGeneration ||
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
		if err != nil || record.EventID != commit.EventID || record.RepositoryID != l.repositoryID || record.SourceClusterID != l.sourceClusterID || record.SourceGeneration != l.sourceGeneration ||
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
		appendLedgerDigest(digest, commitBody, recordBody, ciphertext)
		marked = append(marked, key, receiptKey, commit.RecordKey, record.Object.Key)
		identity := fmt.Sprintf("%d:%s", event.ChannelType, event.ChannelID)
		if bySlot[event.HashSlot] == nil {
			bySlot[event.HashSlot] = make(map[string]PermanentErasureBoundary)
		}
		current := bySlot[event.HashSlot][identity]
		if event.ThroughSeq > current.ThroughSeq {
			bySlot[event.HashSlot][identity] = PermanentErasureBoundary{ChannelID: event.ChannelID, ChannelType: event.ChannelType, ThroughSeq: event.ThroughSeq}
		}
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
	return ErasureLedgerSnapshot{Version: backupartifact.ErasureLedgerSnapshotVersion, Boundary: boundary, SHA256: hex.EncodeToString(digest.Sum(nil)), Keys: marked, events: events}, nil
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

func validErasureLedgerCommitKey(key string) bool {
	if len(key) != len("erasure-ledger/commits/")+20+len(".json") || !strings.HasPrefix(key, "erasure-ledger/commits/") || !strings.HasSuffix(key, ".json") {
		return false
	}
	digits := strings.TrimSuffix(strings.TrimPrefix(key, "erasure-ledger/commits/"), ".json")
	for _, digit := range digits {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	return key != backupartifact.ErasureLedgerCommitKey(0)
}
