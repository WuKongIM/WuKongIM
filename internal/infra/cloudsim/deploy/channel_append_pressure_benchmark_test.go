package deploy

import (
	"context"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	channel "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelworker "github.com/WuKongIM/WuKongIM/pkg/channel/worker"
)

const (
	observedCloudAppendBurst       = 512
	observedCloudAppendServiceTime = 38 * time.Millisecond
	reviewedHotAppendWaitBudget    = 200 * time.Millisecond
)

// BenchmarkCloudMediumChannelAppendPressure replays the bounded store-append
// service envelope observed by the chat-lifecycle rehearsal. Run it with
// -benchtime=1x so one fixed burst remains directly comparable across runtime
// contracts.
func BenchmarkCloudMediumChannelAppendPressure(b *testing.B) {
	contract, err := effectiveNodeRuntimeContractForScale("medium")
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < b.N; i++ {
		p99 := benchmarkCloudAppendBurst(b, contract.ChannelStoreAppendWorkers)
		b.ReportMetric(float64(p99.Microseconds())/1000, "wait-p99-ms")
		if p99 > reviewedHotAppendWaitBudget {
			b.Fatalf("Cloud Medium store-append wait P99 = %s with %d workers; want <= %s", p99, contract.ChannelStoreAppendWorkers, reviewedHotAppendWaitBudget)
		}
	}
}

func benchmarkCloudAppendBurst(b *testing.B, workers int) time.Duration {
	b.Helper()
	observer := &cloudAppendWaitObserver{}
	sink := &cloudAppendBenchmarkSink{}
	sink.wait.Add(observedCloudAppendBurst)
	pool, err := channelworker.NewPool(channelworker.PoolConfig{
		Name:      "cloud-medium-store-append",
		Workers:   workers,
		QueueSize: observedCloudAppendBurst,
	}, channelworker.Deps{}, sink)
	if err != nil {
		b.Fatal(err)
	}
	pool.SetQueueObserver(observer)
	defer pool.Close()

	for i := 0; i < observedCloudAppendBurst; i++ {
		fence := channel.Fence{
			ChannelKey: channel.ChannelKey("cloud-append:" + strconv.Itoa(i)),
			OpID:       channel.OpID(i + 1),
		}
		task := channelworker.Task{
			Kind:  channelworker.TaskFunc,
			Fence: fence,
			RunFunc: func(context.Context) channelworker.Result {
				time.Sleep(observedCloudAppendServiceTime)
				return channelworker.Result{Kind: channelworker.TaskFunc, Fence: fence}
			},
		}
		if err := pool.Submit(context.Background(), task); err != nil {
			b.Fatalf("Submit(%d) error = %v", i, err)
		}
	}
	sink.wait.Wait()
	return observer.p99()
}

type cloudAppendBenchmarkSink struct {
	wait sync.WaitGroup
}

func (s *cloudAppendBenchmarkSink) Complete(channelworker.Result) {
	s.wait.Done()
}

type cloudAppendWaitObserver struct {
	mu    sync.Mutex
	waits []time.Duration
}

func (o *cloudAppendWaitObserver) SetWorkerQueueDepth(string, int)       {}
func (o *cloudAppendWaitObserver) SetWorkerQueueCapacity(string, int)    {}
func (o *cloudAppendWaitObserver) SetWorkerWorkers(string, int)          {}
func (o *cloudAppendWaitObserver) ObserveWorkerAdmission(string, string) {}
func (o *cloudAppendWaitObserver) ObserveWorkerTask(string, channelworker.TaskKind, error, time.Duration) {
}
func (o *cloudAppendWaitObserver) ObserveWorkerBatch(string, channelworker.TaskKind, int, error) {}
func (o *cloudAppendWaitObserver) SetWorkerInflight(string, int)                                 {}
func (o *cloudAppendWaitObserver) SetWorkerInflightPeak(string, int)                             {}
func (o *cloudAppendWaitObserver) SetWorkerAntsPoolUsage(string, int, int, int)                  {}

func (o *cloudAppendWaitObserver) ObserveWorkerWait(_ string, _ channelworker.TaskKind, wait time.Duration) {
	o.mu.Lock()
	o.waits = append(o.waits, wait)
	o.mu.Unlock()
}

func (o *cloudAppendWaitObserver) p99() time.Duration {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.waits) == 0 {
		return 0
	}
	sort.Slice(o.waits, func(i, j int) bool { return o.waits[i] < o.waits[j] })
	index := (99*len(o.waits) + 99) / 100
	return o.waits[index-1]
}
