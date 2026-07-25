package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

const maxGenerationGCCommitBytes = 1 << 20

var errGenerationGCRequestBudget = errors.New("backup generation GC: request budget exhausted")

// GarbageObjectPage is one lexicographically bounded repository scan page.
// AfterKey includes scanned objects newer than the fixed cutoff, allowing a
// durable cursor to advance without rescanning them in the same cycle.
type GarbageObjectPage struct {
	// Objects contains strictly increasing keys after the requested cursor.
	Objects []backupartifact.RepositoryObject
	// AfterKey is the greatest key scanned, including ineligible young objects.
	AfterKey string
	// Complete reports that no lexicographically later object remains.
	Complete bool
}

// GenerationGarbageRepository adds cursor-based bounded listing to the
// restricted garbage-collector repository seam.
type GenerationGarbageRepository interface {
	GarbageRepository
	ListGarbageObjects(context.Context, time.Time, string, int) (GarbageObjectPage, error)
	// DeleteGenerationGarbageObject deletes the sole immutable version without
	// exceeding maxRequests and reports the exact provider calls consumed.
	DeleteGenerationGarbageObject(context.Context, string, int) (int, error)
}

// GenerationGCIntegrityGuard linearizes destructive work with durable audit
// health transitions for the same Hash Slot.
type GenerationGCIntegrityGuard interface {
	// WithGenerationGCDelete runs deleteObject only while hashSlot remains healthy.
	// protectedAuditCycleID proves the caller marked that exact unfinished
	// sparse selection. allowed is false for a different concurrent cycle.
	WithGenerationGCDelete(
		context.Context,
		uint16,
		string,
		func(context.Context) (int, error),
	) (allowed bool, used int, err error)
}

// GenerationGCIntegrityAuditProtectionSource rebuilds the exact sparse
// checkpoint set fixed by one unfinished durable integrity-audit cursor.
type GenerationGCIntegrityAuditProtectionSource interface {
	LoadIntegrityAuditRetainedCheckpoints(
		context.Context,
		backupcontract.IntegrityAuditCursor,
	) ([]backupartifact.CatalogCheckpointReference, error)
}

// GenerationGCProtection identifies every checkpoint and live Slot state that
// must keep a complete Generation reachable.
type GenerationGCProtection struct {
	// RetainedCatalogRootSequence is the oldest catalog page represented by
	// Retained, Held, or ActiveRestore and must be fenced before deletion.
	RetainedCatalogRootSequence uint64
	// Retained contains checkpoint vectors selected by the UTC retention tiers.
	Retained []backupartifact.CatalogCheckpointReference
	// Held contains operator-protected checkpoint vectors.
	Held []backupartifact.CatalogCheckpointReference
	// ActiveRestore identifies the checkpoint currently consumed by restore.
	ActiveRestore *backupartifact.CatalogCheckpointReference
	// Current contains exactly one sorted authoritative frontier per Hash Slot.
	Current []backupcontract.SlotFrontier
	// FrozenHashSlots are degraded/auditor-owned Slots whose every Generation
	// remains protected until repair clears the freeze.
	FrozenHashSlots []uint16
	// IntegrityAudit contributes durable degraded, rebase-required, and failed
	// Slots directly, and identifies an unfinished sparse selection that is
	// rebuilt and added to the mark set across Controller Leader failover.
	IntegrityAudit backupcontract.IntegrityAuditState
}

// GenerationGarbageCollectorOptions configures independent repository sweeps.
type GenerationGarbageCollectorOptions struct {
	// Primary and Secondary are the two explicit independent repository copies.
	Primary   GenerationGarbageRepository
	Secondary GenerationGarbageRepository
	// Catalog loads one exact signed Generation vector from an explicit copy.
	Catalog *ReplicatedCheckpointCatalog
	// Signer authenticates signed segment commits during classification.
	Signer backupartifact.ManifestSigner
	// Cursors persists one compact independent sweep position per copy.
	Cursors GenerationGCCursorStore
	// VectorCache durably resumes bounded protection-vector authentication.
	VectorCache GenerationVectorCache
	// IntegrityGuard prevents a concurrent audit freeze from racing deletion.
	IntegrityGuard GenerationGCIntegrityGuard
	// AuditProtection adds the exact sparse selection of an unfinished audit
	// to the current Generation mark set.
	AuditProtection GenerationGCIntegrityAuditProtectionSource
	// AuditRoots advances the durable retained catalog lower bound before any
	// expired Generation can be deleted.
	AuditRoots CatalogAuditRootStore

	// HashSlotCount is the immutable complete vector width.
	HashSlotCount uint16
	// SafetyWindow protects recently published immutable objects from sweep.
	SafetyWindow time.Duration
	// MaxRequestsPerRepository includes vector-cache misses, one listing, commit
	// classification, exact-version deletion, and Object Lock classification.
	MaxRequestsPerRepository int
	// MaxBytesPerRepository bounds deleted stored bytes in one call.
	MaxBytesPerRepository int64
	// Now supplies the UTC cutoff clock.
	Now func() time.Time
}

