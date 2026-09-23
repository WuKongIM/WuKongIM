//go:build integration

package scripts_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"
)

const nativeRepositoryDiagnostic = "native-repository-fixture-download-started"
const nativeRepositoryDeadline = "native repository fixture deadline elapsed"

// A package-level panic must preserve streamed diagnostics while the shell is
// still running. Arm the injected deadline only after observing the output;
// interpreter startup time must not decide whether the regression test passes.
func TestNativePackageRepositoryKeepsTimeoutOutput(t *testing.T) {
	root := t.TempDir()
	packages := filepath.Join(root, "packages")
	bin := filepath.Join(root, "bin")
	for _, dir := range []string{packages, bin} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"wukongim-test.deb", "wukongim-test.rpm"} {
		if err := os.WriteFile(filepath.Join(packages, name), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	// Deliberately exceed the old 250 ms package deadline before writing. The
	// long sleep holds the real command open until the parent injects a panic.
	fake := "#!/bin/sh\nsleep 0.35\nprintf '%s\\n' '" + nativeRepositoryDiagnostic + "'\nexec sleep 60\n"
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(fake), 0700); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	deadlineRead, deadlineWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer deadlineRead.Close()
	defer deadlineWrite.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, executable, "-test.run=^TestNativePackageRepositoryDeadlineFixture$", "-test.timeout=30s")
	command.Env = append(os.Environ(), "WK_NATIVE_PACKAGE_DEADLINE_FIXTURE=1", "WK_NATIVE_PACKAGE_REPOSITORY_INTEGRATION=1", "WK_NATIVE_PACKAGE_DIST_DIR="+packages, "TMPDIR="+root, "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	command.ExtraFiles = []*os.File{deadlineRead}
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.WaitDelay = time.Second
	output := &nativeRepositoryOutput{observed: make(chan struct{})}
	command.Stdout = output
	command.Stderr = output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	deadlineRead.Close()
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	select {
	case <-output.observed:
		if _, err := deadlineWrite.Write([]byte{1}); err != nil {
			t.Fatal(err)
		}
	case err := <-done:
		t.Fatalf("fixture exited before streaming diagnostics: %v\n%s", err, output.String())
	case <-ctx.Done():
		<-done
		t.Fatalf("fixture did not stream diagnostics before termination: %v\n%s", ctx.Err(), output.String())
	}
	err = <-done
	if ctx.Err() != nil || err == nil || !bytes.Contains([]byte(output.String()), []byte("panic: "+nativeRepositoryDeadline)) {
		t.Fatalf("fixture did not exercise the armed deadline: %v, context=%v\n%s", err, ctx.Err(), output.String())
	}
}

// This child-process test uses the production test entry and a controlled fatal
// deadline. The package's own timer remains a watchdog, not the test stimulus.
func TestNativePackageRepositoryDeadlineFixture(t *testing.T) {
	if os.Getenv("WK_NATIVE_PACKAGE_DEADLINE_FIXTURE") != "1" {
		t.Skip("subprocess fixture only")
	}
	deadline := os.NewFile(3, "fixture-deadline")
	go func() {
		var trigger [1]byte
		if _, err := io.ReadFull(deadline, trigger[:]); err != nil {
			panic("fixture deadline control closed before trigger")
		}
		panic(nativeRepositoryDeadline)
	}()
	TestNativePackageSignedRepository(t)
}

// nativeRepositoryOutput observes complete diagnostic markers across pipe
// reads without racing the waiting test or buffering the child until exit.
type nativeRepositoryOutput struct {
	mu       sync.Mutex
	buffer   bytes.Buffer
	observed chan struct{}
	once     sync.Once
}

func (o *nativeRepositoryOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	n, err := o.buffer.Write(p)
	if bytes.Contains(o.buffer.Bytes(), []byte(nativeRepositoryDiagnostic)) {
		o.once.Do(func() { close(o.observed) })
	}
	return n, err
}

func (o *nativeRepositoryOutput) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.buffer.String()
}
