package benchmark

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime/trace"
	"sync/atomic"
	"time"
)

const (
	workerRuntimeTraceOutputLimit int64 = 16 << 20
	// Each receipt is at most half the total receipt budget, so the old
	// receipt plus its atomic replacement remain within 4 KiB together.
	workerRuntimeTraceReceiptLimit = 2 << 10
)

var (
	errWorkerRuntimeTraceDirectory = errors.New("worker runtime trace requires an existing absolute non-symlink work directory")
	errWorkerRuntimeTraceOutput    = errors.New("worker runtime trace output initialization failed")
	errWorkerRuntimeTraceStart     = errors.New("worker runtime trace recorder start failed")
	errWorkerRuntimeTraceLimit     = errors.New("worker runtime trace output limit exceeded")
)

// workerRuntimeTrace owns one optional process-local flight recorder. Its
// goroutine alone starts, exports and stops the recorder. CLI shutdown only
// requests stop: it must never wait for diagnostic IO or recorder shutdown.
type workerRuntimeTrace struct {
	directory   string
	openPartial func(string) (io.WriteCloser, error)
	notify      chan struct{}
	stop        chan struct{}
	done        chan struct{}
	// notified admits one export for the entire worker process lifetime.
	notified atomic.Bool
	// stopRequested closes stop once and fences notifications without joining.
	stopRequested atomic.Bool
}

type workerRuntimeTraceReceipt struct {
	Schema   string `json:"schema"`
	State    string `json:"state"`
	Reason   string `json:"reason"`
	Bytes    int64  `json:"bytes"`
	SHA256   string `json:"sha256"`
	Coverage string `json:"coverage"`
	// WriterJoined records returned export/recorder ownership, not durability
	// or successful file closure after an output_close_failed result.
	WriterJoined     bool  `json:"writer_joined"`
	OutputLimitBytes int64 `json:"output_limit_bytes"`
}

// newWorkerRuntimeTrace is called only after the worker's explicit opt-in.
// Startup waits for recorder admission and the private pending receipt, before
// HTTP Serve. The optional factory is a trusted diagnostic-IO test seam only.
func newWorkerRuntimeTrace(workDir string, openPartial func(string) (io.WriteCloser, error)) (*workerRuntimeTrace, error) {
	if !filepath.IsAbs(workDir) {
		return nil, errWorkerRuntimeTraceDirectory
	}
	workDir = filepath.Clean(workDir)
	info, err := os.Lstat(workDir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errWorkerRuntimeTraceDirectory
	}
	directory := filepath.Join(workDir, "runtime-trace")
	// Mkdir must be exclusive. Existing output is never reused or removed.
	if err := os.Mkdir(directory, 0700); err != nil {
		return nil, errWorkerRuntimeTraceOutput
	}
	if openPartial == nil {
		openPartial = func(path string) (io.WriteCloser, error) {
			return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		}
	}
	owner := &workerRuntimeTrace{
		directory:   directory,
		openPartial: openPartial,
		notify:      make(chan struct{}, 1),
		stop:        make(chan struct{}),
		done:        make(chan struct{}),
	}
	ready := make(chan error, 1)
	go owner.run(ready)
	if err := <-ready; err != nil {
		return nil, err
	}
	return owner, nil
}

// NotifyHarnessFailure admits at most one export and performs no IO. In
// particular a blocked writer cannot hold up an authenticated worker snapshot.
func (o *workerRuntimeTrace) NotifyHarnessFailure() {
	if o == nil || o.stopRequested.Load() || !o.notified.CompareAndSwap(false, true) {
		return
	}
	select {
	case o.notify <- struct{}{}:
	default:
	}
}

// RequestStop is deliberately nonblocking, including during WriteTo and Stop.
// Process exit can leave pending/partial unjoined evidence; callers do not join.
func (o *workerRuntimeTrace) RequestStop() {
	if o != nil && o.stopRequested.CompareAndSwap(false, true) {
		close(o.stop)
	}
}

// Done is for explicit diagnostic ownership checks, never the CLI exit path.
func (o *workerRuntimeTrace) Done() <-chan struct{} { return o.done }

func (o *workerRuntimeTrace) run(ready chan<- error) {
	defer close(o.done)
	recorder := trace.NewFlightRecorder(trace.FlightRecorderConfig{
		// These are retention hints, not hard memory or coverage guarantees.
		MaxBytes: 8 << 20,
		MinAge:   10 * time.Second,
	})
	if err := recorder.Start(); err != nil {
		ready <- errWorkerRuntimeTraceStart
		return
	}
	receipt := workerRuntimeTraceReceipt{
		Schema:           "wukongim/chat-lifecycle-worker-runtime-trace/v1",
		State:            "pending",
		Reason:           "awaiting_failure",
		Coverage:         "unknown",
		OutputLimitBytes: workerRuntimeTraceOutputLimit,
	}
	if err := o.writeReceipt(receipt); err != nil {
		recorder.Stop()
		ready <- errWorkerRuntimeTraceOutput
		return
	}
	ready <- nil
	select {
	case <-o.stop:
		o.finishWithoutExport(recorder, receipt)
		return
	default:
	}
	select {
	case <-o.stop:
		o.finishWithoutExport(recorder, receipt)
	case <-o.notify:
		// A stop already requested before export takes precedence. Once
		// WriteTo begins, only this owner can finish it and stop the recorder.
		if o.stopRequested.Load() {
			o.finishWithoutExport(recorder, receipt)
			return
		}
		o.export(recorder, receipt)
	}
}

