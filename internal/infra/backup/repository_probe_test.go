package backup

import (
	"context"
	"errors"
	"io"
	"testing"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	"github.com/WuKongIM/WuKongIM/pkg/controller"
)

func TestClusterRepositoryProbeCompletesAndCleansEveryStage(t *testing.T) {
	probe, baseStore := newClusterRepositoryProbeForTest(
		t,
		[]controller.Node{activeProbeDataNode(1)},
		probeRemoteFunc(func(
			context.Context,
			uint64,
			backupcontract.RepositoryProbeCommand,
		) error {
			t.Fatal("single-node cluster used remote probe")
			return nil
		}),
	)
	store := &recordingProbeStore{delegate: baseStore}

	if err := probe.ProbeRepository(
		context.Background(),
		backupcontract.StoreConfig{Kind: backupcontract.StoreKindFile},
		store,
	); err != nil {
		t.Fatalf("ProbeRepository(): %v", err)
	}
	if store.putCalls != 1 ||
		store.openCalls != 1 ||
		store.listCalls < 2 ||
		store.deletePrefixCalls != 1 {
		t.Fatalf(
			"operations put=%d open=%d list=%d delete_prefix=%d",
			store.putCalls, store.openCalls, store.listCalls,
			store.deletePrefixCalls,
		)
	}
	remaining, err := baseStore.List(context.Background(), "probes")
	if err != nil {
		t.Fatalf("List(probes): %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("probe objects remain: %#v", remaining)
	}
}

func TestClusterRepositoryProbeReportsListAndCleanupFailures(t *testing.T) {
	testCases := []struct {
		name   string
		store  func(backupartifact.ArchiveStore) *recordingProbeStore
		stage  backupcontract.RepositoryAccessStage
		reason backupcontract.RepositoryAccessReason
	}{
		{
			name: "list",
			store: func(delegate backupartifact.ArchiveStore) *recordingProbeStore {
				return &recordingProbeStore{
					delegate: delegate,
					listErr:  errors.New("list denied"),
				}
			},
			stage:  backupcontract.RepositoryAccessList,
			reason: backupcontract.RepositoryAccessListFailed,
		},
		{
			name: "delete",
			store: func(delegate backupartifact.ArchiveStore) *recordingProbeStore {
				return &recordingProbeStore{
					delegate:        delegate,
					deletePrefixErr: errors.New("delete denied"),
				}
			},
			stage:  backupcontract.RepositoryAccessDelete,
			reason: backupcontract.RepositoryAccessDeleteFailed,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			probe, baseStore := newClusterRepositoryProbeForTest(
				t,
				[]controller.Node{activeProbeDataNode(1)},
				probeRemoteFunc(func(
					context.Context,
					uint64,
					backupcontract.RepositoryProbeCommand,
				) error {
					return nil
				}),
			)
			store := testCase.store(baseStore)

			err := probe.ProbeRepository(
				context.Background(),
				backupcontract.StoreConfig{
					Kind: backupcontract.StoreKindFile,
				},
				store,
			)
			var accessErr *backupcontract.RepositoryAccessError
			if !errors.As(err, &accessErr) ||
				accessErr.Stage != testCase.stage ||
				accessErr.Reason != testCase.reason {
				t.Fatalf("ProbeRepository() error = %#v", accessErr)
			}
			if store.deletePrefixCalls != 1 ||
				!store.cleanupHadDeadline ||
				store.cleanupContextErr != nil {
				t.Fatalf(
					"cleanup calls=%d deadline=%v context_err=%v",
					store.deletePrefixCalls, store.cleanupHadDeadline,
					store.cleanupContextErr,
				)
			}
		})
	}
}

