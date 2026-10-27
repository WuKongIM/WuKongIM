//go:build integration

package benchmark

// This tests-first overlay exercises the proposed CLI-owned trace APIs. These
// APIs do not exist at the frozen source revision: initial RED is compile RED.
// The generation below is an HTTP protocol fixture, not a native workload and
// not evidence of the cause of R27 underdelivery.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/chatlifecycle"
)

const r28OwnerTraceLimit int64 = 16 << 20

type r28OwnerReceipt struct {
	Schema           string `json:"schema"`
	State            string `json:"state"`
	Reason           string `json:"reason"`
	Bytes            int64  `json:"bytes"`
	SHA256           string `json:"sha256"`
	Coverage         string `json:"coverage"`
	WriterJoined     bool   `json:"writer_joined"`
	OutputLimitBytes int64  `json:"output_limit_bytes"`
}

func TestWorkerRuntimeTraceCappedWriter(t *testing.T) {
	t.Run("strict_bound", func(t *testing.T) {
		var target bytes.Buffer
		writer := newWorkerRuntimeTraceCappedWriter(&target, 16)
		n, err := writer.Write(bytes.Repeat([]byte{1}, 17))
		if err == nil || n < 0 || n > 16 || target.Len() != n {
			t.Fatalf("overflow accepted: n=%d stored=%d has_error=%t", n, target.Len(), err != nil)
		}
		if n, err = writer.Write(bytes.Repeat([]byte{2}, 17)); n < 0 || n > 16 || err == nil || target.Len() > 16 {
			t.Fatalf("repeated overflow exceeded bound: n=%d stored=%d has_error=%t", n, target.Len(), err != nil)
		}
	})
	t.Run("exact_boundary", func(t *testing.T) {
		var target bytes.Buffer
		writer := newWorkerRuntimeTraceCappedWriter(&target, 16)
		if n, err := writer.Write(bytes.Repeat([]byte{1}, 16)); n != 16 || err != nil {
			t.Fatalf("exact bound rejected: n=%d has_error=%t", n, err != nil)
		}
		if n, err := writer.Write([]byte{2}); n != 0 || err == nil || target.Len() != 16 {
			t.Fatalf("post-bound write accepted: n=%d stored=%d has_error=%t", n, target.Len(), err != nil)
		}
	})
	t.Run("underlying_short_write", func(t *testing.T) {
		writer := newWorkerRuntimeTraceCappedWriter(r28OwnerShortWriter{}, 16)
		if n, err := writer.Write([]byte{1, 2}); n != 1 || err == nil {
			t.Fatalf("short write hidden: n=%d has_error=%t", n, err != nil)
		}
	})
}

type r28OwnerShortWriter struct{}

func (r28OwnerShortWriter) Write(p []byte) (int, error) { return len(p) / 2, nil }

func TestWorkerRuntimeTraceOwnerArtifacts(t *testing.T) {
	for _, mode := range []string{"complete", "complete_default", "write_error", "short_write", "close_error", "cap_error"} {
		t.Run(mode, func(t *testing.T) {
			dir := r28OwnerArtifactDir(t, mode)
			child := r28OwnerStartChild(t, mode, dir)
			child.wait(t)
			receipt := r28OwnerReadReceipt(t, dir)
			if !receipt.WriterJoined {
				t.Fatal("finished export did not join its writer")
			}
			if mode != "complete_default" {
				if got := r28OwnerReadSmall(t, filepath.Join(dir, "open-count"), 32); string(got) != "1" {
					t.Fatalf("repeated notification opened %q exporters", got)
				}
			}
			tracePath := filepath.Join(dir, "runtime-trace", "trace.out")
			if mode != "complete" && mode != "complete_default" {
				if receipt.State != "partial" || receipt.Reason == "" {
					t.Fatalf("IO fault falsely completed: state=%q reason_present=%t", receipt.State, receipt.Reason != "")
				}
				if _, err := os.Lstat(tracePath); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("IO fault published trace.out")
				}
				return
			}
			if receipt.State != "complete" || receipt.Bytes <= 0 {
				t.Fatalf("real recorder not completed: state=%q bytes=%d", receipt.State, receipt.Bytes)
			}
			info, err := os.Lstat(tracePath)
			if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > r28OwnerTraceLimit {
				t.Fatal("trace.out missing, not private/regular, or above hard output cap")
			}
			data, err := os.ReadFile(tracePath)
			if err != nil {
				t.Fatal("read completed trace failed")
			}
			digest := sha256.Sum256(data)
			if receipt.Bytes != int64(len(data)) || receipt.SHA256 != hex.EncodeToString(digest[:]) {
				t.Fatal("receipt does not bind exact completed trace bytes")
			}
			if _, err := os.Lstat(filepath.Join(dir, "runtime-trace", "trace.partial")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("successful rename retained trace.partial")
			}
			r28OwnerDecode(t, tracePath, filepath.Join(dir, "decoded.txt"))
		})
	}
}

