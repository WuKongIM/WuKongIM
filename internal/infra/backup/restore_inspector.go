package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strings"

	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

const (
	maxListedRestorePoints          = 1_000_000
	maxEstimatedPartitionChainDepth = 10_000
)

// RestorePointLister exposes only restore-point identities. Callers must load
// and authenticate each manifest before trusting its contents.
type RestorePointLister interface {
	ListRestorePointIDs(context.Context) ([]string, error)
}

// RestoreTargetState is authoritative identity and semantic-emptiness evidence
// for the cluster currently running in explicit restore mode.
type RestoreTargetState struct {
	ClusterID     string
	Generation    string
	HashSlotCount uint16
	Empty         bool
}

// RestoreTargetProbe inspects the real target storage before a plan is created.
type RestoreTargetProbe interface {
	InspectRestoreTarget(context.Context) (RestoreTargetState, error)
}

// RestoreInspectorOptions configures authenticated recovery-point selection.
type RestoreInspectorOptions struct {
	Primary      backupartifact.Repository
	Secondary    backupartifact.Repository
	Signer       backupartifact.ManifestSigner
	RepositoryID string
	Target       RestoreTargetProbe
}

// RestoreInspector selects a fully authenticated restore point and proves that
// its logical identity is compatible with an empty successor cluster.
type RestoreInspector struct {
	primary      backupartifact.Repository
	secondary    backupartifact.Repository
	signer       backupartifact.ManifestSigner
	repositoryID string
	target       RestoreTargetProbe
}

// NewRestoreInspector creates a repository and target preflight inspector.
func NewRestoreInspector(options RestoreInspectorOptions) (*RestoreInspector, error) {
	if options.Primary == nil || options.Secondary == nil || options.Signer == nil || options.Target == nil ||
		strings.TrimSpace(options.RepositoryID) == "" || options.Primary.Name() == "" || options.Secondary.Name() == "" ||
		options.Primary.Name() == options.Secondary.Name() {
		return nil, fmt.Errorf("backup restore inspector: invalid options")
	}
	return &RestoreInspector{
		primary: options.Primary, secondary: options.Secondary, signer: options.Signer,
		repositoryID: strings.TrimSpace(options.RepositoryID), target: options.Target,
	}, nil
}

// Inspect verifies the selected repository, or both copies for latest-verified,
// before returning immutable plan evidence.
func (i *RestoreInspector) Inspect(ctx context.Context, request backupusecase.RestorePlanRequest) (backupusecase.RestoreInspection, error) {
	if i == nil || (request.Repository != "primary" && request.Repository != "secondary") ||
		(strings.TrimSpace(request.RestorePointID) == "") == !request.LatestVerified {
		return backupusecase.RestoreInspection{}, backupusecase.ErrInvalidRequest
	}
	target, err := i.target.InspectRestoreTarget(ctx)
	if err != nil {
		return backupusecase.RestoreInspection{}, fmt.Errorf("backup restore inspector: inspect target: %w", err)
	}
	if strings.TrimSpace(target.ClusterID) == "" || strings.TrimSpace(target.Generation) == "" || target.HashSlotCount == 0 || !target.Empty {
		return backupusecase.RestoreInspection{}, fmt.Errorf("backup restore inspector: target is not a proven empty cluster")
	}
	repository := i.primary
	if request.Repository == "secondary" {
		repository = i.secondary
	}
	var manifest backupartifact.Manifest
	if request.LatestVerified {
		manifest, err = i.latestVerified(ctx)
	} else {
		manifest, err = backupartifact.LoadRestorePoint(ctx, repository, strings.TrimSpace(request.RestorePointID), i.signer)
	}
	if err != nil {
		return backupusecase.RestoreInspection{}, err
	}
	if manifest.RepositoryID != i.repositoryID || manifest.SourceClusterID == target.ClusterID || manifest.SourceGeneration == target.Generation || manifest.HashSlotCount != target.HashSlotCount {
		return backupusecase.RestoreInspection{}, fmt.Errorf("%w: restore point and target identity are incompatible", backupartifact.ErrInvalidManifest)
	}
	plainBytes, cipherBytes, err := estimateRestorePoint(ctx, repository, manifest)
	if err != nil {
		return backupusecase.RestoreInspection{}, err
	}
	body, err := backupartifact.MarshalManifest(manifest)
	if err != nil {
		return backupusecase.RestoreInspection{}, err
	}
	hash := sha256.Sum256(body)
	return backupusecase.RestoreInspection{
		RestorePointID: manifest.RestorePointID, ManifestSHA256: hex.EncodeToString(hash[:]),
		SourceClusterID: manifest.SourceClusterID, SourceGeneration: manifest.SourceGeneration,
		TargetClusterID: target.ClusterID, TargetGeneration: target.Generation, HashSlotCount: target.HashSlotCount,
		EstimatedPlainBytes: &plainBytes, EstimatedCipherBytes: &cipherBytes, TargetEmpty: true,
	}, nil
}

