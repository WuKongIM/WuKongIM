//go:build integration

package scripts_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

type localThresholdPprofMetadata struct {
	Schema  string `json:"schema"`
	Trigger struct {
		Kind          string `json:"kind"`
		ObservedPhase string `json:"observed_phase"`
		PreviousUTC   string `json:"previous_utc"`
		CurrentUTC    string `json:"current_utc"`
	} `json:"trigger"`
	Capture struct {
		Status     string `json:"status"`
		Valid      bool   `json:"valid"`
		Reason     string `json:"reason"`
		StartPhase string `json:"start_phase"`
		EndPhase   string `json:"end_phase"`
		StartedUTC string `json:"started_at_utc"`
		EndedUTC   string `json:"completed_at_utc"`
		CPUSeconds int    `json:"cpu_seconds"`
	} `json:"capture"`
	Nodes []struct {
		Node      string `json:"node"`
		CPU       string `json:"cpu"`
		Heap      string `json:"heap"`
		Goroutine string `json:"goroutine"`
	} `json:"nodes"`
}

type localPprofTestServer struct {
	server         *httptest.Server
	requests       atomic.Int64
	authFailures   atomic.Int64
	activeRequests atomic.Int64
	profileStarted chan struct{}
	profileOnce    sync.Once
}

func newLocalPprofTestServer(t *testing.T, delay time.Duration, failingPath string) *localPprofTestServer {
	return newLocalPprofTestServerWithToken(t, delay, failingPath, "")
}

func newLocalPprofTestServerWithToken(t *testing.T, delay time.Duration, failingPath, token string) *localPprofTestServer {
	t.Helper()
	testServer := &localPprofTestServer{profileStarted: make(chan struct{})}
	testServer.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		testServer.requests.Add(1)
		testServer.activeRequests.Add(1)
		defer testServer.activeRequests.Add(-1)
		if request.URL.Path == "/debug/pprof/profile" {
			testServer.profileOnce.Do(func() { close(testServer.profileStarted) })
			if request.URL.Query().Get("seconds") != "1" {
				http.Error(writer, "unexpected CPU duration", http.StatusBadRequest)
				return
			}
		}
		if token != "" && request.Header.Get("Authorization") != "Bearer "+token {
			testServer.authFailures.Add(1)
			time.Sleep(delay)
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
			return
		}
		select {
		case <-time.After(delay):
		case <-request.Context().Done():
			return
		}
		if request.URL.Path == failingPath {
			http.Error(writer, "injected failure", http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(writer, "profile:"+request.URL.Path)
	}))
	t.Cleanup(testServer.server.Close)
	return testServer
}

