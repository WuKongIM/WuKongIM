// Package keypackage implements the protected deployment trust root used by
// backup runtimes and operator tooling.
package keypackage

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

const (
	deploymentKeyPackageSchema       = "wukongim/backup-key-package/v1"
	deploymentDataKeyEnvelopeVersion = uint32(1)
	deploymentDataKeyAlgorithm       = "AES_256_GCM_KW"
	deploymentSignatureAlgorithm     = "ED25519"
	deploymentRecoveryKitSchema      = "wukongim/backup-recovery-kit/v1"
	deploymentRecoveryKitAlgorithm   = "AES_256_GCM"
	maxDeploymentKeyPackageBytes     = 64 << 10
	maxDeploymentRecoveryKitBytes    = 96 << 10
	maxDeploymentProtectedFileBytes  = maxDeploymentRecoveryKitBytes
	// DeploymentKeyPackageCredentialName is the standard systemd/container
	// credential name discovered without application configuration.
	DeploymentKeyPackageCredentialName = "wukongim-backup-key-package"
	// DeploymentKeyPackageFileEnvironment is the explicit file-path fallback
	// for deployment environments that do not expose a credential directory.
	DeploymentKeyPackageFileEnvironment = "WK_BACKUP_KEY_PACKAGE_FILE"
)

// deploymentKeyState is the closed lifecycle vocabulary for package keys.
type deploymentKeyState string

const (
	deploymentKeyStateActive     deploymentKeyState = "active"
	deploymentKeyStatePending    deploymentKeyState = "pending"
	deploymentKeyStateRetained   deploymentKeyState = "retained"
	deploymentKeyStateVerifyOnly deploymentKeyState = "verify_only"
)

// deploymentKeyPackage is the complete protected runtime trust root.
type deploymentKeyPackage struct {
	// Schema identifies the strict deployment package format.
	Schema string `json:"schema"`
	// PackageID binds envelopes to one immutable deployment trust root.
	PackageID string `json:"package_id"`
	// RepositoryID prevents a package from crossing repository boundaries.
	RepositoryID string `json:"repository_id"`
	// Revision advances on each staged or activated rotation.
	Revision uint64 `json:"revision"`
	// AuthenticationKey authenticates every mutable package field.
	AuthenticationKey []byte `json:"authentication_key"`
	// Authentication is the HMAC-SHA256 package integrity value.
	Authentication []byte `json:"authentication"`
	// ActiveWrappingKeyID selects the key used for new data-key envelopes.
	ActiveWrappingKeyID string `json:"active_wrapping_key_id"`
	// WrappingKeys retains every key required by reachable backup history.
	WrappingKeys []deploymentWrappingKey `json:"wrapping_keys"`
	// ActiveSigningKeyID selects the key used for new artifact signatures.
	ActiveSigningKeyID string `json:"active_signing_key_id"`
	// SigningKeys contains the active/pending seeds and historical public keys.
	SigningKeys []deploymentSigningKey `json:"signing_keys"`
}

// deploymentWrappingKey is one versioned AES-256 KEK.
type deploymentWrappingKey struct {
	// ID is the immutable identity stored in each data-key envelope.
	ID string `json:"id"`
	// State controls whether the key writes, awaits activation, or reads only.
	State deploymentKeyState `json:"state"`
	// Material is one AES-256 key-encryption key.
	Material []byte `json:"material"`
}

// deploymentSigningKey is one versioned Ed25519 signer or verifier.
type deploymentSigningKey struct {
	// ID is the SHA-256 identity of PublicKey.
	ID string `json:"id"`
	// State controls signing, pending rollout, or verify-only use.
	State deploymentKeyState `json:"state"`
	// Seed is present only while the key may sign.
	Seed []byte `json:"seed,omitempty"`
	// PublicKey is retained while any reachable artifact may reference it.
	PublicKey []byte `json:"public_key"`
}

// deploymentRecoveryKit seals one exact package revision for offline recovery.
type deploymentRecoveryKit struct {
	// Schema identifies the strict encrypted recovery-kit format.
	Schema string `json:"schema"`
	// PackageID binds ciphertext to one deployment trust root.
	PackageID string `json:"package_id"`
	// RepositoryID binds recovery to one backup repository.
	RepositoryID string `json:"repository_id"`
	// Revision identifies the exact sealed package revision.
	Revision uint64 `json:"revision"`
	// Algorithm identifies recovery authenticated encryption.
	Algorithm string `json:"algorithm"`
	// Nonce is the recovery AEAD nonce.
	Nonce []byte `json:"nonce"`
	// Ciphertext contains the authenticated exact package bytes.
	Ciphertext []byte `json:"ciphertext"`
}

// DeploymentKeyPackageMetadata is the non-secret identity emitted by key
// package lifecycle commands and readiness diagnostics.
type DeploymentKeyPackageMetadata struct {
	// PackageID identifies one immutable deployment trust root.
	PackageID string `json:"package_id"`
	// RepositoryID identifies the only repository the package may open.
	RepositoryID string `json:"repository_id"`
	// Revision is the current staged or activated package revision.
	Revision uint64 `json:"revision"`
	// ActiveWrappingKeyID identifies the KEK used for new object data keys.
	ActiveWrappingKeyID string `json:"active_wrapping_key_id"`
	// ActiveSigningKeyID identifies the key used for new artifact signatures.
	ActiveSigningKeyID string `json:"active_signing_key_id"`
}

// DeploymentKeyAuthority keeps one validated deployment key package in
// read-only memory and performs local envelope and manifest cryptography.
type DeploymentKeyAuthority struct {
	// packageID domain-separates every wrapped data key.
	packageID string
	// repositoryID prevents cross-repository envelope use.
	repositoryID string
	// revision is the validated package lifecycle revision.
	revision uint64
	// activeWrappingKeyID selects the KEK used for new data keys.
	activeWrappingKeyID string
	// activeSigningKeyID selects the private key used for new signatures.
	activeSigningKeyID string
	// staged reports whether this even revision contains one pending key pair.
	staged bool
	// wrappingKeys includes active, pending, and retained read material.
	wrappingKeys map[string][32]byte
	// signingPrivateKeys includes active and pending signing material.
	signingPrivateKeys map[string]ed25519.PrivateKey
	// signingPublicKeys includes every trusted historical verifier.
	signingPublicKeys map[string]ed25519.PublicKey
}

