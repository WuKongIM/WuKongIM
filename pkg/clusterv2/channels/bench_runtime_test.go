package channels

import (
	"context"
	"errors"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
)

func TestServiceRuntimeSnapshotDelegatesToRuntimeBench(t *testing.T) {
	runtime := &benchRuntimeFake{snapshot: ch.RuntimeSnapshot{NodeID: 2, ActiveTotal: 3}}
	svc, err := NewService(Config{Runtime: runtime})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	got, err := svc.RuntimeSnapshot(context.Background())
	if err != nil {
		t.Fatalf("RuntimeSnapshot() error = %v", err)
	}
	if got.NodeID != 2 || got.ActiveTotal != 3 {
		t.Fatalf("RuntimeSnapshot() = %#v, want delegated snapshot", got)
	}
	if runtime.snapshotCalls != 1 {
		t.Fatalf("snapshot calls = %d, want 1", runtime.snapshotCalls)
	}
}

func TestServiceRuntimeProbeDelegatesToRuntimeBench(t *testing.T) {
	id := ch.ChannelID{ID: "probe", Type: 1}
	runtime := &benchRuntimeFake{probe: ch.RuntimeProbeResult{Checked: 1, Missing: []ch.ChannelID{id}}}
	svc, err := NewService(Config{Runtime: runtime})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	got, err := svc.RuntimeProbe(context.Background(), ch.RuntimeSelector{ChannelIDs: []ch.ChannelID{id}})
	if err != nil {
		t.Fatalf("RuntimeProbe() error = %v", err)
	}
	if got.Checked != 1 || len(got.Missing) != 1 || got.Missing[0] != id {
		t.Fatalf("RuntimeProbe() = %#v, want delegated probe result", got)
	}
	if runtime.probeCalls != 1 || len(runtime.lastProbe.ChannelIDs) != 1 || runtime.lastProbe.ChannelIDs[0] != id {
		t.Fatalf("probe calls=%d selector=%#v, want selector forwarded", runtime.probeCalls, runtime.lastProbe)
	}
}

func TestServiceRuntimeEvictDelegatesToRuntimeBench(t *testing.T) {
	id := ch.ChannelID{ID: "evict", Type: 1}
	runtime := &benchRuntimeFake{evict: ch.RuntimeEvictResult{Requested: 1, Evicted: 1}}
	svc, err := NewService(Config{Runtime: runtime})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	got, err := svc.RuntimeEvict(context.Background(), ch.RuntimeSelector{ChannelIDs: []ch.ChannelID{id}})
	if err != nil {
		t.Fatalf("RuntimeEvict() error = %v", err)
	}
	if got.Requested != 1 || got.Evicted != 1 {
		t.Fatalf("RuntimeEvict() = %#v, want delegated evict result", got)
	}
	if runtime.evictCalls != 1 || len(runtime.lastEvict.ChannelIDs) != 1 || runtime.lastEvict.ChannelIDs[0] != id {
		t.Fatalf("evict calls=%d selector=%#v, want selector forwarded", runtime.evictCalls, runtime.lastEvict)
	}
}

func TestServiceRuntimeBenchUnsupported(t *testing.T) {
	svc, err := NewService(Config{Runtime: &fakeRuntime{}})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	snapshot, err := svc.RuntimeSnapshot(context.Background())
	if !errors.Is(err, ch.ErrInvalidConfig) {
		t.Fatalf("RuntimeSnapshot() error = %v, want ErrInvalidConfig", err)
	}
	if snapshot.NodeID != 0 || snapshot.ActiveTotal != 0 || len(snapshot.Reactors) != 0 || len(snapshot.WorkerQueues) != 0 {
		t.Fatalf("RuntimeSnapshot() = %#v, want zero value", snapshot)
	}

	probe, err := svc.RuntimeProbe(context.Background(), ch.RuntimeSelector{})
	if !errors.Is(err, ch.ErrInvalidConfig) {
		t.Fatalf("RuntimeProbe() error = %v, want ErrInvalidConfig", err)
	}
	if probe.Checked != 0 || probe.LoadedLeader != 0 || probe.LoadedFollower != 0 || len(probe.Missing) != 0 {
		t.Fatalf("RuntimeProbe() = %#v, want zero value", probe)
	}

	evict, err := svc.RuntimeEvict(context.Background(), ch.RuntimeSelector{})
	if !errors.Is(err, ch.ErrInvalidConfig) {
		t.Fatalf("RuntimeEvict() error = %v, want ErrInvalidConfig", err)
	}
	if evict != (ch.RuntimeEvictResult{}) {
		t.Fatalf("RuntimeEvict() = %#v, want zero value", evict)
	}
}

type benchRuntimeFake struct {
	fakeRuntime

	snapshot ch.RuntimeSnapshot
	probe    ch.RuntimeProbeResult
	evict    ch.RuntimeEvictResult

	lastProbe ch.RuntimeSelector
	lastEvict ch.RuntimeSelector

	snapshotCalls int
	probeCalls    int
	evictCalls    int
}

func (f *benchRuntimeFake) RuntimeSnapshot(context.Context) (ch.RuntimeSnapshot, error) {
	f.snapshotCalls++
	return f.snapshot, nil
}

func (f *benchRuntimeFake) RuntimeProbe(_ context.Context, selector ch.RuntimeSelector) (ch.RuntimeProbeResult, error) {
	f.probeCalls++
	f.lastProbe = selector
	return f.probe, nil
}

func (f *benchRuntimeFake) RuntimeEvict(_ context.Context, selector ch.RuntimeSelector) (ch.RuntimeEvictResult, error) {
	f.evictCalls++
	f.lastEvict = selector
	return f.evict, nil
}