func TestWorkerRuntimeTraceBlockedWriterPreservesWorkerLifetime(t *testing.T) {
	for _, mode := range []string{"blocked_stop", "blocked_unexpected"} {
		t.Run(mode, func(t *testing.T) {
			dir := r28OwnerArtifactDir(t, mode)
			child := r28OwnerStartChild(t, mode, dir)
			addr := string(r28OwnerAwaitFile(t, filepath.Join(dir, "http-address"), 128))
			client := &http.Client{Timeout: 2 * time.Second}
			defer client.CloseIdleConnections()
			var info chatlifecycle.WorkerInfo
			r28OwnerHTTP(t, client, addr, http.MethodGet, "/v1/info", nil, &info)
			if info.ProtocolVersion != 15 {
				t.Fatalf("wire version changed: %d", info.ProtocolVersion)
			}
			config := chatlifecycle.LocalConfig()
			config.RunID = "r28-owner-fixture"
			fence := chatlifecycle.WorkerFence{RunID: config.RunID, AssignmentID: "r28-owner-assignment", Generation: 7}
			assignment := chatlifecycle.WorkerAssignment{WorkerFence: fence, WorkerID: 0, WorkerCount: uint64(config.Workload.Workers), Config: config, CoordinatorGrants: true}
			r28OwnerHTTP(t, client, addr, http.MethodPost, "/v1/chat-lifecycle/assign", assignment, nil)
			r28OwnerHTTP(t, client, addr, http.MethodPost, "/v1/chat-lifecycle/start", chatlifecycle.WorkerStartRequest{WorkerFence: fence}, nil)
			var snapshot chatlifecycle.WorkerSnapshot
			r28OwnerHTTP(t, client, addr, http.MethodGet, "/v1/chat-lifecycle/snapshot", nil, &snapshot)
			if snapshot.Generation != 7 || snapshot.Harness.Failures != 1 || snapshot.Harness.OfferedUnderdelivery != 1 || !reflect.DeepEqual(snapshot.Messages, chatlifecycle.WorkerMessageSnapshot{}) {
				t.Fatal("protocol fixture native aggregates changed")
			}
			r28OwnerAwaitFile(t, filepath.Join(dir, "writer-entered"), 32)
			// The writer is now synchronously blocked inside WriteTo, but the
			// actual authenticated HTTP snapshot must still return normally.
			r28OwnerHTTP(t, client, addr, http.MethodGet, "/v1/chat-lifecycle/snapshot", nil, &snapshot)
			if mode == "blocked_stop" {
				r28OwnerHTTP(t, client, addr, http.MethodPost, "/v1/chat-lifecycle/stop", chatlifecycle.WorkerStopRequest{WorkerFence: fence}, nil)
			}
			if _, err := child.stdin.Write([]byte{1}); err != nil {
				t.Fatal("child control write failed")
			}
			child.wait(t)
			receipt := r28OwnerReadReceipt(t, dir)
			if receipt.WriterJoined || receipt.State == "complete" || (receipt.State != "pending" && receipt.State != "partial") {
				t.Fatalf("unjoined writer falsely completed: state=%q joined=%t", receipt.State, receipt.WriterJoined)
			}
			if _, err := os.Lstat(filepath.Join(dir, "runtime-trace", "trace.out")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("blocked unjoined writer published completed trace")
			}
			if got := string(r28OwnerReadSmall(t, filepath.Join(dir, "wait-returned"), 32)); got != mode {
				t.Fatal("original worker wait did not return expected terminal outcome")
			}
		})
	}
}

