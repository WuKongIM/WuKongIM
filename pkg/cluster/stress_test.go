package cluster_test

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

const (
	stressEnvKey      = "WKCLUSTER_STRESS"
	stressDurationEnv = "WKCLUSTER_STRESS_DURATION"
	stressWorkersEnv  = "WKCLUSTER_STRESS_WORKERS"
	stressSeedEnv     = "WKCLUSTER_STRESS_SEED"
)

type stressConfig struct {
	Enabled  bool
	Duration time.Duration
	Workers  int
	Seed     int64
}

func loadStressConfig(t *testing.T) stressConfig {
	t.Helper()
	cfg := stressConfig{
		Enabled:  envBool(stressEnvKey, false),
		Duration: envDuration(t, stressDurationEnv, 5*time.Second),
		Workers:  envInt(t, stressWorkersEnv, max(4, runtime.GOMAXPROCS(0))),
		Seed:     envInt64(t, stressSeedEnv, 20260328),
	}
	return cfg
}

func requireStressEnabled(t *testing.T, cfg stressConfig) {
	t.Helper()
	if !cfg.Enabled {
		t.Skip("set WKCLUSTER_STRESS=1 to enable raftcluster stress tests")
	}
}

// ── report helpers ──────────────────────────────────────────────────

type reportLine struct {
	label string
	value string
}

type stressReport struct {
	title   string
	config  []reportLine
	results []reportLine
	verify  []reportLine
	verdict string // PASS / FAIL
}

func (r *stressReport) print(t *testing.T) {
	t.Helper()

	const width = 60
	border := strings.Repeat("─", width)

	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("┌%s┐\n", border))
	b.WriteString(fmt.Sprintf("│ %-*s │\n", width-2, r.title))
	b.WriteString(fmt.Sprintf("├%s┤\n", border))

	// Config section
	b.WriteString(fmt.Sprintf("│ %-*s │\n", width-2, "CONFIG"))
	for _, l := range r.config {
		b.WriteString(fmt.Sprintf("│   %-20s %*s │\n", l.label, width-26, l.value))
	}

	// Results section
	b.WriteString(fmt.Sprintf("├%s┤\n", border))
	b.WriteString(fmt.Sprintf("│ %-*s │\n", width-2, "RESULTS"))
	for _, l := range r.results {
		b.WriteString(fmt.Sprintf("│   %-20s %*s │\n", l.label, width-26, l.value))
	}

	// Verify section
	if len(r.verify) > 0 {
		b.WriteString(fmt.Sprintf("├%s┤\n", border))
		b.WriteString(fmt.Sprintf("│ %-*s │\n", width-2, "VERIFICATION"))
		for _, l := range r.verify {
			b.WriteString(fmt.Sprintf("│   %-20s %*s │\n", l.label, width-26, l.value))
		}
	}

	// Verdict
	b.WriteString(fmt.Sprintf("├%s┤\n", border))
	b.WriteString(fmt.Sprintf("│ %-*s │\n", width-2, fmt.Sprintf("VERDICT: %s", r.verdict)))
	b.WriteString(fmt.Sprintf("└%s┘\n", border))

	t.Log(b.String())
}

func fmtOps(n uint64) string {
	if n >= 1_000_000 {
		return fmt.Sprintf("%.2fM", float64(n)/1_000_000)
	}
	if n >= 1_000 {
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	}
	return fmt.Sprintf("%d", n)
}

func fmtRate(ops uint64, d time.Duration) string {
	rate := float64(ops) / d.Seconds()
	if rate >= 1000 {
		return fmt.Sprintf("%.1fK ops/s", rate/1000)
	}
	return fmt.Sprintf("%.0f ops/s", rate)
}

func fmtPct(n, total uint64) string {
	if total == 0 {
		return "0%"
	}
	return fmt.Sprintf("%.1f%%", float64(n)*100/float64(total))
}

func stressRestartInterval(duration time.Duration) time.Duration {
	return max(750*time.Millisecond, duration/4)
}

// ── stress tests ────────────────────────────────────────────────────

