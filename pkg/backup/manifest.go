package backup

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
)

// SignManifest validates and signs a copy of manifest with keyID.
func SignManifest(ctx context.Context, manifest Manifest, signer ManifestSigner, keyID string) (Manifest, error) {
	if signer == nil {
		return Manifest{}, fmt.Errorf("%w: signer is required", ErrInvalidManifest)
	}
	if strings.TrimSpace(keyID) == "" {
		return Manifest{}, fmt.Errorf("%w: signing key id is required", ErrInvalidManifest)
	}
	manifest.Signature = nil
	if err := validateManifest(manifest, false); err != nil {
		return Manifest{}, err
	}
	canonical, err := canonicalUnsignedManifest(manifest)
	if err != nil {
		return Manifest{}, err
	}
	signature, err := signer.Sign(ctx, keyID, canonical)
	if err != nil {
		return Manifest{}, fmt.Errorf("sign manifest: %w", err)
	}
	if strings.TrimSpace(signature.Algorithm) == "" || strings.TrimSpace(signature.KeyID) == "" || len(signature.Value) == 0 {
		return Manifest{}, fmt.Errorf("%w: signer returned incomplete signature", ErrInvalidSignature)
	}
	if signature.KeyID != keyID {
		return Manifest{}, fmt.Errorf("%w: signer key id %q does not match requested key %q", ErrInvalidSignature, signature.KeyID, keyID)
	}
	manifest.Signature = &signature
	if err := validateManifest(manifest, true); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

// MarshalManifest validates and serializes one signed manifest.
func MarshalManifest(manifest Manifest) ([]byte, error) {
	if err := validateManifest(manifest, true); err != nil {
		return nil, err
	}
	body, err := json.Marshal(manifest)
	if err != nil {
		return nil, fmt.Errorf("marshal manifest: %w", err)
	}
	return body, nil
}

// LoadManifest strictly decodes, validates, and verifies one signed manifest.
func LoadManifest(ctx context.Context, body []byte, signer ManifestSigner) (Manifest, error) {
	if signer == nil {
		return Manifest{}, fmt.Errorf("%w: verifier is required", ErrInvalidManifest)
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("%w: decode: %v", ErrInvalidManifest, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Manifest{}, fmt.Errorf("%w: trailing JSON data", ErrInvalidManifest)
	}
	if err := validateManifest(manifest, true); err != nil {
		return Manifest{}, err
	}
	signature := *manifest.Signature
	canonical, err := canonicalUnsignedManifest(manifest)
	if err != nil {
		return Manifest{}, err
	}
	if err := signer.Verify(ctx, signature, canonical); err != nil {
		return Manifest{}, fmt.Errorf("%w: %v", ErrInvalidSignature, err)
	}
	return manifest, nil
}

func canonicalUnsignedManifest(manifest Manifest) ([]byte, error) {
	manifest.Signature = nil
	body, err := json.Marshal(manifest)
	if err != nil {
		return nil, fmt.Errorf("canonical manifest: %w", err)
	}
	return body, nil
}

func validateManifest(manifest Manifest, requireSignature bool) error {
	if manifest.Format != ManifestFormat {
		return fmt.Errorf("%w: format %q", ErrInvalidManifest, manifest.Format)
	}
	if manifest.Version != ManifestVersion {
		return fmt.Errorf("%w: version %d: %w", ErrInvalidManifest, manifest.Version, ErrUnsupportedVersion)
	}
	if strings.TrimSpace(manifest.ApplicationVersion) == "" ||
		strings.TrimSpace(manifest.RepositoryID) == "" ||
		strings.TrimSpace(manifest.SourceClusterID) == "" ||
		strings.TrimSpace(manifest.SourceGeneration) == "" ||
		strings.TrimSpace(manifest.RestorePointID) == "" {
		return fmt.Errorf("%w: manifest identity is incomplete", ErrInvalidManifest)
	}
	if err := validateRestorePointID(manifest.RestorePointID); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidManifest, err)
	}
	if manifest.BackupEpoch == 0 {
		return fmt.Errorf("%w: backup epoch must be nonzero", ErrInvalidManifest)
	}
	switch manifest.Kind {
	case RestorePointIncremental, RestorePointSyntheticFull, RestorePointMaterializedFull:
	default:
		return fmt.Errorf("%w: restore point kind %q", ErrInvalidManifest, manifest.Kind)
	}
	if manifest.HashSlotCount == 0 {
		return fmt.Errorf("%w: hash slot count must be nonzero", ErrInvalidManifest)
	}
	if manifest.CreatedAtUnixMillis <= 0 || manifest.EffectiveAtMillis <= 0 {
		return fmt.Errorf("%w: timestamps must be positive", ErrInvalidManifest)
	}
	if manifest.EffectiveAtMillis > manifest.CreatedAtUnixMillis {
		return fmt.Errorf("%w: effective time is after creation", ErrInvalidManifest)
	}
	if err := validateCuts(manifest); err != nil {
		return err
	}
	if err := validateManifestPayload(manifest); err != nil {
		return err
	}
	if requireSignature {
		if manifest.Signature == nil || strings.TrimSpace(manifest.Signature.Algorithm) == "" || strings.TrimSpace(manifest.Signature.KeyID) == "" || len(manifest.Signature.Value) == 0 {
			return fmt.Errorf("%w: signature is required", ErrInvalidSignature)
		}
	} else if manifest.Signature != nil {
		return fmt.Errorf("%w: unsigned validation received a signature", ErrInvalidManifest)
	}
	return nil
}

