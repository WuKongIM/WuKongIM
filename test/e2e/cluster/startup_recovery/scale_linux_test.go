//go:build e2e && linux

package startup_recovery

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	wkclient "github.com/WuKongIM/WuKongIM/pkg/client"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

// scaleFixture is a public-input receipt, never a decoded storage image.
type scaleFixture struct {
	Users         int            `json:"users"`
	HashSlots     int            `json:"hash_slots"`
	PhysicalSlots int            `json:"physical_slots"`
	Spec          suite.NodeSpec `json:"spec"`
	ImportSeconds float64        `json:"import_seconds"`
	// WriteSeconds excludes compaction and shutdown for paired write comparisons.
	WriteSeconds float64 `json:"write_seconds,omitempty"`
}

const scaleToken = "startup-scale-synthetic-token-001"

// TestStartupRecoveryScaleSeed creates a reusable, cleanly stopped fixture.
// Run in an isolated Linux container; copy the stopped fixture for every case.
func TestStartupRecoveryScaleSeed(t *testing.T) {
	root := os.Getenv("WK_E2E_STARTUP_SCALE_SEED")
	if root == "" {
		t.Skip("opt-in three-million-user fixture")
	}
	count := 3000000
	if value := os.Getenv("WK_E2E_STARTUP_SCALE_USERS"); value != "" {
		var err error
		count, err = strconv.Atoi(value)
		require.NoError(t, err)
		require.Positive(t, count)
	}
	_, err := os.Stat(filepath.Join(root, "fixture.json"))
	require.True(t, os.IsNotExist(err), "refuse to overwrite a published fixture")
	node := suite.New(t).StartSingleNodeCluster(suite.WithWorkspaceRootDir(root), suite.WithManagerHTTP(), suite.WithNodeConfigOverrides(1, map[string]string{
		"WK_CLUSTER_HASH_SLOT_COUNT": "256", "WK_CLUSTER_INITIAL_SLOT_COUNT": "1",
		"WK_CLUSTER_SLOT_LOG_COMPACTION_TRIGGER_ENTRIES": "100000",
		"WK_BENCH_API_ENABLE":                            "true", "WK_DEBUG_API_ENABLE": "true",
		"WK_LOG_FORMAT": "json", "WK_LOG_CONSOLE": "false",
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Minute)
	defer cancel()
	started := time.Now()
	var next, accepted atomic.Int64
	errors := make(chan error, 64)
	var wg sync.WaitGroup
	for worker := 0; worker < 64; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				first := int(next.Add(100) - 100)
				if first >= count || ctx.Err() != nil {
					return
				}
				end := min(first+100, count)
				items := make([]map[string]any, 0, end-first)
				for i := first; i < end; i++ {
					items = append(items, map[string]any{"uid": scaleUID(i), "token": scaleToken, "device_flag": 0, "device_level": 1})
				}
				var result struct {
					Accepted int `json:"accepted"`
				}
				_, err := suite.PostJSON(ctx, "http://"+node.APIAddr()+"/bench/v1/users/tokens", map[string]any{"run_id": "startup-scale", "batch_id": fmt.Sprint(first), "upsert": true, "users": items}, &result)
				if err != nil || result.Accepted != len(items) {
					errors <- fmt.Errorf("import batch %d failed: accepted=%d err=%v", first, result.Accepted, err)
					cancel()
					return
				}
				done := accepted.Add(int64(len(items)))
				if done%100000 == 0 {
					t.Logf("persisted users/devices=%d elapsed=%s", done, time.Since(started).Round(time.Second))
				}
			}
		}()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	require.EqualValues(t, count, accepted.Load())
	writeSeconds := time.Since(started).Seconds()
	var compact struct {
		Failed int `json:"failed"`
		Items  []struct {
			Success bool   `json:"success"`
			Index   uint64 `json:"after_snapshot_index"`
		} `json:"items"`
	}
	_, err = suite.PostJSON(ctx, "http://"+node.Spec.ManagerAddr+"/manager/nodes/1/slots/1/compact", nil, &compact)
	require.NoError(t, err)
	require.Zero(t, compact.Failed)
	require.Len(t, compact.Items, 1)
	require.True(t, compact.Items[0].Success)
	require.NotZero(t, compact.Items[0].Index)
	_, err = suite.PostJSON(ctx, "http://"+node.APIAddr()+"/user/token", map[string]any{"uid": "scale-after-snapshot", "token": scaleToken, "device_flag": 0, "device_level": 1}, nil)
	require.NoError(t, err)
	require.NoError(t, node.Stop())
	fixture := scaleFixture{Users: count, HashSlots: 256, PhysicalSlots: 1, Spec: node.Spec, ImportSeconds: time.Since(started).Seconds(), WriteSeconds: writeSeconds}
	data, err := json.MarshalIndent(fixture, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, "fixture.json"), data, 0600))
	t.Logf("published clean fixture: %s", filepath.Join(root, "fixture.json"))
}