// GenerateDeploymentKeyPackage creates one initial package with independent
// wrapping and signing keys. Secret key material is returned only in body.
func GenerateDeploymentKeyPackage(
	repositoryID string,
) ([]byte, DeploymentKeyPackageMetadata, error) {
	return generateDeploymentKeyPackage(repositoryID, rand.Reader)
}

// InspectDeploymentKeyPackage validates a package and returns only non-secret
// identity metadata suitable for operator output.
func InspectDeploymentKeyPackage(
	packageBody []byte,
) (DeploymentKeyPackageMetadata, error) {
	value, err := decodeValidatedDeploymentKeyPackage(packageBody)
	if err != nil {
		return DeploymentKeyPackageMetadata{}, err
	}
	defer zeroDeploymentKeyPackageSecrets(&value)
	return deploymentMetadata(value), nil
}

// generateDeploymentKeyPackage creates one package from an injected entropy
// source so generation failure paths remain deterministic in tests.
func generateDeploymentKeyPackage(
	repositoryID string,
	random io.Reader,
) ([]byte, DeploymentKeyPackageMetadata, error) {
	repositoryID = strings.TrimSpace(repositoryID)
	if repositoryID == "" {
		return nil, DeploymentKeyPackageMetadata{}, fmt.Errorf(
			"backup deployment keys: repository identity is required",
		)
	}
	if random == nil {
		return nil, DeploymentKeyPackageMetadata{}, fmt.Errorf(
			"backup deployment keys: entropy source is required",
		)
	}
	packageID, err := deploymentRandomID(random, "wkbp-", 16)
	if err != nil {
		return nil, DeploymentKeyPackageMetadata{}, err
	}
	authenticationKey := make([]byte, 32)
	if _, err := io.ReadFull(random, authenticationKey); err != nil {
		return nil, DeploymentKeyPackageMetadata{}, fmt.Errorf(
			"backup deployment keys: generate authentication key: %w", err,
		)
	}
	defer zeroSensitiveBytes(authenticationKey)
	wrappingKey := make([]byte, 32)
	if _, err := io.ReadFull(random, wrappingKey); err != nil {
		return nil, DeploymentKeyPackageMetadata{}, fmt.Errorf(
			"backup deployment keys: generate wrapping key: %w", err,
		)
	}
	defer zeroSensitiveBytes(wrappingKey)
	wrappingKeyID := deploymentWrappingKeyID(wrappingKey)
	seed := make([]byte, ed25519.SeedSize)
	if _, err := io.ReadFull(random, seed); err != nil {
		return nil, DeploymentKeyPackageMetadata{}, fmt.Errorf(
			"backup deployment keys: generate signing key: %w", err,
		)
	}
	defer zeroSensitiveBytes(seed)
	privateKey := ed25519.NewKeyFromSeed(seed)
	publicKey := privateKey.Public().(ed25519.PublicKey)
	signingKeyID := deploymentSigningKeyID(publicKey)

	value := deploymentKeyPackage{
		Schema:              deploymentKeyPackageSchema,
		PackageID:           packageID,
		RepositoryID:        repositoryID,
		Revision:            1,
		AuthenticationKey:   append([]byte(nil), authenticationKey...),
		ActiveWrappingKeyID: wrappingKeyID,
		WrappingKeys: []deploymentWrappingKey{{
			ID: wrappingKeyID, State: deploymentKeyStateActive,
			Material: append([]byte(nil), wrappingKey...),
		}},
		ActiveSigningKeyID: signingKeyID,
		SigningKeys: []deploymentSigningKey{{
			ID: signingKeyID, State: deploymentKeyStateActive,
			Seed:      append([]byte(nil), seed...),
			PublicKey: append([]byte(nil), publicKey...),
		}},
	}
	defer zeroDeploymentKeyPackageSecrets(&value)
	return encodeDeploymentKeyPackage(value)
}

// SealDeploymentRecoveryKit encrypts one exact deployment key package under a
// new independent recovery key. The caller must store the kit and key apart.
func SealDeploymentRecoveryKit(
	packageBody []byte,
) (
	[]byte,
	[]byte,
	DeploymentKeyPackageMetadata,
	error,
) {
	recoveryKey := make([]byte, 32)
	if _, err := rand.Read(recoveryKey); err != nil {
		return nil, nil, DeploymentKeyPackageMetadata{}, fmt.Errorf(
			"backup deployment keys: generate recovery key: %w", err,
		)
	}
	kit, metadata, err := sealDeploymentRecoveryKitWithKey(
		packageBody, recoveryKey,
	)
	if err != nil {
		zeroSensitiveBytes(recoveryKey)
		return nil, nil, DeploymentKeyPackageMetadata{}, err
	}
	return kit, recoveryKey, metadata, nil
}

// RefreshDeploymentRecoveryKit encrypts an updated package under an existing
// offline recovery key without exposing package material in command output.
func RefreshDeploymentRecoveryKit(
	packageBody []byte,
	recoveryKey []byte,
) ([]byte, DeploymentKeyPackageMetadata, error) {
	return sealDeploymentRecoveryKitWithKey(packageBody, recoveryKey)
}

