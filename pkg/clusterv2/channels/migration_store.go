package channels

import (
	"context"
	"fmt"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/routing"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
)

const (
	defaultMigrationOwnerTTL = 30 * time.Second
	defaultMigrationFenceTTL = 30 * time.Second
	maxMigrationChannelType  = int64(^uint8(0))
)

// MigrationCommandProposer submits encoded ChannelV2 migration commands to a physical Slot.
type MigrationCommandProposer interface {
	// ProposeChannelMigrationCommand proposes command to the physical Slot that owns hashSlot.
	ProposeChannelMigrationCommand(ctx context.Context, slotID uint32, hashSlot uint16, command []byte) error
}

// MigrationTaskReader reads migration state from one hash-slot-scoped metadata shard.
type MigrationTaskReader interface {
	// GetChannelRuntimeMeta reads the runtime metadata row from the channel-owned hash slot.
	GetChannelRuntimeMeta(ctx context.Context, hashSlot uint16, channelID string, channelType int64) (metadb.ChannelRuntimeMeta, error)
	// GetActiveChannelMigrationTask reads the active task for one channel from hashSlot.
	GetActiveChannelMigrationTask(ctx context.Context, hashSlot uint16, channelID string, channelType int64) (metadb.ChannelMigrationTask, bool, error)
	// GetChannelMigrationTask reads one task for one channel from hashSlot.
	GetChannelMigrationTask(ctx context.Context, hashSlot uint16, channelID string, channelType int64, taskID string) (metadb.ChannelMigrationTask, bool, error)
	// ListActiveChannelMigrationTasks lists active tasks stored in hashSlot.
	ListActiveChannelMigrationTasks(ctx context.Context, hashSlot uint16, limit int) ([]metadb.ChannelMigrationTask, error)
}

// MigrationStoreConfig wires a Slot-backed migration store facade.
type MigrationStoreConfig struct {
	// LocalNode is the owner node recorded when this store claims tasks.
	LocalNode uint64
	// Router resolves the channel-owned hash slot and physical Slot.
	Router PlacementRouter
	// Proposer submits encoded Slot FSM migration commands.
	Proposer MigrationCommandProposer
	// Reader reads authoritative runtime metadata and migration tasks.
	Reader MigrationTaskReader
	// Now returns wall-clock time for durable task timestamps.
	Now func() time.Time
	// OwnerTTL is the claim lease duration. Zero uses the package default.
	OwnerTTL time.Duration
	// FenceTTL is the write-fence lease duration. Zero uses the package default.
	FenceTTL time.Duration
}

// MigrationStore is a typed facade over Slot-owned ChannelV2 migration rows.
type MigrationStore struct {
	localNode uint64
	router    PlacementRouter
	proposer  MigrationCommandProposer
	reader    MigrationTaskReader
	now       func() time.Time
	ownerTTL  time.Duration
	fenceTTL  time.Duration
}

// CreateLeaderTransferRequest creates a manual leader-transfer migration task.
type CreateLeaderTransferRequest struct {
	// ChannelID identifies the ChannelV2 runtime metadata row.
	ChannelID ch.ChannelID
	// TaskID is the durable migration task identity.
	TaskID string
	// DesiredLeader is the existing replica that should become leader.
	DesiredLeader ch.NodeID
}

// CreateReplicaReplaceRequest creates a manual replica-replacement migration task.
type CreateReplicaReplaceRequest struct {
	// ChannelID identifies the ChannelV2 runtime metadata row.
	ChannelID ch.ChannelID
	// TaskID is the durable migration task identity.
	TaskID string
	// SourceNode is the replica being replaced.
	SourceNode ch.NodeID
	// TargetNode is the new replica candidate.
	TargetNode ch.NodeID
}

