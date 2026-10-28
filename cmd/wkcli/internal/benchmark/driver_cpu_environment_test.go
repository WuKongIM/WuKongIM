package benchmark

import (
	"strings"
	"testing"
)

// CPU sampling is a separate private opt-in, and cannot silently turn on the
// older driver instrumentation that supplies its anonymous Turn identity.
func TestDriverCPUDiagnosticsEnvironmentRequiresExplicitBothOptions(t *testing.T) {
	for _, tc := range []struct {
		name      string
		value     string
		driver    bool
		enabled   bool
		wantError bool
	}{
		{"default_off", "", false, false, false},
		{"off_with_driver", "", true, false, false},
		{"zero", "0", true, false, false},
		{"false", "false", false, false, false},
		{"one_requires_driver", "1", false, false, true},
		{"true_requires_driver", "true", false, false, true},
		{"one", "1", true, true, false},
		{"true", "true", true, true, false},
		{"uppercase", "TRUE", true, false, true},
		{"whitespace", " true", true, false, true},
		{"arbitrary", "private-uid-channel-token-do-not-retain", true, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("WK_CHAT_LIFECYCLE_DRIVER_CPU_DIAGNOSTICS", tc.value)
			enabled, err := loadDriverCPUDiagnosticsEnvironment(tc.driver)
			if enabled != tc.enabled || (err != nil) != tc.wantError {
				t.Fatalf("enabled=%t want=%t has_error=%t want_error=%t", enabled, tc.enabled, err != nil, tc.wantError)
			}
			if err != nil && strings.Contains(err.Error(), "private-uid-channel-token-do-not-retain") {
				t.Fatal("private configuration error retained the raw value")
			}
		})
	}
}
