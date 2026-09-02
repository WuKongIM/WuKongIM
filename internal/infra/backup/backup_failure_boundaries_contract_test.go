package backup

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	"github.com/WuKongIM/WuKongIM/pkg/controller"
)

func TestRepositoryProbePreservesRemoteClassificationAndNodeIdentity(t *testing.T) {
	tests := []struct {
		name       string
		remoteErr  error
		wantReason backupcontract.RepositoryAccessReason
		wantStage  backupcontract.RepositoryAccessStage
	}{
		{
			name: "classified repository failure",
			remoteErr: &backupcontract.RepositoryAccessError{
				Reason:   backupcontract.RepositoryAccessDenied,
				Stage:    backupcontract.RepositoryAccessWriteReceipt,
				Provider: backupcontract.StoreKindFile,
			},
			wantReason: backupcontract.RepositoryAccessDenied,
			wantStage:  backupcontract.RepositoryAccessWriteReceipt,
		},
		{
			name:       "unreachable node",
			remoteErr:  errors.New("node transport unavailable"),
			wantReason: backupcontract.RepositoryAccessNodeUnreachable,
			wantStage:  backupcontract.RepositoryAccessReadMarker,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			probe, store := newClusterRepositoryProbeForTest(
				t,
				[]controller.Node{activeProbeDataNode(1), activeProbeDataNode(2)},
				probeRemoteFunc(func(
					context.Context,
					uint64,
					backupcontract.RepositoryProbeCommand,
				) error {
					return test.remoteErr
				}),
			)
			err := probe.ProbeRepository(
				context.Background(),
				backupcontract.StoreConfig{Kind: backupcontract.StoreKindFile},
				store,
			)
			var accessErr *backupcontract.RepositoryAccessError
			if !errors.As(err, &accessErr) || accessErr.NodeID != 2 ||
				accessErr.Reason != test.wantReason || accessErr.Stage != test.wantStage {
				t.Fatalf("ProbeRepository() error = %#v", accessErr)
			}
		})
	}
}

func TestArchiveFinalizerPropagatesEveryOrphanCleanupBoundary(t *testing.T) {
	if _, err := NewArchiveFinalizer(ArchiveFinalizerOptions{}); err == nil {
		t.Fatal("NewArchiveFinalizer(invalid) error = nil")
	}
	finalizer, err := NewArchiveFinalizer(ArchiveFinalizerOptions{
		ClusterID: "cluster", Application: "contract",
		Now: func() time.Time { return time.Unix(1_800_000_000, 0).UTC() },
	})
	if err != nil {
		t.Fatalf("NewArchiveFinalizer(): %v", err)
	}
	if err := finalizer.Publish(
		context.Background(), &orphanFailureStore{},
		backupcontract.BackupJob{Trigger: backupcontract.Trigger("invalid")},
	); err == nil {
		t.Fatal("Publish(invalid trigger) error = nil")
	}

	tests := []struct {
		name      string
		configure func(*orphanFailureStore)
	}{
		{
			name: "pending list",
			configure: func(store *orphanFailureStore) {
				store.listErr = errors.New("list failed")
			},
		},
		{
			name: "COMPLETE close",
			configure: func(store *orphanFailureStore) {
				store.completeReader = &errorReadCloser{err: errors.New("close failed")}
			},
		},
		{
			name: "COMPLETE open",
			configure: func(store *orphanFailureStore) {
				store.openErr = errors.New("open failed")
			},
		},
		{
			name: "archive subtree delete",
			configure: func(store *orphanFailureStore) {
				store.openErr = backupartifact.ErrObjectNotFound
				store.deletePrefixErr = errors.New("delete prefix failed")
			},
		},
		{
			name: "pending marker delete",
			configure: func(store *orphanFailureStore) {
				store.openErr = backupartifact.ErrObjectNotFound
				store.deleteErr = errors.New("delete marker failed")
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &orphanFailureStore{}
			test.configure(store)
			if err := finalizer.ApplyRetention(
				context.Background(), store, 1,
			); err == nil {
				t.Fatal("ApplyRetention() error = nil")
			}
		})
	}
}

func TestDistributedRestoreHealthRetryStopsAtCancellation(t *testing.T) {
	state := distributedRestoreContractState()
	remote := distributedRestoreRemoteFunc(func(
		_ context.Context,
		_ uint64,
		command backupcontract.RestoreNodeCommand,
	) (backupcontract.RestoreNodeReceipt, error) {
		if command.Action == backupcontract.RestoreNodeActionHealth {
			return backupcontract.RestoreNodeReceipt{}, errors.New("health unavailable")
		}
		return backupcontract.RestoreNodeReceipt{}, nil
	})
	executor, _, _ := newDistributedRestoreContractExecutor(t, state, remote)
	job := distributedRestoreContractJob()
	job.Slots = nil
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := executor.ActivateRestore(canceled, job); !errors.Is(
		err, context.Canceled,
	) {
		t.Fatalf("ActivateRestore(canceled health retry) error = %v", err)
	}
}

type orphanFailureStore struct {
	listErr         error
	openErr         error
	completeReader  io.ReadCloser
	deletePrefixErr error
	deleteErr       error
}

func (*orphanFailureStore) Put(context.Context, backupartifact.PutObject) error {
	return nil
}

func (s *orphanFailureStore) Open(
	context.Context,
	string,
) (io.ReadCloser, backupartifact.ArchiveObject, error) {
	if s.openErr != nil {
		return nil, backupartifact.ArchiveObject{}, s.openErr
	}
	reader := s.completeReader
	if reader == nil {
		reader = io.NopCloser(bytes.NewReader(nil))
	}
	return reader, backupartifact.ArchiveObject{}, nil
}

func (s *orphanFailureStore) List(
	context.Context,
	string,
) ([]backupartifact.ArchiveObject, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return []backupartifact.ArchiveObject{{
		Key:      "pending/orphan",
		Modified: time.Unix(1_700_000_000, 0).UTC(),
	}}, nil
}

func (s *orphanFailureStore) Delete(context.Context, string) error {
	return s.deleteErr
}

func (s *orphanFailureStore) DeletePrefix(context.Context, string) error {
	return s.deletePrefixErr
}

type errorReadCloser struct {
	err error
}

func (*errorReadCloser) Read([]byte) (int, error) { return 0, io.EOF }
func (r *errorReadCloser) Close() error           { return r.err }