// NewMigrationStore creates a Slot-backed ChannelV2 migration facade.
func NewMigrationStore(cfg MigrationStoreConfig) *MigrationStore {
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	ownerTTL := cfg.OwnerTTL
	if ownerTTL <= 0 {
		ownerTTL = defaultMigrationOwnerTTL
	}
	fenceTTL := cfg.FenceTTL
	if fenceTTL <= 0 {
		fenceTTL = defaultMigrationFenceTTL
	}
	return &MigrationStore{
		localNode: cfg.LocalNode,
		router:    cfg.Router,
		proposer:  cfg.Proposer,
		reader:    cfg.Reader,
		now:       now,
		ownerTTL:  ownerTTL,
		fenceTTL:  fenceTTL,
	}
}

// CreateLeaderTransfer creates a runtime-guarded leader-transfer task.
func (s *MigrationStore) CreateLeaderTransfer(ctx context.Context, req CreateLeaderTransferRequest) (metadb.ChannelMigrationTask, error) {
	if req.DesiredLeader == 0 {
		return metadb.ChannelMigrationTask{}, fmt.Errorf("%w: desired leader is required", ch.ErrInvalidConfig)
	}
	route, meta, err := s.readRuntimeMeta(ctx, req.ChannelID)
	if err != nil {
		return metadb.ChannelMigrationTask{}, err
	}
	if err := validateLeaderTransferTarget(meta, uint64(req.DesiredLeader)); err != nil {
		return metadb.ChannelMigrationTask{}, err
	}
	nowMS := s.nowMS()
	task := metadb.ChannelMigrationTask{
		TaskID:           migrationTaskID(req.TaskID, "leader-transfer", req.ChannelID, nowMS),
		Kind:             metadb.ChannelMigrationKindLeaderTransfer,
		Status:           metadb.ChannelMigrationStatusPending,
		Phase:            metadb.ChannelMigrationPhaseValidate,
		ChannelID:        req.ChannelID.ID,
		ChannelType:      int64(req.ChannelID.Type),
		SourceNode:       meta.Leader,
		TargetNode:       uint64(req.DesiredLeader),
		DesiredLeader:    uint64(req.DesiredLeader),
		BaseChannelEpoch: meta.ChannelEpoch,
		BaseLeaderEpoch:  meta.LeaderEpoch,
		CreatedAtMS:      nowMS,
		UpdatedAtMS:      nowMS,
	}
	return task, s.propose(ctx, route, metafsm.EncodeCreateChannelMigrationTaskWithRuntimeGuardCommand(metadb.ChannelMigrationTaskCreate{
		Task:         task,
		RuntimeGuard: migrationRuntimeGuard(meta),
	}))
}

// CreateReplicaReplace creates a runtime-guarded replica-replacement task.
func (s *MigrationStore) CreateReplicaReplace(ctx context.Context, req CreateReplicaReplaceRequest) (metadb.ChannelMigrationTask, error) {
	if req.SourceNode == 0 || req.TargetNode == 0 || req.SourceNode == req.TargetNode {
		return metadb.ChannelMigrationTask{}, fmt.Errorf("%w: valid source and target are required", ch.ErrInvalidConfig)
	}
	route, meta, err := s.readRuntimeMeta(ctx, req.ChannelID)
	if err != nil {
		return metadb.ChannelMigrationTask{}, err
	}
	if err := validateReplicaReplaceTarget(meta, uint64(req.SourceNode), uint64(req.TargetNode)); err != nil {
		return metadb.ChannelMigrationTask{}, err
	}
	nowMS := s.nowMS()
	task := metadb.ChannelMigrationTask{
		TaskID:           migrationTaskID(req.TaskID, "replica-replace", req.ChannelID, nowMS),
		Kind:             metadb.ChannelMigrationKindReplicaReplace,
		Status:           metadb.ChannelMigrationStatusPending,
		Phase:            metadb.ChannelMigrationPhaseValidate,
		ChannelID:        req.ChannelID.ID,
		ChannelType:      int64(req.ChannelID.Type),
		SourceNode:       uint64(req.SourceNode),
		TargetNode:       uint64(req.TargetNode),
		BaseChannelEpoch: meta.ChannelEpoch,
		BaseLeaderEpoch:  meta.LeaderEpoch,
		CreatedAtMS:      nowMS,
		UpdatedAtMS:      nowMS,
	}
	return task, s.propose(ctx, route, metafsm.EncodeCreateChannelMigrationTaskWithRuntimeGuardCommand(metadb.ChannelMigrationTaskCreate{
		Task:         task,
		RuntimeGuard: migrationRuntimeGuard(meta),
	}))
}