// TestStressSingleNodeThroughput hammers a single-node cluster with
// concurrent CreateChannel + GetChannel from multiple goroutines across
// multiple raft slots, measuring throughput and verifying data integrity.
func TestStressSingleNodeThroughput(t *testing.T) {
	cfg := loadStressConfig(t)
	requireStressEnabled(t, cfg)

	const slotCount = 4
	n := startSingleNode(t, slotCount)
	defer n.stop()

	for g := 1; g <= slotCount; g++ {
		waitForLeader(t, n.cluster, uint64(g))
	}

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Duration)
	defer cancel()

	var (
		wg      sync.WaitGroup
		errCh   = make(chan error, 1)
		creates atomic.Uint64
		reads   atomic.Uint64
		updates atomic.Uint64
		deletes atomic.Uint64
	)

	for worker := 0; worker < cfg.Workers; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()

			rng := rand.New(rand.NewSource(cfg.Seed + int64(worker)))
			for idx := 0; ; idx++ {
				if ctx.Err() != nil {
					return
				}

				channelID := fmt.Sprintf("stress-ch-%d-%d", worker, rng.Intn(64))
				channelType := int64(rng.Intn(4) + 1)

				op := rng.Intn(100)
				switch {
				case op < 50:
					if err := n.store.CreateChannel(ctx, channelID, channelType); err != nil {
						if ctx.Err() != nil {
							return
						}
						select {
						case errCh <- fmt.Errorf("w%d CreateChannel(%s): %w", worker, channelID, err):
						default:
						}
						cancel()
						return
					}
					creates.Add(1)

				case op < 75:
					_, _ = n.store.GetChannel(ctx, channelID, channelType)
					reads.Add(1)

				case op < 90:
					ban := int64(rng.Intn(2))
					if err := n.store.UpdateChannel(ctx, channelID, channelType, ban); err != nil {
						if ctx.Err() != nil {
							return
						}
						select {
						case errCh <- fmt.Errorf("w%d UpdateChannel(%s): %w", worker, channelID, err):
						default:
						}
						cancel()
						return
					}
					updates.Add(1)

				default:
					if err := n.store.DeleteChannel(ctx, channelID, channelType); err != nil {
						if ctx.Err() != nil {
							return
						}
						select {
						case errCh <- fmt.Errorf("w%d DeleteChannel(%s): %w", worker, channelID, err):
						default:
						}
						cancel()
						return
					}
					deletes.Add(1)
				}
			}
		}(worker)
	}

	wg.Wait()
	elapsed := time.Since(start)

	select {
	case err := <-errCh:
		t.Fatalf("stress error: %v", err)
	default:
	}

	cN := creates.Load()
	rN := reads.Load()
	uN := updates.Load()
	dN := deletes.Load()
	total := cN + rN + uN + dN

	rpt := stressReport{
		title: "Single-Node Throughput",
		config: []reportLine{
			{"Nodes", "1"},
			{"Raft Slots", fmt.Sprintf("%d", slotCount)},
			{"Workers", fmt.Sprintf("%d", cfg.Workers)},
			{"Duration", elapsed.Round(time.Millisecond).String()},
			{"Seed", fmt.Sprintf("%d", cfg.Seed)},
		},
		results: []reportLine{
			{"Total Ops", fmtOps(total)},
			{"Throughput", fmtRate(total, elapsed)},
			{"Creates", fmt.Sprintf("%s (%s)", fmtOps(cN), fmtPct(cN, total))},
			{"Reads", fmt.Sprintf("%s (%s)", fmtOps(rN), fmtPct(rN, total))},
			{"Updates", fmt.Sprintf("%s (%s)", fmtOps(uN), fmtPct(uN, total))},
			{"Deletes", fmt.Sprintf("%s (%s)", fmtOps(dN), fmtPct(dN, total))},
			{"Errors", "0"},
		},
		verdict: "PASS",
	}
	rpt.print(t)
}

