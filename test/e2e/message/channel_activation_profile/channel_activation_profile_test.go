//go:build e2e

package channel_activation_profile

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

const (
	channelActivationProfileEnabledEnv      = "WK_E2E_CHANNEL_ACTIVATION_PROFILE"
	channelActivationProfileArtifactDirEnv  = "WK_E2E_CHANNEL_ACTIVATION_PROFILE_DIR"
	channelActivationProfileChannelCountEnv = "WK_E2E_CHANNEL_ACTIVATION_PROFILE_CHANNELS"
	channelActivationProfileRoundCountEnv   = "WK_E2E_CHANNEL_ACTIVATION_PROFILE_ROUNDS"
	channelActivationProfileCPUSecondsEnv   = "WK_E2E_CHANNEL_ACTIVATION_PROFILE_CPU_SECONDS"
	channelActivationProfileTestTimeoutEnv  = "WK_E2E_CHANNEL_ACTIVATION_PROFILE_TIMEOUT"

	channelActivationProfileDefaultChannelCount = 100
	channelActivationProfileDefaultRoundCount   = 5
	channelActivationProfileDefaultCPUSeconds   = 3
	channelActivationProfileDefaultArtifactDir  = "tmp/profiles/e2e-channel-activation-100"
	channelActivationProfileDefaultTestTimeout  = 120 * time.Second
	channelActivationProfileDefaultPollInterval = 100 * time.Millisecond
)

func TestHundredConcurrentChannelActivationProfile(t *testing.T) {
	cfg, err := loadChannelActivationProfileConfig()
	require.NoError(t, err)
	if !cfg.Enabled {
		t.Skipf("set %s=1 to run the 100-channel activation pprof scenario", channelActivationProfileEnabledEnv)
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.TestTimeout)
	defer cancel()

	s := suite.New(t)
	node := s.StartSingleNodeCluster(suite.WithNodeConfigOverrides(1, map[string]string{
		"WK_DEBUG_API_ENABLE": "true",
	}))
	require.NoError(t, suite.WaitNodeReady(ctx, *node), node.Process.DumpDiagnostics())

	artifactDir := cfg.ArtifactDir
	require.NoError(t, os.MkdirAll(artifactDir, 0o755))
	profileBase := fmt.Sprintf("channel-activation-%dx%d", cfg.ChannelCount, cfg.RoundCount)
	cpuProfilePath := filepath.Join(artifactDir, profileBase+".cpu.pprof")
	cpuTopPath := filepath.Join(artifactDir, profileBase+".cpu.top.txt")
	heapProfilePath := filepath.Join(artifactDir, profileBase+".heap.pprof")
	heapTopPath := filepath.Join(artifactDir, profileBase+".heap.alloc_space.top.txt")

	apiBaseURL := "http://" + node.Spec.APIAddr
	workload := prepareChannelActivationWorkload(t, ctx, node, apiBaseURL, cfg.ChannelCount, cfg.RoundCount)
	defer workload.close()

	profileStarted := make(chan struct{})
	profileErrCh := make(chan error, 1)
	go func() {
		profileURL := fmt.Sprintf("%s/debug/pprof/profile?seconds=%d", apiBaseURL, cfg.CPUSeconds)
		profileErrCh <- downloadProfile(ctx, profileURL, cpuProfilePath, profileStarted)
	}()
	waitForProfileRequest(t, ctx, profileStarted)

	started := time.Now()
	results := make([]channelActivationResult, 0, workload.totalActivations())
	for roundIndex, items := range workload.rounds {
		roundStarted := time.Now()
		roundResults := activateChannelsConcurrently(t, ctx, items)
		t.Logf("channel activation profile round: round=%d channels=%d elapsed=%s", roundIndex+1, len(items), time.Since(roundStarted))
		results = append(results, roundResults...)
	}
	activationElapsed := time.Since(started)
	require.NoError(t, <-profileErrCh)

	for _, item := range workload.allItems() {
		waitForActiveChannelRuntimeMeta(t, ctx, *node, item.channelID)
	}

	heapURL := apiBaseURL + "/debug/pprof/heap"
	require.NoError(t, downloadProfile(ctx, heapURL, heapProfilePath, nil))

	binaryPath := ""
	if node.Process != nil {
		binaryPath = node.Process.BinaryPath
	}
	require.NotEmpty(t, binaryPath, "e2e binary path is required for go tool pprof")
	require.NoError(t, writePprofTop(ctx, binaryPath, cpuProfilePath, cpuTopPath))
	require.NoError(t, writePprofTop(ctx, binaryPath, heapProfilePath, heapTopPath, "-sample_index=alloc_space"))

	stats := summarizeActivationResults(results)
	t.Logf(
		"channel activation profile artifacts: cpu=%s cpu_top=%s heap=%s heap_top=%s channels_per_round=%d rounds=%d total_activations=%d activation_elapsed=%s ack_p50=%s ack_p95=%s ack_max=%s",
		cpuProfilePath,
		cpuTopPath,
		heapProfilePath,
		heapTopPath,
		cfg.ChannelCount,
		cfg.RoundCount,
		workload.totalActivations(),
		activationElapsed,
		stats.p50,
		stats.p95,
		stats.max,
	)
	logPprofTopPreview(t, "cpu", cpuTopPath)
	logPprofTopPreview(t, "heap_alloc_space", heapTopPath)
}

