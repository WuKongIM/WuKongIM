package controller

import (
	"errors"

	"github.com/WuKongIM/WuKongIM/pkg/controller/fsm"
	controllerraft "github.com/WuKongIM/WuKongIM/pkg/controller/raft"
)

var (
	// ErrExpectedRevisionMismatch indicates that a compare-and-set revision fence rejected a command.
	ErrExpectedRevisionMismatch = errors.New("controller: expected revision mismatch")
	// ErrSlotActiveTaskConflict indicates that a Slot already has an active Controller task.
	ErrSlotActiveTaskConflict = errors.New("controller: slot already has active task")
)

// IsExpectedRevisionMismatch reports whether err is a Controller revision-fence rejection.
func IsExpectedRevisionMismatch(err error) bool {
	if errors.Is(err, ErrExpectedRevisionMismatch) {
		return true
	}
	var rejected controllerraft.ProposalRejectedError
	return errors.As(err, &rejected) && rejected.Reason == fsm.ReasonExpectedRevisionMismatch
}

// IsTaskPhaseMismatch reports whether err is an obsolete task phase write rejection.
func IsTaskPhaseMismatch(err error) bool {
	var rejected controllerraft.ProposalRejectedError
	return errors.As(err, &rejected) && rejected.Reason == fsm.ReasonTaskPhaseMismatch
}
