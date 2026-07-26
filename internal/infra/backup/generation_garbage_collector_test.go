package backup_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupinfra "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	"github.com/stretchr/testify/require"
)

const (
	gcGenerationRetained = "rebase-00000-00000000000000000001"
	gcGenerationHeld     = "rebase-00000-00000000000000000002"
	gcGenerationActive   = "rebase-00000-00000000000000000003"
	gcGenerationExpired  = "rebase-00000-00000000000000000004"
	gcGenerationCurrent  = "rebase-00000-00000000000000000005"
	gcGenerationPending  = "rebase-00000-00000000000000000006"
	gcGenerationFrozen   = "rebase-00001-00000000000000000001"
)

func TestGenerationGarbageCollectorProtectsCheckpointHoldRestoreCurrentAndFrozenSlot(t *testing.T) {
	ctx := context.Background()
	primaryFile, err := backupinfra.NewFileRepository("primary", t.TempDir())
	require.NoError(t, err)
	secondaryFile, err := backupinfra.NewFileRepository("secondary", t.TempDir())
	require.NoError(t, err)
	primary := &generationGCRepository{FileRepository: primaryFile}
	secondary := &generationGCRepository{FileRepository: secondaryFile}
	signer := newCatalogTestSigner()
	catalog, err := backupinfra.NewReplicatedCheckpointCatalog(primary, secondary, signer, "signing-key")
	require.NoError(t, err)

	retained := generationGCPublishCheckpoint(t, catalog, nil, "retained-checkpoint", gcGenerationRetained)
	held := generationGCPublishCheckpoint(t, catalog, &retained.Head, "held-checkpoint", gcGenerationHeld)
	active := generationGCPublishCheckpoint(t, catalog, &held.Head, "active-checkpoint", gcGenerationActive)

	protectedKeys := []string{
		"objects/" + gcGenerationRetained + "/attempt-1/00000/meta.bin",
		"objects/" + gcGenerationHeld + "/attempt-1/00000/meta.bin",
		"objects/" + gcGenerationActive + "/attempt-1/00000/meta.bin",
		"objects/" + gcGenerationCurrent + "/attempt-1/00000/meta.bin",
		"objects/" + gcGenerationPending + "/attempt-1/00000/meta.bin",
		"objects/" + gcGenerationFrozen + "/attempt-1/00001/meta.bin",
		"objects/erasure-ledger/event-1/attempt-1.wkb",
		"objects/legacy-restore-job/attempt-1/00000/meta.bin",
		"partition-manifests/legacy-restore-job/00000.json",
	}
	expiredKeys := []string{
		"objects/" + gcGenerationExpired + "/attempt-1/00000/meta.bin",
		"partition-manifests/" + gcGenerationExpired + "/00000.json",
	}
	for _, repository := range []backupartifact.Repository{primary, secondary} {
		for _, key := range append(append([]string(nil), protectedKeys...), expiredKeys...) {
			generationGCPutObject(t, repository, key, []byte("generation:"+key))
		}
		orphanID := strings.Repeat("e", 64)
		orphanChecksum := strings.Repeat("f", 64)
		generationGCPutObject(
			t,
			repository,
			"segments/"+orphanID+"/payloads/"+orphanChecksum+".bin",
			[]byte("orphan-payload"),
		)
	}
	segmentStore, err := backupartifact.NewReplicatedSegmentStore(
		primary, secondary, backupartifact.NewSegmentCodec(testWrappingKeyManager{mask: 0x5a}, nil),
		signer, "signing-key",
	)
	require.NoError(t, err)
	expiredSegment, err := segmentStore.Commit(ctx, backupartifact.SegmentDescriptor{
		Logical: backupartifact.SegmentLogicalDescriptor{
			RepositoryID: "repository-prod", SourceClusterID: "cluster-source",
			SourceGeneration: "source-generation-1", Generation: gcGenerationExpired,
			HashSlot: 0, Stream: backupartifact.SegmentStreamMetadata,
			Sequence: 1, RecordCount: 1,
		},
		KMSKeyID: "kms-backup",
	}, []byte("expired-segment"))
	require.NoError(t, err)
	expiredCommitBody := readRepositoryBody(t, primary, expiredSegment.CommitKey)
	expiredCommit, err := backupartifact.LoadSegmentCommit(ctx, expiredCommitBody, signer)
	require.NoError(t, err)
	orphanRetryPayload := "segments/" + expiredSegment.SegmentID + "/payloads/" + strings.Repeat("1", 64) + ".bin"
	for _, repository := range []backupartifact.Repository{primary, secondary} {
		generationGCPutObject(t, repository, orphanRetryPayload, []byte("orphan-retry-ciphertext"))
	}

	cursorStore, err := backupinfra.NewControllerGenerationGCCursorStore(&erasureLedgerStateStore{})
	require.NoError(t, err)
	cache := newGenerationGCCache(t, signer)
	collector := generationGCCollector(t, primary, secondary, catalog, signer, cursorStore, cache, 256, 1<<30)
	protection := backupinfra.GenerationGCProtection{
		RetainedCatalogRootSequence: 1,
		CatalogRetentionRevision:    1,
		Retained:                    []backupartifact.CatalogCheckpointReference{retained.Checkpoint},
		Held:                        []backupartifact.CatalogCheckpointReference{held.Checkpoint},
		ActiveRestore:               &active.Checkpoint,
		Current: []backupcontract.SlotFrontier{
			{HashSlot: 0, Generation: gcGenerationCurrent, Rebase: &backupcontract.SlotRebase{
				TargetGeneration: gcGenerationPending,
			}},
			{HashSlot: 1, Generation: "generation-current-1"},
		},
		IntegrityAudit: backupcontract.IntegrityAuditState{
			Revision: 1, UpdatedAtUnixMillis: 1_753_400_100_000,
			Slots: []backupcontract.SlotIntegrityAuditState{{
				HashSlot: 1, Generation: gcGenerationFrozen,
				Health:              backupcontract.SlotAuditDegraded,
				Repository:          "secondary",
				Category:            backupcontract.IntegrityCorruptionCiphertext,
				UpdatedAtUnixMillis: 1_753_400_100_000,
			}},
		},
	}
	generationGCCompleteCycle(t, collector, "cycle-protection", protection)

	for _, repository := range []backupartifact.Repository{primary, secondary} {
		for _, key := range protectedKeys {
			_, err := repository.Stat(ctx, key)
			require.NoError(t, err, key)
		}
		for _, key := range append(expiredKeys, expiredSegment.CommitKey, expiredCommit.Payload.Key, orphanRetryPayload) {
			_, err := repository.Stat(ctx, key)
			require.ErrorIs(t, err, backupartifact.ErrObjectNotFound, key)
		}
		orphanKey := "segments/" + strings.Repeat("e", 64) + "/payloads/" + strings.Repeat("f", 64) + ".bin"
		_, err := repository.Stat(ctx, orphanKey)
		require.ErrorIs(t, err, backupartifact.ErrObjectNotFound, orphanKey)
	}

	generationGCCompleteCycle(
		t, collector, "cycle-prune-vector-cache", generationGCCurrentProtection(),
	)
	for _, repository := range []string{primary.Name(), secondary.Name()} {
		_, found, err := cache.LoadGenerationVector(
			ctx, repository, retained.Checkpoint.GenerationVector,
		)
		require.NoError(t, err)
		require.False(t, found, "completed protection decision must prune expired local vectors")
	}
}

