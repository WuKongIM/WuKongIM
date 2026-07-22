package channelappend

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

const defaultRecipientOwnerPushConcurrency = 4

var (
	// ErrInvalidSubscriberCursor reports a non-terminal subscriber page without a usable next cursor.
	ErrInvalidSubscriberCursor = errors.New("internal/channelappend: invalid subscriber cursor")
	// ErrCommitEffectFailed reports a post-commit effect failure that will be logged and dropped.
	ErrCommitEffectFailed = errors.New("internal/channelappend: commit effect failed")
	// ErrDeliveryRetryExhausted reports retryable owner push routes after the final delivery attempt.
	ErrDeliveryRetryExhausted = errors.New("internal/channelappend: delivery retry exhausted")
	// ErrEffectPanic reports a recovered panic from an asynchronous channel append effect.
	ErrEffectPanic = errors.New("internal/channelappend: effect panic")
	// ErrRealtimeDeliveryRequired reports a transient send without a realtime delivery queue.
	ErrRealtimeDeliveryRequired = errors.New("internal/channelappend: realtime delivery required")
	// ErrRecipientPresenceResultMissing reports a target-aware presence response
	// that is not aligned with the submitted exact-target groups.
	ErrRecipientPresenceResultMissing = errors.New("internal/channelappend: recipient presence result missing")
)

// RecipientProcessorOptions configures recipient-authority post-commit processing.
type RecipientProcessorOptions struct {
	// PresenceResolver resolves online recipient endpoints for delivery pushes.
	PresenceResolver PresenceResolver
	// OwnerPusher pushes online delivery commands to owner nodes.
	OwnerPusher OwnerPusher
	// OwnerPushBatchSize bounds routes sent in one owner-node push. Values <= 0 use a bounded default.
	OwnerPushBatchSize int
	// OfflineRecipientsObserver receives durable offline recipients in one batch.
	OfflineRecipientsObserver OfflineRecipientsObserver
	// OfflineRecipientObserver receives durable recipients with no online route.
	OfflineRecipientObserver OfflineRecipientObserver
	// DeliveryRetryMaxAttempts bounds retryable owner push attempts. Values <= 0 use a bounded default.
	DeliveryRetryMaxAttempts int
	// DeliveryRetryInitialBackoff is the first retry sleep for retryable owner pushes. Values <= 0 use a bounded default.
	DeliveryRetryInitialBackoff time.Duration
	// DeliveryRetryMaxBackoff caps retry sleeps for retryable owner pushes. Values <= 0 use a bounded default.
	DeliveryRetryMaxBackoff time.Duration
}

// RecipientProcessor applies recipient-authority delivery effects.
type RecipientProcessor struct {
	ports recipientPorts
}

type recipientPorts struct {
	presence                    PresenceResolver
	pusher                      OwnerPusher
	ownerPushBatchSize          int
	offlineRecipientsObserver   OfflineRecipientsObserver
	offlineRecipientObserver    OfflineRecipientObserver
	deliveryRetryMaxAttempts    int
	deliveryRetryInitialBackoff time.Duration
	deliveryRetryMaxBackoff     time.Duration
}

// NewRecipientProcessor creates a recipient-authority post-commit processor.
func NewRecipientProcessor(opts RecipientProcessorOptions) *RecipientProcessor {
	return &RecipientProcessor{ports: recipientPorts{
		presence:                    opts.PresenceResolver,
		pusher:                      opts.OwnerPusher,
		ownerPushBatchSize:          opts.OwnerPushBatchSize,
		offlineRecipientsObserver:   opts.OfflineRecipientsObserver,
		offlineRecipientObserver:    opts.OfflineRecipientObserver,
		deliveryRetryMaxAttempts:    opts.DeliveryRetryMaxAttempts,
		deliveryRetryInitialBackoff: opts.DeliveryRetryInitialBackoff,
		deliveryRetryMaxBackoff:     opts.DeliveryRetryMaxBackoff,
	}}
}

// ProcessRecipientBatch pushes online delivery routes for a recipient-authority batch.
func (p *RecipientProcessor) ProcessRecipientBatch(ctx context.Context, batch RecipientBatch) error {
	if p == nil {
		return nil
	}
	return processRecipientBatch(ctx, batch, p.ports)
}

