package cluster

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/stretchr/testify/require"
)

type controllerClientTestDiscovery struct {
	addrs map[uint64]string
}

func (d *controllerClientTestDiscovery) Resolve(nodeID uint64) (string, error) {
	addr, ok := d.addrs[nodeID]
	if !ok {
		return "", transport.ErrNodeNotFound
	}
	return addr, nil
}

func TestClusterGetReconcileTaskFallsThroughSlowStaleLeaderToCurrentLeader(t *testing.T) {
	slowFollower := transport.NewServer()
	slowFollowerMux := transport.NewRPCMux()
	slowFollowerMux.Handle(rpcServiceController, func(_ context.Context, body []byte) ([]byte, error) {
		req, err := decodeControllerRequest(body)
		require.NoError(t, err)
		require.Equal(t, controllerRPCGetTask, req.Kind)

		time.Sleep(defaultControllerRequestTimeout + 250*time.Millisecond)
		return encodeControllerResponse(req.Kind, controllerRPCResponse{
			NotLeader: true,
			LeaderID:  2,
		})
	})
	slowFollower.HandleRPCMux(slowFollowerMux)
	require.NoError(t, slowFollower.Start("127.0.0.1:0"))
	t.Cleanup(slowFollower.Stop)

	leader := transport.NewServer()
	leaderMux := transport.NewRPCMux()
	leaderMux.Handle(rpcServiceController, func(_ context.Context, body []byte) ([]byte, error) {
		req, err := decodeControllerRequest(body)
		require.NoError(t, err)
		require.Equal(t, controllerRPCGetTask, req.Kind)

		task := controllermeta.ReconcileTask{
			SlotID:    req.SlotID,
			Kind:      controllermeta.TaskKindRepair,
			Step:      controllermeta.TaskStepAddLearner,
			Attempt:   2,
			Status:    controllermeta.TaskStatusRetrying,
			NextRunAt: time.Now().Add(time.Second),
			LastError: "injected repair failure",
		}
		return encodeControllerResponse(req.Kind, controllerRPCResponse{Task: &task})
	})
	leader.HandleRPCMux(leaderMux)
	require.NoError(t, leader.Start("127.0.0.1:0"))
	t.Cleanup(leader.Stop)

	discovery := &controllerClientTestDiscovery{
		addrs: map[uint64]string{
			1: slowFollower.Listener().Addr().String(),
			2: leader.Listener().Addr().String(),
		},
	}
	pool := transport.NewPool(discovery, 1, 50*time.Millisecond)
	t.Cleanup(pool.Close)

	client := transport.NewClient(pool)
	t.Cleanup(client.Stop)

	cluster := &Cluster{
		cfg: Config{NodeID: 3},
		transportResources: transportResources{
			fwdClient: client,
		},
	}
	controllerClient := newControllerClient(cluster, []NodeConfig{
		{NodeID: 1},
		{NodeID: 2},
	}, nil)
	controllerClient.setLeader(1)
	cluster.controllerClient = controllerClient

	ctx, cancel := context.WithTimeout(context.Background(), defaultControllerLeaderWaitTimeout)
	defer cancel()

	task, err := cluster.GetReconcileTask(ctx, 9)
	require.NoError(t, err)
	require.Equal(t, uint32(9), task.SlotID)
	require.Equal(t, uint32(2), task.Attempt)
	require.Equal(t, controllermeta.TaskStatusRetrying, task.Status)
}

