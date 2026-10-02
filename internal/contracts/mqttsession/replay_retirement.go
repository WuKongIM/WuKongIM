package mqttsession

import (
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// ReplayRetirementCursor is a bounded, body-free scheduling hint. Each resumed
// turn must reread consumer permission; this value never authorizes deletion.
type ReplayRetirementCursor struct {
	Source    meta.MQTTBindingOwner
	Authority [32]byte
	// Captured and Through stay fixed while BeforeAnchor decreases. A newer
	// accepted anchor or increasing consumer progress cannot extend this scan.
	Captured              ch.MQTTReplayAnchorProof
	Through, BeforeAnchor uint64
}

// ReplayRetirementResult reports a finite scan or an independently committed
// decision. Committed never means replica cleanup or source removal completed.
type ReplayRetirementResult struct {
	Next         ReplayRetirementCursor
	ContinueScan bool
	Committed    bool
	Proof        ch.MQTTReplayRetirementProof
}
