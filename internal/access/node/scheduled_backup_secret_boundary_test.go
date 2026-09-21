package node

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"reflect"
	"strings"
	"testing"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
)

func TestScheduledBackupRequestsNeverTransmitCredentialCiphertext(t *testing.T) {
	store := backupcontract.StoreConfig{
		Kind: backupcontract.StoreKindS3, Endpoint: "https://objects.example.test",
		Region: "test-region", Bucket: "archive", Prefix: "cluster", PathStyle: true,
		CredentialRevision: 7, CredentialCiphertext: []byte("fixture-encrypted-credential"),
	}
	tests := []struct {
		name string
		call func(*Client)
	}{
		{"slot", func(c *Client) {
			_, _ = c.ExportBackupSlot(context.Background(), 2, backupcontract.SlotExportCommand{
				Plan: backupcontract.Plan{Store: store}, OwnerNodeID: 2,
			})
		}},
		{"messages", func(c *Client) {
			_, _ = c.ExportBackupMessages(context.Background(), 2, backupcontract.MessageExportCommand{
				Store: store, Shard: backupcontract.MessageShard{NodeID: 2},
			})
		}},
		{"probe", func(c *Client) {
			_ = c.ProbeBackupRepository(context.Background(), 2, backupcontract.RepositoryProbeCommand{Store: store})
		}},
		{"restore", func(c *Client) {
			_, _ = c.RunBackupRestoreNode(context.Background(), 2, backupcontract.RestoreNodeCommand{Store: store})
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := &backupWireCapture{}
			tt.call(NewClient(n))
			if len(n.payload) < 5 {
				t.Fatal("request did not reach the real RPC transport seam")
			}
			for _, forbidden := range [][]byte{
				[]byte("credential_ciphertext"), store.CredentialCiphertext,
				[]byte(base64.StdEncoding.EncodeToString(store.CredentialCiphertext)),
			} {
				if bytes.Contains(n.payload, forbidden) {
					t.Error("backup request transmitted credential ciphertext")
				}
			}
			if !bytes.Contains(n.payload, []byte(`"credential_revision":7`)) {
				t.Error("backup request lost the exact credential revision")
			}
		})
	}
}

type backupWireCapture struct {
	payload []byte
}

func (n *backupWireCapture) CallRPC(_ context.Context, _ uint64, _ uint8, payload []byte) ([]byte, error) {
	n.payload = bytes.Clone(payload)
	return nil, errors.New("captured request")
}

func TestScheduledBackupReceiversResolveExactTargetCredentials(t *testing.T) {
	store := backupcontract.StoreConfig{Kind: backupcontract.StoreKindS3, Endpoint: "https://objects.example.test", Bucket: "archive", CredentialRevision: 7, CredentialCiphertext: []byte("target-local-encrypted-credential")}
	for _, operation := range []string{"slot", "messages", "probe", "restore"} {
		t.Run(operation, func(t *testing.T) {
			effects := &backupStoreEffects{}
			resolver := &backupStoreResolverStub{store: store}
			adapter := New(Options{ScheduledBackup: effects, ScheduledBackupProbe: effects, ScheduledRestore: effects, ScheduledBackupStores: resolver})
			senderStore := store
			senderStore.CredentialCiphertext = []byte("sender-ciphertext-must-not-be-used")
			call, handler := backupStoreOperation(operation, adapter, senderStore)
			node := &fakeManagerConnectionRPCNode{handler: handler}
			if err := call(NewClient(node)); err != nil {
				t.Fatal(err)
			}
			if resolver.calls != 1 || len(effects.stores) != 1 || !reflect.DeepEqual(effects.stores[0], store) {
				t.Fatal("receiver did not execute with the exact target-local credential")
			}
			if string(senderStore.CredentialCiphertext) != "sender-ciphertext-must-not-be-used" {
				t.Fatal("encoding mutated the caller's credential")
			}

			for _, mode := range []string{"unwired", "stale", "error", "canceled"} {
				t.Run(mode, func(t *testing.T) {
					effects.stores = nil
					resolver.err = nil
					resolver.store = store
					adapter.scheduledBackupStores = resolver
					switch mode {
					case "unwired":
						adapter.scheduledBackupStores = nil
					case "stale":
						resolver.store.CredentialRevision++
					case "error":
						resolver.err = errors.New("private credential-source error")
					case "canceled":
						resolver.err = context.Canceled
					}
					err := call(NewClient(node))
					if err == nil || len(effects.stores) != 0 {
						t.Fatal("unresolved repository executed an effect")
					}
					if strings.Contains(err.Error(), "private") {
						t.Fatal("resolver error leaked across RPC")
					}
					if mode == "canceled" && !errors.Is(err, context.Canceled) {
						t.Fatal("lost cancellation identity")
					}
				})
			}
		})
	}
}

