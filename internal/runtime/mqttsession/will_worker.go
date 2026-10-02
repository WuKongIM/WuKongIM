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
	ErrWillWorkerInvalid  = errors.New("mqttsession: invalid Will worker options")
	ErrWillWorkerStopping = errors.New("mqttsession: Will worker still draining")
)

// WillExecution rereads an exact obligation and owns all claims, permission,
// publication and receipt decisions. Discovery and elapsed leases grant none of
// that authority. Calls must honor cancellation and join their effects.
type WillExecution interface {
	ExecuteWill(context.Context, meta.MQTTWillKey) error
}

// WillWorkerOptions separates bounded discovery from slower publication work.
type WillWorkerOptions struct {
	// Source discovers current authority-owned recovery pages.
	Source DeadlineSource
	// Executor rereads and claims each key before any publication effect.
	Executor WillExecution
	// Registry owns the scanner and bounded execution pool.
	Registry *gr.Registry
	// HashSlotCount matches the deployment; zero selects 256.
	HashSlotCount uint16
	// Workers defaults to four, capped at the executor's four-turn admission.
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
	// Now filters due candidates only; it supplies no execution/isolation proof.
	Now func() time.Time
	// Observe receives aggregate counters only and must not block or invoke Stop.
	Observe func(WillObservation)
}

// WillObservation excludes keys, bodies, errors and other unbounded labels.
type WillObservation struct {
	Pages, Visited, Scheduled, Completed, Failures int
	Duration                                       time.Duration
}

// WillWorker owns one scanner and one bounded cohort. Every run keeps only
// body-free identity hints; App must join it before closing its dependencies.
type WillWorker struct {
	opts WillWorkerOptions
	mu   sync.Mutex
	run  *willWorkerRun
}
type willWorkerRun struct {
	ctx     context.Context
	cancel  context.CancelFunc
	done    chan struct{}
	results chan willWorkResult
	queue   *workqueue.BoundedWorkerQueue[meta.MQTTWillKey]
}
type willWorkResult struct {
	key    meta.MQTTWillKey
	failed bool
}

func NewWillWorker(o WillWorkerOptions) (*WillWorker, error) {
	if o.HashSlotCount == 0 {
		o.HashSlotCount = 256
	}
	if o.Workers == 0 {
		o.Workers = 4
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
	if o.Source == nil || o.Executor == nil || o.Workers < 1 || o.Workers > 4 || o.Interval < 10*time.Millisecond || o.Interval > time.Minute || o.ScanTimeout <= 0 || o.ScanTimeout > time.Minute || o.CallTimeout <= 0 || o.CallTimeout > o.ScanTimeout || o.ExecutionTimeout <= 0 || o.ExecutionTimeout > 5*time.Second || o.PagesPerTurn < 1 || o.PagesPerTurn > 32 || o.PageSize < 1 || o.PageSize > 16 {
		return nil, ErrWillWorkerInvalid
	}
	return &WillWorker{opts: o}, nil
}

// Start checks its caller only for admission; the run owns an independent lifetime.
// A cancelled but unjoined run must never overlap a replacement cohort.
func (w *WillWorker) Start(ctx context.Context) error {
	if w == nil || ctx == nil {
		return ErrWillWorkerInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.run != nil {
		if w.run.ctx.Err() != nil {
			return ErrWillWorkerStopping
		}
		return nil
	}
	runCtx, cancel := context.WithCancel(context.Background())
	r := &willWorkerRun{ctx: runCtx, cancel: cancel, done: make(chan struct{}), results: make(chan willWorkResult, w.opts.Workers)}
	q, err := workqueue.NewBoundedWorkerQueue(workqueue.BoundedWorkerQueueConfig{Name: "mqtt_wills", Goroutines: w.opts.Registry, Task: gr.TaskMQTTWillWorker, Workers: w.opts.Workers, QueueSize: w.opts.Workers}, func(_ context.Context, k meta.MQTTWillKey) error {
		// The run context fences queued work even though the generic queue drains it.
		if r.ctx.Err() != nil {
			return nil
		}
		call, done := context.WithTimeout(r.ctx, w.opts.ExecutionTimeout)
		err := w.opts.Executor.ExecuteWill(call, k)
		if err == nil {
			err = call.Err()
		}
		done()
		select {
		case r.results <- willWorkResult{key: k, failed: err != nil}:
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
	gr.SafeGo(w.opts.Registry, gr.TaskMQTTWillScheduler, func() {
		defer close(r.done)
		defer func() { r.cancel(); _ = r.queue.Close(context.Background()) }()
		w.loop(r)
	})
	return nil
}

// Stop cancels and joins scanner plus cohort. A timeout retains this exact run;
// cancellation alone is neither completion nor distributed isolation evidence.
func (w *WillWorker) Stop(ctx context.Context) error {
	if w == nil || ctx == nil {
		return ErrWillWorkerInvalid
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

func (w *WillWorker) loop(r *willWorkerRun) {
	ticker := time.NewTicker(w.opts.Interval)
	defer ticker.Stop()
	admitted := make(map[meta.MQTTWillKey]struct{}, w.opts.Workers)
	var state willScanState
	for r.ctx.Err() == nil {
		var out WillObservation
		draining := true
		for draining {
			select {
			case result := <-r.results:
				delete(admitted, result.key)
				out.Completed++
				if result.failed {
					out.Failures++
				}
			default:
				draining = false
			}
		}
		if len(admitted) < w.opts.Workers {
			scan := w.sweep(r.ctx, &state, func(key meta.MQTTWillKey) bool {
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
			w.opts.Observe(out)
		}
		select {
		case <-r.ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