func TestControllerClientForceReconcileFallsThroughRawNotLeaderError(t *testing.T) {
	staleLeader := transport.NewServer()
	staleLeaderMux := transport.NewRPCMux()
	staleLeaderMux.Handle(rpcServiceController, func(_ context.Context, body []byte) ([]byte, error) {
		req, err := decodeControllerRequest(body)
		require.NoError(t, err)
		require.Equal(t, controllerRPCForceReconcile, req.Kind)
		return nil, ErrNotLeader
	})
	staleLeader.HandleRPCMux(staleLeaderMux)
	require.NoError(t, staleLeader.Start("127.0.0.1:0"))
	t.Cleanup(staleLeader.Stop)

	leader := transport.NewServer()
	leaderMux := transport.NewRPCMux()
	leaderMux.Handle(rpcServiceController, func(_ context.Context, body []byte) ([]byte, error) {
		req, err := decodeControllerRequest(body)
		require.NoError(t, err)
		require.Equal(t, controllerRPCForceReconcile, req.Kind)
		return encodeControllerResponse(req.Kind, controllerRPCResponse{})
	})
	leader.HandleRPCMux(leaderMux)
	require.NoError(t, leader.Start("127.0.0.1:0"))
	t.Cleanup(leader.Stop)

	discovery := &controllerClientTestDiscovery{
		addrs: map[uint64]string{
			1: staleLeader.Listener().Addr().String(),
			2: leader.Listener().Addr().String(),
		},
	}
	pool := transport.NewPool(discovery, 1, 50*time.Millisecond)
	t.Cleanup(pool.Close)

	client := transport.NewClient(pool)
	t.Cleanup(client.Stop)

	cluster := &Cluster{
		cfg: Config{NodeID: 3},
		transportResources: transportResources{
			fwdClient: client,
		},
	}
	controllerClient := newControllerClient(cluster, []NodeConfig{
		{NodeID: 1},
		{NodeID: 2},
	}, nil)
	controllerClient.setLeader(1)

	err := controllerClient.ForceReconcile(context.Background(), 9)
	require.NoError(t, err)
	require.Equal(t, multiraft.NodeID(2), controllerClient.cachedLeader())
}

func TestControllerClientLeaderCacheUsesAtomicUint64(t *testing.T) {
	field, ok := reflect.TypeOf(controllerClient{}).FieldByName("leader")
	require.True(t, ok)
	require.Equal(t, reflect.TypeOf(atomic.Uint64{}), field.Type)
}

func TestControllerClientLogsRetryableRedirect(t *testing.T) {
	staleLeader := transport.NewServer()
	staleLeaderMux := transport.NewRPCMux()
	staleLeaderMux.Handle(rpcServiceController, func(_ context.Context, body []byte) ([]byte, error) {
		req, err := decodeControllerRequest(body)
		require.NoError(t, err)
		require.Equal(t, controllerRPCForceReconcile, req.Kind)
		return nil, ErrNotLeader
	})
	staleLeader.HandleRPCMux(staleLeaderMux)
	require.NoError(t, staleLeader.Start("127.0.0.1:0"))
	t.Cleanup(staleLeader.Stop)

	leader := transport.NewServer()
	leaderMux := transport.NewRPCMux()
	leaderMux.Handle(rpcServiceController, func(_ context.Context, body []byte) ([]byte, error) {
		req, err := decodeControllerRequest(body)
		require.NoError(t, err)
		require.Equal(t, controllerRPCForceReconcile, req.Kind)
		return encodeControllerResponse(req.Kind, controllerRPCResponse{})
	})
	leader.HandleRPCMux(leaderMux)
	require.NoError(t, leader.Start("127.0.0.1:0"))
	t.Cleanup(leader.Stop)

	discovery := &controllerClientTestDiscovery{
		addrs: map[uint64]string{
			1: staleLeader.Listener().Addr().String(),
			2: leader.Listener().Addr().String(),
		},
	}
	pool := transport.NewPool(discovery, 1, 50*time.Millisecond)
	t.Cleanup(pool.Close)

	client := transport.NewClient(pool)
	t.Cleanup(client.Stop)

	logger := newRecordingLogger("cluster")
	cluster := &Cluster{
		cfg:    Config{NodeID: 3},
		logger: logger,
		transportResources: transportResources{
			fwdClient: client,
		},
	}
	controllerClient := newControllerClient(cluster, []NodeConfig{{NodeID: 1}, {NodeID: 2}}, nil)
	controllerClient.setLeader(1)

	err := controllerClient.ForceReconcile(context.Background(), 9)
	require.NoError(t, err)

	entry := requireRecordedLogEntry(t, logger, "WARN", "cluster.controller", "cluster.controller.rpc.retrying")
	require.Equal(t, "controller rpc attempt failed, retrying", entry.msg)
	require.Equal(t, uint64(1), requireRecordedField[uint64](t, entry, "targetNodeID"))
	require.Equal(t, uint64(9), requireRecordedField[uint64](t, entry, "slotID"))
	require.Equal(t, controllerRPCForceReconcile, requireRecordedField[string](t, entry, "rpc"))
	require.ErrorContains(t, requireRecordedField[error](t, entry, "error"), ErrNotLeader.Error())
}

