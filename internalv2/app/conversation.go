package app

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/messageevents"
	conversationusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/conversation"
	"github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
)

type committedSinkGroup []message.CommittedSink

type conversationPatchProjector interface {
	ProjectActivePatches(context.Context, messageevents.MessageCommitted) ([]conversationusecase.ActivePatch, error)
}

type conversationPatchAuthority interface {
	AdmitPatches(context.Context, []conversationusecase.ActivePatch) error
}

type conversationAuthorityFlusher interface {
	Flush(context.Context) error
}

type conversationAuthorityCommittedOptions struct {
	// Projector derives UID-owned active patches from committed message events.
	Projector conversationPatchProjector
	// Authority routes active patches to the current UID authority.
	Authority conversationPatchAuthority
	// Flusher persists any local authority cache rows during explicit flush/stop.
	Flusher conversationAuthorityFlusher
	// LocalAuthority tracks route-authority state for this node.
	LocalAuthority *conversationAuthority
	// LocalNodeID identifies this node when applying route-authority events.
	LocalNodeID uint64
	// Initial returns the currently known route authorities when the sink starts.
	Initial func() []clusterv2.RouteAuthority
	// Watch creates the route-authority event stream when the sink starts.
	Watch func() <-chan clusterv2.RouteAuthorityEvent
	// AdmissionTimeout bounds foreground cache admission after a durable commit.
	AdmissionTimeout time.Duration
	// HandoffTimeout bounds local authority drain during route-authority changes.
	HandoffTimeout time.Duration
	// RPCTimeout bounds one authority admission call.
	RPCTimeout time.Duration
	// RPCBatchRows limits active patches sent in one admission call.
	RPCBatchRows int
	// RPCConcurrency limits concurrent admission calls for one committed event.
	RPCConcurrency int
	// AuthorityObserver receives low-cardinality authority admission observations.
	AuthorityObserver conversationAuthorityObserver
}

type conversationAuthorityPendingKey struct {
	// uid owns the pending active patch.
	uid string
	// channelID identifies the pending conversation row.
	channelID string
	// channelType identifies the pending conversation namespace.
	channelType int64
}

type conversationAuthorityCommittedSink struct {
	projector         conversationPatchProjector
	authority         conversationPatchAuthority
	flusher           conversationAuthorityFlusher
	localAuthority    *conversationAuthority
	localNodeID       uint64
	initial           func() []clusterv2.RouteAuthority
	watch             func() <-chan clusterv2.RouteAuthorityEvent
	admissionTimeout  time.Duration
	handoffTimeout    time.Duration
	rpcTimeout        time.Duration
	rpcBatchRows      int
	rpcConcurrency    int
	authorityObserver conversationAuthorityObserver

	mu     sync.Mutex
	cancel context.CancelFunc
	wg     sync.WaitGroup
	latest map[uint16]conversationusecase.RouteTarget
	// pending keeps patches that were derived after a durable append but not admitted yet.
	pending map[conversationAuthorityPendingKey]conversationusecase.ActivePatch
}

func newConversationAuthorityCommittedSink(opts conversationAuthorityCommittedOptions) *conversationAuthorityCommittedSink {
	if opts.AdmissionTimeout <= 0 {
		opts.AdmissionTimeout = 500 * time.Millisecond
	}
	if opts.HandoffTimeout <= 0 {
		opts.HandoffTimeout = 3 * time.Second
	}
	if opts.RPCTimeout <= 0 {
		opts.RPCTimeout = 500 * time.Millisecond
	}
	if opts.RPCBatchRows <= 0 {
		opts.RPCBatchRows = 512
	}
	if opts.RPCConcurrency <= 0 {
		opts.RPCConcurrency = 16
	}
	return &conversationAuthorityCommittedSink{
		projector:         opts.Projector,
		authority:         opts.Authority,
		flusher:           opts.Flusher,
		localAuthority:    opts.LocalAuthority,
		localNodeID:       opts.LocalNodeID,
		initial:           opts.Initial,
		watch:             opts.Watch,
		admissionTimeout:  opts.AdmissionTimeout,
		handoffTimeout:    opts.HandoffTimeout,
		rpcTimeout:        opts.RPCTimeout,
		rpcBatchRows:      opts.RPCBatchRows,
		rpcConcurrency:    opts.RPCConcurrency,
		authorityObserver: opts.AuthorityObserver,
		latest:            make(map[uint16]conversationusecase.RouteTarget),
		pending:           make(map[conversationAuthorityPendingKey]conversationusecase.ActivePatch),
	}
}