func TestGenerationGarbageCollectorUnionsFixedIntegrityAuditSelection(
	t *testing.T,
) {
	ctx := context.Background()
	primaryFile, err := backupinfra.NewFileRepository(
		"primary", t.TempDir(),
	)
	require.NoError(t, err)
	secondaryFile, err := backupinfra.NewFileRepository(
		"secondary", t.TempDir(),
	)
	require.NoError(t, err)
	primary := &generationGCRepository{FileRepository: primaryFile}
	secondary := &generationGCRepository{FileRepository: secondaryFile}
	signer := newCatalogTestSigner()
	catalog, err := backupinfra.NewReplicatedCheckpointCatalog(
		primary, secondary, signer, "signing-key",
	)
	require.NoError(t, err)
	audited := generationGCPublishCheckpoint(
		t, catalog, nil, "audited-checkpoint", gcGenerationExpired,
	)
	key := "objects/" + gcGenerationExpired +
		"/attempt-1/00000/meta.bin"
	for _, repository := range []backupartifact.Repository{
		primary, secondary,
	} {
		generationGCPutObject(
			t, repository, key, []byte("audit-protected"),
		)
	}
	cursorStore, err := backupinfra.NewControllerGenerationGCCursorStore(
		&erasureLedgerStateStore{},
	)
	require.NoError(t, err)
	collector, err := backupinfra.NewGenerationGarbageCollector(
		backupinfra.GenerationGarbageCollectorOptions{
			Primary: primary, Secondary: secondary, Catalog: catalog,
			Signer: signer, Cursors: cursorStore,
			VectorCache:    newGenerationGCCache(t, signer),
			IntegrityGuard: allowGenerationGCIntegrityGuard{},
			AuditProtection: staticGenerationGCAuditProtection{
				references: []backupartifact.CatalogCheckpointReference{
					audited.Checkpoint,
				},
			},
			AuditRoots:    allowCatalogAuditRootStore{},
			HashSlotCount: 2, SafetyWindow: 7 * 24 * time.Hour,
			MaxRequestsPerRepository: 16,
			MaxBytesPerRepository:    1 << 20,
			Now: func() time.Time {
				return time.Now().UTC().Add(8 * 24 * time.Hour)
			},
		},
	)
	require.NoError(t, err)
	protection := generationGCCurrentProtection()
	protection.IntegrityAudit = backupcontract.IntegrityAuditState{
		Cursor: &backupcontract.IntegrityAuditCursor{
			CycleID:  "catalog-segments-fixed-selection",
			Position: "fixed-selection",
			Phase:    backupcontract.IntegrityAuditPhaseInspect,
		},
	}
	generationGCCompleteCycle(
		t, collector, "cycle-audit-selection", protection,
	)
	for _, repository := range []backupartifact.Repository{
		primary, secondary,
	} {
		_, err := repository.Stat(ctx, key)
		require.NoError(t, err)
	}
}