func TestControllerClientLogsFinalFailure(t *testing.T) {
	discovery := &controllerClientTestDiscovery{addrs: map[uint64]string{}}
	pool := transport.NewPool(discovery, 1, 50*time.Millisecond)
	t.Cleanup(pool.Close)

	client := transport.NewClient(pool)
	t.Cleanup(client.Stop)

	logger := newRecordingLogger("cluster")
	cluster := &Cluster{
		cfg:    Config{NodeID: 3},
		logger: logger,
		transportResources: transportResources{
			fwdClient: client,
		},
	}
	controllerClient := newControllerClient(cluster, []NodeConfig{{NodeID: 1}}, nil)
	controllerClient.setLeader(1)

	err := controllerClient.ForceReconcile(context.Background(), 9)
	require.Error(t, err)

	entry := requireRecordedLogEntry(t, logger, "ERROR", "cluster.controller", "cluster.controller.rpc.failed")
	require.Equal(t, "controller rpc failed", entry.msg)
	require.Equal(t, uint64(1), requireRecordedField[uint64](t, entry, "targetNodeID"))
	require.Equal(t, uint64(9), requireRecordedField[uint64](t, entry, "slotID"))
	require.Equal(t, controllerRPCForceReconcile, requireRecordedField[string](t, entry, "rpc"))
	require.Equal(t, transport.ErrNodeNotFound, requireRecordedField[error](t, entry, "error"))
}

func TestControllerClientBestEffortReadFailureLogsWarning(t *testing.T) {
	discovery := &controllerClientTestDiscovery{addrs: map[uint64]string{}}
	pool := transport.NewPool(discovery, 1, 50*time.Millisecond)
	t.Cleanup(pool.Close)

	client := transport.NewClient(pool)
	t.Cleanup(client.Stop)

	logger := newRecordingLogger("cluster")
	cluster := &Cluster{
		cfg:    Config{NodeID: 3},
		logger: logger,
		transportResources: transportResources{
			fwdClient: client,
		},
	}
	controllerClient := newControllerClient(cluster, []NodeConfig{{NodeID: 1}}, nil)

	_, err := controllerClient.ListNodes(WithBestEffortControllerRead(context.Background()))
	require.ErrorIs(t, err, transport.ErrNodeNotFound)

	entry := requireRecordedLogEntry(t, logger, "WARN", "cluster.controller", "cluster.controller.rpc.failed")
	require.Equal(t, "controller rpc read failed", entry.msg)
	require.Equal(t, controllerRPCListNodes, requireRecordedField[string](t, entry, "rpc"))
	requireNoRecordedLogEntry(t, logger, "ERROR", "cluster.controller", "cluster.controller.rpc.failed")
}

func TestControllerClientRuntimeReportFallsThroughRedirectToCurrentLeader(t *testing.T) {
	staleLeader := transport.NewServer()
	staleLeaderMux := transport.NewRPCMux()
	staleLeaderMux.Handle(rpcServiceController, func(_ context.Context, body []byte) ([]byte, error) {
		req, err := decodeControllerRequest(body)
		require.NoError(t, err)
		require.Equal(t, controllerRPCRuntimeReport, req.Kind)
		return encodeControllerResponse(req.Kind, controllerRPCResponse{
			NotLeader: true,
			LeaderID:  2,
		})
	})
	staleLeader.HandleRPCMux(staleLeaderMux)
	require.NoError(t, staleLeader.Start("127.0.0.1:0"))
	t.Cleanup(staleLeader.Stop)

	leader := transport.NewServer()
	leaderMux := transport.NewRPCMux()
	var seen int32
	leaderMux.Handle(rpcServiceController, func(_ context.Context, body []byte) ([]byte, error) {
		req, err := decodeControllerRequest(body)
		require.NoError(t, err)
		require.Equal(t, controllerRPCRuntimeReport, req.Kind)
		require.NotNil(t, req.RuntimeReport)
		atomic.AddInt32(&seen, 1)
		return encodeControllerResponse(req.Kind, controllerRPCResponse{})
	})
	leader.HandleRPCMux(leaderMux)
	require.NoError(t, leader.Start("127.0.0.1:0"))
	t.Cleanup(leader.Stop)

	discovery := &controllerClientTestDiscovery{
		addrs: map[uint64]string{
			1: staleLeader.Listener().Addr().String(),
			2: leader.Listener().Addr().String(),
		},
	}
	pool := transport.NewPool(discovery, 1, 50*time.Millisecond)
	t.Cleanup(pool.Close)

	client := transport.NewClient(pool)
	t.Cleanup(client.Stop)

	cluster := &Cluster{
		cfg: Config{NodeID: 3},
		transportResources: transportResources{
			fwdClient: client,
		},
	}
	controllerClient := newControllerClient(cluster, []NodeConfig{{NodeID: 1}, {NodeID: 2}}, nil)
	controllerClient.setLeader(1)

	err := controllerClient.ReportRuntimeObservation(context.Background(), runtimeObservationReport{
		NodeID:     3,
		ObservedAt: time.Unix(1710005555, 0),
		FullSync:   true,
		Views: []controllermeta.SlotRuntimeView{{
			SlotID:       1,
			CurrentPeers: []uint64{1, 2, 3},
			LeaderID:     2,
			LastReportAt: time.Unix(1710005555, 0),
		}},
	})
	require.NoError(t, err)
	require.Equal(t, multiraft.NodeID(2), controllerClient.cachedLeader())
	require.EqualValues(t, 1, atomic.LoadInt32(&seen))
}