func (s *conversationAuthorityCommittedSink) Start(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	runCtx, cancel := context.WithCancel(ctx)
	var events <-chan clusterv2.RouteAuthorityEvent
	if s.watch != nil {
		events = s.watch()
	}
	s.mu.Lock()
	if s.cancel != nil {
		s.mu.Unlock()
		cancel()
		return nil
	}
	s.cancel = cancel
	if s.latest == nil {
		s.latest = make(map[uint16]conversationusecase.RouteTarget)
	}
	if events != nil {
		s.wg.Add(1)
		go s.watchRouteAuthorities(runCtx, events)
	}
	s.mu.Unlock()
	s.applyRouteAuthorities(runCtx, s.initialAuthorities())
	return nil
}

func (s *conversationAuthorityCommittedSink) Stop(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	cancel := s.cancel
	s.cancel = nil
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	s.wg.Wait()
	return s.Flush(ctx)
}

func (s *conversationAuthorityCommittedSink) Submit(ctx context.Context, event messageevents.MessageCommitted) error {
	if s == nil || s.projector == nil || s.authority == nil {
		s.observeAuthorityAdmit(conversationAuthorityResultIgnored)
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	admissionCtx, cancel := context.WithTimeout(ctx, s.admissionTimeout)
	defer cancel()
	patches, err := s.projector.ProjectActivePatches(admissionCtx, event)
	if err != nil {
		s.observeAuthorityAdmit(conversationAuthorityResultFromError(err, conversationAuthorityResultError))
		return err
	}
	if len(patches) == 0 {
		s.observeAuthorityAdmit(conversationAuthorityResultIgnored)
		return nil
	}
	attempt := s.pendingSnapshotWith(patches)
	if err := s.admitBatches(admissionCtx, attempt); err != nil {
		s.rememberPending(attempt)
		s.observeAuthorityAdmit(conversationAuthorityResultFromError(err, conversationAuthorityResultError))
		return err
	}
	s.clearPending(attempt)
	s.observeAuthorityAdmit(conversationAuthorityResultOK)
	return nil
}

func (s *conversationAuthorityCommittedSink) SubmitMetadata(ctx context.Context, event messageevents.MessageCommitted) error {
	event.Payload = nil
	event.MessageScopedUIDs = nil
	return s.Submit(ctx, event)
}

func (s *conversationAuthorityCommittedSink) RequiresCommittedPayload() bool {
	return false
}

func (s *conversationAuthorityCommittedSink) Flush(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	err := s.retryPending(ctx)
	if s.flusher != nil {
		if flushErr := s.flusher.Flush(ctx); err == nil {
			err = flushErr
		}
	}
	return err
}

func (s *conversationAuthorityCommittedSink) retryPending(ctx context.Context) error {
	if s == nil {
		return nil
	}
	attempt := s.pendingSnapshotWith(nil)
	if len(attempt) == 0 {
		return nil
	}
	if err := s.admitBatches(ctx, attempt); err != nil {
		s.rememberPending(attempt)
		return err
	}
	s.clearPending(attempt)
	return nil
}

func (s *conversationAuthorityCommittedSink) admitBatches(ctx context.Context, patches []conversationusecase.ActivePatch) error {
	batches := conversationActivePatchBatches(patches, s.rpcBatchRows)
	if len(batches) == 0 {
		return nil
	}
	if s.rpcConcurrency <= 1 || len(batches) == 1 {
		for _, batch := range batches {
			if err := s.admitBatch(ctx, batch); err != nil {
				return err
			}
		}
		return nil
	}
	workers := s.rpcConcurrency
	if workers > len(batches) {
		workers = len(batches)
	}
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan []conversationusecase.ActivePatch)
	var wg sync.WaitGroup
	var firstErr error
	var firstErrOnce sync.Once
	setErr := func(err error) {
		if err == nil {
			return
		}
		firstErrOnce.Do(func() {
			firstErr = err
			cancel()
		})
	}
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for batch := range jobs {
				if err := s.admitBatch(workCtx, batch); err != nil {
					setErr(err)
				}
			}
		}()
	}
