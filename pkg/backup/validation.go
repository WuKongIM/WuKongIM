package backup

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"path"
	"strings"
)

func validateBackupIdentity(value string) error {
	if len(value) == 0 || len(value) > 128 {
		return fmt.Errorf("identity length is invalid")
	}
	for index, char := range value {
		if (char >= 'a' && char <= 'z') ||
			(char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') ||
			char == '-' || char == '_' || (char == '.' && index > 0) {
			continue
		}
		return fmt.Errorf("identity %q contains unsafe characters", value)
	}
	if strings.Contains(value, "..") {
		return fmt.Errorf("identity %q contains an unsafe segment", value)
	}
	return nil
}

func validateObjectEntry(object ObjectEntry, index int) error {
	if err := validateObjectKey(object.Key); err != nil {
		return fmt.Errorf("%w: object[%d]: %v", ErrInvalidManifest, index, err)
	}
	switch object.Kind {
	case ObjectKindMetadata, ObjectKindMessages, ObjectKindErasureLedger,
		ObjectKindChannelIndex:
	default:
		return fmt.Errorf(
			"%w: object[%d] kind %q",
			ErrInvalidManifest, index, object.Kind,
		)
	}
	if err := validateSHA256(object.PlaintextSHA256); err != nil {
		return fmt.Errorf(
			"%w: object[%d] plaintext hash: %v",
			ErrInvalidManifest, index, err,
		)
	}
	if err := validateSHA256(object.CiphertextSHA256); err != nil {
		return fmt.Errorf(
			"%w: object[%d] ciphertext hash: %v",
			ErrInvalidManifest, index, err,
		)
	}
	if object.PlaintextBytes < 0 || object.CiphertextBytes <= 0 {
		return fmt.Errorf(
			"%w: object[%d] sizes are invalid",
			ErrInvalidManifest, index,
		)
	}
	if object.Compression != CompressionZstd ||
		object.Encryption != EncryptionAES256GCM {
		return fmt.Errorf(
			"%w: object[%d] codec is unsupported",
			ErrInvalidManifest, index,
		)
	}
	if err := validateDataKeyEnvelope(object.DataKey); err != nil {
		return fmt.Errorf(
			"%w: object[%d] data-key envelope is invalid",
			ErrInvalidManifest, index,
		)
	}
	if _, err := base64.StdEncoding.DecodeString(object.Nonce); err != nil || object.Nonce == "" {
		return fmt.Errorf(
			"%w: object[%d] nonce is invalid",
			ErrInvalidManifest, index,
		)
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
	if key == "" || strings.Contains(key, "\\") ||
		strings.HasPrefix(key, "/") {
		return fmt.Errorf("unsafe key %q", key)
	}
	clean := path.Clean(key)
	if clean != key || clean == "." || clean == ".." ||
		strings.HasPrefix(clean, "../") {
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
