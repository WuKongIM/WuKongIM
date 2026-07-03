package main

import (
	"context"
	"errors"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/app"
)

func TestRunStartsAndStopsAfterContextCancel(t *testing.T) {
	unsetLoadConfigEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "wukongim.conf")
	writeConf(t, path, requiredConfigLines(dir)...)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fake := &fakeRuntimeApp{
		started: make(chan struct{}),
		stopped: make(chan context.Context, 1),
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- run(ctx, []string{"-config", path}, func(app.Config) (runtimeApp, error) {
			return fake, nil
		})
	}()

	select {
	case <-fake.started:
	case <-time.After(time.Second):
		t.Fatal("run() did not start app")
	}
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("run() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("run() did not return after context cancel")
	}

	select {
	case stopCtx := <-fake.stopped:
		if _, ok := stopCtx.Deadline(); !ok {
			t.Fatal("Stop context has no deadline")
		}
	default:
		t.Fatal("Stop was not called")
	}
}

func TestRunReturnsStartErrorWithoutStopping(t *testing.T) {
	unsetLoadConfigEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "wukongim.conf")
	writeConf(t, path, requiredConfigLines(dir)...)

	startErr := errors.New("start failed")
	fake := &fakeRuntimeApp{startErr: startErr}

	err := run(context.Background(), []string{"-config", path}, func(app.Config) (runtimeApp, error) {
		return fake, nil
	})
	if !errors.Is(err, startErr) {
		t.Fatalf("run() error = %v, want %v", err, startErr)
	}
	if got := fake.startCalls.Load(); got != 1 {
		t.Fatalf("Start calls = %d, want 1", got)
	}
	if got := fake.stopCalls.Load(); got != 0 {
		t.Fatalf("Stop calls = %d, want 0", got)
	}
}

func TestRunReturnsStopErrorAfterContextCancel(t *testing.T) {
	unsetLoadConfigEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "wukongim.conf")
	writeConf(t, path, requiredConfigLines(dir)...)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stopErr := errors.New("stop failed")
	fake := &fakeRuntimeApp{
		onStart: func() { cancel() },
		stopErr: stopErr,
	}

	err := run(ctx, []string{"-config", path}, func(app.Config) (runtimeApp, error) {
		return fake, nil
	})
	if !errors.Is(err, stopErr) {
		t.Fatalf("run() error = %v, want %v", err, stopErr)
	}
	if got := fake.startCalls.Load(); got != 1 {
		t.Fatalf("Start calls = %d, want 1", got)
	}
	if got := fake.stopCalls.Load(); got != 1 {
		t.Fatalf("Stop calls = %d, want 1", got)
	}
}

func TestRunPassesConfigFlagFormsToLoadConfig(t *testing.T) {
	cases := []struct {
		name string
		args func(string) []string
		id   uint64
	}{
		{name: "space separated", args: func(path string) []string { return []string{"-config", path} }, id: 11},
		{name: "equals", args: func(path string) []string { return []string{"-config=" + path} }, id: 12},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			unsetLoadConfigEnv(t)
			dir := t.TempDir()
			path := filepath.Join(dir, "wukongim.conf")
			writeConf(t, path,
				"WK_NODE_ID="+strconv.FormatUint(tc.id, 10),
				"WK_NODE_DATA_DIR="+filepath.Join(dir, "node"),
				"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7001",
			)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			fake := &fakeRuntimeApp{
				onStart: func() { cancel() },
				stopped: make(chan context.Context, 1),
			}
			var got app.Config

			err := run(ctx, tc.args(path), func(cfg app.Config) (runtimeApp, error) {
				got = cfg
				return fake, nil
			})
			if err != nil {
				t.Fatalf("run() error = %v", err)
			}
			if got.NodeID != tc.id || got.Cluster.NodeID != tc.id {
				t.Fatalf("NodeID = %d/%d, want %d", got.NodeID, got.Cluster.NodeID, tc.id)
			}
		})
	}
}

