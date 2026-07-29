package backup

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"golang.org/x/crypto/hkdf"
)

const credentialCipherVersion uint32 = 1

// S3Credentials is the short-lived plaintext credential supplied by Manager.
type S3Credentials struct {
	AccessKey string `json:"access_key"`
	SecretKey string `json:"secret_key"`
}

// CredentialCipher protects S3 credentials before they enter Controller state.
type CredentialCipher struct {
	aead cipher.AEAD
}

// NewCredentialCipher derives a cluster-bound key from the Manager
// installation secret.
func NewCredentialCipher(
	managerSecret string,
	clusterID string,
) (*CredentialCipher, error) {
	if len(managerSecret) < 16 || strings.TrimSpace(clusterID) == "" {
		return nil, fmt.Errorf("backup credentials: Manager secret and cluster identity are required")
	}
	reader := hkdf.New(
		sha256.New,
		[]byte(managerSecret),
		[]byte(clusterID),
		[]byte("wukongim/scheduled-backup/s3-credentials/v1"),
	)
	key := make([]byte, 32)
	if _, err := io.ReadFull(reader, key); err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &CredentialCipher{aead: aead}, nil
}

// Seal encodes and encrypts one credential pair.
func (c *CredentialCipher) Seal(credentials S3Credentials) ([]byte, error) {
	if c == nil || c.aead == nil ||
		strings.TrimSpace(credentials.AccessKey) == "" ||
		credentials.SecretKey == "" ||
		len(credentials.AccessKey) > 256 ||
		len(credentials.SecretKey) > 1024 {
		return nil, fmt.Errorf("backup credentials: invalid credential")
	}
	body, err := json.Marshal(credentials)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	output := binary.BigEndian.AppendUint32(nil, credentialCipherVersion)
	output = append(output, nonce...)
	return c.aead.Seal(output, nonce, body, output[:4]), nil
}

// Open authenticates and decodes one Controller-stored credential blob.
func (c *CredentialCipher) Open(ciphertext []byte) (S3Credentials, error) {
	if c == nil || c.aead == nil ||
		len(ciphertext) < 4+c.aead.NonceSize()+c.aead.Overhead() ||
		binary.BigEndian.Uint32(ciphertext[:4]) != credentialCipherVersion {
		return S3Credentials{}, fmt.Errorf("backup credentials: invalid ciphertext")
	}
	nonce := ciphertext[4 : 4+c.aead.NonceSize()]
	body, err := c.aead.Open(
		nil, nonce, ciphertext[4+c.aead.NonceSize():], ciphertext[:4],
	)
	if err != nil {
		return S3Credentials{}, fmt.Errorf("backup credentials: authenticate ciphertext: %w", err)
	}
	var credentials S3Credentials
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&credentials); err != nil {
		return S3Credentials{}, fmt.Errorf("backup credentials: decode: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return S3Credentials{}, fmt.Errorf("backup credentials: trailing data")
	}
	if strings.TrimSpace(credentials.AccessKey) == "" || credentials.SecretKey == "" {
		return S3Credentials{}, fmt.Errorf("backup credentials: incomplete plaintext")
	}
	return credentials, nil
}
