package node

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	opscontract "github.com/WuKongIM/WuKongIM/internal/contracts/opsmcp"
	"github.com/WuKongIM/WuKongIM/internal/observability/diagnostics"
	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	pluginusecase "github.com/WuKongIM/WuKongIM/internal/usecase/plugin"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
	dbinspect "github.com/WuKongIM/WuKongIM/pkg/db/inspect"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestPresenceRouteLifecyclePreservesAuthorityFencesAcrossNodeRPC(t *testing.T) {
	authority := newFakePresenceAuthority()
	adapter := New(Options{Authority: authority})
	transport := &nodeRPCContractTransport{
		handlers: map[uint8]func(context.Context, []byte) ([]byte, error){
			PresenceAuthorityRPCServiceID: adapter.HandlePresenceAuthorityRPC,
		},
	}
	client := NewClient(transport)
	target := testPresenceTarget()
	committedRoute := testPresenceRoute("u-committed", 501)
	abortedRoute := testPresenceRoute("u-aborted", 502)

	registered, err := client.RegisterRoute(context.Background(), target, committedRoute)
	if err != nil {
		t.Fatalf("RegisterRoute() error = %v", err)
	}
	if err := client.CommitRoute(context.Background(), target, string(registered.PendingToken)); err != nil {
		t.Fatalf("CommitRoute() error = %v", err)
	}
	toAbort, err := client.RegisterRoute(context.Background(), target, abortedRoute)
	if err != nil {
		t.Fatalf("RegisterRoute(abort candidate) error = %v", err)
	}
	if err := client.AbortRoute(context.Background(), target, string(toAbort.PendingToken)); err != nil {
		t.Fatalf("AbortRoute() error = %v", err)
	}
	if err := client.UnregisterRoute(context.Background(), target, committedRoute.Identity(), committedRoute.OwnerSeq); err != nil {
		t.Fatalf("UnregisterRoute() error = %v", err)
	}

	if registered.PendingToken != "pending-1" {
		t.Fatalf("pending token = %q, want pending-1", registered.PendingToken)
	}
	if len(authority.registerCalls) != 2 ||
		!reflect.DeepEqual(authority.registerCalls[0], presenceRegisterCall{target: target, route: committedRoute}) ||
		!reflect.DeepEqual(authority.registerCalls[1], presenceRegisterCall{target: target, route: abortedRoute}) {
		t.Fatalf("register calls = %#v, want exact target and route", authority.registerCalls)
	}
	wantTokenCall := presenceTokenCall{target: target, token: "pending-1"}
	if len(authority.commitCalls) != 1 || !reflect.DeepEqual(authority.commitCalls[0], wantTokenCall) {
		t.Fatalf("commit calls = %#v, want %#v", authority.commitCalls, wantTokenCall)
	}
	if len(authority.abortCalls) != 1 || !reflect.DeepEqual(authority.abortCalls[0], wantTokenCall) {
		t.Fatalf("abort calls = %#v, want %#v", authority.abortCalls, wantTokenCall)
	}
	wantUnregister := presenceUnregisterCall{
		target: target, identity: committedRoute.Identity(), ownerSeq: committedRoute.OwnerSeq,
	}
	if len(authority.unregisterCalls) != 1 || !reflect.DeepEqual(authority.unregisterCalls[0], wantUnregister) {
		t.Fatalf("unregister calls = %#v, want %#v", authority.unregisterCalls, wantUnregister)
	}
	if len(transport.calls) != 5 {
		t.Fatalf("RPC calls = %#v, want five authority lifecycle calls", transport.calls)
	}
	for i, call := range transport.calls {
		if call.nodeID != target.LeaderNodeID || call.serviceID != PresenceAuthorityRPCServiceID {
			t.Fatalf("RPC call[%d] = %#v, want leader %d presence authority", i, call, target.LeaderNodeID)
		}
	}
}

