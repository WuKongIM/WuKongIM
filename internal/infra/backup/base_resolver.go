package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"

	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

const maxLoadedChannelIndexBytes = 256 << 20

// BaseResolverOptions configures signed incremental-base resolution.
type BaseResolverOptions struct {
	Repository       backupartifact.Repository
	Signer           backupartifact.ManifestSigner
	Codec            *backupartifact.ObjectCodec
	RepositoryID     string
	SourceClusterID  string
	SourceGeneration string
	HashSlotCount    uint16
}

// BaseResolver loads per-channel committed cuts from encrypted repository
// indexes while keeping the Controller state bounded.
type BaseResolver struct {
	repository       backupartifact.Repository
	signer           backupartifact.ManifestSigner
	codec            *backupartifact.ObjectCodec
	repositoryID     string
	sourceClusterID  string
	sourceGeneration string
	hashSlotCount    uint16

	mu                   sync.Mutex
	cachedRestorePointID string
	cachedRestorePoint   backupartifact.Manifest
}

// NewBaseResolver creates a signed base resolver.
func NewBaseResolver(options BaseResolverOptions) (*BaseResolver, error) {
	if options.Repository == nil || options.Signer == nil || options.Codec == nil ||
		strings.TrimSpace(options.RepositoryID) == "" || strings.TrimSpace(options.SourceClusterID) == "" ||
		strings.TrimSpace(options.SourceGeneration) == "" || options.HashSlotCount == 0 {
		return nil, fmt.Errorf("backup base resolver: invalid options")
	}
	return &BaseResolver{
		repository: options.Repository, signer: options.Signer, codec: options.Codec,
		repositoryID: options.RepositoryID, sourceClusterID: options.SourceClusterID,
		sourceGeneration: options.SourceGeneration, hashSlotCount: options.HashSlotCount,
	}, nil
}

// ResolvePartitionBase returns the authenticated prior partition reference and channel cuts.
func (r *BaseResolver) ResolvePartitionBase(ctx context.Context, restorePointID string, hashSlot uint16) (*backupartifact.PartitionReference, []backupartifact.ChannelBoundary, error) {
	if r == nil || strings.TrimSpace(restorePointID) == "" || hashSlot >= r.hashSlotCount {
		return nil, nil, fmt.Errorf("backup base resolver: invalid request")
	}
	manifest, err := r.loadManifest(ctx, restorePointID)
	if err != nil {
		return nil, nil, err
	}
	if manifest.RepositoryID != r.repositoryID || manifest.SourceClusterID != r.sourceClusterID || manifest.SourceGeneration != r.sourceGeneration || manifest.HashSlotCount != r.hashSlotCount {
		return nil, nil, fmt.Errorf("%w: incremental base identity mismatch", backupartifact.ErrInvalidManifest)
	}
	if len(manifest.Partitions) != int(r.hashSlotCount) {
		return nil, nil, fmt.Errorf("%w: incremental base has no partition index", backupartifact.ErrInvalidManifest)
	}
	reference := manifest.Partitions[hashSlot]
	partition, err := r.loadPartition(ctx, reference)
	if err != nil {
		return nil, nil, err
	}
	indexObjects := make([]backupartifact.ObjectEntry, 0)
	for _, object := range partition.Objects {
		if object.Kind == backupartifact.ObjectKindChannelIndex {
			indexObjects = append(indexObjects, object)
		}
	}
	if len(indexObjects) == 0 {
		return nil, nil, fmt.Errorf("%w: base partition channel index is missing", backupartifact.ErrInvalidManifest)
	}
	sort.Slice(indexObjects, func(i, j int) bool { return indexObjects[i].Key < indexObjects[j].Key })
	var plaintext bytes.Buffer
	for _, object := range indexObjects {
		if object.PlaintextBytes < 0 || int64(plaintext.Len())+object.PlaintextBytes > maxLoadedChannelIndexBytes {
			return nil, nil, fmt.Errorf("%w: base channel index exceeds limit", backupartifact.ErrInvalidManifest)
		}
		ciphertext, err := readRepositoryObject(ctx, r.repository, object.Key, object.CiphertextBytes, object.CiphertextSHA256)
		if err != nil {
			return nil, nil, err
		}
		chunk, err := r.codec.Open(ctx, object, ciphertext)
		if err != nil {
			return nil, nil, err
		}
		_, _ = plaintext.Write(chunk)
	}
	indexSlot, boundaries, err := backupartifact.LoadChannelIndex(plaintext.Bytes())
	if err != nil {
		return nil, nil, err
	}
	if indexSlot != hashSlot {
		return nil, nil, fmt.Errorf("%w: base channel index hash slot mismatch", backupartifact.ErrInvalidManifest)
	}
	copy := reference
	return &copy, boundaries, nil
}

func (r *BaseResolver) loadManifest(ctx context.Context, restorePointID string) (backupartifact.Manifest, error) {
	r.mu.Lock()
	manifest := r.cachedRestorePoint
	ok := r.cachedRestorePointID == restorePointID
	r.mu.Unlock()
	if ok {
		return manifest, nil
	}
	manifest, err := backupartifact.LoadRestorePoint(ctx, r.repository, restorePointID, r.signer)
	if err != nil {
		return backupartifact.Manifest{}, err
	}
	r.mu.Lock()
	r.cachedRestorePointID = restorePointID
	r.cachedRestorePoint = manifest
	r.mu.Unlock()
	return manifest, nil
}

func (r *BaseResolver) loadPartition(ctx context.Context, reference backupartifact.PartitionReference) (backupartifact.PartitionManifest, error) {
	body, err := readRepositoryObject(ctx, r.repository, reference.Key, reference.Bytes, reference.SHA256)
	if err != nil {
		return backupartifact.PartitionManifest{}, err
	}
	manifest, err := backupartifact.LoadPartitionManifest(body)
	if err != nil {
		return backupartifact.PartitionManifest{}, err
	}
	if manifest.Cut.HashSlot != reference.HashSlot || uint64(len(manifest.Objects)) != reference.ObjectCount || manifest.Evidence != reference.Evidence {
		return backupartifact.PartitionManifest{}, fmt.Errorf("%w: base partition summary mismatch", backupartifact.ErrInvalidManifest)
	}
	return manifest, nil
}

func readRepositoryObject(ctx context.Context, repository backupartifact.Repository, key string, size int64, checksum string) ([]byte, error) {
	if size <= 0 || size > maxLoadedChannelIndexBytes {
		return nil, fmt.Errorf("%w: repository object size is invalid", backupartifact.ErrRepositoryIncomplete)
	}
	reader, object, err := repository.Open(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("%w: repository object %q: %v", backupartifact.ErrRepositoryIncomplete, key, err)
	}
	body, readErr := io.ReadAll(io.LimitReader(reader, size+1))
	closeErr := reader.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	hash := sha256.Sum256(body)
	if int64(len(body)) != size || object.Key != key || object.Size != size || object.SHA256 != checksum || hex.EncodeToString(hash[:]) != checksum {
		return nil, fmt.Errorf("%w: repository object %q verification mismatch", backupartifact.ErrRepositoryIncomplete, key)
	}
	return body, nil
}

var _ PartitionBaseResolver = (*BaseResolver)(nil)