func TestRunReturnsConfigErrorWithoutCreatingApp(t *testing.T) {
	unsetLoadConfigEnv(t)
	chdir(t, t.TempDir())
	created := false

	err := run(context.Background(), nil, func(app.Config) (runtimeApp, error) {
		created = true
		return &fakeRuntimeApp{}, nil
	})
	if err == nil {
		t.Fatal("run() error = nil, want config error")
	}
	if created {
		t.Fatal("new app factory was called after config error")
	}
	if !strings.Contains(err.Error(), "WK_NODE_ID") {
		t.Fatalf("run() error = %v, want missing WK_NODE_ID", err)
	}
}

func TestRunStopTimeoutUsesConfigThenDefault(t *testing.T) {
	cfg := app.Config{}
	if got := stopTimeout(cfg); got != 5*time.Second {
		t.Fatalf("default stopTimeout() = %s, want 5s", got)
	}

	cfg.Cluster.Timeouts.Stop = 25 * time.Millisecond
	if got := stopTimeout(cfg); got != 25*time.Millisecond {
		t.Fatalf("configured stopTimeout() = %s, want 25ms", got)
	}
}

func TestImportBoundaryUsesCanonicalInternalApp(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("Glob(): %v", err)
	}
	fset := token.NewFileSet()
	canonicalImport := strings.Join([]string{"github.com/WuKongIM/WuKongIM/internal", "app"}, "/")
	internalV2Prefix := strings.Join([]string{"github.com/WuKongIM/WuKongIM", "internalv2"}, "/") + "/"
	internalLegacyPrefix := "github.com/WuKongIM/WuKongIM/internal/legacy/"
	foundCanonical := false
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("ParseFile(%s): %v", path, err)
		}
		for _, spec := range file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatalf("Unquote(%s): %v", spec.Path.Value, err)
			}
			if strings.HasPrefix(importPath, internalV2Prefix) {
				t.Fatalf("%s imports v2-suffixed internal package %q", path, importPath)
			}
			if strings.HasPrefix(importPath, internalLegacyPrefix) {
				t.Fatalf("%s imports legacy internal package %q", path, importPath)
			}
			if importPath == canonicalImport {
				foundCanonical = true
			}
		}
	}
	if !foundCanonical {
		t.Fatal("cmd/wukongim production files do not import canonical internal/app")
	}
}

func TestDependencyBoundaryDoesNotReachLegacyBenchInternal(t *testing.T) {
	cmd := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", ".")
	cmd.Env = append(os.Environ(), "GOWORK=off")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list deps failed: %v\n%s", err, out)
	}

	for _, importPath := range strings.Fields(string(out)) {
		if importPath == "github.com/WuKongIM/WuKongIM/internal/bench/model" {
			t.Fatalf("cmd/wukongim dependency closure still imports legacy bench model package %q", importPath)
		}
	}
}

func TestDependencyBoundaryUsesCanonicalController(t *testing.T) {
	cmd := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", ".")
	cmd.Env = append(os.Environ(), "GOWORK=off")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list deps failed: %v\n%s", err, out)
	}

	foundCanonicalController := false
	for _, importPath := range strings.Fields(string(out)) {
		switch {
		case importPath == "github.com/WuKongIM/WuKongIM/pkg/controller":
			foundCanonicalController = true
		case strings.HasPrefix(importPath, "github.com/WuKongIM/WuKongIM/pkg/controller/"):
			foundCanonicalController = true
		case importPath == "github.com/WuKongIM/WuKongIM/pkg/legacy/cluster" ||
			strings.HasPrefix(importPath, "github.com/WuKongIM/WuKongIM/pkg/legacy/cluster/"):
			t.Fatalf("cmd/wukongim dependency closure still imports legacy cluster package %q", importPath)
		case importPath == "github.com/WuKongIM/WuKongIM/pkg/controllerv2" ||
			strings.HasPrefix(importPath, "github.com/WuKongIM/WuKongIM/pkg/controllerv2/"):
			t.Fatalf("cmd/wukongim dependency closure still imports v2-suffixed controller package %q", importPath)
		case importPath == "github.com/WuKongIM/WuKongIM/pkg/legacy/controller" ||
			strings.HasPrefix(importPath, "github.com/WuKongIM/WuKongIM/pkg/legacy/controller/"):
			t.Fatalf("cmd/wukongim dependency closure still imports legacy controller package %q", importPath)
		}
	}
	if !foundCanonicalController {
		t.Fatal("cmd/wukongim dependency closure does not import canonical pkg/controller")
	}
}

