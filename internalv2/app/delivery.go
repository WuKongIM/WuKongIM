package app

import (
	"context"
	"errors"
	"sync"
	"time"

	runtimechannelid "github.com/WuKongIM/WuKongIM/internal/runtime/channelid"
	"github.com/WuKongIM/WuKongIM/internalv2/contracts/messageevents"
	runtimedelivery "github.com/WuKongIM/WuKongIM/internalv2/runtime/delivery"
	"github.com/WuKongIM/WuKongIM/internalv2/runtime/online"
	deliveryusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/delivery"
	"github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
	"github.com/WuKongIM/WuKongIM/internalv2/usecase/presence"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

var errRecvMessageIDOverflow = errors.New("internalv2/app: delivery message id overflows recv packet")
var errDeliveryEventQueueFull = errors.New("internalv2/app: delivery event queue full")

const defaultDeliveryEventQueueSize = 1024
const defaultDeliveryRetryMaxAttempts = 3
const defaultDeliveryRetryBackoff = 10 * time.Millisecond

type deliveryRuntimeAdapter struct {
	// manager handles committed-message fanout and ack mutations.
	manager *runtimedelivery.Manager
}

type deliveryWorkerGroup []WorkerRuntime

type deliveryCommittedSink struct {
	// delivery receives committed message events through the delivery usecase or async adapter.
	delivery interface {
		Submit(context.Context, messageevents.MessageCommitted) error
	}
}

func (s deliveryCommittedSink) Submit(ctx context.Context, event messageevents.MessageCommitted) error {
	if s.delivery == nil {
		return nil
	}
	return s.delivery.Submit(ctx, event)
}

// deliveryAsyncSink decouples SEND result latency from online delivery fanout.
type deliveryAsyncSink struct {
	// delivery receives dequeued committed message events.
	delivery *deliveryusecase.App
	// queue stores cloned committed events waiting for delivery fanout.
	queue chan messageevents.MessageCommitted
	// observeError records non-fatal delivery fanout failures.
	observeError func(error)
	// observeQueue records committed-event queue submit results.
	observeQueue func(string)

	mu      sync.Mutex
	started bool
	stop    chan struct{}
	done    chan struct{}
}

func newDeliveryAsyncSink(delivery *deliveryusecase.App, queueSize int, observeError func(error), observeQueue func(string)) *deliveryAsyncSink {
	if queueSize <= 0 {
		queueSize = defaultDeliveryEventQueueSize
	}
	return &deliveryAsyncSink{
		delivery:     delivery,
		queue:        make(chan messageevents.MessageCommitted, queueSize),
		observeError: observeError,
		observeQueue: observeQueue,
	}
}

func (g deliveryWorkerGroup) Start(ctx context.Context) error {
	for idx, worker := range g {
		if worker == nil {
			continue
		}
		if err := worker.Start(ctx); err != nil {
			for i := idx - 1; i >= 0; i-- {
				if g[i] != nil {
					_ = g[i].Stop(ctx)
				}
			}
			return err
		}
	}
	return nil
}

func (g deliveryWorkerGroup) Stop(ctx context.Context) error {
	var err error
	for i := len(g) - 1; i >= 0; i-- {
		if g[i] == nil {
			continue
		}
		if stopErr := g[i].Stop(ctx); stopErr != nil {
			err = errors.Join(err, stopErr)
		}
	}
	return err
}

func (s *deliveryAsyncSink) Submit(ctx context.Context, event messageevents.MessageCommitted) error {
	if s == nil || s.delivery == nil {
		return nil
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	cloned := event.Clone()
	select {
	case s.queue <- cloned:
		s.recordQueue(runtimedelivery.DeliveryResultOK)
		return nil
	default:
		s.recordQueue("overflow")
		return errDeliveryEventQueueFull
	}
}

func (s *deliveryAsyncSink) Start(context.Context) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return nil
	}
	s.stop = make(chan struct{})
	s.done = make(chan struct{})
	s.started = true
	go s.run(s.stop, s.done)
	return nil
}

func (s *deliveryAsyncSink) Stop(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return nil
	}
	stop := s.stop
	done := s.done
	s.started = false
	s.stop = nil
	s.done = nil
	s.mu.Unlock()

	close(stop)
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *deliveryAsyncSink) run(stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	for {
		select {
		case event := <-s.queue:
			s.deliver(event)
		case <-stop:
			s.drain()
			return
		}
	}
}

func (s *deliveryAsyncSink) drain() {
	for {
		select {
		case event := <-s.queue:
			s.deliver(event)
		default:
			return
		}
	}
}

func (s *deliveryAsyncSink) deliver(event messageevents.MessageCommitted) {
	if s == nil || s.delivery == nil {
		return
	}
	if err := s.delivery.SubmitCommitted(context.Background(), event); err != nil && s.observeError != nil {
		s.observeError(err)
	}
}

