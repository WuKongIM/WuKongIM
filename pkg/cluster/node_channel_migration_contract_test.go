package cluster

import (
	"context"
	"errors"
	"reflect"
	"testing"

	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/propose"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestChannelMigrationMetadataReadsFollowActualLocalSlotLeadership(t *testing.T) {
	node, db := newLocalMetadataScanNode(t)
	node.defaultSlotProposer = fixedUnitChannelMigrationSlotRuntime{localLeader: true}
	channelID := channelruntime.ChannelID{ID: keyForNodeHashSlot(t, 4, 0), Type: 2}
	meta := metadb.NormalizeChannelRuntimeMeta(metadb.ChannelRuntimeMeta{
		ChannelID: channelID.ID, ChannelType: int64(channelID.Type),
		ChannelEpoch: 10, LeaderEpoch: 20, RouteGeneration: 30,
		Leader: 1, Replicas: []uint64{1, 2}, ISR: []uint64{1, 2}, MinISR: 2,
		Status: uint8(channelruntime.StatusActive),
	})
	if err := db.ForHashSlot(0).UpsertChannelRuntimeMeta(context.Background(), meta); err != nil {
		t.Fatalf("UpsertChannelRuntimeMeta() error = %v", err)
	}
	task := channelMigrationContractTask(channelID, "migration-task-1")
	if err := db.ForHashSlot(0).CreateChannelMigrationTask(context.Background(), task); err != nil {
		t.Fatalf("CreateChannelMigrationTask() error = %v", err)
	}

	gotMeta, err := node.readChannelMigrationRuntimeMeta(context.Background(), 0, channelID.ID, int64(channelID.Type))
	if err != nil {
		t.Fatalf("readChannelMigrationRuntimeMeta() error = %v", err)
	}
	if gotMeta.ChannelID != meta.ChannelID || gotMeta.ChannelEpoch != meta.ChannelEpoch || gotMeta.LeaderEpoch != meta.LeaderEpoch {
		t.Fatalf("runtime meta = %#v, want exact stored generation", gotMeta)
	}
	active, ok, err := node.getActiveChannelMigrationTask(context.Background(), 0, channelID.ID, int64(channelID.Type))
	if err != nil || !ok || active.TaskID != task.TaskID {
		t.Fatalf("getActiveChannelMigrationTask() task=%#v ok=%t err=%v", active, ok, err)
	}
	byID, ok, err := node.getChannelMigrationTask(context.Background(), 0, channelID.ID, int64(channelID.Type), task.TaskID)
	if err != nil || !ok || byID.TaskID != task.TaskID {
		t.Fatalf("getChannelMigrationTask() task=%#v ok=%t err=%v", byID, ok, err)
	}
	if _, ok, err := node.getChannelMigrationTask(context.Background(), 0, channelID.ID, int64(channelID.Type), "missing"); err != nil || ok {
		t.Fatalf("getChannelMigrationTask(missing) ok=%t err=%v, want clean absence", ok, err)
	}
	activeTasks, err := node.listActiveChannelMigrationTasks(context.Background(), 0, 8)
	if err != nil || len(activeTasks) != 1 || activeTasks[0].TaskID != task.TaskID {
		t.Fatalf("listActiveChannelMigrationTasks() = %#v err=%v", activeTasks, err)
	}
	if empty, err := node.listActiveChannelMigrationTasks(context.Background(), 0, 0); err != nil || len(empty) != 0 {
		t.Fatalf("listActiveChannelMigrationTasks(limit=0) = %#v err=%v", empty, err)
	}

	// A stale route may point away while the local Slot runtime has already
	// become leader. Metadata authority follows the actual local Raft role.
	node.router.UpdateSlotLeaders([]routing.SlotStatus{{SlotID: 1, Leader: 2, LeaderTerm: 11}})
	gotMeta, err = node.readChannelMigrationRuntimeMeta(context.Background(), 0, channelID.ID, int64(channelID.Type))
	if err != nil || gotMeta.ChannelEpoch != meta.ChannelEpoch {
		t.Fatalf("read with stale route = %#v err=%v, want actual local leader data", gotMeta, err)
	}

	node.defaultSlotProposer = fixedUnitChannelMigrationSlotRuntime{localLeader: false}
	node.router.UpdateSlotLeaders([]routing.SlotStatus{{SlotID: 1, Leader: 1, LeaderTerm: 12}})
	if _, err := node.readChannelMigrationRuntimeMeta(context.Background(), 0, channelID.ID, int64(channelID.Type)); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("read without actual local leadership error = %v, want ErrNotLeader", err)
	}

	otherHashSlotChannel := keyForNodeHashSlot(t, 4, 3)
	if _, err := node.channelMigrationRoute(context.Background(), 0, otherHashSlotChannel); !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("channelMigrationRoute(mismatched hash slot) error = %v, want ErrInvalidArgument", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := node.channelMigrationRoute(canceled, 0, channelID.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("channelMigrationRoute(canceled) error = %v, want context.Canceled", err)
	}
}

