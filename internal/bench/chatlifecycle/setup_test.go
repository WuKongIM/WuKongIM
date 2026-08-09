package chatlifecycle

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
	"github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
)

func TestGroupSetupPreparesDeterministicBoundedGroupCatalog(t *testing.T) {
	cfg := LocalConfig()
	cfg.RunID = "setup-bounded"
	target := &recordingGroupSetupTarget{}
	setup, err := NewGroupSetup(GroupSetupOptions{
		Target:                 target,
		MaxChannelsPerBatch:    7,
		MaxSubscribersPerBatch: 11,
	})
	if err != nil {
		t.Fatalf("NewGroupSetup() error = %v", err)
	}
	if err := setup.Run(context.Background(), cfg); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	identity, err := NewIdentitySpace(cfg.RunID, cfg.Seed, uint64(cfg.Workload.Workers))
	if err != nil {
		t.Fatalf("NewIdentitySpace() error = %v", err)
	}
	catalog, err := NewGroupCatalog(identity, cfg.Workload.Groups)
	if err != nil {
		t.Fatalf("NewGroupCatalog() error = %v", err)
	}
	wantMembers := 0
	for index := 0; index < catalog.Count(); index++ {
		group, groupErr := catalog.Group(uint64(index))
		if groupErr != nil {
			t.Fatalf("Group(%d) error = %v", index, groupErr)
		}
		wantMembers += group.MemberCount
	}
	if target.channels != catalog.Count() {
		t.Fatalf("prepared channels = %d, want %d", target.channels, catalog.Count())
	}
	if target.subscribers != wantMembers {
		t.Fatalf("prepared subscribers = %d, want %d", target.subscribers, wantMembers)
	}
	if target.maxChannelBatch > 7 {
		t.Fatalf("largest channel batch = %d, want <= 7", target.maxChannelBatch)
	}
	if target.maxSubscriberBatch > 11 {
		t.Fatalf("largest subscriber batch = %d, want <= 11", target.maxSubscriberBatch)
	}
	if target.nonGroupChannel {
		t.Fatal("setup attempted to pre-create a person or unknown channel type")
	}
	if target.nonCatalogChannel {
		t.Fatal("setup attempted to pre-create a channel outside the fixed group catalog")
	}
	if target.duplicateBatchID {
		t.Fatal("setup reused a batch ID for different requests")
	}
}

func TestGroupSetupRejectsSubscriberBatchAboveTargetCommandLimit(t *testing.T) {
	if MaxGroupSetupSubscribersPerBatch != fsm.MaxSubscriberCommandUIDs {
		t.Fatalf("setup subscriber batch limit = %d, Raft command limit = %d", MaxGroupSetupSubscribersPerBatch, fsm.MaxSubscriberCommandUIDs)
	}
	target := &recordingGroupSetupTarget{}
	if _, err := NewGroupSetup(GroupSetupOptions{
		Target: target, MaxChannelsPerBatch: 1,
		MaxSubscribersPerBatch: MaxGroupSetupSubscribersPerBatch,
	}); err != nil {
		t.Fatalf("NewGroupSetup() at command limit error = %v", err)
	}
	if _, err := NewGroupSetup(GroupSetupOptions{
		Target: target, MaxChannelsPerBatch: 1,
		MaxSubscribersPerBatch: MaxGroupSetupSubscribersPerBatch + 1,
	}); !errors.Is(err, ErrGroupSetupConfig) {
		t.Fatalf("NewGroupSetup() above command limit error = %v, want %v", err, ErrGroupSetupConfig)
	}
}

