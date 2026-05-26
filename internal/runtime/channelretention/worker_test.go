package channelretention

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

func TestSafeRetentionBoundaryCapsExpiredScanByConfirmedReplayCursor(t *testing.T) {
	view := retentionView(10, 100, 100, 100)

	got := safeRetentionBoundary(80, 50, view)

	if got.AdvanceThroughSeq != 50 || !got.ShouldAdvance || got.BlockedReason != BlockedNone {
		t.Fatalf("safeRetentionBoundary() = %+v, want advance through 50", got)
	}
}

func TestSafeRetentionBoundaryMissingFollowerRetentionProgressBlocksAdvancement(t *testing.T) {
	view := retentionView(40, 90, 90, 40)

	got := safeRetentionBoundary(80, 80, view)

	if got.AdvanceThroughSeq != 40 || got.ShouldAdvance || got.BlockedReason != BlockedMinISRMatchOffset {
		t.Fatalf("safeRetentionBoundary() = %+v, want no advance beyond current retention", got)
	}
}

func TestSafeRetentionBoundaryNeverDecreasesCurrentRetentionThroughSeq(t *testing.T) {
	view := retentionView(60, 100, 100, 100)

	got := safeRetentionBoundary(50, 50, view)

	if got.AdvanceThroughSeq != 60 || got.ShouldAdvance || got.BlockedReason != BlockedExpiredPrefix {
		t.Fatalf("safeRetentionBoundary() = %+v, want no regression below current retention", got)
	}
}

func TestSafeRetentionBoundaryZeroExpiredScanReturnsNoAdvancement(t *testing.T) {
	view := retentionView(20, 100, 100, 100)

	got := safeRetentionBoundary(0, 100, view)

	if got.AdvanceThroughSeq != 20 || got.ShouldAdvance || got.BlockedReason != BlockedNoExpiredPrefix {
		t.Fatalf("safeRetentionBoundary() = %+v, want no advancement", got)
	}
}

func TestSafeRetentionBoundaryCapsByRuntimeWatermarks(t *testing.T) {
	view := retentionView(10, 70, 55, 80)

	got := safeRetentionBoundary(80, 80, view)

	if got.AdvanceThroughSeq != 55 || !got.ShouldAdvance || got.BlockedReason != BlockedNone {
		t.Fatalf("safeRetentionBoundary() = %+v, want advance through checkpoint capped boundary 55", got)
	}
}

func TestRunOnceConfirmsCursorBeforeMetadataAdvanceAndAppliesLocalBoundary(t *testing.T) {
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	ch := testChannel("alpha")
	order := make([]string, 0, 5)
	metadata := &fakeMetadataStore{order: &order}
	store := &fakeStore{
		order:  &order,
		cursor: 45,
		scan:   ScanResult{ThroughSeq: 70},
	}
	runtime := &fakeRuntime{order: &order, views: map[channel.ChannelKey][]channel.RetentionView{
		ch.Key: {retentionViewWithMeta(10, 100, 90, 80, 7, 8, 1, now.Add(time.Minute))},
	}}
	worker := NewWorker(Config{
		Channels:        fakeChannelLister{channels: []Channel{ch}},
		Stores:          fakeStoreProvider{stores: map[channel.ChannelKey]Store{ch.Key: store}},
		Runtime:         runtime,
		Metadata:        metadata,
		LocalNodeID:     1,
		TTL:             time.Hour,
		MaxTrimMessages: 7,
		Now:             func() time.Time { return now },
		ScanInterval:    time.Minute,
	})

	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}

	if len(metadata.advances) != 1 {
		t.Fatalf("metadata advances = %+v, want one advance", metadata.advances)
	}
	wantReq := metadb.ChannelRetentionAdvance{
		ChannelID:            ch.ID.ID,
		ChannelType:          int64(ch.ID.Type),
		ExpectedChannelEpoch: 7,
		ExpectedLeaderEpoch:  8,
		ExpectedLeader:       1,
		ExpectedLeaseUntilMS: now.Add(time.Minute).UnixMilli(),
		RetentionThroughSeq:  45,
		RetentionUpdatedAtMS: now.UnixMilli(),
	}
	if metadata.advances[0] != wantReq {
		t.Fatalf("advance request = %+v, want %+v", metadata.advances[0], wantReq)
	}
	if len(store.confirms) != 1 || store.confirms[0].name != defaultCursorName || store.confirms[0].minSeq != 11 {
		t.Fatalf("confirm calls = %+v, want cursor %q minSeq 11", store.confirms, defaultCursorName)
	}
	if len(store.scans) != 1 || store.scans[0].fromSeq != 11 || store.scans[0].limit != 7 {
		t.Fatalf("scan calls = %+v, want fromSeq 11 limit 7", store.scans)
	}
	if wantCutoff := now.Add(-time.Hour); !store.scans[0].cutoff.Equal(wantCutoff) {
		t.Fatalf("scan cutoff = %s, want %s", store.scans[0].cutoff, wantCutoff)
	}
	if len(runtime.applied) != 1 || runtime.applied[0].boundary != 45 {
		t.Fatalf("local apply calls = %+v, want boundary 45", runtime.applied)
	}
	assertOrder(t, order, []string{"scan", "confirm", "advance", "apply"})
}