func TestChannelMigrationMetadataRPCHandlerPreservesOptionalResults(t *testing.T) {
	node, db := newLocalMetadataScanNode(t)
	node.defaultSlotProposer = fixedUnitChannelMigrationSlotRuntime{localLeader: true}
	channelID := channelruntime.ChannelID{ID: keyForNodeHashSlot(t, 4, 0), Type: 2}
	meta := metadb.NormalizeChannelRuntimeMeta(metadb.ChannelRuntimeMeta{
		ChannelID: channelID.ID, ChannelType: int64(channelID.Type),
		ChannelEpoch: 4, LeaderEpoch: 5, Leader: 1,
		Replicas: []uint64{1}, ISR: []uint64{1}, MinISR: 1,
		Status: uint8(channelruntime.StatusActive),
	})
	if err := db.ForHashSlot(0).UpsertChannelRuntimeMeta(context.Background(), meta); err != nil {
		t.Fatal(err)
	}
	task := channelMigrationContractTask(channelID, "migration-rpc-task")
	if err := db.ForHashSlot(0).CreateChannelMigrationTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	handler := channelMigrationMetaHandler{node: node}

	getRuntime := handleChannelMigrationContractRPC(t, handler, channelMigrationMetaRPCRequest{
		Op: channelMigrationMetaOpGetRuntime, HashSlot: 0,
		ChannelID: channelID.ID, ChannelType: int64(channelID.Type),
	})
	if getRuntime.RuntimeMeta == nil || getRuntime.RuntimeMeta.ChannelEpoch != meta.ChannelEpoch {
		t.Fatalf("get-runtime response = %#v, want stored runtime meta", getRuntime)
	}
	missingRuntime := handleChannelMigrationContractRPC(t, handler, channelMigrationMetaRPCRequest{
		Op: channelMigrationMetaOpGetRuntime, HashSlot: 0,
		ChannelID: distinctChannelIDsForHashSlot(t, 4, 0, 2)[1], ChannelType: int64(channelID.Type),
	})
	if missingRuntime.RuntimeMeta != nil {
		t.Fatalf("missing runtime response = %#v, want omitted runtime_meta", missingRuntime)
	}

	getActive := handleChannelMigrationContractRPC(t, handler, channelMigrationMetaRPCRequest{
		Op: channelMigrationMetaOpGetActive, HashSlot: 0,
		ChannelID: channelID.ID, ChannelType: int64(channelID.Type),
	})
	if getActive.Task == nil || getActive.Task.TaskID != task.TaskID {
		t.Fatalf("get-active response = %#v, want active task", getActive)
	}
	getTask := handleChannelMigrationContractRPC(t, handler, channelMigrationMetaRPCRequest{
		Op: channelMigrationMetaOpGetTask, HashSlot: 0,
		ChannelID: channelID.ID, ChannelType: int64(channelID.Type), TaskID: task.TaskID,
	})
	if getTask.Task == nil || getTask.Task.TaskID != task.TaskID {
		t.Fatalf("get-task response = %#v, want exact task", getTask)
	}
	missingTask := handleChannelMigrationContractRPC(t, handler, channelMigrationMetaRPCRequest{
		Op: channelMigrationMetaOpGetTask, HashSlot: 0,
		ChannelID: channelID.ID, ChannelType: int64(channelID.Type), TaskID: "missing",
	})
	if missingTask.Task != nil {
		t.Fatalf("missing task response = %#v, want omitted task", missingTask)
	}
	list := handleChannelMigrationContractRPC(t, handler, channelMigrationMetaRPCRequest{
		Op: channelMigrationMetaOpListActive, HashSlot: 0, Limit: 8,
	})
	if len(list.Tasks) != 1 || list.Tasks[0].TaskID != task.TaskID {
		t.Fatalf("list-active response = %#v, want exact active task", list)
	}

	unknownBody, err := encodeChannelMigrationMetaRPCRequest(channelMigrationMetaRPCRequest{Op: "unknown"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := handler.HandleRPC(context.Background(), unknownBody); !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("HandleRPC(unknown op) error = %v, want ErrInvalidArgument", err)
	}
	if _, err := (channelMigrationMetaHandler{}).HandleRPC(context.Background(), unknownBody); !errors.Is(err, ErrNotStarted) {
		t.Fatalf("HandleRPC(nil node) error = %v, want ErrNotStarted", err)
	}
	if _, err := decodeChannelMigrationMetaRPCRequest([]byte(`{"version":2}`)); !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("decode request unknown version error = %v, want ErrInvalidArgument", err)
	}
	if _, err := decodeChannelMigrationMetaRPCResponse([]byte(`{"version":0}`)); !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("decode response unknown version error = %v, want ErrInvalidArgument", err)
	}
}