send:
	for _, batch := range batches {
		select {
		case <-workCtx.Done():
			break send
		case jobs <- batch:
		}
	}
	close(jobs)
	wg.Wait()
	if firstErr != nil {
		return firstErr
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

func (s *conversationAuthorityCommittedSink) admitBatch(ctx context.Context, patches []conversationusecase.ActivePatch) error {
	if s.rpcTimeout <= 0 {
		return s.authority.AdmitPatches(ctx, patches)
	}
	rpcCtx, cancel := context.WithTimeout(ctx, s.rpcTimeout)
	defer cancel()
	return s.authority.AdmitPatches(rpcCtx, patches)
}

func (s *conversationAuthorityCommittedSink) pendingSnapshotWith(patches []conversationusecase.ActivePatch) []conversationusecase.ActivePatch {
	s.mu.Lock()
	defer s.mu.Unlock()
	merged := make(map[conversationAuthorityPendingKey]conversationusecase.ActivePatch, len(s.pending)+len(patches))
	for key, patch := range s.pending {
		merged[key] = patch
	}
	for _, patch := range patches {
		if patch.UID == "" || patch.ChannelID == "" || patch.ChannelType == 0 {
			continue
		}
		key := pendingConversationPatchKey(patch)
		merged[key] = mergeConversationActivePatch(merged[key], patch)
	}
	return sortedPendingConversationPatches(merged)
}

func (s *conversationAuthorityCommittedSink) rememberPending(patches []conversationusecase.ActivePatch) {
	if s == nil || len(patches) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending == nil {
		s.pending = make(map[conversationAuthorityPendingKey]conversationusecase.ActivePatch)
	}
	for _, patch := range patches {
		if patch.UID == "" || patch.ChannelID == "" || patch.ChannelType == 0 {
			continue
		}
		key := pendingConversationPatchKey(patch)
		s.pending[key] = mergeConversationActivePatch(s.pending[key], patch)
	}
}

func (s *conversationAuthorityCommittedSink) clearPending(patches []conversationusecase.ActivePatch) {
	if s == nil || len(patches) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, patch := range patches {
		key := pendingConversationPatchKey(patch)
		current, ok := s.pending[key]
		if ok && conversationActivePatchDominates(patch, current) {
			delete(s.pending, key)
		}
	}
}

func pendingConversationPatchKey(patch conversationusecase.ActivePatch) conversationAuthorityPendingKey {
	return conversationAuthorityPendingKey{uid: patch.UID, channelID: patch.ChannelID, channelType: patch.ChannelType}
}

func sortedPendingConversationPatches(patches map[conversationAuthorityPendingKey]conversationusecase.ActivePatch) []conversationusecase.ActivePatch {
	if len(patches) == 0 {
		return nil
	}
	keys := make([]conversationAuthorityPendingKey, 0, len(patches))
	for key := range patches {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].uid != keys[j].uid {
			return keys[i].uid < keys[j].uid
		}
		if keys[i].channelID != keys[j].channelID {
			return keys[i].channelID < keys[j].channelID
		}
		return keys[i].channelType < keys[j].channelType
	})
	out := make([]conversationusecase.ActivePatch, 0, len(keys))
	for _, key := range keys {
		out = append(out, patches[key])
	}
	return out
}

func conversationActivePatchDominates(admitted, current conversationusecase.ActivePatch) bool {
	if current.UID == "" {
		return true
	}
	return mergeConversationActivePatch(current, admitted) == admitted
}

func conversationActivePatchBatches(patches []conversationusecase.ActivePatch, batchRows int) [][]conversationusecase.ActivePatch {
	if len(patches) == 0 {
		return nil
	}
	if batchRows <= 0 || batchRows >= len(patches) {
		return [][]conversationusecase.ActivePatch{append([]conversationusecase.ActivePatch(nil), patches...)}
	}
	batches := make([][]conversationusecase.ActivePatch, 0, (len(patches)+batchRows-1)/batchRows)
	for start := 0; start < len(patches); start += batchRows {
		end := start + batchRows
		if end > len(patches) {
			end = len(patches)
		}
		batches = append(batches, append([]conversationusecase.ActivePatch(nil), patches[start:end]...))
	}
	return batches
}

func (s *conversationAuthorityCommittedSink) initialAuthorities() []clusterv2.RouteAuthority {
	if s == nil || s.initial == nil {
		return nil
	}
	return s.initial()
}

func (s *conversationAuthorityCommittedSink) watchRouteAuthorities(ctx context.Context, events <-chan clusterv2.RouteAuthorityEvent) {
	defer s.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			for _, authority := range event.Authorities {
				s.handleRouteAuthority(ctx, authority)
			}
		}
	}
}

func (s *conversationAuthorityCommittedSink) applyRouteAuthorities(ctx context.Context, authorities []clusterv2.RouteAuthority) {
	if s == nil || s.localAuthority == nil {
		return
	}
	for _, authority := range authorities {
		s.handleRouteAuthority(ctx, authority)
	}
}