func TestGenerationGarbageCollectorRetriesOnlyLockedRepository(t *testing.T) {
	ctx := context.Background()
	lockedKey := "objects/" + gcGenerationExpired + "/attempt-1/00000/meta.bin"
	primaryFile, err := backupinfra.NewFileRepository("primary", t.TempDir())
	require.NoError(t, err)
	secondaryFile, err := backupinfra.NewFileRepository("secondary", t.TempDir())
	require.NoError(t, err)
	primary := &generationGCRepository{FileRepository: primaryFile, lockedKey: lockedKey}
	secondary := &generationGCRepository{FileRepository: secondaryFile}
	for _, repository := range []backupartifact.Repository{primary, secondary} {
		generationGCPutObject(t, repository, lockedKey, []byte("locked-generation"))
	}
	signer := newCatalogTestSigner()
	catalog, err := backupinfra.NewReplicatedCheckpointCatalog(primary, secondary, signer, "signing-key")
	require.NoError(t, err)
	cursorStore, err := backupinfra.NewControllerGenerationGCCursorStore(&erasureLedgerStateStore{})
	require.NoError(t, err)
	cache := newGenerationGCCache(t, signer)
	collector := generationGCCollector(t, primary, secondary, catalog, signer, cursorStore, cache, 256, 1<<30)
	protection := generationGCCurrentProtection()

	first, err := collector.Collect(ctx, "cycle-lock", protection)
	require.NoError(t, err)
	require.Equal(t, 1, first.Repositories[0].LockedObjects)
	require.False(t, first.Repositories[0].Complete)
	require.True(t, first.Repositories[1].Complete)
	secondaryWalks := secondary.walks

	second, err := collector.Collect(ctx, "cycle-lock", protection)
	require.NoError(t, err)
	require.Equal(t, 1, second.Repositories[0].LockedObjects)
	require.Equal(t, secondaryWalks, secondary.walks, "completed peer must not rescan")

	primary.setLockedKey("")
	third, err := collector.Collect(ctx, "cycle-lock", protection)
	require.NoError(t, err)
	require.True(t, third.Repositories[0].Complete)
	require.Equal(t, 1, third.Repositories[0].DeletedObjects)
	_, err = primary.Stat(ctx, lockedKey)
	require.ErrorIs(t, err, backupartifact.ErrObjectNotFound)
}