func TestControllerClientFetchObservationDeltaUsesLocalFastPathWithoutSelfRPC(t *testing.T) {
	cluster, host, _ := newTestLocalControllerCluster(t, true)
	cluster.fwdClient = nil

	host.syncState.reset()
	host.syncState.replaceMetadataSnapshot(controllerMetadataSnapshot{
		Assignments: []controllermeta.SlotAssignment{testObservationAssignment(1, 1)},
		Tasks:       []controllermeta.ReconcileTask{testObservationTask(1, 1)},
		Nodes:       []controllermeta.ClusterNode{testObservationNode(1, controllermeta.NodeStatusAlive)},
	})

	host.warmupMu.RLock()
	leaderGeneration := host.warmupGeneration
	host.warmupMu.RUnlock()

	controllerClient := newControllerClient(cluster, []NodeConfig{{NodeID: cluster.cfg.NodeID}}, nil)

	resp, err := controllerClient.FetchObservationDelta(context.Background(), observationDeltaRequest{
		LeaderID:         uint64(cluster.cfg.NodeID),
		LeaderGeneration: leaderGeneration,
		ForceFullSync:    true,
	})
	require.NoError(t, err)
	require.True(t, resp.FullSync)
	require.Len(t, resp.Assignments, 1)
	require.Equal(t, uint64(cluster.cfg.NodeID), resp.LeaderID)
}

func TestControllerClientListNodesUpdatesDynamicDiscovery(t *testing.T) {
	leader := transport.NewServer()
	leaderMux := transport.NewRPCMux()
	leaderMux.Handle(rpcServiceController, func(_ context.Context, body []byte) ([]byte, error) {
		req, err := decodeControllerRequest(body)
		require.NoError(t, err)
		require.Equal(t, controllerRPCListNodes, req.Kind)
		return encodeControllerResponse(req.Kind, controllerRPCResponse{
			Nodes: []controllermeta.ClusterNode{
				{
					NodeID:         1,
					Addr:           "127.0.0.1:7101",
					Status:         controllermeta.NodeStatusAlive,
					CapacityWeight: 1,
				},
				{
					NodeID:         2,
					Addr:           "127.0.0.1:7102",
					Status:         controllermeta.NodeStatusAlive,
					CapacityWeight: 1,
				},
			},
		})
	})
	leader.HandleRPCMux(leaderMux)
	require.NoError(t, leader.Start("127.0.0.1:0"))
	t.Cleanup(leader.Stop)

	discovery := NewDynamicDiscovery(nil, []NodeConfig{{NodeID: 1, Addr: leader.Listener().Addr().String()}})
	defer discovery.Stop()
	var events []uint64
	discovery.OnAddressChange(func(nodeID uint64, _, _ string) {
		events = append(events, nodeID)
	})
	pool := transport.NewPool(discovery, 1, 50*time.Millisecond)
	t.Cleanup(pool.Close)

	client := transport.NewClient(pool)
	t.Cleanup(client.Stop)

	cluster := &Cluster{
		cfg: Config{NodeID: 3},
		transportResources: transportResources{
			fwdClient: client,
			discovery: discovery,
		},
	}
	controllerClient := newControllerClient(cluster, []NodeConfig{{NodeID: 1}}, nil)

	nodes, err := controllerClient.ListNodes(context.Background())
	require.NoError(t, err)
	require.Len(t, nodes, 2)
	addr, err := discovery.Resolve(1)
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1:7101", addr)
	addr, err = discovery.Resolve(2)
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1:7102", addr)
	require.ElementsMatch(t, []uint64{1, 2}, events)
}

