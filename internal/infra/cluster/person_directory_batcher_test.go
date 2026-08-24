package cluster

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
)

func TestPersonDirectoryBatcherUsesBoundedCollectionWindowAndBatch(t *testing.T) {
	batcher := newPersonDirectoryBatcher(&recordingPersonDirectoryBatchNode{}, nil)
	if batcher.collectWait != 50*time.Millisecond {
		t.Fatalf("collect wait = %v, want 50ms", batcher.collectWait)
	}
	if batcher.targetItems != 32 || personDirectoryBatchMaxItems != 128 {
		t.Fatalf("target/max batch items = %d/%d, want 32/128", batcher.targetItems, personDirectoryBatchMaxItems)
	}
	if cap(batcher.active) != 8 {
		t.Fatalf("active batch capacity = %d, want 8", cap(batcher.active))
	}
}

func TestPersonDirectoryBatcherStopCancelsAndJoinsOwnedBatch(t *testing.T) {
	node := &blockingPersonDirectoryBatchNode{started: make(chan struct{}, 1)}
	registry := goruntimeregistry.New()
	batcher := newPersonDirectoryBatcher(node, registry)
	batcher.collectWait = time.Hour
	batcher.targetItems = 1
	result := make(chan error, 1)
	go func() {
		result <- batcher.ensure(context.Background(), testPersonDirectoryMutation(0))
	}()

	select {
	case <-node.started:
	case <-time.After(time.Second):
		t.Fatal("person-directory batch did not start")
	}
	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := batcher.Stop(stopCtx); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("ensure() error = %v, want canceled owned admission", err)
	}
	if snapshot := registry.Snapshot(); snapshot.ManagedTotal != 0 {
		t.Fatalf("managed goroutines after Stop = %d, want 0", snapshot.ManagedTotal)
	}
	if err := batcher.ensure(context.Background(), testPersonDirectoryMutation(1)); !errors.Is(err, errPersonDirectoryBatcherStopped) {
		t.Fatalf("ensure() after Stop error = %v, want stopped", err)
	}
}

func TestPersonDirectoryBatcherCoalescesConcurrentChannelsIntoOneDurableAdmission(t *testing.T) {
	node := &recordingPersonDirectoryBatchNode{}
	batcher := newPersonDirectoryBatcher(node, nil)
	batcher.collectWait = time.Hour
	batcher.targetItems = 4

	errCh := make(chan error, 4)
	for i := 0; i < 4; i++ {
		index := i
		go func() {
			errCh <- batcher.ensure(context.Background(), testPersonDirectoryMutation(index))
		}()
	}
	for range 4 {
		if err := <-errCh; err != nil {
			t.Fatalf("ensure() error = %v", err)
		}
	}
	node.mu.Lock()
	defer node.mu.Unlock()
	if node.admissionCalls != 1 || len(node.tasks) != 4 {
		t.Fatalf("admission calls/tasks = %d/%d, want 1/4", node.admissionCalls, len(node.tasks))
	}
}

func TestPersonDirectoryBatcherSealsVectorAdmissionAtTargetSize(t *testing.T) {
	const (
		targetItems = 12
		totalItems  = 24
	)
	node := &recordingPersonDirectoryBatchNode{}
	batcher := newPersonDirectoryBatcher(node, nil)
	batcher.collectWait = time.Hour
	batcher.targetItems = targetItems
	admissions := make([]personDirectoryBatchAdmission, totalItems)
	for i := range admissions {
		admissions[i] = personDirectoryBatchAdmission{
			ctx:      context.Background(),
			mutation: testPersonDirectoryMutation(i),
		}
	}

	results := batcher.ensureBatch(admissions)
	for i, err := range results {
		if err != nil {
			t.Fatalf("result %d = %v", i, err)
		}
	}
	node.mu.Lock()
	defer node.mu.Unlock()
	if got, want := node.batchSizes, []int{targetItems, targetItems}; !equalInts(got, want) {
		t.Fatalf("durable batch sizes = %v, want %v", got, want)
	}
}