func TestGenerationGarbageCollectorDoesNotRetryHealthyRepositoryWhenPeerListingFails(t *testing.T) {
	ctx := context.Background()
	key := "objects/" + gcGenerationExpired + "/attempt-1/00000/meta.bin"
	primaryFile, err := backupinfra.NewFileRepository("primary", t.TempDir())
	require.NoError(t, err)
	secondaryFile, err := backupinfra.NewFileRepository("secondary", t.TempDir())
	require.NoError(t, err)
	primary := &generationGCRepository{FileRepository: primaryFile}
	secondary := &generationGCRepository{FileRepository: secondaryFile}
	for _, repository := range []backupartifact.Repository{primary, secondary} {
		generationGCPutObject(t, repository, key, []byte("expired-generation"))
	}
	signer := newCatalogTestSigner()
	catalog, err := backupinfra.NewReplicatedCheckpointCatalog(primary, secondary, signer, "signing-key")
	require.NoError(t, err)
	retained := generationGCPublishCheckpoint(
		t, catalog, nil, "retained-checkpoint", gcGenerationRetained,
	)
	protection := generationGCCurrentProtection()
	protection.Retained = []backupartifact.CatalogCheckpointReference{retained.Checkpoint}
	cursorStore, err := backupinfra.NewControllerGenerationGCCursorStore(&erasureLedgerStateStore{})
	require.NoError(t, err)
	cache := newGenerationGCCache(t, signer)
	collector := generationGCCollector(t, primary, secondary, catalog, signer, cursorStore, cache, 256, 1<<20)
	secondary.setListError(io.ErrUnexpectedEOF)

	first, err := collector.Collect(ctx, "cycle-peer-outage", protection)
	require.Error(t, err)
	require.True(t, first.Repositories[0].Complete)
	require.False(t, first.Repositories[1].Complete)
	primaryWalks := primary.walkCount()
	_, err = primary.Stat(ctx, key)
	require.ErrorIs(t, err, backupartifact.ErrObjectNotFound)
	_, err = secondary.Stat(ctx, key)
	require.NoError(t, err)

	secondary.setListError(nil)
	second, err := collector.Collect(ctx, "cycle-peer-outage", protection)
	require.NoError(t, err)
	require.True(t, second.Repositories[0].Complete)
	require.True(t, second.Repositories[1].Complete)
	require.Equal(t, primaryWalks, primary.walkCount(), "completed healthy copy must perform no repository I/O")
	_, err = secondary.Stat(ctx, key)
	require.ErrorIs(t, err, backupartifact.ErrObjectNotFound)
}

