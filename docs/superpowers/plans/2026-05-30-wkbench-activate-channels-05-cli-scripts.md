# wkbench Activate Channels 05 CLI Scripts Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Expose the activation runner as `wkbench capacity activate-channels`, add a local non-Compose three-node wrapper script, and document the evidence workflow.

**Architecture:** Keep the CLI as a thin parser/dispatcher around `internal/bench/capacity`. The local script starts or reuses the three-node `cmd/wukongimv2` cluster through `scripts/start-wukongimv2-three-nodes.sh`, builds a wkbench binary, runs `capacity activate-channels`, and writes evidence under `docs/development/perf-runs`. Documentation points users to the new activation script for simultaneous live-channel proof instead of using normal QPS capacity scripts for that purpose.

**Tech Stack:** Go stdlib `flag`, `cmd/wkbench`, Bash, existing `scripts/start-wukongimv2-three-nodes.sh`, `go test`, `bash -n`, `GOWORK=off`.

---

## Files

- Modify: `cmd/wkbench/main.go`
- Modify: `cmd/wkbench/main_test.go`
- Modify: `cmd/wkbench/README.md`
- Create: `scripts/activate-wukongimv2-three-nodes-10kch.sh`
- Create: `scripts/wukongimv2_three_node_activate_script_test.go`
- Modify: `docs/development/PERF_TRIAGE.md`

## Task 1: CLI Parser And Dispatch

**Files:**

- Modify: `cmd/wkbench/main.go`
- Modify: `cmd/wkbench/main_test.go`

- [ ] **Step 1: Add failing CLI tests**

Add tests:

```go
func TestRunCapacityUsageIncludesActivateChannels(t *testing.T) {
	var stderr bytes.Buffer
	code := runWithStderr([]string{"capacity"}, &stderr)
	if code != exitConfig {
		t.Fatalf("exit code = %d, want %d", code, exitConfig)
	}
	if !strings.Contains(stderr.String(), "wkbench capacity <send|hot-channel|activate-channels>") {
		t.Fatalf("usage missing activate-channels: %q", stderr.String())
	}
}

func TestRunCapacityActivateChannelsRequiresAPI(t *testing.T) {
	var stderr bytes.Buffer
	code := runWithStderr([]string{"capacity", "activate-channels"}, &stderr)
	if code != exitConfig {
		t.Fatalf("exit code = %d, want %d", code, exitConfig)
	}
	if !strings.Contains(stderr.String(), "--api is required") {
		t.Fatalf("expected api error, got %q", stderr.String())
	}
}

func TestParseCapacityActivateChannelsConfig(t *testing.T) {
	var stderr bytes.Buffer
	cfg, code := parseCapacityActivateChannelsConfig([]string{
		"--api", "http://127.0.0.1:5011,http://127.0.0.1:5012",
		"--gateway", "127.0.0.1:5111,127.0.0.1:5112",
		"--run-id", "activate-a",
		"--channels", "10000",
		"--users", "20000",
		"--members", "10",
		"--activation-concurrency", "2000",
		"--activation-window", "10s",
		"--hold", "1m",
		"--hold-probe-interval", "5s",
		"--probe-batch-size", "500",
		"--stable-p99", "300ms",
		"--evict-after",
		"--report-dir", "./tmp/activate",
	}, &stderr)
	if code != 0 {
		t.Fatalf("parse code = %d stderr=%q", code, stderr.String())
	}
	if strings.Join(cfg.APIAddrs, ",") != "http://127.0.0.1:5011,http://127.0.0.1:5012" {
		t.Fatalf("api addrs = %#v", cfg.APIAddrs)
	}
	if strings.Join(cfg.GatewayTCPAddrs, ",") != "127.0.0.1:5111,127.0.0.1:5112" {
		t.Fatalf("gateway addrs = %#v", cfg.GatewayTCPAddrs)
	}
	if cfg.RunID != "activate-a" || cfg.Channels != 10000 || cfg.Users != 20000 || cfg.GroupMembers != 10 {
		t.Fatalf("parsed config = %#v", cfg)
	}
	if cfg.ActivationConcurrency != 2000 || cfg.ActivationWindow != 10*time.Second || cfg.Hold != time.Minute {
		t.Fatalf("activation config = %#v", cfg)
	}
	if cfg.HoldProbeInterval != 5*time.Second || cfg.ProbeBatchSize != 500 || cfg.StableP99 != 300*time.Millisecond {
		t.Fatalf("probe/latency config = %#v", cfg)
	}
	if !cfg.EvictAfter {
		t.Fatalf("evict-after = false, want true")
	}
}
```