func TestGroupSetupFencesOneRunShapeAndRetriesPartialSetup(t *testing.T) {
	cfg := LocalConfig()
	cfg.RunID = "setup-idempotent"
	target := &failingGroupSetupTarget{failSubscribersOnce: true}
	setup, err := NewGroupSetup(GroupSetupOptions{
		Target: target, MaxChannelsPerBatch: 7, MaxSubscribersPerBatch: 31,
	})
	if err != nil {
		t.Fatalf("NewGroupSetup() error = %v", err)
	}

	if err := setup.Run(context.Background(), cfg); !errors.Is(err, errInjectedGroupSetup) {
		t.Fatalf("first Run() error = %v, want injected target failure", err)
	}
	afterPartial := target.calls
	if afterPartial == 0 {
		t.Fatal("partial setup made no target calls")
	}
	if err := setup.Run(context.Background(), cfg); err != nil {
		t.Fatalf("retry Run() error = %v", err)
	}
	afterComplete := target.calls
	if afterComplete <= afterPartial {
		t.Fatalf("retry target calls = %d, want > partial %d", afterComplete, afterPartial)
	}
	if err := setup.Run(context.Background(), cfg); err != nil {
		t.Fatalf("completed duplicate Run() error = %v", err)
	}
	if target.calls != afterComplete {
		t.Fatalf("completed duplicate target calls = %d, want unchanged %d", target.calls, afterComplete)
	}

	mismatch := cfg
	mismatch.Seed++
	if err := setup.Run(context.Background(), mismatch); !errors.Is(err, ErrGroupSetupShapeMismatch) {
		t.Fatalf("shape mismatch Run() error = %v, want %v", err, ErrGroupSetupShapeMismatch)
	}
	if target.calls != afterComplete {
		t.Fatalf("shape mismatch target calls = %d, want unchanged %d", target.calls, afterComplete)
	}

	classMismatch := cfg
	classMismatch.Workload.Groups.Small--
	classMismatch.Workload.Groups.Medium++
	if err := setup.Run(context.Background(), classMismatch); !errors.Is(err, ErrGroupSetupShapeMismatch) {
		t.Fatalf("class mismatch Run() error = %v, want %v", err, ErrGroupSetupShapeMismatch)
	}
	if target.calls != afterComplete {
		t.Fatalf("class mismatch target calls = %d, want unchanged %d", target.calls, afterComplete)
	}

	otherRun := cfg
	otherRun.RunID = "setup-other-run"
	if err := setup.Run(context.Background(), otherRun); !errors.Is(err, ErrGroupSetupRunConflict) {
		t.Fatalf("other run Run() error = %v, want %v", err, ErrGroupSetupRunConflict)
	}
	if target.calls != afterComplete {
		t.Fatalf("other run target calls = %d, want unchanged %d", target.calls, afterComplete)
	}
}

func TestGroupSetupRunsConcurrentExactCallsAsOneTargetMutationStream(t *testing.T) {
	cfg := LocalConfig()
	cfg.RunID = "setup-single-flight"
	target := &blockingGroupSetupTarget{entered: make(chan struct{}), release: make(chan struct{})}
	setup, err := NewGroupSetup(GroupSetupOptions{
		Target: target, MaxChannelsPerBatch: 7, MaxSubscribersPerBatch: 31,
	})
	if err != nil {
		t.Fatalf("NewGroupSetup() error = %v", err)
	}

	errorsChannel := make(chan error, 2)
	go func() { errorsChannel <- setup.Run(context.Background(), cfg) }()
	<-target.entered
	go func() { errorsChannel <- setup.Run(context.Background(), cfg) }()
	close(target.release)
	for call := 0; call < 2; call++ {
		if err := <-errorsChannel; err != nil {
			t.Fatalf("concurrent Run() error = %v", err)
		}
	}

	baseline := &countingGroupSetupTarget{}
	baselineSetup, err := NewGroupSetup(GroupSetupOptions{
		Target: baseline, MaxChannelsPerBatch: 7, MaxSubscribersPerBatch: 31,
	})
	if err != nil {
		t.Fatalf("baseline NewGroupSetup() error = %v", err)
	}
	if err := baselineSetup.Run(context.Background(), cfg); err != nil {
		t.Fatalf("baseline Run() error = %v", err)
	}
	if got, want := target.calls.Load(), baseline.calls.Load(); got != want {
		t.Fatalf("concurrent target calls = %d, want one mutation stream with %d calls", got, want)
	}
}

func TestGroupSetupWaiterCanCancelWhileTargetMutationIsInFlight(t *testing.T) {
	cfg := LocalConfig()
	cfg.RunID = "setup-cancel-waiter"
	target := &blockingGroupSetupTarget{entered: make(chan struct{}), release: make(chan struct{})}
	setup, err := NewGroupSetup(GroupSetupOptions{
		Target: target, MaxChannelsPerBatch: 7, MaxSubscribersPerBatch: 31,
	})
	if err != nil {
		t.Fatalf("NewGroupSetup() error = %v", err)
	}

	first := make(chan error, 1)
	go func() { first <- setup.Run(context.Background(), cfg) }()
	<-target.entered

	waiterContext, cancelWaiter := context.WithCancel(context.Background())
	waiter := make(chan error, 1)
	go func() { waiter <- setup.Run(waiterContext, cfg) }()
	cancelWaiter()
	select {
	case err := <-waiter:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled waiter error = %v, want context canceled", err)
		}
	case <-time.After(250 * time.Millisecond):
		close(target.release)
		<-first
		t.Fatal("canceled waiter remained blocked behind target I/O")
	}
	close(target.release)
	if err := <-first; err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
}

