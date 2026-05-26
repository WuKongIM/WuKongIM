package manager

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func TestManagerChannelLeaderTransferRoute(t *testing.T) {
	var gotID channel.ChannelID
	var gotReq managementusecase.TransferChannelLeaderRequest
	srv := New(Options{Management: channelMigrationHTTPStub{
		transferIDSink:  &gotID,
		transferReqSink: &gotReq,
		transferResult:  channelMigrationHTTPResult(managementusecase.ChannelMigrationKindLeaderTransfer),
	}})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/channels/2/room-1/leader/transfer", bytes.NewBufferString(`{"target_node_id":3,"dry_run":true}`))
	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, channel.ChannelID{ID: "room-1", Type: 2}, gotID)
	require.Equal(t, managementusecase.TransferChannelLeaderRequest{TargetNodeID: 3, DryRun: true}, gotReq)
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, true, body["dry_run"])
	require.Equal(t, true, body["valid"])
	require.Equal(t, managementusecase.ChannelMigrationKindLeaderTransfer, body["kind"])
	require.Equal(t, []any{"validate", "probe_target"}, body["phase_sequence"])
	require.Equal(t, "task-http", body["task_id"])
}

func TestManagerChannelReplicaMigrationRoute(t *testing.T) {
	var gotID channel.ChannelID
	var gotReq managementusecase.MigrateChannelReplicaRequest
	result := channelMigrationHTTPResult(managementusecase.ChannelMigrationKindReplicaReplace)
	result.Valid = false
	result.Blockers = []string{"target_node_not_alive"}
	srv := New(Options{Management: channelMigrationHTTPStub{
		replicaIDSink:  &gotID,
		replicaReqSink: &gotReq,
		replicaResult:  result,
	}})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/channels/2/room-1/replicas/migrate", bytes.NewBufferString(`{"source_node_id":2,"target_node_id":4,"dry_run":true}`))
	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, channel.ChannelID{ID: "room-1", Type: 2}, gotID)
	require.Equal(t, managementusecase.MigrateChannelReplicaRequest{SourceNodeID: 2, TargetNodeID: 4, DryRun: true}, gotReq)
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, false, body["valid"])
	require.Equal(t, []any{"target_node_not_alive"}, body["blockers"])
}

func TestManagerChannelMigrationDetailAndAbortRoutes(t *testing.T) {
	var getID channel.ChannelID
	var abortID channel.ChannelID
	var abortTaskID string
	detail := channelMigrationHTTPDetail(managementusecase.ChannelMigrationKindReplicaReplace)
	srv := New(Options{Management: channelMigrationHTTPStub{
		getIDSink:     &getID,
		getDetail:     detail,
		abortIDSink:   &abortID,
		abortTaskSink: &abortTaskID,
		abortDetail:   detail,
	}})

	getRec := httptest.NewRecorder()
	getReq := httptest.NewRequest(http.MethodGet, "/manager/channels/2/room-1/migration", nil)
	srv.Engine().ServeHTTP(getRec, getReq)

	require.Equal(t, http.StatusOK, getRec.Code)
	require.Equal(t, channel.ChannelID{ID: "room-1", Type: 2}, getID)
	var getBody map[string]any
	require.NoError(t, json.Unmarshal(getRec.Body.Bytes(), &getBody))
	require.Equal(t, "task-http", getBody["task_id"])
	require.Equal(t, float64(100), getBody["leader_leo"])
	require.Equal(t, true, getBody["fence_active"])

	abortRec := httptest.NewRecorder()
	abortReq := httptest.NewRequest(http.MethodPost, "/manager/channels/2/room-1/migration/task-http/abort", nil)
	srv.Engine().ServeHTTP(abortRec, abortReq)

	require.Equal(t, http.StatusOK, abortRec.Code)
	require.Equal(t, channel.ChannelID{ID: "room-1", Type: 2}, abortID)
	require.Equal(t, "task-http", abortTaskID)
}