// StageDeploymentKeyRotation adds one pending wrapping and signing key while
// leaving the current active keys unchanged for safe rolling distribution.
func StageDeploymentKeyRotation(
	packageBody []byte,
) ([]byte, DeploymentKeyPackageMetadata, error) {
	value, err := decodeValidatedDeploymentKeyPackage(packageBody)
	if err != nil {
		return nil, DeploymentKeyPackageMetadata{}, err
	}
	defer zeroDeploymentKeyPackageSecrets(&value)
	for _, key := range value.WrappingKeys {
		if key.State == deploymentKeyStatePending {
			return nil, DeploymentKeyPackageMetadata{}, fmt.Errorf(
				"backup deployment keys: wrapping-key rotation is already staged",
			)
		}
	}
	for _, key := range value.SigningKeys {
		if key.State == deploymentKeyStatePending {
			return nil, DeploymentKeyPackageMetadata{}, fmt.Errorf(
				"backup deployment keys: signing-key rotation is already staged",
			)
		}
	}
	wrappingKey := make([]byte, 32)
	if _, err := rand.Read(wrappingKey); err != nil {
		return nil, DeploymentKeyPackageMetadata{}, fmt.Errorf(
			"backup deployment keys: generate pending wrapping key: %w", err,
		)
	}
	defer zeroSensitiveBytes(wrappingKey)
	wrappingKeyID := deploymentWrappingKeyID(wrappingKey)
	seed := make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(seed); err != nil {
		return nil, DeploymentKeyPackageMetadata{}, fmt.Errorf(
			"backup deployment keys: generate pending signing key: %w", err,
		)
	}
	defer zeroSensitiveBytes(seed)
	publicKey := ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)
	signingKeyID := deploymentSigningKeyID(publicKey)
	value.WrappingKeys = append(
		value.WrappingKeys,
		deploymentWrappingKey{
			ID:       wrappingKeyID,
			State:    deploymentKeyStatePending,
			Material: append([]byte(nil), wrappingKey...),
		},
	)
	value.SigningKeys = append(
		value.SigningKeys,
		deploymentSigningKey{
			ID:        signingKeyID,
			State:     deploymentKeyStatePending,
			Seed:      append([]byte(nil), seed...),
			PublicKey: append([]byte(nil), publicKey...),
		},
	)
	value.Revision++
	return encodeDeploymentKeyPackage(value)
}

// ActivateDeploymentKeyRotation promotes the single staged key pair, retaining
// old wrapping material for reads and only the old signing public key.
func ActivateDeploymentKeyRotation(
	packageBody []byte,
) ([]byte, DeploymentKeyPackageMetadata, error) {
	value, err := decodeValidatedDeploymentKeyPackage(packageBody)
	if err != nil {
		return nil, DeploymentKeyPackageMetadata{}, err
	}
	defer zeroDeploymentKeyPackageSecrets(&value)
	pendingWrapping := -1
	for index := range value.WrappingKeys {
		switch value.WrappingKeys[index].State {
		case deploymentKeyStateActive:
			value.WrappingKeys[index].State = deploymentKeyStateRetained
		case deploymentKeyStatePending:
			if pendingWrapping >= 0 {
				return nil, DeploymentKeyPackageMetadata{}, fmt.Errorf(
					"backup deployment keys: multiple pending wrapping keys",
				)
			}
			pendingWrapping = index
		}
	}
	pendingSigning := -1
	for index := range value.SigningKeys {
		switch value.SigningKeys[index].State {
		case deploymentKeyStateActive:
			value.SigningKeys[index].State = deploymentKeyStateVerifyOnly
			zeroSensitiveBytes(value.SigningKeys[index].Seed)
			value.SigningKeys[index].Seed = nil
		case deploymentKeyStatePending:
			if pendingSigning >= 0 {
				return nil, DeploymentKeyPackageMetadata{}, fmt.Errorf(
					"backup deployment keys: multiple pending signing keys",
				)
			}
			pendingSigning = index
		}
	}
	if pendingWrapping < 0 || pendingSigning < 0 {
		return nil, DeploymentKeyPackageMetadata{}, fmt.Errorf(
			"backup deployment keys: staged wrapping and signing keys are required",
		)
	}
	value.WrappingKeys[pendingWrapping].State = deploymentKeyStateActive
	value.ActiveWrappingKeyID = value.WrappingKeys[pendingWrapping].ID
	value.SigningKeys[pendingSigning].State = deploymentKeyStateActive
	value.ActiveSigningKeyID = value.SigningKeys[pendingSigning].ID
	value.Revision++
	return encodeDeploymentKeyPackage(value)
}

// sealDeploymentRecoveryKitWithKey authenticates an already validated package
// under an independently stored recovery key.
func sealDeploymentRecoveryKitWithKey(
	packageBody []byte,
	recoveryKey []byte,
) ([]byte, DeploymentKeyPackageMetadata, error) {
	if len(recoveryKey) != 32 {
		return nil, DeploymentKeyPackageMetadata{}, fmt.Errorf(
			"backup deployment keys: recovery key must be 32 bytes",
		)
	}
	value, err := decodeValidatedDeploymentKeyPackage(packageBody)
	if err != nil {
		return nil, DeploymentKeyPackageMetadata{}, err
	}
	defer zeroDeploymentKeyPackageSecrets(&value)
	block, err := aes.NewCipher(recoveryKey)
	if err != nil {
		return nil, DeploymentKeyPackageMetadata{}, fmt.Errorf(
			"backup deployment keys: create recovery cipher: %w", err,
		)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, DeploymentKeyPackageMetadata{}, fmt.Errorf(
			"backup deployment keys: create recovery AEAD: %w", err,
		)
	}
	kit := deploymentRecoveryKit{
		Schema:       deploymentRecoveryKitSchema,
		PackageID:    value.PackageID,
		RepositoryID: value.RepositoryID,
		Revision:     value.Revision,
		Algorithm:    deploymentRecoveryKitAlgorithm,
		Nonce:        make([]byte, aead.NonceSize()),
	}
	if _, err := rand.Read(kit.Nonce); err != nil {
		return nil, DeploymentKeyPackageMetadata{}, fmt.Errorf(
			"backup deployment keys: generate recovery nonce: %w", err,
		)
	}
	kit.Ciphertext = aead.Seal(
		nil, kit.Nonce, packageBody, deploymentRecoveryAssociatedData(kit),
	)
	body, err := json.Marshal(kit)
	if err != nil {
		return nil, DeploymentKeyPackageMetadata{}, fmt.Errorf(
			"backup deployment keys: encode recovery kit: %w", err,
		)
	}
	return append(body, '\n'), deploymentMetadata(value), nil
}

