package benchmark

import (
	"errors"
	"os"
)

// loadDriverAdmissionDiagnosticsEnvironment reads an independent process opt-in.
// It never enables its driver parent or changes workload configuration or gates.
func loadDriverAdmissionDiagnosticsEnvironment(driverEnabled bool) (bool, error) {
	switch os.Getenv("WK_CHAT_LIFECYCLE_DRIVER_ADMISSION_DIAGNOSTICS") {
	case "", "0", "false":
		return false, nil
	case "1", "true":
		if !driverEnabled {
			return false, errors.New("private SDK admission diagnostics require explicit driver diagnostics")
		}
		return true, nil
	default:
		return false, errors.New("invalid private SDK admission diagnostic opt-in")
	}
}
