package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
)

// RestoreInstallNode owns restore-only local database installation methods.
type RestoreInstallNode interface {
	InstallRestoreHashSlotMetadata(context.Context, uint16, io.ReadSeeker, int64, bool) (uint64, error)
	InstallRestoreMessageStream(context.Context, io.ReadSeeker, int64) (channelstore.BackupSnapshotStats, error)
	InstallRestoreChannelRuntimeMeta(context.Context, uint16, []clusterpkg.RestoreVerifyBoundary) error
	RestoreHashSlotMetadataDigest(context.Context, uint16) (string, error)
}

// LocalRestoreInstallerOptions configures one node's repository-driven installer.
type LocalRestoreInstallerOptions struct {
	Primary         backupartifact.Repository
	Secondary       backupartifact.Repository
	Signer          backupartifact.ManifestSigner
	Codec           *backupartifact.ObjectCodec
	Node            RestoreInstallNode
	StagingDir      string
	StagingMaxBytes uint64
}

// LocalRestoreInstaller reconstructs one logical partition on one restore-mode
// node using bounded encrypted chunks and seekable local staging files.
type LocalRestoreInstaller struct {
	primary         backupartifact.Repository
	secondary       backupartifact.Repository
	signer          backupartifact.ManifestSigner
	codec           *backupartifact.ObjectCodec
	node            RestoreInstallNode
	stagingDir      string
	stagingMaxBytes uint64

	mu        sync.Mutex
	manifests map[string]backupartifact.Manifest

	stagingMu    sync.Mutex
	stagingBytes uint64
}

