package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
)

const maxManifestBytes = 16 << 20

// RepositoryObject describes one immutable object stored in a backup repository.
type RepositoryObject struct {
	// Key is the repository-relative immutable object key.
	Key string
	// Size is the stored object size in bytes.
	Size int64
	// SHA256 is the lowercase checksum of stored bytes.
	SHA256 string
}

// Repository is the provider-neutral immutable backup object boundary.
type Repository interface {
	// Name returns a bounded operator-facing repository name.
	Name() string
	// PutImmutable creates key without replacing an existing object.
	PutImmutable(
		ctx context.Context,
		key string,
		size int64,
		checksum string,
		body io.Reader,
	) error
	// Open returns a streaming reader and trusted provider metadata for key.
	Open(
		ctx context.Context,
		key string,
	) (io.ReadCloser, RepositoryObject, error)
	// Stat returns provider metadata without opening the object body.
	Stat(ctx context.Context, key string) (RepositoryObject, error)
}

// RepairRepository can publish a new current version for one corrupted key.
// Production implementations retain older Object-Locked versions; ordinary
// upload credentials never receive this capability implicitly.
type RepairRepository interface {
	Repository
	RepairImmutable(
		ctx context.Context,
		key string,
		size int64,
		checksum string,
		body io.Reader,
	) error
}

// ReplicatedPublisher writes immutable payload objects to two explicit failure
// domains. Checkpoints, catalogs, and segments own their publication protocols;
// this helper has no checkpoint-catalog publication behavior.
type ReplicatedPublisher struct {
	primary   Repository
	secondary Repository
}

// NewReplicatedPublisher creates a payload writer for two distinct repositories.
func NewReplicatedPublisher(
	primary,
	secondary Repository,
) *ReplicatedPublisher {
	return &ReplicatedPublisher{primary: primary, secondary: secondary}
}

// ReplicateObject writes and verifies one sealed immutable payload in both
// repositories.
func (p *ReplicatedPublisher) ReplicateObject(
	ctx context.Context,
	object SealedObject,
) error {
	if err := p.validateRepositories(); err != nil {
		return err
	}
	if err := validateObjectEntry(object.Entry, 0); err != nil {
		return err
	}
	if err := validateSealedObject(object); err != nil {
		return err
	}
	if err := putAndVerify(
		ctx, p.primary, object.Entry.Key,
		object.Entry.CiphertextSHA256, object.Ciphertext,
	); err != nil {
		return fmt.Errorf(
			"%w: %s object %q: %v",
			ErrRepositoryIncomplete, p.primary.Name(), object.Entry.Key, err,
		)
	}
	if err := putAndVerify(
		ctx, p.secondary, object.Entry.Key,
		object.Entry.CiphertextSHA256, object.Ciphertext,
	); err != nil {
		return fmt.Errorf(
			"%w: %s object %q: %v",
			ErrRepositoryIncomplete, p.secondary.Name(), object.Entry.Key, err,
		)
	}
	return nil
}

func (p *ReplicatedPublisher) validateRepositories() error {
	if p == nil || p.primary == nil || p.secondary == nil {
		return fmt.Errorf(
			"%w: primary and secondary repositories are required",
			ErrRepositoryIncomplete,
		)
	}
	if p.primary.Name() == "" || p.secondary.Name() == "" ||
		p.primary.Name() == p.secondary.Name() {
		return fmt.Errorf(
			"%w: repositories must have distinct names",
			ErrRepositoryIncomplete,
		)
	}
	return nil
}

func validateSealedObject(object SealedObject) error {
	if int64(len(object.Ciphertext)) != object.Entry.CiphertextBytes {
		return fmt.Errorf("%w: ciphertext size mismatch", ErrObjectCorrupt)
	}
	hash := sha256.Sum256(object.Ciphertext)
	if hex.EncodeToString(hash[:]) != object.Entry.CiphertextSHA256 {
		return fmt.Errorf("%w: ciphertext checksum mismatch", ErrObjectCorrupt)
	}
	return nil
}

func putAndVerify(
	ctx context.Context,
	repository Repository,
	key,
	checksum string,
	body []byte,
) error {
	err := repository.PutImmutable(
		ctx, key, int64(len(body)), checksum, bytes.NewReader(body),
	)
	if err != nil && !errors.Is(err, ErrObjectExists) {
		return err
	}
	object, err := repository.Stat(ctx, key)
	if err != nil {
		return err
	}
	if object.Key != key || object.Size != int64(len(body)) ||
		object.SHA256 != checksum {
		return fmt.Errorf("repository verification mismatch")
	}
	return nil
}
