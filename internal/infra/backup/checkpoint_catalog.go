package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"strings"

	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

const maxCheckpointCatalogObjectBytes = 4 << 20

// CheckpointCatalogCommit contains the new immutable checkpoint and catalog head.
type CheckpointCatalogCommit = backupartifact.CheckpointCatalogCommit

// ReplicatedCheckpointCatalog appends signed vector cuts to two repositories.
type ReplicatedCheckpointCatalog struct {
	primary      backupartifact.Repository
	secondary    backupartifact.Repository
	signer       backupartifact.ManifestSigner
	signingKeyID string
}

// NewReplicatedCheckpointCatalog creates a dual-repository catalog boundary.
func NewReplicatedCheckpointCatalog(
	primary, secondary backupartifact.Repository,
	signer backupartifact.ManifestSigner,
	signingKeyID string,
) (*ReplicatedCheckpointCatalog, error) {
	catalog := &ReplicatedCheckpointCatalog{
		primary: primary, secondary: secondary,
		signer: signer, signingKeyID: strings.TrimSpace(signingKeyID),
	}
	if primary == nil || secondary == nil || primary.Name() == "" ||
		secondary.Name() == "" || primary.Name() == secondary.Name() ||
		signer == nil || catalog.signingKeyID == "" {
		return nil, fmt.Errorf("backup checkpoint catalog: dependencies are invalid")
	}
	return catalog, nil
}

// Publish signs and dual-commits only the new checkpoint and new catalog page.
// The caller advances the Controller head after this method succeeds.
func (c *ReplicatedCheckpointCatalog) Publish(
	ctx context.Context,
	checkpoint backupartifact.Checkpoint,
	previous *backupartifact.CatalogPageReference,
) (CheckpointCatalogCommit, error) {
	signedCheckpoint, checkpointBody, err := c.prepareCheckpoint(ctx, checkpoint)
	if err != nil {
		return CheckpointCatalogCommit{}, err
	}
	checkpointReference := backupartifact.CatalogCheckpointReference{
		ID: signedCheckpoint.ID, Key: backupartifact.CheckpointObjectKey(signedCheckpoint.ID),
		SHA256: checkpointCatalogSHA256(checkpointBody), Bytes: int64(len(checkpointBody)),
		CreatedAtUnixMillis:   signedCheckpoint.CreatedAtUnixMillis,
		EffectiveAtUnixMillis: signedCheckpoint.EffectiveAtUnixMillis,
	}
	sequence := uint64(1)
	if previous != nil {
		if previous.Sequence == math.MaxUint64 {
			return CheckpointCatalogCommit{}, fmt.Errorf("%w: catalog sequence exhausted", backupartifact.ErrInvalidObject)
		}
		sequence = previous.Sequence + 1
	}
	pageCandidate := backupartifact.CatalogPage{
		Format: backupartifact.CatalogPageFormat, Version: backupartifact.CatalogPageVersion,
		Sequence: sequence, CreatedAtUnixMillis: signedCheckpoint.CreatedAtUnixMillis,
		Previous: cloneCatalogPageReference(previous),
		Entries:  []backupartifact.CatalogCheckpointReference{checkpointReference},
	}
	_, pageBody, err := c.prepareCatalogPage(ctx, pageCandidate)
	if err != nil {
		return CheckpointCatalogCommit{}, err
	}
	head := backupartifact.CatalogPageReference{
		Sequence: sequence,
		Key:      backupartifact.CatalogPageObjectKey(sequence, signedCheckpoint.ID),
		SHA256:   checkpointCatalogSHA256(pageBody), Bytes: int64(len(pageBody)),
		LatestCheckpointID: signedCheckpoint.ID,
	}
	for _, object := range []struct {
		repository backupartifact.Repository
		key        string
		checksum   string
		body       []byte
	}{
		{c.primary, checkpointReference.Key, checkpointReference.SHA256, checkpointBody},
		{c.secondary, checkpointReference.Key, checkpointReference.SHA256, checkpointBody},
		{c.secondary, head.Key, head.SHA256, pageBody},
		{c.primary, head.Key, head.SHA256, pageBody},
	} {
		if err := putCheckpointCatalogObject(ctx, object.repository, object.key, object.checksum, object.body); err != nil {
			return CheckpointCatalogCommit{}, fmt.Errorf(
				"%w: %s %s: %v",
				backupartifact.ErrRepositoryIncomplete,
				object.repository.Name(), object.key, err,
			)
		}
	}
	return CheckpointCatalogCommit{Checkpoint: checkpointReference, Head: head}, nil
}

