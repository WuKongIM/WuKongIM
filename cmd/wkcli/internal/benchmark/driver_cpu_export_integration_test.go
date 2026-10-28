//go:build integration

package benchmark

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/chatlifecycle"
)

const driverCPUExportTestLimit int64 = 64 << 10

func driverCPUExportTestReport() chatlifecycle.DriverCPUReport {
	return chatlifecycle.DriverCPUReport{
		RunHash: "sha256:" + strings.Repeat("a", 64), AssignmentHash: "sha256:" + strings.Repeat("b", 64), WorkerID: 2,
		Diagnostics: &chatlifecycle.DriverCPUDiagnosticsSnapshot{
			Generation: 7,
			Records: []chatlifecycle.DriverCPUDeadlineMiss{{
				Turn: 41, Operation: "advance", Code: 38,
				OwnerEntry: chatlifecycle.DriverCPUClockSample{MonotonicBeforeNanos: 1000, ProcessCPUNanos: 200, MonotonicAfterNanos: 1010, ReadMask: 7},
				Miss:       chatlifecycle.DriverCPUClockSample{MonotonicBeforeNanos: 1100, ProcessCPUNanos: 250, MonotonicAfterNanos: 1110, ReadMask: 7},
				State:      "known", Reason: "none", ProcessCPUUpperBoundNanos: 50,
			}},
		},
	}
}

func driverCPUExportWait(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("private exporter did not return its bounded owner")
	}
}