func validateManifestPayload(manifest Manifest) error {
	if len(manifest.Partitions) == 0 {
		return validateObjects(manifest)
	}
	if len(manifest.Objects) != 0 {
		return fmt.Errorf("%w: top-level objects and partitions are mutually exclusive", ErrInvalidManifest)
	}
	if len(manifest.Partitions) != int(manifest.HashSlotCount) {
		return fmt.Errorf("%w: partitions=%d want=%d", ErrInvalidManifest, len(manifest.Partitions), manifest.HashSlotCount)
	}
	previousKey := ""
	for index, reference := range manifest.Partitions {
		if reference.HashSlot != uint16(index) || reference.Bytes <= 0 || reference.ObjectCount == 0 || reference.CiphertextBytes == 0 {
			return fmt.Errorf("%w: partition reference[%d] summary is invalid", ErrInvalidManifest, index)
		}
		if err := validatePartitionManifestKey(reference.Key); err != nil {
			return fmt.Errorf("%w: partition reference[%d] key: %v", ErrInvalidManifest, index, err)
		}
		if previousKey != "" && reference.Key <= previousKey {
			return fmt.Errorf("%w: partition reference keys must be sorted", ErrInvalidManifest)
		}
		previousKey = reference.Key
		if err := validateSHA256(reference.SHA256); err != nil {
			return fmt.Errorf("%w: partition reference[%d] checksum: %v", ErrInvalidManifest, index, err)
		}
	}
	return nil
}

func validateRestorePointID(value string) error {
	if len(value) == 0 || len(value) > 128 {
		return fmt.Errorf("restore point id length is invalid")
	}
	for index, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '-' || char == '_' || (char == '.' && index > 0) {
			continue
		}
		return fmt.Errorf("restore point id %q contains unsafe characters", value)
	}
	if strings.Contains(value, "..") {
		return fmt.Errorf("restore point id %q contains an unsafe segment", value)
	}
	return nil
}

func validateCuts(manifest Manifest) error {
	if len(manifest.Cuts) != int(manifest.HashSlotCount) {
		return fmt.Errorf("%w: cuts=%d want=%d", ErrInvalidManifest, len(manifest.Cuts), manifest.HashSlotCount)
	}
	oldest := int64(0)
	for index, cut := range manifest.Cuts {
		if cut.HashSlot != uint16(index) {
			return fmt.Errorf("%w: cut[%d] hash slot=%d", ErrInvalidManifest, index, cut.HashSlot)
		}
		if cut.RaftIndex == 0 || cut.CommittedAtMillis <= 0 {
			return fmt.Errorf("%w: cut[%d] boundary must be positive", ErrInvalidManifest, index)
		}
		if oldest == 0 || cut.CommittedAtMillis < oldest {
			oldest = cut.CommittedAtMillis
		}
	}
	if oldest != manifest.EffectiveAtMillis {
		return fmt.Errorf("%w: effective time=%d want oldest cut=%d", ErrInvalidManifest, manifest.EffectiveAtMillis, oldest)
	}
	return nil
}

