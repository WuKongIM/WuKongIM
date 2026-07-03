package node

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestManagerTaskAuditCodecRoundTrip(t *testing.T) {
	req := managerTaskAuditRPCRequest{
		Op: managerTaskAuditOpList,
		List: managementusecase.ControllerTaskAuditListRequest{
			Kind: "slot_replica_move", Status: "failed", SlotID: 2, NodeID: 3, Keyword: "caught", Limit: 20,
		},
	}
	body, err := encodeManagerTaskAuditRequest(req)
	if err != nil {
		t.Fatalf("encode request: %v", err)
	}
	gotReq, err := decodeManagerTaskAuditRequest(body)
	if err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if !reflect.DeepEqual(gotReq, req) {
		t.Fatalf("decoded request = %#v, want %#v", gotReq, req)
	}

	now := time.Date(2026, 6, 29, 10, 0, 0, 0, time.UTC)
	resp := managerTaskAuditRPCResponse{
		Status: rpcStatusOK,
		List: managementusecase.ControllerTaskAuditListResponse{
			Total: 1,
			Limit: 20,
			Items: []managementusecase.ControllerTaskAuditSnapshot{{
				TaskID:               "task-a",
				Kind:                 "slot_replica_move",
				Status:               "completed",
				LastAppliedRaftIndex: 12,
				StartedAt:            now,
				CompletedAt:          now.Add(time.Second),
			}},
		},
		Events: managementusecase.ControllerTaskAuditEventsResponse{
			Task: managementusecase.ControllerTaskAuditSnapshot{TaskID: "task-a", EventCount: 1},
			Events: []managementusecase.ControllerTaskAuditEvent{{
				EventID: "event-1", TaskID: "task-a", Type: "completed", AppliedRaftIndex: 12, OccurredAt: now,
				Details: map[string]any{"reason": "caught up"},
			}},
		},
	}
	body, err = encodeManagerTaskAuditResponse(resp)
	if err != nil {
		t.Fatalf("encode response: %v", err)
	}
	gotResp, err := decodeManagerTaskAuditResponse(body)
	if err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if gotResp.Status != rpcStatusOK || len(gotResp.List.Items) != 1 || gotResp.List.Items[0].TaskID != "task-a" || len(gotResp.Events.Events) != 1 {
		t.Fatalf("decoded response = %#v, want encoded task audit data", gotResp)
	}
}

func TestManagerTaskAuditCodecRejectsTrailingBytes(t *testing.T) {
	reqBody, err := encodeManagerTaskAuditRequest(managerTaskAuditRPCRequest{Op: managerTaskAuditOpEvents, TaskID: "task-a"})
	if err != nil {
		t.Fatalf("encode request: %v", err)
	}
	if _, err := decodeManagerTaskAuditRequest(append(append([]byte(nil), reqBody...), 0)); err == nil {
		t.Fatal("decodeManagerTaskAuditRequest() accepted trailing bytes")
	}

	respBody, err := encodeManagerTaskAuditResponse(managerTaskAuditRPCResponse{Status: rpcStatusOK})
	if err != nil {
		t.Fatalf("encode response: %v", err)
	}
	if _, err := decodeManagerTaskAuditResponse(append(append([]byte(nil), respBody...), 0)); err == nil {
		t.Fatal("decodeManagerTaskAuditResponse() accepted trailing bytes")
	}
}

func TestManagerTaskAuditCodecRejectsUnknownFields(t *testing.T) {
	body := append([]byte(nil), managerTaskAuditRequestMagic[:]...)
	body = append(body, []byte(`{"op":"list","surprise":true}`)...)
	_, err := decodeManagerTaskAuditRequest(body)
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("decodeManagerTaskAuditRequest() error = %v, want unknown field", err)
	}
}