// OpenDeploymentRecoveryKit authenticates one recovery kit and restores the
// exact package bytes that were sealed into it.
func OpenDeploymentRecoveryKit(
	kitBody []byte,
	recoveryKey []byte,
) (
	[]byte,
	DeploymentKeyPackageMetadata,
	error,
) {
	if len(recoveryKey) != 32 {
		return nil, DeploymentKeyPackageMetadata{}, fmt.Errorf(
			"backup deployment keys: recovery key must be 32 bytes",
		)
	}
	kit, err := decodeDeploymentRecoveryKit(kitBody)
	if err != nil {
		return nil, DeploymentKeyPackageMetadata{}, err
	}
	block, err := aes.NewCipher(recoveryKey)
	if err != nil {
		return nil, DeploymentKeyPackageMetadata{}, fmt.Errorf(
			"backup deployment keys: create recovery cipher: %w", err,
		)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, DeploymentKeyPackageMetadata{}, fmt.Errorf(
			"backup deployment keys: create recovery AEAD: %w", err,
		)
	}
	if len(kit.Nonce) != aead.NonceSize() || len(kit.Ciphertext) == 0 {
		return nil, DeploymentKeyPackageMetadata{}, fmt.Errorf(
			"backup deployment keys: recovery kit is incomplete",
		)
	}
	packageBody, err := aead.Open(
		nil,
		kit.Nonce,
		kit.Ciphertext,
		deploymentRecoveryAssociatedData(kit),
	)
	if err != nil {
		return nil, DeploymentKeyPackageMetadata{}, fmt.Errorf(
			"backup deployment keys: recovery authentication failed",
		)
	}
	value, err := decodeValidatedDeploymentKeyPackage(packageBody)
	if err != nil {
		zeroSensitiveBytes(packageBody)
		return nil, DeploymentKeyPackageMetadata{}, fmt.Errorf(
			"backup deployment keys: recovered package is invalid: %w", err,
		)
	}
	defer zeroDeploymentKeyPackageSecrets(&value)
	if value.PackageID != kit.PackageID ||
		value.RepositoryID != kit.RepositoryID ||
		value.Revision != kit.Revision {
		zeroSensitiveBytes(packageBody)
		return nil, DeploymentKeyPackageMetadata{}, fmt.Errorf(
			"backup deployment keys: recovered package metadata mismatch",
		)
	}
	return packageBody, deploymentMetadata(value), nil
}

// OpenDeploymentKeyAuthority strictly decodes and validates one key package
// against the configured repository identity.
func OpenDeploymentKeyAuthority(
	body []byte,
	repositoryID string,
) (*DeploymentKeyAuthority, error) {
	repositoryID = strings.TrimSpace(repositoryID)
	if repositoryID == "" {
		return nil, fmt.Errorf(
			"backup deployment keys: repository identity is required",
		)
	}
	value, err := decodeDeploymentKeyPackage(body)
	if err != nil {
		return nil, err
	}
	defer zeroDeploymentKeyPackageSecrets(&value)
	return openDeploymentKeyAuthority(value, repositoryID)
}

// openDeploymentKeyAuthority validates one decoded package and copies only the
// key material required by the read-only runtime authority.
func openDeploymentKeyAuthority(
	value deploymentKeyPackage,
	repositoryID string,
) (*DeploymentKeyAuthority, error) {
	if value.RepositoryID != repositoryID {
		return nil, fmt.Errorf(
			"backup deployment keys: repository identity mismatch",
		)
	}
	authority := &DeploymentKeyAuthority{
		packageID:           value.PackageID,
		repositoryID:        value.RepositoryID,
		revision:            value.Revision,
		activeWrappingKeyID: value.ActiveWrappingKeyID,
		activeSigningKeyID:  value.ActiveSigningKeyID,
		wrappingKeys:        make(map[string][32]byte, len(value.WrappingKeys)),
		signingPrivateKeys:  make(map[string]ed25519.PrivateKey, len(value.SigningKeys)),
		signingPublicKeys:   make(map[string]ed25519.PublicKey, len(value.SigningKeys)),
	}
	valid := false
	defer func() {
		if !valid {
			zeroDeploymentKeyAuthority(authority)
		}
	}()
	activeWrapping := 0
	activeWrappingKeyID := ""
	pendingWrapping := 0
	for _, key := range value.WrappingKeys {
		if err := authority.addWrappingKey(key); err != nil {
			return nil, err
		}
		if key.State == deploymentKeyStateActive {
			activeWrapping++
			activeWrappingKeyID = strings.TrimSpace(key.ID)
		} else if key.State == deploymentKeyStatePending {
			pendingWrapping++
		}
	}
	activeSigning := 0
	activeSigningKeyID := ""
	pendingSigning := 0
	for _, key := range value.SigningKeys {
		if err := authority.addSigningKey(key); err != nil {
			return nil, err
		}
		if key.State == deploymentKeyStateActive {
			activeSigning++
			activeSigningKeyID = strings.TrimSpace(key.ID)
		} else if key.State == deploymentKeyStatePending {
			pendingSigning++
		}
	}
	if activeWrapping != 1 ||
		activeWrappingKeyID != value.ActiveWrappingKeyID {
		return nil, fmt.Errorf(
			"backup deployment keys: exactly one active wrapping key is required",
		)
	}
	if activeSigning != 1 ||
		activeSigningKeyID != value.ActiveSigningKeyID {
		return nil, fmt.Errorf(
			"backup deployment keys: exactly one active signing key is required",
		)
	}
	if pendingWrapping != pendingSigning || pendingWrapping > 1 ||
		(value.Revision%2 == 0) != (pendingWrapping == 1) {
		return nil, fmt.Errorf(
			"backup deployment keys: package lifecycle revision is invalid",
		)
	}
	authority.staged = pendingWrapping == 1
	if err := authority.Check(context.Background()); err != nil {
		return nil, err
	}
	valid = true
	return authority, nil
}