// Claim records local ownership using expectedVersion as the task UpdatedAtMS guard.
func (s *MigrationStore) Claim(ctx context.Context, task metadb.ChannelMigrationTask, expectedVersion int64) error {
	if s == nil || s.localNode == 0 {
		return fmt.Errorf("%w: local migration owner node is required", ch.ErrInvalidConfig)
	}
	route, err := s.routeTask(ctx, task)
	if err != nil {
		return err
	}
	now := s.now()
	nowMS := now.UnixMilli()
	updatedAtMS := nextMigrationVersion(nowMS, expectedVersion)
	req := metadb.ChannelMigrationTaskClaim{
		Guard:             migrationTaskGuard(task, expectedVersion),
		Status:            metadb.ChannelMigrationStatusRunning,
		Phase:             task.Phase,
		OwnerNodeID:       s.localNode,
		OwnerLeaseUntilMS: now.Add(s.ownerTTL).UnixMilli(),
		NowMS:             nowMS,
		UpdatedAtMS:       updatedAtMS,
	}
	return s.propose(ctx, route, metafsm.EncodeClaimChannelMigrationTaskCommand(req))
}

// Advance updates task phase/status using expectedVersion as the task UpdatedAtMS guard.
func (s *MigrationStore) Advance(ctx context.Context, task metadb.ChannelMigrationTask, expectedVersion int64, phase metadb.ChannelMigrationPhase, status metadb.ChannelMigrationStatus, reason string) error {
	route, err := s.routeTask(ctx, task)
	if err != nil {
		return err
	}
	updatedAtMS := nextMigrationVersion(s.nowMS(), expectedVersion)
	req := metadb.ChannelMigrationTaskAdvance{
		Guard:       migrationTaskGuard(task, expectedVersion),
		Status:      status,
		Phase:       phase,
		Attempt:     task.Attempt + 1,
		UpdatedAtMS: updatedAtMS,
	}
	if status == metadb.ChannelMigrationStatusBlocked {
		req.BlockerMessage = reason
	} else if status == metadb.ChannelMigrationStatusFailed {
		req.LastError = reason
		req.CompletedAtMS = updatedAtMS
	}
	return s.propose(ctx, route, metafsm.EncodeAdvanceChannelMigrationTaskCommand(req))
}

// SetWriteFence sets or renews the task-owned channel write fence.
func (s *MigrationStore) SetWriteFence(ctx context.Context, task metadb.ChannelMigrationTask, reason ch.WriteFenceReason) error {
	id, err := migrationTaskChannelID(task)
	if err != nil {
		return err
	}
	route, meta, err := s.readRuntimeMeta(ctx, id)
	if err != nil {
		return err
	}
	updatedAtMS := nextMigrationVersion(s.nowMS(), task.UpdatedAtMS)
	req := metadb.ChannelMigrationFenceRequest{
		Guard:        migrationTaskGuard(task, task.UpdatedAtMS),
		RuntimeGuard: migrationRuntimeGuard(meta),
		Status:       metadb.ChannelMigrationStatusRunning,
		Phase:        migrationFencePhase(task),
		FenceReason:  uint8(reason),
		FenceUntilMS: s.now().Add(s.fenceTTL).UnixMilli(),
		UpdatedAtMS:  updatedAtMS,
	}
	return s.propose(ctx, route, metafsm.EncodeSetChannelWriteFenceCommand(req))
}

