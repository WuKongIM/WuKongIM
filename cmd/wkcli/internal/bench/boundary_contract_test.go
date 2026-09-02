package bench

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/cmd/wkcli/internal/command"
	wkclient "github.com/WuKongIM/WuKongIM/pkg/client"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestChannelPlannerKeepsRandomSelectionDeterministicAndCommandScoped(t *testing.T) {
	planner, err := newChannelPlanner(sendConfig{
		Channels: 3, ChannelPrefix: "cmd", ChannelType: "cmd", ChannelTypeID: frame.ChannelTypeGroup,
		ChannelPick: channelPickRandom, RandomSeed: 0,
	})
	if err != nil {
		t.Fatalf("newChannelPlanner(): %v", err)
	}
	first := planner.Pick(4)
	if again := planner.Pick(4); again != first {
		t.Fatalf("random selection changed: %+v then %+v", first, again)
	}
	if !strings.HasSuffix(first.ID, commandChannelSuffix) {
		t.Fatalf("command channel = %q, want suffix %q", first.ID, commandChannelSuffix)
	}
	if got := commandChannelID(first.ID); got != first.ID {
		t.Fatalf("commandChannelID() duplicated suffix: %q", got)
	}
	if got := (&channelPlanner{}).Pick(0); got != (benchChannel{}) {
		t.Fatalf("empty Pick() = %+v", got)
	}
	if _, err := newChannelPlanner(sendConfig{}); err == nil {
		t.Fatal("newChannelPlanner(zero channels) error = nil")
	}
}

func TestConfigParsersAcceptAliasesAndRejectUnboundedPayloads(t *testing.T) {
	for _, test := range []struct {
		input string
		id    uint8
		name  string
	}{
		{input: "1", id: frame.ChannelTypePerson, name: "person"},
		{input: " PERSON ", id: frame.ChannelTypePerson, name: "person"},
		{input: "2", id: frame.ChannelTypeGroup, name: "group"},
		{input: "command", id: frame.ChannelTypeGroup, name: "cmd"},
	} {
		id, name, err := parseChannelType(test.input)
		if err != nil || id != test.id || name != test.name {
			t.Fatalf("parseChannelType(%q) = (%d, %q, %v)", test.input, id, name, err)
		}
	}
	for _, test := range []struct {
		input string
		want  int
	}{
		{input: "", want: 128},
		{input: "1KiB", want: 1024},
		{input: "0.5KB", want: 500},
		{input: "2MiB", want: 2 * 1024 * 1024},
	} {
		got, err := parseByteSize(test.input)
		if err != nil || got != test.want {
			t.Fatalf("parseByteSize(%q) = (%d, %v), want %d", test.input, got, err, test.want)
		}
	}
	for _, input := range []string{"B", "0B", "-1B", "NaN", "Inf", "999999999999999999999GB"} {
		if _, err := parseByteSize(input); err == nil {
			t.Fatalf("parseByteSize(%q) error = nil", input)
		}
	}

	cfg, err := normalizeSendConfig(sendConfig{
		Clients: 1, Messages: 1, Channels: 1, Payload: "hello", ChannelType: "cmd", ChannelPick: channelPickRandom,
	})
	if err != nil {
		t.Fatalf("normalizeSendConfig(literal payload): %v", err)
	}
	if cfg.PayloadBytes != 5 || cfg.ChannelType != "cmd" {
		t.Fatalf("normalized config = %+v", cfg)
	}
	bad := cfg
	bad.ChannelPick = "shuffle"
	if _, err := normalizeSendConfig(bad); err == nil {
		t.Fatal("normalizeSendConfig(unknown pick) error = nil")
	}
}

func TestWriteSendResultHonorsCSVJSONAndWriterFailures(t *testing.T) {
	result := sendResultFixture()
	csvPath := filepath.Join(t.TempDir(), "summary.csv")
	var stdout bytes.Buffer
	if err := writeSendResult(command.Deps{Stdout: &stdout}, sendConfig{CSVPath: csvPath, JSON: true}, result); err != nil {
		t.Fatalf("writeSendResult(JSON+CSV): %v", err)
	}
	if body, err := os.ReadFile(csvPath); err != nil || !bytes.Contains(body, []byte("duration_ms")) {
		t.Fatalf("CSV = %q, %v", body, err)
	}
	if !strings.Contains(stdout.String(), `"result": "pass"`) {
		t.Fatalf("JSON stdout = %q", stdout.String())
	}
	if err := writeSendResult(command.Deps{Stdout: failingWriter{}}, sendConfig{}, result); err == nil {
		t.Fatal("writeSendResult(failing human writer) error = nil")
	}
	if err := writeSendResult(command.Deps{Stdout: failingWriter{}}, sendConfig{JSON: true}, result); err == nil {
		t.Fatal("writeSendResult(failing JSON writer) error = nil")
	}
	if err := writeSendResult(command.Deps{Stdout: &bytes.Buffer{}}, sendConfig{CSVPath: filepath.Join(t.TempDir(), "missing", "summary.csv")}, result); err == nil {
		t.Fatal("writeSendResult(missing CSV parent) error = nil")
	}
}

