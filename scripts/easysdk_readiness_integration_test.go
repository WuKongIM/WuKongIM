//go:build integration

package scripts_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestEasySDKStableReadiness(t *testing.T) {
	for _, tc := range []struct {
		name, failures string
		polls          int
		success        bool
	}{
		{"ready", "", 3, true},
		{"flapping resets streak", "2 5", 8, true},
		{"unavailable is bounded", "all", 90, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			count := filepath.Join(dir, "count")
			log := filepath.Join(dir, "server.log")
			for name, body := range map[string]string{
				"curl": `#!/usr/bin/env bash
set -eu
[[ "$*" == "-fsS --max-time 1 http://127.0.0.1:5001/readyz" ]]
n=0
[[ ! -f "$POLL_COUNT" ]] || n=$(cat "$POLL_COUNT")
n=$((n + 1))
echo "$n" > "$POLL_COUNT"
[[ "$FAIL_POLLS" != all ]] || exit 22
for failure in $FAIL_POLLS; do
 [[ "$failure" != "$n" ]] || exit 22
done
`,
				"sleep":      "#!/bin/sh\nexit 0\n",
				"server.log": "fixture diagnostic retained\n",
			} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0700); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "bash", filepath.Join(repoRoot(t), "test/easysdk-release/wait-ready.sh"), strconv.Itoa(os.Getpid()), log)
			cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "POLL_COUNT="+count, "FAIL_POLLS="+tc.failures)
			output, err := cmd.CombinedOutput()
			if ctx.Err() != nil {
				t.Fatal(ctx.Err())
			}
			if (err == nil) != tc.success {
				t.Fatalf("success=%v: %v\n%s", tc.success, err, output)
			}
			actual, err := os.ReadFile(count)
			if err != nil {
				t.Fatal(err)
			}
			if strings.TrimSpace(string(actual)) != strconv.Itoa(tc.polls) {
				t.Fatalf("polls=%s, want %d", actual, tc.polls)
			}
			if !tc.success && (!strings.Contains(string(output), "within 90 polls") || !strings.Contains(string(output), "fixture diagnostic retained")) {
				t.Fatalf("missing failure evidence: %s", output)
			}
		})
	}
}

func TestEasySDKReadinessDetectsExitedServer(t *testing.T) {
	// repoRoot admits this integration test to the parallel scheduler. Start
	// process deadlines only after that potentially long scheduling wait.
	root := repoRoot(t)
	child := exec.Command("sh", "-c", "exit 0")
	if err := child.Run(); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(t.TempDir(), "server.log")
	if err := os.WriteFile(log, []byte("early exit diagnostic\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "bash", filepath.Join(root, "test/easysdk-release/wait-ready.sh"), strconv.Itoa(child.Process.Pid), log).CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("readiness exit check: %v, process error=%v\n%s", ctx.Err(), err, output)
	}
	if err == nil || !strings.Contains(string(output), "exited before stable readiness") || !strings.Contains(string(output), "early exit diagnostic") {
		t.Fatalf("unexpected result: %v\n%s", err, output)
	}
}