func validateObjects(manifest Manifest) error {
	if len(manifest.Objects) == 0 {
		return fmt.Errorf("%w: objects are required", ErrInvalidManifest)
	}
	metadataSlots := make([]bool, manifest.HashSlotCount)
	previousKey := ""
	for index, object := range manifest.Objects {
		if object.HashSlot >= manifest.HashSlotCount {
			return fmt.Errorf("%w: object[%d] hash slot=%d", ErrInvalidManifest, index, object.HashSlot)
		}
		if previousKey != "" && object.Key <= previousKey {
			return fmt.Errorf("%w: object keys must be unique and sorted", ErrInvalidManifest)
		}
		previousKey = object.Key
		if err := validateObjectEntry(object, index); err != nil {
			return err
		}
		switch object.Kind {
		case ObjectKindMetadata:
			metadataSlots[object.HashSlot] = true
		case ObjectKindMessages, ObjectKindErasureLedger, ObjectKindChannelIndex:
		}
	}
	for hashSlot, present := range metadataSlots {
		if !present {
			return fmt.Errorf("%w: metadata object missing for hash slot %d", ErrInvalidManifest, hashSlot)
		}
	}
	return nil
}

func validateObjectEntry(object ObjectEntry, index int) error {
	if err := validateObjectKey(object.Key); err != nil {
		return fmt.Errorf("%w: object[%d]: %v", ErrInvalidManifest, index, err)
	}
	switch object.Kind {
	case ObjectKindMetadata, ObjectKindMessages, ObjectKindErasureLedger, ObjectKindChannelIndex:
	default:
		return fmt.Errorf("%w: object[%d] kind %q", ErrInvalidManifest, index, object.Kind)
	}
	if err := validateSHA256(object.PlaintextSHA256); err != nil {
		return fmt.Errorf("%w: object[%d] plaintext hash: %v", ErrInvalidManifest, index, err)
	}
	if err := validateSHA256(object.CiphertextSHA256); err != nil {
		return fmt.Errorf("%w: object[%d] ciphertext hash: %v", ErrInvalidManifest, index, err)
	}
	if object.PlaintextBytes < 0 || object.CiphertextBytes <= 0 {
		return fmt.Errorf("%w: object[%d] sizes are invalid", ErrInvalidManifest, index)
	}
	if object.Compression != CompressionZstd || object.Encryption != EncryptionAES256GCM {
		return fmt.Errorf("%w: object[%d] codec is unsupported", ErrInvalidManifest, index)
	}
	if strings.TrimSpace(object.KMSKeyID) == "" {
		return fmt.Errorf("%w: object[%d] kms key id is required", ErrInvalidManifest, index)
	}
	if _, err := base64.StdEncoding.DecodeString(object.WrappedKey); err != nil || object.WrappedKey == "" {
		return fmt.Errorf("%w: object[%d] wrapped key is invalid", ErrInvalidManifest, index)
	}
	if _, err := base64.StdEncoding.DecodeString(object.Nonce); err != nil || object.Nonce == "" {
		return fmt.Errorf("%w: object[%d] nonce is invalid", ErrInvalidManifest, index)
	}
	return nil
}

func validateObjectKey(key string) error {
	if err := validateRepositoryKey(key); err != nil {
		return err
	}
	if !strings.HasPrefix(key, "objects/") {
		return fmt.Errorf("key %q is outside objects prefix", key)
	}
	return nil
}

func validatePartitionManifestKey(key string) error {
	if err := validateRepositoryKey(key); err != nil {
		return err
	}
	if !strings.HasPrefix(key, "partition-manifests/") {
		return fmt.Errorf("key %q is outside partition-manifests prefix", key)
	}
	return nil
}

func validateRepositoryKey(key string) error {
	if key == "" || strings.Contains(key, "\\") || strings.HasPrefix(key, "/") {
		return fmt.Errorf("unsafe key %q", key)
	}
	clean := path.Clean(key)
	if clean != key || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("unsafe key %q", key)
	}
	return nil
}

func validateSHA256(value string) error {
	if len(value) != 64 || value != strings.ToLower(value) {
		return fmt.Errorf("must be 64 lowercase hexadecimal characters")
	}
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != 32 {
		return fmt.Errorf("must be a SHA-256 hexadecimal value")
	}
	return nil
}
