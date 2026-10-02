package mqttsession

import (
	"context"
	"math"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
)

// InboxAdmissionMetadata uses current Slot authority for the source checkpoint
// and each participant's qualification page, never a local replica fallback.
type InboxAdmissionMetadata interface {
	ReadMQTT(context.Context, meta.MQTTRead) (meta.MQTTReadResult, error)
	CompareAndSwapMQTTInboxAdmission(context.Context, uint64, meta.MQTTInboxAdmission) (meta.MQTTInboxAdmissionResult, error)
}

// InboxAdmissionSources proves one source prepared, or authoritative closed
// intent. Implementations must retain cleanup debt when reporting closed intent.
type InboxAdmissionSources interface {
	Prepare(context.Context, meta.MQTTSourceBindingKey, SourceChannel) (PreparedInboxSource, error)
}

type InboxAdmissionOptions struct {
	Store   InboxAdmissionMetadata
	Sources InboxAdmissionSources
	// PageSize bounds one participant turn, including candidates whose intent closed.
	PageSize int
	// Timeout bounds all reads, preparation and commits in one turn; default 5s.
	Timeout time.Duration
	// Now supplies positive durable timestamps; regression cannot advance progress.
	Now func() time.Time
}

// InboxAdmissionProgress is preparation evidence, not an append capability. The
// eventual append must still bind to this checkpoint's directory incarnation.
type InboxAdmissionProgress struct {
	Ready      bool
	Examined   int
	Checkpoint meta.MQTTInboxAdmission
}

// InboxAdmission advances one participant/page per call with no worker or socket
// authority. Durable per-candidate commits bound repeated work after interruption.
type InboxAdmission struct{ options InboxAdmissionOptions }