// TestStartupRecoveryScaleRestart compares full product recovery and verifies
// snapshot and suffix credentials; the enclosing cgroup supplies the hard cap.
func TestStartupRecoveryScaleRestart(t *testing.T) {
	runScaleRestart(t, true)
}

// TestStartupRecoveryScaleProfile is attribution only, not an inventory gate.
// Run after the ordinary acceptance case against the same stopped fixture.
func TestStartupRecoveryScaleProfile(t *testing.T) {
	if os.Getenv("WK_E2E_STARTUP_CPU_PROFILE") == "" {
		t.Skip("requires diagnostic-only instrumented binary")
	}
	runScaleRestart(t, false)
}

func runScaleRestart(t *testing.T, inventory bool) {
	t.Helper()
	root := os.Getenv("WK_E2E_STARTUP_SCALE_FIXTURE")
	if root == "" {
		t.Skip("opt-in capped restart")
	}
	data, err := os.ReadFile(filepath.Join(root, "fixture.json"))
	require.NoError(t, err)
	var fixture scaleFixture
	require.NoError(t, json.Unmarshal(data, &fixture))
	require.Equal(t, 256, fixture.HashSlots)
	require.Positive(t, fixture.Users)
	output := os.Getenv("WK_E2E_STARTUP_SCALE_REPORT")
	require.NotEmpty(t, output)
	require.NoError(t, os.MkdirAll(filepath.Dir(output), 0755))
	fixture.Spec.Env = append(fixture.Spec.Env, "GODEBUG=gctrace=1")
	startupProfile := os.Getenv("WK_E2E_STARTUP_CPU_PROFILE")
	if startupProfile != "" {
		fixture.Spec.Env = append(fixture.Spec.Env, "STARTUP_DIAGNOSTIC_PROFILE="+startupProfile)
	}
	var interrupted map[string]any
	if os.Getenv("WK_E2E_STARTUP_SCALE_INTERRUPT") == "1" {
		interrupted = interruptScaleInstallation(t, fixture, filepath.Dir(output))
	}
	process := &suite.NodeProcess{Spec: fixture.Spec, BinaryPath: os.Getenv("WK_E2E_BINARY")}
	require.NotEmpty(t, process.BinaryPath)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	logPath := filepath.Join(fixture.Spec.LogDir, "app.log")
	logStat, err := os.Stat(logPath)
	require.NoError(t, err)
	started := time.Now()
	require.NoError(t, process.Start())
	t.Cleanup(func() { _ = process.Stop() })
	monitor, err := suite.ObserveLinuxProcess(process.Cmd.Process.Pid)
	require.NoError(t, err)
	_, readyErr := process.WaitHTTPReady(ctx, fixture.Spec.APIAddr, "/readyz")
	readyElapsed := time.Since(started)
	observed := monitor.Stop()
	var recoveryEvents []map[string]any
	if readyErr == nil {
		logFile, err := os.Open(logPath)
		require.NoError(t, err)
		logData, err := io.ReadAll(io.NewSectionReader(logFile, logStat.Size(), 4<<20))
		require.NoError(t, err)
		require.NoError(t, logFile.Close())
		stages := map[string]bool{}
		for _, line := range strings.Split(string(logData), "\n") {
			var row map[string]any
			if json.Unmarshal([]byte(line), &row) == nil && row["event"] == "slot.recovery.progress" {
				stage, _ := row["stage"].(string)
				stages[stage] = true
				recoveryEvents = append(recoveryEvents, row)
			}
		}
		if expected := os.Getenv("WK_E2E_STARTUP_EXPECT_STAGE"); expected != "" {
			require.True(t, stages[expected], "missing expected recovery stage %s: %v", expected, stages)
			if expected == "checkpoint_reuse" {
				require.False(t, stages["snapshot_install"], "certified restart rewrote snapshot data")
			}
		}
	}
	if startupProfile != "" && readyErr == nil {
		require.NoError(t, os.WriteFile(startupProfile+".stop", nil, 0600))
		require.Eventually(t, func() bool { _, err := os.Stat(startupProfile + ".done"); return err == nil }, 10*time.Second, 10*time.Millisecond)
		t.Logf("startup CPU profile: %s (instrumented init through observed readiness)", startupProfile)
	}
	require.Contains(t, []string{"2147483648", "4294967296"}, observed.MemoryMax, "run under the declared Linux hard memory cap")
	require.Equal(t, "0", observed.SwapMax, "swap must be disabled for comparable memory evidence")
	verified := 0
	verifiedRows := 0
	var authErr error
	var inventoryErr error
	if readyErr == nil {
		// Deterministic samples span the complete imported key range.
		for i := 0; i <= 256; i++ {
			uid := scaleUID(min(i*fixture.Users/256, fixture.Users-1))
			if i == 256 {
				uid = "scale-after-snapshot"
			}
			client, err := wkclient.New(wkclient.Config{Addr: fixture.Spec.GatewayAddr, OperationTimeout: 5 * time.Second})
			if err == nil {
				_, err = client.Connect(ctx, wkclient.ConnectOptions{UID: uid, Token: scaleToken, DeviceID: "scale-check", DeviceFlag: frame.APP})
				_ = client.Close()
			}
			if err != nil {
				authErr = fmt.Errorf("sample %d authentication failed", i)
				break
			}
			verified++
		}
		suite.CaptureRecoveryProfiles(ctx, "http://"+fixture.Spec.APIAddr, filepath.Dir(output))
		if inventory {
			inventoryCtx, inventoryCancel := context.WithTimeout(context.Background(), 30*time.Minute)
			verifiedRows, inventoryErr = verifyScaleInventory(inventoryCtx, fixture)
			inventoryCancel()
		}
	}
	report := map[string]any{"users": fixture.Users, "hash_slots": 256, "physical_slots": fixture.PhysicalSlots, "ready_ms": readyElapsed.Milliseconds(), "authenticated_samples": verified, "verified_user_device_rows": verifiedRows, "inventory_ok": inventoryErr == nil && verifiedRows == fixture.Users, "resources": observed, "ready": readyErr == nil, "credentials_ok": authErr == nil && verified == 257, "profile_scope": "after readiness; startup uses process counters and gctrace"}
	report["inventory_checked"] = inventory
	report["recovery_events"] = recoveryEvents
	if !inventory {
		report["purpose"] = "startup CPU attribution only; excludes full inventory acceptance"
	}
	if interrupted != nil {
		report["interrupted_installation"] = interrupted
	}
	if startupProfile != "" {
		report["startup_cpu_profile"] = startupProfile
	}
	encoded, err := json.MarshalIndent(report, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(output, encoded, 0600))
	t.Logf("restart report: %s", output)
	require.NoError(t, readyErr, process.DumpDiagnostics())
	require.NoError(t, authErr)
	require.NoError(t, inventoryErr)
	require.NoError(t, process.Stop())
}

