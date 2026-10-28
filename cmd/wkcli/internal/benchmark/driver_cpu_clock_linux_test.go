//go:build linux

package benchmark

import (
	"errors"
	"reflect"
	"testing"

	"golang.org/x/sys/unix"
)

// The injected read function is the adapter's syscall boundary. Independent
// failures preserve successful raw numbers and cannot skip the final bracket.
func TestDriverCPUClockProbeLinuxOrdersReadsAndPreservesPartialResults(t *testing.T) {
	for _, failed := range []int{-1, 0, 1, 2} {
		var ids []int32
		values := []int64{101, 27, 109}
		sample := sampleDriverCPUClocks(func(id int32) (int64, error) {
			index := len(ids)
			ids = append(ids, id)
			if index == failed {
				return 0, errors.New("private syscall details must not be retained")
			}
			return values[index], nil
		})
		if !reflect.DeepEqual(ids, []int32{unix.CLOCK_MONOTONIC, unix.CLOCK_PROCESS_CPUTIME_ID, unix.CLOCK_MONOTONIC}) {
			t.Fatalf("CPU probe changed the three-read order: %v", ids)
		}
		mask := uint8(7)
		if failed >= 0 {
			mask &^= 1 << failed
			values[failed] = 0
		}
		if sample.ReadMask != mask || sample.MonotonicBeforeNanos != values[0] || sample.ProcessCPUNanos != values[1] || sample.MonotonicAfterNanos != values[2] {
			t.Fatalf("partial clock facts changed: failed=%d sample=%+v", failed, sample)
		}
	}
}
