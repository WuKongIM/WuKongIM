package backup_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/backup"
)

type ed25519ManifestSigner struct {
	privateKey ed25519.PrivateKey
}

func (s ed25519ManifestSigner) Sign(
	_ context.Context,
	keyID string,
	message []byte,
) (backup.ManifestSignature, error) {
	return backup.ManifestSignature{
		Algorithm: "ed25519",
		KeyID:     keyID,
		Value:     ed25519.Sign(s.privateKey, message),
	}, nil
}

func (s ed25519ManifestSigner) Verify(
	_ context.Context,
	signature backup.ManifestSignature,
	message []byte,
) error {
	publicKey := s.privateKey.Public().(ed25519.PublicKey)
	if !ed25519.Verify(publicKey, message, signature.Value) {
		return backup.ErrInvalidSignature
	}
	return nil
}

func testSealedObject(
	key string,
	hashSlot uint16,
	ciphertext []byte,
) backup.SealedObject {
	plainHash := sha256.Sum256([]byte("plain:" + key))
	cipherHash := sha256.Sum256(ciphertext)
	return backup.SealedObject{
		Entry: backup.ObjectEntry{
			Key: key, Kind: backup.ObjectKindMetadata, HashSlot: hashSlot,
			PlaintextSHA256:  fmt.Sprintf("%x", plainHash),
			CiphertextSHA256: fmt.Sprintf("%x", cipherHash),
			PlaintextBytes:   10,
			CiphertextBytes:  int64(len(ciphertext)),
			Compression:      backup.CompressionZstd,
			Encryption:       backup.EncryptionAES256GCM,
			KMSKeyID:         "kms-prod",
			WrappedKey:       "d3JhcHBlZA==",
			Nonce:            "bm9uY2Utbm9uY2U=",
		},
		Ciphertext: append([]byte(nil), ciphertext...),
	}
}

type memoryRepository struct {
	name       string
	mu         sync.Mutex
	objects    map[string][]byte
	failPut    bool
	failPutKey string
	openCounts map[string]int
}

func newMemoryRepository(name string) *memoryRepository {
	return &memoryRepository{
		name: name, objects: make(map[string][]byte),
		openCounts: make(map[string]int),
	}
}

func (r *memoryRepository) Name() string { return r.name }

func (r *memoryRepository) PutImmutable(
	_ context.Context,
	key string,
	size int64,
	checksum string,
	body io.Reader,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failPut || key == r.failPutKey {
		return errors.New("repository unavailable")
	}
	if _, ok := r.objects[key]; ok {
		return backup.ErrObjectExists
	}
	value, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	if int64(len(value)) != size {
		return errors.New("size mismatch")
	}
	hash := sha256.Sum256(value)
	if fmt.Sprintf("%x", hash) != checksum {
		return errors.New("checksum mismatch")
	}
	r.objects[key] = value
	return nil
}

func (r *memoryRepository) Open(
	_ context.Context,
	key string,
) (io.ReadCloser, backup.RepositoryObject, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	value, ok := r.objects[key]
	if !ok {
		return nil, backup.RepositoryObject{}, backup.ErrObjectNotFound
	}
	r.openCounts[key]++
	hash := sha256.Sum256(value)
	return io.NopCloser(bytes.NewReader(value)), backup.RepositoryObject{
		Key: key, Size: int64(len(value)), SHA256: fmt.Sprintf("%x", hash),
	}, nil
}

func (r *memoryRepository) openCount(key string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.openCounts[key]
}

func (r *memoryRepository) Stat(
	_ context.Context,
	key string,
) (backup.RepositoryObject, error) {
	reader, object, err := r.Open(context.Background(), key)
	if reader != nil {
		_ = reader.Close()
	}
	return object, err
}

func (r *memoryRepository) has(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.objects[key]
	return ok
}

func (r *memoryRepository) body(key string) []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]byte(nil), r.objects[key]...)
}

func (r *memoryRepository) remove(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.objects, key)
}