func TestScheduledBackupRejectsLegacyAndInjectedCredentialRequests(t *testing.T) {
	store := backupcontract.StoreConfig{Kind: backupcontract.StoreKindFile}
	for _, operation := range []string{"slot", "messages", "probe", "restore"} {
		t.Run(operation, func(t *testing.T) {
			effects := &backupStoreEffects{}
			resolver := &backupStoreResolverStub{store: store}
			adapter := New(Options{ScheduledBackup: effects, ScheduledBackupProbe: effects, ScheduledRestore: effects, ScheduledBackupStores: resolver})
			call, handler := backupStoreOperation(operation, adapter, store)
			wire := &backupWireCapture{}
			_ = call(NewClient(wire))
			if len(wire.payload) < 5 || wire.payload[4] != 2 {
				t.Fatal("sender did not emit credential-reference protocol version 2")
			}
			legacy := bytes.Clone(wire.payload)
			legacy[4] = 1
			injected := bytes.Replace(wire.payload, []byte(`"store":{`), []byte(`"store":{"credential_ciphertext":"c2VjcmV0",`), 1)
			for _, payload := range [][]byte{legacy, injected} {
				if _, err := handler(context.Background(), payload); err == nil {
					t.Fatal("unsafe request was accepted")
				}
			}
			if resolver.calls != 0 || len(effects.stores) != 0 {
				t.Fatal("invalid protocol reached repository resolution or effects")
			}
		})
	}
}

func backupStoreOperation(operation string, adapter *Adapter, store backupcontract.StoreConfig) (func(*Client) error, func(context.Context, []byte) ([]byte, error)) {
	switch operation {
	case "slot":
		return func(c *Client) error {
			_, err := c.ExportBackupSlot(context.Background(), 2, backupcontract.SlotExportCommand{Plan: backupcontract.Plan{Store: store}, OwnerNodeID: 2})
			return err
		}, adapter.HandleScheduledBackupSlotRPC
	case "messages":
		return func(c *Client) error {
			_, err := c.ExportBackupMessages(context.Background(), 2, backupcontract.MessageExportCommand{Store: store, Shard: backupcontract.MessageShard{NodeID: 2}})
			return err
		}, adapter.HandleScheduledBackupMessageRPC
	case "probe":
		return func(c *Client) error {
			return c.ProbeBackupRepository(context.Background(), 2, backupcontract.RepositoryProbeCommand{Store: store})
		}, adapter.HandleScheduledBackupRepositoryProbeRPC
	default:
		return func(c *Client) error {
			_, err := c.RunBackupRestoreNode(context.Background(), 2, backupcontract.RestoreNodeCommand{Store: store})
			return err
		}, adapter.HandleScheduledBackupRestoreRPC
	}
}

type backupStoreResolverStub struct {
	store backupcontract.StoreConfig
	err   error
	calls int
}

func (r *backupStoreResolverStub) ResolveBackupStore(_ context.Context, _ backupcontract.StoreReference) (backupcontract.StoreConfig, error) {
	r.calls++
	return r.store, r.err
}

type backupStoreEffects struct{ stores []backupcontract.StoreConfig }

func (e *backupStoreEffects) ExportSlot(_ context.Context, c backupcontract.SlotExportCommand) (backupcontract.SlotExportReceipt, error) {
	e.stores = append(e.stores, c.Plan.Store)
	return backupcontract.SlotExportReceipt{}, nil
}
func (e *backupStoreEffects) ExportMessages(_ context.Context, c backupcontract.MessageExportCommand) (backupcontract.MessageExportReceipt, error) {
	e.stores = append(e.stores, c.Store)
	return backupcontract.MessageExportReceipt{}, nil
}
func (e *backupStoreEffects) ObserveRepositoryProbe(_ context.Context, c backupcontract.RepositoryProbeCommand) error {
	e.stores = append(e.stores, c.Store)
	return nil
}
func (e *backupStoreEffects) Run(_ context.Context, c backupcontract.RestoreNodeCommand) (backupcontract.RestoreNodeReceipt, error) {
	e.stores = append(e.stores, c.Store)
	return backupcontract.RestoreNodeReceipt{}, nil
}

func TestScheduledBackupRejectsSecretsEmbeddedInRepositoryEndpoint(t *testing.T) {
	for _, endpoint := range []string{"https://user:password@objects.example.test", "https://objects.example.test?token=private", "https://objects.example.test/#private", "file:///private/repository"} {
		for _, operation := range []string{"slot", "messages", "probe", "restore"} {
			store := backupcontract.StoreConfig{Kind: backupcontract.StoreKindS3, Endpoint: endpoint}
			call, _ := backupStoreOperation(operation, &Adapter{}, store)
			wire := &backupWireCapture{}
			err := call(NewClient(wire))
			if err == nil || len(wire.payload) != 0 {
				t.Fatal("secret-bearing endpoint reached transport")
			}
			if strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "password") {
				t.Fatal("endpoint secret leaked in validation error")
			}
		}
	}
}
