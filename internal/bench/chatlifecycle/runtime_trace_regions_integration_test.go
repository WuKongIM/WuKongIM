//go:build integration

package chatlifecycle

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/trace"
	"strings"
	"sync"
	"testing"
	"time"
)

type r28DecodeBuffer struct {
	mu sync.Mutex
	bytes.Buffer
	truncated bool
}

func (b *r28DecodeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	remaining := (1 << 20) - b.Buffer.Len()
	if len(p) > remaining {
		b.truncated = true
		p = p[:remaining]
	}
	_, _ = b.Buffer.Write(p)
	return n, nil
}

type r28RegionWriter struct {
	target    io.Writer
	remaining int64
}

func (w *r28RegionWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.remaining {
		return 0, errors.New("test trace output limit")
	}
	n, err := w.target.Write(p)
	w.remaining -= int64(n)
	return n, err
}

// The helper produces an actual Go1.25 FlightRecorder stream around real
// Engine Step/Advance calls. It adds no fake delay or new business clock.
func TestWorkerRuntimeTraceRegionChild(t *testing.T) {
	if os.Getenv("WK_R28_REGION_CHILD") != "1" {
		return
	}
	output := os.Getenv("WK_R28_REGION_OUTPUT")
	recorder := trace.NewFlightRecorder(trace.FlightRecorderConfig{MaxBytes: 8 << 20, MinAge: 10 * time.Second})
	if err := recorder.Start(); err != nil {
		t.Fatalf("FlightRecorder.Start: %T", err)
	}
	defer recorder.Stop()
	f, _, _, at := newDriverTestFixture(t, true, nil)
	if _, err := f.engine.AdvanceContext(context.Background(), at); err != nil {
		t.Fatal(err)
	}
	if _, err := f.engine.Step(context.Background(), at, nil); err != nil {
		t.Fatal(err)
	}
	if err := f.engine.Stop(); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(output+".partial", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, exportErr := recorder.WriteTo(&r28RegionWriter{target: file, remaining: 16 << 20})
	closeErr := file.Close()
	// The real API requires active WriteTo before Stop. A second Stop is inert.
	recorder.Stop()
	if exportErr != nil || closeErr != nil {
		t.Fatalf("trace export failed: %T/%T", exportErr, closeErr)
	}
	if err := os.Rename(output+".partial", output); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerRuntimeTraceRegionsDecodeActualExpiryStepAndAdvance(t *testing.T) {
	root := os.Getenv("WK_R28_TRACE_TEST_ARTIFACT_ROOT")
	if root == "" {
		root = t.TempDir()
	} else {
		if !filepath.IsAbs(root) {
			t.Fatal("test artifact root must be absolute")
		}
		root = filepath.Join(root, "engine-regions")
		if err := os.Mkdir(root, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	output := filepath.Join(root, "trace.out")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestWorkerRuntimeTraceRegionChild$", "-test.timeout=6s")
	cmd.Env = append(os.Environ(), "WK_R28_REGION_CHILD=1", "WK_R28_REGION_OUTPUT="+output)
	var childOutput r28DecodeBuffer
	cmd.Stdout, cmd.Stderr = &childOutput, &childOutput
	if err := cmd.Run(); err != nil || childOutput.truncated {
		t.Fatalf("real trace child failed or truncated: %T", err)
	}
	info, err := os.Lstat(output)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 16<<20 || info.Mode().Perm() != 0o600 {
		t.Fatal("trace artifact identity/size/permissions invalid")
	}
	decodeCtx, decodeCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer decodeCancel()
	decoder := exec.CommandContext(decodeCtx, filepath.Join(runtime.GOROOT(), "bin", "go"), "tool", "trace", "-d=parsed", output)
	var decoded r28DecodeBuffer
	decoder.Stdout, decoder.Stderr = &decoded, &decoded
	if err := decoder.Run(); err != nil || decoded.truncated {
		t.Fatalf("actual Go trace decode failed/truncated: %T", err)
	}
	if err := os.WriteFile(filepath.Join(root, "decoded.txt"), decoded.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"wkbench.session_expiry.step", "wkbench.session_expiry.advance"} {
		if !strings.Contains(decoded.String(), name) {
			t.Errorf("actual trace lacks static region %s", name)
		}
	}
	encoded, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("actual_runtime_trace path=%s bytes=%d sha256=%x decode=parsed original_historical_cause=unknown running_is_wall=true", output, len(encoded), sha256.Sum256(encoded))
}
