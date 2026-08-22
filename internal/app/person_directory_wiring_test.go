package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/runtime/persondirectory"
	messageusecase "github.com/WuKongIM/WuKongIM/internal/usecase/message"
	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
	obsmetrics "github.com/WuKongIM/WuKongIM/pkg/metrics"
	runtimechannelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	slotproxy "github.com/WuKongIM/WuKongIM/pkg/slot/proxy"
)

func TestMessageSendDoesNotSynchronouslyAdmitPersonDirectory(t *testing.T) {
	cluster := &personDirectoryLifecycleCluster{admissionStarted: make(chan struct{}, 1)}
	app, err := newTestApp(t, Config{}, WithCluster(cluster), WithGateway(nil))
	if err != nil {
		t.Fatalf("newTestApp(): %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, sendErr := app.messages.Send(ctx, messageusecase.SendCommand{
			FromUID: "u1", ChannelID: runtimechannelid.EncodePersonChannel("u1", "u2"), ChannelType: 1,
		})
		done <- sendErr
	}()

	select {
	case <-cluster.admissionStarted:
		cancel()
		<-done
		t.Fatal("person SEND synchronously entered directory admission")
	case sendErr := <-done:
		if sendErr != nil {
			t.Fatalf("Send() error = %v, want append success without directory wait", sendErr)
		}
	}
}

func TestPersonDirectoryPressureUsesTerminalRuntimePoolSeries(t *testing.T) {
	registry := obsmetrics.New(1, "n1")
	observer := personDirectoryPressureMetricsObserver{metrics: registry}
	observer.ObservePersonDirectoryPressure(persondirectory.PressureObservation{
		Pending: 7, Inflight: 2, Capacity: 512, Workers: 8,
	})

	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("Gather(): %v", err)
	}
	wantPoolLabels := map[string]string{"component": "message", "pool": "person_directory"}
	workers := findAppMetricByLabels(t, requireAppMetricFamily(t, families, "wukongim_runtime_pool_workers"), wantPoolLabels)
	if got := workers.GetGauge().GetValue(); got != 8 {
		t.Fatalf("person-directory workers = %v, want 8", got)
	}
	inflight := findAppMetricByLabels(t, requireAppMetricFamily(t, families, "wukongim_runtime_pool_inflight"), wantPoolLabels)
	if got := inflight.GetGauge().GetValue(); got != 2 {
		t.Fatalf("person-directory inflight = %v, want 2", got)
	}
	wantQueueLabels := map[string]string{
		"component": "message", "pool": "person_directory", "queue": "task", "priority": "none",
	}
	depth := findAppMetricByLabels(t, requireAppMetricFamily(t, families, "wukongim_runtime_pool_queue_depth"), wantQueueLabels)
	if got := depth.GetGauge().GetValue(); got != 7 {
		t.Fatalf("person-directory pending depth = %v, want 7", got)
	}
	capacity := findAppMetricByLabels(t, requireAppMetricFamily(t, families, "wukongim_runtime_pool_queue_capacity"), wantQueueLabels)
	if got := capacity.GetGauge().GetValue(); got != 512 {
		t.Fatalf("person-directory queue capacity = %v, want 512", got)
	}
}

