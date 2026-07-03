package runtime

import (
	"context"
	"time"

	core "github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

type fetchLongPollContextKey struct{}

// WithoutFetchLongPoll marks a fetch request context so ServeFetch returns
// immediately on empty results instead of parking the RPC in long-poll mode.
func WithoutFetchLongPoll(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, fetchLongPollContextKey{}, false)
}

// FetchLongPollEnabled reports whether ServeFetch should wait for replica state
// changes before returning an empty fetch response.
func FetchLongPollEnabled(ctx context.Context) bool {
	if ctx == nil {
		return true
	}
	enabled, ok := ctx.Value(fetchLongPollContextKey{}).(bool)
	if !ok {
		return true
	}
	return enabled
}

type followerCursorApplier interface {
	ApplyFollowerCursor(ctx context.Context, req core.ReplicaFollowerCursorUpdate) error
}

func (r *runtime) ServeLanePoll(ctx context.Context, req LanePollRequestEnvelope) (LanePollResponseEnvelope, error) {
	if !r.longPollEnabled() {
		return LanePollResponseEnvelope{Status: LanePollStatusClosed}, nil
	}
	if req.LaneCount != uint16(r.cfg.LongPollLaneCount) {
		return LanePollResponseEnvelope{
			Status:        LanePollStatusNeedReset,
			ResetRequired: true,
			ResetReason:   LanePollResetReasonLaneLayoutMismatch,
		}, nil
	}

	sessionKey := PeerLaneKey{Peer: req.ReplicaID, LaneID: req.LaneID}
	session, ok := r.leaderLanes.Session(sessionKey)
	if req.Op == LanePollOpOpen || !ok {
		session = newLeaderLaneSession(r.requestID.Add(1), 1)
		for _, member := range req.FullMembership {
			ch, _, err := r.ensureChannelForIngress(ctx, member.ChannelKey, ActivationSourceLaneOpen)
			if err != nil {
				continue
			}
			meta := ch.metaSnapshot()
			if meta.Leader != r.cfg.LocalNode || meta.Epoch != member.ChannelEpoch {
				continue
			}
			session.TrackChannel(member.ChannelKey, member.ChannelEpoch, member.ChannelGeneration)
			if ch.Status().LEO > 0 {
				session.MarkDataReady(member.ChannelKey, member.ChannelEpoch)
			}
		}
		r.leaderLanes.RegisterSession(sessionKey, session)
	} else if req.SessionID != 0 && req.SessionEpoch != 0 {
		currentID, currentEpoch := session.Session()
		if req.SessionID != currentID || req.SessionEpoch != currentEpoch {
			return LanePollResponseEnvelope{
				LaneID:        req.LaneID,
				Status:        LanePollStatusNeedReset,
				ResetRequired: true,
				ResetReason:   LanePollResetReasonSessionEpochMismatch,
				SessionID:     currentID,
				SessionEpoch:  currentEpoch,
			}, nil
		}
	}

	budget := LanePollBudget{MaxBytes: req.MaxBytes, MaxChannels: req.MaxChannels}
	if budget.MaxBytes <= 0 {
		budget.MaxBytes = r.cfg.LongPollMaxBytes
	}
	if budget.MaxChannels <= 0 {
		budget.MaxChannels = r.cfg.LongPollMaxChannels
	}

	selectItems := func() (LeaderLanePollResult, *lanePollWaiter) {
		for _, delta := range req.CursorDelta {
			if r.cursorNeedsRetentionReady(delta) {
				session.MarkDataReady(delta.ChannelKey, delta.ChannelEpoch)
			}
		}
		return session.Poll(req.CursorDelta, func(delta LaneCursorDelta) {
			r.applyFollowerCursor(delta, req.ReplicaID)
		}, budget, func(key core.ChannelKey, cursor LaneCursorDelta, mask laneReadyMask) (LeaderLaneReadyItem, bool) {
			return r.selectLaneReadyItem(ctx, cursor, key, req.ReplicaID, budget.MaxBytes, mask)
		})
	}

	result, waiter := selectItems()
	if waiter == nil {
		sessionID, sessionEpoch := session.Session()
		resp := LanePollResponseEnvelope{
			LaneID:       req.LaneID,
			Status:       LanePollStatusOK,
			SessionID:    sessionID,
			SessionEpoch: sessionEpoch,
			MoreReady:    result.MoreReady,
			Items:        make([]LaneResponseItem, len(result.Items)),
		}
		for i, item := range result.Items {
			resp.Items[i] = itemToLaneResponse(item)
		}
		r.cfg.Logger.Debug("lane poll returning items immediately",
			wklog.Event("repl.diag.lane_poll_immediate"),
			wklog.Uint64("replicaID", uint64(req.ReplicaID)),
			wklog.Int("laneID", int(req.LaneID)),
			wklog.Int("items", len(resp.Items)),
		)
		return resp, nil
	}

	maxWait := req.MaxWait
	if maxWait <= 0 {
		maxWait = r.cfg.LongPollMaxWait
	}
	timer := time.NewTimer(maxWait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		r.cfg.Logger.Warn("lane poll context canceled while parked",
			wklog.Event("repl.diag.lane_poll_ctx_canceled"),
			wklog.Uint64("replicaID", uint64(req.ReplicaID)),
			wklog.Int("laneID", int(req.LaneID)),
			wklog.Error(ctx.Err()),
		)
		return LanePollResponseEnvelope{}, ctx.Err()
	case <-waiter.Ready():
		result, _ = selectItems()
	case <-timer.C:
		// r.cfg.Logger.Debug("lane poll timed out with no data",
		// 	wklog.Event("repl.diag.lane_poll_timeout"),
		// 	wklog.Uint64("replicaID", uint64(req.ReplicaID)),
		// 	wklog.Int("laneID", int(req.LaneID)),
		// )
		sessionID, sessionEpoch := session.Session()
		return LanePollResponseEnvelope{
			LaneID:       req.LaneID,
			Status:       LanePollStatusOK,
			SessionID:    sessionID,
			SessionEpoch: sessionEpoch,
			TimedOut:     true,
		}, nil
	}

	sessionID, sessionEpoch := session.Session()
	resp := LanePollResponseEnvelope{
		LaneID:       req.LaneID,
		Status:       LanePollStatusOK,
		SessionID:    sessionID,
		SessionEpoch: sessionEpoch,
		MoreReady:    result.MoreReady,
		Items:        make([]LaneResponseItem, len(result.Items)),
	}
	for i, item := range result.Items {
		resp.Items[i] = itemToLaneResponse(item)
	}
	r.cfg.Logger.Debug("lane poll woke and returning items",
		wklog.Event("repl.diag.lane_poll_woke"),
		wklog.Uint64("replicaID", uint64(req.ReplicaID)),
		wklog.Int("laneID", int(req.LaneID)),
		wklog.Int("items", len(resp.Items)),
	)
	return resp, nil
}