// GenerationGCRepositoryResult is bounded evidence for one independent copy.
type GenerationGCRepositoryResult struct {
	// Repository identifies the independently processed copy.
	Repository string
	// DeletedObjects and DeletedBytes report bounded destructive work.
	DeletedObjects int
	DeletedBytes   int64
	// LockedObjects reports Object Lock stops at the exact current key.
	LockedObjects int
	// Complete reports this copy finished the named cycle.
	Complete bool
	// AfterKey is the durable lexicographic continuation position.
	AfterKey string
}

// GenerationGCResult reports both failure domains even when one failed.
type GenerationGCResult struct {
	// Repositories contains one result for each explicit failure domain.
	Repositories []GenerationGCRepositoryResult
}

// GenerationGarbageCollector independently authenticates and sweeps two
// repository copies under durable bounded cursors.
type GenerationGarbageCollector struct {
	primary, secondary GenerationGarbageRepository
	catalog            *ReplicatedCheckpointCatalog
	signer             backupartifact.ManifestSigner
	cursors            GenerationGCCursorStore
	vectorCache        GenerationVectorCache
	integrityGuard     GenerationGCIntegrityGuard
	auditProtection    GenerationGCIntegrityAuditProtectionSource
	auditRoots         CatalogAuditRootStore
	hashSlotCount      uint16
	safetyWindow       time.Duration
	maxRequests        int
	maxBytes           int64
	now                func() time.Time
}