func TestAppOwnsPersonDirectoryProjectorLifecycle(t *testing.T) {
	task := metadb.PersonDirectoryTask{ChannelID: runtimechannelid.EncodePersonChannel("u1", "u2"), ChannelType: 1, Generation: 1, CommittedTail: 9, CreatedAt: 123}
	cluster := &personDirectoryLifecycleCluster{
		tasks: []metadb.PersonDirectoryTask{task}, projected: make(chan struct{}, 1), admissionStarted: make(chan struct{}, 1),
	}
	registry := goruntimeregistry.New()
	app, err := newTestApp(t, Config{}, WithCluster(cluster), WithGateway(nil), WithGoroutineRegistry(registry))
	if err != nil {
		t.Fatalf("newTestApp(): %v", err)
	}
	if app.personDirectoryProjector == nil {
		t.Fatal("person-directory projector was not wired")
	}
	if app.messageChannelStore == nil {
		t.Fatal("message person-directory admission store was not wired")
	}
	if err := app.Start(context.Background()); err != nil {
		t.Fatalf("Start(): %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = app.Stop(ctx)
	})
	select {
	case <-cluster.projected:
	case <-time.After(time.Second):
		t.Fatal("projector did not materialize the admitted task")
	}
	admissionResult := make(chan error, 1)
	go func() {
		admissionResult <- app.messageChannelStore.AdmitPersonChannelDirectory(
			context.Background(), runtimechannelid.EncodePersonChannel("u3", "u4"), 1,
		)
	}()
	select {
	case <-cluster.admissionStarted:
	case <-time.After(time.Second):
		t.Fatal("person-directory admission batch did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := app.Stop(ctx); err != nil {
		t.Fatalf("Stop(): %v", err)
	}
	if err := <-admissionResult; !errors.Is(err, context.Canceled) {
		t.Fatalf("person-directory admission error = %v, want canceled by App.Stop", err)
	}
	if snapshot := registry.Snapshot(); snapshot.ManagedTotal != 0 {
		t.Fatalf("managed goroutines after Stop = %d, want 0", snapshot.ManagedTotal)
	}
}

func TestAppStopPreservesClusterUntilPersonDirectoryProjectorJoins(t *testing.T) {
	task := metadb.PersonDirectoryTask{
		ChannelID: runtimechannelid.EncodePersonChannel("u1", "u2"), ChannelType: 1,
		Generation: 1, CommittedTail: 9, CreatedAt: 123,
	}
	source := &projectorStopTaskSource{task: task}
	writer := &uninterruptibleMembershipWriter{started: make(chan struct{}), release: make(chan struct{})}
	projector, err := persondirectory.New(persondirectory.Options{Source: source, Memberships: writer})
	if err != nil {
		t.Fatalf("persondirectory.New(): %v", err)
	}
	calls := make([]string, 0, 2)
	app := &App{cluster: &fakeCluster{calls: &calls}, personDirectoryProjector: projector}
	if err := app.Start(context.Background()); err != nil {
		t.Fatalf("Start(): %v", err)
	}
	select {
	case <-writer.started:
	case <-time.After(time.Second):
		close(writer.release)
		t.Fatal("person-directory projection did not start")
	}

	firstCtx, cancelFirst := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancelFirst()
	if err := app.Stop(firstCtx); !errors.Is(err, context.DeadlineExceeded) {
		close(writer.release)
		t.Fatalf("first Stop() error = %v, want projector join deadline", err)
	}
	if got := joinCalls(calls); got != "cluster.start" {
		close(writer.release)
		t.Fatalf("calls after timed-out projector stop = %s, want cluster dependency preserved", got)
	}

	close(writer.release)
	secondCtx, cancelSecond := context.WithTimeout(context.Background(), time.Second)
	defer cancelSecond()
	if err := app.Stop(secondCtx); err != nil {
		t.Fatalf("second Stop() error = %v", err)
	}
	if got := joinCalls(calls); got != "cluster.start,cluster.stop" {
		t.Fatalf("calls after projector joined = %s, want cluster stopped last", got)
	}
}

func TestStartRollbackCancelsPersonDirectoryAdmissionBeforeClusterStop(t *testing.T) {
	calls := make([]string, 0, 4)
	cluster := &personDirectoryLifecycleCluster{fakeCluster: fakeCluster{calls: &calls}, admissionStarted: make(chan struct{}, 1)}
	app, err := newTestApp(t, Config{}, WithCluster(cluster), WithGateway(nil))
	if err != nil {
		t.Fatalf("newTestApp(): %v", err)
	}
	admissionResult := make(chan error, 1)
	startErr := errors.New("top start failed")
	app.top = &recordingWorkerRuntime{
		name: "top", calls: &calls, startErr: startErr,
		onStart: func() {
			go func() {
				admissionResult <- app.messageChannelStore.AdmitPersonChannelDirectory(
					context.Background(), runtimechannelid.EncodePersonChannel("u3", "u4"), 1,
				)
			}()
			select {
			case <-cluster.admissionStarted:
			case <-time.After(time.Second):
				t.Fatal("person-directory admission did not start before rollback")
			}
		},
	}

	if err := app.Start(context.Background()); !errors.Is(err, startErr) {
		t.Fatalf("Start() error = %v, want top start failure", err)
	}
	select {
	case err := <-admissionResult:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("admission error = %v, want rollback cancellation", err)
		}
	case <-time.After(100 * time.Millisecond):
		stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = app.Stop(stopCtx)
		t.Fatal("rollback left the person-directory admission owner running")
	}
}

