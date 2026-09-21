package backup

import (
	"context"
	"errors"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
)

// ResolveBackupStore binds an incoming repository reference to the exact plan
// in this node's Controller mirror. A lagging or rotated credential is rejected;
// no caller-provided ciphertext or remote credential lookup is accepted.
func (s *ScheduledControllerStateStore) ResolveBackupStore(
	ctx context.Context,
	reference backupcontract.StoreReference,
) (backupcontract.StoreConfig, error) {
	if err := ctx.Err(); err != nil {
		return backupcontract.StoreConfig{}, err
	}
	if s == nil || s.controller == nil {
		return backupcontract.StoreConfig{}, errors.New("backup repository state unavailable")
	}
	state, err := s.controller.LocalState(ctx)
	if err != nil {
		return backupcontract.StoreConfig{}, err
	}
	if state.ScheduledBackup == nil || state.ScheduledBackup.Plan == nil {
		return backupcontract.StoreConfig{}, errors.New("backup repository plan unavailable")
	}
	store := planFromController(*state.ScheduledBackup.Plan).Store
	if store.Reference() != reference {
		return backupcontract.StoreConfig{}, errors.New("backup repository reference mismatch")
	}
	switch store.Kind {
	case backupcontract.StoreKindFile:
		if reference != (backupcontract.StoreReference{Kind: backupcontract.StoreKindFile}) || len(store.CredentialCiphertext) != 0 {
			return backupcontract.StoreConfig{}, errors.New("invalid file repository reference")
		}
	case backupcontract.StoreKindOSS, backupcontract.StoreKindCOS, backupcontract.StoreKindS3:
		if store.CredentialRevision == 0 || len(store.CredentialCiphertext) == 0 {
			return backupcontract.StoreConfig{}, errors.New("backup repository credential unavailable")
		}
	default:
		return backupcontract.StoreConfig{}, errors.New("unsupported backup repository reference")
	}
	return store, nil
}
