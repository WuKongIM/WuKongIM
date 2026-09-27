package mqttsession

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	gr "github.com/WuKongIM/WuKongIM/pkg/goroutine"
	"github.com/WuKongIM/WuKongIM/pkg/workqueue"
)

var (
	ErrConsumerWorkerInvalid  = errors.New("mqttsession: invalid consumer worker options")
	ErrConsumerWorkerStopping = errors.New("mqttsession: consumer worker still draining")
)

// ConsumerMaintenance rereads binding and Session authority before bounded
// accounting/progress/removal. Hints grant no authority. Calls must honor
// cancellation and join their effects before returning.
type ConsumerMaintenance interface {
	MaintainConsumer(context.Context, meta.MQTTSourceBindingKey) (ConsumerWork, error)
}

// ConsumerSubscriptionMaintenance rereads pending intent and parent authority;
// it never receives message bodies or derives policy from a recovery hint.
// True confirms observed removal, including retries; it is not a unique count.
type ConsumerSubscriptionMaintenance interface {
	MaintainSubscription(context.Context, meta.MQTTSubscriptionRecoveryCursor) (bool, error)
}

// consumerWorkKey is a tagged primary identity. Exactly one field is set;
// subscription timestamps are cleared so scan updates cannot defeat deduplication.
type consumerWorkKey struct {
	binding      meta.MQTTSourceBindingKey
	subscription meta.MQTTSubscriptionRecoveryCursor
}

// ConsumerWorkerOptions separates bounded discovery from slower consumer work.
type ConsumerWorkerOptions struct {
	// Source discovers current authority-owned recovery pages.
	Source DeadlineSource
	// Maintainer rereads authority before accounting or releasing a source obligation.
	Maintainer ConsumerMaintenance
	// Subscriptions optionally shares this cohort with closed-intent completion.
	// Product MQTT composition supplies it for both target kinds.
	Subscriptions ConsumerSubscriptionMaintenance
	// Registry owns the scanner and bounded execution pool.
	Registry *gr.Registry
	// HashSlotCount matches the deployment; zero selects 256.
	HashSlotCount uint16
	// Workers defaults to 16 and is capped at 128.
	// At most this many keys are retained across queued and executing work.
	Workers int
	// Interval is the scan cadence, default 200ms, between 10ms and one minute.
	Interval time.Duration
	// ScanTimeout bounds a complete discovery turn, default two seconds, max one minute.
	ScanTimeout time.Duration
	// CallTimeout bounds each discovery call, default 250ms, no greater than ScanTimeout.
	CallTimeout time.Duration
	// ExecutionTimeout independently bounds one executor turn, default/maximum five seconds.
	ExecutionTimeout time.Duration
	// PagesPerTurn defaults to 32 and PageSize to 16; these are also their maxima.
	PagesPerTurn, PageSize int
	// Now filters due index candidates; it supplies no execution/isolation proof.
	Now func() time.Time
	// Observe receives aggregate counters only and must not block or invoke Stop.
	Observe func(ConsumerObservation)
}

// ConsumerWork carries only proved per-turn outcomes; it contains no identity or body.
type ConsumerWork struct {
	// SubscriptionRemovalConfirmed counts observed completion, including retries.
	SubscriptionRemovalConfirmed                                                  bool
	Accounted, Projected, Removed, QuotaEnded, RevokedEnded, QualificationRemoved bool
}

// ConsumerObservation excludes keys, bodies, errors and other unbounded labels.
type ConsumerObservation struct {
	SubscriptionRemovalConfirmed                            int
	QualificationRemoved                                    int
	Pages, Visited, Scheduled, Completed, Failures          int
	Duration                                                time.Duration
	Accounted, Projected, Removed, QuotaEnded, RevokedEnded int
	Admitted, Capacity                                      int
}

// ConsumerWorker owns one scanner and one bounded cohort. Every run keeps only
// body-free identity hints; App must join it before closing its dependencies.
type ConsumerWorker struct {
	opts ConsumerWorkerOptions
	mu   sync.Mutex
	run  *consumerWorkerRun
}
type consumerWorkerRun struct {
	ctx     context.Context
	cancel  context.CancelFunc
	done    chan struct{}
	results chan consumerWorkResult
	queue   *workqueue.BoundedWorkerQueue[consumerWorkKey]
}
type consumerWorkResult struct {
	key    consumerWorkKey
	failed bool
	work   ConsumerWork
}

