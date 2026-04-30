package app

import (
	"context"
	"strconv"
	"sync"
	"time"

	deliveryruntime "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

const (
	deliveryRuntimeRetryTickInterval = 200 * time.Millisecond
	deliveryRuntimeSweepInterval     = 30 * time.Second
)

type deliveryRuntimeMaintenance interface {
	ProcessRetryTicks(context.Context) error
	SweepIdle()
	InflightRouteCount() int
	AckBindingCount() int
}

type deliveryRuntimeMetrics interface {
	ObserveRouteExpired(channelType string)
	SetActorInflightRoutes(v int)
	SetAckBindings(v int)
}

type deliveryRuntimeLifecycleConfig struct {
	Runtime       deliveryRuntimeMaintenance
	Observer      deliveryruntime.Observer
	TickInterval  time.Duration
	SweepInterval time.Duration
	After         func(time.Duration) <-chan time.Time
}

type deliveryRuntimeLifecycle struct {
	cfg    deliveryRuntimeLifecycleConfig
	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

func newDeliveryRuntimeLifecycle(cfg deliveryRuntimeLifecycleConfig) *deliveryRuntimeLifecycle {
	if cfg.TickInterval <= 0 {
		cfg.TickInterval = deliveryRuntimeRetryTickInterval
	}
	if cfg.SweepInterval <= 0 {
		cfg.SweepInterval = deliveryRuntimeSweepInterval
	}
	if cfg.After == nil {
		cfg.After = time.After
	}
	return &deliveryRuntimeLifecycle{cfg: cfg}
}

func (l *deliveryRuntimeLifecycle) Start(ctx context.Context) error {
	if l == nil || l.cfg.Runtime == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.cancel != nil {
		return nil
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	l.cancel = cancel
	l.done = done
	go l.run(runCtx, done)
	return nil
}

func (l *deliveryRuntimeLifecycle) Stop() error {
	return l.StopContext(context.Background())
}

func (l *deliveryRuntimeLifecycle) StopContext(ctx context.Context) error {
	if l == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	l.mu.Lock()
	cancel := l.cancel
	done := l.done
	l.mu.Unlock()
	if cancel == nil {
		return nil
	}
	cancel()
	select {
	case <-done:
		l.mu.Lock()
		if l.done == done {
			l.cancel = nil
			l.done = nil
		}
		l.mu.Unlock()
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (l *deliveryRuntimeLifecycle) run(ctx context.Context, done chan struct{}) {
	defer close(done)
	retryTick := l.cfg.After(l.cfg.TickInterval)
	sweepTick := l.cfg.After(l.cfg.SweepInterval)
	for {
		select {
		case <-ctx.Done():
			return
		case <-retryTick:
			_ = l.cfg.Runtime.ProcessRetryTicks(ctx)
			l.observeMaintenanceSnapshot()
			retryTick = l.cfg.After(l.cfg.TickInterval)
		case <-sweepTick:
			l.cfg.Runtime.SweepIdle()
			l.observeMaintenanceSnapshot()
			sweepTick = l.cfg.After(l.cfg.SweepInterval)
		}
	}
}

func (l *deliveryRuntimeLifecycle) observeMaintenanceSnapshot() {
	if l == nil || l.cfg.Runtime == nil {
		return
	}
	snapshot := deliveryruntime.MaintenanceSnapshot{
		InflightRoutes: l.cfg.Runtime.InflightRouteCount(),
		AckBindings:    l.cfg.Runtime.AckBindingCount(),
	}
	if l.cfg.Observer != nil {
		l.cfg.Observer.OnMaintenanceSnapshot(snapshot)
	}
}

type deliveryRuntimeMetricsObserver struct {
	metrics deliveryRuntimeMetrics
}

func (o deliveryRuntimeMetricsObserver) OnRouteExpired(event deliveryruntime.RouteExpiredEvent) {
	if o.metrics == nil {
		return
	}
	o.metrics.ObserveRouteExpired(deliveryChannelTypeLabel(event.ChannelType))
}

func (o deliveryRuntimeMetricsObserver) OnMaintenanceSnapshot(snapshot deliveryruntime.MaintenanceSnapshot) {
	if o.metrics == nil {
		return
	}
	o.metrics.SetActorInflightRoutes(snapshot.InflightRoutes)
	o.metrics.SetAckBindings(snapshot.AckBindings)
}

func deliveryChannelTypeLabel(channelType uint8) string {
	switch channelType {
	case frame.ChannelTypePerson:
		return "person"
	case frame.ChannelTypeGroup:
		return "group"
	default:
		return strconv.FormatUint(uint64(channelType), 10)
	}
}