func TestGenerationGarbageCollectorResumesDurableCursorWithinRequestBudget(t *testing.T) {
	ctx := context.Background()
	primaryFile, err := backupinfra.NewFileRepository("primary", t.TempDir())
	require.NoError(t, err)
	secondaryFile, err := backupinfra.NewFileRepository("secondary", t.TempDir())
	require.NoError(t, err)
	primary := &generationGCRepository{FileRepository: primaryFile}
	secondary := &generationGCRepository{FileRepository: secondaryFile}
	for index := 0; index < 5; index++ {
		key := fmt.Sprintf("objects/%s/attempt-1/00000/object-%02d.bin", gcGenerationExpired, index)
		for _, repository := range []backupartifact.Repository{primary, secondary} {
			generationGCPutObject(t, repository, key, []byte(key))
		}
	}
	signer := newCatalogTestSigner()
	catalog, err := backupinfra.NewReplicatedCheckpointCatalog(primary, secondary, signer, "signing-key")
	require.NoError(t, err)
	retainedOne := generationGCPublishCheckpoint(
		t, catalog, nil, "retained-one", gcGenerationRetained,
	)
	retainedTwo := generationGCPublishCheckpoint(
		t, catalog, &retainedOne.Head, "retained-two", gcGenerationHeld,
	)
	retainedThree := generationGCPublishCheckpoint(
		t, catalog, &retainedTwo.Head, "retained-three", gcGenerationActive,
	)
	stateStore := &erasureLedgerStateStore{}
	cursorStore, err := backupinfra.NewControllerGenerationGCCursorStore(stateStore)
	require.NoError(t, err)
	cacheRoot := t.TempDir()
	cache, err := backupinfra.NewFileGenerationVectorCache(cacheRoot, signer)
	require.NoError(t, err)
	collector := generationGCCollector(t, primary, secondary, catalog, signer, cursorStore, cache, 5, 1<<20)

	protection := generationGCCurrentProtection()
	protection.Retained = []backupartifact.CatalogCheckpointReference{
		retainedOne.Checkpoint, retainedTwo.Checkpoint, retainedThree.Checkpoint,
	}
	first, err := collector.Collect(ctx, "cycle-resume", protection)
	require.NoError(t, err)
	require.LessOrEqual(t, first.Repositories[0].DeletedObjects, 3)
	require.LessOrEqual(t, first.Repositories[1].DeletedObjects, 3)
	require.False(t, first.Repositories[0].Complete)
	require.False(t, first.Repositories[1].Complete)

	// Recreate both the cursor adapter and collector to prove process-local
	// memory is not the continuation authority.
	cursorStore, err = backupinfra.NewControllerGenerationGCCursorStore(stateStore)
	require.NoError(t, err)
	cache, err = backupinfra.NewFileGenerationVectorCache(cacheRoot, signer)
	require.NoError(t, err)
	collector = generationGCCollector(t, primary, secondary, catalog, signer, cursorStore, cache, 5, 1<<20)
	second, err := collector.Collect(ctx, "cycle-resume", protection)
	require.NoError(t, err)
	require.Greater(t, primary.walkCount(), 0, "restarted collector must reuse cached vectors and reach sweep")
	require.Greater(t, secondary.walkCount(), 0, "restarted collector must reuse cached vectors and reach sweep")
	require.LessOrEqual(t, second.Repositories[0].DeletedObjects, 2)
	generationGCCompleteCycle(t, collector, "cycle-resume", protection)
	for _, repository := range []backupartifact.Repository{primary, secondary} {
		for index := 0; index < 5; index++ {
			key := fmt.Sprintf("objects/%s/attempt-1/00000/object-%02d.bin", gcGenerationExpired, index)
			_, err := repository.Stat(ctx, key)
			require.ErrorIs(t, err, backupartifact.ErrObjectNotFound, key)
		}
	}
}