func TestDependencyBoundaryUsesCanonicalCluster(t *testing.T) {
	cmd := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", ".")
	cmd.Env = append(os.Environ(), "GOWORK=off")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list deps failed: %v\n%s", err, out)
	}

	foundCanonicalCluster := false
	for _, importPath := range strings.Fields(string(out)) {
		switch {
		case importPath == "github.com/WuKongIM/WuKongIM/pkg/cluster":
			foundCanonicalCluster = true
		case strings.HasPrefix(importPath, "github.com/WuKongIM/WuKongIM/pkg/cluster/"):
			foundCanonicalCluster = true
		case importPath == "github.com/WuKongIM/WuKongIM/pkg/clusterv2" ||
			strings.HasPrefix(importPath, "github.com/WuKongIM/WuKongIM/pkg/clusterv2/"):
			t.Fatalf("cmd/wukongim dependency closure still imports v2-suffixed cluster package %q", importPath)
		case importPath == "github.com/WuKongIM/WuKongIM/pkg/legacy/cluster" ||
			strings.HasPrefix(importPath, "github.com/WuKongIM/WuKongIM/pkg/legacy/cluster/"):
			t.Fatalf("cmd/wukongim dependency closure still imports legacy cluster package %q", importPath)
		}
	}
	if !foundCanonicalCluster {
		t.Fatal("cmd/wukongim dependency closure does not import canonical pkg/cluster")
	}
}

func TestDependencyBoundaryUsesCanonicalChannel(t *testing.T) {
	cmd := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", ".")
	cmd.Env = append(os.Environ(), "GOWORK=off")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list deps failed: %v\n%s", err, out)
	}

	foundCanonicalChannel := false
	for _, importPath := range strings.Fields(string(out)) {
		switch {
		case importPath == "github.com/WuKongIM/WuKongIM/pkg/channel":
			foundCanonicalChannel = true
		case strings.HasPrefix(importPath, "github.com/WuKongIM/WuKongIM/pkg/channel/"):
			foundCanonicalChannel = true
		case importPath == "github.com/WuKongIM/WuKongIM/pkg/channel" ||
			strings.HasPrefix(importPath, "github.com/WuKongIM/WuKongIM/pkg/channel/"):
			t.Fatalf("cmd/wukongim dependency closure still imports v2-suffixed channel package %q", importPath)
		case importPath == "github.com/WuKongIM/WuKongIM/pkg/legacy/channel" ||
			strings.HasPrefix(importPath, "github.com/WuKongIM/WuKongIM/pkg/legacy/channel/"):
			t.Fatalf("cmd/wukongim dependency closure still imports legacy channel package %q", importPath)
		}
	}
	if !foundCanonicalChannel {
		t.Fatal("cmd/wukongim dependency closure does not import canonical pkg/channel")
	}
}

type fakeRuntimeApp struct {
	startErr   error
	stopErr    error
	startCalls atomic.Int32
	stopCalls  atomic.Int32
	started    chan struct{}
	stopped    chan context.Context
	onStart    func()
}

func (f *fakeRuntimeApp) Start(context.Context) error {
	f.startCalls.Add(1)
	if f.started != nil {
		close(f.started)
	}
	if f.onStart != nil {
		f.onStart()
	}
	return f.startErr
}

func (f *fakeRuntimeApp) Stop(ctx context.Context) error {
	f.stopCalls.Add(1)
	if f.stopped != nil {
		f.stopped <- ctx
	}
	return f.stopErr
}
