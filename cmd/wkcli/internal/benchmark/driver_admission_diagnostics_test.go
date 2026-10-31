package benchmark

import "testing"

// The selected DTO/API and failure inventory were frozen before this file.
func TestDriverAdmissionDiagnosticsEnvironmentRequiresBothOptIns(t *testing.T) {
	for _, tc := range []struct {
		value                      string
		driver, enabled, wantError bool
	}{{"", false, false, false}, {"0", true, false, false}, {"false", true, false, false},
		{"1", true, true, false}, {"true", true, true, false}, {"1", false, false, true},
		{"true", false, false, true}, {"TRUE", true, false, true}, {"yes", true, false, true}} {
		t.Run(tc.value, func(t *testing.T) {
			t.Setenv("WK_CHAT_LIFECYCLE_DRIVER_ADMISSION_DIAGNOSTICS", tc.value)
			t.Setenv("WK_CHAT_LIFECYCLE_DRIVER_CPU_DIAGNOSTICS", "true")
			t.Setenv("WK_CHAT_LIFECYCLE_ATTEMPT_LINK_DIAGNOSTICS", "true")
			enabled, err := loadDriverAdmissionDiagnosticsEnvironment(tc.driver)
			if enabled != tc.enabled || (err != nil) != tc.wantError {
				t.Fatalf("enabled=%v error=%v; other diagnostics must not enable admission", enabled, err)
			}
		})
	}
}
