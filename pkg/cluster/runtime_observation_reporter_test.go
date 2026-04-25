package cluster

import (
	"context"
	"errors"
	"testing"
	"time"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
)

func TestRuntimeObservationReporterFlushesDirtyViewsOnly(t *testing.T) {
	now := time.Unix(1710010000, 0)
	current := []controllermeta.SlotRuntimeView{
		{
			SlotID:              1,
			CurrentPeers:        []uint64{1, 2, 3},
			LeaderID:            1,
			HealthyVoters:       3,
			HasQuorum:           true,
			ObservedConfigEpoch: 1,
			LastReportAt:        now,
		},
		{
			SlotID:              2,
			CurrentPeers:        []uint64{1, 2, 3},
			LeaderID:            2,
			HealthyVoters:       3,
			HasQuorum:           true,
			ObservedConfigEpoch: 1,
			LastReportAt:        now,
		},
	}
	var sent []runtimeObservationReport
	reporter := newRuntimeObservationReporter(runtimeObservationReporterConfig{
		nodeID:   9,
		now:      func() time.Time { return now },
		snapshot: func() ([]controllermeta.SlotRuntimeView, error) { return cloneRuntimeViewsForTest(current), nil },
		send: func(_ context.Context, report runtimeObservationReport) error {
			sent = append(sent, report)
			return nil
		},
		fullSyncInterval: time.Minute,
		flushDebounce:    0,
	})

	reporter.requestFullSync()
	if err := reporter.tick(context.Background()); err != nil {
		t.Fatalf("tick() full sync error = %v", err)
	}

	now = now.Add(time.Second)
	current[1].LeaderID = 3
	current[1].LastReportAt = now
	if err := reporter.tick(context.Background()); err != nil {
		t.Fatalf("tick() dirty flush error = %v", err)
	}

	if len(sent) != 2 {
		t.Fatalf("send calls = %d, want 2", len(sent))
	}
	if sent[1].FullSync {
		t.Fatal("second report FullSync = true, want false")
	}
	if len(sent[1].Views) != 1 || sent[1].Views[0].SlotID != 2 {
		t.Fatalf("second report views = %#v, want only slot 2", sent[1].Views)
	}
	if len(sent[1].ClosedSlots) != 0 {
		t.Fatalf("second report ClosedSlots = %v, want none", sent[1].ClosedSlots)
	}
}

func TestRuntimeObservationReporterQueuesClosedSlots(t *testing.T) {
	now := time.Unix(1710011000, 0)
	current := []controllermeta.SlotRuntimeView{
		{
			SlotID:       1,
			CurrentPeers: []uint64{1, 2, 3},
			LeaderID:     1,
			HasQuorum:    true,
			LastReportAt: now,
		},
		{
			SlotID:       2,
			CurrentPeers: []uint64{1, 2, 3},
			LeaderID:     2,
			HasQuorum:    true,
			LastReportAt: now,
		},
	}
	var sent []runtimeObservationReport
	reporter := newRuntimeObservationReporter(runtimeObservationReporterConfig{
		nodeID:   9,
		now:      func() time.Time { return now },
		snapshot: func() ([]controllermeta.SlotRuntimeView, error) { return cloneRuntimeViewsForTest(current), nil },
		send: func(_ context.Context, report runtimeObservationReport) error {
			sent = append(sent, report)
			return nil
		},
		fullSyncInterval: time.Minute,
	})

	reporter.requestFullSync()
	if err := reporter.tick(context.Background()); err != nil {
		t.Fatalf("tick() full sync error = %v", err)
	}

	current = current[:1]
	reporter.markClosed(2)
	now = now.Add(time.Second)
	current[0].LastReportAt = now
	if err := reporter.tick(context.Background()); err != nil {
		t.Fatalf("tick() close flush error = %v", err)
	}

	if len(sent) != 2 {
		t.Fatalf("send calls = %d, want 2", len(sent))
	}
	if got, want := sent[1].ClosedSlots, []uint32{2}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("second report ClosedSlots = %v, want %v", got, want)
	}
}

