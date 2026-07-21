package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"reflect"
	"time"

	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

// RestorePointVerifier performs a full signed chain audit in both repositories.
type RestorePointVerifier struct {
	primary   backupartifact.Repository
	secondary backupartifact.Repository
	signer    backupartifact.ManifestSigner
	now       func() time.Time
}

// NewRestorePointVerifier creates a dual-repository verifier.
func NewRestorePointVerifier(primary, secondary backupartifact.Repository, signer backupartifact.ManifestSigner, now func() time.Time) (*RestorePointVerifier, error) {
	if primary == nil || secondary == nil || signer == nil || now == nil || primary.Name() == "" || primary.Name() == secondary.Name() {
		return nil, fmt.Errorf("backup verifier: invalid options")
	}
	return &RestorePointVerifier{primary: primary, secondary: secondary, signer: signer, now: now}, nil
}

// Verify loads every partition layer and object from both copies.
func (v *RestorePointVerifier) Verify(ctx context.Context, restorePointID string) (backupusecase.Verification, error) {
	primary, err := backupartifact.LoadRestorePoint(ctx, v.primary, restorePointID, v.signer)
	if err != nil {
		return backupusecase.Verification{}, err
	}
	secondary, err := backupartifact.LoadRestorePoint(ctx, v.secondary, restorePointID, v.signer)
	if err != nil {
		return backupusecase.Verification{}, err
	}
	if !reflect.DeepEqual(primary, secondary) {
		return backupusecase.Verification{}, fmt.Errorf("%w: repository manifests differ", backupartifact.ErrRepositoryIncomplete)
	}
	body, err := backupartifact.MarshalManifest(primary)
	if err != nil {
		return backupusecase.Verification{}, err
	}
	hash := sha256.Sum256(body)
	return backupusecase.Verification{
		RestorePointID: restorePointID, VerifiedAtUnixMillis: v.now().UTC().UnixMilli(),
		PrimaryVerified: true, SecondaryVerified: true, ManifestSHA256: hex.EncodeToString(hash[:]),
	}, nil
}

var _ backupusecase.RestorePointVerifier = (*RestorePointVerifier)(nil)