func TestLocalThresholdPprofAuthenticatesWithoutExposingTokenInArgumentsOrEvidence(t *testing.T) {
	root := repoRoot(t)
	token := "pprof-auth-secret-4b8350a3"
	servers := []*localPprofTestServer{
		newLocalPprofTestServerWithToken(t, 250*time.Millisecond, "", token),
		newLocalPprofTestServerWithToken(t, 250*time.Millisecond, "", token),
		newLocalPprofTestServerWithToken(t, 250*time.Millisecond, "", token),
	}
	phasePath := filepath.Join(t.TempDir(), "phase")
	writeLocalPprofPhase(t, phasePath, "measurement")
	outDir := filepath.Join(t.TempDir(), "capture")
	command := localThresholdPprofCommand(root, token, localThresholdPprofArgs(outDir, phasePath, "actual_offered_ratio", servers)...)
	var commandOutput bytes.Buffer
	command.Stdout = &commandOutput
	command.Stderr = &commandOutput
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-servers[0].profileStarted:
	case <-time.After(3 * time.Second):
		_ = command.Process.Kill()
		t.Fatal("authenticated CPU profile request did not start")
	}
	processes, err := exec.Command("ps", "-axo", "command=").CombinedOutput()
	if err != nil {
		_ = command.Process.Kill()
		t.Fatalf("inspect process arguments: %v\n%s", err, processes)
	}
	if strings.Contains(string(processes), token) {
		_ = command.Process.Kill()
		t.Fatalf("API token leaked into process arguments: %s", processes)
	}
	if err := command.Wait(); err != nil {
		t.Fatalf("authenticated capture failed: %v\n%s", err, commandOutput.String())
	}
	if strings.Contains(commandOutput.String(), token) {
		t.Fatal("API token leaked into helper logs")
	}
	for index, server := range servers {
		if failures := server.authFailures.Load(); failures != 0 {
			t.Fatalf("node %d rejected %d unauthenticated request(s)", index+1, failures)
		}
	}
	metadataBody := readFile(t, filepath.Join(outDir, "metadata.json"))
	metadata := decodeLocalThresholdPprofMetadata(t, metadataBody)
	if metadata.Capture.Status != "complete" || !metadata.Capture.Valid {
		t.Fatalf("authenticated capture metadata = %+v", metadata.Capture)
	}
	if strings.Contains(metadataBody, token) {
		t.Fatal("API token leaked into threshold pprof metadata")
	}
	err = filepath.WalkDir(outDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(body), token) {
			t.Fatalf("API token leaked into evidence file %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestLocalThresholdPprofCapturesThreeNodesOnce(t *testing.T) {
	root := repoRoot(t)
	servers := []*localPprofTestServer{
		newLocalPprofTestServer(t, 20*time.Millisecond, ""),
		newLocalPprofTestServer(t, 20*time.Millisecond, ""),
		newLocalPprofTestServer(t, 20*time.Millisecond, ""),
	}
	phasePath := filepath.Join(t.TempDir(), "phase")
	writeLocalPprofPhase(t, phasePath, "measurement")
	outDir := filepath.Join(t.TempDir(), "capture")
	args := localThresholdPprofArgs(outDir, phasePath, "actual_offered_ratio", servers)

	output, err := runLocalThresholdPprof(root, args...)
	if err != nil {
		t.Fatalf("first capture failed: %v\n%s", err, output)
	}
	metadataBody := readFile(t, filepath.Join(outDir, "metadata.json"))
	metadata := decodeLocalThresholdPprofMetadata(t, metadataBody)
	if metadata.Schema != "wukongim.local_threshold_pprof/v1" || metadata.Trigger.Kind != "actual_offered_ratio" ||
		metadata.Trigger.ObservedPhase != "measurement" ||
		metadata.Trigger.PreviousUTC != "2026-08-13T00:00:00.123456789Z" || metadata.Trigger.CurrentUTC != "2026-08-13T00:00:01.5Z" ||
		metadata.Capture.Status != "complete" || !metadata.Capture.Valid || metadata.Capture.Reason != "ok" ||
		metadata.Capture.StartPhase != "measurement" || metadata.Capture.EndPhase != "measurement" || metadata.Capture.CPUSeconds != 1 {
		t.Fatalf("complete metadata = %+v", metadata)
	}
	if strings.Contains(metadataBody, "127.0.0.1") || strings.Contains(metadataBody, "http://") {
		t.Fatalf("metadata leaked a raw node URL:\n%s", metadataBody)
	}
	if len(metadata.Nodes) != 3 {
		t.Fatalf("metadata nodes = %d, want 3", len(metadata.Nodes))
	}
	for index, node := range metadata.Nodes {
		if node.Node != "node-"+string(rune('1'+index)) || node.CPU != "complete" || node.Heap != "complete" || node.Goroutine != "complete" {
			t.Fatalf("node %d metadata = %+v", index+1, node)
		}
		for _, suffix := range []string{"cpu.pb.gz", "heap.pb.gz", "goroutine.txt"} {
			path := filepath.Join(outDir, "profiles", node.Node+"-"+suffix)
			if info, err := os.Stat(path); err != nil || info.Size() == 0 {
				t.Fatalf("profile %s missing or empty: %v", path, err)
			}
		}
	}
	assertNoLocalPprofTemporaryFiles(t, outDir)

	beforeRequests := totalLocalPprofRequests(servers)
	repeatedArgs := localThresholdPprofArgs(outDir, phasePath, "sendack_p99", servers)
	repeatedOutput, err := runLocalThresholdPprof(root, repeatedArgs...)
	if err != nil || !strings.Contains(repeatedOutput, "first trigger already claimed") {
		t.Fatalf("repeat = %v\n%s", err, repeatedOutput)
	}
	if afterRequests := totalLocalPprofRequests(servers); afterRequests != beforeRequests {
		t.Fatalf("repeat made new network requests: before=%d after=%d", beforeRequests, afterRequests)
	}
	if repeatedMetadata := readFile(t, filepath.Join(outDir, "metadata.json")); repeatedMetadata != metadataBody {
		t.Fatalf("repeat overwrote first-trigger metadata:\nbefore=%s\nafter=%s", metadataBody, repeatedMetadata)
	}
}

func TestLocalThresholdPprofCapturesSingleNodeOnce(t *testing.T) {
	root := repoRoot(t)
	servers := []*localPprofTestServer{
		newLocalPprofTestServer(t, 20*time.Millisecond, ""),
	}
	phasePath := filepath.Join(t.TempDir(), "phase")
	writeLocalPprofPhase(t, phasePath, "measurement")
	outDir := filepath.Join(t.TempDir(), "capture")

	output, err := runLocalThresholdPprof(root, localThresholdPprofArgs(outDir, phasePath, "actual_offered_ratio", servers)...)
	if err != nil {
		t.Fatalf("single-node capture failed: %v\n%s", err, output)
	}
	metadata := decodeLocalThresholdPprofMetadata(t, readFile(t, filepath.Join(outDir, "metadata.json")))
	if metadata.Capture.Status != "complete" || !metadata.Capture.Valid || metadata.Capture.Reason != "ok" {
		t.Fatalf("single-node capture metadata = %+v", metadata.Capture)
	}
	if len(metadata.Nodes) != 1 || metadata.Nodes[0].Node != "node-1" ||
		metadata.Nodes[0].CPU != "complete" || metadata.Nodes[0].Heap != "complete" || metadata.Nodes[0].Goroutine != "complete" {
		t.Fatalf("single-node metadata nodes = %+v", metadata.Nodes)
	}
	if requests := totalLocalPprofRequests(servers); requests != 3 {
		t.Fatalf("single-node capture made %d requests, want one three-profile set", requests)
	}
	for _, suffix := range []string{"cpu.pb.gz", "heap.pb.gz", "goroutine.txt"} {
		path := filepath.Join(outDir, "profiles", "node-1-"+suffix)
		if info, err := os.Stat(path); err != nil || info.Size() == 0 {
			t.Fatalf("profile %s missing or empty: %v", path, err)
		}
	}
	if _, err := os.Stat(filepath.Join(outDir, "profiles", "node-2-cpu.pb.gz")); !os.IsNotExist(err) {
		t.Fatalf("single-node capture created an unexpected node-2 profile: %v", err)
	}
	assertNoLocalPprofTemporaryFiles(t, outDir)
}

func TestLocalThresholdPprofMarksCaptureAcrossDrainPartial(t *testing.T) {
	root := repoRoot(t)
	servers := []*localPprofTestServer{
		newLocalPprofTestServer(t, 250*time.Millisecond, ""),
		newLocalPprofTestServer(t, 250*time.Millisecond, ""),
		newLocalPprofTestServer(t, 250*time.Millisecond, ""),
	}
	phasePath := filepath.Join(t.TempDir(), "phase")
	writeLocalPprofPhase(t, phasePath, "measurement")
	outDir := filepath.Join(t.TempDir(), "capture")
	command := localThresholdPprofCommand(root, "local-threshold-pprof-test-token",
		localThresholdPprofArgs(outDir, phasePath, "actual_offered_ratio", servers)...)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-servers[0].profileStarted:
	case <-time.After(3 * time.Second):
		_ = command.Process.Kill()
		t.Fatal("CPU profile request did not start")
	}
	writeLocalPprofPhase(t, phasePath, "drain")
	if err := command.Wait(); err != nil {
		t.Fatalf("cross-phase capture returned an operational failure: %v", err)
	}
	metadata := decodeLocalThresholdPprofMetadata(t, readFile(t, filepath.Join(outDir, "metadata.json")))
	if metadata.Capture.Status != "partial" || metadata.Capture.Valid ||
		metadata.Capture.Reason != "phase_changed_during_capture" ||
		metadata.Capture.StartPhase != "measurement" || metadata.Capture.EndPhase != "drain" {
		t.Fatalf("cross-phase metadata = %+v", metadata.Capture)
	}
	assertNoLocalPprofTemporaryFiles(t, outDir)
}