// All actual recorder ownership is isolated in one child process per case.
// In particular an intentionally unjoinable diagnostic writer never leaks a
// global runtime tracer into the parent test process or another test case.
func TestWorkerRuntimeTraceOwnerChild(t *testing.T) {
	mode := os.Getenv("WK_R28_OWNER_CHILD")
	if mode == "" {
		t.Skip("subprocess fixture")
	}
	dir := os.Getenv("WK_R28_OWNER_WORKDIR")
	if !filepath.IsAbs(dir) {
		t.Fatal("fixture requires absolute work directory")
	}
	var opens atomic.Uint32
	factory := func(path string) (io.WriteCloser, error) {
		if opens.Add(1) != 1 {
			return nil, errors.New("duplicate trace open")
		}
		if err := os.WriteFile(filepath.Join(dir, "open-count"), []byte("1"), 0600); err != nil {
			return nil, err
		}
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return nil, err
		}
		return &r28OwnerFaultFile{file: file, mode: mode, dir: dir}, nil
	}
	if mode == "complete_default" {
		factory = nil
	}
	owner, err := newWorkerRuntimeTrace(dir, factory)
	if err != nil {
		t.Fatal("trace owner constructor failed")
	}
	if strings.HasPrefix(mode, "blocked_") {
		r28OwnerBlockedChild(t, mode, dir, owner.NotifyHarnessFailure, owner.RequestStop)
		return
	}
	for i := 0; i < 32; i++ {
		owner.NotifyHarnessFailure()
	}
	select {
	case <-owner.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("export did not finish")
	}
	owner.NotifyHarnessFailure()
	owner.RequestStop()
	if mode != "complete_default" && opens.Load() != 1 {
		t.Fatal("notification created more than one exporter")
	}
}

type r28OwnerFaultFile struct {
	file *os.File
	mode string
	dir  string
	once sync.Once
}

func (w *r28OwnerFaultFile) Write(p []byte) (int, error) {
	switch w.mode {
	case "write_error":
		return 0, errors.New("fixture write failure")
	case "short_write":
		if len(p) == 0 {
			return 0, nil
		}
		return w.file.Write(p[:len(p)-1])
	case "cap_error":
		// IO injection uses the actual cap adapter with a zero remaining
		// budget; no extra workload is generated merely to fill 16 MiB.
		return newWorkerRuntimeTraceCappedWriter(w.file, 0).Write(p)
	case "blocked_stop", "blocked_unexpected":
		w.once.Do(func() {
			_ = os.WriteFile(filepath.Join(w.dir, "writer-entered"), []byte("1"), 0600)
		})
		select {} // Deliberate diagnostic IO fault, only inside owned child.
	default:
		return w.file.Write(p)
	}
}

func (w *r28OwnerFaultFile) Close() error {
	err := w.file.Close()
	if w.mode == "close_error" {
		return errors.New("fixture close failure")
	}
	return err
}

func r28OwnerBlockedChild(t *testing.T, mode, dir string, notify, requestStop func()) {
	generation := &r28OwnerGeneration{done: make(chan error, 1)}
	config := chatlifecycle.WorkerServerConfig{
		ControlToken: "r28-control",
		Factory:      chatlifecycle.WorkerGenerationFactoryFunc(func(chatlifecycle.WorkerAssignment) (chatlifecycle.WorkerGeneration, error) { return generation, nil }),
	}
	field := reflect.ValueOf(&config).Elem().FieldByName("ReportHarnessFailure")
	if !field.IsValid() || !field.CanSet() || field.Type() != reflect.TypeOf((func())(nil)) {
		t.Fatal("missing proposed narrow WorkerServer harness notification")
	}
	field.Set(reflect.ValueOf(notify))
	worker, err := chatlifecycle.NewWorkerServer(config)
	if err != nil {
		t.Fatal("worker server fixture constructor failed")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal("fixture listener failed")
	}
	httpServer := &http.Server{Handler: worker, ReadHeaderTimeout: time.Second}
	workerRuntime := &r28OwnerHTTPRuntime{server: httpServer, listener: listener}
	if err := os.WriteFile(filepath.Join(dir, "http-address"), []byte(listener.Addr().String()), 0600); err != nil {
		t.Fatal("fixture address receipt failed")
	}
	go func() {
		var signal [1]byte
		if _, readErr := io.ReadFull(os.Stdin, signal[:]); readErr != nil {
			return
		}
		if mode == "blocked_unexpected" {
			generation.terminate()
		} else {
			_ = httpServer.Close()
		}
	}()
	err = waitChatLifecycleWorker(workerRuntime, worker.UnexpectedExit())
	if mode == "blocked_unexpected" {
		if !errors.Is(err, errChatLifecycleWorkerUnexpected) {
			t.Fatal("original UnexpectedExit outcome changed")
		}
	} else if !errors.Is(err, http.ErrServerClosed) {
		t.Fatal("original Serve termination outcome changed")
	}
	requestStop() // Must not wait for the intentionally blocked writer.
	if err := os.WriteFile(filepath.Join(dir, "wait-returned"), []byte(mode), 0600); err != nil {
		t.Fatal("fixture terminal receipt failed")
	}
	// This is the process boundary the CLI owns. It deliberately does not
	// wait for Done or claim the diagnostic writer joined before exit.
	os.Exit(0)
}