type channelActivationProfileConfig struct {
	Enabled      bool
	ArtifactDir  string
	ChannelCount int
	RoundCount   int
	CPUSeconds   int
	TestTimeout  time.Duration
}

func loadChannelActivationProfileConfig() (channelActivationProfileConfig, error) {
	channelCount, err := channelActivationProfileEnvInt(channelActivationProfileChannelCountEnv, channelActivationProfileDefaultChannelCount)
	if err != nil {
		return channelActivationProfileConfig{}, err
	}
	roundCount, err := channelActivationProfileEnvInt(channelActivationProfileRoundCountEnv, channelActivationProfileDefaultRoundCount)
	if err != nil {
		return channelActivationProfileConfig{}, err
	}
	cpuSeconds, err := channelActivationProfileEnvInt(channelActivationProfileCPUSecondsEnv, channelActivationProfileDefaultCPUSeconds)
	if err != nil {
		return channelActivationProfileConfig{}, err
	}
	testTimeout, err := channelActivationProfileEnvDuration(channelActivationProfileTestTimeoutEnv, channelActivationProfileDefaultTestTimeout)
	if err != nil {
		return channelActivationProfileConfig{}, err
	}

	return channelActivationProfileConfig{
		Enabled:      channelActivationProfileEnvEnabled(channelActivationProfileEnabledEnv),
		ArtifactDir:  channelActivationProfileArtifactDir(),
		ChannelCount: channelCount,
		RoundCount:   roundCount,
		CPUSeconds:   cpuSeconds,
		TestTimeout:  testTimeout,
	}, nil
}

type channelActivationWorkload struct {
	rounds [][]channelActivationItem
}

type channelActivationItem struct {
	channelID string
	senderUID string
	client    *suite.WKProtoClient
}

type channelActivationResult struct {
	channelID  string
	ackLatency time.Duration
	err        error
}

type activationLatencySummary struct {
	p50 time.Duration
	p95 time.Duration
	max time.Duration
}

func prepareChannelActivationWorkload(t *testing.T, ctx context.Context, node *suite.StartedNode, apiBaseURL string, channelsPerRound int, rounds int) channelActivationWorkload {
	t.Helper()

	workload := channelActivationWorkload{rounds: make([][]channelActivationItem, rounds)}
	for round := 0; round < rounds; round++ {
		items := make([]channelActivationItem, 0, channelsPerRound)
		for i := 0; i < channelsPerRound; i++ {
			channelID := fmt.Sprintf("e2e-activation-profile-r%02d-group-%03d", round+1, i)
			senderUID := fmt.Sprintf("e2e-activation-profile-r%02d-sender-%03d", round+1, i)
			postLegacyJSON(t, ctx, apiBaseURL+"/channel", map[string]any{
				"channel_id":   channelID,
				"channel_type": frame.ChannelTypeGroup,
				"subscribers":  []string{senderUID},
			})

			client, err := suite.NewWKProtoClient()
			require.NoError(t, err)
			require.NoError(t, client.Connect(node.GatewayAddr(), senderUID, senderUID+"-device"), node.Process.DumpDiagnostics())
			items = append(items, channelActivationItem{
				channelID: channelID,
				senderUID: senderUID,
				client:    client,
			})
		}
		workload.rounds[round] = items
	}
	return workload
}

