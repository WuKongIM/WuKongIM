package sim

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	wkclient "github.com/WuKongIM/WuKongIM/pkg/client"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

const runtimeRetryBackoff = 2 * time.Second

// targetAPI is the HTTP bench setup surface required by the runtime.
type targetAPI interface {
	preflight(context.Context) (targetPreflight, error)
	setup(context.Context, Config, targetPreflight, Plan) error
}

// simPool is the WKProto client pool surface required by the runtime.
type simPool interface {
	Connect(context.Context, []wkclient.Identity) error
	SendBatch(context.Context, []wkclient.RoutedMessage) ([]wkclient.SendResult, error)
	Close() error
}

// poolFactory builds a connected-client pool for one runtime attempt.
type poolFactory func(Config, []string, wkclient.Observer) (simPool, error)

// sleepFunc sleeps until the duration elapses or the context is canceled.
type sleepFunc func(context.Context, time.Duration) error

// Runtime supervises target setup, WKProto clients, traffic, retries, and shutdown.
type Runtime struct {
	// Config is the normalized simulator configuration.
	Config Config
	// Status receives lifecycle and counter updates.
	Status *statusModel
	// Target prepares v2 bench metadata before traffic starts.
	Target targetAPI
	// NewPool creates the real or test WKProto client pool.
	NewPool poolFactory
	// Sleep controls retry backoff and is injectable for tests.
	Sleep sleepFunc
}

// Run starts the simulator and blocks until the context or max runtime stops it.
func (r *Runtime) Run(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	cfg := r.Config
	status := r.Status
	if status == nil {
		status = newStatus(cfg.RunID)
	}
	r.Status = status
	if r.Sleep == nil {
		r.Sleep = sleepContext
	}
	if r.NewPool == nil {
		r.NewPool = newClientPool
	}
	target := r.Target
	if target == nil {
		target = newTargetClient(cfg.Servers, cfg.BenchToken)
	}
	if cfg.MaxRuntime > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cfg.MaxRuntime)
		defer cancel()
	}

	status.setState(statePreflighting)
	resolved, err := target.preflight(ctx)
	if err != nil {
		status.addSendErrors(0, err.Error())
		status.setState(stateStopped)
		return err
	}
	if len(cfg.Gateways) > 0 {
		resolved.GatewayTCPAddrs = append([]string(nil), cfg.Gateways...)
	}
	status.setTarget(cfg.Servers, resolved.GatewayTCPAddrs)

	plan := buildPlan(cfg)
	status.setTopology(len(plan.Users), len(plan.Groups), cfg.GroupMembers)

	status.setState(stateSettingUp)
	if err := target.setup(ctx, cfg, resolved, plan); err != nil {
		status.addSendErrors(0, err.Error())
		status.setState(stateStopped)
		return err
	}

	var sequence atomic.Uint64
	for {
		status.setState(stateConnecting)
		observer := &clientObserver{status: status}
		pool, err := r.NewPool(cfg, resolved.GatewayTCPAddrs, observer)
		if err != nil {
			status.addSendErrors(0, err.Error())
			status.setState(stateStopped)
			return err
		}
		if err := pool.Connect(ctx, identitiesFromPlan(plan)); err != nil {
			_ = pool.Close()
			if ctx.Err() != nil {
				status.setState(stateStopped)
				return nil
			}
			status.addSendErrors(0, err.Error())
			status.setState(stateStopped)
			return err
		}

		status.setState(stateRunning)
		err = r.runTraffic(ctx, pool, plan, &sequence)
		_ = pool.Close()
		if ctx.Err() != nil {
			status.setState(stateStopped)
			return nil
		}
		if err == nil {
			status.setState(stateStopped)
			return nil
		}
		status.addReconnect(err.Error())
		status.setState(stateRetrying)
		if sleepErr := r.Sleep(ctx, runtimeRetryBackoff); sleepErr != nil {
			status.setState(stateStopped)
			return nil
		}
	}
}

func (r *Runtime) runTraffic(ctx context.Context, pool simPool, plan Plan, sequence *atomic.Uint64) error {
	interval := time.Duration(float64(time.Second) / r.Config.Rate.PerSecond)
	if interval <= 0 {
		interval = time.Nanosecond
	}

	workers := r.Config.Concurrency
	if workers <= 0 {
		workers = 1
	}
	work := make(chan *Group, workers)
	var wg sync.WaitGroup
	var firstErr error
	var firstErrMu sync.Mutex
	recordErr := func(err error) {
		if err == nil {
			return
		}
		firstErrMu.Lock()
		if firstErr == nil {
			firstErr = err
		}
		firstErrMu.Unlock()
	}
	loadErr := func() error {
		firstErrMu.Lock()
		defer firstErrMu.Unlock()
		return firstErr
	}

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for group := range work {
				msg := group.nextMessage(r.Config, sequence.Add(1))
				results, err := pool.SendBatch(ctx, []wkclient.RoutedMessage{msg})
				if err != nil {
					r.Status.addSendErrors(1, err.Error())
					recordErr(err)
					continue
				}
				if len(results) == 0 {
					r.Status.addSendErrors(1, "missing sendack")
					continue
				}
				if results[0].ReasonCode != frame.ReasonSuccess {
					r.Status.addSendErrors(1, results[0].ReasonCode.String())
					continue
				}
				r.Status.addMessagesSent(1)
			}
		}()
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	defer func() {
		close(work)
		wg.Wait()
	}()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			for i := range plan.Groups {
				select {
				case work <- &plan.Groups[i]:
				default:
					r.Status.addSendErrors(1, "sim send work queue full")
				}
			}
			if err := loadErr(); err != nil {
				return err
			}
		}
	}
}

func newClientPool(cfg Config, gateways []string, observer wkclient.Observer) (simPool, error) {
	return wkclient.NewPool(wkclient.PoolConfig{
		Addrs: gateways,
		Client: wkclient.Config{
			OperationTimeout:       cfg.OperationTimeout,
			AckTimeout:             cfg.AckTimeout,
			SendQueueCapacity:      cfg.Concurrency * 4,
			MaxInflight:            cfg.Concurrency,
			InboundFrameBufferSize: cfg.Concurrency * 4,
			AutoRecvAck:            true,
			Observer:               observer,
		},
		ConnectRatePerSecond: cfg.ConnectRate,
	})
}

type clientObserver struct {
	status *statusModel
}

func (o *clientObserver) OnConnect(event wkclient.ConnectEvent) {
	if event.Err != nil {
		o.status.addSendErrors(0, event.Err.Error())
	}
}

func (o *clientObserver) OnSendQueue(event wkclient.SendQueueEvent) {}

func (o *clientObserver) OnSendBatch(event wkclient.SendBatchEvent) {
	if event.Err != nil {
		o.status.addSendErrors(1, event.Err.Error())
	}
}

func (o *clientObserver) OnSendAck(event wkclient.SendAckEvent) {
	if event.Err != nil {
		o.status.addSendErrors(1, event.Err.Error())
	}
}

func (o *clientObserver) OnRecv(event wkclient.RecvEvent) {
	if event.Dropped {
		o.status.addRecv(0, 1)
		return
	}
	o.status.addRecv(1, 0)
}

func (o *clientObserver) OnError(event wkclient.ErrorEvent) {
	if event.Err != nil {
		o.status.addSendErrors(1, fmt.Sprintf("%s: %v", event.Op, event.Err))
	}
}

func sleepContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
