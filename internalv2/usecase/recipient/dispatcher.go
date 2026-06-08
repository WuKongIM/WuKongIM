package recipient

import (
	"context"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/authority"
	"github.com/WuKongIM/WuKongIM/internalv2/contracts/messageevents"
)

const (
	defaultPageSize        = 512
	defaultTargetBatchSize = 512
)

// Dispatcher routes committed messages to recipient-authority processors.
type Dispatcher struct {
	localNodeID     uint64
	recipients      RecipientSource
	resolver        RecipientAuthorityResolver
	local           LocalProcessor
	remote          RecipientRemote
	pageSize        int
	targetBatchSize int
}

type targetRecipientGroup struct {
	target     authority.Target
	recipients []Recipient
}

// NewDispatcher creates a Dispatcher from entry-agnostic ports.
func NewDispatcher(opts DispatcherOptions) *Dispatcher {
	pageSize := opts.PageSize
	if pageSize <= 0 {
		pageSize = defaultPageSize
	}
	targetBatchSize := opts.TargetBatchSize
	if targetBatchSize <= 0 {
		targetBatchSize = defaultTargetBatchSize
	}
	return &Dispatcher{
		localNodeID:     opts.LocalNodeID,
		recipients:      opts.Recipients,
		resolver:        opts.Resolver,
		local:           opts.Local,
		remote:          opts.Remote,
		pageSize:        pageSize,
		targetBatchSize: targetBatchSize,
	}
}

// SubmitCommitted dispatches one committed message by recipient authority.
func (d *Dispatcher) SubmitCommitted(ctx context.Context, event messageevents.MessageCommitted) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(event.MessageScopedUIDs) > 0 {
		recipients := make([]Recipient, 0, len(event.MessageScopedUIDs))
		for _, uid := range event.MessageScopedUIDs {
			if uid == "" {
				continue
			}
			recipients = append(recipients, Recipient{UID: uid})
		}
		return d.dispatchRecipients(ctx, event, recipients)
	}
	if d.recipients == nil {
		return nil
	}

	var cursor string
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		page, err := d.recipients.NextPage(ctx, event, cursor, d.pageSize)
		if err != nil {
			return err
		}
		if err := d.dispatchRecipients(ctx, event, page.Recipients); err != nil {
			return err
		}
		if page.Done {
			return nil
		}
		if page.Cursor == "" || page.Cursor == cursor {
			return ErrRouteNotReady
		}
		cursor = page.Cursor
	}
}

func (d *Dispatcher) dispatchRecipients(ctx context.Context, event messageevents.MessageCommitted, recipients []Recipient) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	groups, err := d.groupByTarget(ctx, recipients)
	if err != nil {
		return err
	}
	for _, group := range groups {
		if err := ctx.Err(); err != nil {
			return err
		}
		for start := 0; start < len(group.recipients); start += d.targetBatchSize {
			end := start + d.targetBatchSize
			if end > len(group.recipients) {
				end = len(group.recipients)
			}
			req := ProcessRequest{
				Target:     group.target,
				Event:      event.Clone(),
				Recipients: append([]Recipient(nil), group.recipients[start:end]...),
			}
			if group.target.IsLocal(d.localNodeID) {
				if d.local == nil {
					return ErrRouteNotReady
				}
				if err := d.local.Process(ctx, req); err != nil {
					return err
				}
				continue
			}
			if d.remote == nil {
				return ErrRouteNotReady
			}
			if err := d.remote.ProcessRemote(ctx, req); err != nil {
				return err
			}
		}
	}
	return nil
}

func (d *Dispatcher) groupByTarget(ctx context.Context, recipients []Recipient) ([]targetRecipientGroup, error) {
	if len(recipients) == 0 {
		return nil, nil
	}
	groups := make([]targetRecipientGroup, 0)
	indexByTarget := make(map[authority.Target]int)
	for _, recipient := range recipients {
		if recipient.UID == "" {
			continue
		}
		if d.resolver == nil {
			return nil, ErrRouteNotReady
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		target, err := d.resolver.ResolveRecipientAuthority(ctx, recipient.UID)
		if err != nil {
			return nil, err
		}
		index, ok := indexByTarget[target]
		if !ok {
			index = len(groups)
			indexByTarget[target] = index
			groups = append(groups, targetRecipientGroup{target: target})
		}
		groups[index].recipients = append(groups[index].recipients, recipient)
	}
	return groups, nil
}
