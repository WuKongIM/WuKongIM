//go:build integration

package scripts_test

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"
)

// Execute the actual exit test across Go's serial-to-parallel scheduling
// boundary. Its five-second process deadline must begin after admission.
func TestEasySDKReadinessDeadlineStartsAfterAdmission(t *testing.T) {
	runHeavyShellScriptTestInParallel(t)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, executable,
		"-test.run=^TestEasySDKReadiness(DetectsExitedServer|SerialDelayFixture)$",
		"-test.timeout=15s", "-test.parallel=2", "-test.v")
	command.Env = append(os.Environ(), "WK_EASYSDK_SERIAL_DELAY_FIXTURE=1")
	command.WaitDelay = time.Second
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("readiness deadline included scheduler wait: %v\n%s", err, output)
	}
}

func TestEasySDKReadinessSerialDelayFixture(t *testing.T) {
	if os.Getenv("WK_EASYSDK_SERIAL_DELAY_FIXTURE") != "1" {
		t.Skip("subprocess scheduling fixture only")
	}
	// Registered after the exit test: hold the serial phase beyond its process
	// budget, before Go resumes queued parallel tests. This delay is the stimulus.
	time.Sleep(6 * time.Second)
}