func TestRunOnceSkipsNonLocalLeaderAndUnreadyChannels(t *testing.T) {
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	nonLeader := testChannel("non-leader")
	unready := testChannel("unready")
	expiredLease := testChannel("expired-lease")
	storeProvider := &recordingStoreProvider{}
	worker := NewWorker(Config{
		Channels: fakeChannelLister{channels: []Channel{nonLeader, unready, expiredLease}},
		Stores:   storeProvider,
		Runtime: &fakeRuntime{views: map[channel.ChannelKey][]channel.RetentionView{
			nonLeader.Key:    {retentionViewWithMeta(10, 100, 100, 100, 7, 8, 2, now.Add(time.Minute))},
			unready.Key:      {retentionViewWithMeta(10, 100, 100, 100, 7, 8, 1, now.Add(time.Minute), func(v *channel.RetentionView) { v.CommitReady = false })},
			expiredLease.Key: {retentionViewWithMeta(10, 100, 100, 100, 7, 8, 1, now.Add(-time.Second))},
		}},
		Metadata:    &fakeMetadataStore{},
		LocalNodeID: 1,
		TTL:         time.Hour,
		Now:         func() time.Time { return now },
	})

	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() error = %v, want nil", err)
	}
	if len(storeProvider.calls) != 0 {
		t.Fatalf("StoreForChannel calls = %+v, want none", storeProvider.calls)
	}
}

func TestRunOnceSkipsUnavailableChannelAndContinuesBatch(t *testing.T) {
	now := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
	unavailable := testChannel("unavailable")
	eligible := testChannel("eligible")
	metadata := &fakeMetadataStore{}
	runtime := &fakeRuntime{
		errs: map[channel.ChannelKey]error{
			unavailable.Key: ErrChannelUnavailable,
		},
		views: map[channel.ChannelKey][]channel.RetentionView{
			eligible.Key: {
				retentionViewWithMeta(2, 10, 10, 10, 7, 8, 1, now.Add(time.Minute)),
				retentionViewWithMeta(2, 10, 10, 10, 7, 8, 1, now.Add(time.Minute)),
			},
		},
	}
	store := &fakeStore{scan: ScanResult{ThroughSeq: 8}, cursor: 8}
	worker := NewWorker(Config{
		Channels: fakeChannelLister{channels: []Channel{unavailable, eligible}},
		Stores: fakeStoreProvider{stores: map[channel.ChannelKey]Store{
			eligible.Key: store,
		}},
		Runtime:     runtime,
		Metadata:    metadata,
		LocalNodeID: 1,
		TTL:         time.Hour,
		Now:         func() time.Time { return now },
	})

	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() error = %v, want nil after skipping unavailable channel", err)
	}
	if len(metadata.advances) != 1 || metadata.advances[0].RetentionThroughSeq != 8 {
		t.Fatalf("metadata advances = %+v, want eligible channel through 8", metadata.advances)
	}
	if len(runtime.viewCalls) < 2 || runtime.viewCalls[0] != unavailable.Key || runtime.viewCalls[1] != eligible.Key {
		t.Fatalf("runtime view calls = %+v, want unavailable then eligible", runtime.viewCalls)
	}
}

func TestRunOnceRetriesExistingRetentionBoundaryBeforeNewExpiredPrefix(t *testing.T) {
	now := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
	ch := testChannel("retry-existing-boundary")
	runtime := &fakeRuntime{views: map[channel.ChannelKey][]channel.RetentionView{
		ch.Key: {retentionViewWithMeta(10, 10, 10, 10, 7, 8, 1, now.Add(time.Minute), func(v *channel.RetentionView) {
			v.LocalRetentionThroughSeq = 10
			v.PhysicalRetentionThroughSeq = 5
		})},
	}}
	store := &fakeStore{scan: ScanResult{}}
	worker := NewWorker(Config{
		Channels: fakeChannelLister{channels: []Channel{ch}},
		Stores: fakeStoreProvider{stores: map[channel.ChannelKey]Store{
			ch.Key: store,
		}},
		Runtime:     runtime,
		Metadata:    &fakeMetadataStore{},
		LocalNodeID: 1,
		TTL:         time.Hour,
		Now:         func() time.Time { return now },
	})

	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if len(runtime.applied) != 1 || runtime.applied[0].boundary != 10 {
		t.Fatalf("applied boundaries = %+v, want retry through 10", runtime.applied)
	}
	if len(store.confirms) != 0 {
		t.Fatalf("confirm calls = %+v, want none without new expired prefix", store.confirms)
	}
}