func (c *ReplicatedCheckpointCatalog) prepareCheckpoint(
	ctx context.Context,
	checkpoint backupartifact.Checkpoint,
) (backupartifact.Checkpoint, []byte, error) {
	checkpoint.Signature = nil
	key := backupartifact.CheckpointObjectKey(checkpoint.ID)
	primaryBody, primary, primaryFound, err := c.loadExistingCheckpoint(ctx, c.primary, key)
	if err != nil {
		return backupartifact.Checkpoint{}, nil, err
	}
	secondaryBody, secondary, secondaryFound, err := c.loadExistingCheckpoint(ctx, c.secondary, key)
	if err != nil {
		return backupartifact.Checkpoint{}, nil, err
	}
	if primaryFound || secondaryFound {
		body, existing := primaryBody, primary
		if !primaryFound {
			body, existing = secondaryBody, secondary
		}
		if primaryFound && secondaryFound && !bytes.Equal(primaryBody, secondaryBody) {
			return backupartifact.Checkpoint{}, nil, backupartifact.ErrRepositoryIncomplete
		}
		signature := existing.Signature
		existing.Signature = nil
		if !reflect.DeepEqual(existing, checkpoint) {
			return backupartifact.Checkpoint{}, nil, backupartifact.ErrInvalidObject
		}
		existing.Signature = signature
		return existing, body, nil
	}
	signed, err := backupartifact.SignCheckpoint(ctx, checkpoint, c.signer, c.signingKeyID)
	if err != nil {
		return backupartifact.Checkpoint{}, nil, err
	}
	body, err := backupartifact.MarshalCheckpoint(signed)
	return signed, body, err
}

func (c *ReplicatedCheckpointCatalog) prepareCatalogPage(
	ctx context.Context,
	page backupartifact.CatalogPage,
) (backupartifact.CatalogPage, []byte, error) {
	page.Signature = nil
	key := backupartifact.CatalogPageObjectKey(page.Sequence, page.Entries[0].ID)
	primaryBody, primary, primaryFound, err := c.loadExistingCatalogPage(ctx, c.primary, key)
	if err != nil {
		return backupartifact.CatalogPage{}, nil, err
	}
	secondaryBody, secondary, secondaryFound, err := c.loadExistingCatalogPage(ctx, c.secondary, key)
	if err != nil {
		return backupartifact.CatalogPage{}, nil, err
	}
	if primaryFound || secondaryFound {
		body, existing := primaryBody, primary
		if !primaryFound {
			body, existing = secondaryBody, secondary
		}
		if primaryFound && secondaryFound && !bytes.Equal(primaryBody, secondaryBody) {
			return backupartifact.CatalogPage{}, nil, backupartifact.ErrRepositoryIncomplete
		}
		signature := existing.Signature
		existing.Signature = nil
		if !reflect.DeepEqual(existing, page) {
			return backupartifact.CatalogPage{}, nil, backupartifact.ErrInvalidObject
		}
		existing.Signature = signature
		return existing, body, nil
	}
	signed, err := backupartifact.SignCatalogPage(ctx, page, c.signer, c.signingKeyID)
	if err != nil {
		return backupartifact.CatalogPage{}, nil, err
	}
	body, err := backupartifact.MarshalCatalogPage(signed)
	return signed, body, err
}

func (c *ReplicatedCheckpointCatalog) loadExistingCheckpoint(
	ctx context.Context,
	repository backupartifact.Repository,
	key string,
) ([]byte, backupartifact.Checkpoint, bool, error) {
	body, found, err := readOptionalCheckpointCatalogObject(ctx, repository, key)
	if err != nil || !found {
		return nil, backupartifact.Checkpoint{}, found, err
	}
	checkpoint, err := backupartifact.LoadCheckpoint(ctx, body, c.signer)
	return body, checkpoint, true, err
}

func (c *ReplicatedCheckpointCatalog) loadExistingCatalogPage(
	ctx context.Context,
	repository backupartifact.Repository,
	key string,
) ([]byte, backupartifact.CatalogPage, bool, error) {
	body, found, err := readOptionalCheckpointCatalogObject(ctx, repository, key)
	if err != nil || !found {
		return nil, backupartifact.CatalogPage{}, found, err
	}
	page, err := backupartifact.LoadCatalogPage(ctx, body, c.signer)
	return body, page, true, err
}