func (w channelActivationWorkload) close() {
	for _, item := range w.allItems() {
		_ = item.client.Close()
	}
}

func (w channelActivationWorkload) allItems() []channelActivationItem {
	total := w.totalActivations()
	out := make([]channelActivationItem, 0, total)
	for _, items := range w.rounds {
		out = append(out, items...)
	}
	return out
}

func (w channelActivationWorkload) totalActivations() int {
	total := 0
	for _, items := range w.rounds {
		total += len(items)
	}
	return total
}

func activateChannelsConcurrently(t *testing.T, ctx context.Context, items []channelActivationItem) []channelActivationResult {
	t.Helper()

	release := make(chan struct{})
	results := make(chan channelActivationResult, len(items))
	for i, item := range items {
		clientSeq := uint64(i + 1)
		clientMsgNo := fmt.Sprintf("e2e-activation-profile-msg-%03d", i)
		go func(item channelActivationItem, clientSeq uint64, clientMsgNo string) {
			select {
			case <-ctx.Done():
				results <- channelActivationResult{channelID: item.channelID, err: ctx.Err()}
				return
			case <-release:
			}

			started := time.Now()
			if err := item.client.SendFrame(&frame.SendPacket{
				ChannelID:   item.channelID,
				ChannelType: frame.ChannelTypeGroup,
				ClientSeq:   clientSeq,
				ClientMsgNo: clientMsgNo,
				Payload:     []byte("activate channel under pprof"),
			}); err != nil {
				results <- channelActivationResult{channelID: item.channelID, err: fmt.Errorf("send frame: %w", err)}
				return
			}

			ack, err := item.client.ReadSendAck()
			if err != nil {
				results <- channelActivationResult{channelID: item.channelID, err: fmt.Errorf("read sendack: %w", err)}
				return
			}
			if ack.ReasonCode != frame.ReasonSuccess {
				results <- channelActivationResult{channelID: item.channelID, err: fmt.Errorf("sendack reason=%s", ack.ReasonCode)}
				return
			}
			if ack.ClientSeq != clientSeq || ack.ClientMsgNo != clientMsgNo {
				results <- channelActivationResult{channelID: item.channelID, err: fmt.Errorf("sendack mismatch seq=%d msg_no=%q", ack.ClientSeq, ack.ClientMsgNo)}
				return
			}
			if ack.MessageID == 0 || ack.MessageSeq == 0 {
				results <- channelActivationResult{channelID: item.channelID, err: fmt.Errorf("sendack missing message id or seq")}
				return
			}
			results <- channelActivationResult{channelID: item.channelID, ackLatency: time.Since(started)}
		}(item, clientSeq, clientMsgNo)
	}

	close(release)
	out := make([]channelActivationResult, 0, len(items))
	for range items {
		select {
		case <-ctx.Done():
			require.NoError(t, ctx.Err())
		case result := <-results:
			require.NoErrorf(t, result.err, "channel_id=%s", result.channelID)
			out = append(out, result)
		}
	}
	return out
}

func waitForProfileRequest(t *testing.T, ctx context.Context, started <-chan struct{}) {
	t.Helper()

	select {
	case <-started:
	case <-ctx.Done():
		require.NoError(t, ctx.Err())
	}
}

type channelRuntimeMetaDetail struct {
	ChannelID     string `json:"channel_id"`
	ChannelType   int64  `json:"channel_type"`
	Leader        uint64 `json:"leader"`
	MaxMessageSeq uint64 `json:"max_message_seq"`
	Status        string `json:"status"`
}

func waitForActiveChannelRuntimeMeta(t *testing.T, ctx context.Context, node suite.StartedNode, channelID string) {
	t.Helper()

	ticker := time.NewTicker(channelActivationProfileDefaultPollInterval)
	defer ticker.Stop()

	var lastErr error
	for {
		detail, body, err := fetchChannelRuntimeMetaDetail(ctx, node, channelID, frame.ChannelTypeGroup)
		if err == nil && detail.Status == "active" && detail.Leader != 0 && detail.MaxMessageSeq > 0 {
			return
		}
		if err != nil {
			lastErr = err
		} else {
			lastErr = fmt.Errorf("channel runtime meta not active: body=%s", body)
		}

		select {
		case <-ctx.Done():
			require.NoErrorf(t, lastErr, "channel_id=%s", channelID)
			require.NoError(t, ctx.Err())
			return
		case <-ticker.C:
		}
	}
}