func (o *workerRuntimeTrace) finishWithoutExport(recorder *trace.FlightRecorder, receipt workerRuntimeTraceReceipt) {
	recorder.Stop()
	receipt.State = "stopped"
	receipt.Reason = "stopped_before_export"
	receipt.WriterJoined = true
	_ = o.writeReceipt(receipt)
}

func (o *workerRuntimeTrace) export(recorder *trace.FlightRecorder, receipt workerRuntimeTraceReceipt) {
	partialPath := filepath.Join(o.directory, "trace.partial")
	output, err := o.openPartial(partialPath)
	if err != nil || output == nil {
		if output != nil {
			_ = output.Close()
		}
		recorder.Stop()
		receipt.State = "partial"
		receipt.Reason = "output_open_failed"
		receipt.WriterJoined = true
		_ = o.writeReceipt(receipt)
		return
	}
	hash := sha256.New()
	writer := newWorkerRuntimeTraceCappedWriter(io.MultiWriter(output, hash), workerRuntimeTraceOutputLimit).(*workerRuntimeTraceCappedWriter)
	// This static marker separates export overhead from the retained pre-cut
	// runtime view. It adds no business clock, identity, task or counter.
	trace.Log(context.Background(), "wkbench.runtime_trace", "export_begin")
	_, writeErr := recorder.WriteTo(writer)
	closeErr := output.Close()
	// WriteTo must run while active. Stop, which itself can block, belongs to
	// this goroutine after WriteTo and output Close, never to CLI shutdown.
	recorder.Stop()
	receipt.WriterJoined = true
	receipt.Bytes = writer.written
	receipt.State = "partial"
	switch {
	case errors.Is(writeErr, errWorkerRuntimeTraceLimit):
		receipt.Reason = "output_limit"
	case writeErr != nil:
		receipt.Reason = "output_write_failed"
	case closeErr != nil:
		receipt.Reason = "output_close_failed"
	default:
		info, statErr := os.Lstat(partialPath)
		if statErr != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() != writer.written || writer.written == 0 {
			receipt.Reason = "output_validation_failed"
			break
		}
		outputPath := filepath.Join(o.directory, "trace.out")
		if _, statErr := os.Lstat(outputPath); !errors.Is(statErr, os.ErrNotExist) {
			receipt.Reason = "output_rename_failed"
			break
		}
		if err := os.Rename(partialPath, outputPath); err != nil {
			receipt.Reason = "output_rename_failed"
			break
		}
		receipt.State = "complete"
		receipt.Reason = "export_complete"
		receipt.SHA256 = hex.EncodeToString(hash.Sum(nil))
		if err := o.writeReceipt(receipt); err != nil {
			// A complete filename alone is not a committed capture. Restore
			// the partial name if receipt publication fails; if that fails,
			// remove the completed name rather than preserve false success.
			if err := os.Rename(outputPath, partialPath); err != nil {
				_ = os.Remove(outputPath)
			}
			receipt.State = "partial"
			receipt.Reason = "receipt_write_failed"
			receipt.SHA256 = ""
			_ = o.writeReceipt(receipt)
		}
		return
	}
	_ = o.writeReceipt(receipt)
}

// writeReceipt publishes only after bounded write and Close both succeed. The
// previous receipt stays authoritative on failure; absent/unreadable output is
// unknown. There is no durability claim and no second copy of trace data.
func (o *workerRuntimeTrace) writeReceipt(receipt workerRuntimeTraceReceipt) error {
	payload, err := json.Marshal(receipt)
	if err != nil || len(payload) > workerRuntimeTraceReceiptLimit {
		return errWorkerRuntimeTraceOutput
	}
	nextPath := filepath.Join(o.directory, "status.next")
	next, err := os.OpenFile(nextPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return errWorkerRuntimeTraceOutput
	}
	n, writeErr := next.Write(payload)
	closeErr := next.Close()
	if writeErr != nil || n != len(payload) || closeErr != nil {
		_ = os.Remove(nextPath)
		return errWorkerRuntimeTraceOutput
	}
	if err := os.Rename(nextPath, filepath.Join(o.directory, "status.json")); err != nil {
		_ = os.Remove(nextPath)
		return errWorkerRuntimeTraceOutput
	}
	return nil
}

// workerRuntimeTraceCappedWriter bounds accepted trace bytes and makes short
// writes explicit. It has one owner and keeps no copy of the trace stream.
type workerRuntimeTraceCappedWriter struct {
	writer    io.Writer
	remaining int64
	written   int64
	err       error
}

func newWorkerRuntimeTraceCappedWriter(writer io.Writer, limit int64) io.Writer {
	if limit < 0 {
		limit = 0
	}
	return &workerRuntimeTraceCappedWriter{writer: writer, remaining: limit}
}

func (w *workerRuntimeTraceCappedWriter) Write(payload []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	if len(payload) == 0 {
		return 0, nil
	}
	if w.remaining == 0 {
		w.err = errWorkerRuntimeTraceLimit
		return 0, w.err
	}
	overflow := int64(len(payload)) > w.remaining
	if overflow {
		payload = payload[:w.remaining]
	}
	n, err := w.writer.Write(payload)
	if n < 0 || n > len(payload) {
		w.err = io.ErrShortWrite
		return 0, w.err
	}
	w.remaining -= int64(n)
	w.written += int64(n)
	if err == nil && n != len(payload) {
		err = io.ErrShortWrite
	}
	if err == nil && overflow {
		err = errWorkerRuntimeTraceLimit
	}
	w.err = err
	return n, err
}