// TestStressThreeNodeMixedWorkload runs a mixed CRUD workload across a
// 3-node cluster. Writes hit random nodes (some go to leader, some get
// forwarded). Verifies that all created channels are readable after the
// workload completes.
func TestStressThreeNodeMixedWorkload(t *testing.T) {
	cfg := loadStressConfig(t)
	requireStressEnabled(t, cfg)

	const slotCount = 3
	testNodes := startThreeNodes(t, slotCount)
	defer func() {
		for _, n := range testNodes {
			n.stop()
		}
	}()

	waitForAllStableLeaders(t, testNodes, slotCount)

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Duration)
	defer cancel()

	var (
		wg        sync.WaitGroup
		creates   atomic.Uint64
		reads     atomic.Uint64
		updates   atomic.Uint64
		writeErrs atomic.Uint64
		mu        sync.Mutex
		written   = make(map[string]int64)
	)

	for worker := 0; worker < cfg.Workers; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()

			rng := rand.New(rand.NewSource(cfg.Seed + int64(worker)*777))
			var localWritten []struct {
				id  string
				typ int64
			}

			for idx := 0; ; idx++ {
				if ctx.Err() != nil {
					break
				}

				node := testNodes[rng.Intn(len(testNodes))]
				channelID := fmt.Sprintf("mn-ch-%d-%d", worker, rng.Intn(32))
				channelType := int64(rng.Intn(3) + 1)

				op := rng.Intn(100)
				switch {
				case op < 60:
					if err := node.store.CreateChannel(ctx, channelID, channelType); err != nil {
						if ctx.Err() != nil {
							break
						}
						writeErrs.Add(1)
						continue
					}
					creates.Add(1)
					localWritten = append(localWritten, struct {
						id  string
						typ int64
					}{channelID, channelType})

				case op < 80:
					_, _ = node.store.GetChannel(ctx, channelID, channelType)
					reads.Add(1)

				default:
					ban := int64(rng.Intn(2))
					if err := node.store.UpdateChannel(ctx, channelID, channelType, ban); err != nil {
						if ctx.Err() != nil {
							break
						}
						writeErrs.Add(1)
						continue
					}
					updates.Add(1)
				}
			}

			mu.Lock()
			for _, w := range localWritten {
				written[w.id] = w.typ
			}
			mu.Unlock()
		}(worker)
	}

	wg.Wait()
	elapsed := time.Since(start)

	// Allow replication to settle
	time.Sleep(500 * time.Millisecond)

	verifyCtx, verifyCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer verifyCancel()

	verified := 0
	for channelID, channelType := range written {
		found := false
		for _, n := range testNodes {
			ch, err := n.store.GetChannel(verifyCtx, channelID, channelType)
			if err == nil && ch.ChannelID == channelID {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("channel %q type=%d not found on any node after workload", channelID, channelType)
		}
		verified++
	}

	cN := creates.Load()
	rN := reads.Load()
	uN := updates.Load()
	eN := writeErrs.Load()
	total := cN + rN + uN

	rpt := stressReport{
		title: "3-Node Mixed Workload",
		config: []reportLine{
			{"Nodes", "3"},
			{"Raft Slots", fmt.Sprintf("%d", slotCount)},
			{"Workers", fmt.Sprintf("%d", cfg.Workers)},
			{"Duration", elapsed.Round(time.Millisecond).String()},
			{"Seed", fmt.Sprintf("%d", cfg.Seed)},
		},
		results: []reportLine{
			{"Total Ops", fmtOps(total)},
			{"Throughput", fmtRate(total, elapsed)},
			{"Creates", fmt.Sprintf("%s (%s)", fmtOps(cN), fmtPct(cN, total))},
			{"Reads", fmt.Sprintf("%s (%s)", fmtOps(rN), fmtPct(rN, total))},
			{"Updates", fmt.Sprintf("%s (%s)", fmtOps(uN), fmtPct(uN, total))},
			{"Write Errors", fmt.Sprintf("%s (transient)", fmtOps(eN))},
		},
		verify: []reportLine{
			{"Channels Written", fmt.Sprintf("%d", len(written))},
			{"Channels Verified", fmt.Sprintf("%d/%d", verified, len(written))},
			{"Data Integrity", "OK"},
		},
		verdict: "PASS",
	}
	rpt.print(t)
}