func (i *RestoreInspector) latestVerified(ctx context.Context) (backupartifact.Manifest, error) {
	primaryLister, primaryOK := i.primary.(RestorePointLister)
	secondaryLister, secondaryOK := i.secondary.(RestorePointLister)
	if !primaryOK || !secondaryOK {
		return backupartifact.Manifest{}, fmt.Errorf("backup restore inspector: latest-verified requires repository listing")
	}
	primaryIDs, err := primaryLister.ListRestorePointIDs(ctx)
	if err != nil {
		return backupartifact.Manifest{}, err
	}
	secondaryIDs, err := secondaryLister.ListRestorePointIDs(ctx)
	if err != nil {
		return backupartifact.Manifest{}, err
	}
	secondarySet := make(map[string]struct{}, len(secondaryIDs))
	for _, id := range secondaryIDs {
		secondarySet[id] = struct{}{}
	}
	common := make([]string, 0)
	for _, id := range primaryIDs {
		if _, ok := secondarySet[id]; ok {
			common = append(common, id)
		}
	}
	sort.Strings(common)
	var selected backupartifact.Manifest
	for _, id := range common {
		primary, primaryErr := backupartifact.LoadRestorePoint(ctx, i.primary, id, i.signer)
		secondary, secondaryErr := backupartifact.LoadRestorePoint(ctx, i.secondary, id, i.signer)
		if primaryErr != nil || secondaryErr != nil || !reflect.DeepEqual(primary, secondary) || primary.RepositoryID != i.repositoryID {
			continue
		}
		if selected.RestorePointID == "" || primary.EffectiveAtMillis > selected.EffectiveAtMillis ||
			(primary.EffectiveAtMillis == selected.EffectiveAtMillis && (primary.CreatedAtUnixMillis > selected.CreatedAtUnixMillis ||
				(primary.CreatedAtUnixMillis == selected.CreatedAtUnixMillis && primary.RestorePointID > selected.RestorePointID))) {
			selected = primary
		}
	}
	if selected.RestorePointID == "" {
		return backupartifact.Manifest{}, fmt.Errorf("%w: no restore point has identical verified copies", backupartifact.ErrRepositoryIncomplete)
	}
	return selected, nil
}

func estimateRestorePoint(ctx context.Context, repository backupartifact.Repository, manifest backupartifact.Manifest) (uint64, uint64, error) {
	seenObjects := make(map[string]backupartifact.ObjectEntry)
	seenPartitions := make(map[string]struct{})
	for _, object := range manifest.Objects {
		if err := addEstimatedObject(seenObjects, object); err != nil {
			return 0, 0, err
		}
	}
	for _, reference := range manifest.Partitions {
		if err := collectPartitionObjects(ctx, repository, reference, seenPartitions, seenObjects, 0); err != nil {
			return 0, 0, err
		}
	}
	var plainBytes uint64
	var cipherBytes uint64
	for _, object := range seenObjects {
		plain, cipher := uint64(object.PlaintextBytes), uint64(object.CiphertextBytes)
		if math.MaxUint64-plainBytes < plain || math.MaxUint64-cipherBytes < cipher {
			return 0, 0, fmt.Errorf("%w: restore size estimate overflow", backupartifact.ErrInvalidManifest)
		}
		plainBytes += plain
		cipherBytes += cipher
	}
	return plainBytes, cipherBytes, nil
}

func collectPartitionObjects(ctx context.Context, repository backupartifact.Repository, reference backupartifact.PartitionReference, seenPartitions map[string]struct{}, seenObjects map[string]backupartifact.ObjectEntry, depth int) error {
	if depth >= maxEstimatedPartitionChainDepth {
		return fmt.Errorf("%w: partition chain exceeds limit", backupartifact.ErrInvalidManifest)
	}
	identity := fmt.Sprintf("%d:%s:%s", reference.HashSlot, reference.Key, reference.SHA256)
	if _, ok := seenPartitions[identity]; ok {
		return nil
	}
	seenPartitions[identity] = struct{}{}
	body, err := readRepositoryObject(ctx, repository, reference.Key, reference.Bytes, reference.SHA256)
	if err != nil {
		return err
	}
	partition, err := backupartifact.LoadPartitionManifest(body)
	if err != nil {
		return err
	}
	for _, object := range partition.Objects {
		if err := addEstimatedObject(seenObjects, object); err != nil {
			return err
		}
	}
	if partition.Base != nil {
		return collectPartitionObjects(ctx, repository, *partition.Base, seenPartitions, seenObjects, depth+1)
	}
	return nil
}

func addEstimatedObject(objects map[string]backupartifact.ObjectEntry, object backupartifact.ObjectEntry) error {
	if existing, ok := objects[object.Key]; ok {
		if !reflect.DeepEqual(existing, object) {
			return fmt.Errorf("%w: object key %q has conflicting metadata", backupartifact.ErrInvalidManifest, object.Key)
		}
		return nil
	}
	objects[object.Key] = object
	return nil
}

func safeRestorePointID(value string) bool {
	if len(value) == 0 || len(value) > 128 || strings.Contains(value, "..") {
		return false
	}
	for index, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '-' || char == '_' || (char == '.' && index > 0) {
			continue
		}
		return false
	}
	return true
}

var _ backupusecase.RestoreInspector = (*RestoreInspector)(nil)
