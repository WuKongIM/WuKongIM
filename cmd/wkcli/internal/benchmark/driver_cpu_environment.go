package benchmark

import (
	"errors"
	"os"
)

// loadDriverCPUDiagnosticsEnvironment is a process-local opt-in, independent
// of workload configuration. CPU observation never enables its driver parent.
func loadDriverCPUDiagnosticsEnvironment(driverEnabled bool) (bool, error) {
	switch os.Getenv("WK_CHAT_LIFECYCLE_DRIVER_CPU_DIAGNOSTICS") {
	case "", "0", "false":
		return false, nil
	case "1", "true":
		if !driverEnabled {
			return false, errors.New("private driver CPU diagnostics require explicit driver diagnostics")
		}
		return true, nil
	default:
		return false, errors.New("invalid private driver CPU diagnostic opt-in")
	}
}