func (s *deliveryAsyncSink) recordQueue(result string) {
	if s != nil && s.observeQueue != nil {
		s.observeQueue(result)
	}
}

type deliveryMessageObserver struct {
	// app records non-fatal delivery sink failures for tests and diagnostics.
	app *App
}

func (o deliveryMessageObserver) CommittedSinkError(_ message.SendCommand, err error) {
	if o.app != nil {
		o.app.recordDeliveryError(err)
	}
}

func (a *App) recordDeliveryError(err error) {
	if a == nil {
		return
	}
	a.deliveryErrors.Add(1)
	if a.metrics != nil {
		if class := runtimedelivery.DeliveryErrorClass(err); class != runtimedelivery.DeliveryErrorClassNone {
			a.metrics.Delivery.ObserveError(class)
		}
	}
}

func (a *App) recordDeliveryQueue(result string) {
	if a == nil || a.metrics == nil {
		return
	}
	a.metrics.Delivery.ObserveEventQueue(result)
}

func (a deliveryRuntimeAdapter) SubmitCommitted(ctx context.Context, event messageevents.MessageCommitted) error {
	if a.manager == nil {
		return nil
	}
	event = scopePersonDeliveryEvent(event)
	return a.manager.SubmitCommitted(ctx, event)
}

// scopePersonDeliveryEvent narrows person-channel fanout to the two channel participants.
func scopePersonDeliveryEvent(event messageevents.MessageCommitted) messageevents.MessageCommitted {
	if event.ChannelType != frame.ChannelTypePerson || len(event.MessageScopedUIDs) > 0 {
		return event
	}
	left, right, err := runtimechannelid.DecodePersonChannel(event.ChannelID)
	if err != nil {
		return event
	}
	if left != "" {
		event.MessageScopedUIDs = append(event.MessageScopedUIDs, left)
	}
	if right != "" && right != left {
		event.MessageScopedUIDs = append(event.MessageScopedUIDs, right)
	}
	return event
}

func (a deliveryRuntimeAdapter) Recvack(ctx context.Context, cmd deliveryusecase.RecvackCommand) error {
	if a.manager == nil {
		return nil
	}
	return a.manager.Recvack(ctx, runtimedelivery.Recvack{
		UID:        cmd.UID,
		SessionID:  cmd.SessionID,
		MessageID:  cmd.MessageID,
		MessageSeq: cmd.MessageSeq,
	})
}

func (a deliveryRuntimeAdapter) SessionClosed(ctx context.Context, cmd deliveryusecase.SessionClosedCommand) error {
	if a.manager == nil {
		return nil
	}
	return a.manager.SessionClosed(ctx, runtimedelivery.SessionClosed{UID: cmd.UID, SessionID: cmd.SessionID})
}

type localOwnerPusher struct {
	// online resolves owner-local concrete sessions.
	online *online.Registry
	// delivery tracks pending recvacks after successful local writes.
	delivery *runtimedelivery.Manager
	// pendingAckTTL bounds stale pending recvack cleanup during delivery activity.
	pendingAckTTL time.Duration
}

func (p localOwnerPusher) Push(_ context.Context, cmd runtimedelivery.PushCommand) (runtimedelivery.PushResult, error) {
	if p.pendingAckTTL > 0 && p.delivery != nil {
		p.delivery.ExpirePendingAcks(p.pendingAckTTL)
	}
	var result runtimedelivery.PushResult
	for _, route := range cmd.Routes {
		session, ok := p.localSession(route)
		if !ok {
			result.Dropped = append(result.Dropped, route)
			continue
		}
		packet, err := buildRecvPacket(cmd.Envelope, route.UID)
		if err != nil {
			result.Dropped = append(result.Dropped, route)
			continue
		}
		pending := runtimedelivery.PendingRecvAck{
			UID:         route.UID,
			SessionID:   route.SessionID,
			MessageID:   cmd.Envelope.MessageID,
			MessageSeq:  cmd.Envelope.MessageSeq,
			ChannelID:   cmd.Envelope.ChannelID,
			ChannelType: cmd.Envelope.ChannelType,
		}
		if p.delivery != nil && !p.delivery.BindPendingAck(pending) {
			result.Dropped = append(result.Dropped, route)
			continue
		}
		if err := session.Session.WriteDelivery(packet); err != nil {
			if p.delivery != nil {
				_ = p.delivery.Recvack(context.Background(), runtimedelivery.Recvack{
					UID:       route.UID,
					SessionID: route.SessionID,
					MessageID: cmd.Envelope.MessageID,
				})
			}
			result.Retryable = append(result.Retryable, route)
			continue
		}
		result.Accepted = append(result.Accepted, route)
	}
	return result, nil
}