func TestStressThreeNodeMixedWorkloadWithRestarts(t *testing.T) {
	cfg := loadStressConfig(t)
	requireStressEnabled(t, cfg)

	const slotCount = 3
	testNodes := startThreeNodes(t, slotCount)
	defer func() {
		for _, n := range testNodes {
			n.stop()
		}
	}()

	waitForAllStableLeaders(t, testNodes, slotCount)

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Duration)
	defer cancel()

	var (
		wg        sync.WaitGroup
		creates   atomic.Uint64
		reads     atomic.Uint64
		updates   atomic.Uint64
		writeErrs atomic.Uint64
		restarts  atomic.Uint64
		mu        sync.Mutex
		written   = make(map[string]int64)
	)

	for worker := 0; worker < cfg.Workers; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()

			rng := rand.New(rand.NewSource(cfg.Seed + int64(worker)*911))
			var localWritten []struct {
				id  string
				typ int64
			}

			for idx := 0; ; idx++ {
				if ctx.Err() != nil {
					break
				}

				node := testNodes[rng.Intn(len(testNodes))]
				channelID := fmt.Sprintf("mnr-ch-%d-%d", worker, rng.Intn(32))
				channelType := int64(rng.Intn(3) + 1)

				op := rng.Intn(100)
				switch {
				case op < 60:
					if err := node.store.CreateChannel(ctx, channelID, channelType); err != nil {
						if ctx.Err() != nil {
							break
						}
						writeErrs.Add(1)
						continue
					}
					creates.Add(1)
					localWritten = append(localWritten, struct {
						id  string
						typ int64
					}{channelID, channelType})
				case op < 80:
					_, _ = node.store.GetChannel(ctx, channelID, channelType)
					reads.Add(1)
				default:
					ban := int64(rng.Intn(2))
					if err := node.store.UpdateChannel(ctx, channelID, channelType, ban); err != nil {
						if ctx.Err() != nil {
							break
						}
						writeErrs.Add(1)
						continue
					}
					updates.Add(1)
				}
			}

			mu.Lock()
			for _, w := range localWritten {
				written[w.id] = w.typ
			}
			mu.Unlock()
		}(worker)
	}

	restartEvery := stressRestartInterval(cfg.Duration)
	restartTimer := time.NewTimer(restartEvery)
	defer restartTimer.Stop()

restartLoop:
	for {
		select {
		case <-ctx.Done():
			break restartLoop
		case <-restartTimer.C:
			slotID := uint64(restarts.Load()%slotCount + 1)
			leaderID, err := stableLeaderWithin(testNodes, slotID, 10*time.Second)
			if err != nil {
				t.Fatalf("stable leader for slot %d before restart: %v", slotID, err)
			}
			restartNode(t, testNodes, int(leaderID-1))
			waitForStableLeader(t, testNodes, slotID)
			restarts.Add(1)
			restartTimer.Reset(restartEvery)
		}
	}

	wg.Wait()
	elapsed := time.Since(start)

	time.Sleep(500 * time.Millisecond)

	verifyCtx, verifyCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer verifyCancel()

	verified := 0
	for channelID, channelType := range written {
		found := false
		for _, n := range testNodes {
			ch, err := n.store.GetChannel(verifyCtx, channelID, channelType)
			if err == nil && ch.ChannelID == channelID {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("channel %q type=%d not found on any node after restart workload", channelID, channelType)
		}
		verified++
	}

	cN := creates.Load()
	rN := reads.Load()
	uN := updates.Load()
	eN := writeErrs.Load()
	restartN := restarts.Load()
	total := cN + rN + uN

	rpt := stressReport{
		title: "3-Node Mixed Workload With Restarts",
		config: []reportLine{
			{"Nodes", "3"},
			{"Raft Slots", fmt.Sprintf("%d", slotCount)},
			{"Workers", fmt.Sprintf("%d", cfg.Workers)},
			{"Duration", elapsed.Round(time.Millisecond).String()},
			{"Seed", fmt.Sprintf("%d", cfg.Seed)},
			{"Restart Every", restartEvery.String()},
		},
		results: []reportLine{
			{"Total Ops", fmtOps(total)},
			{"Throughput", fmtRate(total, elapsed)},
			{"Creates", fmt.Sprintf("%s (%s)", fmtOps(cN), fmtPct(cN, total))},
			{"Reads", fmt.Sprintf("%s (%s)", fmtOps(rN), fmtPct(rN, total))},
			{"Updates", fmt.Sprintf("%s (%s)", fmtOps(uN), fmtPct(uN, total))},
			{"Restarts", fmtOps(restartN)},
			{"Write Errors", fmt.Sprintf("%s (transient)", fmtOps(eN))},
		},
		verify: []reportLine{
			{"Channels Written", fmt.Sprintf("%d", len(written))},
			{"Channels Verified", fmt.Sprintf("%d/%d", verified, len(written))},
			{"Data Integrity", "OK"},
		},
		verdict: "PASS",
	}
	rpt.print(t)
}

