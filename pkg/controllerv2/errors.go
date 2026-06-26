package controllerv2

import (
	"errors"

	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/fsm"
	cv2raft "github.com/WuKongIM/WuKongIM/pkg/controllerv2/raft"
)

var (
	// ErrExpectedRevisionMismatch indicates that a compare-and-set revision fence rejected a command.
	ErrExpectedRevisionMismatch = errors.New("controllerv2: expected revision mismatch")
	// ErrSlotActiveTaskConflict indicates that a Slot already has an active Controller task.
	ErrSlotActiveTaskConflict = errors.New("controllerv2: slot already has active task")
)

// IsExpectedRevisionMismatch reports whether err is a ControllerV2 revision-fence rejection.
func IsExpectedRevisionMismatch(err error) bool {
	if errors.Is(err, ErrExpectedRevisionMismatch) {
		return true
	}
	var rejected cv2raft.ProposalRejectedError
	return errors.As(err, &rejected) && rejected.Reason == fsm.ReasonExpectedRevisionMismatch
}