func TestGenerationGarbageCollectorResumesWithinByteBudget(t *testing.T) {
	ctx := context.Background()
	primaryFile, err := backupinfra.NewFileRepository("primary", t.TempDir())
	require.NoError(t, err)
	secondaryFile, err := backupinfra.NewFileRepository("secondary", t.TempDir())
	require.NoError(t, err)
	primary := &generationGCRepository{FileRepository: primaryFile}
	secondary := &generationGCRepository{FileRepository: secondaryFile}
	for _, key := range []string{
		"objects/" + gcGenerationExpired + "/attempt-1/00000/a.bin",
		"objects/" + gcGenerationExpired + "/attempt-1/00000/b.bin",
	} {
		for _, repository := range []backupartifact.Repository{primary, secondary} {
			generationGCPutObject(t, repository, key, []byte("12345678"))
		}
	}
	signer := newCatalogTestSigner()
	catalog, err := backupinfra.NewReplicatedCheckpointCatalog(primary, secondary, signer, "signing-key")
	require.NoError(t, err)
	cursorStore, err := backupinfra.NewControllerGenerationGCCursorStore(&erasureLedgerStateStore{})
	require.NoError(t, err)
	cache := newGenerationGCCache(t, signer)
	collector := generationGCCollector(t, primary, secondary, catalog, signer, cursorStore, cache, 256, 10)

	protection := generationGCCurrentProtection()
	first, err := collector.Collect(ctx, "cycle-byte-budget", protection)
	require.NoError(t, err)
	require.Equal(t, 1, first.Repositories[0].DeletedObjects)
	require.Equal(t, int64(8), first.Repositories[0].DeletedBytes)
	require.False(t, first.Repositories[0].Complete)
	require.Equal(t, 1, first.Repositories[1].DeletedObjects)
	require.False(t, first.Repositories[1].Complete)

	generationGCCompleteCycle(t, collector, "cycle-byte-budget", protection)
	for _, repository := range []backupartifact.Repository{primary, secondary} {
		for _, key := range []string{
			"objects/" + gcGenerationExpired + "/attempt-1/00000/a.bin",
			"objects/" + gcGenerationExpired + "/attempt-1/00000/b.bin",
		} {
			_, err := repository.Stat(ctx, key)
			require.ErrorIs(t, err, backupartifact.ErrObjectNotFound, key)
		}
	}
}

func TestGenerationGarbageCollectorRechecksAuditFreezeBeforeDelete(t *testing.T) {
	ctx := context.Background()
	primaryFile, err := backupinfra.NewFileRepository("primary", t.TempDir())
	require.NoError(t, err)
	secondaryFile, err := backupinfra.NewFileRepository("secondary", t.TempDir())
	require.NoError(t, err)
	primary := &generationGCRepository{FileRepository: primaryFile}
	secondary := &generationGCRepository{FileRepository: secondaryFile}
	key := "objects/" + gcGenerationExpired + "/attempt-1/00000/meta.bin"
	for _, repository := range []backupartifact.Repository{primary, secondary} {
		generationGCPutObject(t, repository, key, []byte("freeze-before-delete"))
	}
	signer := newCatalogTestSigner()
	catalog, err := backupinfra.NewReplicatedCheckpointCatalog(
		primary, secondary, signer, "signing-key",
	)
	require.NoError(t, err)
	coordination := &erasureLedgerStateStore{}
	auditStore, err := backupinfra.NewControllerIntegrityAuditStateStore(coordination)
	require.NoError(t, err)
	_, err = auditStore.LoadIntegrityAudit(ctx)
	require.NoError(t, err)
	primary.beforeList = func() {
		require.NoError(t, auditStore.CompareAndSwapIntegrityAudit(
			ctx, 0,
			backupcontract.IntegrityAuditState{
				Revision: 1,
				Slots: []backupcontract.SlotIntegrityAuditState{{
					HashSlot: 0, Generation: gcGenerationExpired,
					Health: backupcontract.SlotAuditDegraded,
				}},
			},
		))
	}
	cursorStore, err := backupinfra.NewControllerGenerationGCCursorStore(coordination)
	require.NoError(t, err)
	collector, err := backupinfra.NewGenerationGarbageCollector(
		backupinfra.GenerationGarbageCollectorOptions{
			Primary: primary, Secondary: secondary, Catalog: catalog,
			Signer: signer, Cursors: cursorStore,
			VectorCache:     newGenerationGCCache(t, signer),
			IntegrityGuard:  auditStore,
			AuditProtection: emptyGenerationGCAuditProtection{},
			AuditRoots:      allowCatalogAuditRootStore{},
			HashSlotCount:   2, SafetyWindow: 7 * 24 * time.Hour,
			MaxRequestsPerRepository: 16, MaxBytesPerRepository: 1 << 20,
			Now: func() time.Time {
				return time.Now().UTC().Add(8 * 24 * time.Hour)
			},
		},
	)
	require.NoError(t, err)

	result, err := collector.Collect(
		ctx, "cycle-mid-sweep-freeze", generationGCCurrentProtection(),
	)
	require.NoError(t, err)
	require.True(t, result.Repositories[0].Complete)
	require.True(t, result.Repositories[1].Complete)
	for _, repository := range []backupartifact.Repository{primary, secondary} {
		_, err := repository.Stat(ctx, key)
		require.NoError(t, err)
	}
}