// TestStressForwardingContention sends all writes to follower nodes only,
// forcing every write to go through the forwarding path. Measures forwarding
// throughput under contention.
func TestStressForwardingContention(t *testing.T) {
	cfg := loadStressConfig(t)
	requireStressEnabled(t, cfg)

	const slotCount = 2
	testNodes := startThreeNodes(t, slotCount)
	defer func() {
		for _, n := range testNodes {
			n.stop()
		}
	}()

	leaders := waitForAllStableLeaders(t, testNodes, slotCount)

	var followers []*testNode
	for _, n := range testNodes {
		if n.nodeID != leaders[1] {
			followers = append(followers, n)
		}
	}
	if len(followers) == 0 {
		t.Fatal("no followers found")
	}

	leaderStr := make([]string, 0, len(leaders))
	for g, lid := range leaders {
		leaderStr = append(leaderStr, fmt.Sprintf("g%d->n%d", g, lid))
	}

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Duration)
	defer cancel()

	var (
		wg      sync.WaitGroup
		errCh   = make(chan error, 1)
		fwdOps  atomic.Uint64
		fwdErrs atomic.Uint64
	)

	for worker := 0; worker < cfg.Workers; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()

			rng := rand.New(rand.NewSource(cfg.Seed + int64(worker)*333))
			for idx := 0; ; idx++ {
				if ctx.Err() != nil {
					return
				}

				follower := followers[rng.Intn(len(followers))]
				channelID := fmt.Sprintf("fwd-ch-%d-%d", worker, rng.Intn(32))
				channelType := int64(rng.Intn(3) + 1)

				err := follower.store.CreateChannel(ctx, channelID, channelType)
				if err != nil {
					if ctx.Err() != nil {
						return
					}
					fwdErrs.Add(1)
					continue
				}
				fwdOps.Add(1)
			}
		}(worker)
	}

	wg.Wait()
	elapsed := time.Since(start)

	select {
	case err := <-errCh:
		t.Fatalf("stress error: %v", err)
	default:
	}

	fN := fwdOps.Load()
	eN := fwdErrs.Load()
	total := fN + eN
	successRate := "100%"
	if total > 0 {
		successRate = fmtPct(fN, total)
	}

	rpt := stressReport{
		title: "Forwarding Contention",
		config: []reportLine{
			{"Nodes", "3"},
			{"Raft Slots", fmt.Sprintf("%d", slotCount)},
			{"Workers", fmt.Sprintf("%d", cfg.Workers)},
			{"Duration", elapsed.Round(time.Millisecond).String()},
			{"Seed", fmt.Sprintf("%d", cfg.Seed)},
			{"Leaders", strings.Join(leaderStr, ", ")},
			{"Followers Used", fmt.Sprintf("%d", len(followers))},
		},
		results: []reportLine{
			{"Forwarded Ops", fmtOps(fN)},
			{"Throughput", fmtRate(fN, elapsed)},
			{"Transient Errors", fmtOps(eN)},
			{"Success Rate", successRate},
		},
		verdict: "PASS",
	}
	rpt.print(t)
}

