package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	restorePointPublicationFormat  = "wukongim-cluster-backup-publication"
	restorePointPublicationVersion = 1
	maxPublicationBytes            = 64 << 10
)

// restorePointPublication is a signed commit record created only after the
// identical signed manifest is durably verified in both repositories.
type restorePointPublication struct {
	// Format identifies the publication record schema family.
	Format string `json:"format"`
	// Version selects the publication record schema version.
	Version uint32 `json:"version"`
	// RestorePointID binds the commit record to one restore point.
	RestorePointID string `json:"restore_point_id"`
	// ManifestKey is the immutable staged top-level manifest key.
	ManifestKey string `json:"manifest_key"`
	// ManifestSHA256 binds the commit record to exact manifest bytes.
	ManifestSHA256 string `json:"manifest_sha256"`
	// PrimaryRepository is the operator-facing primary failure-domain identity.
	PrimaryRepository string `json:"primary_repository"`
	// SecondaryRepository is the operator-facing secondary failure-domain identity.
	SecondaryRepository string `json:"secondary_repository"`
	// Signature authenticates every preceding publication field.
	Signature *ManifestSignature `json:"signature,omitempty"`
}

func signRestorePointPublication(ctx context.Context, publication restorePointPublication, signer ManifestSigner, signingKeyID string) (restorePointPublication, error) {
	publication.Signature = nil
	canonical, err := canonicalRestorePointPublication(publication)
	if err != nil {
		return restorePointPublication{}, err
	}
	signature, err := signer.Sign(ctx, signingKeyID, canonical)
	if err != nil {
		return restorePointPublication{}, fmt.Errorf("%w: sign publication: %v", ErrInvalidSignature, err)
	}
	if signature.KeyID != signingKeyID || strings.TrimSpace(signature.Algorithm) == "" || len(signature.Value) == 0 {
		return restorePointPublication{}, fmt.Errorf("%w: publication signer metadata mismatch", ErrInvalidSignature)
	}
	publication.Signature = &signature
	return publication, nil
}

func marshalRestorePointPublication(publication restorePointPublication) ([]byte, error) {
	if err := validateRestorePointPublication(publication, true); err != nil {
		return nil, err
	}
	body, err := json.Marshal(publication)
	if err != nil {
		return nil, fmt.Errorf("marshal restore point publication: %w", err)
	}
	if len(body) > maxPublicationBytes {
		return nil, fmt.Errorf("%w: publication exceeds size limit", ErrInvalidManifest)
	}
	return body, nil
}

func loadRestorePointPublication(ctx context.Context, repository Repository, restorePointID string, signer ManifestSigner) ([]byte, restorePointPublication, error) {
	key := restorePointPublicationKey(restorePointID)
	reader, object, err := repository.Open(ctx, key)
	if err != nil {
		return nil, restorePointPublication{}, err
	}
	defer reader.Close()
	body, err := io.ReadAll(io.LimitReader(reader, maxPublicationBytes+1))
	if err != nil {
		return nil, restorePointPublication{}, err
	}
	hash := sha256.Sum256(body)
	if len(body) == 0 || len(body) > maxPublicationBytes || object.Key != key || object.Size != int64(len(body)) || object.SHA256 != hex.EncodeToString(hash[:]) {
		return nil, restorePointPublication{}, fmt.Errorf("%w: publication object metadata mismatch", ErrRepositoryIncomplete)
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var publication restorePointPublication
	if err := decoder.Decode(&publication); err != nil {
		return nil, restorePointPublication{}, fmt.Errorf("%w: decode publication: %v", ErrInvalidManifest, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, restorePointPublication{}, fmt.Errorf("%w: trailing publication data", ErrInvalidManifest)
	}
	if err := validateRestorePointPublication(publication, true); err != nil {
		return nil, restorePointPublication{}, err
	}
	if publication.RestorePointID != restorePointID || (repository.Name() != publication.PrimaryRepository && repository.Name() != publication.SecondaryRepository) {
		return nil, restorePointPublication{}, fmt.Errorf("%w: publication repository identity mismatch", ErrInvalidManifest)
	}
	canonical, err := canonicalRestorePointPublication(publication)
	if err != nil {
		return nil, restorePointPublication{}, err
	}
	if err := signer.Verify(ctx, *publication.Signature, canonical); err != nil {
		return nil, restorePointPublication{}, fmt.Errorf("%w: verify publication: %v", ErrInvalidSignature, err)
	}
	return body, publication, nil
}

func canonicalRestorePointPublication(publication restorePointPublication) ([]byte, error) {
	publication.Signature = nil
	if err := validateRestorePointPublication(publication, false); err != nil {
		return nil, err
	}
	return json.Marshal(publication)
}

func validateRestorePointPublication(publication restorePointPublication, requireSignature bool) error {
	if publication.Format != restorePointPublicationFormat || publication.Version != restorePointPublicationVersion {
		return fmt.Errorf("%w: publication format or version is unsupported", ErrInvalidManifest)
	}
	if err := validateRestorePointID(publication.RestorePointID); err != nil {
		return fmt.Errorf("%w: publication restore point id: %v", ErrInvalidManifest, err)
	}
	if publication.ManifestKey != restorePointManifestKey(publication.RestorePointID) {
		return fmt.Errorf("%w: publication manifest key mismatch", ErrInvalidManifest)
	}
	if err := validateSHA256(publication.ManifestSHA256); err != nil {
		return fmt.Errorf("%w: publication manifest checksum: %v", ErrInvalidManifest, err)
	}
	primary := strings.TrimSpace(publication.PrimaryRepository)
	secondary := strings.TrimSpace(publication.SecondaryRepository)
	if primary == "" || secondary == "" || primary == secondary || len(primary) > 128 || len(secondary) > 128 {
		return fmt.Errorf("%w: publication repositories are invalid", ErrInvalidManifest)
	}
	if requireSignature && (publication.Signature == nil || strings.TrimSpace(publication.Signature.KeyID) == "" || strings.TrimSpace(publication.Signature.Algorithm) == "" || len(publication.Signature.Value) == 0) {
		return fmt.Errorf("%w: publication signature is required", ErrInvalidSignature)
	}
	return nil
}

func restorePointPublicationKey(restorePointID string) string {
	return "restore-points/" + restorePointID + "/published.json"
}
