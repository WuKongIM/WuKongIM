package backup

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

// GarbageRepository is the separately authorized, provider-neutral mark and
// sweep boundary. Upload-only repository credentials need not implement it.
type GarbageRepository interface {
	backupartifact.Repository
	WalkGarbageObjects(context.Context, time.Time, func(backupartifact.RepositoryObject) (bool, error)) error
	DeleteGarbageObject(context.Context, string) error
}

// RestorePointGarbageCollectorOptions configures dual-repository mark and sweep.
type RestorePointGarbageCollectorOptions struct {
	Primary                 GarbageRepository
	Secondary               GarbageRepository
	Signer                  backupartifact.ManifestSigner
	MinimumAge              time.Duration
	MaxDeletesPerRepository int
	Now                     func() time.Time
}

// GarbageCollectionResult is bounded completion evidence for one sweep.
type GarbageCollectionResult = backupusecase.GarbageCollectionResult

// RestorePointGarbageCollector authenticates every retained graph before it
// removes any old key not reachable from those graphs.
type RestorePointGarbageCollector struct {
	primary    GarbageRepository
	secondary  GarbageRepository
	signer     backupartifact.ManifestSigner
	minimumAge time.Duration
	maxDeletes int
	now        func() time.Time
}

// NewRestorePointGarbageCollector creates a fail-closed cross-region collector.
func NewRestorePointGarbageCollector(options RestorePointGarbageCollectorOptions) (*RestorePointGarbageCollector, error) {
	if options.Primary == nil || options.Secondary == nil || options.Signer == nil || options.Primary.Name() == "" ||
		options.Secondary.Name() == "" || options.Primary.Name() == options.Secondary.Name() || options.MinimumAge < 7*24*time.Hour {
		return nil, fmt.Errorf("backup garbage collector: invalid options")
	}
	if options.MaxDeletesPerRepository == 0 {
		options.MaxDeletesPerRepository = 256
	}
	if options.MaxDeletesPerRepository < 1 || options.MaxDeletesPerRepository > 4096 {
		return nil, fmt.Errorf("backup garbage collector: delete batch must be between 1 and 4096")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &RestorePointGarbageCollector{
		primary: options.Primary, secondary: options.Secondary, signer: options.Signer,
		minimumAge: options.MinimumAge, maxDeletes: options.MaxDeletesPerRepository, now: options.Now,
	}, nil
}

// Collect performs one bounded sweep and reports pending restore points whose
// discoverable manifests are absent from both repositories.
func (c *RestorePointGarbageCollector) Collect(ctx context.Context, retained, pending []backupusecase.RestorePoint, active *backupusecase.Job) (GarbageCollectionResult, error) {
	if c == nil {
		return GarbageCollectionResult{}, fmt.Errorf("backup garbage collector: collector is required")
	}
	marked := make(map[string]struct{})
	for _, point := range retained {
		primaryGraph, err := c.loadRetainedGraph(ctx, c.primary, point)
		if err != nil {
			return GarbageCollectionResult{}, err
		}
		secondaryGraph, err := c.loadRetainedGraph(ctx, c.secondary, point)
		if err != nil {
			return GarbageCollectionResult{}, err
		}
		if !slices.Equal(primaryGraph.Keys, secondaryGraph.Keys) {
			return GarbageCollectionResult{}, fmt.Errorf("backup garbage collector: retained graph %s differs across repositories", point.ID)
		}
		for _, key := range primaryGraph.Keys {
			marked[key] = struct{}{}
		}
	}
	protectedPrefixes := activeGarbagePrefixes(active)
	cutoff := c.now().UTC().Add(-c.minimumAge)
	deletedPrimary, err := c.sweepRepository(ctx, c.primary, cutoff, marked, protectedPrefixes)
	if err != nil {
		return GarbageCollectionResult{}, err
	}
	deletedSecondary, err := c.sweepRepository(ctx, c.secondary, cutoff, marked, protectedPrefixes)
	if err != nil {
		return GarbageCollectionResult{}, err
	}
	result := GarbageCollectionResult{DeletedObjects: deletedPrimary + deletedSecondary}
	for _, point := range pending {
		missingPrimary, err := garbagePublicationMissing(ctx, c.primary, point.ID)
		if err != nil {
			return GarbageCollectionResult{}, err
		}
		missingSecondary, err := garbagePublicationMissing(ctx, c.secondary, point.ID)
		if err != nil {
			return GarbageCollectionResult{}, err
		}
		if missingPrimary && missingSecondary {
			result.CompletedRestorePointIDs = append(result.CompletedRestorePointIDs, point.ID)
		}
	}
	return result, nil
}

func (c *RestorePointGarbageCollector) loadRetainedGraph(ctx context.Context, repository GarbageRepository, point backupusecase.RestorePoint) (backupartifact.RestorePointGraph, error) {
	graph, err := backupartifact.LoadRestorePointGraph(ctx, repository, point.ID, c.signer)
	if err != nil {
		return backupartifact.RestorePointGraph{}, fmt.Errorf("backup garbage collector: authenticate retained point %s in %s: %w", point.ID, repository.Name(), err)
	}
	manifestObject, err := repository.Stat(ctx, restorePointGarbageManifestKey(point.ID))
	if err != nil {
		return backupartifact.RestorePointGraph{}, err
	}
	if point.ManifestSHA256 == "" || manifestObject.SHA256 != point.ManifestSHA256 {
		return backupartifact.RestorePointGraph{}, fmt.Errorf("backup garbage collector: retained point %s state digest mismatch", point.ID)
	}
	return graph, nil
}

func (c *RestorePointGarbageCollector) sweepRepository(ctx context.Context, repository GarbageRepository, cutoff time.Time, marked map[string]struct{}, protectedPrefixes []string) (int, error) {
	keys := make([]string, 0, c.maxDeletes)
	err := repository.WalkGarbageObjects(ctx, cutoff, func(object backupartifact.RepositoryObject) (bool, error) {
		if _, retained := marked[object.Key]; retained || hasAnyPrefix(object.Key, protectedPrefixes) {
			return true, nil
		}
		keys = append(keys, object.Key)
		return len(keys) < c.maxDeletes, nil
	})
	if err != nil {
		return 0, err
	}
	for _, key := range keys {
		if err := repository.DeleteGarbageObject(ctx, key); err != nil {
			return 0, fmt.Errorf("backup garbage collector: delete %s object %q: %w", repository.Name(), key, err)
		}
	}
	return len(keys), nil
}

func activeGarbagePrefixes(active *backupusecase.Job) []string {
	if active == nil || strings.TrimSpace(active.ID) == "" {
		return nil
	}
	prefixes := []string{"objects/" + active.ID + "/", "partition-manifests/" + active.ID + "/"}
	if active.RestorePointID != "" {
		prefixes = append(prefixes, "restore-points/"+active.RestorePointID+"/")
	}
	return prefixes
}

func hasAnyPrefix(key string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

func garbagePublicationMissing(ctx context.Context, repository GarbageRepository, restorePointID string) (bool, error) {
	_, err := repository.Stat(ctx, restorePointGarbagePublicationKey(restorePointID))
	if errors.Is(err, backupartifact.ErrObjectNotFound) {
		return true, nil
	}
	return false, err
}

func restorePointGarbageManifestKey(restorePointID string) string {
	return "restore-points/" + restorePointID + "/manifest.json"
}

func restorePointGarbagePublicationKey(restorePointID string) string {
	return "restore-points/" + restorePointID + "/published.json"
}

var _ interface {
	Collect(context.Context, []backupusecase.RestorePoint, []backupusecase.RestorePoint, *backupusecase.Job) (GarbageCollectionResult, error)
} = (*RestorePointGarbageCollector)(nil)