func (r *runtime) selectLaneReadyItem(ctx context.Context, cursor LaneCursorDelta, key core.ChannelKey, replicaID core.NodeID, maxBytes int, mask laneReadyMask) (LeaderLaneReadyItem, bool) {
	ch, ok := r.lookupChannel(key)
	if !ok {
		return LeaderLaneReadyItem{
			ChannelKey:        key,
			ChannelEpoch:      cursor.ChannelEpoch,
			ChannelGeneration: cursor.ChannelGeneration,
		}, true
	}
	state := ch.Status()
	if canServeHWOnlyLaneReadyWithoutFetch(mask, cursor, state) {
		return LeaderLaneReadyItem{
			ChannelKey:        key,
			ChannelEpoch:      state.Epoch,
			ChannelGeneration: cursor.ChannelGeneration,
			ReadyMask:         mask,
			Response: LaneResponseItem{
				ChannelKey:        key,
				ChannelEpoch:      state.Epoch,
				ChannelGeneration: cursor.ChannelGeneration,
				LeaderEpoch:       state.Epoch,
				Flags:             LanePollItemFlagHWOnly,
				LeaderHW:          visibleLanePollHW(state),
			},
		}, true
	}
	fetchResult, err := ch.replica.Fetch(ctx, core.ReplicaFetchRequest{
		ChannelKey:  key,
		Epoch:       state.Epoch,
		ReplicaID:   replicaID,
		FetchOffset: cursor.MatchOffset,
		OffsetEpoch: cursor.OffsetEpoch,
		MaxBytes:    maxBytes,
	})
	if err != nil {
		return LeaderLaneReadyItem{
			ChannelKey:        key,
			ChannelEpoch:      state.Epoch,
			ChannelGeneration: cursor.ChannelGeneration,
			ReadyMask:         mask,
			SizeBytes:         0,
		}, true
	}

	var flags LanePollItemFlags
	if fetchResult.RetentionReset != nil {
		flags |= LanePollItemFlagReset
	}
	if len(fetchResult.Records) > 0 {
		flags |= LanePollItemFlagData
	}
	if fetchResult.TruncateTo != nil {
		flags |= LanePollItemFlagTruncate
	}
	if flags == 0 {
		flags = LanePollItemFlagHWOnly
	}
	item := LeaderLaneReadyItem{
		ChannelKey:        key,
		ChannelEpoch:      state.Epoch,
		ChannelGeneration: cursor.ChannelGeneration,
		ReadyMask:         mask,
		SizeBytes:         laneRecordsSize(fetchResult.Records),
		Response: LaneResponseItem{
			ChannelKey:        key,
			ChannelEpoch:      state.Epoch,
			ChannelGeneration: cursor.ChannelGeneration,
			LeaderEpoch:       state.Epoch,
			Flags:             flags,
			Records:           fetchResult.Records,
			LeaderHW:          fetchResult.HW,
			TruncateTo:        fetchResult.TruncateTo,
			RetentionReset:    fetchResult.RetentionReset,
		},
	}

	finished := len(fetchResult.Records) == 0 || fetchResult.RetentionReset != nil
	if len(fetchResult.Records) > 0 && cursor.MatchOffset+uint64(len(fetchResult.Records)) >= state.LEO {
		finished = true
	}
	return item, finished
}

