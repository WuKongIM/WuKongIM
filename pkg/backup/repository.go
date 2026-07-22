package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sort"
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
	PutImmutable(ctx context.Context, key string, size int64, checksum string, body io.Reader) error
	// Open returns a streaming reader and trusted provider metadata for key.
	Open(ctx context.Context, key string) (io.ReadCloser, RepositoryObject, error)
	// Stat returns provider metadata without opening the object body.
	Stat(ctx context.Context, key string) (RepositoryObject, error)
}

// RestorePointGraph is one authenticated signed restore point and every
// immutable repository key reachable from it, including its top manifest.
type RestorePointGraph struct {
	Manifest Manifest
	Keys     []string
}

// ReplicatedPublisher writes complete restore points to explicit primary and secondary repositories.
type ReplicatedPublisher struct {
	primary   Repository
	secondary Repository
}

// NewReplicatedPublisher creates a publisher for two explicit failure-domain copies.
func NewReplicatedPublisher(primary, secondary Repository) *ReplicatedPublisher {
	return &ReplicatedPublisher{primary: primary, secondary: secondary}
}

// Publish uploads and verifies every object in both repositories before exposing a signed manifest.
func (p *ReplicatedPublisher) Publish(ctx context.Context, manifest Manifest, objects []SealedObject, signer ManifestSigner, signingKeyID string) (Manifest, error) {
	if err := p.validateRepositories(); err != nil {
		return Manifest{}, err
	}
	manifest.Signature = nil
	if err := validateManifest(manifest, false); err != nil {
		return Manifest{}, err
	}
	if len(objects) != len(manifest.Objects) {
		return Manifest{}, fmt.Errorf("%w: sealed objects=%d manifest objects=%d", ErrInvalidManifest, len(objects), len(manifest.Objects))
	}
	for index := range objects {
		if objects[index].Entry != manifest.Objects[index] {
			return Manifest{}, fmt.Errorf("%w: sealed object[%d] metadata mismatch", ErrInvalidManifest, index)
		}
		if err := p.ReplicateObject(ctx, objects[index]); err != nil {
			return Manifest{}, err
		}
	}
	return p.PublishReferences(ctx, manifest, signer, signingKeyID)
}

// ReplicateObject writes and verifies one sealed immutable object in both repositories.
func (p *ReplicatedPublisher) ReplicateObject(ctx context.Context, object SealedObject) error {
	if err := p.validateRepositories(); err != nil {
		return err
	}
	if err := validateObjectEntry(object.Entry, 0); err != nil {
		return err
	}
	if err := validateSealedObject(object); err != nil {
		return err
	}
	if err := putAndVerify(ctx, p.primary, object.Entry.Key, object.Entry.CiphertextSHA256, object.Ciphertext); err != nil {
		return fmt.Errorf("%w: %s object %q: %v", ErrRepositoryIncomplete, p.primary.Name(), object.Entry.Key, err)
	}
	if err := putAndVerify(ctx, p.secondary, object.Entry.Key, object.Entry.CiphertextSHA256, object.Ciphertext); err != nil {
		return fmt.Errorf("%w: %s object %q: %v", ErrRepositoryIncomplete, p.secondary.Name(), object.Entry.Key, err)
	}
	return nil
}

