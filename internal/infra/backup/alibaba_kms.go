package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"

	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	alibabakms "github.com/alibabacloud-go/kms-20160120/v3/client"
	"github.com/alibabacloud-go/tea/dara"
)

const (
	alibabaKMSECDSASHA256       = "ECDSA_SHA_256"
	alibabaKMSRSAPSSSHA256      = "RSA_PSS_SHA_256"
	kmsEncryptionContextPurpose = "wukongim-cluster-backup"
)

// AlibabaKMSClient is the narrow Alibaba Cloud KMS API surface used by backup.
type AlibabaKMSClient interface {
	GenerateDataKeyWithContext(context.Context, *alibabakms.GenerateDataKeyRequest, *dara.RuntimeOptions) (*alibabakms.GenerateDataKeyResponse, error)
	DecryptWithContext(context.Context, *alibabakms.DecryptRequest, *dara.RuntimeOptions) (*alibabakms.DecryptResponse, error)
	DescribeKeyWithContext(context.Context, *alibabakms.DescribeKeyRequest, *dara.RuntimeOptions) (*alibabakms.DescribeKeyResponse, error)
	AsymmetricSignWithContext(context.Context, *alibabakms.AsymmetricSignRequest, *dara.RuntimeOptions) (*alibabakms.AsymmetricSignResponse, error)
	AsymmetricVerifyWithContext(context.Context, *alibabakms.AsymmetricVerifyRequest, *dara.RuntimeOptions) (*alibabakms.AsymmetricVerifyResponse, error)
}

// AlibabaKMSAdapter implements envelope-key and manifest-signing boundaries.
type AlibabaKMSAdapter struct {
	client AlibabaKMSClient
}

// NewAlibabaKMSAdapter creates an adapter around an injected KMS client.
func NewAlibabaKMSAdapter(client AlibabaKMSClient) (*AlibabaKMSAdapter, error) {
	if client == nil {
		return nil, fmt.Errorf("backup Alibaba KMS: client is required")
	}
	return &AlibabaKMSAdapter{client: client}, nil
}

// GenerateDataKey returns one fresh AES-256 key and its opaque KMS ciphertext.
func (a *AlibabaKMSAdapter) GenerateDataKey(
	ctx context.Context,
	keyID string,
) (backupartifact.DataKey, error) {
	keyID = strings.TrimSpace(keyID)
	if a == nil || a.client == nil || keyID == "" {
		return backupartifact.DataKey{}, fmt.Errorf(
			"backup Alibaba KMS: encryption key id is required",
		)
	}
	output, err := a.client.GenerateDataKeyWithContext(
		ctx,
		(&alibabakms.GenerateDataKeyRequest{}).
			SetKeyId(keyID).
			SetKeySpec("AES_256").
			SetEncryptionContext(alibabaBackupEncryptionContext()),
		&dara.RuntimeOptions{},
	)
	if err != nil {
		return backupartifact.DataKey{}, fmt.Errorf(
			"backup Alibaba KMS: generate data key: %w", err,
		)
	}
	if output == nil || output.Body == nil ||
		strings.TrimSpace(alibabaString(output.Body.KeyId)) != keyID {
		return backupartifact.DataKey{}, fmt.Errorf(
			"backup Alibaba KMS: generated data key identity does not match",
		)
	}
	plaintext, err := base64.StdEncoding.DecodeString(
		strings.TrimSpace(alibabaString(output.Body.Plaintext)),
	)
	if err != nil || len(plaintext) != 32 ||
		strings.TrimSpace(alibabaString(output.Body.CiphertextBlob)) == "" {
		zeroSensitiveBytes(plaintext)
		return backupartifact.DataKey{}, fmt.Errorf(
			"backup Alibaba KMS: generated data key is incomplete",
		)
	}
	return backupartifact.DataKey{
		Plaintext: plaintext,
		Wrapped:   []byte(alibabaString(output.Body.CiphertextBlob)),
	}, nil
}

// UnwrapDataKey decrypts one opaque KMS ciphertext under the exact key.
func (a *AlibabaKMSAdapter) UnwrapDataKey(
	ctx context.Context,
	keyID string,
	wrapped []byte,
) ([]byte, error) {
	keyID = strings.TrimSpace(keyID)
	ciphertext := strings.TrimSpace(string(wrapped))
	if a == nil || a.client == nil || keyID == "" || ciphertext == "" {
		return nil, fmt.Errorf(
			"backup Alibaba KMS: key id and wrapped data key are required",
		)
	}
	output, err := a.client.DecryptWithContext(
		ctx,
		(&alibabakms.DecryptRequest{}).
			SetCiphertextBlob(ciphertext).
			SetEncryptionContext(alibabaBackupEncryptionContext()),
		&dara.RuntimeOptions{},
	)
	if err != nil {
		return nil, fmt.Errorf("backup Alibaba KMS: decrypt data key: %w", err)
	}
	if output == nil || output.Body == nil ||
		strings.TrimSpace(alibabaString(output.Body.KeyId)) != keyID {
		return nil, fmt.Errorf(
			"backup Alibaba KMS: unwrapped key identity does not match",
		)
	}
	plaintext, err := base64.StdEncoding.DecodeString(
		strings.TrimSpace(alibabaString(output.Body.Plaintext)),
	)
	if err != nil || len(plaintext) != 32 {
		zeroSensitiveBytes(plaintext)
		return nil, fmt.Errorf(
			"backup Alibaba KMS: unwrapped data key is not AES-256",
		)
	}
	return plaintext, nil
}