// LoadDeploymentKeyAuthority discovers the standard protected credential and
// opens it for one exact backup repository.
func LoadDeploymentKeyAuthority(
	ctx context.Context,
	repositoryID string,
) (*DeploymentKeyAuthority, error) {
	if ctx == nil {
		return nil, fmt.Errorf(
			"backup deployment keys: context is required",
		)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path, err := deploymentKeyPackagePath()
	if err != nil {
		return nil, err
	}
	body, err := readProtectedDeploymentKeyPackage(path)
	if err != nil {
		return nil, err
	}
	defer zeroSensitiveBytes(body)
	return OpenDeploymentKeyAuthority(body, repositoryID)
}

// NewDataKey returns one fresh AES-256 data key wrapped under the active KEK.
func (a *DeploymentKeyAuthority) NewDataKey(
	ctx context.Context,
) (backupartifact.DataKey, error) {
	if err := deploymentAuthorityContext(ctx, a); err != nil {
		return backupartifact.DataKey{}, err
	}
	plaintext := make([]byte, 32)
	if _, err := rand.Read(plaintext); err != nil {
		return backupartifact.DataKey{}, fmt.Errorf(
			"backup deployment keys: generate data key: %w", err,
		)
	}
	envelope, err := a.wrapDataKey(plaintext)
	if err != nil {
		zeroSensitiveBytes(plaintext)
		return backupartifact.DataKey{}, err
	}
	return backupartifact.DataKey{
		Plaintext: plaintext,
		Envelope:  envelope,
	}, nil
}

// OpenDataKey authenticates and unwraps one exact deployment envelope.
func (a *DeploymentKeyAuthority) OpenDataKey(
	ctx context.Context,
	envelope backupartifact.DataKeyEnvelope,
) ([]byte, error) {
	if err := deploymentAuthorityContext(ctx, a); err != nil {
		return nil, err
	}
	if envelope.Version != deploymentDataKeyEnvelopeVersion ||
		envelope.Algorithm != deploymentDataKeyAlgorithm {
		return nil, fmt.Errorf(
			"backup deployment keys: unsupported data-key envelope",
		)
	}
	key, ok := a.wrappingKeys[strings.TrimSpace(envelope.KeyID)]
	if !ok {
		return nil, fmt.Errorf(
			"backup deployment keys: wrapping key %q is not retained",
			envelope.KeyID,
		)
	}
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, fmt.Errorf(
			"backup deployment keys: open wrapping cipher: %w", err,
		)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf(
			"backup deployment keys: open wrapping AEAD: %w", err,
		)
	}
	if len(envelope.Nonce) != aead.NonceSize() || len(envelope.Value) == 0 {
		return nil, fmt.Errorf(
			"backup deployment keys: data-key envelope is incomplete",
		)
	}
	plaintext, err := aead.Open(
		nil, envelope.Nonce, envelope.Value,
		a.wrappingAssociatedData(envelope.KeyID),
	)
	if err != nil || len(plaintext) != 32 {
		zeroSensitiveBytes(plaintext)
		return nil, fmt.Errorf(
			"backup deployment keys: data-key authentication failed",
		)
	}
	return plaintext, nil
}

// Sign authenticates canonical artifact bytes with the active Ed25519 key.
func (a *DeploymentKeyAuthority) Sign(
	ctx context.Context,
	message []byte,
) (backupartifact.ManifestSignature, error) {
	if err := deploymentAuthorityContext(ctx, a); err != nil {
		return backupartifact.ManifestSignature{}, err
	}
	privateKey := a.signingPrivateKeys[a.activeSigningKeyID]
	if len(privateKey) != ed25519.PrivateKeySize {
		return backupartifact.ManifestSignature{}, fmt.Errorf(
			"backup deployment keys: active signing key is unavailable",
		)
	}
	return backupartifact.ManifestSignature{
		Algorithm: deploymentSignatureAlgorithm,
		KeyID:     a.activeSigningKeyID,
		Value:     ed25519.Sign(privateKey, message),
	}, nil
}

// Verify authenticates canonical artifact bytes against the retained keyring.
func (a *DeploymentKeyAuthority) Verify(
	ctx context.Context,
	signature backupartifact.ManifestSignature,
	message []byte,
) error {
	if err := deploymentAuthorityContext(ctx, a); err != nil {
		return err
	}
	if signature.Algorithm != deploymentSignatureAlgorithm {
		return fmt.Errorf(
			"backup deployment keys: unsupported manifest signature",
		)
	}
	publicKey, ok := a.signingPublicKeys[strings.TrimSpace(signature.KeyID)]
	if !ok || !ed25519.Verify(publicKey, message, signature.Value) {
		return fmt.Errorf(
			"backup deployment keys: manifest signature verification failed",
		)
	}
	return nil
}

// Check proves local wrapping and signing readiness without external I/O.
func (a *DeploymentKeyAuthority) Check(ctx context.Context) error {
	if err := deploymentAuthorityContext(ctx, a); err != nil {
		return err
	}
	dataKey, err := a.NewDataKey(ctx)
	if err != nil {
		return err
	}
	defer zeroSensitiveBytes(dataKey.Plaintext)
	unwrapped, err := a.OpenDataKey(ctx, dataKey.Envelope)
	if err != nil {
		return err
	}
	matched := bytes.Equal(dataKey.Plaintext, unwrapped)
	zeroSensitiveBytes(unwrapped)
	if !matched {
		return fmt.Errorf(
			"backup deployment keys: envelope round trip failed",
		)
	}
	probe := []byte("wukongim-backup-deployment-key-doctor-v1")
	signature, err := a.Sign(ctx, probe)
	if err != nil {
		return err
	}
	return a.Verify(ctx, signature, probe)
}

