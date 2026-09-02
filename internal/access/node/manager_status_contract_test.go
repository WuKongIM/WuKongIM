package node

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/contracts/channelappend"
	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	pluginusecase "github.com/WuKongIM/WuKongIM/internal/usecase/plugin"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestManagerRPCErrorClassesHaveStableWireStatuses(t *testing.T) {
	privateFailure := errors.New("private implementation failure")
	tests := []struct {
		name   string
		mapper func(error) string
		cases  []nodeRPCErrorStatusCase
	}{
		{
			name: "controller raft", mapper: managerControllerRaftRPCStatusForError,
			cases: []nodeRPCErrorStatusCase{
				{want: rpcStatusOK},
				{err: context.Canceled, want: rpcStatusContextCanceled},
				{err: context.DeadlineExceeded, want: rpcStatusContextDeadlineExceeded},
				{err: managementusecase.ErrControllerRaftOperatorUnavailable, want: rpcStatusRejected},
				{err: privateFailure, want: rpcStatusRejected},
			},
		},
		{
			name: "slot raft", mapper: managerSlotRaftRPCStatusForError,
			cases: []nodeRPCErrorStatusCase{
				{want: rpcStatusOK},
				{err: context.Canceled, want: rpcStatusContextCanceled},
				{err: context.DeadlineExceeded, want: rpcStatusContextDeadlineExceeded},
				{err: metadb.ErrNotFound, want: rpcStatusNotFound},
				{err: clusterpkg.ErrSlotNotFound, want: rpcStatusNotFound},
				{err: metadb.ErrInvalidArgument, want: rpcStatusRejected},
				{err: managementusecase.ErrSlotRaftOperatorUnavailable, want: rpcStatusRejected},
				{err: privateFailure, want: rpcStatusRejected},
			},
		},
		{
			name: "distributed log", mapper: managerLogRPCStatusForError,
			cases: []nodeRPCErrorStatusCase{
				{want: rpcStatusOK},
				{err: context.Canceled, want: rpcStatusContextCanceled},
				{err: context.DeadlineExceeded, want: rpcStatusContextDeadlineExceeded},
				{err: metadb.ErrNotFound, want: rpcStatusNotFound},
				{err: clusterpkg.ErrSlotNotFound, want: rpcStatusNotFound},
				{err: metadb.ErrInvalidArgument, want: rpcStatusRejected},
				{err: managementusecase.ErrLogReaderUnavailable, want: rpcStatusRejected},
				{err: privateFailure, want: rpcStatusRejected},
			},
		},
		{
			name: "business channel", mapper: managerChannelRPCStatusForError,
			cases: []nodeRPCErrorStatusCase{
				{want: rpcStatusOK},
				{err: context.Canceled, want: rpcStatusContextCanceled},
				{err: context.DeadlineExceeded, want: rpcStatusContextDeadlineExceeded},
				{err: metadb.ErrInvalidArgument, want: rpcStatusRejected},
				{err: managementusecase.ErrBusinessChannelReaderUnavailable, want: rpcStatusRejected},
				{err: privateFailure, want: rpcStatusRejected},
			},
		},
		{
			name: "connection", mapper: managerConnectionRPCStatusForError,
			cases: []nodeRPCErrorStatusCase{
				{want: rpcStatusOK},
				{err: context.Canceled, want: rpcStatusContextCanceled},
				{err: context.DeadlineExceeded, want: rpcStatusContextDeadlineExceeded},
				{err: metadb.ErrNotFound, want: rpcStatusNotFound},
				{err: metadb.ErrInvalidArgument, want: rpcStatusInvalidArgument},
				{err: managementusecase.ErrConnectionReaderUnavailable, want: rpcStatusRejected},
				{err: managementusecase.ErrNodeScaleInUnavailable, want: rpcStatusRejected},
				{err: privateFailure, want: rpcStatusRejected},
			},
		},
		{
			name: "application log", mapper: managerAppLogRPCStatusForError,
			cases: []nodeRPCErrorStatusCase{
				{want: rpcStatusOK},
				{err: context.Canceled, want: rpcStatusContextCanceled},
				{err: context.DeadlineExceeded, want: rpcStatusContextDeadlineExceeded},
				{err: metadb.ErrNotFound, want: rpcStatusNotFound},
				{err: metadb.ErrInvalidArgument, want: rpcStatusRejected},
				{err: managementusecase.ErrApplicationLogReaderUnavailable, want: rpcStatusRejected},
				{err: privateFailure, want: rpcStatusRejected},
			},
		},
		{
			name: "latest messages", mapper: managerLatestMessagesStatusForError,
			cases: []nodeRPCErrorStatusCase{
				{want: rpcStatusOK},
				{err: context.Canceled, want: rpcStatusContextCanceled},
				{err: context.DeadlineExceeded, want: rpcStatusContextDeadlineExceeded},
				{err: metadb.ErrInvalidArgument, want: rpcStatusInvalidArgument},
				{err: managementusecase.ErrLatestMessagesBackpressured, want: rpcStatusBackpressured},
				{err: privateFailure, want: rpcStatusRejected},
			},
		},
		{
			name: "node config", mapper: managerNodeConfigRPCStatusForError,
			cases: []nodeRPCErrorStatusCase{
				{want: rpcStatusOK},
				{err: context.Canceled, want: rpcStatusContextCanceled},
				{err: context.DeadlineExceeded, want: rpcStatusContextDeadlineExceeded},
				{err: metadb.ErrInvalidArgument, want: rpcStatusInvalidArgument},
				{err: metadb.ErrNotFound, want: rpcStatusNotFound},
				{err: managementusecase.ErrNodeConfigUnavailable, want: rpcStatusUnavailable},
				{err: privateFailure, want: rpcStatusRejected},
			},
		},
		{
			name: "plugin", mapper: managerPluginRPCStatusForError,
			cases: []nodeRPCErrorStatusCase{
				{want: rpcStatusOK},
				{err: context.Canceled, want: rpcStatusContextCanceled},
				{err: context.DeadlineExceeded, want: rpcStatusContextDeadlineExceeded},
				{err: pluginusecase.ErrPluginNotFound, want: rpcStatusNotFound},
				{err: pluginusecase.ErrPluginNoRequired, want: rpcStatusRejected},
				{err: managementusecase.ErrPluginNodeIDRequired, want: rpcStatusRejected},
				{err: managementusecase.ErrPluginNodeUnavailable, want: rpcStatusRejected},
				{err: privateFailure, want: rpcStatusRejected},
			},
		},
		{
			name: "message retention", mapper: managerMessageRetentionRPCStatusForError,
			cases: []nodeRPCErrorStatusCase{
				{want: rpcStatusOK},
				{err: context.Canceled, want: rpcStatusContextCanceled},
				{err: context.DeadlineExceeded, want: rpcStatusContextDeadlineExceeded},
				{err: channelappend.ErrNotLeader, want: rpcStatusNotLeader},
				{err: channelappend.ErrStaleRoute, want: rpcStatusStaleRoute},
				{err: channelappend.ErrRouteNotReady, want: rpcStatusRouteNotReady},
				{err: channelappend.ErrChannelNotFound, want: rpcStatusNotFound},
				{err: metadb.ErrInvalidArgument, want: rpcStatusRejected},
				{err: managementusecase.ErrMessageRetentionUnavailable, want: rpcStatusRejected},
				{err: privateFailure, want: rpcStatusRejected},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for _, testCase := range test.cases {
				if got := test.mapper(testCase.err); got != testCase.want {
					t.Errorf("status(%v) = %q, want %q", testCase.err, got, testCase.want)
				}
			}
		})
	}
}