// Sign signs a local SHA-256 digest and pins the returned KMS key version.
func (a *AlibabaKMSAdapter) Sign(
	ctx context.Context,
	keyID string,
	message []byte,
) (backupartifact.ManifestSignature, error) {
	keyID = strings.TrimSpace(keyID)
	if a == nil || a.client == nil || keyID == "" {
		return backupartifact.ManifestSignature{}, fmt.Errorf(
			"backup Alibaba KMS: signing key id is required",
		)
	}
	algorithm, keyVersionID, err := a.signingParameters(ctx, keyID)
	if err != nil {
		return backupartifact.ManifestSignature{}, err
	}
	digest := sha256.Sum256(message)
	output, err := a.client.AsymmetricSignWithContext(
		ctx,
		(&alibabakms.AsymmetricSignRequest{}).
			SetKeyId(keyID).
			SetKeyVersionId(keyVersionID).
			SetAlgorithm(algorithm).
			SetDigest(base64.StdEncoding.EncodeToString(digest[:])),
		&dara.RuntimeOptions{},
	)
	if err != nil {
		return backupartifact.ManifestSignature{}, fmt.Errorf(
			"backup Alibaba KMS: sign manifest digest: %w", err,
		)
	}
	if output == nil || output.Body == nil ||
		alibabaString(output.Body.KeyId) != keyID ||
		alibabaString(output.Body.KeyVersionId) != keyVersionID {
		return backupartifact.ManifestSignature{}, fmt.Errorf(
			"backup Alibaba KMS: signing response is incomplete",
		)
	}
	signature, err := base64.StdEncoding.DecodeString(
		strings.TrimSpace(alibabaString(output.Body.Value)),
	)
	if err != nil || len(signature) == 0 {
		return backupartifact.ManifestSignature{}, fmt.Errorf(
			"backup Alibaba KMS: signing response is invalid",
		)
	}
	return backupartifact.ManifestSignature{
		Algorithm:    algorithm,
		KeyID:        keyID,
		KeyVersionID: keyVersionID,
		Value:        signature,
	}, nil
}

// Verify verifies an exact digest through the pinned KMS key version.
func (a *AlibabaKMSAdapter) Verify(
	ctx context.Context,
	signature backupartifact.ManifestSignature,
	message []byte,
) error {
	keyID := strings.TrimSpace(signature.KeyID)
	keyVersionID := strings.TrimSpace(signature.KeyVersionID)
	if a == nil || a.client == nil || keyID == "" || keyVersionID == "" ||
		len(signature.Value) == 0 {
		return fmt.Errorf("backup Alibaba KMS: signature metadata is incomplete")
	}
	if !supportedAlibabaKMSSigningAlgorithm(signature.Algorithm) {
		return fmt.Errorf(
			"backup Alibaba KMS: unsupported signing algorithm %q",
			signature.Algorithm,
		)
	}
	digest := sha256.Sum256(message)
	output, err := a.client.AsymmetricVerifyWithContext(
		ctx,
		(&alibabakms.AsymmetricVerifyRequest{}).
			SetKeyId(keyID).
			SetKeyVersionId(keyVersionID).
			SetAlgorithm(signature.Algorithm).
			SetDigest(base64.StdEncoding.EncodeToString(digest[:])).
			SetValue(base64.StdEncoding.EncodeToString(signature.Value)),
		&dara.RuntimeOptions{},
	)
	if err != nil {
		return fmt.Errorf("backup Alibaba KMS: verify manifest digest: %w", err)
	}
	if output == nil || output.Body == nil ||
		alibabaString(output.Body.KeyId) != keyID ||
		alibabaString(output.Body.KeyVersionId) != keyVersionID ||
		output.Body.Value == nil || !*output.Body.Value {
		return fmt.Errorf("backup Alibaba KMS: manifest signature is invalid")
	}
	return nil
}

// Check proves key purposes, envelope round-trip, and signature round-trip.
func (a *AlibabaKMSAdapter) Check(
	ctx context.Context,
	encryptionKeyID string,
	signingKeyID string,
) error {
	if err := a.checkKey(ctx, encryptionKeyID, "ENCRYPT/DECRYPT"); err != nil {
		return fmt.Errorf("backup Alibaba KMS encryption key: %w", err)
	}
	if err := a.checkKey(ctx, signingKeyID, "SIGN/VERIFY"); err != nil {
		return fmt.Errorf("backup Alibaba KMS signing key: %w", err)
	}
	dataKey, err := a.GenerateDataKey(ctx, encryptionKeyID)
	if err != nil {
		return err
	}
	decrypted, err := a.UnwrapDataKey(ctx, encryptionKeyID, dataKey.Wrapped)
	if err != nil {
		zeroSensitiveBytes(dataKey.Plaintext)
		return err
	}
	matched := bytes.Equal(dataKey.Plaintext, decrypted)
	zeroSensitiveBytes(dataKey.Plaintext)
	zeroSensitiveBytes(decrypted)
	if !matched {
		return fmt.Errorf("backup Alibaba KMS: envelope key round-trip mismatch")
	}
	probe := []byte("wukongim-backup-doctor-signature-v1")
	signature, err := a.Sign(ctx, signingKeyID, probe)
	if err != nil {
		return err
	}
	return a.Verify(ctx, signature, probe)
}

