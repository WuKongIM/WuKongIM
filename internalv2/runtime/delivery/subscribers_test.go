package delivery

import (
	"context"
	"reflect"
	"testing"
)

func TestChannelSubscriberPlannerPassesTaskFieldsToSource(t *testing.T) {
	source := &recordingChannelSubscriberSource{
		page: UIDPage{UIDs: []string{"u1", "u2"}, NextCursor: "c2"},
	}
	planner := NewChannelSubscriberPlanner(ChannelSubscriberPlannerOptions{Source: source})
	task := FanoutTask{
		Envelope: Envelope{
			ChannelID:   "g1",
			ChannelType: 2,
		},
		Partition: Partition{ID: 7, LeaderNodeID: 3, HashSlotStart: 10, HashSlotEnd: 19},
	}

	page, err := planner.NextPartitionPage(context.Background(), task, "c1", 128)
	if err != nil {
		t.Fatalf("NextPartitionPage() error = %v", err)
	}

	if !reflect.DeepEqual(page.UIDs, []string{"u1", "u2"}) || page.NextCursor != "c2" || page.Done {
		t.Fatalf("page = %#v, want source page", page)
	}
	want := SubscriberPageRequest{
		ChannelID:   "g1",
		ChannelType: 2,
		Partition:   task.Partition,
		Cursor:      "c1",
		Limit:       128,
	}
	if !reflect.DeepEqual(source.requests, []SubscriberPageRequest{want}) {
		t.Fatalf("requests = %#v, want %#v", source.requests, []SubscriberPageRequest{want})
	}
}

func TestChannelSubscriberPlannerNilSourceCompletes(t *testing.T) {
	planner := NewChannelSubscriberPlanner(ChannelSubscriberPlannerOptions{})

	page, err := planner.NextPartitionPage(context.Background(), FanoutTask{}, "", 0)
	if err != nil {
		t.Fatalf("NextPartitionPage() error = %v", err)
	}
	if !page.Done || len(page.UIDs) != 0 || page.NextCursor != "" {
		t.Fatalf("page = %#v, want terminal empty page", page)
	}
}

func TestChannelSubscriberPlannerUsesDefaultLimit(t *testing.T) {
	source := &recordingChannelSubscriberSource{page: UIDPage{Done: true}}
	planner := NewChannelSubscriberPlanner(ChannelSubscriberPlannerOptions{Source: source})

	_, err := planner.NextPartitionPage(context.Background(), FanoutTask{}, "", 0)
	if err != nil {
		t.Fatalf("NextPartitionPage() error = %v", err)
	}
	if len(source.requests) != 1 || source.requests[0].Limit != defaultFanoutPageSize {
		t.Fatalf("requests = %#v, want default limit %d", source.requests, defaultFanoutPageSize)
	}
}

type recordingChannelSubscriberSource struct {
	requests []SubscriberPageRequest
	page     UIDPage
	err      error
}

func (s *recordingChannelSubscriberSource) ListSubscribers(_ context.Context, req SubscriberPageRequest) (UIDPage, error) {
	s.requests = append(s.requests, req)
	return s.page, s.err
}
