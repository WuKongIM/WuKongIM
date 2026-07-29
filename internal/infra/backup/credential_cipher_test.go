package backup_test

import (
	"bytes"
	"testing"

	backupinfra "github.com/WuKongIM/WuKongIM/internal/infra/backup"
)

func TestCredentialCipherRoundTripIsClusterAndSecretBound(t *testing.T) {
	cipher, err := backupinfra.NewCredentialCipher(
		"manager-installation-secret", "cluster-a",
	)
	if err != nil {
		t.Fatalf("NewCredentialCipher(): %v", err)
	}
	credentials := backupinfra.S3Credentials{
		AccessKey: "access-key", SecretKey: "secret-key",
	}
	sealed, err := cipher.Seal(credentials)
	if err != nil {
		t.Fatalf("Seal(): %v", err)
	}
	if bytes.Contains(sealed, []byte(credentials.AccessKey)) ||
		bytes.Contains(sealed, []byte(credentials.SecretKey)) {
		t.Fatal("ciphertext contains plaintext credentials")
	}
	opened, err := cipher.Open(sealed)
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	if opened != credentials {
		t.Fatalf("opened = %#v, want %#v", opened, credentials)
	}

	wrong, err := backupinfra.NewCredentialCipher(
		"another-manager-secret", "cluster-a",
	)
	if err != nil {
		t.Fatalf("NewCredentialCipher(wrong): %v", err)
	}
	if _, err := wrong.Open(sealed); err == nil {
		t.Fatal("Open(wrong secret) error = nil")
	}
}
