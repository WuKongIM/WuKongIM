package mqttsession

import (
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// ReplayTargetCursor pins one replica's target while retaining scan/donor hints.
// New accepted anchors must not restart unfinished recovery of this target.
type ReplayTargetCursor struct {
	NodeID                      ch.NodeID
	AnchorPosition, AfterAnchor uint64
	DonorAfter                  ch.NodeID
}

// ReplayCursor is bounded process-local scheduling state, never durable evidence.
// Callers serialize turns per source. Continued visits retain Next on failure;
// cold visits seed Pass and recover all content progress from durable storage.
type ReplayCursor struct {
	// Pass seeds cold scheduling only; it never supplies content progress.
	Pass      uint64
	Source    meta.MQTTBindingOwner
	Authority [32]byte
	// Targets follows the exact ordered placement and contains at most 256 entries.
	Targets    []ReplayTargetCursor
	NextTarget int
	// RepairNext gives replica recovery a turn after every attempted copy.
	RepairNext bool
}

// ReplayStepResult acknowledges only this bounded operation. TargetComplete
// confirms one target's coverage and requested original-source release, never
// that all replicas are caught up, learner readiness, shared-content GC or SUBACK.
type ReplayStepResult struct {
	// ContinueScan requests continuation of this exact finite journal scan.
	// Import, completion and failed donor rounds yield to another source.
	ContinueScan                       bool
	Next                               ReplayCursor
	Target                             ch.NodeID
	Anchored, Repaired, TargetComplete bool
}
