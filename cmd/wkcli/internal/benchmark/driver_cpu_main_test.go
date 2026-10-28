package benchmark

import (
	"bytes"
	"strings"
	"testing"
)

func TestDriverCPUWorkerConfigurationValidatesOptInBeforeCredentials(t *testing.T) {
	for _, test := range []struct{ name, driver, cpu string }{
		{"malformed", "0", "invalid-private-value"},
		{"dependency", "0", "1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("WK_CHAT_LIFECYCLE_DRIVER_DIAGNOSTICS", test.driver)
			t.Setenv("WK_CHAT_LIFECYCLE_DRIVER_CPU_DIAGNOSTICS", test.cpu)
			t.Setenv("WK_CHAT_LIFECYCLE_ATTEMPT_LINK_DIAGNOSTICS", "0")
			t.Setenv("WK_CHAT_LIFECYCLE_RUNTIME_TRACE", "0")
			var stderr bytes.Buffer
			got := runWorkerConfig(workerCLIConfig{mode: workerModeChatLifecycle}, &stderr)
			if got != exitConfig || stderr.String() != "chat lifecycle worker CPU diagnostic configuration failed\n" {
				t.Fatalf("CPU opt-in did not fail before credentials/Serve: exit=%d stderr=%q", got, stderr.String())
			}
			if strings.Contains(stderr.String(), test.cpu) && test.cpu != "1" {
				t.Fatal("private configuration value leaked")
			}
		})
	}
}
