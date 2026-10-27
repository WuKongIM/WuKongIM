package benchmark

import "testing"

// Pre-code F06: this private worker opt-in cannot be inferred from the older
// driver opt-in and cannot enter workload assignments, thresholds or reports.
func TestAttemptLinkEnvironmentIsStrictIndependentOptIn(t *testing.T) {
	for _, tc := range []struct {
		raw        string
		want, fail bool
	}{{"", false, false}, {"0", false, false}, {"false", false, false}, {"1", true, false}, {"true", true, false}, {"TRUE", false, true}, {"yes", false, true}, {" true", false, true}, {"2", false, true}} {
		t.Run(tc.raw, func(t *testing.T) {
			t.Setenv("WK_CHAT_LIFECYCLE_ATTEMPT_LINK_DIAGNOSTICS", tc.raw)
			t.Setenv("WK_CHAT_LIFECYCLE_DRIVER_DIAGNOSTICS", "true")
			got, err := loadAttemptLinkDiagnosticsEnvironment()
			if got != tc.want || (err != nil) != tc.fail {
				t.Fatalf("enabled=%v err=%v", got, err)
			}
		})
	}
}
