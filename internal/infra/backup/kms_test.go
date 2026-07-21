package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/kms/types"
)

func TestKMSAdapterGeneratesUnwrapsSignsAndVerifies(t *testing.T) {
	client := &fakeKMSClient{}
	adapter, err := NewKMSAdapter(client)
	if err != nil {
		t.Fatalf("NewKMSAdapter() error = %v", err)
	}
	dataKey, err := adapter.GenerateDataKey(context.Background(), "encryption-key")
	if err != nil {
		t.Fatalf("GenerateDataKey() error = %v", err)
	}
	if len(dataKey.Plaintext) != 32 || !bytes.Equal(dataKey.Wrapped, []byte("wrapped-key")) {
		t.Fatalf("GenerateDataKey() = %#v", dataKey)
	}
	unwrapped, err := adapter.UnwrapDataKey(context.Background(), "encryption-key", dataKey.Wrapped)
	if err != nil || !bytes.Equal(unwrapped, dataKey.Plaintext) {
		t.Fatalf("UnwrapDataKey() = %x, %v", unwrapped, err)
	}
	message := []byte("canonical manifest bytes may be larger than the KMS raw-message limit")
	signature, err := adapter.Sign(context.Background(), "signing-key", message)
	if err != nil {
		t.Fatalf("Sign() error = %v", err)
	}
	if signature.Algorithm != string(types.SigningAlgorithmSpecEcdsaSha256) || signature.KeyID != "signing-key" {
		t.Fatalf("Sign() = %#v", signature)
	}
	wantDigest := sha256.Sum256(message)
	if !bytes.Equal(client.lastSign.Message, wantDigest[:]) || client.lastSign.MessageType != types.MessageTypeDigest {
		t.Fatalf("Sign input did not use SHA-256 digest: %#v", client.lastSign)
	}
	if err := adapter.Verify(context.Background(), signature, message); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if err := adapter.Check(context.Background(), "encryption-key", "signing-key"); err != nil {
		t.Fatalf("Check() error = %v", err)
	}
}

type fakeKMSClient struct {
	lastSign *kms.SignInput
}

func (f *fakeKMSClient) GenerateDataKey(_ context.Context, input *kms.GenerateDataKeyInput, _ ...func(*kms.Options)) (*kms.GenerateDataKeyOutput, error) {
	plain := bytes.Repeat([]byte{0x2a}, 32)
	return &kms.GenerateDataKeyOutput{KeyId: input.KeyId, Plaintext: plain, CiphertextBlob: []byte("wrapped-key")}, nil
}

func (f *fakeKMSClient) Decrypt(_ context.Context, input *kms.DecryptInput, _ ...func(*kms.Options)) (*kms.DecryptOutput, error) {
	return &kms.DecryptOutput{KeyId: input.KeyId, Plaintext: bytes.Repeat([]byte{0x2a}, 32)}, nil
}

func (f *fakeKMSClient) DescribeKey(_ context.Context, input *kms.DescribeKeyInput, _ ...func(*kms.Options)) (*kms.DescribeKeyOutput, error) {
	if aws.ToString(input.KeyId) == "signing-key" {
		return &kms.DescribeKeyOutput{KeyMetadata: &types.KeyMetadata{
			Enabled:           true,
			KeyState:          types.KeyStateEnabled,
			KeyUsage:          types.KeyUsageTypeSignVerify,
			SigningAlgorithms: []types.SigningAlgorithmSpec{types.SigningAlgorithmSpecEcdsaSha256},
		}}, nil
	}
	return &kms.DescribeKeyOutput{KeyMetadata: &types.KeyMetadata{
		Enabled:  true,
		KeyState: types.KeyStateEnabled,
		KeyUsage: types.KeyUsageTypeEncryptDecrypt,
	}}, nil
}

func (f *fakeKMSClient) Sign(_ context.Context, input *kms.SignInput, _ ...func(*kms.Options)) (*kms.SignOutput, error) {
	f.lastSign = input
	hash := sha256.Sum256(append([]byte(string(input.SigningAlgorithm)+":"), input.Message...))
	return &kms.SignOutput{KeyId: input.KeyId, Signature: hash[:], SigningAlgorithm: input.SigningAlgorithm}, nil
}

func (f *fakeKMSClient) Verify(_ context.Context, input *kms.VerifyInput, _ ...func(*kms.Options)) (*kms.VerifyOutput, error) {
	hash := sha256.Sum256(append([]byte(string(input.SigningAlgorithm)+":"), input.Message...))
	return &kms.VerifyOutput{KeyId: input.KeyId, SignatureValid: bytes.Equal(hash[:], input.Signature), SigningAlgorithm: input.SigningAlgorithm}, nil
}
