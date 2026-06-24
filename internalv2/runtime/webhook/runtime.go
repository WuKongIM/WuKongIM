package webhook

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/workqueue"
)

const (
	webhookNotifyQueue  = "webhook_notify"
	webhookOfflineQueue = "webhook_offline"
	webhookOnlineQueue  = "webhook_online_status"

	resultAccepted       = "accepted"
	resultOK             = "ok"
	resultFull           = "full"
	resultClosed         = "closed"
	resultCanceled       = "canceled"
	resultTimeout        = "timeout"
	resultError          = "error"
	resultRetry          = "retry"
	resultRetryExhausted = "retry_exhausted"
	resultEncodeError    = "encode_error"

	defaultOfflineShards = 256
)

// RuntimeOptions configures the bounded webhook runtime.
type RuntimeOptions struct {
	// Sender delivers encoded webhook requests.
	Sender Sender
	// Observer receives low-cardinality runtime observations.
	Observer Observer
	// QueueSize bounds accepted webhook events waiting in memory per event queue.
	QueueSize int
	// Workers bounds concurrent sender calls per event queue.
	Workers int
	// NotifyBatchMaxItems limits msg.notify messages sent in one request.
	NotifyBatchMaxItems int
	// NotifyBatchMaxWait bounds how long msg.notify waits for adjacent messages.
	NotifyBatchMaxWait time.Duration
	// OnlineBatchMaxItems limits user.onlinestatus records sent in one request.
	OnlineBatchMaxItems int
	// OnlineBatchMaxWait bounds how long user.onlinestatus waits for adjacent records.
	OnlineBatchMaxWait time.Duration
	// OfflineUIDBatchSize is the compression threshold used for msg.offline UID chunks.
	OfflineUIDBatchSize int
	// RequestTimeout bounds one outbound sender attempt.
	RequestTimeout time.Duration
	// RetryMaxAttempts bounds attempts for one admitted webhook batch before drop.
	RetryMaxAttempts int
	// FocusEvents limits delivered event names. Empty means all events are delivered.
	FocusEvents []string
}

// Runtime owns bounded webhook event admission, batching, retry, and delivery.
type Runtime struct {
	opts     RuntimeOptions
	sender   Sender
	observer Observer
	focus    map[string]struct{}

	mu      sync.RWMutex
	notify  *workqueue.BoundedBatchPool[Message]
	online  *workqueue.BoundedBatchPool[OnlineStatus]
	offline *workqueue.ShardedMailbox[OfflineMessage]
}

// New creates a webhook runtime. Start opens the queues.
func New(opts RuntimeOptions) (*Runtime, error) {
	if opts.Sender == nil || opts.QueueSize <= 0 || opts.Workers <= 0 {
		return nil, workqueue.ErrInvalidConfig
	}
	if opts.NotifyBatchMaxWait < 0 || opts.OnlineBatchMaxWait < 0 || opts.RequestTimeout <= 0 {
		return nil, workqueue.ErrInvalidConfig
	}
	if opts.NotifyBatchMaxItems < 0 || opts.OnlineBatchMaxItems < 0 || opts.OfflineUIDBatchSize < 0 {
		return nil, workqueue.ErrInvalidConfig
	}
	if opts.NotifyBatchMaxItems == 0 {
		opts.NotifyBatchMaxItems = 1
	}
	if opts.OnlineBatchMaxItems == 0 {
		opts.OnlineBatchMaxItems = 1
	}
	rt := &Runtime{
		opts:     opts,
		sender:   opts.Sender,
		observer: opts.Observer,
		focus:    make(map[string]struct{}, len(opts.FocusEvents)),
	}
	for _, event := range opts.FocusEvents {
		if event != "" {
			rt.focus[event] = struct{}{}
		}
	}
	return rt, nil
}

