package backup_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/backup"
)

func TestObjectCodecCompressesEncryptsAndRestoresPayload(t *testing.T) {
	t.Parallel()

	keys := wrappingKeyManager{wrappingByte: 0xa5}
	codec := backup.NewObjectCodec(keys, bytes.NewReader(bytes.Repeat([]byte{0x42}, 64)))
	plaintext := bytes.Repeat([]byte("large-channel-message-payload/"), 256)

	sealed, err := codec.Seal(context.Background(), backup.ObjectDescriptor{
		Key:      "objects/07/messages-000001.wkb",
		Kind:     backup.ObjectKindMessages,
		HashSlot: 7,
	}, plaintext)
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}
	if bytes.Contains(sealed.Ciphertext, []byte("large-channel-message-payload")) {
		t.Fatal("Seal() ciphertext contains plaintext")
	}
	if sealed.Entry.PlaintextBytes != int64(len(plaintext)) || sealed.Entry.CiphertextBytes != int64(len(sealed.Ciphertext)) {
		t.Fatalf("Seal() sizes = %d/%d, want %d/%d", sealed.Entry.PlaintextBytes, sealed.Entry.CiphertextBytes, len(plaintext), len(sealed.Ciphertext))
	}
	if sealed.Entry.Compression != backup.CompressionZstd || sealed.Entry.Encryption != backup.EncryptionAES256GCM {
		t.Fatalf("Seal() codec = %q/%q", sealed.Entry.Compression, sealed.Entry.Encryption)
	}
	if len(sealed.Entry.DataKey.Value) == 0 || sealed.Entry.Nonce == "" {
		t.Fatalf(
			"Seal() key metadata = wrapped:%x nonce:%q",
			sealed.Entry.DataKey.Value, sealed.Entry.Nonce,
		)
	}

	restored, err := codec.Open(context.Background(), sealed.Entry, sealed.Ciphertext)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if !bytes.Equal(restored, plaintext) {
		t.Fatalf("Open() payload length = %d, want exact %d-byte payload", len(restored), len(plaintext))
	}
}

type wrappingKeyManager struct {
	wrappingByte byte
}

func (m wrappingKeyManager) NewDataKey(
	_ context.Context,
) (backup.DataKey, error) {
	plaintext := bytes.Repeat([]byte{0x6d}, 32)
	return backup.DataKey{
		Plaintext: plaintext,
		Envelope: backup.DataKeyEnvelope{
			Version: 1, Algorithm: "TEST_XOR", KeyID: "test",
			Nonce: []byte{1}, Value: xorBytes(plaintext, m.wrappingByte),
		},
	}, nil
}

func (m wrappingKeyManager) OpenDataKey(
	_ context.Context,
	envelope backup.DataKeyEnvelope,
) ([]byte, error) {
	return xorBytes(envelope.Value, m.wrappingByte), nil
}

func xorBytes(value []byte, mask byte) []byte {
	out := make([]byte, len(value))
	for i := range value {
		out[i] = value[i] ^ mask
	}
	return out
}