// wrapDataKey seals one plaintext DEK under the active package KEK.
func (a *DeploymentKeyAuthority) wrapDataKey(
	plaintext []byte,
) (backupartifact.DataKeyEnvelope, error) {
	key := a.wrappingKeys[a.activeWrappingKeyID]
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return backupartifact.DataKeyEnvelope{}, fmt.Errorf(
			"backup deployment keys: create wrapping cipher: %w", err,
		)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return backupartifact.DataKeyEnvelope{}, fmt.Errorf(
			"backup deployment keys: create wrapping AEAD: %w", err,
		)
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return backupartifact.DataKeyEnvelope{}, fmt.Errorf(
			"backup deployment keys: generate wrapping nonce: %w", err,
		)
	}
	return backupartifact.DataKeyEnvelope{
		Version:   deploymentDataKeyEnvelopeVersion,
		Algorithm: deploymentDataKeyAlgorithm,
		KeyID:     a.activeWrappingKeyID,
		Nonce:     nonce,
		Value: aead.Seal(
			nil, nonce, plaintext,
			a.wrappingAssociatedData(a.activeWrappingKeyID),
		),
	}, nil
}

func (a *DeploymentKeyAuthority) wrappingAssociatedData(
	keyID string,
) []byte {
	return []byte(
		deploymentKeyPackageSchema + "\x00" + a.packageID + "\x00" +
			a.repositoryID + "\x00" + strings.TrimSpace(keyID) +
			"\x00object-data-key",
	)
}

// addWrappingKey accepts one unique material-bound AES-256 key identity and
// copies it into fixed-size authority storage.
func (a *DeploymentKeyAuthority) addWrappingKey(
	key deploymentWrappingKey,
) error {
	key.ID = strings.TrimSpace(key.ID)
	if key.ID == "" ||
		(key.State != deploymentKeyStateActive &&
			key.State != deploymentKeyStateRetained &&
			key.State != deploymentKeyStatePending) {
		return fmt.Errorf(
			"backup deployment keys: invalid wrapping key metadata",
		)
	}
	if _, exists := a.wrappingKeys[key.ID]; exists {
		return fmt.Errorf(
			"backup deployment keys: duplicate wrapping key %q", key.ID,
		)
	}
	if len(key.Material) != 32 {
		return fmt.Errorf(
			"backup deployment keys: wrapping key %q is not AES-256", key.ID,
		)
	}
	if deploymentWrappingKeyID(key.Material) != key.ID {
		return fmt.Errorf(
			"backup deployment keys: wrapping key identity mismatch",
		)
	}
	var fixed [32]byte
	copy(fixed[:], key.Material)
	a.wrappingKeys[key.ID] = fixed
	return nil
}

// addSigningKey accepts one material-bound Ed25519 identity and proves any
// retained private seed corresponds to its public verifier.
func (a *DeploymentKeyAuthority) addSigningKey(
	key deploymentSigningKey,
) error {
	key.ID = strings.TrimSpace(key.ID)
	if key.ID == "" ||
		(key.State != deploymentKeyStateActive &&
			key.State != deploymentKeyStateVerifyOnly &&
			key.State != deploymentKeyStatePending) {
		return fmt.Errorf(
			"backup deployment keys: invalid signing key metadata",
		)
	}
	if _, exists := a.signingPublicKeys[key.ID]; exists {
		return fmt.Errorf(
			"backup deployment keys: duplicate signing key %q", key.ID,
		)
	}
	if len(key.PublicKey) != ed25519.PublicKeySize {
		return fmt.Errorf(
			"backup deployment keys: signing public key %q is invalid", key.ID,
		)
	}
	if deploymentSigningKeyID(key.PublicKey) != key.ID {
		return fmt.Errorf(
			"backup deployment keys: signing key identity mismatch",
		)
	}
	a.signingPublicKeys[key.ID] = append(
		ed25519.PublicKey(nil), key.PublicKey...,
	)
	if key.State == deploymentKeyStateVerifyOnly {
		if len(key.Seed) != 0 {
			return fmt.Errorf(
				"backup deployment keys: verify-only key contains private material",
			)
		}
		return nil
	}
	if len(key.Seed) != ed25519.SeedSize {
		return fmt.Errorf(
			"backup deployment keys: active signing seed is invalid",
		)
	}
	privateKey := ed25519.NewKeyFromSeed(key.Seed)
	if !bytes.Equal(
		privateKey.Public().(ed25519.PublicKey), key.PublicKey,
	) {
		zeroSensitiveBytes(privateKey)
		return fmt.Errorf(
			"backup deployment keys: signing public/private key mismatch",
		)
	}
	a.signingPrivateKeys[key.ID] = privateKey
	return nil
}

func encodeDeploymentKeyPackage(
	value deploymentKeyPackage,
) ([]byte, DeploymentKeyPackageMetadata, error) {
	if len(value.AuthenticationKey) != sha256.Size {
		return nil, DeploymentKeyPackageMetadata{}, fmt.Errorf(
			"backup deployment keys: authentication key is invalid",
		)
	}
	authentication, err := deploymentKeyPackageAuthentication(value)
	if err != nil {
		return nil, DeploymentKeyPackageMetadata{}, err
	}
	defer zeroSensitiveBytes(authentication)
	value.Authentication = authentication
	body, err := json.Marshal(value)
	if err != nil {
		return nil, DeploymentKeyPackageMetadata{}, fmt.Errorf(
			"backup deployment keys: encode package: %w", err,
		)
	}
	return append(body, '\n'), deploymentMetadata(value), nil
}

