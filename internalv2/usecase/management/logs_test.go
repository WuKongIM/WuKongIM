package management

import (
	"context"
	"errors"
	"testing"
)

func TestListControllerLogEntriesDelegatesNodeScopedRead(t *testing.T) {
	reader := &fakeLogReader{
		controller: ControllerLogEntriesResponse{
			NodeID:       2,
			FirstIndex:   1,
			LastIndex:    4,
			CommitIndex:  4,
			AppliedIndex: 3,
			NextCursor:   3,
			Items: []LogEntry{{
				Index:        4,
				Term:         2,
				Type:         "normal",
				DataSize:     12,
				DecodeStatus: "ok",
				DecodedType:  "init_cluster_state",
				Decoded:      map[string]any{"command": "init_cluster_state"},
			}},
		},
	}
	app := New(Options{Logs: reader})

	got, err := app.ListControllerLogEntries(context.Background(), ListControllerLogEntriesRequest{
		NodeID: 2,
		Limit:  2,
		Cursor: 5,
	})
	if err != nil {
		t.Fatalf("ListControllerLogEntries() error = %v", err)
	}
	if reader.controllerReq.NodeID != 2 || reader.controllerReq.Limit != 2 || reader.controllerReq.Cursor != 5 {
		t.Fatalf("controller request = %#v, want node 2 limit 2 cursor 5", reader.controllerReq)
	}
	if !sameLogEntries(got.Items, reader.controller.Items) || got.NextCursor != 3 {
		t.Fatalf("controller page = %#v, want %#v", got, reader.controller)
	}
}

func TestListSlotLogEntriesDelegatesNodeScopedRead(t *testing.T) {
	reader := &fakeLogReader{
		slot: SlotLogEntriesResponse{
			NodeID:       2,
			SlotID:       9,
			FirstIndex:   1,
			LastIndex:    4,
			CommitIndex:  4,
			AppliedIndex: 3,
			NextCursor:   3,
			Items: []LogEntry{{
				Index:        4,
				Term:         2,
				Type:         "normal",
				DataSize:     12,
				DecodeStatus: "ok",
				DecodedType:  "upsert_user",
				Decoded:      map[string]any{"command": "upsert_user", "uid": "u1"},
			}},
		},
	}
	app := New(Options{Logs: reader})

	got, err := app.ListSlotLogEntries(context.Background(), ListSlotLogEntriesRequest{
		NodeID: 2,
		SlotID: 9,
		Limit:  2,
		Cursor: 5,
	})
	if err != nil {
		t.Fatalf("ListSlotLogEntries() error = %v", err)
	}
	if reader.slotReq.NodeID != 2 || reader.slotReq.SlotID != 9 || reader.slotReq.Limit != 2 || reader.slotReq.Cursor != 5 {
		t.Fatalf("slot request = %#v, want node 2 slot 9 limit 2 cursor 5", reader.slotReq)
	}
	if !sameLogEntries(got.Items, reader.slot.Items) || got.NextCursor != 3 {
		t.Fatalf("slot page = %#v, want %#v", got, reader.slot)
	}
}

func TestListLogEntriesRequiresLogReader(t *testing.T) {
	app := New(Options{})

	_, err := app.ListControllerLogEntries(context.Background(), ListControllerLogEntriesRequest{NodeID: 1})
	if !errors.Is(err, ErrLogReaderUnavailable) {
		t.Fatalf("ListControllerLogEntries() error = %v, want %v", err, ErrLogReaderUnavailable)
	}

	_, err = app.ListSlotLogEntries(context.Background(), ListSlotLogEntriesRequest{NodeID: 1, SlotID: 1})
	if !errors.Is(err, ErrLogReaderUnavailable) {
		t.Fatalf("ListSlotLogEntries() error = %v, want %v", err, ErrLogReaderUnavailable)
	}
}

type fakeLogReader struct {
	controllerReq ListControllerLogEntriesRequest
	slotReq       ListSlotLogEntriesRequest
	controller    ControllerLogEntriesResponse
	slot          SlotLogEntriesResponse
	err           error
}

func (f *fakeLogReader) ControllerLogEntries(ctx context.Context, req ListControllerLogEntriesRequest) (ControllerLogEntriesResponse, error) {
	f.controllerReq = req
	return f.controller, f.err
}

func (f *fakeLogReader) SlotLogEntries(ctx context.Context, req ListSlotLogEntriesRequest) (SlotLogEntriesResponse, error) {
	f.slotReq = req
	return f.slot, f.err
}

func sameLogEntries(left, right []LogEntry) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i].Index != right[i].Index ||
			left[i].Term != right[i].Term ||
			left[i].Type != right[i].Type ||
			left[i].DataSize != right[i].DataSize ||
			left[i].DecodeStatus != right[i].DecodeStatus ||
			left[i].DecodedType != right[i].DecodedType {
			return false
		}
	}
	return true
}