// ProcessRecipientDeliveryPlan resolves and processes every exact-target group
// in one bounded plan. Returned errors align with plan.Targets; one failed
// authority group does not prevent other groups from being delivered.
func (p *RecipientProcessor) ProcessRecipientDeliveryPlan(ctx context.Context, plan RecipientDeliveryPlan) []error {
	results := make([]error, len(plan.Targets))
	if p == nil || len(plan.Targets) == 0 {
		return results
	}
	if p.ports.presence == nil ||
		(p.ports.pusher == nil && p.ports.offlineRecipientsObserver == nil && p.ports.offlineRecipientObserver == nil) {
		return results
	}
	if err := contextErr(ctx); err != nil {
		for i, target := range plan.Targets {
			results[i] = withPostCommitFailureDetail(err, postCommitBatchDetail("context", RecipientBatch{
				Event:      plan.Event,
				Recipients: target.Recipients,
			}))
		}
		return results
	}
	targetResolver, ok := p.ports.presence.(RecipientTargetPresenceResolver)
	if !ok {
		for i, target := range plan.Targets {
			results[i] = processRecipientBatchSafely(ctx, RecipientBatch{Event: plan.Event, Recipients: target.Recipients}, p.ports)
		}
		return results
	}

	resolved := targetResolver.EndpointsByTargets(ctx, plan.Targets)
	grouped := make(map[uint64]*recipientPlanOwnerRoutes)
	ownerOrder := make([]uint64, 0)
	for i, target := range plan.Targets {
		batch := RecipientBatch{Event: plan.Event, Recipients: target.Recipients}
		if i >= len(resolved) {
			results[i] = withPostCommitFailureDetail(ErrRecipientPresenceResultMissing, postCommitBatchDetail("presence_resolve", batch))
			continue
		}
		if resolved[i].Err != nil {
			detail := postCommitBatchDetail("presence_resolve", batch)
			detail.UIDCount = recipientUIDCount(batch.Recipients)
			results[i] = withPostCommitFailureDetail(resolved[i].Err, detail)
			continue
		}
		routes, err := prepareRecipientBatchRoutesSafely(ctx, batch, resolved[i].Routes, p.ports)
		if err != nil {
			results[i] = err
			continue
		}
		for _, route := range routes {
			ownerRoutes, exists := grouped[route.OwnerNodeID]
			if !exists {
				ownerRoutes = &recipientPlanOwnerRoutes{}
				grouped[route.OwnerNodeID] = ownerRoutes
				ownerOrder = append(ownerOrder, route.OwnerNodeID)
			}
			ownerRoutes.routes = append(ownerRoutes.routes, route)
			ownerRoutes.targetIndexes = append(ownerRoutes.targetIndexes, i)
		}
	}
	if p.ports.pusher == nil {
		return results
	}
	batchSize := boundedPositive(p.ports.ownerPushBatchSize, defaultRecipientBatchSize)
	errorsByOwner := make([][]error, len(ownerOrder))
	processOwner := func(ownerIndex int) {
		ownerNodeID := ownerOrder[ownerIndex]
		ownerRoutes := grouped[ownerNodeID]
		var ownerErrors []error
		for start := 0; start < len(ownerRoutes.routes); start += batchSize {
			end := start + batchSize
			if end > len(ownerRoutes.routes) {
				end = len(ownerRoutes.routes)
			}
			routes := ownerRoutes.routes[start:end]
			targetIndexes := ownerRoutes.targetIndexes[start:end]
			failedRoutes, err := pushOwnerRoutesWithRetry(ctx, p.ports, PushCommand{
				OwnerNodeID: ownerNodeID,
				Envelope:    plan.Event,
				Routes:      routes,
			})
			if err == nil {
				continue
			}
			if ownerErrors == nil {
				ownerErrors = make([]error, len(plan.Targets))
			}
			applyRecipientPlanOwnerPushFailure(ownerErrors, plan, ownerNodeID, routes, targetIndexes, failedRoutes, err)
		}
		errorsByOwner[ownerIndex] = ownerErrors
	}
	runBoundedRecipientOwnerPushes(len(ownerOrder), defaultRecipientOwnerPushConcurrency, processOwner)
	for _, ownerErrors := range errorsByOwner {
		for targetIndex, err := range ownerErrors {
			if err != nil && results[targetIndex] == nil {
				results[targetIndex] = err
			}
		}
	}
	return results
}

func runBoundedRecipientOwnerPushes(count, concurrency int, run func(int)) {
	if count <= 0 {
		return
	}
	if concurrency <= 1 || count == 1 {
		for index := 0; index < count; index++ {
			run(index)
		}
		return
	}
	if concurrency > count {
		concurrency = count
	}
	var next atomic.Int64
	var wg sync.WaitGroup
	worker := func() {
		for {
			index := int(next.Add(1) - 1)
			if index >= count {
				return
			}
			run(index)
		}
	}
	wg.Add(concurrency - 1)
	for i := 1; i < concurrency; i++ {
		go func() {
			defer wg.Done()
			worker()
		}()
	}
	worker()
	wg.Wait()
}