func decodeDeploymentKeyPackage(
	body []byte,
) (deploymentKeyPackage, error) {
	if len(body) == 0 || len(body) > maxDeploymentKeyPackageBytes {
		return deploymentKeyPackage{}, fmt.Errorf(
			"backup deployment keys: package size is invalid",
		)
	}
	var value deploymentKeyPackage
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		zeroDeploymentKeyPackageSecrets(&value)
		return deploymentKeyPackage{}, fmt.Errorf(
			"backup deployment keys: decode package: %w", err,
		)
	}
	if err := requireDeploymentJSONEOF(decoder); err != nil {
		zeroDeploymentKeyPackageSecrets(&value)
		return deploymentKeyPackage{}, err
	}
	if len(value.AuthenticationKey) != sha256.Size ||
		len(value.Authentication) != sha256.Size {
		zeroDeploymentKeyPackageSecrets(&value)
		return deploymentKeyPackage{}, fmt.Errorf(
			"backup deployment keys: package authentication is invalid",
		)
	}
	expectedAuthentication, err := deploymentKeyPackageAuthentication(value)
	if err != nil {
		zeroDeploymentKeyPackageSecrets(&value)
		return deploymentKeyPackage{}, err
	}
	authenticated := hmac.Equal(
		expectedAuthentication,
		value.Authentication,
	)
	zeroSensitiveBytes(expectedAuthentication)
	if !authenticated {
		zeroDeploymentKeyPackageSecrets(&value)
		return deploymentKeyPackage{}, fmt.Errorf(
			"backup deployment keys: package authentication failed",
		)
	}
	value.Schema = strings.TrimSpace(value.Schema)
	value.PackageID = strings.TrimSpace(value.PackageID)
	value.RepositoryID = strings.TrimSpace(value.RepositoryID)
	value.ActiveWrappingKeyID = strings.TrimSpace(value.ActiveWrappingKeyID)
	value.ActiveSigningKeyID = strings.TrimSpace(value.ActiveSigningKeyID)
	if value.Schema != deploymentKeyPackageSchema ||
		value.PackageID == "" || value.RepositoryID == "" ||
		value.Revision == 0 || len(value.WrappingKeys) == 0 ||
		len(value.SigningKeys) == 0 {
		zeroDeploymentKeyPackageSecrets(&value)
		return deploymentKeyPackage{}, fmt.Errorf(
			"backup deployment keys: package metadata is invalid",
		)
	}
	return value, nil
}

// decodeValidatedDeploymentKeyPackage authenticates and semantically validates
// one package exactly once. The caller owns and must zero the returned secrets.
func decodeValidatedDeploymentKeyPackage(
	body []byte,
) (deploymentKeyPackage, error) {
	value, err := decodeDeploymentKeyPackage(body)
	if err != nil {
		return deploymentKeyPackage{}, err
	}
	authority, err := openDeploymentKeyAuthority(value, value.RepositoryID)
	if err != nil {
		zeroDeploymentKeyPackageSecrets(&value)
		return deploymentKeyPackage{}, err
	}
	zeroDeploymentKeyAuthority(authority)
	return value, nil
}

// deploymentKeyPackageAuthentication computes the canonical HMAC over every
// package field except the authentication key and authentication value.
func deploymentKeyPackageAuthentication(
	value deploymentKeyPackage,
) ([]byte, error) {
	authenticationKey := value.AuthenticationKey
	value.AuthenticationKey = nil
	value.Authentication = nil
	canonical, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf(
			"backup deployment keys: encode package authentication: %w", err,
		)
	}
	defer zeroSensitiveBytes(canonical)
	mac := hmac.New(sha256.New, authenticationKey)
	if _, err := mac.Write(canonical); err != nil {
		return nil, fmt.Errorf(
			"backup deployment keys: authenticate package: %w", err,
		)
	}
	return mac.Sum(nil), nil
}

func decodeDeploymentRecoveryKit(
	body []byte,
) (deploymentRecoveryKit, error) {
	if len(body) == 0 || len(body) > maxDeploymentRecoveryKitBytes {
		return deploymentRecoveryKit{}, fmt.Errorf(
			"backup deployment keys: recovery kit size is invalid",
		)
	}
	var value deploymentRecoveryKit
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return deploymentRecoveryKit{}, fmt.Errorf(
			"backup deployment keys: decode recovery kit: %w", err,
		)
	}
	if err := requireDeploymentJSONEOF(decoder); err != nil {
		return deploymentRecoveryKit{}, err
	}
	value.Schema = strings.TrimSpace(value.Schema)
	value.PackageID = strings.TrimSpace(value.PackageID)
	value.RepositoryID = strings.TrimSpace(value.RepositoryID)
	value.Algorithm = strings.TrimSpace(value.Algorithm)
	if value.Schema != deploymentRecoveryKitSchema ||
		value.PackageID == "" ||
		value.RepositoryID == "" ||
		value.Revision == 0 ||
		value.Algorithm != deploymentRecoveryKitAlgorithm {
		return deploymentRecoveryKit{}, fmt.Errorf(
			"backup deployment keys: recovery kit metadata is invalid",
		)
	}
	return value, nil
}

func deploymentRecoveryAssociatedData(
	kit deploymentRecoveryKit,
) []byte {
	return []byte(
		kit.Schema + "\x00" +
			kit.PackageID + "\x00" +
			kit.RepositoryID + "\x00" +
			fmt.Sprintf("%d", kit.Revision) + "\x00" +
			kit.Algorithm,
	)
}

func deploymentKeyPackagePath() (string, error) {
	if directory := strings.TrimSpace(
		os.Getenv("CREDENTIALS_DIRECTORY"),
	); directory != "" {
		if !filepath.IsAbs(directory) {
			return "", fmt.Errorf(
				"backup deployment keys: credential directory must be absolute",
			)
		}
		return filepath.Join(
			directory, DeploymentKeyPackageCredentialName,
		), nil
	}
	const containerSecretDirectory = "/run/secrets/wukongim"
	containerPath := filepath.Join(
		containerSecretDirectory, DeploymentKeyPackageCredentialName,
	)
	if _, err := os.Lstat(containerPath); err == nil {
		return containerPath, nil
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf(
			"backup deployment keys: inspect container credential: %w", err,
		)
	}
	if explicit := strings.TrimSpace(
		os.Getenv(DeploymentKeyPackageFileEnvironment),
	); explicit != "" {
		if !filepath.IsAbs(explicit) {
			return "", fmt.Errorf(
				"backup deployment keys: package file must be absolute",
			)
		}
		return explicit, nil
	}
	return "", fmt.Errorf(
		"backup deployment keys: protected credential %q is unavailable",
		DeploymentKeyPackageCredentialName,
	)
}

