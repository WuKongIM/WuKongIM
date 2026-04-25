package meta

import (
	"errors"
	"time"
)

var (
	ErrClosed           = errors.New("controllermeta: closed")
	ErrNotFound         = errors.New("controllermeta: not found")
	ErrChecksumMismatch = errors.New("controllermeta: checksum mismatch")
	ErrCorruptValue     = errors.New("controllermeta: corrupt value")
	ErrInvalidArgument  = errors.New("controllermeta: invalid argument")
)

type NodeStatus uint8

const (
	NodeStatusUnknown NodeStatus = iota
	NodeStatusAlive
	NodeStatusSuspect
	NodeStatusDead
	NodeStatusDraining
)

type TaskKind uint8

const (
	TaskKindUnknown TaskKind = iota
	TaskKindBootstrap
	TaskKindRepair
	TaskKindRebalance
)

type TaskStep uint8

const (
	TaskStepUnknown TaskStep = iota
	TaskStepAddLearner
	TaskStepCatchUp
	TaskStepPromote
	TaskStepTransferLeader
	TaskStepRemoveOld
)

type TaskStatus uint8

const (
	TaskStatusUnknown TaskStatus = iota
	TaskStatusPending
	TaskStatusRetrying
	TaskStatusFailed
)

type ClusterNode struct {
	NodeID          uint64
	Addr            string
	Status          NodeStatus
	LastHeartbeatAt time.Time
	CapacityWeight  int
}

type SlotAssignment struct {
	SlotID         uint32
	DesiredPeers   []uint64
	ConfigEpoch    uint64
	BalanceVersion uint64
}

type SlotRuntimeView struct {
	SlotID              uint32
	CurrentPeers        []uint64
	LeaderID            uint64
	HealthyVoters       uint32
	HasQuorum           bool
	ObservedConfigEpoch uint64
	LastReportAt        time.Time
}

type ControllerMembership struct {
	Peers []uint64
}

type ReconcileTask struct {
	SlotID     uint32
	Kind       TaskKind
	Step       TaskStep
	SourceNode uint64
	TargetNode uint64
	Attempt    uint32
	NextRunAt  time.Time
	Status     TaskStatus
	LastError  string
}