type recipientPlanOwnerRoutes struct {
	routes        []Route
	targetIndexes []int
}

func prepareRecipientBatchRoutesSafely(
	ctx context.Context,
	batch RecipientBatch,
	routes []Route,
	ports recipientPorts,
) (prepared []Route, err error) {
	defer recoverRecipientBatchPanic(batch, &err)
	if shouldObserveOfflineRecipients(batch.Event, ports.offlineRecipientsObserver, ports.offlineRecipientObserver) {
		uids := recipientUIDs(batch.Recipients)
		observeOfflineRecipients(ctx, batch, uids, routes, ports.offlineRecipientsObserver, ports.offlineRecipientObserver)
	}
	if ports.pusher == nil {
		return nil, nil
	}
	return filterSenderEchoRoute(batch.Event, routes), nil
}

func applyRecipientPlanOwnerPushFailure(
	results []error,
	plan RecipientDeliveryPlan,
	ownerNodeID uint64,
	routes []Route,
	targetIndexes []int,
	failedRoutes []Route,
	err error,
) {
	orderedTargets, routesByTarget := failedRecipientPlanRoutesByTarget(routes, targetIndexes, failedRoutes)
	for _, targetIndex := range orderedTargets {
		if targetIndex < 0 || targetIndex >= len(plan.Targets) || results[targetIndex] != nil {
			continue
		}
		batch := RecipientBatch{Event: plan.Event, Recipients: plan.Targets[targetIndex].Recipients}
		targetRoutes := routesByTarget[targetIndex]
		detail := postCommitBatchDetail("owner_push", batch)
		detail.UID = firstRouteUID(targetRoutes)
		detail.DispatchOwnerNodeID = ownerNodeID
		detail.DispatchOwnerRouteNum = len(targetRoutes)
		results[targetIndex] = withPostCommitFailureDetail(err, detail)
	}
}

func failedRecipientPlanRoutesByTarget(
	routes []Route,
	targetIndexes []int,
	failedRoutes []Route,
) ([]int, map[int][]Route) {
	if len(routes) != len(targetIndexes) || len(failedRoutes) == 0 {
		return allRecipientPlanRoutesByTarget(routes, targetIndexes)
	}
	positions := make(map[Route][]int, len(routes))
	for i, route := range routes {
		positions[route] = append(positions[route], i)
	}
	used := make(map[Route]int, len(failedRoutes))
	orderedTargets := make([]int, 0)
	routesByTarget := make(map[int][]Route)
	for _, route := range failedRoutes {
		positionList := positions[route]
		positionIndex := used[route]
		if positionIndex >= len(positionList) {
			return allRecipientPlanRoutesByTarget(routes, targetIndexes)
		}
		used[route] = positionIndex + 1
		targetIndex := targetIndexes[positionList[positionIndex]]
		if _, exists := routesByTarget[targetIndex]; !exists {
			orderedTargets = append(orderedTargets, targetIndex)
		}
		routesByTarget[targetIndex] = append(routesByTarget[targetIndex], route)
	}
	return orderedTargets, routesByTarget
}

func allRecipientPlanRoutesByTarget(routes []Route, targetIndexes []int) ([]int, map[int][]Route) {
	orderedTargets := make([]int, 0)
	routesByTarget := make(map[int][]Route)
	for i, route := range routes {
		if i >= len(targetIndexes) {
			break
		}
		targetIndex := targetIndexes[i]
		if _, exists := routesByTarget[targetIndex]; !exists {
			orderedTargets = append(orderedTargets, targetIndex)
		}
		routesByTarget[targetIndex] = append(routesByTarget[targetIndex], route)
	}
	return orderedTargets, routesByTarget
}

func processRecipientBatchSafely(ctx context.Context, batch RecipientBatch, ports recipientPorts) (err error) {
	defer recoverRecipientBatchPanic(batch, &err)
	return processRecipientBatch(ctx, batch, ports)
}

func processRecipientBatchWithRoutesSafely(ctx context.Context, batch RecipientBatch, routes []Route, ports recipientPorts) (err error) {
	defer recoverRecipientBatchPanic(batch, &err)
	return processRecipientBatchWithRoutes(ctx, batch, routes, ports)
}