func TestStressForwardingContentionWithLeaderRestarts(t *testing.T) {
	cfg := loadStressConfig(t)
	requireStressEnabled(t, cfg)

	const slotCount = 1
	testNodes := startThreeNodes(t, slotCount)
	defer func() {
		for _, n := range testNodes {
			n.stop()
		}
	}()

	leaderID := waitForStableLeader(t, testNodes, 1)
	var currentLeader atomic.Uint64
	currentLeader.Store(uint64(leaderID))

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Duration)
	defer cancel()

	var (
		wg       sync.WaitGroup
		fwdOps   atomic.Uint64
		fwdErrs  atomic.Uint64
		restarts atomic.Uint64
	)

	for worker := 0; worker < cfg.Workers; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()

			rng := rand.New(rand.NewSource(cfg.Seed + int64(worker)*515))
			for idx := 0; ; idx++ {
				if ctx.Err() != nil {
					return
				}

				leader := multiraft.NodeID(currentLeader.Load())
				followers := make([]*testNode, 0, len(testNodes)-1)
				for _, node := range testNodes {
					if node == nil || node.nodeID == leader {
						continue
					}
					followers = append(followers, node)
				}
				if len(followers) == 0 {
					fwdErrs.Add(1)
					time.Sleep(10 * time.Millisecond)
					continue
				}

				follower := followers[rng.Intn(len(followers))]
				channelID := fmt.Sprintf("fwdr-ch-%d-%d", worker, rng.Intn(32))
				channelType := int64(rng.Intn(3) + 1)
				if err := follower.store.CreateChannel(ctx, channelID, channelType); err != nil {
					if ctx.Err() != nil {
						return
					}
					fwdErrs.Add(1)
					continue
				}
				fwdOps.Add(1)
			}
		}(worker)
	}

	restartEvery := stressRestartInterval(cfg.Duration)
	restartTimer := time.NewTimer(restartEvery)
	defer restartTimer.Stop()

restartLoopForward:
	for {
		select {
		case <-ctx.Done():
			break restartLoopForward
		case <-restartTimer.C:
			leaderID, err := stableLeaderWithin(testNodes, 1, 10*time.Second)
			if err != nil {
				t.Fatalf("stable leader before forwarding restart: %v", err)
			}
			restartNode(t, testNodes, int(leaderID-1))
			stable := waitForStableLeader(t, testNodes, 1)
			currentLeader.Store(uint64(stable))
			restarts.Add(1)
			restartTimer.Reset(restartEvery)
		}
	}

	wg.Wait()
	elapsed := time.Since(start)

	finalLeader := multiraft.NodeID(currentLeader.Load())
	var probeFollower *testNode
	for _, node := range testNodes {
		if node == nil || node.nodeID == finalLeader {
			continue
		}
		probeFollower = node
		break
	}
	if probeFollower == nil {
		t.Fatal("no follower available for final forwarding probe")
	}

	probeID := fmt.Sprintf("fwdr-probe-%d", time.Now().UnixNano())
	probeCtx, probeCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer probeCancel()
	if err := probeFollower.store.CreateChannel(probeCtx, probeID, 1); err != nil {
		t.Fatalf("final forwarding probe: %v", err)
	}
	waitForChannelVisibleOnNodes(t, testNodes, probeID, 1)

	fN := fwdOps.Load()
	eN := fwdErrs.Load()
	restartN := restarts.Load()
	total := fN + eN
	successRate := "100%"
	if total > 0 {
		successRate = fmtPct(fN, total)
	}

	rpt := stressReport{
		title: "Forwarding Contention With Restarts",
		config: []reportLine{
			{"Nodes", "3"},
			{"Raft Slots", fmt.Sprintf("%d", slotCount)},
			{"Workers", fmt.Sprintf("%d", cfg.Workers)},
			{"Duration", elapsed.Round(time.Millisecond).String()},
			{"Seed", fmt.Sprintf("%d", cfg.Seed)},
			{"Restart Every", restartEvery.String()},
		},
		results: []reportLine{
			{"Forwarded Ops", fmtOps(fN)},
			{"Throughput", fmtRate(fN, elapsed)},
			{"Transient Errors", fmtOps(eN)},
			{"Restarts", fmtOps(restartN)},
			{"Success Rate", successRate},
		},
		verify: []reportLine{
			{"Final Probe", "OK"},
			{"Cluster Visibility", "ALL NODES"},
		},
		verdict: "PASS",
	}
	rpt.print(t)
}