func TestPersonDirectoryBatcherEmitsCompletedWaveBeforeSlowSiblingBatch(t *testing.T) {
	releaseSlow := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(releaseSlow) }) })
	node := &stagedPersonDirectoryBatchNode{releaseSlow: releaseSlow}
	batcher := newPersonDirectoryBatcher(node, nil)
	batcher.collectWait = time.Hour
	batcher.targetItems = 2
	admissions := make([]personDirectoryBatchAdmission, 4)
	for i := range admissions {
		admissions[i] = personDirectoryBatchAdmission{ctx: context.Background(), mutation: testPersonDirectoryMutation(i)}
	}
	waves := make(chan []personDirectoryBatchOutcome, 2)
	done := make(chan struct{})
	go func() {
		batcher.ensureBatchWaves(admissions, func(wave []personDirectoryBatchOutcome) {
			waves <- append([]personDirectoryBatchOutcome(nil), wave...)
		})
		close(done)
	}()

	select {
	case wave := <-waves:
		if got := directoryOutcomeIndexes(wave); !equalInts(got, []int{0, 1}) {
			t.Fatalf("first completed wave indexes = %v, want [0 1]", got)
		}
	case <-time.After(time.Second):
		t.Fatal("fast durable batch was held behind slow sibling")
	}
	releaseOnce.Do(func() { close(releaseSlow) })
	select {
	case wave := <-waves:
		if got := directoryOutcomeIndexes(wave); !equalInts(got, []int{2, 3}) {
			t.Fatalf("second completed wave indexes = %v, want [2 3]", got)
		}
	case <-time.After(time.Second):
		t.Fatal("slow durable batch did not complete after release")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("wave admission did not join all owned work")
	}
}

func TestPersonDirectoryBatcherPublishesFastSourceSlotWithinOneDurableBatch(t *testing.T) {
	releaseSlow := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(releaseSlow) }) })
	node := &stagedIntraBatchPersonDirectoryBatchNode{releaseSlow: releaseSlow}
	batcher := newPersonDirectoryBatcher(node, nil)
	batcher.collectWait = time.Hour
	batcher.targetItems = 2
	admissions := []personDirectoryBatchAdmission{
		{ctx: context.Background(), mutation: testPersonDirectoryMutation(0)},
		{ctx: context.Background(), mutation: testPersonDirectoryMutation(1)},
	}
	waves := make(chan []personDirectoryBatchOutcome, 2)
	done := make(chan struct{})
	go func() {
		batcher.ensureBatchWaves(admissions, func(wave []personDirectoryBatchOutcome) {
			waves <- append([]personDirectoryBatchOutcome(nil), wave...)
		})
		close(done)
	}()

	select {
	case wave := <-waves:
		if got := directoryOutcomeIndexes(wave); !equalInts(got, []int{0}) {
			releaseOnce.Do(func() { close(releaseSlow) })
			t.Fatalf("first completed wave indexes = %v, want fast source index 0", got)
		}
	case <-time.After(100 * time.Millisecond):
		releaseOnce.Do(func() { close(releaseSlow) })
		t.Fatal("fast source result was held until every Slot in the durable batch completed")
	}
	releaseOnce.Do(func() { close(releaseSlow) })
	select {
	case wave := <-waves:
		if got := directoryOutcomeIndexes(wave); !equalInts(got, []int{1}) {
			t.Fatalf("second completed wave indexes = %v, want slow source index 1", got)
		}
	case <-time.After(time.Second):
		t.Fatal("slow source result did not complete after release")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("batched source admission did not join")
	}
}

func TestPersonDirectoryBatcherSingleflightsSameChannelWhileSealedBatchIsActive(t *testing.T) {
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseAll := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseAll)

	node := &blockingPersonDirectoryBatchNode{
		started: make(chan struct{}, 2),
		release: release,
	}
	batcher := newPersonDirectoryBatcher(node, nil)
	batcher.collectWait = time.Hour
	batcher.targetItems = 1
	mutation := testPersonDirectoryMutation(0)
	results := make(chan error, 2)
	go func() { results <- batcher.ensure(context.Background(), mutation) }()
	select {
	case <-node.started:
	case <-time.After(time.Second):
		t.Fatal("first person-directory batch did not start")
	}

	go func() { results <- batcher.ensure(context.Background(), mutation) }()
	select {
	case <-node.started:
		releaseAll()
		for range 2 {
			<-results
		}
		t.Fatal("same channel started a second durable batch while the first batch was active")
	case <-time.After(50 * time.Millisecond):
	}

	releaseAll()
	for range 2 {
		if err := <-results; err != nil {
			t.Fatalf("ensure() error = %v", err)
		}
	}
}

func TestPersonDirectoryBatcherReturnsAdmissionFailure(t *testing.T) {
	admissionErr := errors.New("admission failed")
	node := &recordingPersonDirectoryBatchNode{admissionErr: admissionErr}
	batcher := newPersonDirectoryBatcher(node, nil)
	batcher.collectWait = time.Millisecond
	batcher.targetItems = 8

	err := batcher.ensure(context.Background(), testPersonDirectoryMutation(0))
	if !errors.Is(err, admissionErr) {
		t.Fatalf("ensure() error = %v, want admission failure", err)
	}
}