// ClearWriteFence clears the matching task-owned channel write fence.
func (s *MigrationStore) ClearWriteFence(ctx context.Context, task metadb.ChannelMigrationTask) error {
	id, err := migrationTaskChannelID(task)
	if err != nil {
		return err
	}
	route, meta, err := s.readRuntimeMeta(ctx, id)
	if err != nil {
		return err
	}
	updatedAtMS := nextMigrationVersion(s.nowMS(), task.UpdatedAtMS)
	status, phase, completedAtMS := migrationClearFenceTransition(task, updatedAtMS)
	req := metadb.ChannelMigrationClearFenceRequest{
		Guard:         migrationTaskGuard(task, task.UpdatedAtMS),
		RuntimeGuard:  migrationRuntimeGuard(meta),
		Status:        status,
		Phase:         phase,
		UpdatedAtMS:   updatedAtMS,
		CompletedAtMS: completedAtMS,
	}
	return s.propose(ctx, route, metafsm.EncodeClearChannelWriteFenceCommand(req))
}

// Abort marks the task aborted and clears its task-owned fence when present.
func (s *MigrationStore) Abort(ctx context.Context, task metadb.ChannelMigrationTask, reason string) error {
	id, err := migrationTaskChannelID(task)
	if err != nil {
		return err
	}
	route, meta, err := s.readRuntimeMeta(ctx, id)
	if err != nil {
		return err
	}
	updatedAtMS := nextMigrationVersion(s.nowMS(), task.UpdatedAtMS)
	req := metadb.ChannelMigrationAbortRequest{
		Guard:         migrationTaskGuard(task, task.UpdatedAtMS),
		RuntimeGuard:  migrationRuntimeGuard(meta),
		Status:        metadb.ChannelMigrationStatusAborted,
		Phase:         task.Phase,
		UpdatedAtMS:   updatedAtMS,
		CompletedAtMS: updatedAtMS,
		LastError:     reason,
	}
	return s.propose(ctx, route, metafsm.EncodeAbortChannelMigrationCommand(req))
}

// GetActive reads the active migration task for id from the channel-owned hash slot.
func (s *MigrationStore) GetActive(ctx context.Context, id ch.ChannelID) (metadb.ChannelMigrationTask, bool, error) {
	route, err := s.routeChannel(ctx, id)
	if err != nil {
		return metadb.ChannelMigrationTask{}, false, err
	}
	if s == nil || s.reader == nil {
		return metadb.ChannelMigrationTask{}, false, fmt.Errorf("%w: migration reader is nil", ch.ErrInvalidConfig)
	}
	return s.reader.GetActiveChannelMigrationTask(ctx, route.HashSlot, id.ID, int64(id.Type))
}

// Get reads one migration task for id from the channel-owned hash slot.
func (s *MigrationStore) Get(ctx context.Context, id ch.ChannelID, taskID string) (metadb.ChannelMigrationTask, bool, error) {
	route, err := s.routeChannel(ctx, id)
	if err != nil {
		return metadb.ChannelMigrationTask{}, false, err
	}
	if s == nil || s.reader == nil {
		return metadb.ChannelMigrationTask{}, false, fmt.Errorf("%w: migration reader is nil", ch.ErrInvalidConfig)
	}
	return s.reader.GetChannelMigrationTask(ctx, route.HashSlot, id.ID, int64(id.Type), taskID)
}

// ListActive lists active migration tasks from the hash slot that owns id.
func (s *MigrationStore) ListActive(ctx context.Context, id ch.ChannelID, limit int) ([]metadb.ChannelMigrationTask, error) {
	route, err := s.routeChannel(ctx, id)
	if err != nil {
		return nil, err
	}
	if s == nil || s.reader == nil {
		return nil, fmt.Errorf("%w: migration reader is nil", ch.ErrInvalidConfig)
	}
	return s.reader.ListActiveChannelMigrationTasks(ctx, route.HashSlot, limit)
}