// LoadPage requires matching authenticated copies of the exact referenced page.
func (c *ReplicatedCheckpointCatalog) LoadPage(
	ctx context.Context,
	reference backupartifact.CatalogPageReference,
) (backupartifact.CatalogPage, error) {
	primary, err := readCheckpointCatalogObject(ctx, c.primary, reference.Key, reference.SHA256, reference.Bytes)
	if err != nil {
		return backupartifact.CatalogPage{}, err
	}
	secondary, err := readCheckpointCatalogObject(ctx, c.secondary, reference.Key, reference.SHA256, reference.Bytes)
	if err != nil {
		return backupartifact.CatalogPage{}, err
	}
	if !bytes.Equal(primary, secondary) {
		return backupartifact.CatalogPage{}, backupartifact.ErrRepositoryIncomplete
	}
	page, err := backupartifact.LoadCatalogPage(ctx, primary, c.signer)
	if err != nil {
		return backupartifact.CatalogPage{}, err
	}
	if page.Sequence != reference.Sequence ||
		page.Entries[0].ID != reference.LatestCheckpointID {
		return backupartifact.CatalogPage{}, backupartifact.ErrObjectCorrupt
	}
	return page, nil
}

// LoadCheckpoint requires matching authenticated copies of one catalog entry.
func (c *ReplicatedCheckpointCatalog) LoadCheckpoint(
	ctx context.Context,
	reference backupartifact.CatalogCheckpointReference,
) (backupartifact.Checkpoint, error) {
	primary, err := readCheckpointCatalogObject(ctx, c.primary, reference.Key, reference.SHA256, reference.Bytes)
	if err != nil {
		return backupartifact.Checkpoint{}, err
	}
	secondary, err := readCheckpointCatalogObject(ctx, c.secondary, reference.Key, reference.SHA256, reference.Bytes)
	if err != nil {
		return backupartifact.Checkpoint{}, err
	}
	if !bytes.Equal(primary, secondary) {
		return backupartifact.Checkpoint{}, backupartifact.ErrRepositoryIncomplete
	}
	checkpoint, err := backupartifact.LoadCheckpoint(ctx, primary, c.signer)
	if err != nil {
		return backupartifact.Checkpoint{}, err
	}
	if checkpoint.ID != reference.ID ||
		checkpoint.CreatedAtUnixMillis != reference.CreatedAtUnixMillis ||
		checkpoint.EffectiveAtUnixMillis != reference.EffectiveAtUnixMillis {
		return backupartifact.Checkpoint{}, backupartifact.ErrObjectCorrupt
	}
	return checkpoint, nil
}

func putCheckpointCatalogObject(
	ctx context.Context,
	repository backupartifact.Repository,
	key, checksum string,
	body []byte,
) error {
	err := repository.PutImmutable(ctx, key, int64(len(body)), checksum, bytes.NewReader(body))
	if err != nil && !errors.Is(err, backupartifact.ErrObjectExists) {
		return err
	}
	object, err := repository.Stat(ctx, key)
	if err != nil {
		return err
	}
	if object.Key != key || object.Size != int64(len(body)) || object.SHA256 != checksum {
		return backupartifact.ErrObjectCorrupt
	}
	return nil
}

func readCheckpointCatalogObject(
	ctx context.Context,
	repository backupartifact.Repository,
	key, checksum string,
	size int64,
) ([]byte, error) {
	if size <= 0 || size > maxCheckpointCatalogObjectBytes {
		return nil, backupartifact.ErrInvalidObject
	}
	reader, object, err := repository.Open(ctx, key)
	if err != nil {
		return nil, err
	}
	body, readErr := io.ReadAll(io.LimitReader(reader, size+1))
	closeErr := reader.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if int64(len(body)) != size || object.Key != key || object.Size != size ||
		object.SHA256 != checksum || checkpointCatalogSHA256(body) != checksum {
		return nil, backupartifact.ErrObjectCorrupt
	}
	return body, nil
}

func readOptionalCheckpointCatalogObject(
	ctx context.Context,
	repository backupartifact.Repository,
	key string,
) ([]byte, bool, error) {
	reader, object, err := repository.Open(ctx, key)
	if errors.Is(err, backupartifact.ErrObjectNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	body, readErr := io.ReadAll(io.LimitReader(reader, maxCheckpointCatalogObjectBytes+1))
	closeErr := reader.Close()
	if readErr != nil {
		return nil, false, readErr
	}
	if closeErr != nil {
		return nil, false, closeErr
	}
	checksum := checkpointCatalogSHA256(body)
	if len(body) == 0 || len(body) > maxCheckpointCatalogObjectBytes ||
		object.Key != key || object.Size != int64(len(body)) || object.SHA256 != checksum {
		return nil, false, backupartifact.ErrObjectCorrupt
	}
	return body, true, nil
}

func checkpointCatalogSHA256(body []byte) string {
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}

func cloneCatalogPageReference(reference *backupartifact.CatalogPageReference) *backupartifact.CatalogPageReference {
	if reference == nil {
		return nil
	}
	out := *reference
	return &out
}