func TestControllerClientRuntimeReportRedirectMarksReporterForFullSync(t *testing.T) {
	staleLeader := transport.NewServer()
	staleLeaderMux := transport.NewRPCMux()
	staleLeaderMux.Handle(rpcServiceController, func(_ context.Context, body []byte) ([]byte, error) {
		req, err := decodeControllerRequest(body)
		require.NoError(t, err)
		require.Equal(t, controllerRPCRuntimeReport, req.Kind)
		return encodeControllerResponse(req.Kind, controllerRPCResponse{
			NotLeader: true,
			LeaderID:  2,
		})
	})
	staleLeader.HandleRPCMux(staleLeaderMux)
	require.NoError(t, staleLeader.Start("127.0.0.1:0"))
	t.Cleanup(staleLeader.Stop)

	leader := transport.NewServer()
	leaderMux := transport.NewRPCMux()
	leaderMux.Handle(rpcServiceController, func(_ context.Context, body []byte) ([]byte, error) {
		req, err := decodeControllerRequest(body)
		require.NoError(t, err)
		require.Equal(t, controllerRPCRuntimeReport, req.Kind)
		return encodeControllerResponse(req.Kind, controllerRPCResponse{})
	})
	leader.HandleRPCMux(leaderMux)
	require.NoError(t, leader.Start("127.0.0.1:0"))
	t.Cleanup(leader.Stop)

	discovery := &controllerClientTestDiscovery{
		addrs: map[uint64]string{
			1: staleLeader.Listener().Addr().String(),
			2: leader.Listener().Addr().String(),
		},
	}
	pool := transport.NewPool(discovery, 1, 50*time.Millisecond)
	t.Cleanup(pool.Close)

	client := transport.NewClient(pool)
	t.Cleanup(client.Stop)

	cluster := &Cluster{
		cfg: Config{NodeID: 3},
		transportResources: transportResources{
			fwdClient: client,
		},
	}
	controllerClient := newControllerClient(cluster, []NodeConfig{{NodeID: 1}, {NodeID: 2}}, nil)
	controllerClient.setLeader(1)

	fullSyncMarks := 0
	controllerClient.onLeaderChange = func(multiraft.NodeID) {
		fullSyncMarks++
	}

	err := controllerClient.ReportRuntimeObservation(context.Background(), runtimeObservationReport{
		NodeID:     3,
		ObservedAt: time.Unix(1710006666, 0),
		FullSync:   false,
	})
	require.NoError(t, err)
	require.Equal(t, multiraft.NodeID(2), controllerClient.cachedLeader())
	require.Equal(t, 1, fullSyncMarks)
}

func TestControllerClientJoinClusterUpdatesDynamicDiscovery(t *testing.T) {
	leader := transport.NewServer()
	leaderMux := transport.NewRPCMux()
	leaderMux.Handle(rpcServiceController, func(_ context.Context, body []byte) ([]byte, error) {
		req, err := decodeControllerRequest(body)
		require.NoError(t, err)
		require.Equal(t, controllerRPCJoinCluster, req.Kind)
		require.NotNil(t, req.Join)
		return encodeControllerResponse(req.Kind, controllerRPCResponse{
			Join: &joinClusterResponse{
				Nodes: []controllermeta.ClusterNode{
					{
						NodeID:         1,
						Addr:           "127.0.0.1:7101",
						Status:         controllermeta.NodeStatusAlive,
						CapacityWeight: 1,
					},
					{
						NodeID:         req.Join.NodeID,
						Addr:           req.Join.Addr,
						Status:         controllermeta.NodeStatusAlive,
						CapacityWeight: 1,
					},
				},
			},
		})
	})
	leader.HandleRPCMux(leaderMux)
	require.NoError(t, leader.Start("127.0.0.1:0"))
	t.Cleanup(leader.Stop)

	discovery := NewDynamicDiscovery(nil, []NodeConfig{{NodeID: 1, Addr: leader.Listener().Addr().String()}})
	defer discovery.Stop()
	pool := transport.NewPool(discovery, 1, 50*time.Millisecond)
	t.Cleanup(pool.Close)

	client := transport.NewClient(pool)
	t.Cleanup(client.Stop)

	cluster := &Cluster{
		cfg: Config{NodeID: 3},
		transportResources: transportResources{
			fwdClient: client,
			discovery: discovery,
		},
	}
	controllerClient := newControllerClient(cluster, []NodeConfig{{NodeID: 1}}, nil)

	resp, err := controllerClient.JoinCluster(context.Background(), joinClusterRequest{
		NodeID:  3,
		Addr:    "127.0.0.1:7303",
		Token:   "join-secret",
		Version: supportedJoinProtocolVersion,
	})
	require.NoError(t, err)
	require.Len(t, resp.Nodes, 2)
	addr, err := discovery.Resolve(3)
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1:7303", addr)
}