// PublishReferences verifies existing copies before exposing a signed top-level manifest.
func (p *ReplicatedPublisher) PublishReferences(ctx context.Context, manifest Manifest, signer ManifestSigner, signingKeyID string) (Manifest, error) {
	if err := p.validateRepositories(); err != nil {
		return Manifest{}, err
	}
	manifest.Signature = nil
	if err := validateManifest(manifest, false); err != nil {
		return Manifest{}, err
	}
	for _, entry := range manifest.Objects {
		if err := verifyRepositoryObject(ctx, p.primary, entry); err != nil {
			return Manifest{}, fmt.Errorf("%w: %s object %q: %v", ErrRepositoryIncomplete, p.primary.Name(), entry.Key, err)
		}
		if err := verifyRepositoryObject(ctx, p.secondary, entry); err != nil {
			return Manifest{}, fmt.Errorf("%w: %s object %q: %v", ErrRepositoryIncomplete, p.secondary.Name(), entry.Key, err)
		}
	}
	for _, reference := range manifest.Partitions {
		if err := verifyPartitionReference(ctx, p.primary, reference); err != nil {
			return Manifest{}, fmt.Errorf("%w: %s partition %q: %v", ErrRepositoryIncomplete, p.primary.Name(), reference.Key, err)
		}
		if err := verifyPartitionReference(ctx, p.secondary, reference); err != nil {
			return Manifest{}, fmt.Errorf("%w: %s partition %q: %v", ErrRepositoryIncomplete, p.secondary.Name(), reference.Key, err)
		}
	}
	key := restorePointManifestKey(manifest.RestorePointID)
	existingPrimaryBody, existingPrimary, primaryFound, err := loadPublishedManifest(ctx, p.primary, key, signer)
	if err != nil {
		return Manifest{}, fmt.Errorf("%w: %s manifest retry: %v", ErrRepositoryIncomplete, p.primary.Name(), err)
	}
	existingSecondaryBody, existingSecondary, secondaryFound, err := loadPublishedManifest(ctx, p.secondary, key, signer)
	if err != nil {
		return Manifest{}, fmt.Errorf("%w: %s manifest retry: %v", ErrRepositoryIncomplete, p.secondary.Name(), err)
	}
	if primaryFound || secondaryFound {
		existingBody := existingPrimaryBody
		existing := existingPrimary
		if !primaryFound {
			existingBody = existingSecondaryBody
			existing = existingSecondary
		}
		if primaryFound && secondaryFound && !bytes.Equal(existingPrimaryBody, existingSecondaryBody) {
			return Manifest{}, fmt.Errorf("%w: replicated manifests disagree", ErrRepositoryIncomplete)
		}
		candidate := manifest
		candidate.CreatedAtUnixMillis = existing.CreatedAtUnixMillis
		candidateCanonical, err := canonicalUnsignedManifest(candidate)
		if err != nil {
			return Manifest{}, err
		}
		existingCanonical, err := canonicalUnsignedManifest(existing)
		if err != nil {
			return Manifest{}, err
		}
		if !bytes.Equal(candidateCanonical, existingCanonical) {
			return Manifest{}, fmt.Errorf("%w: existing restore point manifest does not match retry", ErrInvalidManifest)
		}
		hash := sha256.Sum256(existingBody)
		checksum := hex.EncodeToString(hash[:])
		if !secondaryFound {
			if err := putAndVerify(ctx, p.secondary, key, checksum, existingBody); err != nil {
				return Manifest{}, fmt.Errorf("%w: %s manifest repair: %v", ErrRepositoryIncomplete, p.secondary.Name(), err)
			}
		}
		if !primaryFound {
			if err := putAndVerify(ctx, p.primary, key, checksum, existingBody); err != nil {
				return Manifest{}, fmt.Errorf("%w: %s manifest repair: %v", ErrRepositoryIncomplete, p.primary.Name(), err)
			}
		}
		return existing, nil
	}

	signed, err := SignManifest(ctx, manifest, signer, signingKeyID)
	if err != nil {
		return Manifest{}, err
	}
	body, err := MarshalManifest(signed)
	if err != nil {
		return Manifest{}, err
	}
	hash := sha256.Sum256(body)
	checksum := hex.EncodeToString(hash[:])
	if err := putAndVerify(ctx, p.secondary, key, checksum, body); err != nil {
		return Manifest{}, fmt.Errorf("%w: %s manifest: %v", ErrRepositoryIncomplete, p.secondary.Name(), err)
	}
	if err := putAndVerify(ctx, p.primary, key, checksum, body); err != nil {
		return Manifest{}, fmt.Errorf("%w: %s manifest: %v", ErrRepositoryIncomplete, p.primary.Name(), err)
	}
	return signed, nil
}

func loadPublishedManifest(ctx context.Context, repository Repository, key string, signer ManifestSigner) ([]byte, Manifest, bool, error) {
	reader, object, err := repository.Open(ctx, key)
	if errors.Is(err, ErrObjectNotFound) {
		return nil, Manifest{}, false, nil
	}
	if err != nil {
		return nil, Manifest{}, false, err
	}
	defer reader.Close()
	body, err := io.ReadAll(io.LimitReader(reader, maxManifestBytes+1))
	if err != nil {
		return nil, Manifest{}, false, err
	}
	if len(body) > maxManifestBytes || object.Key != key || object.Size != int64(len(body)) {
		return nil, Manifest{}, false, fmt.Errorf("manifest object metadata mismatch")
	}
	hash := sha256.Sum256(body)
	if object.SHA256 != hex.EncodeToString(hash[:]) {
		return nil, Manifest{}, false, fmt.Errorf("manifest checksum mismatch")
	}
	manifest, err := LoadManifest(ctx, body, signer)
	if err != nil {
		return nil, Manifest{}, false, err
	}
	return body, manifest, true, nil
}

