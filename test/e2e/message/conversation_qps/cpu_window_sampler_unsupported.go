//go:build !linux

package conversation_qps

import (
	"fmt"
	"time"
)

func startCPUWindowSampler(_ []cpuSampleProcess, _ []string, _ time.Time, _ time.Duration) (func() CPUWindowSamples, error) {
	return nil, fmt.Errorf("CPU window sampling requires Linux procfs and CLOCK_THREAD_CPUTIME_ID")
}