func TestControllerClientJoinClusterValidatesMembershipBeforeApply(t *testing.T) {
	leader := transport.NewServer()
	leaderMux := transport.NewRPCMux()
	leaderMux.Handle(rpcServiceController, func(_ context.Context, body []byte) ([]byte, error) {
		req, err := decodeControllerRequest(body)
		require.NoError(t, err)
		require.Equal(t, controllerRPCJoinCluster, req.Kind)
		require.NotNil(t, req.Join)
		return encodeControllerResponse(req.Kind, controllerRPCResponse{
			Join: &joinClusterResponse{
				Nodes: []controllermeta.ClusterNode{{
					NodeID:         9,
					Addr:           "127.0.0.1:7909",
					Status:         controllermeta.NodeStatusAlive,
					CapacityWeight: 1,
				}},
			},
		})
	})
	leader.HandleRPCMux(leaderMux)
	require.NoError(t, leader.Start("127.0.0.1:0"))
	t.Cleanup(leader.Stop)

	discovery := NewDynamicDiscovery(nil, []NodeConfig{{NodeID: 1, Addr: leader.Listener().Addr().String()}})
	defer discovery.Stop()
	pool := transport.NewPool(discovery, 1, 50*time.Millisecond)
	t.Cleanup(pool.Close)

	client := transport.NewClient(pool)
	t.Cleanup(client.Stop)

	cluster := &Cluster{
		cfg: Config{NodeID: 3},
		transportResources: transportResources{
			fwdClient: client,
			discovery: discovery,
		},
	}
	controllerClient := newControllerClient(cluster, []NodeConfig{{NodeID: 1}}, nil)

	_, err := controllerClient.JoinCluster(context.Background(), joinClusterRequest{
		NodeID:  3,
		Addr:    "127.0.0.1:7303",
		Token:   "join-secret",
		Version: supportedJoinProtocolVersion,
	})
	require.ErrorIs(t, err, ErrInvalidConfig)
	_, err = discovery.Resolve(9)
	require.ErrorIs(t, err, transport.ErrNodeNotFound)
}

func TestControllerClientJoinClusterReturnsTypedJoinError(t *testing.T) {
	leader := transport.NewServer()
	leaderMux := transport.NewRPCMux()
	leaderMux.Handle(rpcServiceController, func(_ context.Context, body []byte) ([]byte, error) {
		req, err := decodeControllerRequest(body)
		require.NoError(t, err)
		require.Equal(t, controllerRPCJoinCluster, req.Kind)
		require.NotNil(t, req.Join)
		return encodeControllerResponse(req.Kind, controllerRPCResponse{
			Join: &joinClusterResponse{
				JoinErrorCode:    joinErrorInvalidToken,
				JoinErrorMessage: "invalid join token",
			},
		})
	})
	leader.HandleRPCMux(leaderMux)
	require.NoError(t, leader.Start("127.0.0.1:0"))
	t.Cleanup(leader.Stop)

	discovery := &controllerClientTestDiscovery{
		addrs: map[uint64]string{1: leader.Listener().Addr().String()},
	}
	pool := transport.NewPool(discovery, 1, 50*time.Millisecond)
	t.Cleanup(pool.Close)

	client := transport.NewClient(pool)
	t.Cleanup(client.Stop)

	cluster := &Cluster{
		cfg: Config{NodeID: 2},
		transportResources: transportResources{
			fwdClient: client,
		},
	}
	controllerClient := newControllerClient(cluster, []NodeConfig{{NodeID: 1}}, nil)

	_, err := controllerClient.JoinCluster(context.Background(), joinClusterRequest{
		NodeID:  2,
		Addr:    "127.0.0.1:2222",
		Token:   "wrong",
		Version: supportedJoinProtocolVersion,
	})
	require.Error(t, err)
	var joinErr *joinClusterError
	require.True(t, errors.As(err, &joinErr))
	require.Equal(t, joinErrorInvalidToken, joinErr.Code)
	require.False(t, joinErr.Retryable())
}