type personDirectoryLifecycleCluster struct {
	fakeCluster

	mu               sync.Mutex
	tasks            []metadb.PersonDirectoryTask
	projected        chan struct{}
	admissionStarted chan struct{}
}

func (*personDirectoryLifecycleCluster) NodeID() uint64 { return 1 }

func (*personDirectoryLifecycleCluster) ResolveChannelAppendAuthority(_ context.Context, id channelruntime.ChannelID) (channelruntime.Meta, error) {
	return fakeChannelAuthorityMeta(1, id), nil
}

func (*personDirectoryLifecycleCluster) AppendChannelBatch(_ context.Context, request channelruntime.AppendBatchRequest) (channelruntime.AppendBatchResult, error) {
	items := make([]channelruntime.AppendBatchItemResult, len(request.Messages))
	for i, message := range request.Messages {
		message.MessageSeq = uint64(i + 1)
		items[i] = channelruntime.AppendBatchItemResult{MessageID: message.MessageID, MessageSeq: message.MessageSeq, Message: message}
	}
	return channelruntime.AppendBatchResult{Items: items}, nil
}

type projectorStopTaskSource struct {
	task metadb.PersonDirectoryTask
}

func (*projectorStopTaskSource) LocalLeaderHashSlots(context.Context) ([]metadb.HashSlot, error) {
	return []metadb.HashSlot{7}, nil
}

func (*projectorStopTaskSource) IsLocalLeaderHashSlot(context.Context, metadb.HashSlot) (bool, error) {
	return true, nil
}

func (s *projectorStopTaskSource) ListPersonDirectoryTaskPage(context.Context, metadb.HashSlot, metadb.PersonDirectoryTaskCursor, int) ([]metadb.PersonDirectoryTask, metadb.PersonDirectoryTaskCursor, bool, error) {
	return []metadb.PersonDirectoryTask{s.task}, metadb.PersonDirectoryTaskCursor{ChannelID: s.task.ChannelID, ChannelType: s.task.ChannelType}, true, nil
}

func (*projectorStopTaskSource) ValidatePersonDirectoryTasks(_ context.Context, tasks []metadb.PersonDirectoryTaskLocation) []error {
	return make([]error, len(tasks))
}

func (*projectorStopTaskSource) CompletePersonDirectoryTasks(_ context.Context, tasks []metadb.PersonDirectoryTaskLocation) []error {
	return make([]error, len(tasks))
}

type uninterruptibleMembershipWriter struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (w *uninterruptibleMembershipWriter) EnsureUserChannelMembershipBatch(context.Context, []metadb.UserChannelMembership) []persondirectory.MembershipResult {
	w.once.Do(func() { close(w.started) })
	<-w.release
	return make([]persondirectory.MembershipResult, 2)
}

func (c *personDirectoryLifecycleCluster) GetChannelMetadata(context.Context, string, int64) (metadb.Channel, error) {
	return metadb.Channel{}, metadb.ErrNotFound
}

func (c *personDirectoryLifecycleCluster) GetChannelMetadataAuthoritative(context.Context, string, int64) (metadb.Channel, error) {
	return metadb.Channel{}, metadb.ErrNotFound
}

func (c *personDirectoryLifecycleCluster) UpsertChannelMetadata(context.Context, metadb.Channel) error {
	return nil
}

func (c *personDirectoryLifecycleCluster) DeleteChannelMetadata(context.Context, string, int64) error {
	return nil
}

