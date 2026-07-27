package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"testing"

	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	alibabakms "github.com/alibabacloud-go/kms-20160120/v3/client"
	"github.com/alibabacloud-go/tea/dara"
)

func TestAlibabaKMSAdapterGeneratesUnwrapsSignsAndVerifies(t *testing.T) {
	client := &fakeAlibabaKMSClient{}
	adapter, err := NewAlibabaKMSAdapter(client)
	if err != nil {
		t.Fatalf("NewAlibabaKMSAdapter() error = %v", err)
	}
	dataKey, err := adapter.GenerateDataKey(context.Background(), "encryption-key")
	if err != nil {
		t.Fatalf("GenerateDataKey() error = %v", err)
	}
	if len(dataKey.Plaintext) != 32 ||
		!bytes.Equal(dataKey.Wrapped, []byte("wrapped-key")) {
		t.Fatalf("GenerateDataKey() = %#v", dataKey)
	}
	unwrapped, err := adapter.UnwrapDataKey(
		context.Background(), "encryption-key", dataKey.Wrapped,
	)
	if err != nil || !bytes.Equal(unwrapped, dataKey.Plaintext) {
		t.Fatalf("UnwrapDataKey() = %x, %v", unwrapped, err)
	}
	message := []byte("canonical manifest bytes")
	signature, err := adapter.Sign(context.Background(), "signing-key", message)
	if err != nil {
		t.Fatalf("Sign() error = %v", err)
	}
	if signature.Algorithm != alibabaKMSECDSASHA256 ||
		signature.KeyID != "signing-key" ||
		signature.KeyVersionID != "signing-version-1" {
		t.Fatalf("Sign() = %#v", signature)
	}
	wantDigest := sha256.Sum256(message)
	if client.lastSign == nil ||
		alibabaString(client.lastSign.Digest) !=
			base64.StdEncoding.EncodeToString(wantDigest[:]) ||
		alibabaString(client.lastSign.KeyVersionId) != "signing-version-1" {
		t.Fatalf("AsymmetricSign input = %#v", client.lastSign)
	}
	if err := adapter.Verify(context.Background(), signature, message); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if err := adapter.Check(
		context.Background(), "encryption-key", "signing-key",
	); err != nil {
		t.Fatalf("Check() error = %v", err)
	}
}

func TestAlibabaKMSAdapterRejectsUnpinnedSignatureVersion(t *testing.T) {
	adapter, err := NewAlibabaKMSAdapter(&fakeAlibabaKMSClient{})
	if err != nil {
		t.Fatalf("NewAlibabaKMSAdapter() error = %v", err)
	}
	err = adapter.Verify(context.Background(), backupartifact.ManifestSignature{
		Algorithm: alibabaKMSECDSASHA256,
		KeyID:     "signing-key",
		Value:     []byte("signature"),
	}, []byte("message"))
	if err == nil {
		t.Fatal("Verify() error = nil, want missing key-version rejection")
	}
}

func TestAlibabaKMSAdapterRejectsAliasResolution(t *testing.T) {
	adapter, err := NewAlibabaKMSAdapter(&fakeAlibabaKMSClient{
		describeKeyID: "resolved-concrete-key-id",
	})
	if err != nil {
		t.Fatalf("NewAlibabaKMSAdapter() error = %v", err)
	}
	if _, err := adapter.Sign(
		context.Background(), "alias/backup-signing", []byte("message"),
	); err == nil {
		t.Fatal("Sign() error = nil, want alias identity rejection")
	}
}

type fakeAlibabaKMSClient struct {
	lastSign      *alibabakms.AsymmetricSignRequest
	lastVerify    *alibabakms.AsymmetricVerifyRequest
	describeKeyID string
}