func (p *ReplicatedPublisher) validateRepositories() error {
	if p == nil || p.primary == nil || p.secondary == nil {
		return fmt.Errorf("%w: primary and secondary repositories are required", ErrRepositoryIncomplete)
	}
	if p.primary.Name() == "" || p.secondary.Name() == "" || p.primary.Name() == p.secondary.Name() {
		return fmt.Errorf("%w: repositories must have distinct names", ErrRepositoryIncomplete)
	}
	return nil
}

func verifyRepositoryObject(ctx context.Context, repository Repository, entry ObjectEntry) error {
	stored, err := repository.Stat(ctx, entry.Key)
	if err != nil {
		return err
	}
	if stored.Key != entry.Key || stored.Size != entry.CiphertextBytes || stored.SHA256 != entry.CiphertextSHA256 {
		return fmt.Errorf("repository verification mismatch")
	}
	return nil
}

func verifyPartitionReference(ctx context.Context, repository Repository, reference PartitionReference) error {
	stored, err := repository.Stat(ctx, reference.Key)
	if err != nil {
		return err
	}
	if stored.Key != reference.Key || stored.Size != reference.Bytes || stored.SHA256 != reference.SHA256 {
		return fmt.Errorf("partition repository verification mismatch")
	}
	return nil
}

// LoadRestorePoint loads and verifies a signed manifest and every referenced object in repository.
func LoadRestorePoint(ctx context.Context, repository Repository, restorePointID string, signer ManifestSigner) (Manifest, error) {
	graph, err := LoadRestorePointGraph(ctx, repository, restorePointID, signer)
	return graph.Manifest, err
}

// LoadRestorePointGraph verifies one restore point and returns its complete,
// sorted immutable reference graph for fail-closed garbage collection.
func LoadRestorePointGraph(ctx context.Context, repository Repository, restorePointID string, signer ManifestSigner) (RestorePointGraph, error) {
	if repository == nil {
		return RestorePointGraph{}, fmt.Errorf("%w: repository is required", ErrRepositoryIncomplete)
	}
	if err := validateRestorePointID(restorePointID); err != nil {
		return RestorePointGraph{}, fmt.Errorf("%w: %v", ErrInvalidManifest, err)
	}
	key := restorePointManifestKey(restorePointID)
	reader, object, err := repository.Open(ctx, key)
	if err != nil {
		return RestorePointGraph{}, fmt.Errorf("%w: %s manifest: %v", ErrRepositoryIncomplete, repository.Name(), err)
	}
	defer reader.Close()
	body, err := io.ReadAll(io.LimitReader(reader, maxManifestBytes+1))
	if err != nil {
		return RestorePointGraph{}, fmt.Errorf("%w: read manifest: %v", ErrRepositoryIncomplete, err)
	}
	if len(body) > maxManifestBytes || object.Key != key || object.Size != int64(len(body)) {
		return RestorePointGraph{}, fmt.Errorf("%w: manifest object metadata mismatch", ErrRepositoryIncomplete)
	}
	hash := sha256.Sum256(body)
	if object.SHA256 != hex.EncodeToString(hash[:]) {
		return RestorePointGraph{}, fmt.Errorf("%w: manifest checksum mismatch", ErrRepositoryIncomplete)
	}
	manifest, err := LoadManifest(ctx, body, signer)
	if err != nil {
		return RestorePointGraph{}, err
	}
	if manifest.RestorePointID != restorePointID {
		return RestorePointGraph{}, fmt.Errorf("%w: manifest restore point id mismatch", ErrInvalidManifest)
	}
	references := map[string]struct{}{key: {}}
	for _, entry := range manifest.Objects {
		stored, err := repository.Stat(ctx, entry.Key)
		if err != nil {
			return RestorePointGraph{}, fmt.Errorf("%w: %s object %q: %v", ErrRepositoryIncomplete, repository.Name(), entry.Key, err)
		}
		if stored.Key != entry.Key || stored.Size != entry.CiphertextBytes || stored.SHA256 != entry.CiphertextSHA256 {
			return RestorePointGraph{}, fmt.Errorf("%w: %s object %q verification mismatch", ErrRepositoryIncomplete, repository.Name(), entry.Key)
		}
		references[entry.Key] = struct{}{}
	}
	visited := make(map[string]struct{})
	for _, reference := range manifest.Partitions {
		if err := loadAndVerifyPartitionChain(ctx, repository, reference, visited, references, 0); err != nil {
			return RestorePointGraph{}, err
		}
	}
	keys := make([]string, 0, len(references))
	for reference := range references {
		keys = append(keys, reference)
	}
	sort.Strings(keys)
	return RestorePointGraph{Manifest: manifest, Keys: keys}, nil
}