// Start opens bounded queue admission.
func (r *Runtime) Start(ctx context.Context) error {
	if r == nil {
		return workqueue.ErrInvalidConfig
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.notify != nil || r.online != nil || r.offline != nil {
		return nil
	}

	notify, err := workqueue.NewBoundedBatchPool[Message](workqueue.BoundedBatchPoolConfig[Message]{
		Name:      webhookNotifyQueue,
		Workers:   r.opts.Workers,
		QueueSize: r.opts.QueueSize,
		Policy: func(Message) workqueue.BatchOptions {
			return workqueue.BatchOptions{MaxItems: r.opts.NotifyBatchMaxItems, MaxWait: r.opts.NotifyBatchMaxWait}
		},
	}, r.handleNotifyBatch)
	if err != nil {
		return err
	}

	online, err := workqueue.NewBoundedBatchPool[OnlineStatus](workqueue.BoundedBatchPoolConfig[OnlineStatus]{
		Name:      webhookOnlineQueue,
		Workers:   r.opts.Workers,
		QueueSize: r.opts.QueueSize,
		Policy: func(OnlineStatus) workqueue.BatchOptions {
			return workqueue.BatchOptions{MaxItems: r.opts.OnlineBatchMaxItems, MaxWait: r.opts.OnlineBatchMaxWait}
		},
	}, r.handleOnlineBatch)
	if err != nil {
		_ = notify.Close(context.Background())
		return err
	}

	shards, queueSizePerShard := offlineMailboxSizing(r.opts.QueueSize)
	offline, err := workqueue.NewShardedMailbox[OfflineMessage](workqueue.ShardedMailboxConfig{
		Name:              webhookOfflineQueue,
		Shards:            shards,
		Workers:           r.opts.Workers,
		QueueSizePerShard: queueSizePerShard,
		BatchMaxItems:     1,
	}, r.handleOfflineBatch)
	if err != nil {
		_ = online.Close(context.Background())
		_ = notify.Close(context.Background())
		return err
	}

	r.notify = notify
	r.online = online
	r.offline = offline
	return nil
}

// Stop closes admission and drains accepted webhook work until ctx expires.
func (r *Runtime) Stop(ctx context.Context) error {
	if r == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	r.mu.Lock()
	notify := r.notify
	online := r.online
	offline := r.offline
	r.notify = nil
	r.online = nil
	r.offline = nil
	r.mu.Unlock()

	return errors.Join(
		notify.Close(ctx),
		online.Close(ctx),
		offline.Close(ctx),
	)
}

// Notify admits one committed message for msg.notify delivery.
func (r *Runtime) Notify(ctx context.Context, msg Message) {
	if r == nil || !r.enabled(EventMsgNotify) {
		return
	}
	notify := r.notifyQueue()
	if notify == nil {
		r.observeAdmission(EventMsgNotify, webhookNotifyQueue, resultClosed, 1, 0, r.opts.QueueSize, nil)
		return
	}
	err := notify.Submit(ctx, cloneMessage(msg))
	r.observeAdmission(EventMsgNotify, webhookNotifyQueue, admissionResult(err), 1, notify.QueueDepth(), notify.QueueCapacity(), err)
}

// Offline admits one bounded recipient chunk for msg.offline delivery.
func (r *Runtime) Offline(ctx context.Context, msg OfflineMessage) {
	if r == nil || !r.enabled(EventMsgOffline) {
		return
	}
	offline := r.offlineQueue()
	items := len(msg.ToUIDs)
	if offline == nil {
		r.observeAdmission(EventMsgOffline, webhookOfflineQueue, resultClosed, items, 0, r.opts.QueueSize, nil)
		return
	}
	cloned := cloneOfflineMessage(msg)
	err := offline.Submit(ctx, cloned.Message.ChannelID, cloned)
	r.observeAdmission(EventMsgOffline, webhookOfflineQueue, admissionResult(err), items, offline.QueueDepth(), r.opts.QueueSize, err)
}

// OnlineStatus admits one legacy-compatible status record.
func (r *Runtime) OnlineStatus(ctx context.Context, status OnlineStatus) {
	if r == nil || !r.enabled(EventUserOnlineStatus) || status.Value == "" {
		return
	}
	online := r.onlineQueue()
	if online == nil {
		r.observeAdmission(EventUserOnlineStatus, webhookOnlineQueue, resultClosed, 1, 0, r.opts.QueueSize, nil)
		return
	}
	err := online.Submit(ctx, status)
	r.observeAdmission(EventUserOnlineStatus, webhookOnlineQueue, admissionResult(err), 1, online.QueueDepth(), online.QueueCapacity(), err)
}

func (r *Runtime) handleNotifyBatch(ctx context.Context, batch []Message) error {
	body, err := buildNotifyBody(batch)
	if err != nil {
		r.observeSend(EventMsgNotify, resultEncodeError, len(batch), 0, 0, err)
		return nil
	}
	r.sendWithRetry(ctx, EventMsgNotify, body, len(batch))
	return nil
}

func (r *Runtime) handleOnlineBatch(ctx context.Context, batch []OnlineStatus) error {
	body, err := buildOnlineStatusBody(batch)
	if err != nil {
		r.observeSend(EventUserOnlineStatus, resultEncodeError, len(batch), 0, 0, err)
		return nil
	}
	r.sendWithRetry(ctx, EventUserOnlineStatus, body, len(batch))
	return nil
}

func (r *Runtime) handleOfflineBatch(ctx context.Context, batch workqueue.MailboxBatch[OfflineMessage]) error {
	for _, item := range batch.Items {
		body, err := buildOfflineBody(item, r.opts.OfflineUIDBatchSize)
		items := len(item.ToUIDs)
		if err != nil {
			r.observeSend(EventMsgOffline, resultEncodeError, items, 0, 0, err)
			continue
		}
		r.sendWithRetry(ctx, EventMsgOffline, body, items)
	}
	return nil
}

func (r *Runtime) sendWithRetry(ctx context.Context, event string, body []byte, items int) {
	if ctx == nil {
		ctx = context.Background()
	}
	attempts := r.opts.RetryMaxAttempts
	if attempts <= 0 {
		attempts = 1
	}
	for attempt := 1; attempt <= attempts; attempt++ {
		attemptCtx := ctx
		cancel := func() {}
		if r.opts.RequestTimeout > 0 {
			attemptCtx, cancel = context.WithTimeout(ctx, r.opts.RequestTimeout)
		}
		started := time.Now()
		err := r.sender.Send(attemptCtx, SendRequest{Event: event, Body: body})
		cancel()
		duration := time.Since(started)
		if err == nil {
			r.observeSend(event, resultOK, items, attempt, duration, nil)
			return
		}
		if attempt == attempts {
			r.observeSend(event, resultRetryExhausted, items, attempt, duration, err)
			return
		}
		r.observeSend(event, resultRetry, items, attempt, duration, err)
	}
}

func (r *Runtime) enabled(event string) bool {
	if r == nil {
		return false
	}
	if len(r.focus) == 0 {
		return true
	}
	_, ok := r.focus[event]
	return ok
}

func (r *Runtime) notifyQueue() *workqueue.BoundedBatchPool[Message] {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.notify
}

func (r *Runtime) onlineQueue() *workqueue.BoundedBatchPool[OnlineStatus] {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.online
}

func (r *Runtime) offlineQueue() *workqueue.ShardedMailbox[OfflineMessage] {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.offline
}

func (r *Runtime) observeAdmission(event string, queue string, result string, items int, depth int, size int, err error) {
	if r == nil || r.observer == nil {
		return
	}
	r.observer.ObserveWebhook(Observation{
		Queue:      queue,
		Event:      event,
		Result:     result,
		Items:      items,
		QueueDepth: depth,
		QueueSize:  size,
		Err:        err,
	})
}

func (r *Runtime) observeSend(event string, result string, items int, attempt int, duration time.Duration, err error) {
	if r == nil || r.observer == nil {
		return
	}
	r.observer.ObserveWebhook(Observation{
		Queue:    queueForEvent(event),
		Event:    event,
		Result:   result,
		Items:    items,
		Attempt:  attempt,
		Duration: duration,
		Err:      err,
	})
}

func queueForEvent(event string) string {
	switch event {
	case EventMsgNotify:
		return webhookNotifyQueue
	case EventMsgOffline:
		return webhookOfflineQueue
	case EventUserOnlineStatus:
		return webhookOnlineQueue
	default:
		return ""
	}
}

func admissionResult(err error) string {
	switch {
	case err == nil:
		return resultAccepted
	case errors.Is(err, workqueue.ErrFull):
		return resultFull
	case errors.Is(err, workqueue.ErrClosed):
		return resultClosed
	case errors.Is(err, context.Canceled):
		return resultCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return resultTimeout
	default:
		return resultError
	}
}

func cloneMessage(msg Message) Message {
	msg.Payload = append([]byte(nil), msg.Payload...)
	return msg
}

func cloneOfflineMessage(msg OfflineMessage) OfflineMessage {
	msg.Message = cloneMessage(msg.Message)
	msg.ToUIDs = append([]string(nil), msg.ToUIDs...)
	return msg
}

func offlineMailboxSizing(queueSize int) (int, int) {
	if queueSize <= 1 {
		return 1, 1
	}
	shards := defaultOfflineShards
	if queueSize < shards {
		shards = queueSize
	}
	queueSizePerShard := (queueSize + shards - 1) / shards
	if queueSizePerShard <= 0 {
		queueSizePerShard = 1
	}
	return shards, queueSizePerShard
}
