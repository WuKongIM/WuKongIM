package mqttsession

import (
	"context"
	"errors"
	"time"

	appendcontract "github.com/WuKongIM/WuKongIM/internal/contracts/channelappend"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// ErrInboxAdmissionPending retains resumable preparation without admitting a write.
var ErrInboxAdmissionPending = errors.New("mqttsession: inbox admission pending")

// InboxAppendDirectory admits durable native projection work and wakes its owner.
// A successful admission is not directory completion evidence.
type InboxAppendDirectory interface {
	Admit(context.Context, SourceChannel) error
}

// InboxAppendNext preserves the ordinary durable appender's receipt semantics.
type InboxAppendNext interface {
	AppendBatch(context.Context, appendcontract.AppendBatchRequest) (appendcontract.AppendBatchResult, error)
}

type InboxAppenderOptions struct {
	Admission *InboxAdmission
	Directory InboxAppendDirectory
	Next      InboxAppendNext
	// Attempts bounds preparation turns per batch; default 64, maximum 256.
	Attempts int
	// Retry delays unchanged pending work; default 25ms, range 1ms through 1s.
	Retry time.Duration
	// Timeout bounds preparation and append together; default 5s, maximum 1m.
	Timeout time.Duration
}

// InboxAppender prepares ordinary persistent person messages at the shared
// durable boundary. It owns no worker, queue or connection execution scope.
type InboxAppender struct{ options InboxAppenderOptions }

func NewInboxAppender(o InboxAppenderOptions) (*InboxAppender, error) {
	if o.Attempts == 0 {
		o.Attempts = 64
	}
	if o.Retry == 0 {
		o.Retry = 25 * time.Millisecond
	}
	if o.Timeout == 0 {
		o.Timeout = 5 * time.Second
	}
	if o.Admission == nil || o.Directory == nil || o.Next == nil || o.Attempts < 1 || o.Attempts > 256 || o.Retry < time.Millisecond || o.Retry > time.Second || o.Timeout <= 0 || o.Timeout > time.Minute {
		return nil, ErrInvalid
	}
	return &InboxAppender{options: o}, nil
}

// AppendBatch keeps borrowed messages immutable and binds the final preparation
// snapshot to the actual quorum append. Once append starts, cancellation cannot
// replace its receipt; possible writes use ordinary idempotency recovery.
func (p *InboxAppender) AppendBatch(parent context.Context, q appendcontract.AppendBatchRequest) (out appendcontract.AppendBatchResult, err error) {
	if p == nil || parent == nil || len(q.Messages) == 0 {
		return out, appendcontract.ErrInvalidCommand
	}
	appending := false
	defer func() {
		if recover() != nil {
			out = appendcontract.AppendBatchResult{}
			if appending {
				err = appendcontract.ErrAppendFailed
			} else {
				err = errors.Join(appendcontract.ErrRouteNotReady, ErrSubscriptionCallback)
			}
		}
	}()
	if err = parent.Err(); err != nil {
		return out, err
	}
	ordinary, command := false, false
	for _, m := range q.Messages {
		ordinary = ordinary || !m.SyncOnce
		command = command || m.SyncOnce
	}
	if q.ChannelID.Type != 1 || !ordinary {
		appending = true
		return p.options.Next.AppendBatch(parent, q)
	}
	if command || q.ExpectedEpoch == 0 || q.ExpectedLeaderEpoch == 0 || (q.CommitMode != 0 && q.CommitMode != appendcontract.CommitModeQuorum) || meta.ValidateMQTTRead(meta.MQTTRead{Kind: meta.MQTTReadInboxAdmission, AdmissionChannel: q.ChannelID.ID}) != nil {
		return out, appendcontract.ErrInvalidCommand
	}
	ctx, cancel := context.WithTimeout(parent, p.options.Timeout)
	defer cancel()
	progress, err := p.prepare(ctx, SourceChannel{ID: q.ChannelID.ID, Type: 1})
	if err != nil {
		return out, errors.Join(appendcontract.ErrRouteNotReady, err)
	}
	view, err := p.options.Admission.current(ctx, q.ChannelID.ID)
	if err != nil {
		return out, errors.Join(appendcontract.ErrRouteNotReady, err)
	}
	if !inboxDirectoryReady(view) || view.Checkpoint == nil || *view.Checkpoint != progress.Checkpoint || view.Checkpoint.Participant != 2 || view.Checkpoint.DirectoryGeneration != view.Runtime.DirectoryGeneration {
		return out, appendcontract.ErrStaleRoute
	}
	runtime := view.Runtime
	if runtime.ChannelEpoch != q.ExpectedEpoch || runtime.LeaderEpoch != q.ExpectedLeaderEpoch || runtime.RouteGeneration == 0 || (q.ExpectedRouteGeneration != 0 && runtime.RouteGeneration != q.ExpectedRouteGeneration) {
		return out, appendcontract.ErrStaleRoute
	}
	q.ExpectedRouteGeneration = runtime.RouteGeneration
	if err = ctx.Err(); err != nil {
		return out, err
	}
	appending = true
	return p.options.Next.AppendBatch(ctx, q)
}

func (p *InboxAppender) prepare(ctx context.Context, ch SourceChannel) (InboxAdmissionProgress, error) {
	admitted := false
	previous := meta.MQTTInboxAdmission{}
	for turn := 0; turn < p.options.Attempts; turn++ {
		progress, err := p.options.Admission.Advance(ctx, ch)
		if err != nil || progress.Ready {
			return progress, err
		}
		if progress.Checkpoint.Revision == 0 && !admitted {
			if err := ctx.Err(); err != nil {
				return InboxAdmissionProgress{}, err
			}
			if err := p.options.Directory.Admit(ctx, ch); err != nil {
				return InboxAdmissionProgress{}, err
			}
			admitted = true
		}
		if err := ctx.Err(); err != nil {
			return InboxAdmissionProgress{}, err
		}
		if progress.Checkpoint == previous && turn+1 < p.options.Attempts {
			timer := time.NewTimer(p.options.Retry)
			select {
			case <-ctx.Done():
				timer.Stop()
				return InboxAdmissionProgress{}, ctx.Err()
			case <-timer.C:
			}
		}
		previous = progress.Checkpoint
	}
	return InboxAdmissionProgress{}, ErrInboxAdmissionPending
}