func (a *AlibabaKMSAdapter) checkKey(
	ctx context.Context,
	keyID string,
	usage string,
) error {
	keyID = strings.TrimSpace(keyID)
	if keyID == "" {
		return fmt.Errorf("key id is required")
	}
	metadata, err := a.describeKey(ctx, keyID)
	if err != nil {
		return err
	}
	if alibabaString(metadata.KeyState) != "Enabled" {
		return fmt.Errorf("key must be enabled")
	}
	if alibabaString(metadata.KeyUsage) != usage {
		return fmt.Errorf(
			"key usage is %q, want %q", alibabaString(metadata.KeyUsage), usage,
		)
	}
	if usage == "ENCRYPT/DECRYPT" {
		switch alibabaString(metadata.KeySpec) {
		case "Aliyun_AES_256", "AES_256":
		default:
			return fmt.Errorf("encryption key must be AES-256")
		}
		return nil
	}
	if _, ok := alibabaKMSSigningAlgorithm(alibabaString(metadata.KeySpec)); !ok ||
		strings.TrimSpace(alibabaString(metadata.PrimaryKeyVersion)) == "" {
		return fmt.Errorf("signing key has no supported SHA-256 key version")
	}
	return nil
}

func (a *AlibabaKMSAdapter) signingParameters(
	ctx context.Context,
	keyID string,
) (string, string, error) {
	metadata, err := a.describeKey(ctx, keyID)
	if err != nil {
		return "", "", fmt.Errorf(
			"backup Alibaba KMS: describe signing key: %w", err,
		)
	}
	if alibabaString(metadata.KeyState) != "Enabled" ||
		alibabaString(metadata.KeyUsage) != "SIGN/VERIFY" {
		return "", "", fmt.Errorf(
			"backup Alibaba KMS: signing key is not enabled for SIGN/VERIFY",
		)
	}
	algorithm, ok := alibabaKMSSigningAlgorithm(alibabaString(metadata.KeySpec))
	keyVersionID := strings.TrimSpace(alibabaString(metadata.PrimaryKeyVersion))
	if !ok || keyVersionID == "" {
		return "", "", fmt.Errorf(
			"backup Alibaba KMS: signing key has no supported SHA-256 key version",
		)
	}
	return algorithm, keyVersionID, nil
}

func (a *AlibabaKMSAdapter) describeKey(
	ctx context.Context,
	keyID string,
) (*alibabakms.DescribeKeyResponseBodyKeyMetadata, error) {
	output, err := a.client.DescribeKeyWithContext(
		ctx,
		(&alibabakms.DescribeKeyRequest{}).SetKeyId(keyID),
		&dara.RuntimeOptions{},
	)
	if err != nil {
		return nil, fmt.Errorf("describe key: %w", err)
	}
	if output == nil || output.Body == nil || output.Body.KeyMetadata == nil {
		return nil, fmt.Errorf("describe key response is incomplete")
	}
	if strings.TrimSpace(alibabaString(output.Body.KeyMetadata.KeyId)) != keyID {
		return nil, fmt.Errorf(
			"key identity does not match; configure a concrete Alibaba KMS key id, not an alias",
		)
	}
	return output.Body.KeyMetadata, nil
}

func alibabaKMSSigningAlgorithm(keySpec string) (string, bool) {
	switch strings.TrimSpace(keySpec) {
	case "EC_P256", "EC_P256K":
		return alibabaKMSECDSASHA256, true
	case "RSA_2048", "RSA_3072", "RSA_4096":
		return alibabaKMSRSAPSSSHA256, true
	default:
		return "", false
	}
}

func supportedAlibabaKMSSigningAlgorithm(algorithm string) bool {
	return algorithm == alibabaKMSECDSASHA256 ||
		algorithm == alibabaKMSRSAPSSSHA256
}

func alibabaBackupEncryptionContext() map[string]interface{} {
	return map[string]interface{}{
		"wukongim-purpose": kmsEncryptionContextPurpose,
	}
}

func zeroSensitiveBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

func alibabaStringPointer(value string) *string {
	return &value
}

func alibabaString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

var (
	_ backupartifact.DataKeyManager = (*AlibabaKMSAdapter)(nil)
	_ backupartifact.ManifestSigner = (*AlibabaKMSAdapter)(nil)
	_ KMSDoctor                     = (*AlibabaKMSAdapter)(nil)
)