func TestChannelMigrationRuntimeRPCOperationsUseHostedChannelService(t *testing.T) {
	node, _ := newLocalMetadataScanNode(t)
	channelID := channelruntime.ChannelID{ID: keyForNodeHashSlot(t, 4, 0), Type: 2}
	service := &migrationRuntimeChannelService{
		probe: channelruntime.RuntimeProbeResult{Channels: []channelruntime.RuntimeProbeChannel{{
			ChannelID: channelID, ChannelEpoch: 7, LeaderEpoch: 3,
			Role: channelruntime.RoleLeader, Status: channelruntime.StatusActive, LEO: 12, HW: 11,
		}}},
		drain: channelruntime.DrainChannelResult{Drained: true, LEO: 12, HW: 12},
	}
	node.channels = service
	handler := channelMigrationMetaHandler{node: node}

	probe := handleChannelMigrationContractRPC(t, handler, channelMigrationMetaRPCRequest{
		Op: channelMigrationMetaOpRuntimeProbe, ChannelID: channelID.ID, ChannelType: int64(channelID.Type),
	})
	if probe.RuntimeProbe == nil || probe.RuntimeProbe.ChannelID != channelID || probe.RuntimeProbe.HW != 11 {
		t.Fatalf("runtime probe response = %#v", probe)
	}
	drainRequest := channelruntime.DrainChannelRequest{ChannelID: channelID, LeaderEpoch: 3, FenceVersion: 9}
	drain := handleChannelMigrationContractRPC(t, handler, channelMigrationMetaRPCRequest{
		Op: channelMigrationMetaOpRuntimeDrain, DrainRequest: &drainRequest,
	})
	if drain.DrainResult == nil || !drain.DrainResult.Drained || service.drainCalls != 1 || service.lastDrain != drainRequest {
		t.Fatalf("runtime drain response=%#v calls=%d request=%#v", drain, service.drainCalls, service.lastDrain)
	}
	meta := metadb.ChannelRuntimeMeta{
		ChannelID: channelID.ID, ChannelType: int64(channelID.Type),
		ChannelEpoch: 7, LeaderEpoch: 3, Leader: 1,
		Replicas: []uint64{1}, ISR: []uint64{1}, MinISR: 1,
		Status: uint8(channelruntime.StatusActive),
	}
	apply := handleChannelMigrationContractRPC(t, handler, channelMigrationMetaRPCRequest{
		Op: channelMigrationMetaOpRuntimeApply, RuntimeMeta: &meta,
	})
	if apply.Version != channelMigrationMetaRPCVersion || apply.RuntimeMeta != nil || apply.Task != nil || len(apply.Tasks) != 0 || apply.RuntimeProbe != nil || apply.DrainResult != nil {
		t.Fatalf("runtime apply response = %#v, want empty versioned response", apply)
	}
	if len(service.applied) != 1 || service.applied[0].ID != channelID || service.applied[0].Epoch != 7 || service.applied[0].LeaderEpoch != 3 {
		t.Fatalf("applied metas = %#v, want projected authoritative meta", service.applied)
	}

	for _, req := range []channelMigrationMetaRPCRequest{
		{Op: channelMigrationMetaOpRuntimeDrain},
		{Op: channelMigrationMetaOpRuntimeApply},
	} {
		body, err := encodeChannelMigrationMetaRPCRequest(req)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := handler.HandleRPC(context.Background(), body); !errors.Is(err, metadb.ErrInvalidArgument) {
			t.Fatalf("HandleRPC(%s missing payload) error = %v, want ErrInvalidArgument", req.Op, err)
		}
	}

	localProbe, err := node.ProbeChannel(context.Background(), 1, channelID.ID, channelID.Type)
	if err != nil || localProbe.ChannelID != channelID {
		t.Fatalf("ProbeChannel(local) = %#v err=%v", localProbe, err)
	}
	localDrain, err := node.DrainChannel(context.Background(), 1, drainRequest)
	if err != nil || !localDrain.Drained {
		t.Fatalf("DrainChannel(local) = %#v err=%v", localDrain, err)
	}
	if err := node.ApplyChannelMeta(context.Background(), 1, meta); err != nil {
		t.Fatalf("ApplyChannelMeta(local) error = %v", err)
	}
	if _, err := node.ProbeChannel(context.Background(), 0, channelID.ID, channelID.Type); !errors.Is(err, ErrNotStarted) {
		t.Fatalf("ProbeChannel(node=0) error = %v, want ErrNotStarted", err)
	}
}