func TestManagerChannelMigrationRouteErrors(t *testing.T) {
	t.Run("invalid channel type", func(t *testing.T) {
		srv := New(Options{Management: channelMigrationHTTPStub{}})
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/manager/channels/bad/room-1/leader/transfer", bytes.NewBufferString(`{"target_node_id":3}`))
		srv.Engine().ServeHTTP(rec, req)
		require.Equal(t, http.StatusBadRequest, rec.Code)
		require.JSONEq(t, `{"error":"bad_request","message":"invalid channel_type"}`, rec.Body.String())
	})

	t.Run("invalid body", func(t *testing.T) {
		srv := New(Options{Management: channelMigrationHTTPStub{}})
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/manager/channels/2/room-1/replicas/migrate", bytes.NewBufferString(`{`))
		srv.Engine().ServeHTTP(rec, req)
		require.Equal(t, http.StatusBadRequest, rec.Code)
		require.JSONEq(t, `{"error":"bad_request","message":"invalid body"}`, rec.Body.String())
	})

	t.Run("zero target", func(t *testing.T) {
		srv := New(Options{Management: channelMigrationHTTPStub{}})
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/manager/channels/2/room-1/leader/transfer", bytes.NewBufferString(`{"target_node_id":0}`))
		srv.Engine().ServeHTTP(rec, req)
		require.Equal(t, http.StatusBadRequest, rec.Code)
		require.JSONEq(t, `{"error":"bad_request","message":"invalid target_node_id"}`, rec.Body.String())
	})

	t.Run("zero source", func(t *testing.T) {
		srv := New(Options{Management: channelMigrationHTTPStub{}})
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/manager/channels/2/room-1/replicas/migrate", bytes.NewBufferString(`{"source_node_id":0,"target_node_id":4}`))
		srv.Engine().ServeHTTP(rec, req)
		require.Equal(t, http.StatusBadRequest, rec.Code)
		require.JSONEq(t, `{"error":"bad_request","message":"invalid source_node_id"}`, rec.Body.String())
	})

	t.Run("channel type out of range", func(t *testing.T) {
		srv := New(Options{Management: channelMigrationHTTPStub{}})
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/manager/channels/256/room-1/migration", nil)
		srv.Engine().ServeHTTP(rec, req)
		require.Equal(t, http.StatusBadRequest, rec.Code)
		require.JSONEq(t, `{"error":"bad_request","message":"invalid channel_type"}`, rec.Body.String())
	})

	t.Run("usecase invalid argument", func(t *testing.T) {
		srv := New(Options{Management: channelMigrationHTTPStub{transferErr: metadb.ErrInvalidArgument}})
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/manager/channels/2/room-1/leader/transfer", bytes.NewBufferString(`{"target_node_id":3}`))
		srv.Engine().ServeHTTP(rec, req)
		require.Equal(t, http.StatusBadRequest, rec.Code)
		require.JSONEq(t, `{"error":"bad_request","message":"invalid channel migration request"}`, rec.Body.String())
	})

	t.Run("usecase blockers are returned", func(t *testing.T) {
		result := channelMigrationHTTPResult(managementusecase.ChannelMigrationKindReplicaReplace)
		result.Valid = false
		result.Blockers = []string{"active_task_exists"}
		srv := New(Options{Management: channelMigrationHTTPStub{replicaResult: result, replicaErr: metadb.ErrInvalidArgument}})
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/manager/channels/2/room-1/replicas/migrate", bytes.NewBufferString(`{"source_node_id":2,"target_node_id":4}`))
		srv.Engine().ServeHTTP(rec, req)
		require.Equal(t, http.StatusBadRequest, rec.Code)
		var body map[string]any
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		require.Equal(t, false, body["valid"])
		require.Equal(t, []any{"active_task_exists"}, body["blockers"])
	})

	t.Run("not found", func(t *testing.T) {
		srv := New(Options{Management: channelMigrationHTTPStub{getErr: metadb.ErrNotFound}})
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/manager/channels/2/room-1/migration", nil)
		srv.Engine().ServeHTTP(rec, req)
		require.Equal(t, http.StatusNotFound, rec.Code)
		require.JSONEq(t, `{"error":"not_found","message":"channel migration or channel not found"}`, rec.Body.String())
	})

	t.Run("stale conflict", func(t *testing.T) {
		srv := New(Options{Management: channelMigrationHTTPStub{abortErr: metadb.ErrStaleMeta}})
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/manager/channels/2/room-1/migration/task-http/abort", nil)
		srv.Engine().ServeHTTP(rec, req)
		require.Equal(t, http.StatusConflict, rec.Code)
		require.JSONEq(t, `{"error":"conflict","message":"stale channel migration state"}`, rec.Body.String())
	})

	t.Run("leader unavailable", func(t *testing.T) {
		srv := New(Options{Management: channelMigrationHTTPStub{getErr: raftcluster.ErrNoLeader}})
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/manager/channels/2/room-1/migration", nil)
		srv.Engine().ServeHTTP(rec, req)
		require.Equal(t, http.StatusServiceUnavailable, rec.Code)
		require.JSONEq(t, `{"error":"service_unavailable","message":"channel migration unavailable"}`, rec.Body.String())
	})

	t.Run("deadline unavailable", func(t *testing.T) {
		srv := New(Options{Management: channelMigrationHTTPStub{getErr: context.DeadlineExceeded}})
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/manager/channels/2/room-1/migration", nil)
		srv.Engine().ServeHTTP(rec, req)
		require.Equal(t, http.StatusServiceUnavailable, rec.Code)
		require.JSONEq(t, `{"error":"service_unavailable","message":"channel migration unavailable"}`, rec.Body.String())
	})
}