func TestManagerTaskAuditRPCServerAndClient(t *testing.T) {
	now := time.Date(2026, 6, 29, 10, 0, 0, 0, time.UTC)
	reader := &fakeManagerTaskAuditReader{
		list: managementusecase.ControllerTaskAuditListResponse{
			Total: 1,
			Limit: 20,
			Items: []managementusecase.ControllerTaskAuditSnapshot{{
				TaskID: "task-a", Kind: "slot_replica_move", Status: "running", StartedAt: now,
			}},
		},
		events: managementusecase.ControllerTaskAuditEventsResponse{
			Task:   managementusecase.ControllerTaskAuditSnapshot{TaskID: "task-a", EventCount: 1},
			Events: []managementusecase.ControllerTaskAuditEvent{{EventID: "event-1", TaskID: "task-a", Type: "running", OccurredAt: now}},
		},
	}
	adapter := New(Options{ManagerTaskAudit: reader})
	node := &fakeManagerTaskAuditRPCNode{handler: adapter.HandleManagerTaskAuditRPC}
	client := NewClient(node)

	list, err := client.ListManagerControllerTaskAudits(context.Background(), 2, managementusecase.ControllerTaskAuditListRequest{Kind: "slot_replica_move", Limit: 20})
	if err != nil {
		t.Fatalf("ListManagerControllerTaskAudits() error = %v", err)
	}
	events, err := client.ManagerControllerTaskAuditEvents(context.Background(), 2, "task-a")
	if err != nil {
		t.Fatalf("ManagerControllerTaskAuditEvents() error = %v", err)
	}

	if len(list.Items) != 1 || list.Items[0].TaskID != "task-a" || len(events.Events) != 1 || events.Events[0].EventID != "event-1" {
		t.Fatalf("list/events = %#v / %#v, want retained task audit data", list, events)
	}
	if reader.listReq.Kind != "slot_replica_move" || reader.listReq.Limit != 20 || reader.taskID != "task-a" {
		t.Fatalf("reader inputs list=%#v taskID=%q, want routed task audit requests", reader.listReq, reader.taskID)
	}
	if node.nodeID != 2 || node.serviceID != ManagerTaskAuditRPCServiceID {
		t.Fatalf("rpc target = node:%d service:%d, want node 2 service %d", node.nodeID, node.serviceID, ManagerTaskAuditRPCServiceID)
	}
}

func TestManagerTaskAuditRPCUnavailableWhenNotConfigured(t *testing.T) {
	body, err := encodeManagerTaskAuditRequest(managerTaskAuditRPCRequest{Op: managerTaskAuditOpList})
	if err != nil {
		t.Fatalf("encode request: %v", err)
	}
	respBody, err := New(Options{}).HandleManagerTaskAuditRPC(context.Background(), body)
	if err != nil {
		t.Fatalf("HandleManagerTaskAuditRPC() error = %v", err)
	}
	resp, err := decodeManagerTaskAuditResponse(respBody)
	if err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Status != rpcStatusUnavailable {
		t.Fatalf("status = %q, want %q", resp.Status, rpcStatusUnavailable)
	}
}

func TestManagerTaskAuditRPCStatusMapping(t *testing.T) {
	cases := []struct {
		name   string
		status string
		want   error
	}{
		{name: "invalid argument", status: rpcStatusInvalidArgument, want: metadb.ErrInvalidArgument},
		{name: "not found", status: rpcStatusNotFound, want: managementusecase.ErrControllerTaskAuditNotFound},
		{name: "unavailable", status: rpcStatusUnavailable, want: managementusecase.ErrControllerTaskAuditUnavailable},
		{name: "canceled", status: rpcStatusContextCanceled, want: context.Canceled},
		{name: "deadline", status: rpcStatusContextDeadlineExceeded, want: context.DeadlineExceeded},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := managerTaskAuditRPCErrorForStatus(tc.status)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
}

type fakeManagerTaskAuditReader struct {
	listReq managementusecase.ControllerTaskAuditListRequest
	taskID  string
	list    managementusecase.ControllerTaskAuditListResponse
	events  managementusecase.ControllerTaskAuditEventsResponse
	err     error
}

func (f *fakeManagerTaskAuditReader) ListControllerTaskAudits(_ context.Context, req managementusecase.ControllerTaskAuditListRequest) (managementusecase.ControllerTaskAuditListResponse, error) {
	f.listReq = req
	return f.list, f.err
}

func (f *fakeManagerTaskAuditReader) ControllerTaskAuditEvents(_ context.Context, taskID string) (managementusecase.ControllerTaskAuditEventsResponse, error) {
	f.taskID = taskID
	return f.events, f.err
}

type fakeManagerTaskAuditRPCNode struct {
	handler   func(context.Context, []byte) ([]byte, error)
	nodeID    uint64
	serviceID uint8
	payload   []byte
}

func (f *fakeManagerTaskAuditRPCNode) CallRPC(ctx context.Context, nodeID uint64, serviceID uint8, payload []byte) ([]byte, error) {
	f.nodeID = nodeID
	f.serviceID = serviceID
	f.payload = append([]byte(nil), payload...)
	return f.handler(ctx, payload)
}