func (f *fakeAlibabaKMSClient) GenerateDataKeyWithContext(
	_ context.Context,
	_ *alibabakms.GenerateDataKeyRequest,
	_ *dara.RuntimeOptions,
) (*alibabakms.GenerateDataKeyResponse, error) {
	plaintext := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x2a}, 32))
	return &alibabakms.GenerateDataKeyResponse{
		Body: &alibabakms.GenerateDataKeyResponseBody{
			KeyId:          alibabaStringPointer("encryption-key"),
			KeyVersionId:   alibabaStringPointer("encryption-version-1"),
			Plaintext:      &plaintext,
			CiphertextBlob: alibabaStringPointer("wrapped-key"),
		},
	}, nil
}

func (f *fakeAlibabaKMSClient) DecryptWithContext(
	_ context.Context,
	_ *alibabakms.DecryptRequest,
	_ *dara.RuntimeOptions,
) (*alibabakms.DecryptResponse, error) {
	plaintext := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x2a}, 32))
	return &alibabakms.DecryptResponse{
		Body: &alibabakms.DecryptResponseBody{
			KeyId:     alibabaStringPointer("encryption-key"),
			Plaintext: &plaintext,
		},
	}, nil
}

func (f *fakeAlibabaKMSClient) DescribeKeyWithContext(
	_ context.Context,
	request *alibabakms.DescribeKeyRequest,
	_ *dara.RuntimeOptions,
) (*alibabakms.DescribeKeyResponse, error) {
	metadata := &alibabakms.DescribeKeyResponseBodyKeyMetadata{
		KeyId:    request.KeyId,
		KeyState: alibabaStringPointer("Enabled"),
	}
	if f.describeKeyID != "" {
		metadata.KeyId = alibabaStringPointer(f.describeKeyID)
	}
	if alibabaString(request.KeyId) == "signing-key" {
		metadata.KeyUsage = alibabaStringPointer("SIGN/VERIFY")
		metadata.KeySpec = alibabaStringPointer("EC_P256")
		metadata.PrimaryKeyVersion = alibabaStringPointer("signing-version-1")
	} else {
		metadata.KeyUsage = alibabaStringPointer("ENCRYPT/DECRYPT")
		metadata.KeySpec = alibabaStringPointer("Aliyun_AES_256")
		metadata.PrimaryKeyVersion = alibabaStringPointer("encryption-version-1")
	}
	return &alibabakms.DescribeKeyResponse{
		Body: &alibabakms.DescribeKeyResponseBody{KeyMetadata: metadata},
	}, nil
}

func (f *fakeAlibabaKMSClient) AsymmetricSignWithContext(
	_ context.Context,
	request *alibabakms.AsymmetricSignRequest,
	_ *dara.RuntimeOptions,
) (*alibabakms.AsymmetricSignResponse, error) {
	f.lastSign = request
	digest, _ := base64.StdEncoding.DecodeString(alibabaString(request.Digest))
	value := sha256.Sum256(append(
		[]byte(alibabaString(request.Algorithm)+":"),
		digest...,
	))
	signature := base64.StdEncoding.EncodeToString(value[:])
	return &alibabakms.AsymmetricSignResponse{
		Body: &alibabakms.AsymmetricSignResponseBody{
			KeyId:        request.KeyId,
			KeyVersionId: request.KeyVersionId,
			Value:        &signature,
		},
	}, nil
}

func (f *fakeAlibabaKMSClient) AsymmetricVerifyWithContext(
	_ context.Context,
	request *alibabakms.AsymmetricVerifyRequest,
	_ *dara.RuntimeOptions,
) (*alibabakms.AsymmetricVerifyResponse, error) {
	f.lastVerify = request
	digest, _ := base64.StdEncoding.DecodeString(alibabaString(request.Digest))
	want := sha256.Sum256(append(
		[]byte(alibabaString(request.Algorithm)+":"),
		digest...,
	))
	got, _ := base64.StdEncoding.DecodeString(alibabaString(request.Value))
	valid := bytes.Equal(want[:], got)
	return &alibabakms.AsymmetricVerifyResponse{
		Body: &alibabakms.AsymmetricVerifyResponseBody{
			KeyId:        request.KeyId,
			KeyVersionId: request.KeyVersionId,
			Value:        &valid,
		},
	}, nil
}