func (p localOwnerPusher) localSession(route runtimedelivery.Route) (online.LocalSession, bool) {
	if p.online == nil || route.UID == "" || route.SessionID == 0 || route.OwnerNodeID == 0 || route.OwnerBootID == 0 || route.OwnerSeq == 0 {
		return online.LocalSession{}, false
	}
	session, ok := p.online.LocalSession(route.SessionID)
	if !ok || session.State != online.RouteStateActive || session.Session == nil {
		return online.LocalSession{}, false
	}
	local := session.Route
	if local.UID != route.UID || local.SessionID != route.SessionID {
		return online.LocalSession{}, false
	}
	if local.OwnerNodeID != route.OwnerNodeID || local.OwnerBootID != route.OwnerBootID || local.OwnerSeq != route.OwnerSeq {
		return online.LocalSession{}, false
	}
	return session, true
}

func buildRecvPacket(env runtimedelivery.Envelope, uid string) (*frame.RecvPacket, error) {
	if env.MessageID > uint64(1<<63-1) {
		return nil, errRecvMessageIDOverflow
	}
	channelID := env.ChannelID
	if env.ChannelType == frame.ChannelTypePerson {
		channelID = recipientPersonChannelView(env, uid)
	}
	return &frame.RecvPacket{
		Framer: frame.Framer{
			RedDot: env.RedDot,
		},
		MessageID:   int64(env.MessageID),
		MessageSeq:  env.MessageSeq,
		ClientMsgNo: env.ClientMsgNo,
		Timestamp:   int32(time.Now().Unix()),
		ChannelID:   channelID,
		ChannelType: env.ChannelType,
		FromUID:     env.FromUID,
		Payload:     append([]byte(nil), env.Payload...),
	}, nil
}

func recipientPersonChannelView(env runtimedelivery.Envelope, recipientUID string) string {
	if recipientUID == "" {
		return env.ChannelID
	}
	left, right, err := runtimechannelid.DecodePersonChannel(env.ChannelID)
	if err != nil {
		return env.FromUID
	}
	switch recipientUID {
	case left:
		return right
	case right:
		return left
	default:
		return env.FromUID
	}
}

type appSubscriberPlanner struct {
	// channel scans durable subscribers for non-person channel fanout.
	channel runtimedelivery.SubscriberPlanner
}

func (p appSubscriberPlanner) NextPartitionPage(ctx context.Context, task runtimedelivery.FanoutTask, cursor string, limit int) (runtimedelivery.UIDPage, error) {
	if task.Envelope.ChannelType != frame.ChannelTypePerson {
		if p.channel == nil {
			return runtimedelivery.UIDPage{Done: true}, nil
		}
		return p.channel.NextPartitionPage(ctx, task, cursor, limit)
	}
	if cursor != "" {
		return runtimedelivery.UIDPage{Done: true}, nil
	}
	left, right, err := runtimechannelid.DecodePersonChannel(task.Envelope.ChannelID)
	if err != nil {
		return runtimedelivery.UIDPage{Done: true}, nil
	}
	uids := make([]string, 0, 2)
	if left != "" {
		uids = append(uids, left)
	}
	if right != "" && right != left {
		uids = append(uids, right)
	}
	return runtimedelivery.UIDPage{UIDs: uids, Done: true}, nil
}

type noopSubscriberPlanner struct{}

func (noopSubscriberPlanner) NextPartitionPage(context.Context, runtimedelivery.FanoutTask, string, int) (runtimedelivery.UIDPage, error) {
	return runtimedelivery.UIDPage{Done: true}, nil
}

type presenceResolverAdapter struct {
	// presence resolves authoritative routes for selected UIDs.
	presence *presence.App
}

func (r presenceResolverAdapter) EndpointsByUIDs(ctx context.Context, uids []string) (map[string][]runtimedelivery.Route, error) {
	out := make(map[string][]runtimedelivery.Route, len(uids))
	if r.presence == nil {
		return out, nil
	}
	routesByUID, err := r.presence.EndpointsByUIDs(ctx, uids)
	if err != nil {
		return nil, err
	}
	for uid, routes := range routesByUID {
		for _, route := range routes {
			out[uid] = append(out[uid], runtimedelivery.Route{
				UID:         route.UID,
				OwnerNodeID: route.OwnerNodeID,
				OwnerBootID: route.OwnerBootID,
				OwnerSeq:    route.OwnerSeq,
				SessionID:   route.SessionID,
				DeviceID:    route.DeviceID,
				DeviceFlag:  route.DeviceFlag,
				DeviceLevel: route.DeviceLevel,
			})
		}
	}
	return out, nil
}