const maxPartitionChainDepth = 10_000

func loadAndVerifyPartitionChain(ctx context.Context, repository Repository, reference PartitionReference, visited, references map[string]struct{}, depth int) error {
	if depth >= maxPartitionChainDepth {
		return fmt.Errorf("%w: partition chain exceeds limit", ErrInvalidManifest)
	}
	identity := fmt.Sprintf("%d:%s:%s", reference.HashSlot, reference.Key, reference.SHA256)
	if _, ok := visited[identity]; ok {
		return nil
	}
	visited[identity] = struct{}{}
	references[reference.Key] = struct{}{}
	reader, object, err := repository.Open(ctx, reference.Key)
	if err != nil {
		return fmt.Errorf("%w: %s partition %q: %v", ErrRepositoryIncomplete, repository.Name(), reference.Key, err)
	}
	body, readErr := io.ReadAll(io.LimitReader(reader, maxManifestBytes+1))
	closeErr := reader.Close()
	if readErr != nil {
		return fmt.Errorf("%w: read partition %q: %v", ErrRepositoryIncomplete, reference.Key, readErr)
	}
	if closeErr != nil {
		return closeErr
	}
	hash := sha256.Sum256(body)
	if len(body) > maxManifestBytes || object.Key != reference.Key || object.Size != reference.Bytes || object.SHA256 != reference.SHA256 || hex.EncodeToString(hash[:]) != reference.SHA256 {
		return fmt.Errorf("%w: partition %q metadata mismatch", ErrRepositoryIncomplete, reference.Key)
	}
	partition, err := LoadPartitionManifest(body)
	if err != nil {
		return err
	}
	if partition.Cut.HashSlot != reference.HashSlot || uint64(len(partition.Objects)) != reference.ObjectCount || partition.Evidence != reference.Evidence {
		return fmt.Errorf("%w: partition %q summary mismatch", ErrInvalidManifest, reference.Key)
	}
	var ciphertextBytes uint64
	for _, entry := range partition.Objects {
		ciphertextBytes += uint64(entry.CiphertextBytes)
		if err := verifyRepositoryObject(ctx, repository, entry); err != nil {
			return fmt.Errorf("%w: %s object %q: %v", ErrRepositoryIncomplete, repository.Name(), entry.Key, err)
		}
		references[entry.Key] = struct{}{}
	}
	if ciphertextBytes != reference.CiphertextBytes {
		return fmt.Errorf("%w: partition %q byte summary mismatch", ErrInvalidManifest, reference.Key)
	}
	if partition.Base != nil {
		return loadAndVerifyPartitionChain(ctx, repository, *partition.Base, visited, references, depth+1)
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

func putAndVerify(ctx context.Context, repository Repository, key, checksum string, body []byte) error {
	err := repository.PutImmutable(ctx, key, int64(len(body)), checksum, bytes.NewReader(body))
	if err != nil && !errors.Is(err, ErrObjectExists) {
		return err
	}
	object, err := repository.Stat(ctx, key)
	if err != nil {
		return err
	}
	if object.Key != key || object.Size != int64(len(body)) || object.SHA256 != checksum {
		return fmt.Errorf("repository verification mismatch")
	}
	return nil
}

func restorePointManifestKey(restorePointID string) string {
	return "restore-points/" + restorePointID + "/manifest.json"
}
