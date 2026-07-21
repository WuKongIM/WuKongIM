package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"strings"

	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/kms/types"
)

const kmsEncryptionContextPurpose = "wukongim-cluster-backup"

// KMSClient is the narrow AWS SDK v2 surface required for envelope keys,
// manifest signatures, and startup doctor checks.
type KMSClient interface {
	GenerateDataKey(context.Context, *kms.GenerateDataKeyInput, ...func(*kms.Options)) (*kms.GenerateDataKeyOutput, error)
	Decrypt(context.Context, *kms.DecryptInput, ...func(*kms.Options)) (*kms.DecryptOutput, error)
	DescribeKey(context.Context, *kms.DescribeKeyInput, ...func(*kms.Options)) (*kms.DescribeKeyOutput, error)
	Sign(context.Context, *kms.SignInput, ...func(*kms.Options)) (*kms.SignOutput, error)
	Verify(context.Context, *kms.VerifyInput, ...func(*kms.Options)) (*kms.VerifyOutput, error)
}

// KMSAdapter implements backup envelope-key and manifest-signing boundaries.
type KMSAdapter struct {
	client KMSClient
}

// NewKMSAdapter creates a backup key adapter around an injected KMS client.
func NewKMSAdapter(client KMSClient) (*KMSAdapter, error) {
	if client == nil {
		return nil, fmt.Errorf("backup KMS: client is required")
	}
	return &KMSAdapter{client: client}, nil
}

// LoadKMSAdapter loads the AWS SDK default credential chain for region and an
// optional explicit HTTPS KMS-compatible endpoint.
func LoadKMSAdapter(ctx context.Context, region, endpoint string) (*KMSAdapter, error) {
	region = strings.TrimSpace(region)
	if region == "" {
		return nil, fmt.Errorf("backup KMS: region is required")
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("backup KMS: load AWS credentials: %w", err)
	}
	client := kms.NewFromConfig(cfg, func(options *kms.Options) {
		if endpoint = strings.TrimSpace(endpoint); endpoint != "" {
			options.BaseEndpoint = aws.String(endpoint)
		}
	})
	return NewKMSAdapter(client)
}

// GenerateDataKey returns a fresh AES-256 key and its KMS-wrapped ciphertext.
func (a *KMSAdapter) GenerateDataKey(ctx context.Context, keyID string) (backupartifact.DataKey, error) {
	if a == nil || a.client == nil || strings.TrimSpace(keyID) == "" {
		return backupartifact.DataKey{}, fmt.Errorf("backup KMS: encryption key id is required")
	}
	output, err := a.client.GenerateDataKey(ctx, &kms.GenerateDataKeyInput{
		KeyId:             aws.String(keyID),
		KeySpec:           types.DataKeySpecAes256,
		EncryptionContext: backupEncryptionContext(),
	})
	if err != nil {
		return backupartifact.DataKey{}, fmt.Errorf("backup KMS: generate data key: %w", err)
	}
	if output == nil || len(output.Plaintext) != 32 || len(output.CiphertextBlob) == 0 {
		return backupartifact.DataKey{}, fmt.Errorf("backup KMS: generated data key is incomplete")
	}
	return backupartifact.DataKey{
		Plaintext: append([]byte(nil), output.Plaintext...),
		Wrapped:   append([]byte(nil), output.CiphertextBlob...),
	}, nil
}

// UnwrapDataKey decrypts one wrapped AES-256 key under the exact configured key.
func (a *KMSAdapter) UnwrapDataKey(ctx context.Context, keyID string, wrapped []byte) ([]byte, error) {
	if a == nil || a.client == nil || strings.TrimSpace(keyID) == "" || len(wrapped) == 0 {
		return nil, fmt.Errorf("backup KMS: key id and wrapped data key are required")
	}
	output, err := a.client.Decrypt(ctx, &kms.DecryptInput{
		KeyId:             aws.String(keyID),
		CiphertextBlob:    append([]byte(nil), wrapped...),
		EncryptionContext: backupEncryptionContext(),
	})
	if err != nil {
		return nil, fmt.Errorf("backup KMS: decrypt data key: %w", err)
	}
	if output == nil || len(output.Plaintext) != 32 {
		return nil, fmt.Errorf("backup KMS: unwrapped data key is not AES-256")
	}
	return append([]byte(nil), output.Plaintext...), nil
}

// Sign hashes arbitrary canonical manifest bytes locally and asks KMS to sign
// the SHA-256 digest with a supported asymmetric signing algorithm.
func (a *KMSAdapter) Sign(ctx context.Context, keyID string, message []byte) (backupartifact.ManifestSignature, error) {
	if a == nil || a.client == nil || strings.TrimSpace(keyID) == "" {
		return backupartifact.ManifestSignature{}, fmt.Errorf("backup KMS: signing key id is required")
	}
	algorithm, err := a.signingAlgorithm(ctx, keyID)
	if err != nil {
		return backupartifact.ManifestSignature{}, err
	}
	digest := sha256.Sum256(message)
	output, err := a.client.Sign(ctx, &kms.SignInput{
		KeyId:            aws.String(keyID),
		Message:          digest[:],
		MessageType:      types.MessageTypeDigest,
		SigningAlgorithm: algorithm,
	})
	if err != nil {
		return backupartifact.ManifestSignature{}, fmt.Errorf("backup KMS: sign manifest digest: %w", err)
	}
	if output == nil || len(output.Signature) == 0 || output.SigningAlgorithm != algorithm {
		return backupartifact.ManifestSignature{}, fmt.Errorf("backup KMS: signing response is incomplete")
	}
	return backupartifact.ManifestSignature{
		Algorithm: string(algorithm),
		KeyID:     keyID,
		Value:     append([]byte(nil), output.Signature...),
	}, nil
}