func TestNodeRPCOperationalReadsRouteToExactNodeAndReturnDetachedDTOs(t *testing.T) {
	connections := &fakeManagerConnectionService{
		page: managementusecase.ListConnectionsResponse{
			Total: 1,
			Items: []managementusecase.Connection{{NodeID: 2, SessionID: 20, UID: "u2"}},
		},
	}
	controllerRaft := &fakeManagerControllerRaftService{
		status: managementusecase.ControllerRaftStatus{NodeID: 3, Role: "follower", LeaderID: 1, Term: 8},
	}
	appLogs := &fakeManagerAppLogReader{
		sourcesResp: managementusecase.ApplicationLogSourcesResponse{
			NodeID:  4,
			Sources: []managementusecase.ApplicationLogSource{{Name: "app", Available: true}},
		},
	}
	logs := &fakeManagerLogService{
		controller: managementusecase.ControllerLogEntriesResponse{
			NodeID: 5,
			Items:  []managementusecase.ControllerLogEntry{{Index: 9, Term: 3, DecodedType: "config"}},
		},
	}
	plugins := &fakeManagerPluginService{
		list: managementusecase.NodePluginList{
			NodeID:  6,
			Plugins: []managementusecase.Plugin{{NodeID: 6, No: "wk.audit", Enabled: true}},
		},
	}
	diagnosticStore := &fakeManagerDiagnosticsService{
		result: diagnostics.QueryResult{Scope: "local_node", NodeID: 7, Status: diagnostics.StatusOK},
	}
	adapter := New(Options{
		ManagerConnections:    connections,
		ManagerControllerRaft: controllerRaft,
		ManagerAppLogs:        appLogs,
		ManagerLogs:           logs,
		ManagerPlugins:        plugins,
		ManagerDiagnostics:    diagnosticStore,
	})
	audits := &nodeRPCAuditReader{entries: []opscontract.AuditEntry{{
		RequestID: "request-8", RecorderNodeID: 8, Phase: "owner", Result: "ok",
	}}}
	opsAdapter := NewOpsMCPRPCAdapter(nil, nil, audits)
	transport := &nodeRPCContractTransport{
		callerNodeID: 1,
		handlers: map[uint8]func(context.Context, []byte) ([]byte, error){
			ManagerConnectionRPCServiceID:     adapter.HandleManagerConnectionRPC,
			ManagerControllerRaftRPCServiceID: adapter.HandleManagerControllerRaftRPC,
			ManagerAppLogRPCServiceID:         adapter.HandleManagerAppLogRPC,
			ManagerLogRPCServiceID:            adapter.HandleManagerLogRPC,
			ManagerPluginRPCServiceID:         adapter.HandleManagerPluginRPC,
			ManagerDiagnosticsRPCServiceID:    adapter.HandleManagerDiagnosticsRPC,
			OpsMCPRPCServiceID:                opsAdapter.HandleRPC,
		},
	}
	client := NewClient(transport)

	connectionPage, err := client.ListManagerConnections(context.Background(), managementusecase.ListConnectionsRequest{
		NodeID: 2, Limit: 25,
	})
	if err != nil {
		t.Fatalf("ListManagerConnections() error = %v", err)
	}
	controllerStatus, err := client.GetManagerControllerRaftStatus(context.Background(), 3)
	if err != nil {
		t.Fatalf("GetManagerControllerRaftStatus() error = %v", err)
	}
	logSources, err := client.GetManagerApplicationLogSources(context.Background(), managementusecase.ApplicationLogSourcesRequest{NodeID: 4})
	if err != nil {
		t.Fatalf("GetManagerApplicationLogSources() error = %v", err)
	}
	controllerLogs, err := client.GetManagerControllerLogEntries(context.Background(), managementusecase.ListControllerLogEntriesRequest{
		NodeID: 5, Limit: 10,
	})
	if err != nil {
		t.Fatalf("GetManagerControllerLogEntries() error = %v", err)
	}
	pluginList, err := client.ListManagerPlugins(context.Background(), 6)
	if err != nil {
		t.Fatalf("ListManagerPlugins() error = %v", err)
	}
	diagnosticsResult, err := client.QueryManagerDiagnostics(context.Background(), 7, diagnostics.Query{TraceID: "trace-7", Limit: 20})
	if err != nil {
		t.Fatalf("QueryManagerDiagnostics() error = %v", err)
	}
	auditEntries, err := client.ReadOpsMCPAudits(context.Background(), 8, 10)
	if err != nil {
		t.Fatalf("ReadOpsMCPAudits() error = %v", err)
	}

	if connectionPage.Total != 1 || len(connectionPage.Items) != 1 || connectionPage.Items[0].UID != "u2" ||
		controllerStatus.NodeID != 3 || controllerStatus.Term != 8 ||
		logSources.NodeID != 4 || len(logSources.Sources) != 1 || logSources.Sources[0].Name != "app" ||
		controllerLogs.NodeID != 5 || len(controllerLogs.Items) != 1 || controllerLogs.Items[0].Index != 9 ||
		len(pluginList) != 1 || pluginList[0].NodeID != 6 || pluginList[0].No != "wk.audit" ||
		diagnosticsResult.NodeID != 7 || diagnosticStore.query.TraceID != "trace-7" ||
		len(auditEntries) != 1 || auditEntries[0].RecorderNodeID != 8 || audits.limit != 10 {
		t.Fatalf("operational DTOs were not preserved: connections=%#v controller=%#v sources=%#v logs=%#v plugins=%#v diagnostics=%#v audits=%#v",
			connectionPage, controllerStatus, logSources, controllerLogs, pluginList, diagnosticsResult, auditEntries)
	}
	wantCalls := []nodeRPCContractCall{
		{nodeID: 2, serviceID: ManagerConnectionRPCServiceID},
		{nodeID: 3, serviceID: ManagerControllerRaftRPCServiceID},
		{nodeID: 4, serviceID: ManagerAppLogRPCServiceID},
		{nodeID: 5, serviceID: ManagerLogRPCServiceID},
		{nodeID: 6, serviceID: ManagerPluginRPCServiceID},
		{nodeID: 7, serviceID: ManagerDiagnosticsRPCServiceID},
		{nodeID: 8, serviceID: OpsMCPRPCServiceID},
	}
	if !reflect.DeepEqual(transport.calls, wantCalls) {
		t.Fatalf("RPC calls = %#v, want exact node/service routing %#v", transport.calls, wantCalls)
	}

	connectionPage.Items[0].UID = "mutated"
	pluginList[0].No = "mutated"
	auditEntries[0].RequestID = "mutated"
	if connections.page.Items[0].UID != "u2" || plugins.list.Plugins[0].No != "wk.audit" || audits.entries[0].RequestID != "request-8" {
		t.Fatal("remote operational read result aliases provider-owned DTO storage")
	}
}

