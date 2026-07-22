package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"

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

// Load verifies an existing manifest, repairs a missing replica with the
// original immutable bytes, and verifies every directly referenced object.
func (s *ReplicatedManifestStore) Load(ctx context.Context, key string) ([]byte, string, error) {
	if s == nil || key == "" {
		return nil, "", fmt.Errorf("%w: invalid partition manifest key", backupartifact.ErrInvalidManifest)
	}
	primaryBody, primaryChecksum, primaryFound, err := loadReplicatedPartitionManifestCopy(ctx, s.primary, key)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %s partition manifest: %v", backupartifact.ErrRepositoryIncomplete, s.primary.Name(), err)
	}
	secondaryBody, secondaryChecksum, secondaryFound, err := loadReplicatedPartitionManifestCopy(ctx, s.secondary, key)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %s partition manifest: %v", backupartifact.ErrRepositoryIncomplete, s.secondary.Name(), err)
	}
	if !primaryFound && !secondaryFound {
		return nil, "", backupartifact.ErrObjectNotFound
	}
	body, checksum := primaryBody, primaryChecksum
	if !primaryFound {
		body, checksum = secondaryBody, secondaryChecksum
	}
	if primaryFound && secondaryFound && (!bytes.Equal(primaryBody, secondaryBody) || primaryChecksum != secondaryChecksum) {
		return nil, "", fmt.Errorf("%w: partition manifest replicas disagree", backupartifact.ErrObjectCorrupt)
	}
	if !secondaryFound {
		if err := putManifestAndVerify(ctx, s.secondary, key, checksum, body); err != nil {
			return nil, "", fmt.Errorf("%w: %s partition manifest repair: %v", backupartifact.ErrRepositoryIncomplete, s.secondary.Name(), err)
		}
	}
	if !primaryFound {
		if err := putManifestAndVerify(ctx, s.primary, key, checksum, body); err != nil {
			return nil, "", fmt.Errorf("%w: %s partition manifest repair: %v", backupartifact.ErrRepositoryIncomplete, s.primary.Name(), err)
		}
	}
	manifest, err := backupartifact.LoadPartitionManifest(body)
	if err != nil {
		return nil, "", err
	}
	if err := verifyPartitionManifestObjects(ctx, s.primary, manifest); err != nil {
		return nil, "", fmt.Errorf("%w: %s partition objects: %v", backupartifact.ErrRepositoryIncomplete, s.primary.Name(), err)
	}
	if err := verifyPartitionManifestObjects(ctx, s.secondary, manifest); err != nil {
		return nil, "", fmt.Errorf("%w: %s partition objects: %v", backupartifact.ErrRepositoryIncomplete, s.secondary.Name(), err)
	}
	return body, checksum, nil
}

func loadReplicatedPartitionManifestCopy(ctx context.Context, repository backupartifact.Repository, key string) ([]byte, string, bool, error) {
	reader, object, err := repository.Open(ctx, key)
	if errors.Is(err, backupartifact.ErrObjectNotFound) {
		return nil, "", false, nil
	}
	if err != nil {
		return nil, "", false, err
	}
	defer reader.Close()
	body, err := io.ReadAll(io.LimitReader(reader, maxReplicatedPartitionManifestBytes+1))
	if err != nil {
		return nil, "", false, err
	}
	hash := sha256.Sum256(body)
	checksum := hex.EncodeToString(hash[:])
	if len(body) == 0 || len(body) > maxReplicatedPartitionManifestBytes || object.Key != key || object.Size != int64(len(body)) || object.SHA256 != checksum {
		return nil, "", false, backupartifact.ErrObjectCorrupt
	}
	if _, err := backupartifact.LoadPartitionManifest(body); err != nil {
		return nil, "", false, err
	}
	return body, checksum, true, nil
}

func verifyPartitionManifestObjects(ctx context.Context, repository backupartifact.Repository, manifest backupartifact.PartitionManifest) error {
	for _, object := range manifest.Objects {
		stored, err := repository.Stat(ctx, object.Key)
		if err != nil {
			return err
		}
		if stored.Key != object.Key || stored.Size != object.CiphertextBytes || stored.SHA256 != object.CiphertextSHA256 {
			return backupartifact.ErrObjectCorrupt
		}
	}
	if manifest.Base != nil {
		stored, err := repository.Stat(ctx, manifest.Base.Key)
		if err != nil {
			return err
		}
		if stored.Key != manifest.Base.Key || stored.Size != manifest.Base.Bytes || stored.SHA256 != manifest.Base.SHA256 {
			return backupartifact.ErrObjectCorrupt
		}
	}
	return nil
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