func fetchChannelRuntimeMetaDetail(ctx context.Context, node suite.StartedNode, channelID string, channelType uint8) (channelRuntimeMetaDetail, string, error) {
	endpoint := fmt.Sprintf(
		"http://%s/manager/channel-runtime-meta/%d/%s",
		node.Spec.ManagerAddr,
		channelType,
		url.PathEscape(channelID),
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return channelRuntimeMetaDetail{}, "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return channelRuntimeMetaDetail{}, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return channelRuntimeMetaDetail{}, "", err
	}
	if resp.StatusCode != http.StatusOK {
		return channelRuntimeMetaDetail{}, string(body), fmt.Errorf("status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var detail channelRuntimeMetaDetail
	if err := json.Unmarshal(body, &detail); err != nil {
		return channelRuntimeMetaDetail{}, string(body), err
	}
	return detail, string(body), nil
}

func downloadProfile(ctx context.Context, endpoint, path string, started chan<- struct{}) error {
	var startedOnce sync.Once
	if started != nil {
		ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
			WroteRequest: func(httptrace.WroteRequestInfo) {
				startedOnce.Do(func() { close(started) })
			},
		})
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		if started != nil {
			startedOnce.Do(func() { close(started) })
		}
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if started != nil {
		startedOnce.Do(func() { close(started) })
	}
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s returned %d: %s", endpoint, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return os.WriteFile(path, body, 0o644)
}

func writePprofTop(ctx context.Context, binaryPath, profilePath, outputPath string, extraArgs ...string) error {
	args := []string{"tool", "pprof", "-top"}
	args = append(args, extraArgs...)
	args = append(args, binaryPath, profilePath)
	cmd := exec.CommandContext(ctx, "go", args...)
	output, err := cmd.CombinedOutput()
	if writeErr := os.WriteFile(outputPath, output, 0o644); writeErr != nil {
		return writeErr
	}
	if err != nil {
		return fmt.Errorf("go %s: %w\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}

func postLegacyJSON(t *testing.T, ctx context.Context, endpoint string, payload any) {
	t.Helper()

	data, err := json.Marshal(payload)
	require.NoError(t, err)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode, strings.TrimSpace(string(body)))
	require.JSONEq(t, `{"status":200}`, string(body))
}

func summarizeActivationResults(results []channelActivationResult) activationLatencySummary {
	if len(results) == 0 {
		return activationLatencySummary{}
	}
	latencies := make([]time.Duration, 0, len(results))
	for _, result := range results {
		latencies = append(latencies, result.ackLatency)
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	return activationLatencySummary{
		p50: percentileDuration(latencies, 50),
		p95: percentileDuration(latencies, 95),
		max: latencies[len(latencies)-1],
	}
}

func percentileDuration(sorted []time.Duration, percentile int) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	if percentile <= 0 {
		return sorted[0]
	}
	if percentile >= 100 {
		return sorted[len(sorted)-1]
	}
	index := (len(sorted)*percentile + 99) / 100
	if index <= 0 {
		index = 1
	}
	if index > len(sorted) {
		index = len(sorted)
	}
	return sorted[index-1]
}

func channelActivationProfileArtifactDir() string {
	if value := strings.TrimSpace(os.Getenv(channelActivationProfileArtifactDirEnv)); value != "" {
		return value
	}
	return channelActivationProfileDefaultArtifactDir
}

func channelActivationProfileEnvInt(name string, fallback int) (int, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", name, err)
	}
	if parsed <= 0 {
		return 0, fmt.Errorf("%s must be > 0, got %d", name, parsed)
	}
	return parsed, nil
}

func channelActivationProfileEnvDuration(name string, fallback time.Duration) (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", name, err)
	}
	if parsed <= 0 {
		return 0, fmt.Errorf("%s must be > 0, got %s", name, parsed)
	}
	return parsed, nil
}

func channelActivationProfileEnvEnabled(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func logPprofTopPreview(t *testing.T, label, path string) {
	t.Helper()

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) > 20 {
		lines = lines[:20]
	}
	t.Logf("%s pprof top preview (%s):\n%s", label, path, strings.Join(lines, "\n"))
}
