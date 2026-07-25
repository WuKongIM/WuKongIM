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
	// GenerationVectorFormat identifies one complete Hash Slot generation map.
	GenerationVectorFormat = "wukongim-backup-generation-vector"
	// GenerationVectorVersion is the current complete-vector schema.
	GenerationVectorVersion uint16 = 1

	maxGenerationVectorBytes = 4 << 20
)

// GenerationVector is one signed, content-addressed complete Slot map.
type GenerationVector struct {
	// Format and Version identify the portable generation-vector schema.
	Format  string `json:"format"`
	Version uint16 `json:"version"`
	// ID is the SHA-256 of the canonical vector identity without its signature.
	ID string `json:"id"`
	// HashSlotCount is the exact vector width.
	HashSlotCount uint16 `json:"hash_slot_count"`
	// Generations maps slice position to Hash Slot.
	Generations []string `json:"generations"`
	// Signature authenticates the complete vector including ID.
	Signature *ManifestSignature `json:"signature,omitempty"`
}

// GenerationVectorReference authenticates one immutable signed vector object.
type GenerationVectorReference struct {
	// ID is the canonical logical vector digest.
	ID string `json:"id"`
	// Key locates the immutable signed vector.
	Key string `json:"key"`
	// SHA256 and Bytes authenticate the exact stored representation.
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
	// HashSlotCount is the exact vector width.
	HashSlotCount uint16 `json:"hash_slot_count"`
}

// NewGenerationVector builds and identifies one unsigned complete vector.
func NewGenerationVector(generations []string) (GenerationVector, error) {
	if len(generations) == 0 || len(generations) > int(^uint16(0)) {
		return GenerationVector{}, fmt.Errorf("%w: generation vector coverage is invalid", ErrInvalidObject)
	}
	vector := GenerationVector{
		Format: GenerationVectorFormat, Version: GenerationVectorVersion,
		HashSlotCount: uint16(len(generations)),
		Generations:   append([]string(nil), generations...),
	}
	id, err := generationVectorIdentity(vector)
	if err != nil {
		return GenerationVector{}, err
	}
	vector.ID = id
	return vector, nil
}

// SignGenerationVector signs one identified complete vector.
func SignGenerationVector(
	ctx context.Context,
	vector GenerationVector,
	signer ManifestSigner,
	keyID string,
) (GenerationVector, error) {
	vector.Signature = nil
	canonical, err := canonicalGenerationVector(vector)
	if err != nil || signer == nil || strings.TrimSpace(keyID) == "" {
		if err != nil {
			return GenerationVector{}, err
		}
		return GenerationVector{}, fmt.Errorf("%w: generation vector signer is required", ErrInvalidSignature)
	}
	signature, err := signer.Sign(ctx, keyID, canonical)
	if err != nil {
		return GenerationVector{}, fmt.Errorf("%w: sign generation vector: %v", ErrInvalidSignature, err)
	}
	if signature.KeyID != keyID || strings.TrimSpace(signature.Algorithm) == "" || len(signature.Value) == 0 {
		return GenerationVector{}, fmt.Errorf("%w: generation vector signer metadata mismatch", ErrInvalidSignature)
	}
	vector.Signature = &signature
	return vector, validateGenerationVector(vector, true)
}

// MarshalGenerationVector serializes one signed complete vector.
func MarshalGenerationVector(vector GenerationVector) ([]byte, error) {
	if err := validateGenerationVector(vector, true); err != nil {
		return nil, err
	}
	body, err := json.Marshal(vector)
	if err != nil || len(body) > maxGenerationVectorBytes {
		return nil, fmt.Errorf("%w: generation vector exceeds encoding limit", ErrInvalidObject)
	}
	return body, nil
}

// LoadGenerationVector strictly decodes and verifies one complete vector.
func LoadGenerationVector(
	ctx context.Context,
	body []byte,
	signer ManifestSigner,
) (GenerationVector, error) {
	var vector GenerationVector
	if signer == nil || strictGenerationVectorJSON(body, &vector) != nil {
		return GenerationVector{}, fmt.Errorf("%w: generation vector encoding is invalid", ErrInvalidObject)
	}
	if err := validateGenerationVector(vector, true); err != nil {
		return GenerationVector{}, err
	}
	signature := *vector.Signature
	canonical, err := canonicalGenerationVector(vector)
	if err != nil {
		return GenerationVector{}, err
	}
	if err := signer.Verify(ctx, signature, canonical); err != nil {
		return GenerationVector{}, fmt.Errorf("%w: verify generation vector: %v", ErrInvalidSignature, err)
	}
	return vector, nil
}

// GenerationVectorObjectKey returns the immutable content-addressed path.
func GenerationVectorObjectKey(id string) string {
	return "generation-vectors/" + id + ".json"
}

func canonicalGenerationVector(vector GenerationVector) ([]byte, error) {
	vector.Signature = nil
	if err := validateGenerationVector(vector, false); err != nil {
		return nil, err
	}
	return json.Marshal(vector)
}

func generationVectorIdentity(vector GenerationVector) (string, error) {
	identity := struct {
		Format        string   `json:"format"`
		Version       uint16   `json:"version"`
		HashSlotCount uint16   `json:"hash_slot_count"`
		Generations   []string `json:"generations"`
	}{
		Format: vector.Format, Version: vector.Version,
		HashSlotCount: vector.HashSlotCount, Generations: vector.Generations,
	}
	if err := validateGenerationVectorGenerations(vector); err != nil {
		return "", err
	}
	body, err := json.Marshal(identity)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:]), nil
}

func validateGenerationVector(vector GenerationVector, requireSignature bool) error {
	if vector.Format != GenerationVectorFormat ||
		vector.Version != GenerationVectorVersion ||
		validateSHA256(vector.ID) != nil ||
		validateGenerationVectorGenerations(vector) != nil {
		return fmt.Errorf("%w: generation vector identity is invalid", ErrInvalidObject)
	}
	expected, err := generationVectorIdentity(vector)
	if err != nil || expected != vector.ID {
		return fmt.Errorf("%w: generation vector digest is invalid", ErrInvalidObject)
	}
	return validateCheckpointSignature(vector.Signature, requireSignature)
}

func validateGenerationVectorGenerations(vector GenerationVector) error {
	if vector.HashSlotCount == 0 ||
		len(vector.Generations) != int(vector.HashSlotCount) {
		return ErrInvalidObject
	}
	for _, generation := range vector.Generations {
		if validateRestorePointID(generation) != nil {
			return ErrInvalidObject
		}
	}
	return nil
}

func validateGenerationVectorReference(reference GenerationVectorReference) error {
	if validateSHA256(reference.ID) != nil ||
		reference.Key != GenerationVectorObjectKey(reference.ID) ||
		validateSHA256(reference.SHA256) != nil ||
		reference.Bytes <= 0 || reference.Bytes > maxGenerationVectorBytes ||
		reference.HashSlotCount == 0 {
		return ErrInvalidObject
	}
	return nil
}

func strictGenerationVectorJSON(body []byte, target *GenerationVector) error {
	if len(body) == 0 || len(body) > maxGenerationVectorBytes {
		return ErrInvalidObject
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ErrInvalidObject
	}
	return nil
}