// interruptScaleInstallation observes installation after the durable range
// deletion/pending marker, stops all threads and rejects completion before KILL.
// It never decodes or mutates database files and rejects a missed kill window.
func interruptScaleInstallation(t *testing.T, fixture scaleFixture, output string) map[string]any {
	t.Helper()
	file, err := os.Open(filepath.Join(fixture.Spec.LogDir, "app.log"))
	require.NoError(t, err)
	defer file.Close()
	_, err = file.Seek(0, io.SeekEnd)
	require.NoError(t, err)
	process := &suite.NodeProcess{Spec: fixture.Spec, BinaryPath: os.Getenv("WK_E2E_BINARY")}
	require.NoError(t, process.Start())
	t.Cleanup(func() { _ = process.Cmd.Process.Kill(); _ = process.Stop() })
	reader := bufio.NewReaderSize(file, 64<<10)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	var partial string
	var candidate map[string]any
	completed := false
	readEvents := func() {
		for {
			line, readErr := reader.ReadString('\n')
			partial += line
			require.LessOrEqual(t, len(partial), 64<<10, "bounded recovery log line")
			if strings.HasSuffix(partial, "\n") {
				var row map[string]any
				if json.Unmarshal([]byte(partial), &row) == nil && row["event"] == "slot.recovery.progress" {
					stage, _ := row["stage"].(string)
					if stage == "snapshot_installed" || stage == "complete" {
						completed = true
					}
					entries, _ := row["entries"].(float64)
					total, _ := row["totalEntries"].(float64)
					if stage == "snapshot_install" && entries < total {
						candidate = row
					}
				}
				partial = ""
			}
			if readErr == io.EOF {
				return
			}
			require.NoError(t, readErr)
		}
	}
	for candidate == nil {
		readEvents()
		require.False(t, completed, "installation finished before an incomplete install was observed")
		if candidate != nil {
			break
		}
		if err, exited := process.ExitResult(); exited {
			t.Fatalf("process exited before interruption: %v", err)
		}
		select {
		case <-ctx.Done():
			t.Fatal("no incomplete installation within bounded observation")
		case <-ticker.C:
		}
	}
	require.NoError(t, process.Cmd.Process.Signal(syscall.SIGSTOP))
	// A stopped status closes the race between the log observation and SIGSTOP.
	require.Eventually(t, func() bool {
		status, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", process.Cmd.Process.Pid))
		return err == nil && strings.Contains(string(status), "State:\tT")
	}, time.Second, time.Millisecond)
	readEvents()
	require.False(t, completed, "missed interruption window; refusing to claim crash recovery")
	require.NoError(t, process.Cmd.Process.Kill())
	require.NoError(t, process.Stop())
	report := map[string]any{"signal": "SIGKILL", "observed_installation_event": candidate, "durable_preparation_observed": true, "completed_before_kill": false}
	encoded, err := json.MarshalIndent(report, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(output, "interruption.json"), encoded, 0600))
	return report
}