- [ ] **Step 2: Run failing tests**

```bash
GOWORK=off go test ./cmd/wkbench -run 'TestRunCapacity|TestParseCapacityActivateChannels' -count=1
```

Expected:

```text
FAIL
... activate-channels ...
```

- [ ] **Step 3: Add dispatch**

In `runCapacity`, change usage to:

```go
const capacityUsage = "usage: wkbench capacity <send|hot-channel|activate-channels>"
```

Add:

```go
case "activate-channels":
	return runCapacityActivateChannels(args[1:], stderr)
```

Implement:

```go
func runCapacityActivateChannels(args []string, stderr io.Writer) int {
	cfg, code := parseCapacityActivateChannelsConfig(args, stderr)
	if code != 0 {
		return code
	}
	discovered, err := capacity.DiscoverTarget(context.Background(), capacity.Config{
		APIAddrs:        cfg.APIAddrs,
		GatewayTCPAddrs: cfg.GatewayTCPAddrs,
		BenchToken:      cfg.BenchToken,
		Profile:         capacity.ProfileGroup,
		StartQPS:        1,
		MaxQPS:          1,
		StepFactor:      2,
		Duration:        cfg.ActivationWindow,
		Warmup:          time.Second,
		Cooldown:        0,
		StableP99:       cfg.StableP99,
		GroupMembers:    cfg.GroupMembers,
		ReportDir:       cfg.ReportDir,
	})
	if err != nil {
		fmt.Fprintf(stderr, "capacity preflight failed: %v\n", err)
		return exitPreflight
	}
	result, err := capacity.NewActivateChannelsRunner(cfg, discovered).Run(context.Background())
	if writeErr := capacity.WriteActivateChannelsResult(result.ReportDir, result); writeErr != nil {
		fmt.Fprintf(stderr, "activate-channels report write failed: %v\n", writeErr)
		return exitInternal
	}
	fmt.Fprint(stderr, capacity.ActivateChannelsConsoleSummary(result))
	if err != nil {
		fmt.Fprintf(stderr, "activate-channels run failed: %v\n", err)
		return exitWorker
	}
	return result.ExitCode()
}
```

Add `time` to imports.

- [ ] **Step 4: Add parser**

Add:

```go
func parseCapacityActivateChannelsConfig(args []string, stderr io.Writer) (capacity.ActivateChannelsConfig, int) {
	cfg := capacity.DefaultActivateChannelsConfig()
	fs := flag.NewFlagSet("capacity activate-channels", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var apiCSV string
	var gatewayCSV string
	fs.StringVar(&apiCSV, "api", "", "comma-separated target HTTP API base addresses")
	fs.StringVar(&gatewayCSV, "gateway", "", "optional comma-separated WKProto TCP gateway addresses")
	fs.StringVar(&cfg.BenchToken, "bench-token", cfg.BenchToken, "optional bearer token for bench API routes")
	fs.StringVar(&cfg.RunID, "run-id", cfg.RunID, "stable benchmark run id used for generated channel ids")
	fs.IntVar(&cfg.Channels, "channels", cfg.Channels, "number of group channels to activate")
	fs.IntVar(&cfg.Users, "users", cfg.Users, "online user pool size")
	fs.IntVar(&cfg.GroupMembers, "members", cfg.GroupMembers, "members per generated group channel")
	fs.IntVar(&cfg.ActivationConcurrency, "activation-concurrency", cfg.ActivationConcurrency, "maximum concurrent activation sends")
	fs.DurationVar(&cfg.ActivationWindow, "activation-window", cfg.ActivationWindow, "time window used to schedule one SEND per channel")
	fs.DurationVar(&cfg.Hold, "hold", cfg.Hold, "duration to hold activated channels without new sends")
	fs.DurationVar(&cfg.HoldProbeInterval, "hold-probe-interval", cfg.HoldProbeInterval, "snapshot interval during hold")
	fs.IntVar(&cfg.ProbeBatchSize, "probe-batch-size", cfg.ProbeBatchSize, "number of generated channels per runtime probe request")
	fs.DurationVar(&cfg.StableP99, "stable-p99", cfg.StableP99, "maximum allowed activation sendack p99 latency")
	fs.Float64Var(&cfg.MaxSendackErrorRate, "max-sendack-error-rate", cfg.MaxSendackErrorRate, "maximum allowed sendack error rate")
	fs.Float64Var(&cfg.MaxConnectErrorRate, "max-connect-error-rate", cfg.MaxConnectErrorRate, "maximum allowed connect error rate")
	fs.BoolVar(&cfg.EvictAfter, "evict-after", cfg.EvictAfter, "evict safe selected runtime state after final probe")
	fs.StringVar(&cfg.ReportDir, "report-dir", cfg.ReportDir, "activation report output directory")
	if err := fs.Parse(args); err != nil {
		return capacity.ActivateChannelsConfig{}, exitConfig
	}
	cfg.APIAddrs = splitCSV(apiCSV)
	cfg.GatewayTCPAddrs = splitCSV(gatewayCSV)
	if len(cfg.APIAddrs) == 0 {
		fmt.Fprintln(stderr, "--api is required")
		return capacity.ActivateChannelsConfig{}, exitConfig
	}
	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(stderr, "config validation failed: %v\n", err)
		return capacity.ActivateChannelsConfig{}, exitConfig
	}
	return cfg, 0
}
```