type r28OwnerHTTPRuntime struct {
	server   *http.Server
	listener net.Listener
}

func (r *r28OwnerHTTPRuntime) Serve() error                       { return r.server.Serve(r.listener) }
func (r *r28OwnerHTTPRuntime) Shutdown(ctx context.Context) error { return r.server.Shutdown(ctx) }

// This generation supplies only fixed protocol evidence. It never runs an
// Engine, SDK, product process, timer, RPC, or native traffic workload.
type r28OwnerGeneration struct {
	done chan error
	once sync.Once
}

func (*r28OwnerGeneration) Start(context.Context) error                      { return nil }
func (*r28OwnerGeneration) UpdateRate(context.Context, uint64, uint64) error { return nil }
func (*r28OwnerGeneration) ApplyGrant(context.Context, uint64) (chatlifecycle.WorkerGrantApplication, error) {
	return chatlifecycle.WorkerGrantApplication{Admitted: true}, nil
}
func (*r28OwnerGeneration) TrafficReady() bool { return true }
func (g *r28OwnerGeneration) Checkpoint(ctx context.Context) (chatlifecycle.WorkerSnapshot, error) {
	return g.Snapshot(ctx)
}
func (*r28OwnerGeneration) Drain(context.Context) error { return nil }
func (g *r28OwnerGeneration) Stop()                     { g.once.Do(func() { g.done <- nil; close(g.done) }) }
func (g *r28OwnerGeneration) Done() <-chan error        { return g.done }
func (g *r28OwnerGeneration) terminate() {
	g.once.Do(func() { g.done <- errors.New("fixture generation terminated"); close(g.done) })
}
func (*r28OwnerGeneration) Snapshot(context.Context) (chatlifecycle.WorkerSnapshot, error) {
	return chatlifecycle.WorkerSnapshot{
		Harness: chatlifecycle.WorkerHarnessSnapshot{Failures: 1, OfferedUnderdelivery: 1, Classification: chatlifecycle.SyncClassificationHarnessInvalid},
		Evidence: chatlifecycle.EvidenceSnapshot{
			Classification: chatlifecycle.SyncClassificationHarnessInvalid,
			Classes: []chatlifecycle.EvidenceClassSnapshot{{
				Class: chatlifecycle.FailureClassHarness, Count: 1,
				First: []chatlifecycle.EvidenceExample{{Stage: chatlifecycle.EvidenceStageCapacity, Code: chatlifecycle.FailureCode(38), Value: 1}},
			}},
		},
	}, nil
}

type r28OwnerChild struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	done   chan struct{}
	err    error
	output *r28OwnerBoundedBuffer
	cancel context.CancelFunc
}

func r28OwnerStartChild(t *testing.T, mode, dir string) *r28OwnerChild {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal("resolve test executable failed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestWorkerRuntimeTraceOwnerChild$", "-test.timeout=10s", "-test.count=1")
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "WK_") {
			cmd.Env = append(cmd.Env, value)
		}
	}
	cmd.Env = append(cmd.Env, "WK_R28_OWNER_CHILD="+mode, "WK_R28_OWNER_WORKDIR="+dir)
	output := &r28OwnerBoundedBuffer{limit: 32 << 10}
	cmd.Stdout, cmd.Stderr = output, output
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		t.Fatal("child stdin failed")
	}
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatal("child start failed")
	}
	child := &r28OwnerChild{cmd: cmd, stdin: stdin, done: make(chan struct{}), output: output, cancel: cancel}
	go func() { child.err = cmd.Wait(); close(child.done) }()
	t.Cleanup(func() {
		_ = stdin.Close()
		cancel()
		_ = cmd.Process.Kill()
		select {
		case <-child.done:
		case <-time.After(2 * time.Second):
			t.Error("owned fixture process did not join after kill")
		}
	})
	return child
}

