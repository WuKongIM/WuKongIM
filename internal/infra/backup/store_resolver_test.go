package backup_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupinfra "github.com/WuKongIM/WuKongIM/internal/infra/backup"
)

func TestBackupStoreResolverUsesExactLocalPlanAndCredentialRevision(t *testing.T) {
	runtime := &fakeScheduledBackupController{}
	resolver, err := backupinfra.NewScheduledControllerStateStore(runtime)
	if err != nil {
		t.Fatal(err)
	}
	state := scheduledSystemState()
	if err := resolver.CompareAndSwap(context.Background(), 0, state); err != nil {
		t.Fatal(err)
	}
	ref := state.Plan.Store.Reference()
	got, err := resolver.ResolveBackupStore(context.Background(), ref)
	if err != nil || !bytes.Equal(got.CredentialCiphertext, state.Plan.Store.CredentialCiphertext) {
		t.Fatal("exact Controller credential did not resolve")
	}
	got.CredentialCiphertext[0] ^= 1
	again, err := resolver.ResolveBackupStore(context.Background(), ref)
	if err != nil || !bytes.Equal(again.CredentialCiphertext, state.Plan.Store.CredentialCiphertext) {
		t.Fatal("resolver exposed aliased Controller credentials")
	}
	for _, mutate := range []func(*backupcontract.StoreReference){
		func(r *backupcontract.StoreReference) { r.Kind = backupcontract.StoreKindOSS },
		func(r *backupcontract.StoreReference) { r.Endpoint += "/changed" },
		func(r *backupcontract.StoreReference) { r.Region += "changed" },
		func(r *backupcontract.StoreReference) { r.Bucket += "changed" },
		func(r *backupcontract.StoreReference) { r.Prefix += "/changed" },
		func(r *backupcontract.StoreReference) { r.PathStyle = !r.PathStyle },
		func(r *backupcontract.StoreReference) { r.CredentialRevision++ },
	} {
		changed := ref
		mutate(&changed)
		if resolved, err := resolver.ResolveBackupStore(context.Background(), changed); err == nil || len(resolved.CredentialCiphertext) != 0 {
			t.Fatal("mismatched reference resolved a credential")
		}
	}
	state.Plan.Store.CredentialRevision++
	state.Plan.Store.CredentialCiphertext = []byte("rotated-encrypted-credential")
	if err := resolver.CompareAndSwap(context.Background(), 1, state); err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.ResolveBackupStore(context.Background(), ref); err == nil {
		t.Fatal("old credential revision remained usable after rotation")
	}
	if got, err := resolver.ResolveBackupStore(context.Background(), state.Plan.Store.Reference()); err != nil || !bytes.Equal(got.CredentialCiphertext, state.Plan.Store.CredentialCiphertext) {
		t.Fatal("new credential revision did not resolve")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := resolver.ResolveBackupStore(ctx, state.Plan.Store.Reference()); !errors.Is(err, context.Canceled) {
		t.Fatal("resolver lost caller cancellation")
	}
}

func TestBackupStoreResolverRejectsMissingPlanAndCredentials(t *testing.T) {
	runtime := &fakeScheduledBackupController{}
	resolver, err := backupinfra.NewScheduledControllerStateStore(runtime)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.ResolveBackupStore(context.Background(), backupcontract.StoreReference{}); err == nil {
		t.Fatal("missing plan was accepted")
	}
	state := scheduledSystemState()
	state.Plan.Store.CredentialCiphertext = nil
	if err := resolver.CompareAndSwap(context.Background(), 0, state); err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.ResolveBackupStore(context.Background(), state.Plan.Store.Reference()); err == nil {
		t.Fatal("missing encrypted credential was accepted")
	}
	state.Plan.Store = backupcontract.StoreConfig{Kind: backupcontract.StoreKindFile}
	if err := resolver.CompareAndSwap(context.Background(), 1, state); err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.ResolveBackupStore(context.Background(), state.Plan.Store.Reference()); err != nil {
		t.Fatal("canonical file repository failed to resolve")
	}
}
