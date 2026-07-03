package cluster

import (
	"context"
	"errors"
	"testing"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
)

func TestManagementTaskAuditReaderRoutesToControllerLeaderRPC(t *testing.T) {
	service := &fakeRemoteTaskAuditService{
		list: managementusecase.ControllerTaskAuditListResponse{
			Total: 1,
			Items: []managementusecase.ControllerTaskAuditSnapshot{{
				TaskID: "slot-1-replica-move-2-to-4-r9",
				Status: "completed",
			}},
		},
		events: managementusecase.ControllerTaskAuditEventsResponse{
			Task: managementusecase.ControllerTaskAuditSnapshot{TaskID: "slot-1-replica-move-2-to-4-r9"},
			Events: []managementusecase.ControllerTaskAuditEvent{{
				EventID: "event-completed",
				TaskID:  "slot-1-replica-move-2-to-4-r9",
				Type:    "completed",
			}},
		},
	}
	adapter := accessnode.New(accessnode.Options{ManagerTaskAudit: service})
	node := &fakeManagementTaskAuditNode{
		nodeID: 1,
		status: cluster.ControllerRaftStatus{
			NodeID:   1,
			Role:     "follower",
			LeaderID: 2,
		},
		handler: adapter.HandleManagerTaskAuditRPC,
	}
	reader := NewManagementTaskAuditReader(node, nil)

	list, err := reader.ListControllerTaskAudits(context.Background(), managementusecase.ControllerTaskAuditListRequest{Limit: 20})
	if err != nil {
		t.Fatalf("ListControllerTaskAudits() error = %v", err)
	}
	if list.Total != 1 || list.Items[0].TaskID != "slot-1-replica-move-2-to-4-r9" {
		t.Fatalf("list = %+v, want remote leader audit history", list)
	}
	if node.calledNodeID != 2 || node.calledServiceID != accessnode.ManagerTaskAuditRPCServiceID {
		t.Fatalf("rpc target = node:%d service:%d, want leader node 2 service %d", node.calledNodeID, node.calledServiceID, accessnode.ManagerTaskAuditRPCServiceID)
	}

	events, err := reader.ControllerTaskAuditEvents(context.Background(), "slot-1-replica-move-2-to-4-r9")
	if err != nil {
		t.Fatalf("ControllerTaskAuditEvents() error = %v", err)
	}
	if len(events.Events) != 1 || events.Events[0].EventID != "event-completed" {
		t.Fatalf("events = %+v, want remote leader timeline", events)
	}
	if service.eventsTaskID != "slot-1-replica-move-2-to-4-r9" {
		t.Fatalf("events task id = %q, want requested task", service.eventsTaskID)
	}
}

func TestManagementTaskAuditReaderUsesLocalStoreWhenLocalNodeIsLeader(t *testing.T) {
	local := &fakeRemoteTaskAuditService{
		list: managementusecase.ControllerTaskAuditListResponse{
			Total: 1,
			Items: []managementusecase.ControllerTaskAuditSnapshot{{TaskID: "local-task"}},
		},
	}
	node := &fakeManagementTaskAuditNode{
		nodeID: 1,
		status: cluster.ControllerRaftStatus{
			NodeID:   1,
			Role:     "leader",
			LeaderID: 1,
		},
	}
	reader := NewManagementTaskAuditReader(node, local)

	list, err := reader.ListControllerTaskAudits(context.Background(), managementusecase.ControllerTaskAuditListRequest{})
	if err != nil {
		t.Fatalf("ListControllerTaskAudits() error = %v", err)
	}
	if list.Total != 1 || list.Items[0].TaskID != "local-task" {
		t.Fatalf("list = %+v, want local task audit history", list)
	}
	if node.calledServiceID != 0 {
		t.Fatalf("called service = %d, want local read without RPC", node.calledServiceID)
	}
}

func TestManagementTaskAuditReaderUsesSnapshotLeaderWhenRaftStatusUnavailable(t *testing.T) {
	service := &fakeRemoteTaskAuditService{
		list: managementusecase.ControllerTaskAuditListResponse{
			Total: 1,
			Items: []managementusecase.ControllerTaskAuditSnapshot{{TaskID: "mirror-task"}},
		},
	}
	adapter := accessnode.New(accessnode.Options{ManagerTaskAudit: service})
	node := &fakeManagementTaskAuditNode{
		nodeID:    3,
		statusErr: errors.New("local raft unavailable"),
		snapshot:  control.Snapshot{ControllerID: 2},
		handler:   adapter.HandleManagerTaskAuditRPC,
	}
	reader := NewManagementTaskAuditReader(node, nil)

	list, err := reader.ListControllerTaskAudits(context.Background(), managementusecase.ControllerTaskAuditListRequest{})
	if err != nil {
		t.Fatalf("ListControllerTaskAudits() error = %v", err)
	}
	if list.Total != 1 || list.Items[0].TaskID != "mirror-task" {
		t.Fatalf("list = %+v, want mirror read routed to snapshot controller", list)
	}
	if node.calledNodeID != 2 || node.calledServiceID != accessnode.ManagerTaskAuditRPCServiceID {
		t.Fatalf("rpc target = node:%d service:%d, want snapshot controller node 2 service %d", node.calledNodeID, node.calledServiceID, accessnode.ManagerTaskAuditRPCServiceID)
	}
}

type fakeManagementTaskAuditNode struct {
	nodeID          uint64
	calledNodeID    uint64
	calledServiceID uint8
	handler         func(context.Context, []byte) ([]byte, error)
	status          cluster.ControllerRaftStatus
	statusErr       error
	snapshot        control.Snapshot
}

func (f *fakeManagementTaskAuditNode) NodeID() uint64 { return f.nodeID }

func (f *fakeManagementTaskAuditNode) CallRPC(ctx context.Context, nodeID uint64, serviceID uint8, payload []byte) ([]byte, error) {
	f.calledNodeID = nodeID
	f.calledServiceID = serviceID
	return f.handler(ctx, payload)
}

func (f *fakeManagementTaskAuditNode) LocalControllerRaftStatus(context.Context) (cluster.ControllerRaftStatus, error) {
	if f.statusErr != nil {
		return cluster.ControllerRaftStatus{}, f.statusErr
	}
	return f.status, nil
}

func (f *fakeManagementTaskAuditNode) LocalControlSnapshot(context.Context) (control.Snapshot, error) {
	return f.snapshot, nil
}

type fakeRemoteTaskAuditService struct {
	list         managementusecase.ControllerTaskAuditListResponse
	events       managementusecase.ControllerTaskAuditEventsResponse
	listReq      managementusecase.ControllerTaskAuditListRequest
	eventsTaskID string
}

func (f *fakeRemoteTaskAuditService) ListControllerTaskAudits(_ context.Context, req managementusecase.ControllerTaskAuditListRequest) (managementusecase.ControllerTaskAuditListResponse, error) {
	f.listReq = req
	return f.list, nil
}

func (f *fakeRemoteTaskAuditService) ControllerTaskAuditEvents(_ context.Context, taskID string) (managementusecase.ControllerTaskAuditEventsResponse, error) {
	f.eventsTaskID = taskID
	return f.events, nil
}