func TestManagerChannelMigrationRoutesRequirePermissions(t *testing.T) {
	t.Run("read route requires read permission", func(t *testing.T) {
		srv := New(Options{
			Auth: testAuthConfig([]UserConfig{{
				Username: "writer",
				Password: "secret",
				Permissions: []PermissionConfig{{
					Resource: "cluster.channel",
					Actions:  []string{"w"},
				}},
			}}),
			Management: channelMigrationHTTPStub{getDetail: channelMigrationHTTPDetail(managementusecase.ChannelMigrationKindReplicaReplace)},
		})
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/manager/channels/2/room-1/migration", nil)
		req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "writer"))
		srv.Engine().ServeHTTP(rec, req)
		require.Equal(t, http.StatusForbidden, rec.Code)
	})

	t.Run("write route requires write permission", func(t *testing.T) {
		srv := New(Options{
			Auth: testAuthConfig([]UserConfig{{
				Username: "reader",
				Password: "secret",
				Permissions: []PermissionConfig{{
					Resource: "cluster.channel",
					Actions:  []string{"r"},
				}},
			}}),
			Management: channelMigrationHTTPStub{transferResult: channelMigrationHTTPResult(managementusecase.ChannelMigrationKindLeaderTransfer)},
		})
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/manager/channels/2/room-1/leader/transfer", bytes.NewBufferString(`{"target_node_id":3}`))
		req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "reader"))
		srv.Engine().ServeHTTP(rec, req)
		require.Equal(t, http.StatusForbidden, rec.Code)
	})
}

type channelMigrationHTTPStub struct {
	managementStub
	transferIDSink  *channel.ChannelID
	transferReqSink *managementusecase.TransferChannelLeaderRequest
	transferResult  managementusecase.ChannelMigrationResult
	transferErr     error
	replicaIDSink   *channel.ChannelID
	replicaReqSink  *managementusecase.MigrateChannelReplicaRequest
	replicaResult   managementusecase.ChannelMigrationResult
	replicaErr      error
	getIDSink       *channel.ChannelID
	getDetail       managementusecase.ChannelMigrationDetail
	getErr          error
	abortIDSink     *channel.ChannelID
	abortTaskSink   *string
	abortDetail     managementusecase.ChannelMigrationDetail
	abortErr        error
}

func (s channelMigrationHTTPStub) TransferChannelLeader(_ context.Context, id channel.ChannelID, req managementusecase.TransferChannelLeaderRequest) (managementusecase.ChannelMigrationResult, error) {
	if s.transferIDSink != nil {
		*s.transferIDSink = id
	}
	if s.transferReqSink != nil {
		*s.transferReqSink = req
	}
	return s.transferResult, s.transferErr
}

func (s channelMigrationHTTPStub) MigrateChannelReplica(_ context.Context, id channel.ChannelID, req managementusecase.MigrateChannelReplicaRequest) (managementusecase.ChannelMigrationResult, error) {
	if s.replicaIDSink != nil {
		*s.replicaIDSink = id
	}
	if s.replicaReqSink != nil {
		*s.replicaReqSink = req
	}
	return s.replicaResult, s.replicaErr
}

func (s channelMigrationHTTPStub) GetChannelMigration(_ context.Context, id channel.ChannelID) (managementusecase.ChannelMigrationDetail, error) {
	if s.getIDSink != nil {
		*s.getIDSink = id
	}
	return s.getDetail, s.getErr
}

func (s channelMigrationHTTPStub) AbortChannelMigration(_ context.Context, id channel.ChannelID, taskID string) (managementusecase.ChannelMigrationDetail, error) {
	if s.abortIDSink != nil {
		*s.abortIDSink = id
	}
	if s.abortTaskSink != nil {
		*s.abortTaskSink = taskID
	}
	return s.abortDetail, s.abortErr
}

func channelMigrationHTTPResult(kind string) managementusecase.ChannelMigrationResult {
	detail := channelMigrationHTTPDetail(kind)
	return managementusecase.ChannelMigrationResult{
		DryRun:        true,
		Valid:         true,
		TaskID:        detail.TaskID,
		Kind:          kind,
		PhaseSequence: []string{"validate", "probe_target"},
		Detail:        detail,
	}
}

func channelMigrationHTTPDetail(kind string) managementusecase.ChannelMigrationDetail {
	return managementusecase.ChannelMigrationDetail{
		TaskID:              "task-http",
		Kind:                kind,
		Status:              "running",
		Phase:               "warm_catch_up",
		ChannelID:           "room-1",
		ChannelType:         2,
		SourceNode:          2,
		TargetNode:          4,
		DesiredLeader:       3,
		BaseChannelEpoch:    5,
		BaseLeaderEpoch:     7,
		CurrentChannelEpoch: 6,
		CurrentLeaderEpoch:  8,
		Progress: metadb.ChannelMigrationProgress{
			LeaderLEO:          100,
			LeaderHW:           98,
			TargetLEO:          95,
			TargetCheckpointHW: 94,
			LagRecords:         5,
			StableSinceMS:      1750000001000,
		},
		FenceActive:    true,
		FenceUntilMS:   1750000005000,
		FenceReason:    1,
		BlockerCode:    metadb.ChannelMigrationBlockerNeedsSnapshotBootstrap,
		BlockerMessage: "snapshot required",
		Attempt:        2,
		NextRunAtMS:    1750000006000,
		LastError:      "target lagging",
		CreatedAtMS:    1750000000000,
		UpdatedAtMS:    1750000002000,
	}
}
