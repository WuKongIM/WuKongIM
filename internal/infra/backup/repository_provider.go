package backup

import (
	"context"
	"fmt"
	"os"
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
	// Resolve a pre-mounted repository alias once at startup. The resulting
	// store remains anchored to the real directory and still refuses every
	// symlink below that root.
	resolved, resolveErr := filepath.EvalSymlinks(root)
	if resolveErr == nil {
		root = resolved
	} else if !os.IsNotExist(resolveErr) {
		return nil, fmt.Errorf(
			"backup repository provider: resolve file root: %w", resolveErr,
		)
	}
	return &RepositoryProvider{fileRoot: root, cipher: cipher}, nil
}

// Open creates the store selected by one durable plan snapshot.
func (p *RepositoryProvider) Open(
	_ context.Context,
	config backupcontract.StoreConfig,
) (backupartifact.ArchiveStore, error) {
	if p == nil {
		return nil, classifyRepositoryError(
			config.Kind, backupcontract.RepositoryAccessOpen,
			fmt.Errorf("backup repository provider: unavailable"),
		)
	}
	switch config.Kind {
	case backupcontract.StoreKindFile:
		store, err := NewFileArchiveStore(p.fileRoot)
		return store, classifyRepositoryError(
			config.Kind, backupcontract.RepositoryAccessOpen, err,
		)
	case backupcontract.StoreKindOSS,
		backupcontract.StoreKindCOS,
		backupcontract.StoreKindS3:
		if p.cipher == nil {
			return nil, classifyRepositoryError(
				config.Kind, backupcontract.RepositoryAccessOpen,
				fmt.Errorf(
					"backup repository provider: Manager authentication secret is required for object storage credentials",
				),
			)
		}
		credentials, err := p.cipher.Open(config.CredentialCiphertext)
		if err != nil {
			return nil, classifyRepositoryError(
				config.Kind, backupcontract.RepositoryAccessOpen, err,
			)
		}
		store, err := NewS3ArchiveStore(S3ArchiveStoreOptions{
			Endpoint: repositoryEndpoint(config), Region: config.Region,
			Bucket: config.Bucket, Prefix: config.Prefix,
			PathStyle: config.PathStyle,
			VirtualHost: config.Kind == backupcontract.StoreKindOSS ||
				config.Kind == backupcontract.StoreKindCOS,
			AccessKey: credentials.AccessKey, SecretKey: credentials.SecretKey,
		})
		return store, classifyRepositoryError(
			config.Kind, backupcontract.RepositoryAccessOpen, err,
		)
	default:
		return nil, classifyRepositoryError(
			config.Kind, backupcontract.RepositoryAccessOpen,
			fmt.Errorf(
				"backup repository provider: unsupported store kind %q",
				config.Kind,
			),
		)
	}
}

func repositoryEndpoint(config backupcontract.StoreConfig) string {
	if config.Endpoint != "" {
		return config.Endpoint
	}
	switch config.Kind {
	case backupcontract.StoreKindOSS:
		return "https://oss-" + config.Region + ".aliyuncs.com"
	case backupcontract.StoreKindCOS:
		return "https://cos." + config.Region + ".myqcloud.com"
	default:
		return ""
	}
}

// SealObjectStoreCredentials encrypts a replacement credential before plan
// publication.
func (p *RepositoryProvider) SealObjectStoreCredentials(
	accessKey string,
	secretKey string,
) ([]byte, error) {
	if p == nil || p.cipher == nil {
		return nil, fmt.Errorf("backup repository provider: unavailable")
	}
	return p.cipher.Seal(ObjectStoreCredentials{
		AccessKey: accessKey, SecretKey: secretKey,
	})
}
