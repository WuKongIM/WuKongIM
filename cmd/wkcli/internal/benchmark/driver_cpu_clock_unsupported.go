//go:build !linux

package benchmark

import (
	"errors"

	"github.com/WuKongIM/WuKongIM/internal/bench/chatlifecycle"
)

// Unsupported hosts reject the explicit opt-in rather than substitute a wall
// clock or fabricate a zero process-CPU observation. Default-off never calls it.
func newDriverCPUClockProbe() (func() chatlifecycle.DriverCPUClockSample, error) {
	return nil, errors.New("private driver CPU diagnostics require Linux process clocks")
}