// readProtectedDeploymentKeyPackage rejects links, broad permissions, file
// replacement races, and oversized credentials before returning secret bytes.
func readProtectedDeploymentKeyPackage(path string) ([]byte, error) {
	body, err := ReadProtectedDeploymentFile(
		path, maxDeploymentKeyPackageBytes,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"backup deployment keys: protected credential: %w", err,
		)
	}
	return body, nil
}

// ReadProtectedDeploymentFile returns one stable private regular-file
// snapshot. It rejects link replacement, in-place metadata changes, broad
// permissions, and command-specific size violations.
func ReadProtectedDeploymentFile(
	path string,
	maxBytes int64,
) ([]byte, error) {
	path = strings.TrimSpace(path)
	if path == "" ||
		maxBytes <= 0 ||
		maxBytes > maxDeploymentProtectedFileBytes {
		return nil, fmt.Errorf("protected file bounds are invalid")
	}
	before, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect protected file: %w", err)
	}
	if err := validateProtectedDeploymentFileInfo(
		before, maxBytes,
	); err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open protected file: %w", err)
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat protected file: %w", err)
	}
	if !sameProtectedDeploymentFileSnapshot(before, opened) {
		return nil, fmt.Errorf("protected file changed while opening")
	}
	body, err := io.ReadAll(io.LimitReader(
		file, maxBytes+1,
	))
	if err != nil {
		return nil, fmt.Errorf("read protected file: %w", err)
	}
	afterRead, statErr := file.Stat()
	pathAfterRead, pathErr := os.Lstat(path)
	if statErr != nil ||
		pathErr != nil ||
		!sameProtectedDeploymentFileSnapshot(opened, afterRead) ||
		!sameProtectedDeploymentFileSnapshot(afterRead, pathAfterRead) {
		zeroSensitiveBytes(body)
		return nil, fmt.Errorf("protected file changed while reading")
	}
	if len(body) == 0 ||
		int64(len(body)) > maxBytes ||
		int64(len(body)) != afterRead.Size() {
		zeroSensitiveBytes(body)
		return nil, fmt.Errorf("protected file size is invalid")
	}
	return body, nil
}

// validateProtectedDeploymentFileInfo rejects non-regular, group/world
// accessible, empty, and oversized credential snapshots.
func validateProtectedDeploymentFileInfo(
	info os.FileInfo,
	maxBytes int64,
) error {
	if info == nil || !info.Mode().IsRegular() {
		return fmt.Errorf("protected file must be a regular file")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("protected file permissions are too broad")
	}
	if info.Size() <= 0 || info.Size() > maxBytes {
		return fmt.Errorf("protected file size is invalid")
	}
	return nil
}

// sameProtectedDeploymentFileSnapshot proves path and descriptor snapshots
// still name one inode with unchanged mode, size, and modification time.
func sameProtectedDeploymentFileSnapshot(
	left os.FileInfo,
	right os.FileInfo,
) bool {
	return left != nil &&
		right != nil &&
		left.Mode().IsRegular() &&
		right.Mode().IsRegular() &&
		os.SameFile(left, right) &&
		left.Mode() == right.Mode() &&
		left.Size() == right.Size() &&
		left.ModTime().Equal(right.ModTime())
}

func requireDeploymentJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf(
				"backup deployment keys: trailing package data",
			)
		}
		return fmt.Errorf(
			"backup deployment keys: decode trailing package data: %w", err,
		)
	}
	return nil
}

func deploymentMetadata(
	value deploymentKeyPackage,
) DeploymentKeyPackageMetadata {
	return DeploymentKeyPackageMetadata{
		PackageID:           value.PackageID,
		RepositoryID:        value.RepositoryID,
		Revision:            value.Revision,
		ActiveWrappingKeyID: value.ActiveWrappingKeyID,
		ActiveSigningKeyID:  value.ActiveSigningKeyID,
	}
}

func deploymentRandomID(
	random io.Reader,
	prefix string,
	byteCount int,
) (string, error) {
	value := make([]byte, byteCount)
	if _, err := io.ReadFull(random, value); err != nil {
		return "", fmt.Errorf(
			"backup deployment keys: generate identity: %w", err,
		)
	}
	return prefix + hex.EncodeToString(value), nil
}

func deploymentSigningKeyID(publicKey []byte) string {
	digest := sha256.Sum256(publicKey)
	return "ed25519:sha256:" + hex.EncodeToString(digest[:])
}

func deploymentWrappingKeyID(material []byte) string {
	digest := sha256.Sum256(material)
	return "aes256:sha256:" + hex.EncodeToString(digest[:])
}

func deploymentAuthorityContext(
	ctx context.Context,
	authority *DeploymentKeyAuthority,
) error {
	if authority == nil {
		return fmt.Errorf(
			"backup deployment keys: authority is required",
		)
	}
	if ctx == nil {
		return fmt.Errorf(
			"backup deployment keys: context is required",
		)
	}
	return ctx.Err()
}

func zeroSensitiveBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

func zeroDeploymentKeyPackageSecrets(value *deploymentKeyPackage) {
	if value == nil {
		return
	}
	zeroSensitiveBytes(value.AuthenticationKey)
	value.AuthenticationKey = nil
	for index := range value.WrappingKeys {
		zeroSensitiveBytes(value.WrappingKeys[index].Material)
		value.WrappingKeys[index].Material = nil
	}
	for index := range value.SigningKeys {
		zeroSensitiveBytes(value.SigningKeys[index].Seed)
		value.SigningKeys[index].Seed = nil
	}
}

func zeroDeploymentKeyAuthority(authority *DeploymentKeyAuthority) {
	if authority == nil {
		return
	}
	for keyID := range authority.wrappingKeys {
		authority.wrappingKeys[keyID] = [32]byte{}
		delete(authority.wrappingKeys, keyID)
	}
	for keyID, privateKey := range authority.signingPrivateKeys {
		zeroSensitiveBytes(privateKey)
		delete(authority.signingPrivateKeys, keyID)
	}
}