func scaleUID(i int) string { return fmt.Sprintf("scale-%08d", i) }

// verifyScaleInventory walks public Manager pages and checks every imported UID
// exactly once with its device/token presence. The bitset is bounded by input
// cardinality (375 KiB for three million rows); it stores no credential values.
func verifyScaleInventory(ctx context.Context, fixture scaleFixture) (int, error) {
	seen := make([]byte, (fixture.Users+7)/8)
	count := 0
	cursor := ""
	suffix := false
	for page := 0; page < fixture.Users/200+1000; page++ {
		var response struct {
			Items []struct {
				UID           string `json:"uid"`
				DeviceCount   int    `json:"device_count"`
				TokenSetCount int    `json:"token_set_count"`
			} `json:"items"`
			HasMore    bool   `json:"has_more"`
			NextCursor string `json:"next_cursor"`
		}
		_, err := suite.GetJSON(ctx, "http://"+fixture.Spec.ManagerAddr+"/manager/users?limit=200&cursor="+url.QueryEscape(cursor), &response)
		if err != nil {
			return count, err
		}
		for _, item := range response.Items {
			if item.UID == "scale-after-snapshot" {
				suffix = item.DeviceCount == 1 && item.TokenSetCount == 1
				continue
			}
			if !strings.HasPrefix(item.UID, "scale-") {
				continue
			}
			index, err := strconv.Atoi(strings.TrimPrefix(item.UID, "scale-"))
			if err != nil || index < 0 || index >= fixture.Users || item.UID != scaleUID(index) {
				return count, fmt.Errorf("unexpected synthetic inventory identity")
			}
			if seen[index/8]&(1<<uint(index%8)) != 0 {
				return count, fmt.Errorf("duplicate inventory row at offset %d", index)
			}
			if item.DeviceCount != 1 || item.TokenSetCount != 1 {
				return count, fmt.Errorf("device/token presence mismatch at offset %d", index)
			}
			seen[index/8] |= 1 << uint(index%8)
			count++
		}
		if !response.HasMore {
			if count != fixture.Users || !suffix {
				return count, fmt.Errorf("incomplete inventory: imported=%d expected=%d suffix=%v", count, fixture.Users, suffix)
			}
			return count, nil
		}
		if response.NextCursor == "" || response.NextCursor == cursor {
			return count, fmt.Errorf("non-advancing inventory cursor")
		}
		cursor = response.NextCursor
	}
	return count, fmt.Errorf("inventory exceeded bounded page budget")
}
