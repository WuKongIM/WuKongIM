package mqttsession

import (
	"bytes"
	"context"
	"errors"
	"math"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/publication"
)

var (
	// ErrWillPending retains an obligation whose publication is not yet proved.
	ErrWillPending = errors.New("mqttsession: Will publication outcome pending")
	// ErrWillReceiptTargetMissing describes fresh target absence, never nonpublication.
	ErrWillReceiptTargetMissing = errors.New("mqttsession: Will receipt target absent")
	// ErrWillBusy rejects a turn before creating a waiter or retaining its body.
	ErrWillBusy = errors.New("mqttsession: Will execution capacity exhausted")
)

// WillExecutionStore provides foreground Slot reads and committed exact CAS.
// Detached obligations are independent of the current Session generation.
type WillExecutionStore interface {
	ReadMQTT(context.Context, meta.MQTTRead) (meta.MQTTReadResult, error)
	CompareAndSwapMQTTWill(context.Context, uint64, meta.MQTTWill) (meta.MQTTWillResult, error)
}

// WillPublication contains immutable, server-bound metadata v2. Ports may borrow
// the bytes only for the call; any retained asynchronous work must own its copy.
type WillPublication struct {
	UID, ClientMsgNo             string
	Target                       WillTarget
	Payload, PublicationMetadata []byte
	// AppendAdmission must be honored synchronously at the origin router before
	// local admission or remote forwarding; it must never cross a node RPC.
	AppendAdmission func(context.Context) error
}

// WillPublicationReceipt preserves the original committed source append time.
type WillPublicationReceipt struct {
	MessageID, MessageSeq uint64
	PublishedAtMS         int64
}

// WillPublications reuses ordinary publish orchestration and independently reads
// retained proof through fresh authority. Lookup must verify exact sender,
// server key, client number, body and metadata. Absence proves no nonpublication.
type WillPublications interface {
	// PrepareWillPublication runs hooks without publishing and returns owned output.
	PrepareWillPublication(context.Context, WillPublication) ([]byte, error)
	LookupWillPublication(context.Context, WillPublication) (WillPublicationReceipt, bool, error)
	PublishWill(context.Context, WillPublication) error
}

// WillDispatchFence persists admission before calling publication and can seal
// only the captured attempt's non-dispatch. No absence/lease inference is allowed.
type WillDispatchFence interface {
	PrepareAttempt(context.Context, contract.WillAttempt) error
	BeginDispatch(context.Context, contract.WillAttempt) error
	// BeginAppend irreversibly issues exact submission permission. Routing retries
	// reuse that permission; issued attempts cannot grant non-dispatch proof.
	BeginAppend(context.Context, contract.WillAttempt) error
	SealUndispatched(context.Context, contract.WillAttempt) error
	ReleaseAttempt(context.Context, contract.WillAttempt) error
}

type WillExecutionOptions struct {
	// Store rereads obligations and conditionally commits through current Slot authority.
	Store WillExecutionStore
	// Publications preserves server-domain content and independently resolves its receipt.
	Publications WillPublications
	// DispatchFence is required by product wiring; absence retains positive-only recovery.
	DispatchFence WillDispatchFence
	// ReclamationJournal supplies captured local attempts only on capacity pressure.
	// Cleanup requires an independent authoritative row and grants no retry proof.
	ReclamationJournal WillReclamationJournal
	// Authorizer evaluates current policy before resumable preparation/dispatch.
	Authorizer WillAuthorizer
	// NodeID and BootID identify this executor process, never a connection owner.
	NodeID uint64
	BootID string
	// Now must retain monotonic time. The lease starts before proposing its CAS.
	Now func() time.Time
	// LeaseDuration bounds this turn's execution ownership, at most one minute.
	LeaseDuration time.Duration
	// TurnTimeout bounds all dependencies, at most five seconds per turn.
	TurnTimeout time.Duration
}

// WillExecutor performs one bounded preparation, dispatch or recovery turn.
// It owns no worker, scan cursor or persistent body cache.
type WillExecutor struct {
	opts      WillExecutionOptions
	admission chan struct{}
	// active pins at most four body-free keys, preventing equal local reservations.
	mu     sync.Mutex
	active map[meta.MQTTWillKey]struct{}
	// reclamation admits one pressure page without queuing another executor turn.
	reclamation chan struct{}
}