func recoverRecipientBatchPanic(batch RecipientBatch, err *error) {
	if recovered := recover(); recovered != nil {
		*err = withPostCommitFailureDetail(
			effectPanicError(effectStagePostCommit, recovered),
			postCommitBatchDetail("panic", batch),
		)
	}
}

func processRecipientBatch(ctx context.Context, batch RecipientBatch, ports recipientPorts) error {
	if len(batch.Recipients) == 0 {
		return nil
	}
	if err := contextErr(ctx); err != nil {
		return withPostCommitFailureDetail(err, postCommitBatchDetail("context", batch))
	}
	if ports.presence == nil ||
		(ports.pusher == nil && ports.offlineRecipientsObserver == nil && ports.offlineRecipientObserver == nil) {
		return nil
	}
	uids := recipientUIDs(batch.Recipients)
	routes, err := ports.presence.EndpointsByUIDs(ctx, uids)
	if err != nil {
		detail := postCommitBatchDetail("presence_resolve", batch)
		detail.UIDCount = len(uids)
		return withPostCommitFailureDetail(err, detail)
	}
	return processRecipientBatchWithRoutes(ctx, batch, routes, ports)
}

func processRecipientBatchWithRoutes(ctx context.Context, batch RecipientBatch, routes []Route, ports recipientPorts) error {
	if shouldObserveOfflineRecipients(batch.Event, ports.offlineRecipientsObserver, ports.offlineRecipientObserver) {
		uids := recipientUIDs(batch.Recipients)
		observeOfflineRecipients(ctx, batch, uids, routes, ports.offlineRecipientsObserver, ports.offlineRecipientObserver)
	}
	if ports.pusher == nil {
		return nil
	}
	routes = filterSenderEchoRoute(batch.Event, routes)
	grouped, ownerOrder := routesByOwner(routes)
	batchSize := boundedPositive(ports.ownerPushBatchSize, defaultRecipientBatchSize)
	for _, ownerNodeID := range ownerOrder {
		ownerRoutes := grouped[ownerNodeID]
		for start := 0; start < len(ownerRoutes); start += batchSize {
			end := start + batchSize
			if end > len(ownerRoutes) {
				end = len(ownerRoutes)
			}
			failedRoutes, err := pushOwnerRoutesWithRetry(ctx, ports, PushCommand{
				OwnerNodeID: ownerNodeID,
				Envelope:    batch.Event,
				Routes:      ownerRoutes[start:end],
			})
			if err != nil {
				detail := postCommitBatchDetail("owner_push", batch)
				detail.UID = firstRouteUID(failedRoutes)
				detail.UIDCount = recipientUIDCount(batch.Recipients)
				detail.DispatchOwnerNodeID = ownerNodeID
				detail.DispatchOwnerRouteNum = len(failedRoutes)
				return withPostCommitFailureDetail(err, detail)
			}
		}
	}
	return nil
}

func shouldObserveOfflineRecipients(
	event CommittedEnvelope,
	batchObserver OfflineRecipientsObserver,
	singleObserver OfflineRecipientObserver,
) bool {
	return (batchObserver != nil || singleObserver != nil) && offlineRecipientObserverEligible(event)
}

func observeOfflineRecipients(
	ctx context.Context,
	batch RecipientBatch,
	uids []string,
	routes []Route,
	batchObserver OfflineRecipientsObserver,
	singleObserver OfflineRecipientObserver,
) {
	if (batchObserver == nil && singleObserver == nil) || !offlineRecipientObserverEligible(batch.Event) {
		return
	}
	online := make(map[string]struct{}, len(routes))
	for _, route := range routes {
		if route.UID != "" {
			online[route.UID] = struct{}{}
		}
	}
	seenOffline := make(map[string]struct{}, len(uids))
	offlineUIDs := make([]string, 0, len(uids))
	for _, uid := range uids {
		if uid == "" {
			continue
		}
		if _, ok := online[uid]; ok {
			continue
		}
		if _, ok := seenOffline[uid]; ok {
			continue
		}
		seenOffline[uid] = struct{}{}
		offlineUIDs = append(offlineUIDs, uid)
	}
	if len(offlineUIDs) == 0 {
		return
	}
	if batchObserver == nil && singleObserver != nil {
		batchObserver, _ = singleObserver.(OfflineRecipientsObserver)
	}
	if batchObserver != nil {
		batchObserver.ObserveOfflineRecipients(ctx, OfflineRecipientsEvent{
			Event: batch.Event,
			UIDs:  append([]string(nil), offlineUIDs...),
		})
		return
	}
	if singleObserver == nil {
		return
	}
	for _, uid := range offlineUIDs {
		singleObserver.ObserveOfflineRecipient(ctx, OfflineRecipientEvent{Event: batch.Event, UID: uid})
	}
}