func TestControllerClientUpdatePeersReplacesRetryTargets(t *testing.T) {
	first := transport.NewServer()
	firstMux := transport.NewRPCMux()
	firstMux.Handle(rpcServiceController, func(_ context.Context, body []byte) ([]byte, error) {
		req, err := decodeControllerRequest(body)
		require.NoError(t, err)
		require.Equal(t, controllerRPCJoinCluster, req.Kind)
		return encodeControllerResponse(req.Kind, controllerRPCResponse{
			NotLeader: true,
			LeaderID:  2,
		})
	})
	first.HandleRPCMux(firstMux)
	require.NoError(t, first.Start("127.0.0.1:0"))
	t.Cleanup(first.Stop)

	second := transport.NewServer()
	secondMux := transport.NewRPCMux()
	var seen int32
	secondMux.Handle(rpcServiceController, func(_ context.Context, body []byte) ([]byte, error) {
		req, err := decodeControllerRequest(body)
		require.NoError(t, err)
		require.Equal(t, controllerRPCJoinCluster, req.Kind)
		atomic.AddInt32(&seen, 1)
		return encodeControllerResponse(req.Kind, controllerRPCResponse{
			Join: &joinClusterResponse{
				Nodes: []controllermeta.ClusterNode{
					{
						NodeID:         2,
						Addr:           "127.0.0.1:2222",
						Status:         controllermeta.NodeStatusAlive,
						CapacityWeight: 1,
					},
					{
						NodeID:         req.Join.NodeID,
						Addr:           req.Join.Addr,
						Status:         controllermeta.NodeStatusAlive,
						CapacityWeight: 1,
					},
				},
			},
		})
	})
	second.HandleRPCMux(secondMux)
	require.NoError(t, second.Start("127.0.0.1:0"))
	t.Cleanup(second.Stop)

	discovery := &controllerClientTestDiscovery{
		addrs: map[uint64]string{
			1: first.Listener().Addr().String(),
			2: second.Listener().Addr().String(),
		},
	}
	pool := transport.NewPool(discovery, 1, 50*time.Millisecond)
	t.Cleanup(pool.Close)

	client := transport.NewClient(pool)
	t.Cleanup(client.Stop)

	cluster := &Cluster{
		cfg: Config{NodeID: 3},
		transportResources: transportResources{
			fwdClient: client,
		},
	}
	controllerClient := newControllerClient(cluster, []NodeConfig{{NodeID: 1}}, nil)
	controllerClient.UpdatePeers([]multiraft.NodeID{2})

	resp, err := controllerClient.JoinCluster(context.Background(), joinClusterRequest{
		NodeID:  3,
		Addr:    "127.0.0.1:3333",
		Token:   "join-secret",
		Version: supportedJoinProtocolVersion,
	})
	require.NoError(t, err)
	require.Len(t, resp.Nodes, 2)
	require.EqualValues(t, 1, atomic.LoadInt32(&seen))
}

func TestControllerClientUsesDedicatedControllerRPCClient(t *testing.T) {
	leader := transport.NewServer()
	leaderMux := transport.NewRPCMux()
	leaderMux.Handle(rpcServiceController, func(_ context.Context, body []byte) ([]byte, error) {
		req, err := decodeControllerRequest(body)
		require.NoError(t, err)
		require.Equal(t, controllerRPCListNodes, req.Kind)
		return encodeControllerResponse(req.Kind, controllerRPCResponse{
			Nodes: []controllermeta.ClusterNode{{
				NodeID:         1,
				Addr:           "127.0.0.1:7101",
				Status:         controllermeta.NodeStatusAlive,
				CapacityWeight: 1,
			}},
		})
	})
	leader.HandleRPCMux(leaderMux)
	require.NoError(t, leader.Start("127.0.0.1:0"))
	t.Cleanup(leader.Stop)

	fwdPool := transport.NewPool(&controllerClientTestDiscovery{addrs: map[uint64]string{}}, 1, 50*time.Millisecond)
	t.Cleanup(fwdPool.Close)
	controllerPool := transport.NewPool(&controllerClientTestDiscovery{
		addrs: map[uint64]string{1: leader.Listener().Addr().String()},
	}, 1, 50*time.Millisecond)
	t.Cleanup(controllerPool.Close)

	fwdClient := transport.NewClient(fwdPool)
	t.Cleanup(fwdClient.Stop)
	controllerRPCClient := transport.NewClient(controllerPool)
	t.Cleanup(controllerRPCClient.Stop)

	cluster := &Cluster{
		cfg: Config{NodeID: 3},
		transportResources: transportResources{
			fwdClient:           fwdClient,
			controllerRPCClient: controllerRPCClient,
		},
	}
	controllerClient := newControllerClient(cluster, []NodeConfig{{NodeID: 1}}, nil)

	nodes, err := controllerClient.ListNodes(context.Background())
	require.NoError(t, err)
	require.Len(t, nodes, 1)
	require.EqualValues(t, 1, nodes[0].NodeID)
}