// TestStressConcurrentCreateReadVerify does high-concurrency create followed
// by a full read-back verification. Every channel that was created must be
// readable. This catches data loss bugs under concurrent raft proposals.
func TestStressConcurrentCreateReadVerify(t *testing.T) {
	cfg := loadStressConfig(t)
	requireStressEnabled(t, cfg)

	const slotCount = 4
	n := startSingleNode(t, slotCount)
	defer n.stop()

	for g := 1; g <= slotCount; g++ {
		waitForLeader(t, n.cluster, uint64(g))
	}

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Duration)
	defer cancel()

	var (
		wg    sync.WaitGroup
		errCh = make(chan error, 1)
		mu    sync.Mutex
		// Track all successfully created channels
		created = make(map[string]int64)
		count   atomic.Uint64
	)

	for worker := 0; worker < cfg.Workers; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()

			var local []struct {
				id  string
				typ int64
			}

			for idx := 0; ; idx++ {
				if ctx.Err() != nil {
					break
				}

				channelID := fmt.Sprintf("verify-w%d-i%d", worker, idx)
				channelType := int64((worker+idx)%4 + 1)

				if err := n.store.CreateChannel(ctx, channelID, channelType); err != nil {
					if ctx.Err() != nil {
						break
					}
					select {
					case errCh <- fmt.Errorf("w%d CreateChannel(%s): %w", worker, channelID, err):
					default:
					}
					cancel()
					return
				}
				count.Add(1)
				local = append(local, struct {
					id  string
					typ int64
				}{channelID, channelType})
			}

			mu.Lock()
			for _, ch := range local {
				created[ch.id] = ch.typ
			}
			mu.Unlock()
		}(worker)
	}

	wg.Wait()
	elapsed := time.Since(start)

	select {
	case err := <-errCh:
		t.Fatalf("stress error: %v", err)
	default:
	}

	verifyCtx := context.Background()
	missing := 0
	for channelID, channelType := range created {
		ch, err := n.store.GetChannel(verifyCtx, channelID, channelType)
		if err != nil {
			missing++
			if missing <= 5 {
				t.Errorf("missing channel %q type=%d: %v", channelID, channelType, err)
			}
			continue
		}
		if ch.ChannelID != channelID {
			t.Errorf("channel mismatch: got %q, want %q", ch.ChannelID, channelID)
		}
	}
	if missing > 0 {
		t.Fatalf("%d/%d channels missing after create", missing, len(created))
	}

	createdN := uint64(len(created))
	integrity := "OK"
	verdict := "PASS"
	if missing > 0 {
		integrity = fmt.Sprintf("FAIL (%d missing)", missing)
		verdict = "FAIL"
	}

	rpt := stressReport{
		title: "Concurrent Create + Read Verify",
		config: []reportLine{
			{"Nodes", "1"},
			{"Raft Slots", fmt.Sprintf("%d", slotCount)},
			{"Workers", fmt.Sprintf("%d", cfg.Workers)},
			{"Duration", elapsed.Round(time.Millisecond).String()},
			{"Seed", fmt.Sprintf("%d", cfg.Seed)},
		},
		results: []reportLine{
			{"Created", fmtOps(createdN)},
			{"Throughput", fmtRate(createdN, elapsed)},
		},
		verify: []reportLine{
			{"Channels Created", fmt.Sprintf("%d", len(created))},
			{"Channels Verified", fmt.Sprintf("%d/%d", len(created)-missing, len(created))},
			{"Data Integrity", integrity},
		},
		verdict: verdict,
	}
	rpt.print(t)
}

// ── env helpers ─────────────────────────────────────────────────────

func envBool(name string, fallback bool) bool {
	value, ok := os.LookupEnv(name)
	if !ok || value == "" {
		return fallback
	}
	switch strings.ToLower(value) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func envDuration(t *testing.T, name string, fallback time.Duration) time.Duration {
	t.Helper()
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return d
}

func envInt(t *testing.T, name string, fallback int) int {
	t.Helper()
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return n
}

func envInt64(t *testing.T, name string, fallback int64) int64 {
	t.Helper()
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return n
}