func canServeHWOnlyLaneReadyWithoutFetch(mask laneReadyMask, cursor LaneCursorDelta, state core.ReplicaState) bool {
	if mask != laneReadyHWOnly {
		return false
	}
	// A pure HW wake only needs a fetch when the follower cursor could still
	// require records, truncation, or retention repair.
	return cursor.MatchOffset == state.LEO && cursor.OffsetEpoch == state.OffsetEpoch
}

func visibleLanePollHW(state core.ReplicaState) uint64 {
	if state.CommitReady {
		return state.HW
	}
	if state.CheckpointHW < state.HW {
		return state.CheckpointHW
	}
	return state.HW
}

func itemToLaneResponse(item LeaderLaneReadyItem) LaneResponseItem {
	if item.Response.ChannelKey != "" {
		return item.Response
	}
	return LaneResponseItem{
		ChannelKey:        item.ChannelKey,
		ChannelEpoch:      item.ChannelEpoch,
		ChannelGeneration: item.ChannelGeneration,
		Flags:             LanePollItemFlagHWOnly,
	}
}

func (r *runtime) cursorNeedsRetentionReady(delta LaneCursorDelta) bool {
	ch, ok := r.lookupChannel(delta.ChannelKey)
	if !ok {
		return false
	}
	state := ch.Status()
	if state.Epoch != delta.ChannelEpoch {
		return false
	}
	if state.RetentionThroughSeq == 0 || state.RetentionThroughSeq < state.LogStartOffset {
		return false
	}
	retainedThrough := state.RetentionThroughSeq
	if state.LogStartOffset > retainedThrough {
		retainedThrough = state.LogStartOffset
	}
	if delta.MatchOffset < retainedThrough {
		return true
	}
	return delta.MatchOffset == retainedThrough && delta.MatchOffset < state.LEO
}

func laneRecordsSize(records []core.Record) int {
	total := 0
	for _, record := range records {
		total += record.SizeBytes
	}
	return total
}

func (r *runtime) applyFollowerCursor(delta LaneCursorDelta, replicaID core.NodeID) {
	ch, ok := r.lookupChannel(delta.ChannelKey)
	if !ok {
		return
	}
	state := ch.Status()
	if state.Epoch != delta.ChannelEpoch {
		return
	}
	if applier, ok := ch.replica.(followerCursorApplier); ok {
		_ = applier.ApplyFollowerCursor(context.Background(), core.ReplicaFollowerCursorUpdate{
			ChannelKey:  delta.ChannelKey,
			Epoch:       delta.ChannelEpoch,
			ReplicaID:   replicaID,
			MatchOffset: delta.MatchOffset,
			OffsetEpoch: delta.OffsetEpoch,
		})
		return
	}
	_ = ch.replica.ApplyProgressAck(context.Background(), core.ReplicaProgressAckRequest{
		ChannelKey:  delta.ChannelKey,
		Epoch:       delta.ChannelEpoch,
		ReplicaID:   replicaID,
		MatchOffset: delta.MatchOffset,
	})
}
