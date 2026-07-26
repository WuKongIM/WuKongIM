package backup

import (
	"context"
	"time"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
)

const defaultDoctorRetryInterval = time.Minute

// CoordinatorDoctor gates backup repository work without affecting foreground readiness.
type CoordinatorDoctor interface {
	Check(context.Context) (backupcontract.DoctorReport, error)
}

// CoordinatorLeadership identifies the local node and current Controller leader.
type CoordinatorLeadership interface {
	NodeID() uint64
	BackupControllerLeaderID() uint64
}

// CheckpointObservation is the newest authenticated durable checkpoint timing.
type CheckpointObservation struct {
	// EffectiveAtUnixMillis is the oldest Slot watermark in the checkpoint.
	EffectiveAtUnixMillis int64
	// CreatedAtUnixMillis is the immutable catalog publication time.
	CreatedAtUnixMillis int64
}

// ContinuousCheckpointObservationSource reads the newest durable catalog state
// without depending on which node published it.
type ContinuousCheckpointObservationSource interface {
	LatestCheckpoint(context.Context) (CheckpointObservation, bool, error)
}

// ControllerMaintenance advances one bounded Leader-only maintenance step.
type ControllerMaintenance interface {
	RunIfLeader(context.Context, CoordinatorLeadership) (bool, error)
}

// ContinuousProjectionRunner keeps all-node local safety projections aligned.
type ContinuousProjectionRunner interface {
	Run(context.Context) error
}

// RuntimeObserver receives low-cardinality continuous-backup and restore evidence.
type RuntimeObserver interface {
	SetBackupControllerLeader(bool)
	SetBackupDoctorHealth(string)
	SetBackupCheckpointAgeSeconds(*int64)
	ObserveBackupFailure(string)
	SetBackupRestoreProgress(int, int, int)
}

// CoordinatorStatus is a bounded node-local operational snapshot.
type CoordinatorStatus struct {
	// Running reports whether this node's continuous coordinator loop is active.
	Running bool
	// ControllerLeader reports whether this node currently owns coordination.
	ControllerLeader bool
	// DoctorHealth is the aggregate dependency qualification state.
	DoctorHealth backupcontract.Health
	// LastDoctorAtUnixMillis is the latest completed dependency check.
	LastDoctorAtUnixMillis int64
	// LastSuccessAtUnixMillis is the newest authenticated checkpoint publication.
	LastSuccessAtUnixMillis int64
	// LastFailureCategory is the newest bounded operational failure class.
	LastFailureCategory string
	// Doctor is retained inside the runtime for diagnostics; access surfaces
	// expose only aggregate health and bounded failure category.
	Doctor backupcontract.DoctorReport
}