func TestLocalThresholdPprofRecordsMissedMeasuredStartWithoutNetwork(t *testing.T) {
	root := repoRoot(t)
	servers := []*localPprofTestServer{
		newLocalPprofTestServer(t, 10*time.Millisecond, ""),
		newLocalPprofTestServer(t, 10*time.Millisecond, ""),
		newLocalPprofTestServer(t, 10*time.Millisecond, ""),
	}
	phasePath := filepath.Join(t.TempDir(), "phase")
	writeLocalPprofPhase(t, phasePath, "drain")
	outDir := filepath.Join(t.TempDir(), "capture")
	output, err := runLocalThresholdPprof(root, localThresholdPprofArgs(outDir, phasePath, "actual_offered_ratio", servers)...)
	if err != nil {
		t.Fatalf("missed-start capture must preserve evidence without failing its parent: %v\n%s", err, output)
	}
	metadata := decodeLocalThresholdPprofMetadata(t, readFile(t, filepath.Join(outDir, "metadata.json")))
	if metadata.Capture.Status != "partial" || metadata.Capture.Valid ||
		metadata.Capture.Reason != "capture_start_missed_measurement" ||
		metadata.Capture.StartPhase != "drain" || metadata.Capture.EndPhase != "drain" {
		t.Fatalf("missed-start metadata = %+v", metadata.Capture)
	}
	if requests := totalLocalPprofRequests(servers); requests != 0 {
		t.Fatalf("missed-start trigger made %d network requests, want 0", requests)
	}
	entries, err := os.ReadDir(filepath.Join(outDir, "profiles"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("missed-start trigger created %d profile blob(s), want none", len(entries))
	}
	assertNoLocalPprofTemporaryFiles(t, outDir)
}

func TestLocalThresholdPprofConcurrentTriggerDoesNotRepeatNetwork(t *testing.T) {
	root := repoRoot(t)
	servers := []*localPprofTestServer{
		newLocalPprofTestServer(t, 300*time.Millisecond, ""),
		newLocalPprofTestServer(t, 300*time.Millisecond, ""),
		newLocalPprofTestServer(t, 300*time.Millisecond, ""),
	}
	phasePath := filepath.Join(t.TempDir(), "phase")
	writeLocalPprofPhase(t, phasePath, "measurement")
	outDir := filepath.Join(t.TempDir(), "capture")
	first := localThresholdPprofCommand(root, "local-threshold-pprof-test-token",
		localThresholdPprofArgs(outDir, phasePath, "actual_offered_ratio", servers)...)
	if err := first.Start(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-servers[0].profileStarted:
	case <-time.After(3 * time.Second):
		_ = first.Process.Kill()
		t.Fatal("first trigger did not begin capture")
	}
	repeatedOutput, err := runLocalThresholdPprof(root, localThresholdPprofArgs(outDir, phasePath, "sendack_p99", servers)...)
	if err != nil || !strings.Contains(repeatedOutput, "first trigger already claimed") {
		_ = first.Process.Kill()
		t.Fatalf("concurrent repeat = %v\n%s", err, repeatedOutput)
	}
	if err := first.Wait(); err != nil {
		t.Fatal(err)
	}
	if requests := totalLocalPprofRequests(servers); requests != 9 {
		t.Fatalf("concurrent trigger made %d requests, want exactly one three-node profile set (9)", requests)
	}
	metadata := decodeLocalThresholdPprofMetadata(t, readFile(t, filepath.Join(outDir, "metadata.json")))
	if metadata.Trigger.Kind != "actual_offered_ratio" || metadata.Capture.Status != "complete" {
		t.Fatalf("concurrent repeat displaced first-trigger evidence: %+v", metadata)
	}
}

func TestLocalThresholdPprofRecordsMissingProfile(t *testing.T) {
	root := repoRoot(t)
	servers := []*localPprofTestServer{
		newLocalPprofTestServer(t, 10*time.Millisecond, ""),
		newLocalPprofTestServer(t, 10*time.Millisecond, "/debug/pprof/heap"),
		newLocalPprofTestServer(t, 10*time.Millisecond, ""),
	}
	phasePath := filepath.Join(t.TempDir(), "phase")
	writeLocalPprofPhase(t, phasePath, "measurement")
	outDir := filepath.Join(t.TempDir(), "capture")
	output, err := runLocalThresholdPprof(root, localThresholdPprofArgs(outDir, phasePath, "terminal_product_failure", servers)...)
	if err != nil {
		t.Fatalf("partial capture returned an operational failure: %v\n%s", err, output)
	}
	metadata := decodeLocalThresholdPprofMetadata(t, readFile(t, filepath.Join(outDir, "metadata.json")))
	if metadata.Capture.Status != "partial" || metadata.Capture.Valid || metadata.Capture.Reason != "profile_capture_missing" ||
		len(metadata.Nodes) != 3 || metadata.Nodes[1].Heap != "missing" {
		t.Fatalf("missing-profile metadata = %+v", metadata)
	}
	if _, err := os.Stat(filepath.Join(outDir, "profiles", "node-2-heap.pb.gz")); !os.IsNotExist(err) {
		t.Fatalf("failed heap request left a promoted blob: %v", err)
	}
	assertNoLocalPprofTemporaryFiles(t, outDir)
}

func TestLocalThresholdPprofInterruptJoinsRequestsAndWritesPartialMetadata(t *testing.T) {
	root := repoRoot(t)
	servers := []*localPprofTestServer{
		newLocalPprofTestServer(t, 10*time.Second, ""),
		newLocalPprofTestServer(t, 10*time.Second, ""),
		newLocalPprofTestServer(t, 10*time.Second, ""),
	}
	phasePath := filepath.Join(t.TempDir(), "phase")
	writeLocalPprofPhase(t, phasePath, "measurement")
	outDir := filepath.Join(t.TempDir(), "capture")
	command := localThresholdPprofCommand(root, "local-threshold-pprof-test-token",
		localThresholdPprofArgs(outDir, phasePath, "sendack_p99", servers)...)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-servers[0].profileStarted:
	case <-time.After(3 * time.Second):
		_ = command.Process.Kill()
		t.Fatal("CPU profile request did not start")
	}
	started := time.Now()
	if err := command.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err != nil {
		t.Fatalf("interrupted helper must return success after preserving evidence: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("interrupt did not join bounded child requests promptly: %s", elapsed)
	}
	metadata := decodeLocalThresholdPprofMetadata(t, readFile(t, filepath.Join(outDir, "metadata.json")))
	if metadata.Capture.Status != "partial" || metadata.Capture.Valid || metadata.Capture.Reason != "interrupted" {
		t.Fatalf("interrupted metadata = %+v", metadata.Capture)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		active := int64(0)
		for _, server := range servers {
			active += server.activeRequests.Load()
		}
		if active == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	for index, server := range servers {
		if active := server.activeRequests.Load(); active != 0 {
			t.Fatalf("server %d still has %d active request(s) after helper exit", index+1, active)
		}
	}
	assertNoLocalPprofTemporaryFiles(t, outDir)
}

func localThresholdPprofArgs(outDir, phasePath, trigger string, servers []*localPprofTestServer) []string {
	args := []string{
		"--out-dir", outDir,
		"--phase-state-file", phasePath,
		"--trigger-kind", trigger,
		"--trigger-observed-phase", "measurement",
		"--previous-utc", "2026-08-13T08:00:00.123456789+08:00",
		"--current-utc", "2026-08-13T08:00:01.500+08:00",
	}
	for _, server := range servers {
		args = append(args, "--node", server.server.URL)
	}
	return append(args, "--cpu-seconds", "1")
}

func runLocalThresholdPprof(root string, args ...string) (string, error) {
	command := localThresholdPprofCommand(root, "local-threshold-pprof-test-token", args...)
	output, err := command.CombinedOutput()
	return string(output), err
}

func localThresholdPprofCommand(root, token string, args ...string) *exec.Cmd {
	command := exec.Command("bash", append([]string{
		filepath.Join(root, "scripts", "capture-wukongim-local-threshold-pprof.sh"),
	}, args...)...)
	command.Dir = root
	command.Env = testEnvironmentWith("WK_BENCH_API_TOKEN", token)
	return command
}

func writeLocalPprofPhase(t *testing.T, path, phase string) {
	t.Helper()
	temporary := path + ".next"
	if err := os.WriteFile(temporary, []byte(phase+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(temporary, path); err != nil {
		t.Fatal(err)
	}
}

func decodeLocalThresholdPprofMetadata(t *testing.T, body string) localThresholdPprofMetadata {
	t.Helper()
	var metadata localThresholdPprofMetadata
	decoder := json.NewDecoder(strings.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&metadata); err != nil {
		t.Fatalf("decode metadata: %v\n%s", err, body)
	}
	return metadata
}

func totalLocalPprofRequests(servers []*localPprofTestServer) int64 {
	var total int64
	for _, server := range servers {
		total += server.requests.Load()
	}
	return total
}

func assertNoLocalPprofTemporaryFiles(t *testing.T, outDir string) {
	t.Helper()
	err := filepath.WalkDir(outDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.Contains(entry.Name(), ".next.") {
			t.Fatalf("temporary capture file remains after helper exit: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