func NewConsumerWorker(o ConsumerWorkerOptions) (*ConsumerWorker, error) {
	if o.HashSlotCount == 0 {
		o.HashSlotCount = 256
	}
	if o.Workers == 0 {
		o.Workers = 16
	}
	if o.Interval == 0 {
		o.Interval = 200 * time.Millisecond
	}
	if o.ScanTimeout == 0 {
		o.ScanTimeout = 2 * time.Second
	}
	if o.CallTimeout == 0 {
		o.CallTimeout = 250 * time.Millisecond
	}
	if o.ExecutionTimeout == 0 {
		o.ExecutionTimeout = 5 * time.Second
	}
	if o.PagesPerTurn == 0 {
		o.PagesPerTurn = 32
	}
	if o.PageSize == 0 {
		o.PageSize = 16
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Source == nil || o.Maintainer == nil || o.Workers < 1 || o.Workers > 128 || o.Interval < 10*time.Millisecond || o.Interval > time.Minute || o.ScanTimeout <= 0 || o.ScanTimeout > time.Minute || o.CallTimeout <= 0 || o.CallTimeout > o.ScanTimeout || o.ExecutionTimeout <= 0 || o.ExecutionTimeout > 5*time.Second || o.PagesPerTurn < 1 || o.PagesPerTurn > 32 || o.PageSize < 1 || o.PageSize > 16 {
		return nil, ErrConsumerWorkerInvalid
	}
	return &ConsumerWorker{opts: o}, nil
}

// Start checks its caller only for admission; the run owns an independent lifetime.
// A cancelled but unjoined run must never overlap a replacement cohort.
func (w *ConsumerWorker) Start(ctx context.Context) error {
	if w == nil || ctx == nil {
		return ErrConsumerWorkerInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.run != nil {
		if w.run.ctx.Err() != nil {
			return ErrConsumerWorkerStopping
		}
		return nil
	}
	runCtx, cancel := context.WithCancel(context.Background())
	r := &consumerWorkerRun{ctx: runCtx, cancel: cancel, done: make(chan struct{}), results: make(chan consumerWorkResult, w.opts.Workers)}
	q, err := workqueue.NewBoundedWorkerQueue(workqueue.BoundedWorkerQueueConfig{Name: "mqtt_consumers", Goroutines: w.opts.Registry, Task: gr.TaskMQTTConsumerWorker, Workers: w.opts.Workers, QueueSize: w.opts.Workers}, func(_ context.Context, k consumerWorkKey) error {
		// The run context fences queued work even though the generic queue drains it.
		if r.ctx.Err() != nil {
			return nil
		}
		call, done := context.WithTimeout(r.ctx, w.opts.ExecutionTimeout)
		var work ConsumerWork
		var err error
		if k.subscription != (meta.MQTTSubscriptionRecoveryCursor{}) {
			work.SubscriptionRemovalConfirmed, err = w.opts.Subscriptions.MaintainSubscription(call, k.subscription)
		} else {
			work, err = w.opts.Maintainer.MaintainConsumer(call, k.binding)
		}
		if err == nil {
			err = call.Err()
		}
		if err != nil {
			work.SubscriptionRemovalConfirmed = false
		}
		done()
		select {
		case r.results <- consumerWorkResult{key: k, work: work, failed: err != nil}:
		case <-r.ctx.Done():
		}
		return nil // Durable state, not queue retry, retains failed obligations.
	})
	if err != nil {
		cancel()
		return err
	}
	r.queue = q
	w.run = r
	gr.SafeGo(w.opts.Registry, gr.TaskMQTTConsumerScheduler, func() {
		defer close(r.done)
		defer func() {
			r.cancel()
			_ = r.queue.Close(context.Background())
			if w.opts.Observe != nil {
				w.opts.Observe(ConsumerObservation{Capacity: w.opts.Workers})
			}
		}()
		w.loop(r)
	})
	return nil
}

// Stop cancels and joins scanner plus cohort. A timeout retains this exact run;
// cancellation alone is neither completion nor distributed isolation evidence.
func (w *ConsumerWorker) Stop(ctx context.Context) error {
	if w == nil || ctx == nil {
		return ErrConsumerWorkerInvalid
	}
	w.mu.Lock()
	r := w.run
	w.mu.Unlock()
	if r == nil {
		return nil
	}
	r.cancel()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-r.done:
	}
	w.mu.Lock()
	if w.run == r {
		w.run = nil
	}
	w.mu.Unlock()
	return nil
}

func (w *ConsumerWorker) loop(r *consumerWorkerRun) {
	ticker := time.NewTicker(w.opts.Interval)
	defer ticker.Stop()
	admitted := make(map[consumerWorkKey]struct{}, w.opts.Workers)
	var state consumerScanState
	for r.ctx.Err() == nil {
		var out ConsumerObservation
		draining := true
		for draining {
			select {
			case result := <-r.results:
				delete(admitted, result.key)
				out.Completed++
				if result.work.SubscriptionRemovalConfirmed {
					out.SubscriptionRemovalConfirmed++
				}
				if result.work.QualificationRemoved {
					out.QualificationRemoved++
				}
				if result.work.Accounted {
					out.Accounted++
				}
				if result.work.Projected {
					out.Projected++
				}
				if result.work.Removed {
					out.Removed++
				}
				if result.work.QuotaEnded {
					out.QuotaEnded++
				}
				if result.work.RevokedEnded {
					out.RevokedEnded++
				}
				if result.failed {
					out.Failures++
				}
			default:
				draining = false
			}
		}
		if len(admitted) < w.opts.Workers {
			scan := w.sweep(r.ctx, &state, func(key consumerWorkKey) bool {
				if r.ctx.Err() != nil {
					return false
				}
				if _, ok := admitted[key]; ok {
					return true
				}
				if len(admitted) >= w.opts.Workers {
					return false
				}
				admitted[key] = struct{}{}
				if err := r.queue.Submit(r.ctx, key); err != nil {
					delete(admitted, key)
					return false
				}
				return true
			})
			out.Pages, out.Visited, out.Scheduled, out.Duration = scan.Pages, scan.Visited, scan.Scheduled, scan.Duration
			out.Failures += scan.Failures
		}
		if w.opts.Observe != nil {
			out.Admitted, out.Capacity = len(admitted), w.opts.Workers
			w.opts.Observe(out)
		}
		select {
		case <-r.ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