func driverCPUExportRead(t *testing.T, workDir string) (chatlifecycle.DriverCPUReport, []byte) {
	t.Helper()
	directory := filepath.Join(workDir, "driver-cpu")
	dirInfo, err := os.Lstat(directory)
	if err != nil || !dirInfo.IsDir() || dirInfo.Mode().Perm() != 0700 {
		t.Fatal("private exporter directory is missing, not private, or not a directory")
	}
	path := filepath.Join(directory, "diagnostics.json")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() <= 0 || info.Size() > driverCPUExportTestLimit {
		t.Fatal("completed sidecar is absent, nonregular, public, empty, or above its hard bound")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("completed private sidecar was unreadable")
	}
	var envelope struct {
		Schema string                        `json:"schema"`
		Report chatlifecycle.DriverCPUReport `json:"report"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil || envelope.Schema != "wukongim/chat-lifecycle-driver-cpu-diagnostics/v1" {
		t.Fatal("completed sidecar is not the fixed CPU report envelope")
	}
	if _, err := os.Lstat(filepath.Join(directory, "diagnostics.partial")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("successful publication retained a second partial copy")
	}
	digest := sha256.Sum256(body)
	t.Logf("driver_cpu_export path=%s bytes=%d sha256=%s", path, len(body), hex.EncodeToString(digest[:]))
	return envelope.Report, body
}

func TestDriverCPUExporterPublishesExactPrivateFencedReport(t *testing.T) {
	workDir := t.TempDir()
	owner, err := newDriverCPUExporter(workDir, nil)
	if err != nil {
		t.Fatal("private exporter initialization failed")
	}
	defer owner.RequestStop()
	if cap(owner.Reports()) != 1 {
		t.Fatal("CPU report inbox is not bounded to one report")
	}
	want := driverCPUExportTestReport()
	before, _ := json.Marshal(want)
	select {
	case owner.Reports() <- want:
	default:
		t.Fatal("empty private inbox refused its first joined report")
	}
	driverCPUExportWait(t, owner.Done())
	got, body := driverCPUExportRead(t, workDir)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("private fenced report changed: got=%+v want=%+v", got, want)
	}
	after, _ := json.Marshal(want)
	if string(before) != string(after) {
		t.Fatal("exporter mutated the producer-owned detached report")
	}
	for _, forbidden := range []string{"run_id", "assignment_id", "client_msg_no", "channel_id", "payload", "raw_error", "actor_cpu", "pure_cpu"} {
		if strings.Contains(strings.ToLower(string(body)), forbidden) {
			t.Fatalf("private sidecar acquired an identity or unsupported attribution field: %s", forbidden)
		}
	}
}

func TestDriverCPUExporterStopDrainsPreviouslyQueuedReport(t *testing.T) {
	workDir := t.TempDir()
	owner, err := newDriverCPUExporter(workDir, nil)
	if err != nil {
		t.Fatal("private exporter initialization failed")
	}
	want := driverCPUExportTestReport()
	owner.Reports() <- want
	owner.RequestStop()
	owner.RequestStop()
	driverCPUExportWait(t, owner.Done())
	got, _ := driverCPUExportRead(t, workDir)
	if !reflect.DeepEqual(got, want) {
		t.Fatal("stop discarded or changed an already joined queued report")
	}
}

func TestDriverCPUExporterStopWithoutReportExitsWithoutInventedEvidence(t *testing.T) {
	workDir := t.TempDir()
	owner, err := newDriverCPUExporter(workDir, nil)
	if err != nil {
		t.Fatal("private exporter initialization failed")
	}
	owner.RequestStop()
	owner.RequestStop()
	driverCPUExportWait(t, owner.Done())
	select {
	case owner.Reports() <- driverCPUExportTestReport():
	default:
	}
	if _, err := os.Lstat(filepath.Join(workDir, "driver-cpu", "diagnostics.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("stopped exporter published a late report or invented completion")
	}
}

func TestDriverCPUExporterBlockedWriterNeverHoldsControlOrCreatesMoreWriters(t *testing.T) {
	for _, boundary := range []string{"open", "write", "close"} {
		t.Run(boundary, func(t *testing.T) {
			workDir := t.TempDir()
			entered := make(chan struct{})
			release := make(chan struct{})
			var releaseOnce sync.Once
			var enteredOnce sync.Once
			var opens atomic.Int64
			block := func() { enteredOnce.Do(func() { close(entered) }); <-release }
			owner, err := newDriverCPUExporter(workDir, func(path string) (io.WriteCloser, error) {
				opens.Add(1)
				if boundary == "open" {
					block()
				}
				file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
				if err != nil {
					return nil, err
				}
				return &driverCPUExportBlockingWriter{File: file, boundary: boundary, block: block}, nil
			})
			if err != nil {
				t.Fatal("private exporter initialization failed")
			}
			t.Cleanup(func() {
				releaseOnce.Do(func() { close(release) })
				owner.RequestStop()
				driverCPUExportWait(t, owner.Done())
			})
			want := driverCPUExportTestReport()
			owner.Reports() <- want
			driverCPUExportWait(t, entered)
			returned := make(chan struct{})
			go func() {
				for i := 0; i < 1000; i++ {
					select {
					case owner.Reports() <- driverCPUExportTestReport():
					default:
					}
				}
				owner.RequestStop()
				owner.RequestStop()
				close(returned)
			}()
			driverCPUExportWait(t, returned)
			select {
			case <-owner.Done():
				t.Fatal("blocked IO was falsely reported as a joined exporter")
			default:
			}
			releaseOnce.Do(func() { close(release) })
			driverCPUExportWait(t, owner.Done())
			if opens.Load() != 1 {
				t.Fatalf("repeated reports created %d writers", opens.Load())
			}
			got, _ := driverCPUExportRead(t, workDir)
			if !reflect.DeepEqual(got, want) {
				t.Fatal("queued notifications replaced the first report")
			}
		})
	}
}

type driverCPUExportBlockingWriter struct {
	*os.File
	boundary string
	block    func()
}

func (w *driverCPUExportBlockingWriter) Write(p []byte) (int, error) {
	if w.boundary == "write" {
		w.block()
	}
	return w.File.Write(p)
}

func (w *driverCPUExportBlockingWriter) Close() error {
	if w.boundary == "close" {
		w.block()
	}
	return w.File.Close()
}

func TestDriverCPUExporterIOFaultsNeverPublishCompletedFilename(t *testing.T) {
	for _, mode := range []string{"open_error", "nil_writer", "write_error", "short_write", "close_error"} {
		t.Run(mode, func(t *testing.T) {
			workDir := t.TempDir()
			var opens atomic.Int64
			owner, err := newDriverCPUExporter(workDir, func(path string) (io.WriteCloser, error) {
				opens.Add(1)
				if mode == "open_error" {
					return nil, errors.New("private-uid-channel-token-raw-error")
				}
				if mode == "nil_writer" {
					return nil, nil
				}
				file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
				if err != nil {
					return nil, err
				}
				return &driverCPUExportFaultWriter{File: file, mode: mode}, nil
			})
			if err != nil {
				t.Fatal("private exporter initialization failed")
			}
			defer owner.RequestStop()
			owner.Reports() <- driverCPUExportTestReport()
			driverCPUExportWait(t, owner.Done())
			if opens.Load() != 1 {
				t.Fatal("IO fault triggered exporter retries")
			}
			if _, err := os.Lstat(filepath.Join(workDir, "driver-cpu", "diagnostics.json")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("IO fault published a completed sidecar")
			}
			entries, err := os.ReadDir(filepath.Join(workDir, "driver-cpu"))
			if err != nil {
				t.Fatal("private exporter directory disappeared")
			}
			for _, entry := range entries {
				if entry.Name() != "diagnostics.partial" {
					t.Fatal("IO error text became a private artifact name")
				}
				info, err := entry.Info()
				if err != nil || info.Size() > driverCPUExportTestLimit || info.Mode().Perm() != 0600 {
					t.Fatal("partial output exceeded its byte or permission bound")
				}
			}
		})
	}
}

type driverCPUExportFaultWriter struct {
	*os.File
	mode string
}

func (w *driverCPUExportFaultWriter) Write(p []byte) (int, error) {
	if w.mode == "write_error" {
		return 0, errors.New("private-uid-channel-token-raw-error")
	}
	if w.mode == "short_write" {
		return w.File.Write(p[:len(p)/2])
	}
	return w.File.Write(p)
}

func (w *driverCPUExportFaultWriter) Close() error {
	err := w.File.Close()
	if w.mode == "close_error" {
		return errors.New("private-uid-channel-token-raw-error")
	}
	return err
}

func TestDriverCPUExporterRejectsUnsafeWorkDirectoryAndPreservesExistingArtifacts(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("existing"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"relative-workdir", filepath.Join(root, "missing"), file, link} {
		owner, err := newDriverCPUExporter(path, nil)
		if owner != nil {
			owner.RequestStop()
		}
		if err == nil {
			t.Fatal("unsafe work directory was accepted")
		}
	}
	for _, isDirectory := range []bool{false, true} {
		workDir := t.TempDir()
		existing := filepath.Join(workDir, "driver-cpu")
		if isDirectory {
			if err := os.Mkdir(existing, 0700); err != nil {
				t.Fatal(err)
			}
			existing = filepath.Join(existing, "sentinel")
		}
		if err := os.WriteFile(existing, []byte("preserve-exactly"), 0600); err != nil {
			t.Fatal(err)
		}
		owner, err := newDriverCPUExporter(workDir, nil)
		if owner != nil {
			owner.RequestStop()
		}
		if err == nil {
			t.Fatal("existing exporter path was reused")
		}
		body, err := os.ReadFile(existing)
		if err != nil || string(body) != "preserve-exactly" {
			t.Fatal("exclusive initialization changed existing evidence")
		}
	}
}

func TestDriverCPUExporterNeverOverwritesFinalPathConflict(t *testing.T) {
	workDir := t.TempDir()
	owner, err := newDriverCPUExporter(workDir, nil)
	if err != nil {
		t.Fatal("private exporter initialization failed")
	}
	defer owner.RequestStop()
	path := filepath.Join(workDir, "driver-cpu", "diagnostics.json")
	if err := os.WriteFile(path, []byte("existing-final-evidence"), 0600); err != nil {
		t.Fatal(err)
	}
	owner.Reports() <- driverCPUExportTestReport()
	driverCPUExportWait(t, owner.Done())
	body, err := os.ReadFile(path)
	if err != nil || string(body) != "existing-final-evidence" {
		t.Fatal("exporter replaced conflicting final evidence")
	}
}

func TestDriverCPUExporterRejectsUnboundedOrUnfencedReportsBeforeOpeningOutput(t *testing.T) {
	for _, mode := range []string{"nil", "raw_run", "raw_assignment", "uppercase_hash", "worker", "generation", "too_many", "turn", "code", "operation", "state", "reason", "oversized"} {
		t.Run(mode, func(t *testing.T) {
			workDir := t.TempDir()
			var opens atomic.Int64
			owner, err := newDriverCPUExporter(workDir, func(string) (io.WriteCloser, error) { opens.Add(1); return nil, errors.New("unexpected open") })
			if err != nil {
				t.Fatal("private exporter initialization failed")
			}
			defer owner.RequestStop()
			report := driverCPUExportTestReport()
			switch mode {
			case "nil":
				report.Diagnostics = nil
			case "raw_run":
				report.RunHash = "private-run-id"
			case "raw_assignment":
				report.AssignmentHash = "private-assignment-id"
			case "uppercase_hash":
				report.RunHash = "sha256:" + strings.Repeat("A", 64)
			case "worker":
				report.WorkerID = 3
			case "generation":
				report.Diagnostics.Generation = 0
			case "too_many":
				report.Diagnostics.Records = make([]chatlifecycle.DriverCPUDeadlineMiss, 17)
			case "turn":
				report.Diagnostics.Records[0].Turn = 0
			case "code":
				report.Diagnostics.Records[0].Code = 36
			case "operation":
				report.Diagnostics.Records[0].Operation = "private-uid-channel-token-raw-error"
			case "state":
				report.Diagnostics.Records[0].State = "private-uid-channel-token-raw-error"
			case "reason":
				report.Diagnostics.Records[0].Reason = "private-uid-channel-token-raw-error"
			case "oversized":
				report.RunHash = strings.Repeat("private-uid-channel-token-raw-error", 1<<16)
			}
			owner.Reports() <- report
			driverCPUExportWait(t, owner.Done())
			if opens.Load() != 0 {
				t.Fatal("invalid private evidence reached output IO")
			}
			if _, err := os.Lstat(filepath.Join(workDir, "driver-cpu", "diagnostics.json")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("invalid private evidence acquired a completed filename")
			}
		})
	}
}

func TestDriverCPUExporterMaximumOriginalUnknownFactsStayBounded(t *testing.T) {
	workDir := t.TempDir()
	owner, err := newDriverCPUExporter(workDir, nil)
	if err != nil {
		t.Fatal("private exporter initialization failed")
	}
	defer owner.RequestStop()
	report := driverCPUExportTestReport()
	report.Diagnostics.Generation = math.MaxUint64
	report.Diagnostics.DroppedTurns = math.MaxUint64
	report.Diagnostics.Records = nil
	for i := 0; i < 16; i++ {
		report.Diagnostics.Records = append(report.Diagnostics.Records, chatlifecycle.DriverCPUDeadlineMiss{
			Turn: math.MaxUint64 - uint64(15-i), Operation: "advance", Code: 37,
			OwnerEntry: chatlifecycle.DriverCPUClockSample{MonotonicBeforeNanos: math.MaxInt64, ProcessCPUNanos: math.MaxInt64, MonotonicAfterNanos: math.MaxInt64, ReadMask: 7},
			Miss:       chatlifecycle.DriverCPUClockSample{MonotonicBeforeNanos: math.MaxInt64, ProcessCPUNanos: math.MaxInt64 - 1, MonotonicAfterNanos: math.MaxInt64, ReadMask: 7},
			State:      "unknown", Reason: "process_cpu_regressed",
		})
	}
	owner.Reports() <- report
	driverCPUExportWait(t, owner.Done())
	got, _ := driverCPUExportRead(t, workDir)
	if !reflect.DeepEqual(got, report) {
		t.Fatal("maximum original clock facts were truncated or converted into positive CPU evidence")
	}
}