type WillExecutionResult struct {
	Stage   meta.MQTTWillStage
	Pending bool
	Receipt WillPublicationReceipt
}

func NewWillExecutor(o WillExecutionOptions) (*WillExecutor, error) {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Store == nil || o.Publications == nil || o.Authorizer == nil || o.ReclamationJournal != nil && o.DispatchFence == nil || o.NodeID == 0 || o.BootID == "" || len(o.BootID) > 128 || !utf8.ValidString(o.BootID) || strings.ContainsRune(o.BootID, 0) || o.LeaseDuration < time.Millisecond || o.LeaseDuration > time.Minute || o.LeaseDuration%time.Millisecond != 0 || o.TurnTimeout <= 0 || o.TurnTimeout > 5*time.Second {
		return nil, ErrInvalid
	}
	e := &WillExecutor{opts: o, admission: make(chan struct{}, 4), active: make(map[meta.MQTTWillKey]struct{}, 4), reclamation: make(chan struct{}, 1)}
	if _, err := e.now(); err != nil {
		return nil, err
	}
	return e, nil
}

func (e *WillExecutor) now() (time.Time, error) {
	t := e.opts.Now()
	if t == t.Round(0) || t.UnixMilli() <= 0 || t.UnixMilli() > math.MaxInt64-int64(time.Minute/time.Millisecond)-1 {
		return time.Time{}, ErrClock
	}
	return t, nil
}

