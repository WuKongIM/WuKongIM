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

// CheckpointCatalogStateCommit contains one durable checkpoint retention-state append.
type CheckpointCatalogStateCommit struct {
	// Checkpoint is the newly signed hold/release state.
	Checkpoint backupartifact.CatalogCheckpointReference
	// Head authenticates the catalog append containing Checkpoint.
	Head backupartifact.CatalogPageReference
}

// ReplicatedCheckpointCatalog appends signed vector cuts to two repositories.
type ReplicatedCheckpointCatalog struct {
	primary         backupartifact.Repository
	secondary       backupartifact.Repository
	primaryRepair   backupartifact.RepairRepository
	secondaryRepair backupartifact.RepairRepository
	signer          backupartifact.ManifestSigner
	signingKeyID    string
}

// NewReplicatedCheckpointCatalog creates a dual-repository catalog boundary.
func NewReplicatedCheckpointCatalog(
	primary, secondary backupartifact.Repository,
	signer backupartifact.ManifestSigner,
	signingKeyID string,
) (*ReplicatedCheckpointCatalog, error) {
	return newReplicatedCheckpointCatalog(
		primary, secondary, nil, nil, signer, signingKeyID,
	)
}

// NewReplicatedCheckpointCatalogWithRepair creates a catalog whose integrity
// auditor has explicit overwrite capability without weakening ordinary writes.
func NewReplicatedCheckpointCatalogWithRepair(
	primary, secondary backupartifact.Repository,
	primaryRepair, secondaryRepair backupartifact.RepairRepository,
	signer backupartifact.ManifestSigner,
	signingKeyID string,
) (*ReplicatedCheckpointCatalog, error) {
	if primaryRepair == nil || secondaryRepair == nil ||
		primary == nil || secondary == nil ||
		primaryRepair.Name() != primary.Name() ||
		secondaryRepair.Name() != secondary.Name() {
		return nil, fmt.Errorf(
			"backup checkpoint catalog: repair capabilities are invalid",
		)
	}
	return newReplicatedCheckpointCatalog(
		primary, secondary, primaryRepair, secondaryRepair,
		signer, signingKeyID,
	)
}

