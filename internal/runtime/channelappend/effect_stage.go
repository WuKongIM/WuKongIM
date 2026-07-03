package channelappend

import (
	"errors"
	"fmt"

	"github.com/panjf2000/ants/v2"
)

const (
	effectStagePrepare    = "prepare"
	effectStageAppend     = "append"
	effectStagePostCommit = "post_commit"
	effectStageRealtime   = "realtime"
)

func effectPanicError(stage string, recovered any) error {
	return fmt.Errorf("%w: %s: %v", ErrEffectPanic, stage, recovered)
}

func effectScheduleError(stage string, err error) error {
	if err == nil || errors.Is(err, ants.ErrPoolOverload) || errors.Is(err, ants.ErrPoolClosed) {
		return ErrBackpressured
	}
	return fmt.Errorf("%w: %s scheduler submit: %w", ErrBackpressured, stage, err)
}