func TestSendStatsExposeFailureAndProgressWithoutAliasing(t *testing.T) {
	cfg := sendConfig{Messages: 4, PayloadBytes: 100, Clients: 1, BatchSize: 1, ChannelPick: channelPickRoundRobin}
	stats := newSendStats(cfg, 2, 2)
	stats.recordScheduled("c1")
	stats.recordScheduled("c2")
	stats.recordSendack(frame.ReasonSuccess, time.Millisecond)
	stats.recordSendError(2, "timeout")
	snapshot := stats.snapshot(time.Second)
	if snapshot.Done != 3 || snapshot.Errors != 2 || snapshot.Percent != 75 || snapshot.Throughput != 1 || snapshot.BandwidthBytes != 100 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	result := stats.finish(time.Second)
	if result.Result != resultFail || result.Success != 1 || result.Errors != 2 || result.ErrorCounts["timeout"] != 2 {
		t.Fatalf("result = %+v", result)
	}
	result.ErrorCounts["timeout"] = 99
	if stats.errorCounts["timeout"] != 2 {
		t.Fatal("finish() returned aliased error counts")
	}
	for err, want := range map[error]string{
		context.DeadlineExceeded:  "timeout",
		context.Canceled:          "canceled",
		errors.New("send failed"): "error",
	} {
		if got := classifyError(err); got != want {
			t.Fatalf("classifyError(%v) = %q, want %q", err, got, want)
		}
	}
}

func TestFormattingClampsTerminalProgressBoundaries(t *testing.T) {
	if got := formatProgressBar(-1, 0); got != "[>]" {
		t.Fatalf("negative progress bar = %q", got)
	}
	if got := formatProgressBar(100, 3); got != "[===]" {
		t.Fatalf("complete progress bar = %q", got)
	}
	if clampPercent(101) != 100 || clampPercent(-1) != 0 || upperResult(resultFail) != "FAIL" || formatDuration(0) != "0s" {
		t.Fatal("formatting boundary changed")
	}
	if got := formatInt64(-1234567); got != "-1,234,567" {
		t.Fatalf("formatInt64(-1234567) = %q", got)
	}
	if got := percentileDuration(nil, 0.99); got != 0 {
		t.Fatalf("percentileDuration(nil) = %v", got)
	}
}

func TestCommandAndExecutionFailuresUseStableExitClasses(t *testing.T) {
	var stdout, stderr bytes.Buffer
	cmd := NewCommand(command.Deps{Stdout: &stdout, Stderr: &stderr})
	cmd.SetArgs(nil)
	err := cmd.Execute()
	var exit command.Exit
	if !errors.As(err, &exit) || exit.Code != command.ExitConfig || !strings.Contains(stdout.String(), "Run WuKongIM benchmark helpers") {
		t.Fatalf("bench root = exit %+v, stdout %q, err %v", exit, stdout.String(), err)
	}
	if _, err := executeSendConfig(context.Background(), sendConfig{}); err == nil {
		t.Fatal("executeSendConfig(invalid) error = nil")
	}
}

func TestRunSendPropagatesPoolConstructionAndConnectFailures(t *testing.T) {
	original := newSendPool
	t.Cleanup(func() { newSendPool = original })
	wantErr := errors.New("pool unavailable")
	newSendPool = func(sendPoolConfig) (sendPool, error) { return nil, wantErr }
	cfg := validDirectSendConfig()
	if _, err := runSend(context.Background(), cfg); !errors.Is(err, wantErr) {
		t.Fatalf("runSend(pool error) = %v, want %v", err, wantErr)
	}
	connectErr := errors.New("connect failed")
	newSendPool = func(sendPoolConfig) (sendPool, error) { return &connectFailPool{err: connectErr}, nil }
	if _, err := runSend(context.Background(), cfg); !errors.Is(err, connectErr) {
		t.Fatalf("runSend(connect error) = %v, want %v", err, connectErr)
	}
}

func validDirectSendConfig() sendConfig {
	return sendConfig{
		GatewayAddrs: []string{"127.0.0.1:5100"}, Clients: 1, Messages: 1, Size: "1B", Channels: 1,
		Channel: "c1", ChannelType: "group", ChannelPick: channelPickRoundRobin,
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

type connectFailPool struct{ err error }

func (p *connectFailPool) Connect(context.Context, []wkclient.Identity) error { return p.err }
func (*connectFailPool) SendBatch(context.Context, []wkclient.RoutedMessage) ([]wkclient.SendResult, error) {
	return nil, errors.New("unexpected SendBatch")
}
func (*connectFailPool) Close() error { return nil }

func TestRandomPlannerDoesNotMutateChannelInventory(t *testing.T) {
	planner, err := newChannelPlanner(sendConfig{Channels: 2, ChannelPrefix: "g", ChannelTypeID: 2, ChannelPick: channelPickRandom, RandomSeed: 7})
	if err != nil {
		t.Fatal(err)
	}
	want := append([]string(nil), planner.ChannelIDs()...)
	for offset := 0; offset < 20; offset++ {
		_ = planner.Pick(offset)
	}
	if got := planner.ChannelIDs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("planner inventory mutated: got %v want %v", got, want)
	}
}
