package channelretention

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
)

func TestSafeBoundaryCapsExpiredScanByConfirmedReplayCursor(t *testing.T) {
	view := channel.RetentionView{
		RetentionThroughSeq: 10,
		HW:                  100,
		CheckpointHW:        100,
		MinISRMatchOffset:   100,
	}

	got := safeBoundary(80, 50, view)

	if got.AdvanceThroughSeq != 50 || !got.ShouldAdvance {
		t.Fatalf("safeBoundary() = %+v, want advance through 50", got)
	}
}

func TestSafeBoundaryMissingFollowerRetentionProgressBlocksAdvancement(t *testing.T) {
	view := channel.RetentionView{
		RetentionThroughSeq: 40,
		HW:                  90,
		CheckpointHW:        90,
		MinISRMatchOffset:   40,
	}

	got := safeBoundary(80, 80, view)

	if got.AdvanceThroughSeq != 40 || got.ShouldAdvance {
		t.Fatalf("safeBoundary() = %+v, want no advance beyond current retention", got)
	}
}

func TestSafeBoundaryNeverDecreasesCurrentRetentionThroughSeq(t *testing.T) {
	view := channel.RetentionView{
		RetentionThroughSeq: 60,
		HW:                  100,
		CheckpointHW:        100,
		MinISRMatchOffset:   100,
	}

	got := safeBoundary(50, 50, view)

	if got.AdvanceThroughSeq != 60 || got.ShouldAdvance {
		t.Fatalf("safeBoundary() = %+v, want no regression below current retention", got)
	}
}

func TestSafeBoundaryZeroExpiredScanReturnsNoAdvancement(t *testing.T) {
	view := channel.RetentionView{
		RetentionThroughSeq: 20,
		HW:                  100,
		CheckpointHW:        100,
		MinISRMatchOffset:   100,
	}

	got := safeBoundary(0, 100, view)

	if got.AdvanceThroughSeq != 20 || got.ShouldAdvance {
		t.Fatalf("safeBoundary() = %+v, want no advancement", got)
	}
}

func TestSafeBoundaryCapsByRuntimeWatermarks(t *testing.T) {
	view := channel.RetentionView{
		RetentionThroughSeq: 10,
		HW:                  70,
		CheckpointHW:        55,
		MinISRMatchOffset:   80,
	}

	got := safeBoundary(80, 80, view)

	if got.AdvanceThroughSeq != 55 || !got.ShouldAdvance {
		t.Fatalf("safeBoundary() = %+v, want advance through checkpoint capped boundary 55", got)
	}
}

func TestRunOnceConfirmsCursorBeforeMetadataAdvanceAndUsesFinalBoundary(t *testing.T) {
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	ch := testChannel("alpha")
	order := make([]string, 0, 4)
	metadata := &fakeMetadataStore{order: &order}
	replay := &fakeReplayCursor{order: &order, cursors: map[channel.ChannelKey]uint64{ch.Key: 45}}
	scanner := &fakeExpiredScanner{order: &order, prefixes: map[channel.ChannelKey]ExpiredPrefix{ch.Key: {ThroughSeq: 70}}}
	worker := NewWorker(Config{
		Channels:     fakeChannelLister{channels: []Channel{ch}},
		Scanner:      scanner,
		Runtime:      &fakeRuntimeView{views: map[channel.ChannelKey]channel.RetentionView{ch.Key: retentionView(10, 100, 90, 80)}},
		Replay:       replay,
		Metadata:     metadata,
		TTL:          time.Hour,
		ScanLimit:    7,
		Now:          func() time.Time { return now },
		ScanInterval: time.Minute,
	})

	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}

	if len(metadata.advances) != 1 {
		t.Fatalf("metadata advances = %+v, want one advance", metadata.advances)
	}
	if metadata.advances[0].boundary != 45 {
		t.Fatalf("advance boundary = %d, want cursor-capped boundary 45", metadata.advances[0].boundary)
	}
	if replay.confirmed[0].minSeq != 11 {
		t.Fatalf("confirm minSeq = %d, want current retention + 1", replay.confirmed[0].minSeq)
	}
	if scanner.calls[0].limit != 7 {
		t.Fatalf("scan limit = %d, want configured limit 7", scanner.calls[0].limit)
	}
	if wantCutoff := now.Add(-time.Hour); !scanner.calls[0].cutoff.Equal(wantCutoff) {
		t.Fatalf("scan cutoff = %s, want %s", scanner.calls[0].cutoff, wantCutoff)
	}
	assertOrder(t, order, []string{"scan", "confirm", "advance"})
}

