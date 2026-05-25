package clusterv2

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/internal/lifecycle"
)

func TestNodeStartStartsResourcesInOrder(t *testing.T) {
	var calls []string
	node, err := New(validNodeConfig(t), withResources(
		namedTestResource("net", &recordingResource{name: "net", calls: &calls}),
		namedTestResource("control", &recordingResource{name: "control", calls: &calls}),
	))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	want := []string{"start:net", "start:control"}
	if !equalStrings(calls, want) {
		t.Fatalf("calls = %#v, want %#v", calls, want)
	}
}

func TestNodeStopStopsResourcesInReverseOrder(t *testing.T) {
	var calls []string
	node, err := New(validNodeConfig(t), withResources(
		namedTestResource("net", &recordingResource{name: "net", calls: &calls}),
		namedTestResource("control", &recordingResource{name: "control", calls: &calls}),
	))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	calls = nil
	if err := node.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	want := []string{"stop:control", "stop:net"}
	if !equalStrings(calls, want) {
		t.Fatalf("calls = %#v, want %#v", calls, want)
	}
}

func TestNodeStartStopsStartedResourcesOnFailure(t *testing.T) {
	var calls []string
	boom := errors.New("boom")
	node, err := New(validNodeConfig(t), withResources(
		namedTestResource("net", &recordingResource{name: "net", calls: &calls}),
		namedTestResource("control", &recordingResource{name: "control", calls: &calls, startErr: boom}),
	))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := node.Start(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("Start() error = %v, want boom", err)
	}
	want := []string{"start:net", "start:control", "stop:net"}
	if !equalStrings(calls, want) {
		t.Fatalf("calls = %#v, want %#v", calls, want)
	}
}

func TestNodeRejectsInvalidConfig(t *testing.T) {
	if _, err := New(Config{}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("New() error = %v, want ErrInvalidConfig", err)
	}
}

func TestStoppedNodeRejectsForegroundWithErrStopping(t *testing.T) {
	node, err := New(validNodeConfig(t))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := node.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if _, err := node.RouteKey("u1"); !errors.Is(err, ErrStopping) {
		t.Fatalf("RouteKey() error = %v, want ErrStopping", err)
	}
}

func namedTestResource(name string, resource lifecycle.Resource) lifecycle.NamedResource {
	return lifecycle.NamedResource{Name: name, Resource: resource}
}

func validNodeConfig(t *testing.T) Config {
	t.Helper()
	return Config{NodeID: 1, ListenAddr: "127.0.0.1:0", DataDir: t.TempDir()}
}

type recordingResource struct {
	name     string
	calls    *[]string
	startErr error
	stopErr  error
}

func (r *recordingResource) Start(context.Context) error {
	*r.calls = append(*r.calls, "start:"+r.name)
	return r.startErr
}

func (r *recordingResource) Stop(context.Context) error {
	*r.calls = append(*r.calls, "stop:"+r.name)
	return r.stopErr
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
