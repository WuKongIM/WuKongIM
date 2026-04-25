package cluster

import (
	"context"
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

func requireRecordedField[T any](t *testing.T, entry recordedLogEntry, key string) T {
	t.Helper()
	field, ok := entry.field(key)
	require.True(t, ok, "field %q not found in entry %#v", key, entry)
	value, ok := field.Value.(T)
	require.True(t, ok, "field %q has type %T, want %T", key, field.Value, *new(T))
	return value
}