type recordedLogEntry struct {
	level  string
	module string
	msg    string
	fields []wklog.Field
}

func (e recordedLogEntry) field(key string) (wklog.Field, bool) {
	for _, field := range e.fields {
		if field.Key == key {
			return field, true
		}
	}
	return wklog.Field{}, false
}

type recordingLoggerSink struct {
	mu      sync.Mutex
	entries []recordedLogEntry
}

type recordingLogger struct {
	module string
	base   []wklog.Field
	sink   *recordingLoggerSink
}

func newRecordingLogger(module string) *recordingLogger {
	return &recordingLogger{module: module, sink: &recordingLoggerSink{}}
}

func (r *recordingLogger) Debug(msg string, fields ...wklog.Field) { r.log("DEBUG", msg, fields...) }
func (r *recordingLogger) Info(msg string, fields ...wklog.Field)  { r.log("INFO", msg, fields...) }
func (r *recordingLogger) Warn(msg string, fields ...wklog.Field)  { r.log("WARN", msg, fields...) }
func (r *recordingLogger) Error(msg string, fields ...wklog.Field) { r.log("ERROR", msg, fields...) }
func (r *recordingLogger) Fatal(msg string, fields ...wklog.Field) { r.log("FATAL", msg, fields...) }

func (r *recordingLogger) Named(name string) wklog.Logger {
	if name == "" {
		return r
	}
	module := name
	if r.module != "" {
		module = r.module + "." + name
	}
	return &recordingLogger{module: module, base: append([]wklog.Field(nil), r.base...), sink: r.sink}
}

func (r *recordingLogger) With(fields ...wklog.Field) wklog.Logger {
	merged := append(append([]wklog.Field(nil), r.base...), fields...)
	return &recordingLogger{module: r.module, base: merged, sink: r.sink}
}

func (r *recordingLogger) Sync() error { return nil }

func (r *recordingLogger) log(level, msg string, fields ...wklog.Field) {
	if r == nil || r.sink == nil {
		return
	}
	entry := recordedLogEntry{
		level:  level,
		module: r.module,
		msg:    msg,
		fields: append(append([]wklog.Field(nil), r.base...), fields...),
	}
	r.sink.mu.Lock()
	defer r.sink.mu.Unlock()
	r.sink.entries = append(r.sink.entries, entry)
}

func (r *recordingLogger) entries() []recordedLogEntry {
	if r == nil || r.sink == nil {
		return nil
	}
	r.sink.mu.Lock()
	defer r.sink.mu.Unlock()
	out := make([]recordedLogEntry, len(r.sink.entries))
	copy(out, r.sink.entries)
	return out
}

func requireRecordedLogEntry(t *testing.T, logger *recordingLogger, level, module, event string) recordedLogEntry {
	t.Helper()
	for _, entry := range logger.entries() {
		if entry.level != level || entry.module != module {
			continue
		}
		field, ok := entry.field("event")
		if ok && field.Value == event {
			return entry
		}
	}
	t.Fatalf("log entry not found: level=%s module=%s event=%s entries=%#v", level, module, event, logger.entries())
	return recordedLogEntry{}
}

func requireNoRecordedLogEntry(t *testing.T, logger *recordingLogger, level, module, event string) {
	t.Helper()
	for _, entry := range logger.entries() {
		if entry.level != level || entry.module != module {
			continue
		}
		field, ok := entry.field("event")
		if ok && field.Value == event {
			t.Fatalf("unexpected log entry found: level=%s module=%s event=%s entry=%#v", level, module, event, entry)
		}
	}
}

func requireRecordedField[T any](t *testing.T, entry recordedLogEntry, key string) T {
	t.Helper()
	field, ok := entry.field(key)
	require.True(t, ok, "field %q not found in entry %#v", key, entry)
	value, ok := field.Value.(T)
	require.True(t, ok, "field %q has type %T, want %T", key, field.Value, *new(T))
	return value
}