func (s *MigrationStore) readRuntimeMeta(ctx context.Context, id ch.ChannelID) (routing.Route, metadb.ChannelRuntimeMeta, error) {
	route, err := s.routeChannel(ctx, id)
	if err != nil {
		return routing.Route{}, metadb.ChannelRuntimeMeta{}, err
	}
	if s == nil || s.reader == nil {
		return routing.Route{}, metadb.ChannelRuntimeMeta{}, fmt.Errorf("%w: migration reader is nil", ch.ErrInvalidConfig)
	}
	meta, err := s.reader.GetChannelRuntimeMeta(ctx, route.HashSlot, id.ID, int64(id.Type))
	if err != nil {
		return routing.Route{}, metadb.ChannelRuntimeMeta{}, err
	}
	meta = metadb.NormalizeChannelRuntimeMeta(meta)
	if meta.ChannelID != id.ID || meta.ChannelType != int64(id.Type) {
		return routing.Route{}, metadb.ChannelRuntimeMeta{}, fmt.Errorf("%w: runtime meta identity mismatch", ch.ErrInvalidConfig)
	}
	return route, meta, nil
}

func (s *MigrationStore) routeTask(ctx context.Context, task metadb.ChannelMigrationTask) (routing.Route, error) {
	id, err := migrationTaskChannelID(task)
	if err != nil {
		return routing.Route{}, err
	}
	return s.routeChannel(ctx, id)
}

func (s *MigrationStore) routeChannel(ctx context.Context, id ch.ChannelID) (routing.Route, error) {
	if err := ctxErr(ctx); err != nil {
		return routing.Route{}, err
	}
	if s == nil || s.router == nil {
		return routing.Route{}, fmt.Errorf("%w: migration router is nil", ch.ErrInvalidConfig)
	}
	if id.ID == "" {
		return routing.Route{}, fmt.Errorf("%w: channel id is required", ch.ErrInvalidConfig)
	}
	return s.router.RouteKey(id.ID)
}

func (s *MigrationStore) propose(ctx context.Context, route routing.Route, command []byte) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if s == nil || s.proposer == nil {
		return fmt.Errorf("%w: migration proposer is nil", ch.ErrInvalidConfig)
	}
	if len(command) == 0 {
		return fmt.Errorf("%w: empty migration command", ch.ErrInvalidConfig)
	}
	return s.proposer.ProposeChannelMigrationCommand(ctx, route.SlotID, route.HashSlot, command)
}

func (s *MigrationStore) nowMS() int64 {
	if s == nil || s.now == nil {
		return time.Now().UnixMilli()
	}
	return s.now().UnixMilli()
}

func migrationTaskID(taskID string, kind string, id ch.ChannelID, nowMS int64) string {
	if taskID != "" {
		return taskID
	}
	return fmt.Sprintf("channel-%s-%d-%s-%d", id.ID, id.Type, kind, nowMS)
}

func migrationTaskChannelID(task metadb.ChannelMigrationTask) (ch.ChannelID, error) {
	if task.ChannelID == "" {
		return ch.ChannelID{}, fmt.Errorf("%w: task channel id is required", ch.ErrInvalidConfig)
	}
	if task.TaskID == "" {
		return ch.ChannelID{}, fmt.Errorf("%w: task id is required", ch.ErrInvalidConfig)
	}
	if task.ChannelType < 0 || task.ChannelType > maxMigrationChannelType {
		return ch.ChannelID{}, fmt.Errorf("%w: task channel type %d out of range", ch.ErrInvalidConfig, task.ChannelType)
	}
	return ch.ChannelID{ID: task.ChannelID, Type: uint8(task.ChannelType)}, nil
}