func (child *r28OwnerChild) wait(t *testing.T) {
	t.Helper()
	select {
	case <-child.done:
		child.cancel()
		if child.err != nil || child.output.overflow {
			t.Fatalf("owner child failed or exceeded bounded output: failed=%t overflow=%t\n%s", child.err != nil, child.output.overflow, child.output.buf.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("owner child lifetime exceeded fixed bound")
	}
}

// exec.Cmd serializes writes to a shared stdout/stderr writer. This buffer
// bounds retained tool output without blocking the subprocess on overflow.
type r28OwnerBoundedBuffer struct {
	buf      bytes.Buffer
	limit    int
	overflow bool
}

func (b *r28OwnerBoundedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remaining := b.limit - b.buf.Len()
	if remaining < len(p) {
		b.overflow = true
		p = p[:remaining]
	}
	_, _ = b.buf.Write(p)
	return n, nil
}

func r28OwnerArtifactDir(t *testing.T, mode string) string {
	t.Helper()
	root := os.Getenv("WK_R28_TRACE_TEST_ARTIFACT_ROOT")
	if root == "" {
		return t.TempDir()
	}
	if !filepath.IsAbs(root) {
		t.Fatal("artifact root must be absolute")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal("artifact root creation failed")
	}
	dir, err := os.MkdirTemp(root, "owner-"+mode+"-")
	if err != nil {
		t.Fatal("artifact directory creation failed")
	}
	return dir
}

func r28OwnerReadSmall(t *testing.T, path string, limit int64) []byte {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal("expected fixture artifact absent")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(data)) > limit {
		t.Fatal("fixture artifact read failed or exceeded bound")
	}
	return data
}

func r28OwnerAwaitFile(t *testing.T, path string, limit int64) []byte {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		if _, err := os.Stat(path); err == nil {
			return r28OwnerReadSmall(t, path, limit)
		}
		select {
		case <-deadline.C:
			t.Fatal("fixture handshake exceeded bound")
		case <-tick.C:
		}
	}
}

func r28OwnerReadReceipt(t *testing.T, dir string) r28OwnerReceipt {
	t.Helper()
	traceDir := filepath.Join(dir, "runtime-trace")
	info, err := os.Lstat(traceDir)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		t.Fatal("trace directory is not private")
	}
	data := r28OwnerReadSmall(t, filepath.Join(traceDir, "status.json"), 4096)
	statusInfo, err := os.Lstat(filepath.Join(traceDir, "status.json"))
	if err != nil || !statusInfo.Mode().IsRegular() || statusInfo.Mode().Perm() != 0600 {
		t.Fatal("trace receipt is not private and regular")
	}
	var receipt r28OwnerReceipt
	if json.Unmarshal(data, &receipt) != nil {
		t.Fatal("invalid trace receipt")
	}
	if receipt.Schema != "wukongim/chat-lifecycle-worker-runtime-trace/v1" || receipt.Coverage != "unknown" || receipt.OutputLimitBytes != r28OwnerTraceLimit || receipt.Bytes < 0 || receipt.Bytes > r28OwnerTraceLimit {
		t.Fatal("receipt schema, unknown coverage, or hard output cap changed")
	}
	return receipt
}

func r28OwnerHTTP(t *testing.T, client *http.Client, addr, method, path string, body, destination any) {
	t.Helper()
	var payload io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal("fixture request encoding failed")
		}
		payload = bytes.NewReader(data)
	}
	request, err := http.NewRequest(method, "http://"+addr+path, payload)
	if err != nil {
		t.Fatal("fixture request construction failed")
	}
	request.Header.Set("Authorization", "Bearer r28-control")
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal("worker HTTP did not return within fixed test bound")
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, (256<<10)+1))
	if err != nil || len(data) > 256<<10 || response.StatusCode != http.StatusOK {
		t.Fatal("worker HTTP fixture failed or exceeded bounded response")
	}
	if destination != nil && json.Unmarshal(data, destination) != nil {
		t.Fatal("worker HTTP fixture response invalid")
	}
}

func r28OwnerDecode(t *testing.T, tracePath, decodedPath string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, filepath.Join(runtime.GOROOT(), "bin", "go"), "tool", "trace", "-d=parsed", tracePath)
	output := &r28OwnerBoundedBuffer{limit: 1 << 20}
	command.Stdout, command.Stderr = output, output
	if err := command.Run(); err != nil || output.overflow || output.buf.Len() == 0 {
		t.Fatalf("actual Go1.25 trace decoder failed/overflowed/empty: failed=%t overflow=%t", err != nil, output.overflow)
	}
	if err := os.WriteFile(decodedPath, output.buf.Bytes(), 0600); err != nil {
		t.Fatal("decoded trace artifact write failed")
	}
	// Decoding validates a real runtime stream. Running-state wall time is
	// not pure CPU time; this test adds no CPU profiler or physical-cause claim.
	if !bytes.Contains(output.buf.Bytes(), []byte("StateTransition")) {
		t.Fatal("decoded trace contains no runtime state transitions")
	}
}