func NewInboxAdmission(o InboxAdmissionOptions) (*InboxAdmission, error) {
	if o.PageSize == 0 {
		o.PageSize = 8
	}
	if o.Timeout == 0 {
		o.Timeout = 5 * time.Second
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Store == nil || o.Sources == nil || o.PageSize < 1 || o.PageSize > 64 || o.Timeout <= 0 || o.Timeout > time.Minute {
		return nil, ErrInvalid
	}
	return &InboxAdmission{options: o}, nil
}

// Advance waits for native directory completion, then scans qualifications only
// after both UID registrations are committed. A source error preserves previous
// progress; a changed directory incarnation cannot publish stale completion.
func (p *InboxAdmission) Advance(parent context.Context, ch SourceChannel) (out InboxAdmissionProgress, err error) {
	if p == nil || parent == nil || ch.Type != 1 || meta.ValidateMQTTRead(meta.MQTTRead{Kind: meta.MQTTReadInboxAdmission, AdmissionChannel: ch.ID}) != nil {
		return out, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(parent, p.options.Timeout)
	defer cancel()
	defer func() {
		if recover() != nil {
			err = ErrSubscriptionCallback
		}
		if canceled := ctx.Err(); canceled != nil {
			err = canceled
		}
		if err != nil {
			out = InboxAdmissionProgress{}
		}
	}()
	view, err := p.current(ctx, ch.ID)
	if err != nil {
		return out, err
	}
	if !inboxDirectoryReady(view) {
		return out, nil
	}
	now := p.options.Now()
	if now.IsZero() || now.UnixMilli() <= 0 {
		return out, ErrClock
	}
	row := meta.MQTTInboxAdmission{ChannelID: ch.ID, DirectoryGeneration: view.Runtime.DirectoryGeneration}
	if view.Checkpoint != nil {
		row = *view.Checkpoint
		if now.UnixMilli() < row.UpdatedAtMS {
			return out, ErrClock
		}
	}
	if row.DirectoryGeneration != view.Runtime.DirectoryGeneration || row.Revision == 0 {
		row.DirectoryGeneration, row.Participant, row.After = view.Runtime.DirectoryGeneration, 0, meta.MQTTSourceBindingKey{}
		row, err = p.write(ctx, row)
		if err != nil {
			return out, err
		}
	}
	if row.Participant == 2 {
		return InboxAdmissionProgress{Ready: true, Checkpoint: row}, nil
	}
	left, right, _ := channelid.DecodePersonChannel(ch.ID)
	uid := left
	if row.Participant == 1 {
		uid = right
	}
	query := meta.MQTTRead{Kind: meta.MQTTReadSourceCandidates, Owner: meta.MQTTBindingOwner{Kind: meta.MQTTBindingUID, ID: uid}, After: meta.MQTTReadCursor{Binding: row.After}, Limit: p.options.PageSize}
	page, err := p.read(ctx, query)
	if err != nil {
		return out, err
	}
	if err = validateInboxAdmissionCandidates(query, page); err != nil {
		return out, err
	}
	for _, candidate := range page.Bindings {
		prepared, e := p.options.Sources.Prepare(ctx, candidate.Key, ch)
		if e != nil {
			return out, e
		}
		if err = ctx.Err(); err != nil {
			return out, err
		}
		if !validInboxAdmissionPreparation(candidate, ch, prepared) {
			return out, ErrEvidence
		}
		row.After = candidate.Key
		row, err = p.write(ctx, row)
		if err != nil {
			return out, err
		}
		out.Examined++
	}
	if page.Done {
		row.Participant++
		row.After = meta.MQTTSourceBindingKey{}
		row, err = p.write(ctx, row)
		if err != nil {
			return out, err
		}
	}
	// A response is not completion proof after deletion or a stale local read.
	view, err = p.current(ctx, ch.ID)
	if err != nil {
		return out, err
	}
	if !inboxDirectoryReady(view) || view.Runtime.DirectoryGeneration != row.DirectoryGeneration {
		return out, ErrConflict
	}
	actual := view.Checkpoint
	if actual == nil || actual.DirectoryGeneration != row.DirectoryGeneration || actual.Revision < row.Revision || actual.Participant < row.Participant || (actual.Participant == row.Participant && actual.After != row.After && !inboxBindingKeyAfter(row.After, actual.After)) {
		return out, ErrEvidence
	}
	out.Checkpoint, out.Ready = *actual, actual.Participant == 2
	return out, nil
}

func inboxDirectoryReady(v *meta.MQTTInboxAdmissionView) bool {
	return v.Runtime != nil && v.Channel != nil && v.Channel.DirectoryProjectionState == meta.DirectoryProjectionReady && v.Channel.DirectoryProjectionGeneration == v.Runtime.DirectoryGeneration
}

func (p *InboxAdmission) write(ctx context.Context, row meta.MQTTInboxAdmission) (meta.MQTTInboxAdmission, error) {
	if err := ctx.Err(); err != nil {
		return meta.MQTTInboxAdmission{}, err
	}
	if row.Revision == math.MaxUint64 {
		return meta.MQTTInboxAdmission{}, ErrEvidence
	}
	now := p.options.Now()
	if now.IsZero() || now.UnixMilli() <= 0 || now.UnixMilli() < row.UpdatedAtMS {
		return meta.MQTTInboxAdmission{}, ErrClock
	}
	expected := row.Revision
	row.Revision++
	row.UpdatedAtMS = now.UnixMilli()
	result, err := p.options.Store.CompareAndSwapMQTTInboxAdmission(ctx, expected, row)
	if canceled := ctx.Err(); canceled != nil {
		return meta.MQTTInboxAdmission{}, canceled
	}
	if err != nil {
		return meta.MQTTInboxAdmission{}, err
	}
	if result.Status == meta.MQTTSessionCASConflict {
		return meta.MQTTInboxAdmission{}, ErrConflict
	}
	if result.Status != meta.MQTTSessionCASApplied && result.Status != meta.MQTTSessionCASUnchanged || result.CurrentRevision != row.Revision {
		return meta.MQTTInboxAdmission{}, ErrEvidence
	}
	return row, nil
}