func TestManagerRPCWireStatusesMapToStableCallerErrors(t *testing.T) {
	tests := []struct {
		name   string
		mapper func(string) error
		cases  []nodeRPCStatusErrorCase
	}{
		{
			name: "controller raft", mapper: managerControllerRaftRPCErrorForStatus,
			cases: commonManagerStatusCases(managementusecase.ErrControllerRaftOperatorUnavailable, nil),
		},
		{
			name: "slot raft", mapper: managerSlotRaftRPCErrorForStatus,
			cases: commonManagerStatusCases(managementusecase.ErrSlotRaftOperatorUnavailable, clusterpkg.ErrSlotNotFound),
		},
		{
			name: "distributed log", mapper: managerLogRPCErrorForStatus,
			cases: commonManagerStatusCases(managementusecase.ErrLogReaderUnavailable, metadb.ErrNotFound),
		},
		{
			name: "business channel", mapper: managerChannelRPCErrorForStatus,
			cases: commonManagerStatusCases(managementusecase.ErrBusinessChannelReaderUnavailable, nil),
		},
		{
			name: "connection", mapper: managerConnectionRPCErrorForStatus,
			cases: append(commonManagerStatusCases(managementusecase.ErrConnectionReaderUnavailable, metadb.ErrNotFound),
				nodeRPCStatusErrorCase{status: rpcStatusInvalidArgument, want: metadb.ErrInvalidArgument}),
		},
		{
			name: "application log", mapper: managerAppLogRPCErrorForStatus,
			cases: commonManagerStatusCases(managementusecase.ErrApplicationLogReaderUnavailable, metadb.ErrNotFound),
		},
		{
			name: "latest messages", mapper: managerLatestMessagesErrorForStatus,
			cases: append(commonManagerStatusCases(managementusecase.ErrLatestMessagesUnavailable, nil),
				nodeRPCStatusErrorCase{status: rpcStatusInvalidArgument, want: metadb.ErrInvalidArgument},
				nodeRPCStatusErrorCase{status: rpcStatusBackpressured, want: managementusecase.ErrLatestMessagesBackpressured}),
		},
		{
			name: "node config", mapper: managerNodeConfigRPCErrorForStatus,
			cases: append(commonManagerStatusCases(managementusecase.ErrNodeConfigUnavailable, metadb.ErrNotFound),
				nodeRPCStatusErrorCase{status: rpcStatusInvalidArgument, want: metadb.ErrInvalidArgument},
				nodeRPCStatusErrorCase{status: rpcStatusUnavailable, want: managementusecase.ErrNodeConfigUnavailable}),
		},
		{
			name: "plugin", mapper: managerPluginRPCErrorForStatus,
			cases: commonManagerStatusCases(managementusecase.ErrPluginNodeUnavailable, pluginusecase.ErrPluginNotFound),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for _, testCase := range test.cases {
				got := test.mapper(testCase.status)
				switch {
				case testCase.want == nil && testCase.status == rpcStatusOK && got != nil:
					t.Errorf("error(%q) = %v, want nil", testCase.status, got)
				case testCase.want != nil && !errors.Is(got, testCase.want):
					t.Errorf("error(%q) = %v, want stable %v", testCase.status, got, testCase.want)
				case testCase.status == "future_status" && got == nil:
					t.Errorf("error(%q) = nil, want fail-closed unknown status", testCase.status)
				}
			}
		})
	}

	if err := managerConnectionRPCDrainErrorForStatus(rpcStatusRejected); !errors.Is(err, managementusecase.ErrNodeScaleInUnavailable) {
		t.Fatalf("drain rejected error = %v, want ErrNodeScaleInUnavailable", err)
	}
	if err := managerConnectionRPCDrainErrorForStatus(rpcStatusContextCanceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("drain canceled error = %v, want context.Canceled", err)
	}
}

type nodeRPCErrorStatusCase struct {
	err  error
	want string
}

type nodeRPCStatusErrorCase struct {
	status string
	want   error
}

func commonManagerStatusCases(rejected, notFound error) []nodeRPCStatusErrorCase {
	cases := []nodeRPCStatusErrorCase{
		{status: rpcStatusOK},
		{status: rpcStatusContextCanceled, want: context.Canceled},
		{status: rpcStatusContextDeadlineExceeded, want: context.DeadlineExceeded},
	}
	if notFound != nil {
		cases = append(cases, nodeRPCStatusErrorCase{status: rpcStatusNotFound, want: notFound})
	}
	if rejected != nil {
		cases = append(cases, nodeRPCStatusErrorCase{status: rpcStatusRejected, want: rejected})
	}
	return append(cases, nodeRPCStatusErrorCase{status: "future_status"})
}