// NewLocalRestoreInstaller creates a fail-closed local partition installer.
func NewLocalRestoreInstaller(options LocalRestoreInstallerOptions) (*LocalRestoreInstaller, error) {
	if options.Primary == nil || options.Secondary == nil || options.Signer == nil || options.Codec == nil || options.Node == nil ||
		options.Primary.Name() == "" || options.Secondary.Name() == "" || options.Primary.Name() == options.Secondary.Name() ||
		strings.TrimSpace(options.StagingDir) == "" || options.StagingMaxBytes == 0 {
		return nil, fmt.Errorf("backup local restore installer: invalid options")
	}
	absolute, err := filepath.Abs(options.StagingDir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(absolute, 0o750); err != nil {
		return nil, err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, err
	}
	return &LocalRestoreInstaller{
		primary: options.Primary, secondary: options.Secondary, signer: options.Signer, codec: options.Codec,
		node: options.Node, stagingDir: resolved, stagingMaxBytes: options.StagingMaxBytes,
		manifests: make(map[string]backupartifact.Manifest),
	}, nil
}

// InstallPartition installs the latest full semantic metadata view and every
// base-to-tip committed message delta for one hash slot.
func (i *LocalRestoreInstaller) InstallPartition(ctx context.Context, plan backupusecase.RestorePlan, hashSlot uint16) (backupusecase.RestorePartition, error) {
	if i == nil || plan.ID == "" || plan.RestorePointID == "" || hashSlot >= plan.HashSlotCount ||
		(plan.Repository != "primary" && plan.Repository != "secondary") {
		return backupusecase.RestorePartition{}, backupusecase.ErrInvalidRequest
	}
	repository := i.repository(plan.Repository)
	manifest, err := i.loadPlanManifest(ctx, repository, plan)
	if err != nil {
		return backupusecase.RestorePartition{}, err
	}
	layers, err := loadRestorePartitionLayers(ctx, repository, manifest.Partitions[hashSlot])
	if err != nil {
		return backupusecase.RestorePartition{}, err
	}
	boundaries, err := loadRestoreExpectedBoundariesFromLayers(ctx, repository, i.codec, manifest.Partitions[hashSlot].HashSlot, layers)
	if err != nil {
		return backupusecase.RestorePartition{}, err
	}
	result := backupusecase.RestorePartition{HashSlot: hashSlot, EvidenceVersion: backupartifact.PartitionEvidenceVersion}
	metadataGroups, err := restoreObjectGroups(layers[len(layers)-1].Objects, backupartifact.ObjectKindMetadata)
	if err != nil || len(metadataGroups) != 1 || metadataGroups[0].Name != string(backupartifact.ObjectKindMetadata) {
		return backupusecase.RestorePartition{}, fmt.Errorf("%w: latest partition metadata stream is invalid", backupartifact.ErrInvalidManifest)
	}
	metadataBytes, err := i.withStagedStream(ctx, repository, metadataGroups[0].Objects, func(file *os.File, size int64) error {
		var installErr error
		result.MetadataRecordCount, installErr = i.node.InstallRestoreHashSlotMetadata(ctx, hashSlot, file, size, plan.InvalidateTokens)
		return installErr
	})
	if err != nil {
		return backupusecase.RestorePartition{}, err
	}
	result.PlainBytes = metadataBytes
	for _, layer := range layers {
		groups, err := restoreObjectGroups(layer.Objects, backupartifact.ObjectKindMessages)
		if err != nil {
			return backupusecase.RestorePartition{}, err
		}
		for _, group := range groups {
			plainBytes, err := i.withStagedStream(ctx, repository, group.Objects, func(file *os.File, size int64) error {
				stats, err := i.node.InstallRestoreMessageStream(ctx, file, size)
				if err == nil {
					if stats.HashSlot != hashSlot {
						return fmt.Errorf("%w: restored message hash slot mismatch", backupartifact.ErrInvalidManifest)
					}
					if math.MaxUint64-result.MessageCount < stats.MessageCount {
						return fmt.Errorf("%w: restored message count overflow", backupartifact.ErrInvalidManifest)
					}
					result.MessageCount += stats.MessageCount
					if stats.MaxMessageID > result.MaxMessageID {
						result.MaxMessageID = stats.MaxMessageID
					}
				}
				return err
			})
			if err != nil {
				return backupusecase.RestorePartition{}, err
			}
			if math.MaxUint64-result.PlainBytes < plainBytes {
				return backupusecase.RestorePartition{}, fmt.Errorf("%w: restored byte count overflow", backupartifact.ErrInvalidManifest)
			}
			result.PlainBytes += plainBytes
		}
	}
	for start := 0; start < len(boundaries); start += maxRestoreVerifyBoundariesPerRequest {
		end := start + maxRestoreVerifyBoundariesPerRequest
		if end > len(boundaries) {
			end = len(boundaries)
		}
		if err := i.node.InstallRestoreChannelRuntimeMeta(ctx, hashSlot, boundaries[start:end]); err != nil {
			return backupusecase.RestorePartition{}, fmt.Errorf("backup restore: install target channel runtime metadata: %w", err)
		}
	}
	result.MetadataSHA256, err = i.node.RestoreHashSlotMetadataDigest(ctx, hashSlot)
	if err != nil {
		return backupusecase.RestorePartition{}, fmt.Errorf("backup restore: read installed metadata digest: %w", err)
	}
	if !validLowerSHA256(result.MetadataSHA256) {
		return backupusecase.RestorePartition{}, fmt.Errorf("backup restore: installed metadata digest is invalid")
	}
	result.Installed = true
	return result, nil
}

func validLowerSHA256(value string) bool {
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func (i *LocalRestoreInstaller) repository(name string) backupartifact.Repository {
	if name == "secondary" {
		return i.secondary
	}
	return i.primary
}

func (i *LocalRestoreInstaller) loadPlanManifest(ctx context.Context, repository backupartifact.Repository, plan backupusecase.RestorePlan) (backupartifact.Manifest, error) {
	cacheKey := repository.Name() + ":" + plan.RestorePointID + ":" + plan.ManifestSHA256
	i.mu.Lock()
	manifest, ok := i.manifests[cacheKey]
	i.mu.Unlock()
	if ok {
		return manifest, nil
	}
	manifest, err := backupartifact.LoadRestorePoint(ctx, repository, plan.RestorePointID, i.signer)
	if err != nil {
		return backupartifact.Manifest{}, err
	}
	body, err := backupartifact.MarshalManifest(manifest)
	if err != nil {
		return backupartifact.Manifest{}, err
	}
	hash := sha256.Sum256(body)
	if hex.EncodeToString(hash[:]) != plan.ManifestSHA256 || manifest.SourceClusterID != plan.SourceClusterID ||
		manifest.SourceGeneration != plan.SourceGeneration || manifest.HashSlotCount != plan.HashSlotCount ||
		len(manifest.Partitions) != int(plan.HashSlotCount) {
		return backupartifact.Manifest{}, fmt.Errorf("%w: restore plan manifest fence mismatch", backupartifact.ErrInvalidManifest)
	}
	i.mu.Lock()
	i.manifests[cacheKey] = manifest
	i.mu.Unlock()
	return manifest, nil
}

func loadRestorePartitionLayers(ctx context.Context, repository backupartifact.Repository, tip backupartifact.PartitionReference) ([]backupartifact.PartitionManifest, error) {
	reversed := make([]backupartifact.PartitionManifest, 0, 8)
	seen := make(map[string]struct{})
	reference := &tip
	for depth := 0; reference != nil; depth++ {
		if depth >= maxEstimatedPartitionChainDepth {
			return nil, fmt.Errorf("%w: restore partition chain exceeds limit", backupartifact.ErrInvalidManifest)
		}
		identity := reference.Key + ":" + reference.SHA256
		if _, exists := seen[identity]; exists {
			return nil, fmt.Errorf("%w: restore partition chain contains a cycle", backupartifact.ErrInvalidManifest)
		}
		seen[identity] = struct{}{}
		body, err := readRepositoryObject(ctx, repository, reference.Key, reference.Bytes, reference.SHA256)
		if err != nil {
			return nil, err
		}
		layer, err := backupartifact.LoadPartitionManifest(body)
		if err != nil {
			return nil, err
		}
		if layer.Cut.HashSlot != tip.HashSlot || uint64(len(layer.Objects)) != reference.ObjectCount || layer.Evidence != reference.Evidence {
			return nil, fmt.Errorf("%w: restore partition layer summary mismatch", backupartifact.ErrInvalidManifest)
		}
		reversed = append(reversed, layer)
		reference = layer.Base
	}
	layers := make([]backupartifact.PartitionManifest, len(reversed))
	for index := range reversed {
		layers[len(reversed)-1-index] = reversed[index]
	}
	return layers, nil
}

type restoreObjectGroup struct {
	Name    string
	Objects []backupartifact.ObjectEntry
}

func restoreObjectGroups(objects []backupartifact.ObjectEntry, kind backupartifact.ObjectKind) ([]restoreObjectGroup, error) {
	groups := make(map[string]map[int]backupartifact.ObjectEntry)
	for _, object := range objects {
		if object.Kind != kind {
			continue
		}
		name, ordinal, err := parseRestoreObjectStreamKey(object.Key)
		if err != nil || (kind == backupartifact.ObjectKindMessages && name != string(kind) && !strings.HasPrefix(name, string(kind)+"-")) {
			return nil, fmt.Errorf("%w: invalid %s stream key %q", backupartifact.ErrInvalidManifest, kind, object.Key)
		}
		if groups[name] == nil {
			groups[name] = make(map[int]backupartifact.ObjectEntry)
		}
		if _, exists := groups[name][ordinal]; exists {
			return nil, fmt.Errorf("%w: duplicate stream ordinal", backupartifact.ErrInvalidManifest)
		}
		groups[name][ordinal] = object
	}
	names := make([]string, 0, len(groups))
	for name := range groups {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]restoreObjectGroup, 0, len(names))
	for _, name := range names {
		ordinals := groups[name]
		entries := make([]backupartifact.ObjectEntry, len(ordinals))
		for ordinal := range entries {
			entry, exists := ordinals[ordinal]
			if !exists {
				return nil, fmt.Errorf("%w: non-contiguous stream ordinals", backupartifact.ErrInvalidManifest)
			}
			entries[ordinal] = entry
		}
		result = append(result, restoreObjectGroup{Name: name, Objects: entries})
	}
	return result, nil
}

func parseRestoreObjectStreamKey(key string) (string, int, error) {
	parts := strings.Split(key, "/")
	// Four components are retained for restore compatibility with objects
	// created before attempts were nested below an explicit job prefix. New
	// objects use objects/<job>/<attempt>/<slot>/<stream>-<ordinal>.bin so GC
	// can protect every in-flight object with one job-scoped prefix.
	if (len(parts) != 4 && len(parts) != 5) || parts[0] != "objects" {
		return "", 0, fmt.Errorf("invalid object key")
	}
	filename := parts[len(parts)-1]
	if len(filename) < 12 || !strings.HasSuffix(filename, ".bin") || filename[len(filename)-11] != '-' {
		return "", 0, fmt.Errorf("invalid stream filename")
	}
	ordinal, err := strconv.Atoi(filename[len(filename)-10 : len(filename)-4])
	if err != nil || ordinal < 0 || ordinal >= maxBackupChunksPerStream {
		return "", 0, fmt.Errorf("invalid stream ordinal")
	}
	name := filename[:len(filename)-11]
	if name == "" {
		return "", 0, fmt.Errorf("missing stream name")
	}
	return name, ordinal, nil
}

func (i *LocalRestoreInstaller) withStagedStream(ctx context.Context, repository backupartifact.Repository, objects []backupartifact.ObjectEntry, install func(*os.File, int64) error) (uint64, error) {
	if len(objects) == 0 {
		return 0, fmt.Errorf("%w: restore object stream is empty", backupartifact.ErrInvalidManifest)
	}
	file, err := os.CreateTemp(i.stagingDir, ".restore-stream-*")
	if err != nil {
		return 0, err
	}
	name := file.Name()
	defer os.Remove(name)
	defer file.Close()
	var total uint64
	defer func() { i.releaseStagingBytes(total) }()
	for _, object := range objects {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		ciphertext, err := readRepositoryObject(ctx, repository, object.Key, object.CiphertextBytes, object.CiphertextSHA256)
		if err != nil {
			return 0, err
		}
		plaintext, err := i.codec.Open(ctx, object, ciphertext)
		if err != nil {
			return 0, err
		}
		plainBytes := uint64(len(plaintext))
		if plainBytes > i.stagingMaxBytes-total {
			return 0, fmt.Errorf("backup restore: staged stream exceeds configured limit")
		}
		if !i.reserveStagingBytes(plainBytes) {
			return 0, fmt.Errorf("backup restore: node staging usage exceeds configured limit")
		}
		total += plainBytes
		if _, err := file.Write(plaintext); err != nil {
			return 0, err
		}
	}
	if total > math.MaxInt64 {
		return 0, fmt.Errorf("backup restore: staged stream size overflow")
	}
	if err := file.Sync(); err != nil {
		return 0, err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return 0, err
	}
	if err := install(file, int64(total)); err != nil {
		return 0, err
	}
	return total, nil
}

func (i *LocalRestoreInstaller) reserveStagingBytes(size uint64) bool {
	i.stagingMu.Lock()
	defer i.stagingMu.Unlock()
	if size > i.stagingMaxBytes-i.stagingBytes {
		return false
	}
	i.stagingBytes += size
	return true
}

func (i *LocalRestoreInstaller) releaseStagingBytes(size uint64) {
	i.stagingMu.Lock()
	defer i.stagingMu.Unlock()
	if size > i.stagingBytes {
		panic("backup restore: staging byte accounting underflow")
	}
	i.stagingBytes -= size
}