func TestRunOnceReturnsFirstErrorWithoutUnsafeAdvance(t *testing.T) {
	boom := errors.New("boom")
	ch1 := testChannel("first")
	ch2 := testChannel("second")
	tests := []struct {
		name             string
		store            *fakeStore
		metadata         *fakeMetadataStore
		runtime          *fakeRuntime
		wantAdvanceCalls int
	}{
		{
			name:             "scan error stops before confirm",
			store:            &fakeStore{scanErr: boom},
			metadata:         &fakeMetadataStore{},
			runtime:          &fakeRuntime{},
			wantAdvanceCalls: 0,
		},
		{
			name:             "confirm error stops before metadata advance",
			store:            &fakeStore{scan: ScanResult{ThroughSeq: 80}, confirmErr: boom},
			metadata:         &fakeMetadataStore{},
			runtime:          &fakeRuntime{},
			wantAdvanceCalls: 0,
		},
		{
			name:             "advance error is returned before local apply",
			store:            &fakeStore{scan: ScanResult{ThroughSeq: 80}, cursor: 80},
			metadata:         &fakeMetadataStore{err: boom},
			runtime:          &fakeRuntime{},
			wantAdvanceCalls: 1,
		},
		{
			name:             "local apply error is returned after metadata advance",
			store:            &fakeStore{scan: ScanResult{ThroughSeq: 80}, cursor: 80},
			metadata:         &fakeMetadataStore{},
			runtime:          &fakeRuntime{applyErr: boom},
			wantAdvanceCalls: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.runtime.views == nil {
				tt.runtime.views = map[channel.ChannelKey][]channel.RetentionView{}
			}
			tt.runtime.views[ch1.Key] = []channel.RetentionView{retentionViewWithMeta(10, 100, 100, 100, 7, 8, 1, time.Unix(2000, 0))}
			tt.runtime.views[ch2.Key] = []channel.RetentionView{retentionViewWithMeta(10, 100, 100, 100, 7, 8, 1, time.Unix(2000, 0))}
			worker := NewWorker(Config{
				Channels:    fakeChannelLister{channels: []Channel{ch1, ch2}},
				Stores:      fakeStoreProvider{stores: map[channel.ChannelKey]Store{ch1.Key: tt.store, ch2.Key: &fakeStore{}}},
				Runtime:     tt.runtime,
				Metadata:    tt.metadata,
				LocalNodeID: 1,
				TTL:         time.Hour,
				Now:         func() time.Time { return time.Unix(1000, 0) },
			})

			err := worker.RunOnce(context.Background())
			if !errors.Is(err, boom) {
				t.Fatalf("RunOnce() error = %v, want boom", err)
			}
			if len(tt.metadata.advances) != tt.wantAdvanceCalls {
				t.Fatalf("advance calls = %d, want %d", len(tt.metadata.advances), tt.wantAdvanceCalls)
			}
			if len(tt.runtime.viewCalls) != 1 && tt.name != "advance error is returned before local apply" && tt.name != "local apply error is returned after metadata advance" {
				t.Fatalf("runtime calls = %+v, want only first channel", tt.runtime.viewCalls)
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
		Stores:   fakeStoreProvider{stores: map[channel.ChannelKey]Store{ch.Key: &fakeStore{}}},
		Runtime: &fakeRuntime{views: map[channel.ChannelKey][]channel.RetentionView{
			ch.Key: {retentionViewWithMeta(0, 0, 0, 0, 1, 1, 1, time.Unix(2000, 0))},
		}},
		Metadata:     &fakeMetadataStore{},
		LocalNodeID:  1,
		TTL:          time.Hour,
		Now:          func() time.Time { return time.Unix(1000, 0) },
		ScanInterval: time.Hour,
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

func TestWorkerStartLogsBackgroundRunOnceError(t *testing.T) {
	logger := newFakeLogger()
	worker := NewWorker(Config{
		Channels:     fakeChannelLister{err: errors.New("list failed")},
		Stores:       fakeStoreProvider{},
		Runtime:      &fakeRuntime{},
		Metadata:     &fakeMetadataStore{},
		LocalNodeID:  1,
		TTL:          time.Hour,
		ScanInterval: time.Hour,
		Logger:       logger,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := worker.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Second)
		defer stopCancel()
		_ = worker.Stop(stopCtx)
	}()

	select {
	case entry := <-logger.warns:
		if entry.message != "channel retention pass failed" {
			t.Fatalf("warn message = %q, want retention pass failure", entry.message)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("worker did not log background RunOnce error before timeout")
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

type fakeRuntime struct {
	views     map[channel.ChannelKey][]channel.RetentionView
	errs      map[channel.ChannelKey]error
	viewCalls []channel.ChannelKey
	applied   []applyCall
	applyErr  error
	order     *[]string
}

func (f *fakeRuntime) RetentionView(ctx context.Context, key channel.ChannelKey) (channel.RetentionView, error) {
	f.viewCalls = append(f.viewCalls, key)
	if err := f.errs[key]; err != nil {
		return channel.RetentionView{}, err
	}
	views := f.views[key]
	if len(views) == 0 {
		return channel.RetentionView{}, nil
	}
	if len(views) > 1 {
		f.views[key] = views[1:]
	}
	return views[0], nil
}

func (f *fakeRuntime) ApplyRetentionBoundary(ctx context.Context, key channel.ChannelKey, throughSeq uint64) error {
	f.applied = append(f.applied, applyCall{key: key, boundary: throughSeq})
	if f.order != nil {
		*f.order = append(*f.order, "apply")
	}
	return f.applyErr
}

type scanCall struct {
	fromSeq uint64
	cutoff  time.Time
	limit   int
}

type confirmCall struct {
	name   string
	minSeq uint64
}

type fakeStore struct {
	scan       ScanResult
	scanErr    error
	cursor     uint64
	confirmErr error
	scans      []scanCall
	confirms   []confirmCall
	order      *[]string
}

func (f *fakeStore) ScanExpiredMessagePrefix(fromSeq uint64, cutoff time.Time, limit int) (ScanResult, error) {
	f.scans = append(f.scans, scanCall{fromSeq: fromSeq, cutoff: cutoff, limit: limit})
	if f.order != nil {
		*f.order = append(*f.order, "scan")
	}
	return f.scan, f.scanErr
}

func (f *fakeStore) ConfirmCommittedDispatchCursorDurable(name string, minSeq uint64) (uint64, error) {
	f.confirms = append(f.confirms, confirmCall{name: name, minSeq: minSeq})
	if f.order != nil {
		*f.order = append(*f.order, "confirm")
	}
	return f.cursor, f.confirmErr
}

type fakeStoreProvider struct {
	stores map[channel.ChannelKey]Store
	err    error
}

func (f fakeStoreProvider) StoreForChannel(ctx context.Context, ch Channel) (Store, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.stores[ch.Key], nil
}

type recordingStoreProvider struct {
	calls []Channel
}

func (r *recordingStoreProvider) StoreForChannel(ctx context.Context, ch Channel) (Store, error) {
	r.calls = append(r.calls, ch)
	return &fakeStore{}, nil
}

type fakeMetadataStore struct {
	advances []metadb.ChannelRetentionAdvance
	err      error
	order    *[]string
}

func (f *fakeMetadataStore) AdvanceChannelRetentionThroughSeq(ctx context.Context, req metadb.ChannelRetentionAdvance) error {
	f.advances = append(f.advances, req)
	if f.order != nil {
		*f.order = append(*f.order, "advance")
	}
	return f.err
}

type applyCall struct {
	key      channel.ChannelKey
	boundary uint64
}

type fakeLogEntry struct {
	message string
	fields  []wklog.Field
}

type fakeLogger struct {
	warns chan fakeLogEntry
}

func newFakeLogger() *fakeLogger {
	return &fakeLogger{warns: make(chan fakeLogEntry, 10)}
}

func (f *fakeLogger) Debug(string, ...wklog.Field) {}
func (f *fakeLogger) Info(string, ...wklog.Field)  {}
func (f *fakeLogger) Warn(msg string, fields ...wklog.Field) {
	f.warns <- fakeLogEntry{message: msg, fields: append([]wklog.Field(nil), fields...)}
}
func (f *fakeLogger) Error(string, ...wklog.Field) {}
func (f *fakeLogger) Fatal(string, ...wklog.Field) {}
func (f *fakeLogger) Named(string) wklog.Logger    { return f }
func (f *fakeLogger) With(...wklog.Field) wklog.Logger {
	return f
}
func (f *fakeLogger) Sync() error { return nil }

func testChannel(id string) Channel {
	return Channel{Key: channel.ChannelKey("channel/1/" + id), ID: channel.ChannelID{ID: id, Type: 1}}
}

func retentionView(current, hw, checkpointHW, minISR uint64) channel.RetentionView {
	return retentionViewWithMeta(current, hw, checkpointHW, minISR, 1, 1, 1, time.Unix(2000, 0))
}

func retentionViewWithMeta(current, hw, checkpointHW, minISR, epoch, leaderEpoch uint64, leader channel.NodeID, leaseUntil time.Time, opts ...func(*channel.RetentionView)) channel.RetentionView {
	view := channel.RetentionView{
		Epoch:                       epoch,
		LeaderEpoch:                 leaderEpoch,
		Leader:                      leader,
		LeaseUntil:                  leaseUntil,
		HW:                          hw,
		CheckpointHW:                checkpointHW,
		CommitReady:                 true,
		RetentionThroughSeq:         current,
		MinAvailableSeq:             channel.EffectiveMinAvailableSeq(current, 0),
		LocalRetentionThroughSeq:    current,
		PhysicalRetentionThroughSeq: current,
		MinISRMatchOffset:           minISR,
	}
	for _, opt := range opts {
		opt(&view)
	}
	return view
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

func TestRetentionAdvanceRequestKeepsZeroLeaseFenceAsZero(t *testing.T) {
	ch := testChannel("zero-lease")
	view := retentionViewWithMeta(10, 100, 100, 100, 7, 8, 1, time.Time{})
	req := retentionAdvanceRequest(ch, view, 20, time.Unix(1000, 0))

	if req.ExpectedLeaseUntilMS != 0 {
		t.Fatalf("ExpectedLeaseUntilMS = %d, want 0 for unset lease", req.ExpectedLeaseUntilMS)
	}
}

func TestRunOnceRequiresLocalNodeIDForLeaderGuard(t *testing.T) {
	ch := testChannel("missing-local-node")
	worker := NewWorker(Config{
		Channels: fakeChannelLister{channels: []Channel{ch}},
		Stores: fakeStoreProvider{stores: map[channel.ChannelKey]Store{
			ch.Key: &fakeStore{scan: ScanResult{ThroughSeq: 80}, cursor: 80},
		}},
		Runtime: &fakeRuntime{views: map[channel.ChannelKey][]channel.RetentionView{
			ch.Key: {retentionViewWithMeta(10, 100, 100, 100, 7, 8, 2, time.Unix(2000, 0))},
		}},
		Metadata: &fakeMetadataStore{},
		TTL:      time.Hour,
		Now:      func() time.Time { return time.Unix(1000, 0) },
	})

	err := worker.RunOnce(context.Background())
	if !errors.Is(err, channel.ErrInvalidConfig) {
		t.Fatalf("RunOnce() error = %v, want invalid config", err)
	}
}

func TestRunOnceFollowerLagBlocksThenObservedProgressAllowsAdvance(t *testing.T) {
	ch := testChannel("follower-lag")
	metadata := &fakeMetadataStore{}
	store := &fakeStore{scan: ScanResult{ThroughSeq: 10}, cursor: 10}
	runtime := &fakeRuntime{views: map[channel.ChannelKey][]channel.RetentionView{
		ch.Key: {
			retentionViewWithMeta(5, 10, 10, 5, 7, 8, 1, time.Unix(2000, 0)),
			retentionViewWithMeta(5, 10, 10, 10, 7, 8, 1, time.Unix(2000, 0)),
			retentionViewWithMeta(5, 10, 10, 10, 7, 8, 1, time.Unix(2000, 0)),
		},
	}}
	worker := NewWorker(Config{
		Channels: fakeChannelLister{channels: []Channel{ch}},
		Stores: fakeStoreProvider{stores: map[channel.ChannelKey]Store{
			ch.Key: store,
		}},
		Runtime:     runtime,
		Metadata:    metadata,
		LocalNodeID: 1,
		TTL:         time.Hour,
		Now:         func() time.Time { return time.Unix(1000, 0) },
	})

	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if len(metadata.advances) != 0 {
		t.Fatalf("metadata advances while follower lagged = %+v, want none", metadata.advances)
	}

	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatalf("second RunOnce() error = %v", err)
	}
	if len(metadata.advances) != 1 || metadata.advances[0].RetentionThroughSeq != 10 {
		t.Fatalf("metadata advances after follower progress = %+v, want through 10", metadata.advances)
	}
}