// Execute rereads an exact key and persists hook output before dispatch. Only a
// definite Started transition may publish. Existing Started work also needs an
// exact sealed non-dispatch; legacy work remains positive-receipt-only.
func (e *WillExecutor) Execute(parent context.Context, key meta.MQTTWillKey) (out WillExecutionResult, err error) {
	if e == nil || parent == nil || meta.ValidateMQTTRead(meta.MQTTRead{Kind: meta.MQTTReadWill, WillKey: key}) != nil {
		return out, ErrInvalid
	}
	if err = parent.Err(); err != nil {
		return out, err
	}
	select {
	case e.admission <- struct{}{}:
		defer func() { <-e.admission }()
	default:
		return out, ErrWillBusy
	}
	if e.opts.DispatchFence != nil {
		e.mu.Lock()
		_, busy := e.active[key]
		if !busy {
			e.active[key] = struct{}{}
		}
		e.mu.Unlock()
		if busy {
			return out, ErrWillBusy
		}
		defer func() { e.mu.Lock(); delete(e.active, key); e.mu.Unlock() }()
	}
	ctx, cancel := context.WithTimeout(parent, e.opts.TurnTimeout)
	defer cancel()
	r, err := e.opts.Store.ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadWill, WillKey: key})
	if err != nil {
		return out, err
	}
	if len(r.Wills) == 0 {
		return out, ErrFenced
	}
	if len(r.Wills) != 1 || r.Wills[0].Key != key || meta.ValidateMQTTWill(r.Wills[0]) != nil {
		return out, ErrEvidence
	}
	w := r.Wills[0]
	out = willExecutionResult(w)
	if !out.Pending {
		return out, nil
	}
	if w.Stage != meta.MQTTWillReady && w.Stage != meta.MQTTWillExecuting {
		return out, ErrWillPending
	}
	q, err := willPublication(w)
	if err != nil {
		return out, err
	}
	started, err := e.now()
	if err != nil {
		return out, err
	}
	if started.UnixMilli() < w.UpdatedAtMS {
		return out, ErrClock
	}
	if w.Stage == meta.MQTTWillExecuting && started.UnixMilli() < w.LeaseUntilMS {
		return out, ErrWillPending
	}
	if w.Revision > math.MaxUint64-4 || w.ExecutionGeneration == math.MaxUint64 {
		return out, ErrEvidence
	}
	previous := willAttempt(w)
	var recoveredReceipt WillPublicationReceipt
	resumeDispatch := false
	if e.opts.DispatchFence != nil && w.Stage == meta.MQTTWillExecuting && (w.DispatchStage == meta.MQTTWillDispatchStarted || w.DispatchStage == meta.MQTTWillDispatchLegacy) {
		receipt, found, lookupErr := e.opts.Publications.LookupWillPublication(ctx, q)
		if lookupErr != nil && !errors.Is(lookupErr, ErrWillReceiptTargetMissing) {
			return out, lookupErr
		}
		if found {
			if lookupErr != nil || !validWillReceipt(receipt) {
				return out, ErrEvidence
			}
			recoveredReceipt = receipt
		} else {
			if receipt != (WillPublicationReceipt{}) {
				return out, ErrEvidence
			}
			if w.DispatchStage != meta.MQTTWillDispatchStarted {
				return out, ErrWillPending
			}
			if sealErr := e.opts.DispatchFence.SealUndispatched(ctx, previous); sealErr != nil {
				// Preserve the original attempt tuple until proof exists; claiming on
				// absence would erase the only identity its owning node can verify.
				return out, errors.Join(ErrWillPending, sealErr)
			}
			if err := e.opts.Authorizer.AuthorizeWill(ctx, w.UID, q.Target); err != nil {
				if errors.Is(err, ErrWillDenied) {
					return e.rejectSealed(ctx, w, started)
				}
				return out, errors.Join(ErrWillPending, err)
			}
			resumeDispatch = true
		}
	}
	first := w.Stage == meta.MQTTWillReady
	next := w
	next.Stage = meta.MQTTWillExecuting
	if first {
		next.DispatchStage = meta.MQTTWillDispatchPreparing
	}
	next.ExecutionGeneration++
	next.ExecutorNodeID, next.ExecutorBootID = e.opts.NodeID, e.opts.BootID
	next.Revision++
	next.UpdatedAtMS = started.UnixMilli()
	until := started.Add(e.opts.LeaseDuration)
	next.LeaseUntilMS = leaseUpperBoundMS(until)
	leaseCtx, leaseCancel := context.WithDeadline(ctx, until)
	defer leaseCancel()
	ctx = leaseCtx
	if err = ctx.Err(); err != nil {
		return out, err
	}
	if resumeDispatch {
		if err := e.prepareAttempt(ctx, willAttempt(next)); err != nil {
			return out, err
		}
	}
	claimed, err := e.opts.Store.CompareAndSwapMQTTWill(ctx, w.Revision, next)
	if err != nil {
		return out, err
	}
	if err = willExecutionCAS(claimed, next.Revision); err != nil {
		// An equal local tuple may have been reserved by an earlier unknown
		// claim that applies late. Even this call's definite conflict cannot
		// discard that attempt's recovery evidence.
		return out, err
	}
	if claimed.Status != meta.MQTTSessionCASApplied {
		return out, ErrWillPending
	}
	if e.opts.DispatchFence != nil && previous.Validate() == nil {
		// The definite successor claim consumes sealed proof or positively known
		// publication, or fences an old pre-Started reservation. Cleanup is exact.
		_ = e.opts.DispatchFence.ReleaseAttempt(ctx, previous)
	}
	w = next
	out = willExecutionResult(w)
	last := started
	check := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		now, err := e.now()
		if err != nil {
			return err
		}
		if now.Before(last) || now.UnixMilli() < last.UnixMilli() {
			return ErrClock
		}
		if !now.Before(until) || now.UnixMilli() >= w.LeaseUntilMS {
			return ErrFenced
		}
		last = now
		return nil
	}
	finish := func(stage meta.MQTTWillStage, receipt WillPublicationReceipt) (WillExecutionResult, error) {
		if err := check(); err != nil {
			return out, err
		}
		next := w
		next.Stage, next.Revision, next.UpdatedAtMS = stage, w.Revision+1, last.UnixMilli()
		next.LeaseUntilMS = 0
		if stage == meta.MQTTWillPublished {
			next.MessageID, next.MessageSeq, next.PublishedAtMS = receipt.MessageID, receipt.MessageSeq, receipt.PublishedAtMS
		} else {
			next.RejectReason = meta.MQTTWillPermissionRevoked
		}
		r, err := e.opts.Store.CompareAndSwapMQTTWill(ctx, w.Revision, next)
		if err != nil {
			return out, err
		}
		if err = willExecutionCAS(r, next.Revision); err != nil {
			return out, err
		}
		if e.opts.DispatchFence != nil {
			_ = e.opts.DispatchFence.ReleaseAttempt(ctx, willAttempt(w))
		}
		return willExecutionResult(next), nil
	}
	if err = check(); err != nil {
		return out, err
	}
	if recoveredReceipt != (WillPublicationReceipt{}) {
		return finish(meta.MQTTWillPublished, recoveredReceipt)
	}
	var publishErr error
	if w.DispatchStage == meta.MQTTWillDispatchPreparing || w.DispatchStage == meta.MQTTWillDispatchPrepared {
		if err = e.opts.Authorizer.AuthorizeWill(ctx, w.UID, q.Target); err != nil {
			if errors.Is(err, ErrWillDenied) {
				return finish(meta.MQTTWillRejected, WillPublicationReceipt{})
			}
			return out, err
		}
		if err = check(); err != nil {
			return out, err
		}
		phase := func(stage meta.MQTTWillDispatchStage, payload []byte) error {
			if err := check(); err != nil {
				return err
			}
			next := w
			next.Revision++
			next.UpdatedAtMS = last.UnixMilli()
			next.DispatchStage, next.DispatchPayload = stage, bytes.Clone(payload)
			r, err := e.opts.Store.CompareAndSwapMQTTWill(ctx, w.Revision, next)
			if err != nil {
				return err
			}
			if err := willExecutionCAS(r, next.Revision); err != nil {
				return err
			}
			if r.Status != meta.MQTTSessionCASApplied {
				return ErrWillPending
			}
			w = next
			q.Payload = bytes.Clone(payload)
			return nil
		}
		if w.DispatchStage == meta.MQTTWillDispatchPreparing {
			body, err := e.opts.Publications.PrepareWillPublication(ctx, q)
			if err != nil {
				if errors.Is(err, ErrWillDenied) {
					return finish(meta.MQTTWillRejected, WillPublicationReceipt{})
				}
				return out, err
			}
			if len(body) > 65535 {
				return out, ErrEvidence
			}
			if err := phase(meta.MQTTWillDispatchPrepared, body); err != nil {
				return out, err
			}
		}
		if e.opts.DispatchFence != nil {
			if err := e.prepareAttempt(ctx, willAttempt(w)); err != nil {
				return out, err
			}
		}
		if err := phase(meta.MQTTWillDispatchStarted, w.DispatchPayload); err != nil {
			return out, err
		}
		// gofail: var wkMQTTWillAfterStarted bool
		// if wkMQTTWillAfterStarted {
		//     return out, context.DeadlineExceeded
		// }
		resumeDispatch = true
	}
	if resumeDispatch {
		if err := check(); err != nil {
			return out, err
		}
		if e.opts.DispatchFence != nil {
			if err := e.opts.DispatchFence.BeginDispatch(ctx, willAttempt(w)); err != nil {
				return out, err
			}
		}
		// gofail: var wkMQTTWillAfterAdmitted bool
		// if wkMQTTWillAfterAdmitted {
		//     return out, context.DeadlineExceeded
		// }
		if err := check(); err != nil {
			return out, err
		}
		if e.opts.DispatchFence != nil {
			attempt := willAttempt(w)
			q.AppendAdmission = func(call context.Context) error {
				if err := check(); err != nil {
					return err
				}
				return e.opts.DispatchFence.BeginAppend(call, attempt)
			}
		}
		publishErr = e.opts.Publications.PublishWill(ctx, q)
		q.AppendAdmission = nil
		// gofail: var wkMQTTWillAfterPublication bool
		// if wkMQTTWillAfterPublication {
		//     return out, context.DeadlineExceeded
		// }

		// Even an error may follow a committed append; resolve positive evidence
		// before preserving the original error for the next recovery turn.
	}
	if err = check(); err != nil {
		return out, err
	}
	receipt, found, err := e.opts.Publications.LookupWillPublication(ctx, q)
	if err != nil {
		return out, errors.Join(publishErr, err)
	}
	if !found {
		if receipt != (WillPublicationReceipt{}) {
			return out, ErrEvidence
		}
		return out, errors.Join(publishErr, ErrWillPending)
	}
	if !validWillReceipt(receipt) {
		return out, ErrEvidence
	}
	return finish(meta.MQTTWillPublished, receipt)
}