func TestGroupSetupRejectsConflictsWhileExactMutationIsInFlight(t *testing.T) {
	cfg := LocalConfig()
	cfg.RunID = "setup-inflight-conflict"
	target := &blockingGroupSetupTarget{entered: make(chan struct{}), release: make(chan struct{})}
	setup, err := NewGroupSetup(GroupSetupOptions{
		Target: target, MaxChannelsPerBatch: 7, MaxSubscribersPerBatch: 31,
	})
	if err != nil {
		t.Fatalf("NewGroupSetup() error = %v", err)
	}

	first := make(chan error, 1)
	go func() { first <- setup.Run(context.Background(), cfg) }()
	<-target.entered

	shape := cfg
	shape.Seed++
	shapeResult := make(chan error, 1)
	go func() { shapeResult <- setup.Run(context.Background(), shape) }()
	var shapeErr error
	select {
	case shapeErr = <-shapeResult:
	case <-time.After(250 * time.Millisecond):
		close(target.release)
		<-first
		t.Fatal("in-flight shape mismatch blocked behind target I/O")
	}
	if !errors.Is(shapeErr, ErrGroupSetupShapeMismatch) {
		close(target.release)
		<-first
		t.Fatalf("in-flight shape mismatch error = %v, want %v", shapeErr, ErrGroupSetupShapeMismatch)
	}
	other := cfg
	other.RunID = "setup-inflight-other"
	otherResult := make(chan error, 1)
	go func() { otherResult <- setup.Run(context.Background(), other) }()
	var otherErr error
	select {
	case otherErr = <-otherResult:
	case <-time.After(250 * time.Millisecond):
		close(target.release)
		<-first
		t.Fatal("in-flight run conflict blocked behind target I/O")
	}
	if !errors.Is(otherErr, ErrGroupSetupRunConflict) {
		close(target.release)
		<-first
		t.Fatalf("in-flight run conflict error = %v, want %v", otherErr, ErrGroupSetupRunConflict)
	}
	if got := target.calls.Load(); got != 1 {
		close(target.release)
		<-first
		t.Fatalf("target calls after in-flight conflicts = %d, want 1", got)
	}
	close(target.release)
	if err := <-first; err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
}

var errInjectedGroupSetup = errors.New("injected group setup failure")

type failingGroupSetupTarget struct {
	calls               int
	failSubscribersOnce bool
}

type countingGroupSetupTarget struct {
	calls atomic.Int64
}

func (t *countingGroupSetupTarget) UpsertChannels(context.Context, model.BatchChannelsRequest) error {
	t.calls.Add(1)
	return nil
}

func (t *countingGroupSetupTarget) AddSubscribers(context.Context, model.BatchSubscribersRequest) error {
	t.calls.Add(1)
	return nil
}

type blockingGroupSetupTarget struct {
	countingGroupSetupTarget
	once    sync.Once
	entered chan struct{}
	release chan struct{}
}

func (t *blockingGroupSetupTarget) UpsertChannels(context.Context, model.BatchChannelsRequest) error {
	t.calls.Add(1)
	t.once.Do(func() {
		close(t.entered)
		<-t.release
	})
	return nil
}

func (t *failingGroupSetupTarget) UpsertChannels(context.Context, model.BatchChannelsRequest) error {
	t.calls++
	return nil
}

func (t *failingGroupSetupTarget) AddSubscribers(context.Context, model.BatchSubscribersRequest) error {
	t.calls++
	if t.failSubscribersOnce {
		t.failSubscribersOnce = false
		return errInjectedGroupSetup
	}
	return nil
}

type recordingGroupSetupTarget struct {
	channels           int
	subscribers        int
	maxChannelBatch    int
	maxSubscriberBatch int
	nonGroupChannel    bool
	nonCatalogChannel  bool
	duplicateBatchID   bool
	batchIDs           map[string]struct{}
}

func (t *recordingGroupSetupTarget) UpsertChannels(_ context.Context, req model.BatchChannelsRequest) error {
	t.recordBatch(req.BatchID)
	if len(req.Channels) > t.maxChannelBatch {
		t.maxChannelBatch = len(req.Channels)
	}
	for _, channel := range req.Channels {
		if channel.ChannelType != groupChannelType {
			t.nonGroupChannel = true
		}
		if len(channel.ChannelID) < len(groupIDPrefix) || channel.ChannelID[:len(groupIDPrefix)] != groupIDPrefix {
			t.nonCatalogChannel = true
		}
		t.channels++
	}
	return nil
}

func (t *recordingGroupSetupTarget) AddSubscribers(_ context.Context, req model.BatchSubscribersRequest) error {
	t.recordBatch(req.BatchID)
	for _, item := range req.Items {
		if item.ChannelType != groupChannelType {
			t.nonGroupChannel = true
		}
		if len(item.Subscribers) > t.maxSubscriberBatch {
			t.maxSubscriberBatch = len(item.Subscribers)
		}
		t.subscribers += len(item.Subscribers)
	}
	return nil
}

func (t *recordingGroupSetupTarget) recordBatch(batchID string) {
	if t.batchIDs == nil {
		t.batchIDs = make(map[string]struct{})
	}
	if _, duplicate := t.batchIDs[batchID]; duplicate {
		t.duplicateBatchID = true
	}
	t.batchIDs[batchID] = struct{}{}
}