func TestRuntimeObservationReporterRetainsDirtyAfterSendFailure(t *testing.T) {
	now := time.Unix(1710012000, 0)
	current := []controllermeta.SlotRuntimeView{{
		SlotID:       1,
		CurrentPeers: []uint64{1, 2, 3},
		LeaderID:     1,
		HasQuorum:    true,
		LastReportAt: now,
	}}
	var sent []runtimeObservationReport
	failOnce := true
	reporter := newRuntimeObservationReporter(runtimeObservationReporterConfig{
		nodeID: 9,
		now:    func() time.Time { return now },
		snapshot: func() ([]controllermeta.SlotRuntimeView, error) {
			return cloneRuntimeViewsForTest(current), nil
		},
		send: func(_ context.Context, report runtimeObservationReport) error {
			sent = append(sent, report)
			if !report.FullSync && failOnce {
				failOnce = false
				return errors.New("injected send failure")
			}
			return nil
		},
		fullSyncInterval: time.Minute,
	})

	reporter.requestFullSync()
	if err := reporter.tick(context.Background()); err != nil {
		t.Fatalf("tick() full sync error = %v", err)
	}

	now = now.Add(time.Second)
	current[0].LeaderID = 3
	current[0].LastReportAt = now
	if err := reporter.tick(context.Background()); err == nil {
		t.Fatal("tick() error = nil, want injected send failure")
	}
	if err := reporter.tick(context.Background()); err != nil {
		t.Fatalf("tick() retry error = %v", err)
	}

	if len(sent) != 3 {
		t.Fatalf("send calls = %d, want 3", len(sent))
	}
	if sent[1].Views[0].LeaderID != 3 || sent[2].Views[0].LeaderID != 3 {
		t.Fatalf("retry did not retain dirty payload: second=%#v third=%#v", sent[1].Views, sent[2].Views)
	}
}

func TestRuntimeObservationReporterRequestsFullSyncAfterLeaderChange(t *testing.T) {
	now := time.Unix(1710013000, 0)
	current := []controllermeta.SlotRuntimeView{{
		SlotID:       1,
		CurrentPeers: []uint64{1, 2, 3},
		LeaderID:     1,
		HasQuorum:    true,
		LastReportAt: now,
	}}
	var sent []runtimeObservationReport
	reporter := newRuntimeObservationReporter(runtimeObservationReporterConfig{
		nodeID:   9,
		now:      func() time.Time { return now },
		snapshot: func() ([]controllermeta.SlotRuntimeView, error) { return cloneRuntimeViewsForTest(current), nil },
		send: func(_ context.Context, report runtimeObservationReport) error {
			sent = append(sent, report)
			return nil
		},
		fullSyncInterval: time.Minute,
	})

	reporter.requestFullSync()
	if err := reporter.tick(context.Background()); err != nil {
		t.Fatalf("tick() initial full sync error = %v", err)
	}

	reporter.requestFullSync()
	now = now.Add(2 * time.Second)
	current[0].LastReportAt = now
	if err := reporter.tick(context.Background()); err != nil {
		t.Fatalf("tick() leader-change full sync error = %v", err)
	}

	if len(sent) != 2 {
		t.Fatalf("send calls = %d, want 2", len(sent))
	}
	if !sent[1].FullSync {
		t.Fatal("second report FullSync = false, want true")
	}
}

func TestRuntimeObservationReporterIdleTickSkipsRuntimeRPC(t *testing.T) {
	now := time.Unix(1710014000, 0)
	current := []controllermeta.SlotRuntimeView{{
		SlotID:       1,
		CurrentPeers: []uint64{1, 2, 3},
		LeaderID:     1,
		HasQuorum:    true,
		LastReportAt: now,
	}}
	sendCalls := 0
	reporter := newRuntimeObservationReporter(runtimeObservationReporterConfig{
		nodeID:           9,
		now:              func() time.Time { return now },
		snapshot:         func() ([]controllermeta.SlotRuntimeView, error) { return cloneRuntimeViewsForTest(current), nil },
		send:             func(_ context.Context, report runtimeObservationReport) error { sendCalls++; _ = report; return nil },
		fullSyncInterval: time.Minute,
	})

	reporter.requestFullSync()
	if err := reporter.tick(context.Background()); err != nil {
		t.Fatalf("tick() initial full sync error = %v", err)
	}

	now = now.Add(5 * time.Second)
	current[0].LastReportAt = now
	if err := reporter.tick(context.Background()); err != nil {
		t.Fatalf("tick() idle error = %v", err)
	}

	if sendCalls != 1 {
		t.Fatalf("send calls = %d, want 1", sendCalls)
	}
}

func cloneRuntimeViewsForTest(src []controllermeta.SlotRuntimeView) []controllermeta.SlotRuntimeView {
	if len(src) == 0 {
		return nil
	}
	dst := make([]controllermeta.SlotRuntimeView, len(src))
	for i := range src {
		dst[i] = src[i]
		dst[i].CurrentPeers = append([]uint64(nil), src[i].CurrentPeers...)
	}
	return dst
}
