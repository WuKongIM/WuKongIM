package benchmark

import "testing"

// Yield wall stays within the existing driver opt-in. It must never enable a
// CPU exporter or runtime recorder as a side effect of enabling driver timing.
func TestDriverYieldUsesExistingDriverOptInWithoutOtherObservers(t *testing.T) {
	for _, value := range []string{"", "0", "false", "1", "true"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("WK_CHAT_LIFECYCLE_DRIVER_DIAGNOSTICS", value)
			t.Setenv("WK_CHAT_LIFECYCLE_DRIVER_CPU_DIAGNOSTICS", "0")
			t.Setenv("WK_CHAT_LIFECYCLE_RUNTIME_TRACE", "0")
			driver, err := loadDriverDiagnosticsEnvironment()
			if err != nil || driver != (value == "1" || value == "true") {
				t.Fatalf("existing driver opt-in changed: driver=%v err=%v", driver, err)
			}
			cpu, err := loadDriverCPUDiagnosticsEnvironment(driver)
			if err != nil || cpu {
				t.Fatal("yield timing implicitly enabled process CPU observation")
			}
			trace, err := loadWorkerRuntimeTraceEnvironment()
			if err != nil || trace {
				t.Fatal("yield timing implicitly enabled known disruptive runtime tracing")
			}
		})
	}
}