- [ ] **Step 5: Verify**

```bash
GOWORK=off go test ./cmd/wkbench -run 'TestRunCapacity|TestParseCapacityActivateChannels' -count=1
```

Expected:

```text
ok  	github.com/WuKongIM/WuKongIM/cmd/wkbench
```

## Task 2: CLI Documentation

**Files:**

- Modify: `cmd/wkbench/README.md`

- [ ] **Step 1: Document command and target routes**

Add `capacity activate-channels` to the command table:

```markdown
| `capacity activate-channels` | Activates a fixed number of group channels through real SEND traffic, holds them live, probes ChannelV2 runtime state, and writes activation evidence. |
```

Extend target requirements:

```markdown
- `GET /bench/v1/channel-runtime/snapshot`
- `POST /bench/v1/channel-runtime/probe`
- `POST /bench/v1/channel-runtime/evict` when `--evict-after` is used
```

Add section:

```markdown
## Capacity Activate Channels

`capacity activate-channels` is the preferred proof for simultaneous live ChannelV2 channel capacity. It prepares group data through the bench API, sends exactly one WKProto group SEND per generated channel during the activation window, then holds and probes runtime state through bench-only runtime endpoints.

```bash
wkbench capacity activate-channels \
  --api http://127.0.0.1:5011,http://127.0.0.1:5012,http://127.0.0.1:5013 \
  --gateway 127.0.0.1:5111,127.0.0.1:5112,127.0.0.1:5113 \
  --channels 10000 \
  --users 20000 \
  --members 10 \
  --activation-concurrency 2000 \
  --activation-window 10s \
  --hold 60s \
  --report-dir ./tmp/wkbench-activate-channels
```

The command fails when activation success is not equal to channel count, activation errors occur, ChannelV2 activation rejects increase, active leaders are missing, hold probes lose channels, or activation sendack p99 exceeds `--stable-p99`.
```

- [ ] **Step 2: Verify docs**

```bash
rg -n "capacity activate-channels|channel-runtime/snapshot|Capacity Activate Channels" cmd/wkbench/README.md
```

Expected: all three patterns match.

## Task 3: Local Three-Node Activation Script

**Files:**

- Create: `scripts/activate-wukongimv2-three-nodes-10kch.sh`
- Create: `scripts/wukongimv2_three_node_activate_script_test.go`

- [ ] **Step 1: Add static script tests**

Add:

```go
func TestWukongIMV2ThreeNodeActivateScriptUsesLocalStartupAndCapacityCommand(t *testing.T) {
	root := repoRoot(t)
	script := readFile(t, filepath.Join(root, "scripts", "activate-wukongimv2-three-nodes-10kch.sh"))
	for _, want := range []string{
		"scripts/start-wukongimv2-three-nodes.sh",
		"capacity activate-channels",
		"--activation-concurrency",
		"--activation-window",
		"--probe-batch-size",
		"WK_BENCH_CHANNELS:-10000",
		"WK_BENCH_USERS:-20000",
		"WK_BENCH_GROUP_MEMBERS:-10",
		"activation_report.json",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("activate script missing %q", want)
		}
	}
	if strings.Contains(script, "docker compose") {
		t.Fatalf("activate script must use local startup script, not docker compose")
	}
}
```

Add a syntax smoke test:

```go
func TestWukongIMV2ThreeNodeActivateScriptBashSyntax(t *testing.T) {
	root := repoRoot(t)
	cmd := exec.Command("bash", "-n", "scripts/activate-wukongimv2-three-nodes-10kch.sh")
	cmd.Dir = root
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bash -n failed: %v\n%s", err, output)
	}
}
```

- [ ] **Step 2: Create script**

Script responsibilities:

- Defaults:
  - `WK_BENCH_CHANNELS:-10000`
  - `WK_BENCH_USERS:-20000`
  - `WK_BENCH_GROUP_MEMBERS:-10`
  - `WK_BENCH_CONCURRENCY:-2000`
  - `WK_BENCH_ACTIVATION_WINDOW:-10s`
  - `WK_BENCH_HOLD:-60s`
  - `WK_BENCH_PROBE_BATCH_SIZE:-1000`
  - `WK_BENCH_STABLE_P99:-400ms`
  - `WK_BENCH_API_ADDRS:-http://127.0.0.1:5011,http://127.0.0.1:5012,http://127.0.0.1:5013`
  - `WK_BENCH_GATEWAY_ADDRS:-127.0.0.1:5111,127.0.0.1:5112,127.0.0.1:5113`
- Options:
  - `--out-dir`
  - `--wkbench-bin`
  - `--no-start`
  - `--no-clean`
  - `--start-script`
  - `--ready-timeout`
  - `--api`
  - `--gateway`
  - `--channels`
  - `--users`
  - `--members`
  - `--activation-concurrency`
  - `--activation-window`
  - `--hold`
  - `--probe-batch-size`
  - `--stable-p99`
  - `--evict-after`
- Uses `go build -o "$WK_BENCH_BIN" ./cmd/wkbench`.
- Starts the cluster with:

```bash
"$START_SCRIPT" --ready-timeout "$READY_TIMEOUT" "${clean_arg[@]}" >"$OUT_DIR/logs/cluster-start.log" 2>&1 &
```

- Runs:

```bash
"$WK_BENCH_BIN" capacity activate-channels \
  --api "$API_ADDRS" \
  --gateway "$GATEWAY_ADDRS" \
  --channels "$CHANNELS" \
  --users "$USERS" \
  --members "$GROUP_MEMBERS" \
  --activation-concurrency "$CONCURRENCY" \
  --activation-window "$ACTIVATION_WINDOW" \
  --hold "$HOLD" \
  --probe-batch-size "$PROBE_BATCH_SIZE" \
  --stable-p99 "$STABLE_P99" \
  --report-dir "$OUT_DIR/report" \
  "${evict_arg[@]}"
```

- Writes:
  - `env.txt`
  - `git.txt`
  - `logs/cluster-start.log`
  - `report/activation_report.json`
  - `report/summary.md`

- [ ] **Step 3: Verify script tests**

```bash
GOWORK=off go test ./scripts -run 'TestWukongIMV2ThreeNodeActivate' -count=1
```

Expected:

```text
ok  	github.com/WuKongIM/WuKongIM/scripts
```

## Task 4: Performance Triage Docs

**Files:**

- Modify: `docs/development/PERF_TRIAGE.md`

- [ ] **Step 1: Point 10k live-channel proof to activation script**

Replace the local ChannelV2 10k paragraph with:

```markdown
For `cmd/wukongimv2` local three-node ChannelV2 simultaneous live-channel runs, do not use the Compose dev-sim path. Use the local activation wrapper:

```bash
scripts/activate-wukongimv2-three-nodes-10kch.sh
```

This wrapper starts nodes through `scripts/start-wukongimv2-three-nodes.sh`, defaults to 10,000 group channels, runs `wkbench capacity activate-channels`, and stores evidence under `docs/development/perf-runs/<timestamp>-three-node-activate-10kch/`. The primary pass/fail artifact is `report/activation_report.json`; `report/summary.md` lists activation success, send errors, sendack p99, active leaders, activation rejection delta, and probe misses.
```

- [ ] **Step 2: Verify docs**

```bash
rg -n "activate-wukongimv2-three-nodes-10kch|activation_report.json|capacity activate-channels" docs/development/PERF_TRIAGE.md
```

Expected: all three patterns match.

## Task 5: Full CLI And Script Verification

**Files:**

- Modify: files from Tasks 1-4

- [ ] **Step 1: Run focused tests**

```bash
GOWORK=off go test ./cmd/wkbench ./scripts -count=1
```

Expected:

```text
ok  	github.com/WuKongIM/WuKongIM/cmd/wkbench
ok  	github.com/WuKongIM/WuKongIM/scripts
```

- [ ] **Step 2: Run script syntax check**

```bash
bash -n scripts/activate-wukongimv2-three-nodes-10kch.sh
```

Expected: exits 0.

- [ ] **Step 3: Commit**

```bash
git add cmd/wkbench/main.go cmd/wkbench/main_test.go cmd/wkbench/README.md scripts/activate-wukongimv2-three-nodes-10kch.sh scripts/wukongimv2_three_node_activate_script_test.go docs/development/PERF_TRIAGE.md
git commit -m "feat(wkbench): expose activate channels scenario"
```