func migrationTaskGuard(task metadb.ChannelMigrationTask, expectedVersion int64) metadb.ChannelMigrationTaskGuard {
	return metadb.ChannelMigrationTaskGuard{
		ChannelID:                 task.ChannelID,
		ChannelType:               task.ChannelType,
		TaskID:                    task.TaskID,
		ExpectedStatus:            task.Status,
		ExpectedPhase:             task.Phase,
		ExpectedOwnerNodeID:       task.OwnerNodeID,
		ExpectedOwnerLeaseUntilMS: task.OwnerLeaseUntilMS,
		ExpectedUpdatedAtMS:       expectedVersion,
	}
}

func migrationRuntimeGuard(meta metadb.ChannelRuntimeMeta) metadb.ChannelMigrationRuntimeGuard {
	meta = metadb.NormalizeChannelRuntimeMeta(meta)
	return metadb.ChannelMigrationRuntimeGuard{
		ChannelID:               meta.ChannelID,
		ChannelType:             meta.ChannelType,
		ExpectedChannelEpoch:    meta.ChannelEpoch,
		ExpectedLeaderEpoch:     meta.LeaderEpoch,
		ExpectedLeader:          meta.Leader,
		ExpectedFenceToken:      meta.WriteFenceToken,
		ExpectedFenceVersion:    meta.WriteFenceVersion,
		ExpectedRouteGeneration: meta.RouteGeneration,
	}
}

func migrationFencePhase(task metadb.ChannelMigrationTask) metadb.ChannelMigrationPhase {
	if task.Kind == metadb.ChannelMigrationKindLeaderTransfer || task.EmbeddedLeaderTransfer {
		if task.Phase == metadb.ChannelMigrationPhaseWriteFence {
			return metadb.ChannelMigrationPhaseDrainLeader
		}
		return task.Phase
	}
	if task.Kind == metadb.ChannelMigrationKindReplicaReplace && task.Phase == metadb.ChannelMigrationPhaseWarmCatchUp {
		return metadb.ChannelMigrationPhaseCutoverFence
	}
	return task.Phase
}

func migrationClearFenceTransition(task metadb.ChannelMigrationTask, updatedAtMS int64) (metadb.ChannelMigrationStatus, metadb.ChannelMigrationPhase, int64) {
	if task.Kind == metadb.ChannelMigrationKindReplicaReplace &&
		task.EmbeddedLeaderTransfer &&
		task.Phase == metadb.ChannelMigrationPhaseVerifyNewLeader {
		return metadb.ChannelMigrationStatusRunning, metadb.ChannelMigrationPhaseAddLearner, 0
	}
	return metadb.ChannelMigrationStatusCompleted, metadb.ChannelMigrationPhaseClearFence, updatedAtMS
}

func validateLeaderTransferTarget(meta metadb.ChannelRuntimeMeta, desiredLeader uint64) error {
	if meta.Leader == desiredLeader {
		return fmt.Errorf("%w: desired leader is already current leader", ch.ErrInvalidConfig)
	}
	if !migrationNodeInList(meta.Replicas, desiredLeader) {
		return fmt.Errorf("%w: desired leader is not a channel replica", ch.ErrInvalidConfig)
	}
	if !migrationNodeInList(meta.ISR, desiredLeader) {
		return fmt.Errorf("%w: desired leader is not in channel ISR", ch.ErrInvalidConfig)
	}
	return nil
}

func validateReplicaReplaceTarget(meta metadb.ChannelRuntimeMeta, sourceNode uint64, targetNode uint64) error {
	if !migrationNodeInList(meta.Replicas, sourceNode) {
		return fmt.Errorf("%w: source node is not a channel replica", ch.ErrInvalidConfig)
	}
	if migrationNodeInList(meta.Replicas, targetNode) {
		return fmt.Errorf("%w: target node is already a channel replica", ch.ErrInvalidConfig)
	}
	return nil
}

func migrationNodeInList(nodes []uint64, node uint64) bool {
	for _, candidate := range nodes {
		if candidate == node {
			return true
		}
	}
	return false
}

func nextMigrationVersion(nowMS int64, expectedVersion int64) int64 {
	if nowMS <= expectedVersion {
		return expectedVersion + 1
	}
	return nowMS
}
