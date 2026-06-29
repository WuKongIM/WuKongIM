package management

import (
	"context"
	"errors"
	"testing"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestListControllerTaskAuditsFiltersAndLimits(t *testing.T) {
	var seen ControllerTaskAuditListRequest
	reader := &fakeControllerTaskAuditReader{
		listSink: &seen,
		list: ControllerTaskAuditListResponse{
			Total: 1,
			Limit: 20,
			Items: []ControllerTaskAuditSnapshot{{
				TaskID:                "slot-1-replica-move-2-to-4-r9",
				Kind:                  "slot_replica_move",
				Status:                "failed",
				SlotID:                1,
				SourceNode:            2,
				TargetNode:            4,
				LastAppliedRaftIndex:  12,
				FirstAppliedRaftIndex: 10,
				EventCount:            3,
				LastReason:            "not caught up",
			}},
		},
	}
	app := New(Options{ControllerTaskAudit: reader})

	got, err := app.ListControllerTaskAudits(context.Background(), ControllerTaskAuditListRequest{
		Kind:    "slot_replica_move",
		Status:  "failed",
		SlotID:  1,
		NodeID:  4,
		Keyword: "caught",
		Limit:   20,
	})

	if err != nil {
		t.Fatalf("ListControllerTaskAudits() error = %v", err)
	}
	if seen != (ControllerTaskAuditListRequest{Kind: "slot_replica_move", Status: "failed", SlotID: 1, NodeID: 4, Keyword: "caught", Limit: 20}) {
		t.Fatalf("request = %#v, want normalized filters", seen)
	}
	if got.Total != 1 || got.Limit != 20 || len(got.Items) != 1 || got.Items[0].TaskID != "slot-1-replica-move-2-to-4-r9" {
		t.Fatalf("response = %#v, want one audit snapshot", got)
	}
}

func TestControllerTaskAuditEventsReturnsTimeline(t *testing.T) {
	var seen string
	now := time.Date(2026, 6, 29, 10, 0, 0, 0, time.UTC)
	reader := &fakeControllerTaskAuditReader{
		eventsSink: &seen,
		events: ControllerTaskAuditEventsResponse{
			Task: ControllerTaskAuditSnapshot{TaskID: "task-a", EventCount: 2},
			Events: []ControllerTaskAuditEvent{
				{EventID: "event-1", TaskID: "task-a", Type: "created", AppliedRaftIndex: 1, OccurredAt: now},
				{EventID: "event-2", TaskID: "task-a", Type: "completed", AppliedRaftIndex: 2, OccurredAt: now.Add(time.Second)},
			},
		},
	}
	app := New(Options{ControllerTaskAudit: reader})

	got, err := app.ControllerTaskAuditEvents(context.Background(), "task-a")

	if err != nil {
		t.Fatalf("ControllerTaskAuditEvents() error = %v", err)
	}
	if seen != "task-a" {
		t.Fatalf("task id = %q, want task-a", seen)
	}
	if len(got.Events) != 2 || got.Events[0].AppliedRaftIndex != 1 || got.Events[1].AppliedRaftIndex != 2 {
		t.Fatalf("events = %#v, want timeline order", got.Events)
	}
}

func TestControllerTaskAuditUnavailableAndValidation(t *testing.T) {
	if _, err := New(Options{}).ListControllerTaskAudits(context.Background(), ControllerTaskAuditListRequest{}); !errors.Is(err, ErrControllerTaskAuditUnavailable) {
		t.Fatalf("missing reader list error = %v, want ErrControllerTaskAuditUnavailable", err)
	}
	if _, err := New(Options{}).ControllerTaskAuditEvents(context.Background(), "task-a"); !errors.Is(err, ErrControllerTaskAuditUnavailable) {
		t.Fatalf("missing reader events error = %v, want ErrControllerTaskAuditUnavailable", err)
	}
	reader := &fakeControllerTaskAuditReader{}
	if _, err := New(Options{ControllerTaskAudit: reader}).ListControllerTaskAudits(context.Background(), ControllerTaskAuditListRequest{Limit: -1}); !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("invalid limit error = %v, want invalid argument", err)
	}
	if _, err := New(Options{ControllerTaskAudit: reader}).ControllerTaskAuditEvents(context.Background(), " "); !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("blank task id error = %v, want invalid argument", err)
	}
}

type fakeControllerTaskAuditReader struct {
	listSink   *ControllerTaskAuditListRequest
	eventsSink *string
	list       ControllerTaskAuditListResponse
	events     ControllerTaskAuditEventsResponse
	err        error
}

func (f *fakeControllerTaskAuditReader) ListControllerTaskAudits(_ context.Context, req ControllerTaskAuditListRequest) (ControllerTaskAuditListResponse, error) {
	if f.listSink != nil {
		*f.listSink = req
	}
	return f.list, f.err
}

func (f *fakeControllerTaskAuditReader) ControllerTaskAuditEvents(_ context.Context, taskID string) (ControllerTaskAuditEventsResponse, error) {
	if f.eventsSink != nil {
		*f.eventsSink = taskID
	}
	return f.events, f.err
}
