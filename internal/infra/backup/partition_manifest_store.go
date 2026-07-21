package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

const maxReplicatedPartitionManifestBytes = 16 << 20

// ReplicatedManifestStore writes small partition manifests to two explicit repositories.
type ReplicatedManifestStore struct {
	primary   backupartifact.Repository
	secondary backupartifact.Repository
}

// NewReplicatedManifestStore creates a two-repository partition manifest store.
func NewReplicatedManifestStore(primary, secondary backupartifact.Repository) (*ReplicatedManifestStore, error) {
	if primary == nil || secondary == nil || primary.Name() == "" || secondary.Name() == "" || primary.Name() == secondary.Name() {
		return nil, fmt.Errorf("%w: distinct partition manifest repositories are required", backupartifact.ErrRepositoryIncomplete)
	}
	return &ReplicatedManifestStore{primary: primary, secondary: secondary}, nil
}

// Put verifies and publishes one immutable partition manifest in both repositories.
func (s *ReplicatedManifestStore) Put(ctx context.Context, key, checksum string, body []byte) error {
	if s == nil || len(body) == 0 || len(body) > maxReplicatedPartitionManifestBytes || !validFileChecksum(checksum) {
		return fmt.Errorf("%w: invalid partition manifest object", backupartifact.ErrInvalidManifest)
	}
	hash := sha256.Sum256(body)
	if hex.EncodeToString(hash[:]) != checksum {
		return fmt.Errorf("%w: partition manifest checksum mismatch", backupartifact.ErrObjectCorrupt)
	}
	if err := putManifestAndVerify(ctx, s.secondary, key, checksum, body); err != nil {
		return fmt.Errorf("%w: %s partition manifest: %v", backupartifact.ErrRepositoryIncomplete, s.secondary.Name(), err)
	}
	if err := putManifestAndVerify(ctx, s.primary, key, checksum, body); err != nil {
		return fmt.Errorf("%w: %s partition manifest: %v", backupartifact.ErrRepositoryIncomplete, s.primary.Name(), err)
	}
	return nil
}

func putManifestAndVerify(ctx context.Context, repository backupartifact.Repository, key, checksum string, body []byte) error {
	err := repository.PutImmutable(ctx, key, int64(len(body)), checksum, bytes.NewReader(body))
	if err != nil && !errors.Is(err, backupartifact.ErrObjectExists) {
		return err
	}
	object, err := repository.Stat(ctx, key)
	if err != nil {
		return err
	}
	if object.Key != key || object.Size != int64(len(body)) || object.SHA256 != checksum {
		return fmt.Errorf("partition manifest verification mismatch")
	}
	return nil
}