func newReplicatedCheckpointCatalog(
	primary, secondary backupartifact.Repository,
	primaryRepair, secondaryRepair backupartifact.RepairRepository,
	signer backupartifact.ManifestSigner,
	signingKeyID string,
) (*ReplicatedCheckpointCatalog, error) {
	catalog := &ReplicatedCheckpointCatalog{
		primary: primary, secondary: secondary,
		primaryRepair: primaryRepair, secondaryRepair: secondaryRepair,
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
	vector, vectorBody, err := c.prepareGenerationVector(
		ctx, checkpointGenerationVector(signedCheckpoint),
	)
	if err != nil {
		return CheckpointCatalogCommit{}, err
	}
	vectorReference := backupartifact.GenerationVectorReference{
		ID: vector.ID, Key: backupartifact.GenerationVectorObjectKey(vector.ID),
		SHA256: checkpointCatalogSHA256(vectorBody), Bytes: int64(len(vectorBody)),
		HashSlotCount: vector.HashSlotCount,
	}
	checkpointReference := backupartifact.CatalogCheckpointReference{
		ID: signedCheckpoint.ID, Key: backupartifact.CheckpointObjectKey(signedCheckpoint.ID),
		SHA256: checkpointCatalogSHA256(checkpointBody), Bytes: int64(len(checkpointBody)),
		CreatedAtUnixMillis:   signedCheckpoint.CreatedAtUnixMillis,
		EffectiveAtUnixMillis: signedCheckpoint.EffectiveAtUnixMillis,
		GenerationVector:      vectorReference,
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

// SetCheckpointHold appends one signed hold/release state for an authenticated
// checkpoint. The caller advances the Controller head only after both page
// copies commit.
func (c *ReplicatedCheckpointCatalog) SetCheckpointHold(
	ctx context.Context,
	checkpoint backupartifact.CatalogCheckpointReference,
	held bool,
	createdAtUnixMillis int64,
	previous *backupartifact.CatalogPageReference,
) (CheckpointCatalogStateCommit, error) {
	if c == nil || previous == nil || previous.Sequence == math.MaxUint64 ||
		createdAtUnixMillis < checkpoint.CreatedAtUnixMillis || checkpoint.Held == held {
		return CheckpointCatalogStateCommit{}, backupartifact.ErrInvalidObject
	}
	if _, err := c.LoadCheckpoint(ctx, checkpoint); err != nil {
		return CheckpointCatalogStateCommit{}, err
	}
	checkpoint.Held = held
	checkpoint.StateOnly = true
	sequence := previous.Sequence + 1
	pageCandidate := backupartifact.CatalogPage{
		Format: backupartifact.CatalogPageFormat, Version: backupartifact.CatalogPageVersion,
		Sequence: sequence, CreatedAtUnixMillis: createdAtUnixMillis,
		Previous: cloneCatalogPageReference(previous),
		Entries:  []backupartifact.CatalogCheckpointReference{checkpoint},
	}
	_, pageBody, err := c.prepareCatalogPage(ctx, pageCandidate)
	if err != nil {
		return CheckpointCatalogStateCommit{}, err
	}
	head := backupartifact.CatalogPageReference{
		Sequence: sequence,
		Key:      backupartifact.CatalogPageObjectKey(sequence, checkpoint.ID),
		SHA256:   checkpointCatalogSHA256(pageBody), Bytes: int64(len(pageBody)),
		LatestCheckpointID: checkpoint.ID,
	}
	for _, repository := range []backupartifact.Repository{c.secondary, c.primary} {
		if err := putCheckpointCatalogObject(
			ctx, repository, head.Key, head.SHA256, pageBody,
		); err != nil {
			return CheckpointCatalogStateCommit{}, fmt.Errorf(
				"%w: %s %s: %v",
				backupartifact.ErrRepositoryIncomplete,
				repository.Name(), head.Key, err,
			)
		}
	}
	return CheckpointCatalogStateCommit{Checkpoint: checkpoint, Head: head}, nil
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

func (c *ReplicatedCheckpointCatalog) prepareGenerationVector(
	ctx context.Context,
	generations []string,
) (backupartifact.GenerationVector, []byte, error) {
	candidate, err := backupartifact.NewGenerationVector(generations)
	if err != nil {
		return backupartifact.GenerationVector{}, nil, err
	}
	key := backupartifact.GenerationVectorObjectKey(candidate.ID)
	primaryBody, primary, primaryFound, err := c.loadExistingGenerationVector(ctx, c.primary, key)
	if err != nil {
		return backupartifact.GenerationVector{}, nil, err
	}
	secondaryBody, secondary, secondaryFound, err := c.loadExistingGenerationVector(ctx, c.secondary, key)
	if err != nil {
		return backupartifact.GenerationVector{}, nil, err
	}
	var signed backupartifact.GenerationVector
	var body []byte
	if primaryFound || secondaryFound {
		body, signed = primaryBody, primary
		if !primaryFound {
			body, signed = secondaryBody, secondary
		}
		if primaryFound && secondaryFound && !bytes.Equal(primaryBody, secondaryBody) {
			return backupartifact.GenerationVector{}, nil, backupartifact.ErrRepositoryIncomplete
		}
		existing := signed
		existing.Signature = nil
		if !reflect.DeepEqual(existing, candidate) {
			return backupartifact.GenerationVector{}, nil, backupartifact.ErrInvalidObject
		}
	} else {
		signed, err = backupartifact.SignGenerationVector(
			ctx, candidate, c.signer, c.signingKeyID,
		)
		if err != nil {
			return backupartifact.GenerationVector{}, nil, err
		}
		body, err = backupartifact.MarshalGenerationVector(signed)
		if err != nil {
			return backupartifact.GenerationVector{}, nil, err
		}
	}
	for _, repository := range []backupartifact.Repository{c.primary, c.secondary} {
		if repository.Name() == c.primary.Name() && primaryFound {
			continue
		}
		if repository.Name() == c.secondary.Name() && secondaryFound {
			continue
		}
		if err := putCheckpointCatalogObject(
			ctx, repository, key, checkpointCatalogSHA256(body), body,
		); err != nil {
			return backupartifact.GenerationVector{}, nil, fmt.Errorf(
				"%w: %s %s: %v",
				backupartifact.ErrRepositoryIncomplete, repository.Name(), key, err,
			)
		}
	}
	return signed, body, nil
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

func (c *ReplicatedCheckpointCatalog) loadExistingGenerationVector(
	ctx context.Context,
	repository backupartifact.Repository,
	key string,
) ([]byte, backupartifact.GenerationVector, bool, error) {
	body, found, err := readOptionalCheckpointCatalogObject(ctx, repository, key)
	if err != nil || !found {
		return nil, backupartifact.GenerationVector{}, found, err
	}
	vector, err := backupartifact.LoadGenerationVector(ctx, body, c.signer)
	return body, vector, true, err
}

// LoadPageForIntegrityAudit repairs one unhealthy copy from its authenticated
// peer, revalidates it, and then returns the detached signed page.
func (c *ReplicatedCheckpointCatalog) LoadPageForIntegrityAudit(
	ctx context.Context,
	reference backupartifact.CatalogPageReference,
) (backupartifact.CatalogPage, error) {
	body, err := c.loadAndRepairCatalogObject(
		ctx, reference.Key, reference.SHA256, reference.Bytes,
		func(body []byte) error {
			page, loadErr := backupartifact.LoadCatalogPage(ctx, body, c.signer)
			if loadErr != nil {
				return loadErr
			}
			if page.Sequence != reference.Sequence ||
				len(page.Entries) == 0 ||
				page.Entries[0].ID != reference.LatestCheckpointID {
				return backupartifact.ErrObjectCorrupt
			}
			return nil
		},
	)
	if err != nil {
		return backupartifact.CatalogPage{}, err
	}
	return backupartifact.LoadCatalogPage(ctx, body, c.signer)
}

// LoadCheckpointForIntegrityAudit repairs and revalidates the checkpoint and
// its content-addressed Generation vector before navigation continues.
func (c *ReplicatedCheckpointCatalog) LoadCheckpointForIntegrityAudit(
	ctx context.Context,
	reference backupartifact.CatalogCheckpointReference,
) (backupartifact.Checkpoint, error) {
	body, err := c.loadAndRepairCatalogObject(
		ctx, reference.Key, reference.SHA256, reference.Bytes,
		func(body []byte) error {
			checkpoint, loadErr := backupartifact.LoadCheckpoint(
				ctx, body, c.signer,
			)
			if loadErr != nil {
				return loadErr
			}
			if checkpoint.ID != reference.ID ||
				checkpoint.CreatedAtUnixMillis !=
					reference.CreatedAtUnixMillis ||
				checkpoint.EffectiveAtUnixMillis !=
					reference.EffectiveAtUnixMillis {
				return backupartifact.ErrObjectCorrupt
			}
			return nil
		},
	)
	if err != nil {
		return backupartifact.Checkpoint{}, err
	}
	checkpoint, err := backupartifact.LoadCheckpoint(ctx, body, c.signer)
	if err != nil {
		return backupartifact.Checkpoint{}, err
	}
	vectorBody, err := c.loadAndRepairCatalogObject(
		ctx, reference.GenerationVector.Key,
		reference.GenerationVector.SHA256,
		reference.GenerationVector.Bytes,
		func(body []byte) error {
			vector, loadErr := backupartifact.LoadGenerationVector(
				ctx, body, c.signer,
			)
			if loadErr != nil {
				return loadErr
			}
			if !generationVectorMatchesReference(
				vector, body, reference.GenerationVector,
			) {
				return backupartifact.ErrObjectCorrupt
			}
			return nil
		},
	)
	if err != nil {
		return backupartifact.Checkpoint{}, err
	}
	vector, err := backupartifact.LoadGenerationVector(
		ctx, vectorBody, c.signer,
	)
	if err != nil {
		return backupartifact.Checkpoint{}, err
	}
	if !checkpointMatchesGenerationVector(checkpoint, vector) {
		return backupartifact.Checkpoint{}, backupartifact.ErrObjectCorrupt
	}
	return checkpoint, nil
}

func (c *ReplicatedCheckpointCatalog) loadAndRepairCatalogObject(
	ctx context.Context,
	key, checksum string,
	size int64,
	validate func([]byte) error,
) ([]byte, error) {
	type catalogCopy struct {
		repository backupartifact.Repository
		repair     backupartifact.RepairRepository
		body       []byte
		healthy    bool
	}
	copies := []catalogCopy{
		{repository: c.primary, repair: c.primaryRepair},
		{repository: c.secondary, repair: c.secondaryRepair},
	}
	for index := range copies {
		body, err := readCheckpointCatalogObject(
			ctx, copies[index].repository, key, checksum, size,
		)
		if err == nil && validate(body) == nil {
			copies[index].body = body
			copies[index].healthy = true
		}
	}
	switch {
	case copies[0].healthy && copies[1].healthy:
		if !bytes.Equal(copies[0].body, copies[1].body) {
			return nil, backupartifact.ErrRepositoryIncomplete
		}
		return copies[0].body, nil
	case !copies[0].healthy && !copies[1].healthy:
		return nil, backupartifact.ErrRepositoryIncomplete
	}
	healthyIndex, damagedIndex := 0, 1
	if !copies[0].healthy {
		healthyIndex, damagedIndex = 1, 0
	}
	if copies[damagedIndex].repair == nil {
		return nil, fmt.Errorf(
			"%w: catalog repair capability for %s is not configured",
			backupartifact.ErrRepositoryIncomplete,
			copies[damagedIndex].repository.Name(),
		)
	}
	healthyBody := copies[healthyIndex].body
	if err := copies[damagedIndex].repair.RepairImmutable(
		ctx, key, size, checksum, bytes.NewReader(healthyBody),
	); err != nil {
		return nil, err
	}
	revalidated, err := readCheckpointCatalogObject(
		ctx, copies[damagedIndex].repository, key, checksum, size,
	)
	if err != nil || validate(revalidated) != nil ||
		!bytes.Equal(healthyBody, revalidated) {
		return nil, backupartifact.ErrRepositoryIncomplete
	}
	return healthyBody, nil
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

// LoadPageCopy authenticates one explicit repository copy without requiring
// the peer repository to be available during disaster recovery.
func (c *ReplicatedCheckpointCatalog) LoadPageCopy(
	ctx context.Context,
	repository backupartifact.Repository,
	reference backupartifact.CatalogPageReference,
) (backupartifact.CatalogPage, error) {
	if c == nil || repository == nil ||
		(repository.Name() != c.primary.Name() && repository.Name() != c.secondary.Name()) {
		return backupartifact.CatalogPage{}, backupartifact.ErrInvalidObject
	}
	body, err := readCheckpointCatalogObject(
		ctx, repository, reference.Key, reference.SHA256, reference.Bytes,
	)
	if err != nil {
		return backupartifact.CatalogPage{}, err
	}
	page, err := backupartifact.LoadCatalogPage(ctx, body, c.signer)
	if err != nil {
		return backupartifact.CatalogPage{}, err
	}
	if page.Sequence != reference.Sequence || len(page.Entries) == 0 ||
		page.Entries[0].ID != reference.LatestCheckpointID {
		return backupartifact.CatalogPage{}, backupartifact.ErrObjectCorrupt
	}
	return page, nil
}

// ResolveCheckpointForRestore authenticates a checkpoint's original catalog
// membership under one pinned head using exactly one selected repository copy.
func (c *ReplicatedCheckpointCatalog) ResolveCheckpointForRestore(
	ctx context.Context,
	repository backupartifact.Repository,
	head backupartifact.CatalogPageReference,
	checkpointID string,
	latest bool,
) (backupartifact.CheckpointCatalogProof, backupartifact.Checkpoint, error) {
	checkpointID = strings.TrimSpace(checkpointID)
	if c == nil || repository == nil || (checkpointID == "") == !latest {
		return backupartifact.CheckpointCatalogProof{}, backupartifact.Checkpoint{},
			backupartifact.ErrInvalidObject
	}
	reference := &head
	for reference != nil {
		if err := ctx.Err(); err != nil {
			return backupartifact.CheckpointCatalogProof{}, backupartifact.Checkpoint{}, err
		}
		pageReference := *reference
		page, err := c.LoadPageCopy(ctx, repository, pageReference)
		if err != nil {
			return backupartifact.CheckpointCatalogProof{}, backupartifact.Checkpoint{}, err
		}
		for _, entry := range page.Entries {
			if entry.StateOnly || (!latest && entry.ID != checkpointID) {
				continue
			}
			checkpoint, err := c.LoadCheckpointCopy(ctx, repository, entry)
			if err != nil {
				return backupartifact.CheckpointCatalogProof{}, backupartifact.Checkpoint{}, err
			}
			proof := backupartifact.CheckpointCatalogProof{
				Head: head, EntryPage: pageReference, Checkpoint: entry,
			}
			if err := backupartifact.ValidateCheckpointCatalogProof(proof); err != nil {
				return backupartifact.CheckpointCatalogProof{}, backupartifact.Checkpoint{}, err
			}
			return proof, checkpoint, nil
		}
		reference = page.Previous
	}
	return backupartifact.CheckpointCatalogProof{}, backupartifact.Checkpoint{},
		fmt.Errorf("%w: checkpoint is absent from pinned catalog", backupartifact.ErrObjectNotFound)
}

// ResolveCheckpointForRestoreDual authenticates every catalog link and the
// selected checkpoint in both independent repositories before admission.
func (c *ReplicatedCheckpointCatalog) ResolveCheckpointForRestoreDual(
	ctx context.Context,
	head backupartifact.CatalogPageReference,
	checkpointID string,
) (
	backupartifact.CheckpointCatalogProof,
	backupartifact.Checkpoint,
	error,
) {
	checkpointID = strings.TrimSpace(checkpointID)
	if c == nil || checkpointID == "" {
		return backupartifact.CheckpointCatalogProof{},
			backupartifact.Checkpoint{}, backupartifact.ErrInvalidObject
	}
	reference := &head
	for reference != nil {
		if err := ctx.Err(); err != nil {
			return backupartifact.CheckpointCatalogProof{},
				backupartifact.Checkpoint{}, err
		}
		pageReference := *reference
		page, err := c.LoadPage(ctx, pageReference)
		if err != nil {
			return backupartifact.CheckpointCatalogProof{},
				backupartifact.Checkpoint{}, err
		}
		for _, entry := range page.Entries {
			if entry.StateOnly || entry.ID != checkpointID {
				continue
			}
			checkpoint, err := c.LoadCheckpoint(ctx, entry)
			if err != nil {
				return backupartifact.CheckpointCatalogProof{},
					backupartifact.Checkpoint{}, err
			}
			proof := backupartifact.CheckpointCatalogProof{
				Head: head, EntryPage: pageReference,
				Checkpoint: entry,
			}
			if err := backupartifact.ValidateCheckpointCatalogProof(
				proof,
			); err != nil {
				return backupartifact.CheckpointCatalogProof{},
					backupartifact.Checkpoint{}, err
			}
			return proof, checkpoint, nil
		}
		reference = page.Previous
	}
	return backupartifact.CheckpointCatalogProof{},
		backupartifact.Checkpoint{},
		fmt.Errorf(
			"%w: checkpoint is absent from pinned catalog",
			backupartifact.ErrObjectNotFound,
		)
}

// LoadCheckpointProofCopy revalidates the bounded membership proof persisted
// by restore admission without replaying the catalog history once per Hash
// Slot. Admission already authenticated every Head-to-EntryPage link before
// Controller Raft accepted the immutable proof.
func (c *ReplicatedCheckpointCatalog) LoadCheckpointProofCopy(
	ctx context.Context,
	repository backupartifact.Repository,
	proof backupartifact.CheckpointCatalogProof,
) (backupartifact.Checkpoint, error) {
	if err := backupartifact.ValidateCheckpointCatalogProof(proof); err != nil {
		return backupartifact.Checkpoint{}, err
	}
	page, err := c.LoadPageCopy(ctx, repository, proof.EntryPage)
	if err != nil {
		return backupartifact.Checkpoint{}, err
	}
	found := false
	for _, entry := range page.Entries {
		if !entry.StateOnly && reflect.DeepEqual(entry, proof.Checkpoint) {
			found = true
			break
		}
	}
	if !found {
		return backupartifact.Checkpoint{},
			fmt.Errorf(
				"%w: checkpoint is absent from admitted catalog page",
				backupartifact.ErrObjectCorrupt,
			)
	}
	return c.LoadCheckpointCopy(ctx, repository, proof.Checkpoint)
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
	vector, err := c.LoadGenerationVector(ctx, reference.GenerationVector)
	if err != nil {
		return backupartifact.Checkpoint{}, err
	}
	if !checkpointMatchesGenerationVector(checkpoint, vector) {
		return backupartifact.Checkpoint{}, backupartifact.ErrObjectCorrupt
	}
	return checkpoint, nil
}

// LoadCheckpointCopy authenticates one explicit repository copy without
// coupling an independent repair or GC cursor to the peer's availability.
func (c *ReplicatedCheckpointCatalog) LoadCheckpointCopy(
	ctx context.Context,
	repository backupartifact.Repository,
	reference backupartifact.CatalogCheckpointReference,
) (backupartifact.Checkpoint, error) {
	if c == nil || repository == nil ||
		(repository.Name() != c.primary.Name() && repository.Name() != c.secondary.Name()) {
		return backupartifact.Checkpoint{}, backupartifact.ErrInvalidObject
	}
	body, err := readCheckpointCatalogObject(
		ctx, repository, reference.Key, reference.SHA256, reference.Bytes,
	)
	if err != nil {
		return backupartifact.Checkpoint{}, err
	}
	checkpoint, err := backupartifact.LoadCheckpoint(ctx, body, c.signer)
	if err != nil {
		return backupartifact.Checkpoint{}, err
	}
	if checkpoint.ID != reference.ID ||
		checkpoint.CreatedAtUnixMillis != reference.CreatedAtUnixMillis ||
		checkpoint.EffectiveAtUnixMillis != reference.EffectiveAtUnixMillis {
		return backupartifact.Checkpoint{}, backupartifact.ErrObjectCorrupt
	}
	vector, _, err := c.LoadGenerationVectorCopy(
		ctx, repository, reference.GenerationVector,
	)
	if err != nil {
		return backupartifact.Checkpoint{}, err
	}
	if !checkpointMatchesGenerationVector(checkpoint, vector) {
		return backupartifact.Checkpoint{}, backupartifact.ErrObjectCorrupt
	}
	return checkpoint, nil
}

// LoadGenerationVector requires identical authenticated copies.
func (c *ReplicatedCheckpointCatalog) LoadGenerationVector(
	ctx context.Context,
	reference backupartifact.GenerationVectorReference,
) (backupartifact.GenerationVector, error) {
	primary, err := readCheckpointCatalogObject(
		ctx, c.primary, reference.Key, reference.SHA256, reference.Bytes,
	)
	if err != nil {
		return backupartifact.GenerationVector{}, err
	}
	secondary, err := readCheckpointCatalogObject(
		ctx, c.secondary, reference.Key, reference.SHA256, reference.Bytes,
	)
	if err != nil {
		return backupartifact.GenerationVector{}, err
	}
	if !bytes.Equal(primary, secondary) {
		return backupartifact.GenerationVector{}, backupartifact.ErrRepositoryIncomplete
	}
	vector, err := backupartifact.LoadGenerationVector(ctx, primary, c.signer)
	if err != nil || !generationVectorMatchesReference(vector, primary, reference) {
		if err != nil {
			return backupartifact.GenerationVector{}, err
		}
		return backupartifact.GenerationVector{}, backupartifact.ErrObjectCorrupt
	}
	return vector, nil
}

// LoadGenerationVectorCopy authenticates one explicit repository copy and
// returns its exact bytes for a local rebuildable GC cache.
func (c *ReplicatedCheckpointCatalog) LoadGenerationVectorCopy(
	ctx context.Context,
	repository backupartifact.Repository,
	reference backupartifact.GenerationVectorReference,
) (backupartifact.GenerationVector, []byte, error) {
	if c == nil || repository == nil ||
		(repository.Name() != c.primary.Name() && repository.Name() != c.secondary.Name()) {
		return backupartifact.GenerationVector{}, nil, backupartifact.ErrInvalidObject
	}
	body, err := readCheckpointCatalogObject(
		ctx, repository, reference.Key, reference.SHA256, reference.Bytes,
	)
	if err != nil {
		return backupartifact.GenerationVector{}, nil, err
	}
	vector, err := backupartifact.LoadGenerationVector(ctx, body, c.signer)
	if err != nil || !generationVectorMatchesReference(vector, body, reference) {
		if err != nil {
			return backupartifact.GenerationVector{}, nil, err
		}
		return backupartifact.GenerationVector{}, nil, backupartifact.ErrObjectCorrupt
	}
	return vector, body, nil
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

func checkpointGenerationVector(checkpoint backupartifact.Checkpoint) []string {
	generations := make([]string, len(checkpoint.Slots))
	for index, slot := range checkpoint.Slots {
		generations[index] = slot.Generation
	}
	return generations
}

func checkpointMatchesGenerationVector(
	checkpoint backupartifact.Checkpoint,
	vector backupartifact.GenerationVector,
) bool {
	return vector.HashSlotCount == checkpoint.HashSlotCount &&
		reflect.DeepEqual(vector.Generations, checkpointGenerationVector(checkpoint))
}

func generationVectorMatchesReference(
	vector backupartifact.GenerationVector,
	body []byte,
	reference backupartifact.GenerationVectorReference,
) bool {
	return vector.ID == reference.ID &&
		vector.HashSlotCount == reference.HashSlotCount &&
		reference.Key == backupartifact.GenerationVectorObjectKey(vector.ID) &&
		int64(len(body)) == reference.Bytes &&
		checkpointCatalogSHA256(body) == reference.SHA256
}