func TestClusterRepositoryProbeReportsExactMissingReceiptNode(t *testing.T) {
	probe, baseStore := newClusterRepositoryProbeForTest(
		t,
		[]controller.Node{
			activeProbeDataNode(1),
			activeProbeDataNode(2),
		},
		probeRemoteFunc(func(
			context.Context,
			uint64,
			backupcontract.RepositoryProbeCommand,
		) error {
			return nil
		}),
	)

	err := probe.ProbeRepository(
		context.Background(),
		backupcontract.StoreConfig{Kind: backupcontract.StoreKindFile},
		baseStore,
	)
	var accessErr *backupcontract.RepositoryAccessError
	if !errors.As(err, &accessErr) ||
		accessErr.Stage != backupcontract.RepositoryAccessReadReceipt ||
		accessErr.Reason != backupcontract.RepositoryAccessReadFailed ||
		accessErr.NodeID != 2 {
		t.Fatalf("ProbeRepository() error = %#v", accessErr)
	}
}

func newClusterRepositoryProbeForTest(
	t *testing.T,
	nodes []controller.Node,
	remote RepositoryProbeRemote,
) (*ClusterRepositoryProbe, backupartifact.ArchiveStore) {
	t.Helper()
	provider, err := NewRepositoryProvider(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("NewRepositoryProvider(): %v", err)
	}
	cluster := probeClusterForTest{
		nodeID: 1,
		state:  controller.ClusterState{Nodes: nodes},
	}
	probe, err := NewClusterRepositoryProbe(cluster, provider, remote)
	if err != nil {
		t.Fatalf("NewClusterRepositoryProbe(): %v", err)
	}
	store, err := provider.Open(
		context.Background(),
		backupcontract.StoreConfig{Kind: backupcontract.StoreKindFile},
	)
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	return probe, store
}

func activeProbeDataNode(nodeID uint64) controller.Node {
	return controller.Node{
		NodeID:    nodeID,
		Roles:     []controller.NodeRole{controller.NodeRoleData},
		JoinState: controller.NodeJoinStateActive,
	}
}

type probeClusterForTest struct {
	nodeID uint64
	state  controller.ClusterState
}

func (c probeClusterForTest) NodeID() uint64 {
	return c.nodeID
}

func (c probeClusterForTest) LocalState(
	context.Context,
) (controller.ClusterState, error) {
	return c.state.Clone(), nil
}

type probeRemoteFunc func(
	context.Context,
	uint64,
	backupcontract.RepositoryProbeCommand,
) error

func (f probeRemoteFunc) ProbeBackupRepository(
	ctx context.Context,
	nodeID uint64,
	command backupcontract.RepositoryProbeCommand,
) error {
	return f(ctx, nodeID, command)
}

type recordingProbeStore struct {
	delegate           backupartifact.ArchiveStore
	putCalls           int
	openCalls          int
	listCalls          int
	deletePrefixCalls  int
	listErr            error
	deletePrefixErr    error
	cleanupHadDeadline bool
	cleanupContextErr  error
}

func (s *recordingProbeStore) Put(
	ctx context.Context,
	object backupartifact.PutObject,
) error {
	s.putCalls++
	return s.delegate.Put(ctx, object)
}

func (s *recordingProbeStore) Open(
	ctx context.Context,
	key string,
) (io.ReadCloser, backupartifact.ArchiveObject, error) {
	s.openCalls++
	return s.delegate.Open(ctx, key)
}

func (s *recordingProbeStore) List(
	ctx context.Context,
	prefix string,
) ([]backupartifact.ArchiveObject, error) {
	s.listCalls++
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.delegate.List(ctx, prefix)
}

func (s *recordingProbeStore) Delete(
	ctx context.Context,
	key string,
) error {
	return s.delegate.Delete(ctx, key)
}

func (s *recordingProbeStore) DeletePrefix(
	ctx context.Context,
	prefix string,
) error {
	s.deletePrefixCalls++
	_, s.cleanupHadDeadline = ctx.Deadline()
	s.cleanupContextErr = ctx.Err()
	if s.deletePrefixErr != nil {
		return s.deletePrefixErr
	}
	return s.delegate.DeletePrefix(ctx, prefix)
}