func generationGCPublishCheckpoint(
	t *testing.T,
	catalog *backupinfra.ReplicatedCheckpointCatalog,
	previous *backupartifact.CatalogPageReference,
	id, slotZeroGeneration string,
) backupartifact.CheckpointCatalogCommit {
	t.Helper()
	checkpoint := catalogTestCheckpoint(id, time.Now().UTC().UnixMilli())
	checkpoint.Slots[0].Generation = slotZeroGeneration
	checkpoint.Slots[1].Generation = "generation-current-1"
	commit, err := catalog.Publish(context.Background(), checkpoint, previous)
	require.NoError(t, err)
	return commit
}

func generationGCCollector(
	t *testing.T,
	primary, secondary backupinfra.GenerationGarbageRepository,
	catalog *backupinfra.ReplicatedCheckpointCatalog,
	signer backupartifact.ManifestSigner,
	cursors backupinfra.GenerationGCCursorStore,
	cache backupinfra.GenerationVectorCache,
	maxRequests int,
	maxBytes int64,
) *backupinfra.GenerationGarbageCollector {
	t.Helper()
	collector, err := backupinfra.NewGenerationGarbageCollector(backupinfra.GenerationGarbageCollectorOptions{
		Primary: primary, Secondary: secondary, Catalog: catalog,
		Signer: signer, Cursors: cursors, VectorCache: cache,
		IntegrityGuard:  allowGenerationGCIntegrityGuard{},
		AuditProtection: emptyGenerationGCAuditProtection{},
		AuditRoots:      allowCatalogAuditRootStore{},
		HashSlotCount:   2, SafetyWindow: 7 * 24 * time.Hour,
		MaxRequestsPerRepository: maxRequests, MaxBytesPerRepository: maxBytes,
		Now: func() time.Time { return time.Now().UTC().Add(8 * 24 * time.Hour) },
	})
	require.NoError(t, err)
	return collector
}

type allowGenerationGCIntegrityGuard struct{}

func (allowGenerationGCIntegrityGuard) WithGenerationGCDelete(
	_ context.Context,
	_ uint16,
	_ string,
	_ uint64,
	deleteObject func(context.Context) (int, error),
) (bool, int, error) {
	used, err := deleteObject(context.Background())
	return true, used, err
}

type emptyGenerationGCAuditProtection struct{}

func (emptyGenerationGCAuditProtection) LoadIntegrityAuditRetainedCheckpoints(
	context.Context,
	backupcontract.IntegrityAuditCursor,
) ([]backupartifact.CatalogCheckpointReference, error) {
	return nil, nil
}

type staticGenerationGCAuditProtection struct {
	references []backupartifact.CatalogCheckpointReference
}

func (s staticGenerationGCAuditProtection) LoadIntegrityAuditRetainedCheckpoints(
	context.Context,
	backupcontract.IntegrityAuditCursor,
) ([]backupartifact.CatalogCheckpointReference, error) {
	return append(
		[]backupartifact.CatalogCheckpointReference(nil),
		s.references...,
	), nil
}

func newGenerationGCCache(
	t *testing.T,
	signer backupartifact.ManifestSigner,
) backupinfra.GenerationVectorCache {
	t.Helper()
	cache, err := backupinfra.NewFileGenerationVectorCache(t.TempDir(), signer)
	require.NoError(t, err)
	return cache
}

