package benchmark

import "testing"

// A private diagnostic environment value is read once at CLI composition and
// never changes workload TOML, assignments, gates or business configuration.
func TestDriverDiagnosticsEnvironmentOptInIsExplicit(t *testing.T) {
	for _, tc := range []struct {
		value              string
		enabled, wantError bool
	}{{"", false, false}, {"false", false, false}, {"0", false, false}, {"1", true, false}, {"true", true, false}, {"TRUE", false, true}, {"yes", false, true}} {
		t.Run(tc.value, func(t *testing.T) {
			t.Setenv("WK_CHAT_LIFECYCLE_DRIVER_DIAGNOSTICS", tc.value)
			enabled, err := loadDriverDiagnosticsEnvironment()
			if enabled != tc.enabled || (err != nil) != tc.wantError {
				t.Fatalf("enabled=%v err=%v", enabled, err)
			}
		})
	}
}