// Verify verifies the exact canonical manifest digest through KMS.
func (a *KMSAdapter) Verify(ctx context.Context, signature backupartifact.ManifestSignature, message []byte) error {
	if a == nil || a.client == nil || strings.TrimSpace(signature.KeyID) == "" || len(signature.Value) == 0 {
		return fmt.Errorf("backup KMS: signature metadata is incomplete")
	}
	algorithm := types.SigningAlgorithmSpec(signature.Algorithm)
	if !supportedBackupSigningAlgorithm(algorithm) {
		return fmt.Errorf("backup KMS: unsupported signing algorithm %q", signature.Algorithm)
	}
	digest := sha256.Sum256(message)
	output, err := a.client.Verify(ctx, &kms.VerifyInput{
		KeyId:            aws.String(signature.KeyID),
		Message:          digest[:],
		MessageType:      types.MessageTypeDigest,
		Signature:        append([]byte(nil), signature.Value...),
		SigningAlgorithm: algorithm,
	})
	if err != nil {
		return fmt.Errorf("backup KMS: verify manifest digest: %w", err)
	}
	if output == nil || !output.SignatureValid {
		return fmt.Errorf("backup KMS: manifest signature is invalid")
	}
	return nil
}

// Check proves encryption-key usage, signing-key usage, envelope round-trip,
// and signature round-trip before backup scheduling begins.
func (a *KMSAdapter) Check(ctx context.Context, encryptionKeyID, signingKeyID string) error {
	if err := a.checkKey(ctx, encryptionKeyID, types.KeyUsageTypeEncryptDecrypt); err != nil {
		return fmt.Errorf("backup KMS encryption key: %w", err)
	}
	if err := a.checkKey(ctx, signingKeyID, types.KeyUsageTypeSignVerify); err != nil {
		return fmt.Errorf("backup KMS signing key: %w", err)
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
		return fmt.Errorf("backup KMS: envelope key round-trip mismatch")
	}
	probe := []byte("wukongim-backup-doctor-signature-v1")
	signature, err := a.Sign(ctx, signingKeyID, probe)
	if err != nil {
		return err
	}
	if err := a.Verify(ctx, signature, probe); err != nil {
		return err
	}
	return nil
}

func (a *KMSAdapter) checkKey(ctx context.Context, keyID string, usage types.KeyUsageType) error {
	if strings.TrimSpace(keyID) == "" {
		return fmt.Errorf("key id is required")
	}
	output, err := a.client.DescribeKey(ctx, &kms.DescribeKeyInput{KeyId: aws.String(keyID)})
	if err != nil {
		return fmt.Errorf("describe key: %w", err)
	}
	if output == nil || output.KeyMetadata == nil || !output.KeyMetadata.Enabled || output.KeyMetadata.KeyState != types.KeyStateEnabled {
		return fmt.Errorf("key must be enabled")
	}
	if output.KeyMetadata.KeyUsage != usage {
		return fmt.Errorf("key usage is %q, want %q", output.KeyMetadata.KeyUsage, usage)
	}
	if usage == types.KeyUsageTypeSignVerify {
		if _, ok := chooseBackupSigningAlgorithm(output.KeyMetadata.SigningAlgorithms); !ok {
			return fmt.Errorf("key has no supported SHA-256 signing algorithm")
		}
	}
	return nil
}

func (a *KMSAdapter) signingAlgorithm(ctx context.Context, keyID string) (types.SigningAlgorithmSpec, error) {
	output, err := a.client.DescribeKey(ctx, &kms.DescribeKeyInput{KeyId: aws.String(keyID)})
	if err != nil {
		return "", fmt.Errorf("backup KMS: describe signing key: %w", err)
	}
	if output == nil || output.KeyMetadata == nil || !output.KeyMetadata.Enabled || output.KeyMetadata.KeyUsage != types.KeyUsageTypeSignVerify {
		return "", fmt.Errorf("backup KMS: signing key is not enabled for SIGN_VERIFY")
	}
	algorithm, ok := chooseBackupSigningAlgorithm(output.KeyMetadata.SigningAlgorithms)
	if !ok {
		return "", fmt.Errorf("backup KMS: signing key has no supported SHA-256 algorithm")
	}
	return algorithm, nil
}

func chooseBackupSigningAlgorithm(algorithms []types.SigningAlgorithmSpec) (types.SigningAlgorithmSpec, bool) {
	preference := []types.SigningAlgorithmSpec{
		types.SigningAlgorithmSpecEcdsaSha256,
		types.SigningAlgorithmSpecRsassaPssSha256,
		types.SigningAlgorithmSpecRsassaPkcs1V15Sha256,
	}
	for _, wanted := range preference {
		for _, available := range algorithms {
			if available == wanted {
				return wanted, true
			}
		}
	}
	return "", false
}

func supportedBackupSigningAlgorithm(algorithm types.SigningAlgorithmSpec) bool {
	_, ok := chooseBackupSigningAlgorithm([]types.SigningAlgorithmSpec{algorithm})
	return ok
}

func backupEncryptionContext() map[string]string {
	return map[string]string{"wukongim-purpose": kmsEncryptionContextPurpose}
}

func zeroSensitiveBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

var (
	_ backupartifact.DataKeyManager = (*KMSAdapter)(nil)
	_ backupartifact.ManifestSigner = (*KMSAdapter)(nil)
)