func (c *personDirectoryLifecycleCluster) AddChannelSubscribers(context.Context, string, int64, []string, uint64) error {
	return nil
}

func (c *personDirectoryLifecycleCluster) RemoveChannelSubscribers(context.Context, string, int64, []string, uint64) error {
	return nil
}

func (c *personDirectoryLifecycleCluster) ListChannelSubscribersPage(context.Context, string, int64, string, int) ([]string, string, bool, error) {
	return nil, "", true, nil
}

func (c *personDirectoryLifecycleCluster) ListChannelSubscribersAuthoritative(context.Context, string, int64, string, int) ([]string, string, bool, error) {
	return nil, "", true, nil
}

func (c *personDirectoryLifecycleCluster) ContainsChannelSubscriberAuthoritative(context.Context, string, int64, string) (bool, error) {
	return false, nil
}

func (c *personDirectoryLifecycleCluster) HasChannelSubscribersAuthoritative(context.Context, string, int64) (bool, error) {
	return false, nil
}

func (c *personDirectoryLifecycleCluster) ReadPermissionMetadataBatchAuthoritative(_ context.Context, reads []slotproxy.PermissionMetadataRead) []slotproxy.PermissionMetadataReadResult {
	return make([]slotproxy.PermissionMetadataReadResult, len(reads))
}

func (c *personDirectoryLifecycleCluster) CommittedChannelTail(context.Context, string, int64) (uint64, error) {
	return 0, nil
}

func (c *personDirectoryLifecycleCluster) AdmitPersonDirectoryTasks(ctx context.Context, tasks []metadb.PersonDirectoryTask) []error {
	select {
	case c.admissionStarted <- struct{}{}:
	default:
	}
	<-ctx.Done()
	results := make([]error, len(tasks))
	for index := range results {
		results[index] = ctx.Err()
	}
	return results
}

func (c *personDirectoryLifecycleCluster) AdmitPersonDirectoryTaskWaves(ctx context.Context, tasks []metadb.PersonDirectoryTask, emit func(int, error)) {
	for i, err := range c.AdmitPersonDirectoryTasks(ctx, tasks) {
		emit(i, err)
	}
}

func (c *personDirectoryLifecycleCluster) LocalLeaderHashSlots(context.Context) ([]metadb.HashSlot, error) {
	return []metadb.HashSlot{7}, nil
}

func (c *personDirectoryLifecycleCluster) IsLocalLeaderHashSlot(context.Context, metadb.HashSlot) (bool, error) {
	return true, nil
}

func (c *personDirectoryLifecycleCluster) ListPersonDirectoryTaskPage(_ context.Context, _ metadb.HashSlot, _ metadb.PersonDirectoryTaskCursor, limit int) ([]metadb.PersonDirectoryTask, metadb.PersonDirectoryTaskCursor, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	end := min(limit, len(c.tasks))
	rows := append([]metadb.PersonDirectoryTask(nil), c.tasks[:end]...)
	cursor := metadb.PersonDirectoryTaskCursor{}
	if len(rows) > 0 {
		last := rows[len(rows)-1]
		cursor = metadb.PersonDirectoryTaskCursor{ChannelID: last.ChannelID, ChannelType: last.ChannelType}
	}
	return rows, cursor, end == len(c.tasks), nil
}

func (c *personDirectoryLifecycleCluster) EnsureUserChannelMembershipBatch(_ context.Context, memberships []metadb.UserChannelMembership) []error {
	if len(memberships) != 2 {
		return []error{metadb.ErrInvalidArgument}
	}
	return make([]error, len(memberships))
}

func (c *personDirectoryLifecycleCluster) ValidatePersonDirectoryTasks(_ context.Context, tasks []metadb.PersonDirectoryTaskLocation) []error {
	return make([]error, len(tasks))
}

func (c *personDirectoryLifecycleCluster) CompletePersonDirectoryTasks(_ context.Context, tasks []metadb.PersonDirectoryTaskLocation) []error {
	c.mu.Lock()
	c.tasks = nil
	c.mu.Unlock()
	select {
	case c.projected <- struct{}{}:
	default:
	}
	return make([]error, len(tasks))
}