func TestManagerReadRPCsPreserveStableRemoteFailureClasses(t *testing.T) {
	providerFailure := errors.New("private provider detail")
	tests := []struct {
		name        string
		providerErr error
		wantErr     error
		call        func(error) error
	}{
		{
			name: "Controller Raft canceled", providerErr: context.Canceled, wantErr: context.Canceled,
			call: func(providerErr error) error {
				adapter := New(Options{ManagerControllerRaft: &fakeManagerControllerRaftService{err: providerErr}})
				_, err := NewClient(&fakeManagerConnectionRPCNode{handler: adapter.HandleManagerControllerRaftRPC}).GetManagerControllerRaftStatus(context.Background(), 2)
				return err
			},
		},
		{
			name: "Controller Raft unavailable", providerErr: providerFailure, wantErr: managementusecase.ErrControllerRaftOperatorUnavailable,
			call: func(providerErr error) error {
				adapter := New(Options{ManagerControllerRaft: &fakeManagerControllerRaftService{err: providerErr}})
				_, err := NewClient(&fakeManagerConnectionRPCNode{handler: adapter.HandleManagerControllerRaftRPC}).GetManagerControllerRaftStatus(context.Background(), 2)
				return err
			},
		},
		{
			name: "Slot missing", providerErr: clusterpkg.ErrSlotNotFound, wantErr: clusterpkg.ErrSlotNotFound,
			call: func(providerErr error) error {
				adapter := New(Options{ManagerSlotRaft: &fakeManagerSlotRaftService{err: providerErr}})
				_, err := NewClient(&fakeManagerConnectionRPCNode{handler: adapter.HandleManagerSlotRaftRPC}).GetManagerSlotRaftStatus(context.Background(), 2, 9)
				return err
			},
		},
		{
			name: "Slot status unavailable", providerErr: providerFailure, wantErr: managementusecase.ErrSlotRaftOperatorUnavailable,
			call: func(providerErr error) error {
				adapter := New(Options{ManagerSlotRaft: &fakeManagerSlotRaftService{err: providerErr}})
				_, err := NewClient(&fakeManagerConnectionRPCNode{handler: adapter.HandleManagerSlotRaftRPC}).GetManagerSlotRaftStatus(context.Background(), 2, 9)
				return err
			},
		},
		{
			name: "Controller log missing", providerErr: metadb.ErrNotFound, wantErr: metadb.ErrNotFound,
			call: func(providerErr error) error {
				adapter := New(Options{ManagerLogs: &fakeManagerLogService{err: providerErr}})
				_, err := NewClient(&fakeManagerConnectionRPCNode{handler: adapter.HandleManagerLogRPC}).GetManagerControllerLogEntries(context.Background(), managementusecase.ListControllerLogEntriesRequest{NodeID: 2, Limit: 10})
				return err
			},
		},
		{
			name: "Controller log unavailable", providerErr: providerFailure, wantErr: managementusecase.ErrLogReaderUnavailable,
			call: func(providerErr error) error {
				adapter := New(Options{ManagerLogs: &fakeManagerLogService{err: providerErr}})
				_, err := NewClient(&fakeManagerConnectionRPCNode{handler: adapter.HandleManagerLogRPC}).GetManagerControllerLogEntries(context.Background(), managementusecase.ListControllerLogEntriesRequest{NodeID: 2, Limit: 10})
				return err
			},
		},
		{
			name: "Channel read deadline", providerErr: context.DeadlineExceeded, wantErr: context.DeadlineExceeded,
			call: func(providerErr error) error {
				adapter := New(Options{ManagerChannels: &fakeManagerChannelService{err: providerErr}})
				_, err := NewClient(&fakeManagerConnectionRPCNode{handler: adapter.HandleManagerChannelRPC}).ListManagerBusinessChannels(context.Background(), managementusecase.ListBusinessChannelsRequest{NodeID: 2, Limit: 10})
				return err
			},
		},
		{
			name: "Channel read unavailable", providerErr: providerFailure, wantErr: managementusecase.ErrBusinessChannelReaderUnavailable,
			call: func(providerErr error) error {
				adapter := New(Options{ManagerChannels: &fakeManagerChannelService{err: providerErr}})
				_, err := NewClient(&fakeManagerConnectionRPCNode{handler: adapter.HandleManagerChannelRPC}).ListManagerBusinessChannels(context.Background(), managementusecase.ListBusinessChannelsRequest{NodeID: 2, Limit: 10})
				return err
			},
		},
		{
			name: "Connection request invalid", providerErr: metadb.ErrInvalidArgument, wantErr: metadb.ErrInvalidArgument,
			call: func(providerErr error) error {
				adapter := New(Options{ManagerConnections: &fakeManagerConnectionService{err: providerErr}})
				_, err := NewClient(&fakeManagerConnectionRPCNode{handler: adapter.HandleManagerConnectionRPC}).ListManagerConnections(context.Background(), managementusecase.ListConnectionsRequest{NodeID: 2, Limit: 10})
				return err
			},
		},
		{
			name: "Connection missing", providerErr: metadb.ErrNotFound, wantErr: metadb.ErrNotFound,
			call: func(providerErr error) error {
				adapter := New(Options{ManagerConnections: &fakeManagerConnectionService{err: providerErr}})
				_, err := NewClient(&fakeManagerConnectionRPCNode{handler: adapter.HandleManagerConnectionRPC}).ListManagerConnections(context.Background(), managementusecase.ListConnectionsRequest{NodeID: 2, Limit: 10})
				return err
			},
		},
		{
			name: "Connection read unavailable", providerErr: providerFailure, wantErr: managementusecase.ErrConnectionReaderUnavailable,
			call: func(providerErr error) error {
				adapter := New(Options{ManagerConnections: &fakeManagerConnectionService{err: providerErr}})
				_, err := NewClient(&fakeManagerConnectionRPCNode{handler: adapter.HandleManagerConnectionRPC}).ListManagerConnections(context.Background(), managementusecase.ListConnectionsRequest{NodeID: 2, Limit: 10})
				return err
			},
		},
		{
			name: "Application log missing", providerErr: metadb.ErrNotFound, wantErr: metadb.ErrNotFound,
			call: func(providerErr error) error {
				adapter := New(Options{ManagerAppLogs: &fakeManagerAppLogReader{err: providerErr}})
				_, err := NewClient(&fakeManagerConnectionRPCNode{handler: adapter.HandleManagerAppLogRPC}).GetManagerApplicationLogSources(context.Background(), managementusecase.ApplicationLogSourcesRequest{NodeID: 2})
				return err
			},
		},
		{
			name: "Application log unavailable", providerErr: providerFailure, wantErr: managementusecase.ErrApplicationLogReaderUnavailable,
			call: func(providerErr error) error {
				adapter := New(Options{ManagerAppLogs: &fakeManagerAppLogReader{err: providerErr}})
				_, err := NewClient(&fakeManagerConnectionRPCNode{handler: adapter.HandleManagerAppLogRPC}).GetManagerApplicationLogSources(context.Background(), managementusecase.ApplicationLogSourcesRequest{NodeID: 2})
				return err
			},
		},
		{
			name: "Plugin missing", providerErr: pluginusecase.ErrPluginNotFound, wantErr: pluginusecase.ErrPluginNotFound,
			call: func(providerErr error) error {
				adapter := New(Options{ManagerPlugins: &fakeManagerPluginService{err: providerErr}})
				_, err := NewClient(&fakeManagerConnectionRPCNode{handler: adapter.HandleManagerPluginRPC}).ListManagerPlugins(context.Background(), 2)
				return err
			},
		},
		{
			name: "Plugin read unavailable", providerErr: providerFailure, wantErr: managementusecase.ErrPluginNodeUnavailable,
			call: func(providerErr error) error {
				adapter := New(Options{ManagerPlugins: &fakeManagerPluginService{err: providerErr}})
				_, err := NewClient(&fakeManagerConnectionRPCNode{handler: adapter.HandleManagerPluginRPC}).ListManagerPlugins(context.Background(), 2)
				return err
			},
		},
		{
			name: "Latest messages invalid", providerErr: metadb.ErrInvalidArgument, wantErr: metadb.ErrInvalidArgument,
			call: func(providerErr error) error {
				adapter := New(Options{ManagerLatestMessages: &fakeManagerLatestMessageReader{err: providerErr}})
				_, err := NewClient(&fakeManagerConnectionRPCNode{handler: adapter.HandleManagerLatestMessagesRPC}).ListManagerLatestMessages(context.Background(), 2, 0, 10)
				return err
			},
		},
		{
			name: "Latest messages backpressured", providerErr: managementusecase.ErrLatestMessagesBackpressured, wantErr: managementusecase.ErrLatestMessagesBackpressured,
			call: func(providerErr error) error {
				adapter := New(Options{ManagerLatestMessages: &fakeManagerLatestMessageReader{err: providerErr}})
				_, err := NewClient(&fakeManagerConnectionRPCNode{handler: adapter.HandleManagerLatestMessagesRPC}).ListManagerLatestMessages(context.Background(), 2, 0, 10)
				return err
			},
		},
		{
			name: "Latest messages unavailable", providerErr: providerFailure, wantErr: managementusecase.ErrLatestMessagesUnavailable,
			call: func(providerErr error) error {
				adapter := New(Options{ManagerLatestMessages: &fakeManagerLatestMessageReader{err: providerErr}})
				_, err := NewClient(&fakeManagerConnectionRPCNode{handler: adapter.HandleManagerLatestMessagesRPC}).ListManagerLatestMessages(context.Background(), 2, 0, 10)
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call(tt.providerErr)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("remote read error = %v, want stable %v", err, tt.wantErr)
			}
			if tt.providerErr == providerFailure && strings.Contains(err.Error(), providerFailure.Error()) {
				t.Fatalf("remote read leaked provider detail: %v", err)
			}
		})
	}
}

