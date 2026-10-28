//go:build linux

package benchmark

import (
	"github.com/WuKongIM/WuKongIM/internal/bench/chatlifecycle"
	"golang.org/x/sys/unix"
)

// newDriverCPUClockProbe supplies the Linux process-wide clock observation.
// Constructing it does not read clocks or start another sampler goroutine.
func newDriverCPUClockProbe() (func() chatlifecycle.DriverCPUClockSample, error) {
	return func() chatlifecycle.DriverCPUClockSample {
		return sampleDriverCPUClocks(func(id int32) (int64, error) {
			var value unix.Timespec
			err := unix.ClockGettime(id, &value)
			return value.Nano(), err
		})
	}, nil
}

// sampleDriverCPUClocks preserves every original number and independent read
// bit. MONOTONIC brackets expose uncertainty without another business clock.
func sampleDriverCPUClocks(read func(int32) (int64, error)) chatlifecycle.DriverCPUClockSample {
	var sample chatlifecycle.DriverCPUClockSample
	var err error
	sample.MonotonicBeforeNanos, err = read(unix.CLOCK_MONOTONIC)
	if err == nil {
		sample.ReadMask |= 1
	}
	sample.ProcessCPUNanos, err = read(unix.CLOCK_PROCESS_CPUTIME_ID)
	if err == nil {
		sample.ReadMask |= 2
	}
	sample.MonotonicAfterNanos, err = read(unix.CLOCK_MONOTONIC)
	if err == nil {
		sample.ReadMask |= 4
	}
	return sample
}
