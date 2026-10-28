package benchmark

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/WuKongIM/WuKongIM/internal/bench/chatlifecycle"
)

const driverCPUExportLimit = 64 << 10

var errDriverCPUExportDirectory = errors.New("private driver CPU export requires a new output directory under an existing absolute work directory")

// driverCPUExporter owns one optional post-join report and one bounded writer.
// Production requests stop but never joins diagnostic IO through Done.
type driverCPUExporter struct {
	directory   string
	openPartial func(string) (io.WriteCloser, error)
	// reports owns detached values handed off by the generation's nonblocking send.
	// One later report may remain buffered after the first export terminates.
	reports       chan chatlifecycle.DriverCPUReport
	stop          chan struct{}
	done          chan struct{}
	stopRequested atomic.Bool
}

// newDriverCPUExporter exclusively owns a private directory before serving.
// openPartial is a trusted test seam; ordinary export uses an exclusive file.
func newDriverCPUExporter(workDir string, openPartial func(string) (io.WriteCloser, error)) (*driverCPUExporter, error) {
	if !filepath.IsAbs(workDir) {
		return nil, errDriverCPUExportDirectory
	}
	workDir = filepath.Clean(workDir)
	info, err := os.Lstat(workDir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errDriverCPUExportDirectory
	}
	directory := filepath.Join(workDir, "driver-cpu")
	if err := os.Mkdir(directory, 0700); err != nil {
		return nil, errDriverCPUExportDirectory
	}
	if err := os.Chmod(directory, 0700); err != nil {
		return nil, errDriverCPUExportDirectory
	}
	if openPartial == nil {
		openPartial = func(path string) (io.WriteCloser, error) {
			return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		}
	}
	owner := &driverCPUExporter{
		directory: directory, openPartial: openPartial,
		reports: make(chan chatlifecycle.DriverCPUReport, 1), stop: make(chan struct{}), done: make(chan struct{}),
	}
	go owner.run()
	return owner, nil
}

// Reports is a capacity-one inbox; the factory must always send with default.
// Its producer owns detach-before-handoff and never mutates a sent snapshot.
func (o *driverCPUExporter) Reports() chan<- chatlifecycle.DriverCPUReport {
	if o == nil {
		return nil
	}
	return o.reports
}

// Done is only for explicit diagnostic ownership checks, never native Stop.
func (o *driverCPUExporter) Done() <-chan struct{} {
	if o == nil {
		return nil
	}
	return o.done
}

// RequestStop releases an idle owner without joining an in-progress writer.
func (o *driverCPUExporter) RequestStop() {
	if o != nil && o.stopRequested.CompareAndSwap(false, true) {
		close(o.stop)
	}
}

func (o *driverCPUExporter) run() {
	defer close(o.done)
	// A report admitted before RequestStop must not be lost to select order.
	select {
	case report := <-o.reports:
		o.export(report)
		return
	default:
	}
	select {
	case report := <-o.reports:
		o.export(report)
	case <-o.stop:
		select {
		case report := <-o.reports:
			o.export(report)
		default:
		}
	}
}

// export keeps optional malformed or incomplete observations unknown. It never
// retries, logs raw failures, mutates the submitted report, or changes a gate.
func (o *driverCPUExporter) export(report chatlifecycle.DriverCPUReport) {
	if !validDriverCPUExportReport(report) {
		return
	}
	envelope := struct {
		Schema string                        `json:"schema"`
		Report chatlifecycle.DriverCPUReport `json:"report"`
	}{Schema: "wukongim/chat-lifecycle-driver-cpu-diagnostics/v1", Report: report}
	body, err := json.Marshal(envelope)
	if err != nil || len(body) > driverCPUExportLimit {
		return
	}
	partial := filepath.Join(o.directory, "diagnostics.partial")
	output, err := o.openPartial(partial)
	if err != nil || output == nil {
		if output != nil {
			_ = output.Close()
		}
		return
	}
	n, writeErr := output.Write(body)
	closeErr := output.Close()
	if writeErr != nil || n != len(body) || closeErr != nil {
		return
	}
	info, err := os.Lstat(partial)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() != int64(len(body)) {
		return
	}
	final := filepath.Join(o.directory, "diagnostics.json")
	if _, err := os.Lstat(final); !errors.Is(err, os.ErrNotExist) {
		return
	}
	// This directory was exclusively created for this single writer. A
	// successful rename is publication, not a promise of crash durability.
	_ = os.Rename(partial, final)
}

func validDriverCPUExportReport(report chatlifecycle.DriverCPUReport) bool {
	if !validDriverCPUExportHash(report.RunHash) || !validDriverCPUExportHash(report.AssignmentHash) || report.WorkerID >= 3 ||
		report.Diagnostics == nil || report.Diagnostics.Generation == 0 || len(report.Diagnostics.Records) > 16 {
		return false
	}
	var previous uint64
	for _, record := range report.Diagnostics.Records {
		if record.Turn == 0 || record.Turn <= previous || (record.Operation != "step" && record.Operation != "advance") ||
			(record.Code != 37 && record.Code != 38) {
			return false
		}
		previous = record.Turn
		if record.State == "known" {
			if record.Reason != "none" || record.ProcessCPUUpperBoundNanos < 0 {
				return false
			}
		} else if record.State == "unknown" {
			if record.ProcessCPUUpperBoundNanos != 0 {
				return false
			}
			switch record.Reason {
			case "read_failed", "negative_clock", "monotonic_regressed", "process_cpu_regressed", "bracket_wide":
			default:
				return false
			}
		} else {
			return false
		}
	}
	return true
}

func validDriverCPUExportHash(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, digit := range value[len("sha256:"):] {
		if (digit < '0' || digit > '9') && (digit < 'a' || digit > 'f') {
			return false
		}
	}
	return true
}