type fixedUnitChannelMigrationSlotRuntime struct {
	localLeader bool
}

func (r fixedUnitChannelMigrationSlotRuntime) IsLocalLeader(uint32) bool {
	return r.localLeader
}

func (fixedUnitChannelMigrationSlotRuntime) Propose(context.Context, uint32, []byte) error {
	return nil
}

var _ propose.SlotRuntime = fixedUnitChannelMigrationSlotRuntime{}

type migrationRuntimeChannelService struct {
	noopChannelService
	probe      channelruntime.RuntimeProbeResult
	drain      channelruntime.DrainChannelResult
	drainCalls int
	lastDrain  channelruntime.DrainChannelRequest
	applied    []channelruntime.Meta
}

func (s *migrationRuntimeChannelService) RuntimeProbe(context.Context, channelruntime.RuntimeSelector) (channelruntime.RuntimeProbeResult, error) {
	return s.probe, nil
}

func (s *migrationRuntimeChannelService) DrainChannel(_ context.Context, req channelruntime.DrainChannelRequest) (channelruntime.DrainChannelResult, error) {
	s.drainCalls++
	s.lastDrain = req
	return s.drain, nil
}

func (s *migrationRuntimeChannelService) ApplyMeta(meta channelruntime.Meta) error {
	s.applied = append(s.applied, meta)
	return nil
}

func channelMigrationContractTask(id channelruntime.ChannelID, taskID string) metadb.ChannelMigrationTask {
	return metadb.ChannelMigrationTask{
		TaskID: taskID, Kind: metadb.ChannelMigrationKindReplicaReplace,
		Status: metadb.ChannelMigrationStatusPending, Phase: metadb.ChannelMigrationPhaseValidate,
		ChannelID: id.ID, ChannelType: int64(id.Type), SourceNode: 1, TargetNode: 2,
		BaseChannelEpoch: 10, BaseLeaderEpoch: 20,
		CreatedAtMS: 1750000000000, UpdatedAtMS: 1750000000000,
	}
}

func handleChannelMigrationContractRPC(t *testing.T, handler channelMigrationMetaHandler, req channelMigrationMetaRPCRequest) channelMigrationMetaRPCResponse {
	t.Helper()
	body, err := encodeChannelMigrationMetaRPCRequest(req)
	if err != nil {
		t.Fatalf("encodeChannelMigrationMetaRPCRequest(%s) error = %v", req.Op, err)
	}
	responseBody, err := handler.HandleRPC(context.Background(), body)
	if err != nil {
		t.Fatalf("HandleRPC(%s) error = %v", req.Op, err)
	}
	response, err := decodeChannelMigrationMetaRPCResponse(responseBody)
	if err != nil {
		t.Fatalf("decodeChannelMigrationMetaRPCResponse(%s) error = %v", req.Op, err)
	}
	return response
}

func TestChannelMigrationRPCCodecRoundTripsDetachedValues(t *testing.T) {
	meta := metadb.ChannelRuntimeMeta{ChannelID: "room", ChannelType: 2, Replicas: []uint64{1, 2}}
	req := channelMigrationMetaRPCRequest{Op: channelMigrationMetaOpRuntimeApply, RuntimeMeta: &meta}
	body, err := encodeChannelMigrationMetaRPCRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeChannelMigrationMetaRPCRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	meta.Replicas[0] = 9
	if decoded.RuntimeMeta == nil || !reflect.DeepEqual(decoded.RuntimeMeta.Replicas, []uint64{1, 2}) {
		t.Fatalf("decoded request aliases source: %#v", decoded)
	}
}