func offlineRecipientObserverEligible(event CommittedEnvelope) bool {
	return event.MessageSeq > 0 && !event.SyncOnce && len(event.MessageScopedUIDs) == 0
}

func postCommitBatchDetail(phase string, batch RecipientBatch) PostCommitFailureDetail {
	uids := recipientUIDs(batch.Recipients)
	return PostCommitFailureDetail{
		Phase:          phase,
		UID:            firstString(uids),
		UIDCount:       len(uids),
		RecipientCount: len(batch.Recipients),
	}
}

func recipientUIDs(recipients []Recipient) []string {
	uids := make([]string, 0, len(recipients))
	for _, recipient := range recipients {
		if recipient.UID == "" {
			continue
		}
		uids = append(uids, recipient.UID)
	}
	return uids
}

func recipientUIDCount(recipients []Recipient) int {
	count := 0
	for _, recipient := range recipients {
		if recipient.UID != "" {
			count++
		}
	}
	return count
}

func filterSenderEchoRoute(event CommittedEnvelope, routes []Route) []Route {
	out := routes[:0]
	for _, route := range routes {
		if route.UID == event.FromUID &&
			route.OwnerNodeID == event.SenderNodeID &&
			route.SessionID == event.SenderSessionID {
			continue
		}
		out = append(out, route)
	}
	return out
}

func routesByOwner(routes []Route) (map[uint64][]Route, []uint64) {
	out := make(map[uint64][]Route)
	order := make([]uint64, 0)
	for _, route := range routes {
		if _, ok := out[route.OwnerNodeID]; !ok {
			order = append(order, route.OwnerNodeID)
		}
		out[route.OwnerNodeID] = append(out[route.OwnerNodeID], route)
	}
	return out, order
}

func firstRouteUID(routes []Route) string {
	if len(routes) == 0 {
		return ""
	}
	return routes[0].UID
}

func pushOwnerRoutesWithRetry(ctx context.Context, ports recipientPorts, cmd PushCommand) ([]Route, error) {
	attempts := ports.deliveryRetryMaxAttempts
	if attempts <= 0 {
		attempts = defaultDeliveryRetryMaxAttempts
	}
	backoff := ports.deliveryRetryInitialBackoff
	if backoff <= 0 {
		backoff = defaultDeliveryRetryInitialBackoff
	}
	maxBackoff := ports.deliveryRetryMaxBackoff
	if maxBackoff <= 0 {
		maxBackoff = defaultDeliveryRetryMaxBackoff
	}
	if len(cmd.Routes) == 0 {
		return nil, nil
	}
	for attempt := 1; attempt <= attempts; attempt++ {
		if err := contextErr(ctx); err != nil {
			return append([]Route(nil), cmd.Routes...), err
		}
		result, err := pushOwnerRoutesSafely(ctx, ports.pusher, cmd)
		if err != nil {
			if errors.Is(err, ErrEffectPanic) {
				return append([]Route(nil), cmd.Routes...), err
			}
			if attempt == attempts {
				return append([]Route(nil), cmd.Routes...), err
			}
		} else {
			if len(result.Retryable) == 0 {
				return nil, nil
			}
			cmd.Routes = append([]Route(nil), result.Retryable...)
			if attempt == attempts {
				return append([]Route(nil), cmd.Routes...), ErrDeliveryRetryExhausted
			}
		}
		if err := sleepDeliveryRetry(ctx, backoff); err != nil {
			return append([]Route(nil), cmd.Routes...), err
		}
		backoff = nextDeliveryRetryBackoff(backoff, maxBackoff)
	}
	return nil, nil
}

func pushOwnerRoutesSafely(ctx context.Context, pusher OwnerPusher, cmd PushCommand) (result PushResult, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			result = PushResult{}
			err = effectPanicError(effectStagePostCommit, recovered)
		}
	}()
	return pusher.Push(ctx, cmd)
}

func sleepDeliveryRetry(ctx context.Context, backoff time.Duration) error {
	timer := time.NewTimer(backoff)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func nextDeliveryRetryBackoff(backoff, maxBackoff time.Duration) time.Duration {
	next := backoff * 2
	if next > maxBackoff {
		return maxBackoff
	}
	return next
}
