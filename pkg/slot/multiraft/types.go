package multiraft

import (
	"context"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.etcd.io/raft/v3/raftpb"
)

type SlotID uint64
type NodeID uint64

type Options struct {
	NodeID       NodeID
	TickInterval time.Duration
	Workers      int
	Transport    Transport
	Logger       wklog.Logger
	Raft         RaftOptions
	// Observer receives low-cardinality scheduler pressure observations.
	Observer SchedulerObserver
}

// SchedulerObserver receives low-cardinality Slot scheduler pressure observations.
// Implementations are called synchronously from scheduler and worker paths and should be concurrency-safe and non-blocking.
type SchedulerObserver interface {
	SetSchedulerWorkers(workers int)
	SetSchedulerInflight(inflight int)
	SetSchedulerState(event SchedulerStateEvent)
	ObserveSchedulerAdmission(result string)
	ObserveSchedulerTask(task string, d time.Duration)
}

// SchedulerStateEvent reports aggregate Slot scheduler queue state.
type SchedulerStateEvent struct {
	// Depth is the number of runnable Slot IDs queued in the dispatch channel.
	Depth int
	// Capacity is the dispatch channel capacity.
	Capacity int
	// Pending is the number of Slot IDs waiting behind a full dispatch channel.
	Pending int
	// Queued is the number of Slot IDs marked queued.
	Queued int
	// Processing is the number of Slot IDs currently being processed.
	Processing int
	// Dirty is the number of processing Slot IDs marked for another pass.
	Dirty int
}

type RaftOptions struct {
	ElectionTick  int
	HeartbeatTick int
	PreVote       bool
	CheckQuorum   bool
	MaxSizePerMsg uint64
	MaxInflight   int
	// LogCompaction controls local Slot Raft snapshot compaction.
	LogCompaction LogCompactionConfig
}

type SlotOptions struct {
	ID           SlotID
	Storage      Storage
	StateMachine StateMachine
}

type BootstrapSlotRequest struct {
	Slot   SlotOptions
	Voters []NodeID
}

type Envelope struct {
	SlotID  SlotID
	Message raftpb.Message
}

type Future interface {
	Wait(ctx context.Context) (Result, error)
}

type Result struct {
	Index uint64
	Term  uint64
	Data  []byte
}

type Status struct {
	SlotID   SlotID
	NodeID   NodeID
	LeaderID NodeID
	// CurrentVoters is the Raft voter set currently observed by this runtime.
	CurrentVoters []NodeID
	Term          uint64
	CommitIndex   uint64
	AppliedIndex  uint64
	Role          Role
}

type Transport interface {
	Send(ctx context.Context, batch []Envelope) error
}

type Storage interface {
	InitialState(ctx context.Context) (BootstrapState, error)
	Entries(ctx context.Context, lo, hi, maxSize uint64) ([]raftpb.Entry, error)
	Term(ctx context.Context, index uint64) (uint64, error)
	FirstIndex(ctx context.Context) (uint64, error)
	LastIndex(ctx context.Context) (uint64, error)
	Snapshot(ctx context.Context) (raftpb.Snapshot, error)

	Save(ctx context.Context, st PersistentState) error
	MarkApplied(ctx context.Context, index uint64) error
}

type BootstrapState struct {
	HardState    raftpb.HardState
	ConfState    raftpb.ConfState
	AppliedIndex uint64
}

type PersistentState struct {
	HardState *raftpb.HardState
	Entries   []raftpb.Entry
	Snapshot  *raftpb.Snapshot
}

type StateMachine interface {
	Apply(ctx context.Context, cmd Command) ([]byte, error)
	Restore(ctx context.Context, snap Snapshot) error
	Snapshot(ctx context.Context) (Snapshot, error)
}

// BatchStateMachine extends StateMachine with batched apply support.
// When implemented, processReady will collect contiguous normal entries
// and apply them in a single call, amortizing fsync cost.
type BatchStateMachine interface {
	StateMachine
	ApplyBatch(ctx context.Context, cmds []Command) ([][]byte, error)
}

type Command struct {
	SlotID   SlotID
	HashSlot uint16
	Index    uint64
	Term     uint64
	Data     []byte
}

type Snapshot struct {
	Index uint64
	Term  uint64
	Data  []byte
}

type ConfigChange struct {
	Type    ChangeType
	NodeID  NodeID
	Context []byte
}

type ChangeType uint8

const (
	AddVoter ChangeType = iota + 1
	RemoveVoter
	AddLearner
	PromoteLearner
)

type Role uint8

const (
	RoleFollower Role = iota + 1
	RoleCandidate
	RoleLeader
)
