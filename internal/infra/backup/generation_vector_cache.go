package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

const maxCachedGenerationVectorBytes = 4 << 20

// GenerationVectorCache is a rebuildable local cache for independently
// authenticated repository vector copies.
type GenerationVectorCache interface {
	LoadGenerationVector(
		context.Context,
		string,
		backupartifact.GenerationVectorReference,
	) (backupartifact.GenerationVector, bool, error)
	StoreGenerationVector(
		context.Context,
		string,
		backupartifact.GenerationVectorReference,
		backupartifact.GenerationVector,
		[]byte,
	) error
	PruneGenerationVectors(context.Context, string, map[string]struct{}) error
}

// FileGenerationVectorCache keeps content-addressed vectors across process
// restarts without placing checkpoint-sized state in Controller Raft.
type FileGenerationVectorCache struct {
	root   string
	signer backupartifact.ManifestSigner
}

// NewFileGenerationVectorCache creates one rebuildable node-local cache.
func NewFileGenerationVectorCache(
	root string,
	signer backupartifact.ManifestSigner,
) (*FileGenerationVectorCache, error) {
	root = strings.TrimSpace(root)
	if root == "" || signer == nil {
		return nil, fmt.Errorf("backup generation vector cache: root and signer are required")
	}
	clean := filepath.Clean(root)
	if !filepath.IsAbs(clean) {
		return nil, fmt.Errorf("backup generation vector cache: root must be absolute")
	}
	if err := os.MkdirAll(clean, 0o700); err != nil {
		return nil, err
	}
	return &FileGenerationVectorCache{root: clean, signer: signer}, nil
}

// LoadGenerationVector loads and reauthenticates one cached exact copy.
func (c *FileGenerationVectorCache) LoadGenerationVector(
	ctx context.Context,
	repository string,
	reference backupartifact.GenerationVectorReference,
) (backupartifact.GenerationVector, bool, error) {
	if err := ctx.Err(); err != nil {
		return backupartifact.GenerationVector{}, false, err
	}
	target, err := c.target(repository, reference)
	if err != nil {
		return backupartifact.GenerationVector{}, false, err
	}
	file, err := os.Open(target)
	if errors.Is(err, os.ErrNotExist) {
		return backupartifact.GenerationVector{}, false, nil
	}
	if err != nil {
		return backupartifact.GenerationVector{}, false, err
	}
	body, readErr := io.ReadAll(io.LimitReader(file, maxCachedGenerationVectorBytes+1))
	closeErr := file.Close()
	if readErr != nil {
		return backupartifact.GenerationVector{}, false, readErr
	}
	if closeErr != nil {
		return backupartifact.GenerationVector{}, false, closeErr
	}
	vector, err := c.authenticate(ctx, reference, body)
	if err == nil {
		return vector, true, nil
	}
	// The cache is derived and safe to discard. A later provider read repairs
	// this exact content-addressed entry.
	if removeErr := os.Remove(target); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		return backupartifact.GenerationVector{}, false, errors.Join(err, removeErr)
	}
	return backupartifact.GenerationVector{}, false, nil
}

// StoreGenerationVector atomically persists one already authenticated copy.
func (c *FileGenerationVectorCache) StoreGenerationVector(
	ctx context.Context,
	repository string,
	reference backupartifact.GenerationVectorReference,
	vector backupartifact.GenerationVector,
	body []byte,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	target, err := c.target(repository, reference)
	if err != nil {
		return err
	}
	loaded, err := c.authenticate(ctx, reference, body)
	if err != nil || loaded.ID != vector.ID ||
		!equalGenerationVector(loaded, vector) {
		if err != nil {
			return err
		}
		return backupartifact.ErrObjectCorrupt
	}
	if existing, found, err := c.LoadGenerationVector(ctx, repository, reference); err != nil {
		return err
	} else if found {
		if !equalGenerationVector(existing, vector) {
			return backupartifact.ErrObjectCorrupt
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(target), ".generation-vector-*")
	if err != nil {
		return err
	}
	tempPath := temporary.Name()
	remove := true
	defer func() {
		if remove {
			_ = os.Remove(tempPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(body); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, target); err != nil {
		return err
	}
	remove = false
	return nil
}

// PruneGenerationVectors removes derived entries not used by the completed
// protection decision. Every removed entry remains recoverable from its
// authenticated repository copy.
func (c *FileGenerationVectorCache) PruneGenerationVectors(
	ctx context.Context,
	repository string,
	keep map[string]struct{},
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	directory, err := c.repositoryDirectory(repository)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		if _, retained := keep[id]; retained {
			continue
		}
		if len(id) != sha256.Size*2 {
			continue
		}
		if _, err := hex.DecodeString(id); err != nil ||
			strings.ToLower(id) != id {
			continue
		}
		if err := os.Remove(filepath.Join(directory, entry.Name())); err != nil &&
			!errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func (c *FileGenerationVectorCache) authenticate(
	ctx context.Context,
	reference backupartifact.GenerationVectorReference,
	body []byte,
) (backupartifact.GenerationVector, error) {
	digest := sha256.Sum256(body)
	if len(body) == 0 || len(body) > maxCachedGenerationVectorBytes ||
		int64(len(body)) != reference.Bytes ||
		hex.EncodeToString(digest[:]) != reference.SHA256 ||
		reference.Key != backupartifact.GenerationVectorObjectKey(reference.ID) {
		return backupartifact.GenerationVector{}, backupartifact.ErrObjectCorrupt
	}
	vector, err := backupartifact.LoadGenerationVector(ctx, body, c.signer)
	if err != nil {
		return backupartifact.GenerationVector{}, err
	}
	if vector.ID != reference.ID ||
		vector.HashSlotCount != reference.HashSlotCount {
		return backupartifact.GenerationVector{}, backupartifact.ErrObjectCorrupt
	}
	return vector, nil
}

func (c *FileGenerationVectorCache) target(
	repository string,
	reference backupartifact.GenerationVectorReference,
) (string, error) {
	repository = strings.TrimSpace(repository)
	if c == nil || c.signer == nil || repository == "" ||
		len(reference.ID) != sha256.Size*2 ||
		len(reference.SHA256) != sha256.Size*2 ||
		reference.Bytes <= 0 || reference.Bytes > maxCachedGenerationVectorBytes ||
		reference.HashSlotCount == 0 {
		return "", backupartifact.ErrInvalidObject
	}
	for _, digest := range []string{reference.ID, reference.SHA256} {
		if _, err := hex.DecodeString(digest); err != nil ||
			strings.ToLower(digest) != digest {
			return "", backupartifact.ErrInvalidObject
		}
	}
	directory, err := c.repositoryDirectory(repository)
	if err != nil {
		return "", err
	}
	return filepath.Join(directory, reference.ID+".json"), nil
}

func (c *FileGenerationVectorCache) repositoryDirectory(
	repository string,
) (string, error) {
	repository = strings.TrimSpace(repository)
	if c == nil || c.signer == nil || repository == "" {
		return "", backupartifact.ErrInvalidObject
	}
	repositoryDigest := sha256.Sum256([]byte(repository))
	return filepath.Join(c.root, hex.EncodeToString(repositoryDigest[:])), nil
}

func equalGenerationVector(
	left, right backupartifact.GenerationVector,
) bool {
	leftBody, leftErr := backupartifact.MarshalGenerationVector(left)
	rightBody, rightErr := backupartifact.MarshalGenerationVector(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftBody, rightBody)
}

var _ GenerationVectorCache = (*FileGenerationVectorCache)(nil)