func (s *conversationAuthorityCommittedSink) handleRouteAuthority(ctx context.Context, authority clusterv2.RouteAuthority) {
	if s == nil || s.localAuthority == nil {
		return
	}
	target := conversationRouteTarget(authority)
	previous, hadPrevious, accepted := s.acceptRouteAuthorityTarget(target)
	if !accepted {
		return
	}
	switch {
	case target.LeaderNodeID == s.localNodeID:
		s.localAuthority.markActive(target)
	case target.LeaderNodeID == 0:
		if hadPrevious && s.localAuthorityCapable(previous) {
			s.drainAuthorityTarget(ctx, previous)
		}
		s.localAuthority.markWarming(target)
	default:
		if hadPrevious && s.localAuthorityCapable(previous) {
			s.drainAuthorityTarget(ctx, previous)
		}
	}
}

func (s *conversationAuthorityCommittedSink) acceptRouteAuthorityTarget(target conversationusecase.RouteTarget) (conversationusecase.RouteTarget, bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.latest == nil {
		s.latest = make(map[uint16]conversationusecase.RouteTarget)
	}
	current, ok := s.latest[target.HashSlot]
	if ok && !conversationAuthorityRouteTargetNewer(target, current) {
		return current, true, false
	}
	s.latest[target.HashSlot] = target
	return current, ok, true
}

func conversationAuthorityRouteTargetNewer(next, current conversationusecase.RouteTarget) bool {
	if next.RouteRevision != current.RouteRevision {
		return next.RouteRevision > current.RouteRevision
	}
	return next.AuthorityEpoch > current.AuthorityEpoch
}

func (s *conversationAuthorityCommittedSink) localAuthorityCapable(target conversationusecase.RouteTarget) bool {
	return target.LeaderNodeID == 0 || target.LeaderNodeID == s.localNodeID
}

func (s *conversationAuthorityCommittedSink) drainAuthorityTarget(ctx context.Context, target conversationusecase.RouteTarget) {
	if s == nil || s.localAuthority == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	drainCtx := ctx
	var cancel context.CancelFunc
	if s.handoffTimeout > 0 {
		drainCtx, cancel = context.WithTimeout(ctx, s.handoffTimeout)
		defer cancel()
	}
	_, _ = s.localAuthority.DrainAuthority(drainCtx, target)
}

func conversationRouteTarget(authority clusterv2.RouteAuthority) conversationusecase.RouteTarget {
	return conversationusecase.RouteTarget{
		HashSlot:       authority.HashSlot,
		SlotID:         authority.SlotID,
		LeaderNodeID:   authority.LeaderNodeID,
		RouteRevision:  authority.RouteRevision,
		AuthorityEpoch: authority.AuthorityEpoch,
	}
}

func (s *conversationAuthorityCommittedSink) observeAuthorityAdmit(result string) {
	if s == nil || s.authorityObserver == nil {
		return
	}
	s.authorityObserver.ObserveConversationAuthorityAdmit(conversationAuthorityAdmitEvent{Result: result})
}

type metadataCommittedSink interface {
	SubmitMetadata(context.Context, messageevents.MessageCommitted) error
}

func combineCommittedSinks(sinks ...message.CommittedSink) message.CommittedSink {
	group := committedSinkGroup{}
	for _, sink := range sinks {
		if sink != nil {
			group = append(group, sink)
		}
	}
	if len(group) == 0 {
		return nil
	}
	return group
}

func (g committedSinkGroup) Submit(ctx context.Context, event messageevents.MessageCommitted) error {
	var firstErr error
	for _, sink := range g {
		if sink == nil {
			continue
		}
		if err := submitCommittedEvent(ctx, sink, event); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (g committedSinkGroup) RequiresCommittedPayload() bool {
	for _, sink := range g {
		if sink == nil {
			continue
		}
		policy, ok := sink.(message.CommittedPayloadPolicy)
		if !ok || policy.RequiresCommittedPayload() {
			return true
		}
	}
	return false
}

func submitCommittedEvent(ctx context.Context, sink message.CommittedSink, event messageevents.MessageCommitted) error {
	if metadataSink, ok := sink.(metadataCommittedSink); ok {
		event.Payload = nil
		event.MessageScopedUIDs = nil
		return metadataSink.SubmitMetadata(ctx, event)
	}
	return sink.Submit(ctx, event.Clone())
}