func TestPersonDirectoryBatcherPreservesAlignedPartialAdmissionResults(t *testing.T) {
	admissionErr := errors.New("second source slot unavailable")
	node := &partialPersonDirectoryBatchNode{admissionErr: admissionErr}
	batcher := newPersonDirectoryBatcher(node, nil)
	batcher.collectWait = time.Hour
	batcher.targetItems = 2

	first := make(chan error, 1)
	second := make(chan error, 1)
	go func() { first <- batcher.ensure(context.Background(), testPersonDirectoryMutation(0)) }()
	go func() { second <- batcher.ensure(context.Background(), testPersonDirectoryMutation(1)) }()

	if err := <-first; err != nil {
		t.Fatalf("first aligned admission error = %v, want success", err)
	}
	if err := <-second; !errors.Is(err, admissionErr) {
		t.Fatalf("second aligned admission error = %v, want %v", err, admissionErr)
	}
}

func TestPersonDirectoryBatcherWaitsForCapacityInsteadOfRejectingColdWave(t *testing.T) {
	const queuedItems = 32
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseAll := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseAll)

	node := &blockingPersonDirectoryBatchNode{release: release}
	batcher := newPersonDirectoryBatcher(node, nil)
	batcher.collectWait = time.Hour
	batcher.targetItems = 1
	batcher.maxQueued = queuedItems

	results := make(chan error, queuedItems+1)
	for index := 0; index < queuedItems; index++ {
		index := index
		go func() {
			results <- batcher.ensure(context.Background(), testPersonDirectoryMutation(index))
		}()
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		batcher.mu.Lock()
		queued := batcher.queuedItems
		batcher.mu.Unlock()
		if queued == queuedItems {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("queued person directories = %d, want %d", queued, queuedItems)
		}
		time.Sleep(time.Millisecond)
	}

	extra := make(chan error, 1)
	go func() {
		extra <- batcher.ensure(context.Background(), testPersonDirectoryMutation(queuedItems))
	}()
	select {
	case err := <-extra:
		releaseAll()
		for range queuedItems {
			<-results
		}
		t.Fatalf("extra ensure returned %v while the bounded queue was transiently full; want it to wait", err)
	case <-time.After(50 * time.Millisecond):
	}

	releaseAll()
	for range queuedItems {
		if err := <-results; err != nil {
			t.Fatalf("queued ensure error = %v", err)
		}
	}
	if err := <-extra; err != nil {
		t.Fatalf("extra ensure after capacity release error = %v", err)
	}
}

func TestPersonDirectoryBatcherRunsEightColdDirectoryBatchesConcurrently(t *testing.T) {
	const concurrentBatches = 8
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseAll := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseAll)

	node := &blockingPersonDirectoryBatchNode{
		started: make(chan struct{}, concurrentBatches),
		release: release,
	}
	batcher := newPersonDirectoryBatcher(node, nil)
	batcher.collectWait = time.Hour
	batcher.targetItems = 1

	results := make(chan error, concurrentBatches)
	for index := 0; index < concurrentBatches; index++ {
		index := index
		go func() {
			results <- batcher.ensure(context.Background(), testPersonDirectoryMutation(index))
		}()
		select {
		case <-node.started:
		case <-time.After(time.Second):
			releaseAll()
			for range index + 1 {
				<-results
			}
			t.Fatalf("active person-directory batches = %d, want %d", index, concurrentBatches)
		}
	}
	releaseAll()
	for range concurrentBatches {
		if err := <-results; err != nil {
			t.Fatalf("ensure() error = %v", err)
		}
	}
}

func testPersonDirectoryMutation(index int) personDirectoryMutation {
	channelID := string(rune('a'+index)) + "@z"
	return personDirectoryMutation{
		task: metadb.PersonDirectoryTask{ChannelID: channelID, ChannelType: 1, CommittedTail: uint64(index), CreatedAt: 1},
	}
}

type recordingPersonDirectoryBatchNode struct {
	mu sync.Mutex

	admissionCalls int
	tasks          []metadb.PersonDirectoryTask
	batchSizes     []int
	admissionErr   error
}

type blockingPersonDirectoryBatchNode struct {
	started chan struct{}
	release <-chan struct{}
}

type partialPersonDirectoryBatchNode struct {
	admissionErr error
}

type stagedPersonDirectoryBatchNode struct {
	releaseSlow <-chan struct{}
}