func TestRunOnceReturnsFirstErrorWithoutUnsafeAdvance(t *testing.T) {
	boom := errors.New("boom")
	ch1 := testChannel("first")
	ch2 := testChannel("second")
	tests := []struct {
		name             string
		scanner          *fakeExpiredScanner
		replay           *fakeReplayCursor
		metadata         *fakeMetadataStore
		wantAdvanceCalls int
	}{
		{
			name:             "scan error stops before confirm",
			scanner:          &fakeExpiredScanner{errs: map[channel.ChannelKey]error{ch1.Key: boom}},
			replay:           &fakeReplayCursor{cursors: map[channel.ChannelKey]uint64{ch1.Key: 80}},
			metadata:         &fakeMetadataStore{},
			wantAdvanceCalls: 0,
		},
		{
			name:             "confirm error stops before metadata advance",
			scanner:          &fakeExpiredScanner{prefixes: map[channel.ChannelKey]ExpiredPrefix{ch1.Key: {ThroughSeq: 80}}},
			replay:           &fakeReplayCursor{errs: map[channel.ChannelKey]error{ch1.Key: boom}},
			metadata:         &fakeMetadataStore{},
			wantAdvanceCalls: 0,
		},
		{
			name:             "advance error is returned",
			scanner:          &fakeExpiredScanner{prefixes: map[channel.ChannelKey]ExpiredPrefix{ch1.Key: {ThroughSeq: 80}}},
			replay:           &fakeReplayCursor{cursors: map[channel.ChannelKey]uint64{ch1.Key: 80}},
			metadata:         &fakeMetadataStore{err: boom},
			wantAdvanceCalls: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runtimeView := &fakeRuntimeView{views: map[channel.ChannelKey]channel.RetentionView{
				ch1.Key: retentionView(10, 100, 100, 100),
				ch2.Key: retentionView(10, 100, 100, 100),
			}}
			worker := NewWorker(Config{
				Channels: fakeChannelLister{channels: []Channel{ch1, ch2}},
				Scanner:  tt.scanner,
				Runtime:  runtimeView,
				Replay:   tt.replay,
				Metadata: tt.metadata,
				TTL:      time.Hour,
				Now:      func() time.Time { return time.Unix(1000, 0) },
			})

			err := worker.RunOnce(context.Background())
			if !errors.Is(err, boom) {
				t.Fatalf("RunOnce() error = %v, want boom", err)
			}
			if len(tt.metadata.advances) != tt.wantAdvanceCalls {
				t.Fatalf("advance calls = %d, want %d", len(tt.metadata.advances), tt.wantAdvanceCalls)
			}
			if len(runtimeView.calls) != 1 || runtimeView.calls[0].Key != ch1.Key {
				t.Fatalf("runtime calls = %+v, want only first channel", runtimeView.calls)
			}
		})
	}
}

func TestRunOnceDisabledTTLSkipsPorts(t *testing.T) {
	worker := NewWorker(Config{
		Channels: fakeChannelLister{err: errors.New("must not list")},
		TTL:      0,
	})

	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() error = %v, want nil for disabled TTL", err)
	}
}