func generationGCCurrentProtection() backupinfra.GenerationGCProtection {
	return backupinfra.GenerationGCProtection{
		RetainedCatalogRootSequence: 1,
		CatalogRetentionRevision:    1,
		Current: []backupcontract.SlotFrontier{
			{HashSlot: 0, Generation: gcGenerationCurrent},
			{HashSlot: 1, Generation: "generation-current-1"},
		}}
}

type allowCatalogAuditRootStore struct{}

func (allowCatalogAuditRootStore) AdvanceCatalogAuditRoot(
	context.Context,
	uint64,
) error {
	return nil
}

func generationGCCompleteCycle(
	t *testing.T,
	collector *backupinfra.GenerationGarbageCollector,
	cycleID string,
	protection backupinfra.GenerationGCProtection,
) {
	t.Helper()
	for attempt := 0; attempt < 64; attempt++ {
		result, err := collector.Collect(context.Background(), cycleID, protection)
		require.NoError(t, err)
		complete := true
		for _, repository := range result.Repositories {
			complete = complete && repository.Complete
		}
		if complete {
			return
		}
	}
	t.Fatalf("generation GC cycle %q did not complete", cycleID)
}

func generationGCPutObject(t *testing.T, repository backupartifact.Repository, key string, body []byte) {
	t.Helper()
	hash := sha256.Sum256(body)
	require.NoError(t, repository.PutImmutable(
		context.Background(), key, int64(len(body)), fmt.Sprintf("%x", hash), bytes.NewReader(body),
	))
}

type generationGCRepository struct {
	*backupinfra.FileRepository
	mu         sync.Mutex
	lockedKey  string
	walks      int
	opens      int
	listErr    error
	beforeList func()
}

func (r *generationGCRepository) WalkGarbageObjects(
	ctx context.Context,
	before time.Time,
	visit func(backupartifact.RepositoryObject) (bool, error),
) error {
	r.mu.Lock()
	r.walks++
	r.mu.Unlock()
	return r.FileRepository.WalkGarbageObjects(ctx, before, visit)
}

func (r *generationGCRepository) ListGarbageObjects(
	ctx context.Context,
	before time.Time,
	afterKey string,
	limit int,
) (backupinfra.GarbageObjectPage, error) {
	r.mu.Lock()
	r.walks++
	err := r.listErr
	beforeList := r.beforeList
	r.beforeList = nil
	r.mu.Unlock()
	if beforeList != nil {
		beforeList()
	}
	if err != nil {
		return backupinfra.GarbageObjectPage{}, err
	}
	return r.FileRepository.ListGarbageObjects(ctx, before, afterKey, limit)
}

func (r *generationGCRepository) Open(
	ctx context.Context,
	key string,
) (io.ReadCloser, backupartifact.RepositoryObject, error) {
	r.mu.Lock()
	r.opens++
	r.mu.Unlock()
	return r.FileRepository.Open(ctx, key)
}

func (r *generationGCRepository) DeleteGarbageObject(ctx context.Context, key string) error {
	r.mu.Lock()
	locked := key == r.lockedKey
	r.mu.Unlock()
	if locked {
		return backupartifact.ErrObjectLocked
	}
	return r.FileRepository.DeleteGarbageObject(ctx, key)
}

func (r *generationGCRepository) DeleteGenerationGarbageObject(
	ctx context.Context,
	key string,
	maxRequests int,
) (int, error) {
	r.mu.Lock()
	locked := key == r.lockedKey
	r.mu.Unlock()
	if locked {
		if maxRequests < 1 {
			return 0, fmt.Errorf("unexpected zero request budget")
		}
		return 1, backupartifact.ErrObjectLocked
	}
	return r.FileRepository.DeleteGenerationGarbageObject(ctx, key, maxRequests)
}

func (r *generationGCRepository) setLockedKey(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lockedKey = key
}

func (r *generationGCRepository) setListError(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.listErr = err
}

func (r *generationGCRepository) walkCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.walks
}

var _ backupinfra.GenerationGarbageRepository = (*generationGCRepository)(nil)