type stagedIntraBatchPersonDirectoryBatchNode struct {
	releaseSlow <-chan struct{}
}

func (n *stagedIntraBatchPersonDirectoryBatchNode) AdmitPersonDirectoryTaskWaves(ctx context.Context, tasks []metadb.PersonDirectoryTask, emit func(int, error)) {
	if len(tasks) > 0 {
		emit(0, nil)
	}
	if len(tasks) < 2 {
		return
	}
	select {
	case <-ctx.Done():
		emit(1, ctx.Err())
	case <-n.releaseSlow:
		emit(1, nil)
	}
}

func (n *stagedIntraBatchPersonDirectoryBatchNode) AdmitPersonDirectoryTasks(ctx context.Context, tasks []metadb.PersonDirectoryTask) []error {
	results := make([]error, len(tasks))
	n.AdmitPersonDirectoryTaskWaves(ctx, tasks, func(index int, err error) { results[index] = err })
	return results
}

func (n *stagedPersonDirectoryBatchNode) AdmitPersonDirectoryTasks(ctx context.Context, tasks []metadb.PersonDirectoryTask) []error {
	results := make([]error, len(tasks))
	if len(tasks) == 0 || tasks[0].ChannelID != "c@z" {
		return results
	}
	select {
	case <-ctx.Done():
		for i := range results {
			results[i] = ctx.Err()
		}
	case <-n.releaseSlow:
	}
	return results
}

func (n *stagedPersonDirectoryBatchNode) AdmitPersonDirectoryTaskWaves(ctx context.Context, tasks []metadb.PersonDirectoryTask, emit func(int, error)) {
	emitPersonDirectoryAdmissionResults(n.AdmitPersonDirectoryTasks(ctx, tasks), emit)
}

func (n *partialPersonDirectoryBatchNode) AdmitPersonDirectoryTasks(_ context.Context, tasks []metadb.PersonDirectoryTask) []error {
	results := make([]error, len(tasks))
	for i, task := range tasks {
		if task.ChannelID == "b@z" {
			results[i] = n.admissionErr
		}
	}
	return results
}

func (n *partialPersonDirectoryBatchNode) AdmitPersonDirectoryTaskWaves(ctx context.Context, tasks []metadb.PersonDirectoryTask, emit func(int, error)) {
	emitPersonDirectoryAdmissionResults(n.AdmitPersonDirectoryTasks(ctx, tasks), emit)
}

func (n *blockingPersonDirectoryBatchNode) AdmitPersonDirectoryTasks(ctx context.Context, tasks []metadb.PersonDirectoryTask) []error {
	if n.started != nil {
		n.started <- struct{}{}
	}
	var err error
	select {
	case <-ctx.Done():
		err = ctx.Err()
	case <-n.release:
	}
	results := make([]error, len(tasks))
	for i := range results {
		results[i] = err
	}
	return results
}

func (n *blockingPersonDirectoryBatchNode) AdmitPersonDirectoryTaskWaves(ctx context.Context, tasks []metadb.PersonDirectoryTask, emit func(int, error)) {
	emitPersonDirectoryAdmissionResults(n.AdmitPersonDirectoryTasks(ctx, tasks), emit)
}

func (n *recordingPersonDirectoryBatchNode) AdmitPersonDirectoryTasks(_ context.Context, tasks []metadb.PersonDirectoryTask) []error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.admissionCalls++
	n.tasks = append(n.tasks, tasks...)
	n.batchSizes = append(n.batchSizes, len(tasks))
	results := make([]error, len(tasks))
	for i := range results {
		results[i] = n.admissionErr
	}
	return results
}

func (n *recordingPersonDirectoryBatchNode) AdmitPersonDirectoryTaskWaves(ctx context.Context, tasks []metadb.PersonDirectoryTask, emit func(int, error)) {
	emitPersonDirectoryAdmissionResults(n.AdmitPersonDirectoryTasks(ctx, tasks), emit)
}

func emitPersonDirectoryAdmissionResults(results []error, emit func(int, error)) {
	for i, err := range results {
		emit(i, err)
	}
}

func equalInts(left, right []int) bool {
	if len(left) != len(right) {
		return false
	}
	counts := make(map[int]int, len(left))
	for _, value := range left {
		counts[value]++
	}
	for _, value := range right {
		counts[value]--
		if counts[value] < 0 {
			return false
		}
	}
	return true
}

func directoryOutcomeIndexes(outcomes []personDirectoryBatchOutcome) []int {
	indexes := make([]int, len(outcomes))
	for i, outcome := range outcomes {
		indexes[i] = outcome.index
	}
	return indexes
}
