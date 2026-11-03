//go:build integration

package scripts_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestLocalSendObservations runs the real postload exporter against loopback
// HTTP fixtures. It never starts a product node or a benchmark workload.
func TestLocalSendObservations(t *testing.T) {
	root := repoRoot(t)
	artifact := os.Getenv("LOCAL_SEND_CAPTURE_TEST_ARTIFACT")
	if artifact == "" {
		artifact = filepath.Join(t.TempDir(), "local-send-capture-results.json")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "python3", filepath.Join(root, "scripts/testdata/local_send_observations_test.py"))
	command.Dir = root
	command.Env = append(os.Environ(), "LOCAL_SEND_CAPTURE_REPO="+root, "LOCAL_SEND_CAPTURE_TEST_ARTIFACT="+artifact)
	if output, err := runPreClockFixture(command); err != nil {
		t.Errorf("postload export fixtures failed: %v\n%s", err, output)
	}
	var result struct {
		TestsRun    int  `json:"tests_run"`
		Failures    int  `json:"failures"`
		Errors      int  `json:"errors"`
		FixtureOnly bool `json:"fixture_only"`
	}
	body, err := os.ReadFile(artifact)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, &result); err != nil || result.TestsRun < 8 || result.Failures != 0 || result.Errors != 0 || !result.FixtureOnly {
		t.Fatalf("invalid black-box export artifact: %v / %+v", err, result)
	}
}
