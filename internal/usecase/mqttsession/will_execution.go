package mqttsession

import (
	"bytes"
	"context"
	"errors"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/publication"
)

var (
	// ErrWillPending retains an obligation whose publication is not yet proved.
	ErrWillPending = errors.New("mqttsession: Will publication outcome pending")
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
	LookupWillPublication(context.Context, WillPublication) (WillPublicationReceipt, bool, error)
	PublishWill(context.Context, WillPublication) error
}

type WillExecutionOptions struct {
	// Store rereads obligations and conditionally commits through current Slot authority.
	Store WillExecutionStore
	// Publications preserves server-domain content and independently resolves its receipt.
	Publications WillPublications
	// Authorizer evaluates current policy for first dispatch, independently of setup.
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

// WillExecutor performs one bounded first-dispatch or positive-recovery turn.
// It owns no worker, scan cursor or persistent body cache.
type WillExecutor struct {
	opts      WillExecutionOptions
	admission chan struct{}
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
	if o.Store == nil || o.Publications == nil || o.Authorizer == nil || o.NodeID == 0 || o.BootID == "" || len(o.BootID) > 128 || !utf8.ValidString(o.BootID) || strings.ContainsRune(o.BootID, 0) || o.LeaseDuration < time.Millisecond || o.LeaseDuration > time.Minute || o.LeaseDuration%time.Millisecond != 0 || o.TurnTimeout <= 0 || o.TurnTimeout > 5*time.Second {
		return nil, ErrInvalid
	}
	e := &WillExecutor{opts: o, admission: make(chan struct{}, 4)}
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

// Execute rereads an exact key. Only a definite first Ready claim may publish;
// a previous Executing attempt can finish from positive proof but cannot safely
// redispatch or reject on absence, lease expiry or a later permission change.
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
	if w.Revision >= math.MaxUint64-1 || w.ExecutionGeneration == math.MaxUint64 {
		return out, ErrEvidence
	}
	first := w.Stage == meta.MQTTWillReady
	next := w
	next.Stage = meta.MQTTWillExecuting
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
	claimed, err := e.opts.Store.CompareAndSwapMQTTWill(ctx, w.Revision, next)
	if err != nil {
		return out, err
	}
	if err = willExecutionCAS(claimed, next.Revision); err != nil {
		return out, err
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
		return willExecutionResult(next), nil
	}
	if err = check(); err != nil {
		return out, err
	}
	var publishErr error
	if first && claimed.Status == meta.MQTTSessionCASApplied {
		if err = e.opts.Authorizer.AuthorizeWill(ctx, w.UID, q.Target); err != nil {
			if errors.Is(err, ErrWillDenied) {
				return finish(meta.MQTTWillRejected, WillPublicationReceipt{})
			}
			return out, err
		}
		if err = check(); err != nil {
			return out, err
		}
		publishErr = e.opts.Publications.PublishWill(ctx, q)
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
	if receipt.MessageID == 0 || receipt.MessageSeq == 0 || receipt.PublishedAtMS <= 0 {
		return out, ErrEvidence
	}
	return finish(meta.MQTTWillPublished, receipt)
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
	return WillPublication{UID: w.UID, ClientMsgNo: w.ClientMsgNo, Target: WillTarget{Topic: w.Topic, TargetID: w.TargetID, TargetType: w.TargetType}, Payload: bytes.Clone(w.Payload), PublicationMetadata: encoded}, nil
}
