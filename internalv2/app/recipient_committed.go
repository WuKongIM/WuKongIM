package app

import (
	"context"
	"errors"
	"sync"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/messageevents"
	runtimedelivery "github.com/WuKongIM/WuKongIM/internalv2/runtime/delivery"
	recipientusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/recipient"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

var (
	errRecipientCommittedWorkerClosed = errors.New("internalv2/app: recipient committed worker closed")
	errRecipientCommittedWorkerFull   = errors.New("internalv2/app: recipient committed worker queue full")
)

type recipientCommittedDispatcher interface {
	SubmitCommitted(context.Context, messageevents.MessageCommitted) error
}

// recipientCommittedWorker admits committed messages quickly and dispatches recipient work in the background.
type recipientCommittedWorker struct {
	dispatcher      recipientCommittedDispatcher
	queueSize       int
	requiresPayload bool
	logger          wklog.Logger

	mu      sync.Mutex
	started bool
	closed  bool
	queue   chan messageevents.MessageCommitted
	done    chan struct{}
	cancel  context.CancelFunc
}

func newRecipientCommittedWorker(dispatcher recipientCommittedDispatcher, queueSize int, requiresPayload bool, logger wklog.Logger) *recipientCommittedWorker {
	if queueSize <= 0 {
		queueSize = 1
	}
	if logger == nil {
		logger = wklog.NewNop()
	}
	return &recipientCommittedWorker{
		dispatcher:      dispatcher,
		queueSize:       queueSize,
		requiresPayload: requiresPayload,
		logger:          logger,
	}
}

// RequiresCommittedPayload keeps payload and request-scoped recipients available for delivery.
func (w *recipientCommittedWorker) RequiresCommittedPayload() bool {
	return w != nil && w.requiresPayload
}

// Submit enqueues committed recipient work; before lifecycle Start it runs synchronously for tests.
func (w *recipientCommittedWorker) Submit(ctx context.Context, event messageevents.MessageCommitted) error {
	if w == nil || w.dispatcher == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	event = scopePersonDeliveryEvent(event.Clone())

	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return errRecipientCommittedWorkerClosed
	}
	if !w.started {
		w.mu.Unlock()
		return w.dispatcher.SubmitCommitted(ctx, event)
	}
	select {
	case w.queue <- event:
		w.mu.Unlock()
		return nil
	default:
		w.mu.Unlock()
		return errRecipientCommittedWorkerFull
	}
}

// Start opens the bounded recipient committed queue.
func (w *recipientCommittedWorker) Start(context.Context) error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return errRecipientCommittedWorkerClosed
	}
	if w.started {
		return nil
	}
	w.queue = make(chan messageevents.MessageCommitted, w.queueSize)
	w.done = make(chan struct{})
	runCtx, cancel := context.WithCancel(context.Background())
	w.cancel = cancel
	w.started = true
	go w.run(runCtx, w.queue, w.done)
	return nil
}

// Stop closes admission and drains accepted committed messages until ctx expires.
func (w *recipientCommittedWorker) Stop(ctx context.Context) error {
	if w == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	w.mu.Lock()
	if w.closed {
		done := w.done
		cancel := w.cancel
		w.mu.Unlock()
		return waitRecipientWorkerDone(ctx, done, cancel)
	}
	w.closed = true
	if !w.started {
		w.mu.Unlock()
		return nil
	}
	queue := w.queue
	done := w.done
	cancel := w.cancel
	close(queue)
	w.mu.Unlock()
	return waitRecipientWorkerDone(ctx, done, cancel)
}

func (w *recipientCommittedWorker) run(ctx context.Context, queue <-chan messageevents.MessageCommitted, done chan<- struct{}) {
	defer close(done)
	for event := range queue {
		if err := w.dispatcher.SubmitCommitted(ctx, event); err != nil {
			w.logger.Warn("recipient committed dispatch failed",
				wklog.Event("internalv2.app.recipient.committed_dispatch_failed"),
				wklog.Error(err),
			)
		}
	}
}

func waitRecipientWorkerDone(ctx context.Context, done <-chan struct{}, cancel context.CancelFunc) error {
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		if cancel != nil {
			cancel()
		}
		return ctx.Err()
	}
}

type recipientDeliverySubmitter struct {
	delivery interface {
		SubmitCommitted(context.Context, messageevents.MessageCommitted) error
	}
}

func (s recipientDeliverySubmitter) SubmitDelivery(ctx context.Context, event messageevents.MessageCommitted) error {
	if s.delivery == nil {
		return nil
	}
	return s.delivery.SubmitCommitted(ctx, event)
}

type deliverySubscriberRecipientSource struct {
	source runtimedelivery.ChannelSubscriberSource
}

func (s deliverySubscriberRecipientSource) NextPage(ctx context.Context, event messageevents.MessageCommitted, cursor string, limit int) (recipientusecase.RecipientPage, error) {
	if s.source == nil {
		return recipientusecase.RecipientPage{Done: true}, nil
	}
	page, err := s.source.ListSubscribers(ctx, runtimedelivery.SubscriberPageRequest{
		ChannelID:   event.ChannelID,
		ChannelType: event.ChannelType,
		Cursor:      cursor,
		Limit:       limit,
	})
	if err != nil {
		return recipientusecase.RecipientPage{}, err
	}
	recipients := make([]recipientusecase.Recipient, 0, len(page.UIDs))
	for _, uid := range page.UIDs {
		if uid != "" {
			recipients = append(recipients, recipientusecase.Recipient{UID: uid})
		}
	}
	return recipientusecase.RecipientPage{Recipients: recipients, Cursor: page.NextCursor, Done: page.Done}, nil
}
