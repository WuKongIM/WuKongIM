package bench

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/cmd/wkcli/internal/command"
)

func TestBenchSendCommandParsesConfigAndRunsExecutor(t *testing.T) {
	orig := executeSend
	t.Cleanup(func() { executeSend = orig })
	var captured sendConfig
	executeSend = func(_ context.Context, cfg sendConfig) (sendResult, error) {
		captured = cfg
		return sendResultFixture(), nil
	}
	var stdout, stderr bytes.Buffer
	cmd := NewCommand(command.Deps{Stdout: &stdout, Stderr: &stderr})
	cmd.SetArgs([]string{
		"send",
		"--gateway", "127.0.0.1:5100",
		"--clients", "4",
		"--msgs", "1000",
		"--channels", "10",
		"--channel-prefix", "bench-g",
		"--channel-type", "group",
		"--size", "128B",
		"--batch", "8",
		"--throughput", "500",
		"--ack-timeout", "2s",
		"--no-progress",
	})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v stderr %q", err, stderr.String())
	}
	if captured.Clients != 4 || captured.Messages != 1000 || captured.Channels != 10 || captured.BatchSize != 8 {
		t.Fatalf("captured config = %#v", captured)
	}
	if captured.AckTimeout != 2*time.Second {
		t.Fatalf("AckTimeout = %s, want 2s", captured.AckTimeout)
	}
	if !strings.Contains(stdout.String(), "WKProto SEND summary") {
		t.Fatalf("expected human summary, got stdout %q stderr %q", stdout.String(), stderr.String())
	}
}

func TestBenchCommandHasNoPubAlias(t *testing.T) {
	var stdout, stderr bytes.Buffer
	cmd := NewCommand(command.Deps{Stdout: &stdout, Stderr: &stderr})
	cmd.SetArgs([]string{"pub"})

	err := cmd.Execute()

	if err == nil {
		t.Fatalf("expected unknown command error")
	}
	if !strings.Contains(err.Error(), `unknown command "pub"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}
