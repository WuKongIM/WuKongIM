//go:build !linux

package conversation_qps

import (
	"fmt"
	"time"
)

// applyCPUPlacement never simulates Linux affinity on an unsupported platform.
func applyCPUPlacement(_ int, _ []int, mode string) (*CPUPlacementReceipt, error) {
	err := fmt.Errorf("CPU placement requires Linux")
	return &CPUPlacementReceipt{CPUPlacementPlan: CPUPlacementPlan{Mode: mode}, StartedAt: time.Now().UTC(), FinishedAt: time.Now().UTC(), Failure: err.Error()}, err
}