func TestManagerControllerAuditAndDBInspectRPCsMapProviderFailuresEndToEnd(t *testing.T) {
	privateFailure := errors.New("private storage failure")
	t.Run("Controller task audit", func(t *testing.T) {
		tests := []struct {
			name        string
			providerErr error
			wantErr     error
		}{
			{name: "canceled", providerErr: context.Canceled, wantErr: context.Canceled},
			{name: "deadline", providerErr: context.DeadlineExceeded, wantErr: context.DeadlineExceeded},
			{name: "invalid", providerErr: metadb.ErrInvalidArgument, wantErr: metadb.ErrInvalidArgument},
			{name: "not found", providerErr: managementusecase.ErrControllerTaskAuditNotFound, wantErr: managementusecase.ErrControllerTaskAuditNotFound},
			{name: "unavailable", providerErr: managementusecase.ErrControllerTaskAuditUnavailable, wantErr: managementusecase.ErrControllerTaskAuditUnavailable},
			{name: "rejected", providerErr: privateFailure},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				reader := &fakeManagerTaskAuditReader{err: tt.providerErr}
				adapter := New(Options{ManagerTaskAudit: reader})
				_, err := NewClient(&fakeManagerConnectionRPCNode{handler: adapter.HandleManagerTaskAuditRPC}).ListManagerControllerTaskAudits(
					context.Background(), 2, managementusecase.ControllerTaskAuditListRequest{NodeID: 2, Limit: 20},
				)
				if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
					t.Fatalf("ListManagerControllerTaskAudits() error = %v, want %v", err, tt.wantErr)
				}
				if tt.wantErr == nil && err == nil {
					t.Fatal("ListManagerControllerTaskAudits() error = nil, want fail-closed rejection")
				}
				if strings.Contains(err.Error(), privateFailure.Error()) {
					t.Fatalf("task audit RPC leaked provider detail: %v", err)
				}
			})
		}
	})

	t.Run("DB inspect", func(t *testing.T) {
		tests := []struct {
			name        string
			providerErr error
			wantErr     error
		}{
			{name: "canceled", providerErr: context.Canceled, wantErr: context.Canceled},
			{name: "deadline", providerErr: context.DeadlineExceeded, wantErr: context.DeadlineExceeded},
			{name: "invalid query", providerErr: dbinspect.ErrInvalidQuery, wantErr: metadb.ErrInvalidArgument},
			{name: "cursor mismatch", providerErr: dbinspect.ErrCursorMismatch, wantErr: dbinspect.ErrCursorMismatch},
			{name: "unavailable", providerErr: managementusecase.ErrDBInspectUnavailable, wantErr: managementusecase.ErrDBInspectUnavailable},
			{name: "rejected", providerErr: privateFailure},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				reader := &fakeManagerDBInspectReader{err: tt.providerErr}
				adapter := New(Options{ManagerDBInspect: reader})
				_, err := NewClient(&fakeManagerConnectionRPCNode{handler: adapter.HandleManagerDBInspectRPC}).NodeDBInspectQuery(
					context.Background(), managementusecase.DBInspectQueryRequest{NodeID: 2},
				)
				if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
					t.Fatalf("NodeDBInspectQuery() error = %v, want %v", err, tt.wantErr)
				}
				if tt.wantErr == nil && err == nil {
					t.Fatal("NodeDBInspectQuery() error = nil, want fail-closed rejection")
				}
				if strings.Contains(err.Error(), privateFailure.Error()) {
					t.Fatalf("DB inspect RPC leaked provider detail: %v", err)
				}
			})
		}
	})
}

type nodeRPCContractCall struct {
	nodeID    uint64
	serviceID uint8
}

type nodeRPCContractTransport struct {
	callerNodeID uint64
	handlers     map[uint8]func(context.Context, []byte) ([]byte, error)
	calls        []nodeRPCContractCall
}

func (n *nodeRPCContractTransport) NodeID() uint64 {
	return n.callerNodeID
}

func (n *nodeRPCContractTransport) CallRPC(
	ctx context.Context,
	nodeID uint64,
	serviceID uint8,
	payload []byte,
) ([]byte, error) {
	n.calls = append(n.calls, nodeRPCContractCall{nodeID: nodeID, serviceID: serviceID})
	handler := n.handlers[serviceID]
	if handler == nil {
		return nil, fmt.Errorf("unexpected RPC service %d", serviceID)
	}
	return handler(ctx, payload)
}

type nodeRPCAuditReader struct {
	limit   int
	entries []opscontract.AuditEntry
}

func (r *nodeRPCAuditReader) RecentAudits(
	_ context.Context,
	limit int,
) ([]opscontract.AuditEntry, error) {
	r.limit = limit
	return append([]opscontract.AuditEntry(nil), r.entries...), nil
}