func TestWorkerStartRunsUntilStopAndIsIdempotent(t *testing.T) {
	called := make(chan struct{}, 1)
	ch := testChannel("background")
	worker := NewWorker(Config{
		Channels: &notifyingLister{channels: []Channel{ch}, called: called},
		Scanner:  &fakeExpiredScanner{prefixes: map[channel.ChannelKey]ExpiredPrefix{ch.Key: {}}},
		Runtime:  &fakeRuntimeView{views: map[channel.ChannelKey]channel.RetentionView{ch.Key: retentionView(0, 0, 0, 0)}},
		Replay:   &fakeReplayCursor{},
		Metadata: &fakeMetadataStore{},
		TTL:      time.Hour,
		Now:      func() time.Time { return time.Unix(1000, 0) },
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := worker.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := worker.Start(ctx); err != nil {
		t.Fatalf("second Start() error = %v", err)
	}

	select {
	case <-called:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("worker did not run before timeout")
	}

	stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Second)
	defer stopCancel()
	if err := worker.Stop(stopCtx); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if err := worker.Stop(stopCtx); err != nil {
		t.Fatalf("second Stop() error = %v", err)
	}
}

type fakeChannelLister struct {
	channels []Channel
	err      error
}

func (f fakeChannelLister) ListRetentionChannels(context.Context) ([]Channel, error) {
	if f.err != nil {
		return nil, f.err
	}
	return append([]Channel(nil), f.channels...), nil
}

type notifyingLister struct {
	channels []Channel
	called   chan<- struct{}
}

func (n *notifyingLister) ListRetentionChannels(ctx context.Context) ([]Channel, error) {
	select {
	case n.called <- struct{}{}:
	default:
	}
	return append([]Channel(nil), n.channels...), nil
}

type fakeRuntimeView struct {
	views map[channel.ChannelKey]channel.RetentionView
	errs  map[channel.ChannelKey]error
	calls []Channel
}

func (f *fakeRuntimeView) RetentionView(ctx context.Context, ch Channel) (channel.RetentionView, error) {
	f.calls = append(f.calls, ch)
	if err := f.errs[ch.Key]; err != nil {
		return channel.RetentionView{}, err
	}
	return f.views[ch.Key], nil
}

type scanCall struct {
	ch     Channel
	cutoff time.Time
	limit  int
}

type fakeExpiredScanner struct {
	prefixes map[channel.ChannelKey]ExpiredPrefix
	errs     map[channel.ChannelKey]error
	calls    []scanCall
	order    *[]string
}

func (f *fakeExpiredScanner) ScanExpiredPrefix(ctx context.Context, ch Channel, cutoff time.Time, limit int) (ExpiredPrefix, error) {
	f.calls = append(f.calls, scanCall{ch: ch, cutoff: cutoff, limit: limit})
	if f.order != nil {
		*f.order = append(*f.order, "scan")
	}
	if err := f.errs[ch.Key]; err != nil {
		return ExpiredPrefix{}, err
	}
	return f.prefixes[ch.Key], nil
}

type confirmCall struct {
	ch     Channel
	minSeq uint64
}

type fakeReplayCursor struct {
	cursors   map[channel.ChannelKey]uint64
	errs      map[channel.ChannelKey]error
	confirmed []confirmCall
	order     *[]string
}

func (f *fakeReplayCursor) ConfirmCommittedReplayCursor(ctx context.Context, ch Channel, minSeq uint64) (uint64, error) {
	f.confirmed = append(f.confirmed, confirmCall{ch: ch, minSeq: minSeq})
	if f.order != nil {
		*f.order = append(*f.order, "confirm")
	}
	if err := f.errs[ch.Key]; err != nil {
		return 0, err
	}
	return f.cursors[ch.Key], nil
}

type advanceCall struct {
	ch       Channel
	boundary uint64
	now      time.Time
}

type fakeMetadataStore struct {
	advances []advanceCall
	err      error
	order    *[]string
}

func (f *fakeMetadataStore) AdvanceRetention(ctx context.Context, ch Channel, boundary uint64, now time.Time) error {
	f.advances = append(f.advances, advanceCall{ch: ch, boundary: boundary, now: now})
	if f.order != nil {
		*f.order = append(*f.order, "advance")
	}
	return f.err
}

func testChannel(id string) Channel {
	return Channel{Key: channel.ChannelKey("channel/1/" + id), ID: channel.ChannelID{ID: id, Type: 1}}
}

func retentionView(current, hw, checkpointHW, minISR uint64) channel.RetentionView {
	return channel.RetentionView{
		RetentionThroughSeq: current,
		HW:                  hw,
		CheckpointHW:        checkpointHW,
		MinISRMatchOffset:   minISR,
	}
}

func assertOrder(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}
