package backup

import (
	"context"
	"fmt"
	"path/filepath"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

// RepositoryProvider resolves the single plan repository without exposing
// plaintext credentials through Controller state.
type RepositoryProvider struct {
	fileRoot string
	cipher   *CredentialCipher
}

// NewRepositoryProvider creates a plan-driven archive store provider.
func NewRepositoryProvider(
	dataDir string,
	cipher *CredentialCipher,
) (*RepositoryProvider, error) {
	if dataDir == "" {
		return nil, fmt.Errorf("backup repository provider: data directory is required")
	}
	root, err := filepath.Abs(filepath.Join(dataDir, "backup-repository"))
	if err != nil {
		return nil, err
	}
	return &RepositoryProvider{fileRoot: root, cipher: cipher}, nil
}

// Open creates the store selected by one durable plan snapshot.
func (p *RepositoryProvider) Open(
	_ context.Context,
	config backupcontract.StoreConfig,
) (backupartifact.ArchiveStore, error) {
	if p == nil {
		return nil, fmt.Errorf("backup repository provider: unavailable")
	}
	switch config.Kind {
	case backupcontract.StoreKindFile:
		return NewFileArchiveStore(p.fileRoot)
	case backupcontract.StoreKindS3:
		if p.cipher == nil {
			return nil, fmt.Errorf(
				"backup repository provider: Manager authentication secret is required for S3 credentials",
			)
		}
		credentials, err := p.cipher.Open(config.CredentialCiphertext)
		if err != nil {
			return nil, err
		}
		return NewS3ArchiveStore(S3ArchiveStoreOptions{
			Endpoint: config.Endpoint, Region: config.Region,
			Bucket: config.Bucket, Prefix: config.Prefix,
			PathStyle: config.PathStyle,
			AccessKey: credentials.AccessKey, SecretKey: credentials.SecretKey,
		})
	default:
		return nil, fmt.Errorf("backup repository provider: unsupported store kind %q", config.Kind)
	}
}

// SealS3Credentials encrypts a replacement credential before plan publication.
func (p *RepositoryProvider) SealS3Credentials(
	accessKey string,
	secretKey string,
) ([]byte, error) {
	if p == nil || p.cipher == nil {
		return nil, fmt.Errorf("backup repository provider: unavailable")
	}
	return p.cipher.Seal(S3Credentials{
		AccessKey: accessKey, SecretKey: secretKey,
	})
}