// NewGenerationGarbageCollector creates a Generation-aware bounded collector.
func NewGenerationGarbageCollector(options GenerationGarbageCollectorOptions) (*GenerationGarbageCollector, error) {
	if options.Primary == nil || options.Secondary == nil ||
		options.Primary.Name() == "" || options.Secondary.Name() == "" ||
		options.Primary.Name() == options.Secondary.Name() ||
		options.Catalog == nil || options.Signer == nil ||
		options.Cursors == nil || options.VectorCache == nil ||
		options.IntegrityGuard == nil ||
		options.AuditProtection == nil ||
		options.AuditRoots == nil ||
		options.HashSlotCount == 0 || options.SafetyWindow <= 0 {
		return nil, fmt.Errorf("backup generation GC: invalid dependencies")
	}
	if options.MaxRequestsPerRepository == 0 {
		options.MaxRequestsPerRepository = 4096
	}
	if options.MaxBytesPerRepository == 0 {
		options.MaxBytesPerRepository = 1 << 30
	}
	if options.MaxRequestsPerRepository < 5 || options.MaxRequestsPerRepository > 4096 ||
		options.MaxBytesPerRepository <= 0 {
		return nil, fmt.Errorf("backup generation GC: invalid work budget")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &GenerationGarbageCollector{
		primary: options.Primary, secondary: options.Secondary,
		catalog: options.Catalog, signer: options.Signer,
		cursors: options.Cursors, vectorCache: options.VectorCache,
		integrityGuard:  options.IntegrityGuard,
		auditProtection: options.AuditProtection,
		auditRoots:      options.AuditRoots,
		hashSlotCount:   options.HashSlotCount, safetyWindow: options.SafetyWindow,
		maxRequests: options.MaxRequestsPerRepository, maxBytes: options.MaxBytesPerRepository,
		now: options.Now,
	}, nil
}

// Collect executes or resumes one named sweep. A completed repository is not
// rescanned while the peer retries the same cycle.
func (c *GenerationGarbageCollector) Collect(
	ctx context.Context,
	cycleID string,
	protection GenerationGCProtection,
) (GenerationGCResult, error) {
	if c == nil || !validControllerCaptureGeneration(strings.TrimSpace(cycleID)) {
		return GenerationGCResult{}, fmt.Errorf("backup generation GC: invalid cycle")
	}
	auditCycleID, auditRetained, err :=
		c.loadIntegrityAuditProtection(
			ctx, protection.IntegrityAudit,
		)
	if err != nil {
		return GenerationGCResult{}, err
	}
	protection.Retained = append(
		append(
			[]backupartifact.CatalogCheckpointReference(nil),
			protection.Retained...,
		),
		auditRetained...,
	)
	baseProtected, current, frozen, err := c.buildLiveProtection(protection)
	if err != nil {
		return GenerationGCResult{}, err
	}
	if protection.RetainedCatalogRootSequence == 0 {
		return GenerationGCResult{}, fmt.Errorf(
			"backup generation GC: retained catalog root is invalid",
		)
	}
	if err := c.auditRoots.AdvanceCatalogAuditRoot(
		ctx, protection.RetainedCatalogRootSequence,
	); err != nil {
		return GenerationGCResult{}, err
	}
	type repositoryPair struct {
		current GenerationGarbageRepository
		peer    GenerationGarbageRepository
	}
	pairs := []repositoryPair{{c.primary, c.secondary}, {c.secondary, c.primary}}
	result := GenerationGCResult{Repositories: make([]GenerationGCRepositoryResult, 0, 2)}
	var collectedErr error
	for _, pair := range pairs {
		completed, found, err := c.completedRepositoryCycle(ctx, cycleID, pair.current)
		if err != nil {
			result.Repositories = append(result.Repositories, GenerationGCRepositoryResult{
				Repository: pair.current.Name(),
			})
			collectedErr = errors.Join(collectedErr, err)
			continue
		}
		if found {
			result.Repositories = append(result.Repositories, completed)
			continue
		}
		requests := c.maxRequests
		protected, vectorIDs, ready, err := c.buildRepositoryGenerationSet(
			ctx, pair.current, protection, baseProtected, &requests,
		)
		if err != nil {
			result.Repositories = append(result.Repositories, GenerationGCRepositoryResult{
				Repository: pair.current.Name(),
			})
			collectedErr = errors.Join(collectedErr, err)
			continue
		}
		if !ready {
			result.Repositories = append(result.Repositories, GenerationGCRepositoryResult{
				Repository: pair.current.Name(),
			})
			continue
		}
		repositoryResult, err := c.collectRepository(
			ctx, cycleID, pair.current,
			protected, current, frozen, vectorIDs, requests,
			auditCycleID,
		)
		result.Repositories = append(result.Repositories, repositoryResult)
		if err != nil {
			collectedErr = errors.Join(collectedErr, err)
		}
	}
	return result, collectedErr
}

func (c *GenerationGarbageCollector) loadIntegrityAuditProtection(
	ctx context.Context,
	state backupcontract.IntegrityAuditState,
) (
	string,
	[]backupartifact.CatalogCheckpointReference,
	error,
) {
	cursor := state.Cursor
	if cursor == nil ||
		!strings.HasPrefix(cursor.CycleID, "catalog-segments-") ||
		cursor.Phase == backupcontract.IntegrityAuditPhaseComplete {
		return "", nil, nil
	}
	references, err := c.auditProtection.
		LoadIntegrityAuditRetainedCheckpoints(ctx, *cursor)
	if err != nil {
		return "", nil, fmt.Errorf(
			"backup generation GC: load integrity audit protection: %w",
			err,
		)
	}
	if len(references) == 0 {
		return "", nil, fmt.Errorf(
			"backup generation GC: integrity audit protection is empty",
		)
	}
	return cursor.CycleID, references, nil
}

func (c *GenerationGarbageCollector) completedRepositoryCycle(
	ctx context.Context,
	cycleID string,
	repository GenerationGarbageRepository,
) (GenerationGCRepositoryResult, bool, error) {
	cursor, found, err := c.cursors.LoadGenerationGCCursor(ctx, repository.Name())
	if err != nil || !found || cursor.CycleID != cycleID || !cursor.Complete {
		return GenerationGCRepositoryResult{}, false, err
	}
	return GenerationGCRepositoryResult{
		Repository: repository.Name(), Complete: true, AfterKey: cursor.AfterKey,
	}, true, nil
}

type generationIdentity struct {
	hashSlot   uint16
	generation string
}

func (c *GenerationGarbageCollector) buildLiveProtection(
	protection GenerationGCProtection,
) (map[generationIdentity]struct{}, map[uint16]string, map[uint16]struct{}, error) {
	if len(protection.Current) != int(c.hashSlotCount) {
		return nil, nil, nil, fmt.Errorf("backup generation GC: current Slot coverage is incomplete")
	}
	protected := make(map[generationIdentity]struct{}, len(protection.Current))
	current := make(map[uint16]string, len(protection.Current))
	for index, frontier := range protection.Current {
		if frontier.HashSlot != uint16(index) ||
			!validControllerCaptureGeneration(frontier.Generation) {
			return nil, nil, nil, fmt.Errorf("backup generation GC: current Slot frontier is unsafe")
		}
		identity := generationIdentity{hashSlot: frontier.HashSlot, generation: frontier.Generation}
		protected[identity] = struct{}{}
		if frontier.Rebase != nil {
			if !validControllerCaptureGeneration(frontier.Rebase.TargetGeneration) ||
				frontier.Rebase.TargetGeneration == frontier.Generation {
				return nil, nil, nil, fmt.Errorf("backup generation GC: pending replacement is unsafe")
			}
			protected[generationIdentity{
				hashSlot: frontier.HashSlot, generation: frontier.Rebase.TargetGeneration,
			}] = struct{}{}
		}
		current[frontier.HashSlot] = frontier.Generation
	}
	durableFrozen := backupcontract.FrozenAuditHashSlots(protection.IntegrityAudit)
	frozen := make(map[uint16]struct{}, len(protection.FrozenHashSlots)+len(durableFrozen))
	for index, hashSlot := range protection.FrozenHashSlots {
		if hashSlot >= c.hashSlotCount ||
			(index > 0 && protection.FrozenHashSlots[index-1] >= hashSlot) {
			return nil, nil, nil, fmt.Errorf("backup generation GC: frozen Slots are invalid")
		}
		frozen[hashSlot] = struct{}{}
	}
	for _, hashSlot := range durableFrozen {
		if hashSlot >= c.hashSlotCount {
			return nil, nil, nil, fmt.Errorf("backup generation GC: durable audit Slot is invalid")
		}
		frozen[hashSlot] = struct{}{}
	}
	return protected, current, frozen, nil
}

func (c *GenerationGarbageCollector) buildRepositoryGenerationSet(
	ctx context.Context,
	repository GenerationGarbageRepository,
	protection GenerationGCProtection,
	baseProtected map[generationIdentity]struct{},
	requests *int,
) (map[generationIdentity]struct{}, map[string]struct{}, bool, error) {
	if requests == nil || *requests < 2 {
		return nil, nil, false, errGenerationGCRequestBudget
	}
	protected := make(map[generationIdentity]struct{}, len(baseProtected))
	for identity := range baseProtected {
		protected[identity] = struct{}{}
	}
	references := make([]backupartifact.CatalogCheckpointReference, 0, len(protection.Retained)+len(protection.Held)+1)
	references = append(references, protection.Retained...)
	references = append(references, protection.Held...)
	if protection.ActiveRestore != nil {
		references = append(references, *protection.ActiveRestore)
	}
	seenVectors := make(map[string]struct{}, len(references))
	for _, reference := range references {
		vectorReference := reference.GenerationVector
		if _, seen := seenVectors[vectorReference.ID]; seen {
			continue
		}
		seenVectors[vectorReference.ID] = struct{}{}
		vector, found, err := c.vectorCache.LoadGenerationVector(
			ctx, repository.Name(), vectorReference,
		)
		if err != nil {
			return nil, nil, false, fmt.Errorf(
				"backup generation GC: load %s vector cache: %w",
				repository.Name(), err,
			)
		}
		if !found {
			// Keep two provider requests available for one listing and
			// classification/deletion progress. The authenticated local cache
			// is the durable protection-phase continuation authority.
			if *requests <= 2 {
				return protected, seenVectors, false, nil
			}
			*requests--
			var body []byte
			vector, body, err = c.catalog.LoadGenerationVectorCopy(
				ctx, repository, vectorReference,
			)
			if err != nil {
				return nil, nil, false, fmt.Errorf(
					"backup generation GC: authenticate protected vector %s in %s: %w",
					vectorReference.ID, repository.Name(), err,
				)
			}
			if err := c.vectorCache.StoreGenerationVector(
				ctx, repository.Name(), vectorReference, vector, body,
			); err != nil {
				return nil, nil, false, fmt.Errorf(
					"backup generation GC: store %s vector cache: %w",
					repository.Name(), err,
				)
			}
		}
		if vector.HashSlotCount != c.hashSlotCount ||
			len(vector.Generations) != int(c.hashSlotCount) {
			return nil, nil, false, fmt.Errorf("backup generation GC: protected checkpoint coverage differs")
		}
		for hashSlot, generation := range vector.Generations {
			if !validControllerCaptureGeneration(generation) {
				return nil, nil, false, fmt.Errorf("backup generation GC: protected checkpoint generation is invalid")
			}
			protected[generationIdentity{
				hashSlot: uint16(hashSlot), generation: generation,
			}] = struct{}{}
		}
	}
	return protected, seenVectors, true, nil
}

func (c *GenerationGarbageCollector) collectRepository(
	ctx context.Context,
	cycleID string,
	repository GenerationGarbageRepository,
	protected map[generationIdentity]struct{},
	current map[uint16]string,
	frozen map[uint16]struct{},
	vectorIDs map[string]struct{},
	requestBudget int,
	auditCycleID string,
) (GenerationGCRepositoryResult, error) {
	result := GenerationGCRepositoryResult{Repository: repository.Name()}
	cursor, found, err := c.cursors.LoadGenerationGCCursor(ctx, repository.Name())
	if err != nil {
		return result, err
	}
	if !found {
		cursor = backupcontract.GenerationGCCursor{
			Repository: repository.Name(), CycleID: cycleID,
			CutoffUnixMillis: c.now().UTC().Add(-c.safetyWindow).UnixMilli(),
		}
		if err := c.saveCursor(ctx, cursor, 0); err != nil {
			return result, err
		}
		cursor.Revision = 1
	} else if cursor.CycleID != cycleID {
		if !cursor.Complete {
			return result, fmt.Errorf("backup generation GC: repository %s has unfinished cycle %s", repository.Name(), cursor.CycleID)
		}
		next := backupcontract.GenerationGCCursor{
			Repository: repository.Name(), CycleID: cycleID,
			CutoffUnixMillis: c.now().UTC().Add(-c.safetyWindow).UnixMilli(),
		}
		if err := c.saveCursor(ctx, next, cursor.Revision); err != nil {
			return result, err
		}
		next.Revision = cursor.Revision + 1
		cursor = next
	}
	if cursor.Complete {
		result.Complete = true
		result.AfterKey = cursor.AfterKey
		return result, nil
	}
	if requestBudget < 2 {
		return result, errGenerationGCRequestBudget
	}

	page, err := repository.ListGarbageObjects(
		ctx, time.UnixMilli(cursor.CutoffUnixMillis).UTC(),
		cursor.AfterKey, min(requestBudget-1, 4096),
	)
	if err != nil {
		return result, fmt.Errorf("backup generation GC: list %s: %w", repository.Name(), err)
	}
	if page.AfterKey < cursor.AfterKey ||
		(page.Complete && page.AfterKey == "" && cursor.AfterKey != "") ||
		(!page.Complete && page.AfterKey == cursor.AfterKey) {
		return result, fmt.Errorf("backup generation GC: repository %s returned an invalid scan cursor", repository.Name())
	}
	previousKey := cursor.AfterKey
	for _, object := range page.Objects {
		if object.Key <= previousKey || object.Key > page.AfterKey {
			return result, fmt.Errorf("backup generation GC: repository %s returned an invalid object page", repository.Name())
		}
		previousKey = object.Key
	}
	requests := requestBudget - 1
	afterKey := cursor.AfterKey
	processedAll := true
	for _, object := range page.Objects {
		identity, classified, err := c.classifyGenerationObject(
			ctx, repository, object.Key, &requests,
		)
		if errors.Is(err, errGenerationGCRequestBudget) {
			processedAll = false
			break
		}
		if err != nil {
			return result, err
		}
		eligible := classified
		if identity.generation != "" {
			if _, slotFrozen := frozen[identity.hashSlot]; slotFrozen {
				eligible = false
			}
			if _, retained := protected[identity]; retained {
				eligible = false
			}
			if successor, ok := current[identity.hashSlot]; !ok || successor == identity.generation {
				eligible = false
			}
		}
		if eligible {
			if object.Size <= 0 {
				return result, fmt.Errorf("backup generation GC: repository %s returned invalid object size", repository.Name())
			}
			if object.Size > c.maxBytes {
				return result, fmt.Errorf(
					"backup generation GC: object %q exceeds per-repository byte budget", object.Key,
				)
			}
			if object.Size > c.maxBytes-result.DeletedBytes {
				processedAll = false
				break
			}
			allowed := true
			var used int
			if identity.generation == "" {
				used, err = repository.DeleteGenerationGarbageObject(
					ctx, object.Key, requests,
				)
			} else {
				allowed, used, err = c.integrityGuard.WithGenerationGCDelete(
					ctx, identity.hashSlot, auditCycleID,
					func(deleteCtx context.Context) (int, error) {
						return repository.DeleteGenerationGarbageObject(
							deleteCtx, object.Key, requests,
						)
					},
				)
			}
			if used < 0 || used > requests {
				return result, fmt.Errorf("backup generation GC: repository %s exceeded delete request budget", repository.Name())
			}
			requests -= used
			if !allowed {
				afterKey = object.Key
				continue
			}
			if errors.Is(err, errGenerationGCRequestBudget) {
				processedAll = false
				break
			}
			if err != nil {
				if errors.Is(err, backupartifact.ErrObjectLocked) {
					result.LockedObjects++
					processedAll = false
					break
				}
				return result, fmt.Errorf("backup generation GC: delete %s object %q: %w", repository.Name(), object.Key, err)
			}
			result.DeletedObjects++
			result.DeletedBytes += object.Size
		}
		afterKey = object.Key
	}
	if processedAll {
		afterKey = page.AfterKey
	}
	complete := processedAll && page.Complete
	if complete {
		if err := c.vectorCache.PruneGenerationVectors(
			ctx, repository.Name(), vectorIDs,
		); err != nil {
			return result, fmt.Errorf(
				"backup generation GC: prune %s vector cache: %w",
				repository.Name(), err,
			)
		}
	}
	next := cursor
	next.AfterKey = afterKey
	next.Complete = complete
	if err := c.saveCursor(ctx, next, cursor.Revision); err != nil {
		return result, err
	}
	result.Complete = complete
	result.AfterKey = afterKey
	return result, nil
}

func (c *GenerationGarbageCollector) saveCursor(
	ctx context.Context,
	cursor backupcontract.GenerationGCCursor,
	expectedRevision uint64,
) error {
	cursor.Revision = expectedRevision + 1
	cursor.UpdatedAtUnixMillis = c.now().UTC().UnixMilli()
	return c.cursors.CompareAndSwapGenerationGCCursor(
		ctx, cursor.Repository, expectedRevision, cursor,
	)
}

func (c *GenerationGarbageCollector) classifyGenerationObject(
	ctx context.Context,
	repository GenerationGarbageRepository,
	key string,
	requests *int,
) (generationIdentity, bool, error) {
	parts := strings.Split(key, "/")
	switch {
	case len(parts) >= 2 && parts[0] == "objects" && parts[1] == "erasure-ledger":
		return generationIdentity{}, false, nil
	case len(parts) >= 5 && parts[0] == "objects":
		// Materialized baseline objects are
		// objects/<Generation>/<attempt>/<HashSlot>/<object>.
		return c.pathGenerationIdentity(parts[1], parts[3])
	case len(parts) == 3 && parts[0] == "partition-manifests" && strings.HasSuffix(parts[2], ".json"):
		return c.pathGenerationIdentity(parts[1], strings.TrimSuffix(parts[2], ".json"))
	case len(parts) == 3 && parts[0] == "segments" && parts[2] == "commit.json":
		commit, err := c.loadGenerationSegmentCommit(ctx, repository, key, parts[1], requests)
		if err != nil {
			return generationIdentity{}, false, fmt.Errorf("backup generation GC: authenticate %s commit %q: %w", repository.Name(), key, err)
		}
		return c.commitGenerationIdentity(commit)
	case len(parts) == 4 && parts[0] == "segments" && parts[2] == "payloads":
		commitKey := "segments/" + parts[1] + "/commit.json"
		commit, err := c.loadGenerationSegmentCommit(ctx, repository, commitKey, parts[1], requests)
		if errors.Is(err, backupartifact.ErrObjectNotFound) {
			// A payload without a commit in this repository is an unreachable
			// partial attempt for this independently swept copy.
			// The safety cutoff protects in-flight attempts without retaining
			// an unbounded Controller object list.
			return generationIdentity{}, true, nil
		}
		if err != nil {
			return generationIdentity{}, false, fmt.Errorf("backup generation GC: authenticate payload owner %q: %w", key, err)
		}
		if commit.Payload.Key != key {
			// Retrying identical logical segment content can leave encrypted
			// payload attempts under the same SegmentID. The one signed commit
			// owns exactly one payload key; every other old key is unreachable.
			return generationIdentity{}, true, nil
		}
		return c.commitGenerationIdentity(commit)
	default:
		return generationIdentity{}, false, nil
	}
}

func (c *GenerationGarbageCollector) pathGenerationIdentity(
	generation, slotText string,
) (generationIdentity, bool, error) {
	slot, err := strconv.ParseUint(slotText, 10, 16)
	if err != nil || slot >= uint64(c.hashSlotCount) {
		return generationIdentity{}, false, fmt.Errorf("backup generation GC: invalid Generation object path")
	}
	prefix := fmt.Sprintf("rebase-%05d-", slot)
	if !strings.HasPrefix(generation, prefix) || len(generation) != len(prefix)+20 {
		// Legacy restore-point JobIDs share the outer objects and manifest
		// prefixes. Only the runtime's exact materialized-Generation identity is
		// owned by this collector.
		return generationIdentity{}, false, nil
	}
	epoch, err := strconv.ParseUint(strings.TrimPrefix(generation, prefix), 10, 64)
	if err != nil || epoch == 0 {
		return generationIdentity{}, false, fmt.Errorf("backup generation GC: invalid materialized Generation identity")
	}
	return generationIdentity{hashSlot: uint16(slot), generation: generation}, true, nil
}

func (c *GenerationGarbageCollector) loadGenerationSegmentCommit(
	ctx context.Context,
	repository GenerationGarbageRepository,
	key, segmentID string,
	requests *int,
) (backupartifact.SegmentCommit, error) {
	if requests == nil || *requests == 0 {
		return backupartifact.SegmentCommit{}, errGenerationGCRequestBudget
	}
	*requests--
	reader, object, err := repository.Open(ctx, key)
	if err != nil {
		return backupartifact.SegmentCommit{}, err
	}
	body, readErr := io.ReadAll(io.LimitReader(reader, maxGenerationGCCommitBytes+1))
	closeErr := reader.Close()
	if readErr != nil {
		return backupartifact.SegmentCommit{}, readErr
	}
	if closeErr != nil {
		return backupartifact.SegmentCommit{}, closeErr
	}
	hash := sha256.Sum256(body)
	checksum := hex.EncodeToString(hash[:])
	if len(body) == 0 || len(body) > maxGenerationGCCommitBytes ||
		object.Key != key || object.Size != int64(len(body)) || object.SHA256 != checksum {
		return backupartifact.SegmentCommit{}, backupartifact.ErrObjectCorrupt
	}
	commit, err := backupartifact.LoadSegmentCommit(ctx, body, c.signer)
	if err != nil {
		return backupartifact.SegmentCommit{}, err
	}
	if commit.SegmentID != segmentID ||
		commit.PrimaryRepository != c.primary.Name() ||
		commit.SecondaryRepository != c.secondary.Name() {
		return backupartifact.SegmentCommit{}, backupartifact.ErrObjectCorrupt
	}
	return commit, nil
}

func (c *GenerationGarbageCollector) commitGenerationIdentity(
	commit backupartifact.SegmentCommit,
) (generationIdentity, bool, error) {
	logical := commit.Header.Logical
	if logical.HashSlot >= c.hashSlotCount ||
		!validControllerCaptureGeneration(logical.Generation) {
		return generationIdentity{}, false, backupartifact.ErrObjectCorrupt
	}
	return generationIdentity{hashSlot: logical.HashSlot, generation: logical.Generation}, true, nil
}
