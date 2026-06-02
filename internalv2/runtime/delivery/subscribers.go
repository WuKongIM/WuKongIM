package delivery

import "context"

// ChannelSubscriberSource lists durable subscribers for one channel partition.
type ChannelSubscriberSource interface {
	ListSubscribers(context.Context, SubscriberPageRequest) (UIDPage, error)
}

// SubscriberPageRequest describes one channel subscriber page scan.
type SubscriberPageRequest struct {
	// ChannelID identifies the committed-message channel being delivered.
	ChannelID string
	// ChannelType is the WuKong channel type for ChannelID.
	ChannelType uint8
	// Partition is the delivery authority partition being scanned.
	Partition Partition
	// Cursor resumes a source-specific subscriber scan.
	Cursor string
	// Limit bounds the number of UIDs returned by one page.
	Limit int
}

// ChannelSubscriberPlannerOptions configures a channel subscriber planner.
type ChannelSubscriberPlannerOptions struct {
	// Source provides durable subscriber pages; nil makes the planner terminal.
	Source ChannelSubscriberSource
}

// ChannelSubscriberPlanner adapts a durable channel subscriber source to fanout paging.
type ChannelSubscriberPlanner struct {
	// source provides durable subscriber pages for channel fanout.
	source ChannelSubscriberSource
}

// NewChannelSubscriberPlanner creates a channel subscriber fanout planner.
func NewChannelSubscriberPlanner(opts ChannelSubscriberPlannerOptions) *ChannelSubscriberPlanner {
	return &ChannelSubscriberPlanner{source: opts.Source}
}

// NextPartitionPage returns one subscriber page for a fanout partition.
func (p *ChannelSubscriberPlanner) NextPartitionPage(ctx context.Context, task FanoutTask, cursor string, limit int) (UIDPage, error) {
	if p == nil || p.source == nil {
		return UIDPage{Done: true}, nil
	}
	if limit <= 0 {
		limit = defaultFanoutPageSize
	}
	page, err := p.source.ListSubscribers(ctx, SubscriberPageRequest{
		ChannelID:   task.Envelope.ChannelID,
		ChannelType: task.Envelope.ChannelType,
		Partition:   task.Partition,
		Cursor:      cursor,
		Limit:       limit,
	})
	if err != nil {
		return UIDPage{}, err
	}
	return page, nil
}