// rejectSealed consumes the exact durable non-dispatch proof and current denial
// without reserving a successor. Unknown/late writes retain the captured journal.
func (e *WillExecutor) rejectSealed(ctx context.Context, w meta.MQTTWill, observed time.Time) (WillExecutionResult, error) {
	out := willExecutionResult(w)
	now, err := e.now()
	if err != nil {
		return out, err
	}
	if now.Before(observed) || now.UnixMilli() < observed.UnixMilli() {
		return out, ErrClock
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	next := w
	next.Stage, next.DispatchStage, next.RejectReason = meta.MQTTWillRejected, meta.MQTTWillDispatchSealed, meta.MQTTWillPermissionRevoked
	next.Revision, next.UpdatedAtMS, next.LeaseUntilMS = w.Revision+1, now.UnixMilli(), 0
	r, err := e.opts.Store.CompareAndSwapMQTTWill(ctx, w.Revision, next)
	if err != nil {
		return out, err
	}
	if err := willExecutionCAS(r, next.Revision); err != nil {
		return out, err
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	_ = e.opts.DispatchFence.ReleaseAttempt(ctx, willAttempt(w))
	return willExecutionResult(next), nil
}

func willExecutionCAS(r meta.MQTTWillResult, revision uint64) error {
	if r.Status == meta.MQTTSessionCASConflict {
		return ErrConflict
	}
	if r.Status != meta.MQTTSessionCASApplied && r.Status != meta.MQTTSessionCASUnchanged || r.CurrentRevision != revision {
		return ErrEvidence
	}
	return nil
}

func willExecutionResult(w meta.MQTTWill) WillExecutionResult {
	r := WillExecutionResult{Stage: w.Stage, Pending: w.Stage != meta.MQTTWillPublished && w.Stage != meta.MQTTWillCancelled && w.Stage != meta.MQTTWillRejected}
	if w.Stage == meta.MQTTWillPublished {
		r.Receipt = WillPublicationReceipt{MessageID: w.MessageID, MessageSeq: w.MessageSeq, PublishedAtMS: w.PublishedAtMS}
	}
	return r
}

func willPublication(w meta.MQTTWill) (WillPublication, error) {
	md, err := publication.Decode(w.PublicationMetadata)
	if err != nil || md.Source != publication.SourceWill || md.QoS != w.QoS || md.AcceptedAtMS != 0 || md.ServerWillKey != "" || md.PublisherNamespace != w.Key.Namespace || md.PublisherClientID != w.Key.ClientID || md.OriginalTopic != w.Topic {
		return WillPublication{}, ErrEvidence
	}
	md.ServerWillKey = w.IdempotencyKey
	encoded, err := publication.Encode(md)
	if err != nil {
		return WillPublication{}, ErrEvidence
	}
	body := w.Payload
	if w.DispatchStage >= meta.MQTTWillDispatchPrepared {
		body = w.DispatchPayload
	}
	return WillPublication{UID: w.UID, ClientMsgNo: w.ClientMsgNo, Target: WillTarget{Topic: w.Topic, TargetID: w.TargetID, TargetType: w.TargetType}, Payload: bytes.Clone(body), PublicationMetadata: encoded}, nil
}

func willAttempt(w meta.MQTTWill) contract.WillAttempt {
	return contract.WillAttempt{Key: contract.Key{Namespace: w.Key.Namespace, ClientID: w.Key.ClientID}, SessionGeneration: w.Key.SessionGeneration, WillGeneration: w.Key.WillGeneration, NodeID: w.ExecutorNodeID, BootID: w.ExecutorBootID, ExecutionGeneration: w.ExecutionGeneration}
}

func validWillReceipt(r WillPublicationReceipt) bool {
	return r.MessageID != 0 && r.MessageSeq != 0 && r.PublishedAtMS > 0
}
