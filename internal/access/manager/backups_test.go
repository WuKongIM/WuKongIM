package manager

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	"github.com/stretchr/testify/require"
)

func TestManagerBackupStatusExposesOnlyContinuousModel(t *testing.T) {
	age := int64(42)
	provider := &fakeBackupManagement{status: backupusecase.StatusSnapshot{
		Enabled: true, Health: backupusecase.HealthHealthy,
		CheckpointAgeSeconds: &age,
		LatestCheckpoint: &backupusecase.CheckpointSummary{
			ID: "checkpoint-2", CreatedAtUnixMillis: 200,
			EffectiveAtUnixMillis: 190,
		},
		FailureCategory: "checkpoint",
		Policy: backupusecase.PolicySnapshot{
			CaptureReconcileIntervalSeconds: 1,
			CheckpointIntervalSeconds:       60,
			CaptureWorkerCount:              8, StagingMaxBytes: 1024,
		},
		ErasureStreams: []backupusecase.ErasureStreamProgress{{
			HashSlot: 17, Sequence: 9, Pending: true,
		}},
		CaptureLeases: []backupusecase.CaptureLeaseSnapshot{{
			HashSlot: 17, SlotID: 3, SourceSlotID: 2,
			HolderNodeID: 2, LeaderTerm: 11, ConfigEpoch: 5,
			Generation: "slot-generation-1", LeaseSequence: 4,
			LastPromotionPreviousGeneration: "slot-generation-0",
			LastPromotionReason:             "audit_corruption",
			LastPromotionAtUnixMillis:       123,
		}},
		IntegrityAudit: backupusecase.IntegrityAuditSnapshot{
			Revision: 12, DebtObjects: 7,
			Cursor: &backupusecase.IntegrityAuditCursorSnapshot{
				CycleID: "catalog-segments-12", ScrubEpoch: 4,
				CatalogSequence: 12, HashSlot: 17,
				Generation:          "slot-generation-1",
				Phase:               backupcontract.IntegrityAuditPhaseRebase,
				Category:            backupcontract.IntegrityCorruptionCiphertext,
				UpdatedAtUnixMillis: 456,
			},
			Slots: []backupusecase.SlotIntegrityAuditSnapshot{{
				HashSlot: 17, Generation: "slot-generation-1",
				Health:              backupcontract.SlotAuditRebaseRequired,
				Category:            backupcontract.IntegrityCorruptionCiphertext,
				UpdatedAtUnixMillis: 456,
			}},
			UpdatedAtUnixMillis: 456,
		},
	}}
	srv := New(Options{Backup: provider})
	recorder := httptest.NewRecorder()
	srv.Engine().ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodGet, "/manager/backups/status", nil),
	)
	require.Equal(t, http.StatusOK, recorder.Code)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &decoded))
	require.Equal(t, float64(42), decoded["checkpoint_age_seconds"])
	require.Equal(t, "checkpoint-2",
		decoded["latest_checkpoint"].(map[string]any)["id"])
	lease := decoded["capture_leases"].([]any)[0].(map[string]any)
	require.Equal(
		t, "slot-generation-0",
		lease["last_promotion_previous_generation"],
	)
	require.Equal(t, "audit_corruption", lease["last_promotion_reason"])
	require.Equal(t, float64(123), lease["last_promotion_at_unix_millis"])
	audit := decoded["integrity_audit"].(map[string]any)
	require.Equal(t, float64(12), audit["revision"])
	require.Equal(t, float64(7), audit["debt_objects"])
	cursor := audit["cursor"].(map[string]any)
	require.Equal(t, "rebase", cursor["phase"])
	require.NotContains(t, cursor, "position")
	slot := audit["slots"].([]any)[0].(map[string]any)
	require.Equal(t, "rebase_required", slot["health"])
	for _, removed := range []string{
		"active", "latest", "verification", "dependencies", "capacity",
		"recovery_point_age_seconds", "verification_age_seconds",
		"pending_garbage_count", "target_segment_bytes",
		"object_lock_days", "primary_region", "key_authority",
		"active_signing_key_id", "active_wrapping_key_id",
	} {
		require.NotContains(t, decoded, removed)
	}
	require.NotContains(t, recorder.Body.String(), "commit_sha256")
}

func TestManagerBackupCheckpointWritePermission(t *testing.T) {
	provider := &fakeBackupManagement{
		publication: backupusecase.CheckpointPublication{
			Checkpoint:       backupusecase.CheckpointSummary{ID: "checkpoint-3"},
			CheckpointSHA256: strings.Repeat("a", 64),
		},
	}
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{
			{Username: "reader", Password: "secret", Permissions: []PermissionConfig{{Resource: "cluster.backup", Actions: []string{"r"}}}},
			{Username: "writer", Password: "secret", Permissions: []PermissionConfig{{Resource: "cluster.backup", Actions: []string{"w"}}}},
		}),
		Backup: provider,
	})
	request := func(user string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(
			http.MethodPost, "/manager/backups/checkpoints", nil,
		)
		req.Header.Set(
			"Authorization", "Bearer "+mustIssueTestToken(t, srv, user),
		)
		srv.Engine().ServeHTTP(recorder, req)
		return recorder
	}
	require.Equal(t, http.StatusForbidden, request("reader").Code)
	require.False(t, provider.published)
	writer := request("writer")
	require.Equal(t, http.StatusCreated, writer.Code)
	require.Contains(t, writer.Body.String(),
		`"checkpoint_sha256":"`+strings.Repeat("a", 64)+`"`)
	require.NotContains(t, writer.Body.String(), "manifest_sha256")
	require.True(t, provider.published)
}

func TestManagerSourceFenceRequiresExplicitGrantAndBindsSuccessor(t *testing.T) {
	provider := &fakeBackupManagement{
		sourceFenceReceipt: backupusecase.SourceFenceReceipt{
			SourceFenceRecord: backupartifact.SourceFenceRecord{
				ID: "source-fence-1",
			},
		},
	}
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{
			{Username: "wildcard-admin", Password: "secret", Permissions: []PermissionConfig{{Resource: "*", Actions: []string{"*"}}}},
			{Username: "source-fencer", Password: "secret", Permissions: []PermissionConfig{{Resource: "cluster.backup.source_fence", Actions: []string{"w"}}}},
		}),
		Backup: provider,
	})
	body := `{"restore_plan_id":" plan-1 ","checkpoint_id":" checkpoint-1 ","target_cluster_id":" target-cluster ","target_generation":" target-generation-1 "}`
	call := func(user string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(
			http.MethodPost, "/manager/backups/source-fence",
			bytes.NewBufferString(body),
		)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set(
			"Authorization", "Bearer "+mustIssueTestToken(t, srv, user),
		)
		srv.Engine().ServeHTTP(recorder, request)
		return recorder
	}
	require.Equal(t, http.StatusForbidden, call("wildcard-admin").Code)
	allowed := call("source-fencer")
	require.Equal(t, http.StatusOK, allowed.Code)
	require.Equal(t, "plan-1", provider.sourceFenceRequest.RestorePlanID)
	require.Equal(t, "checkpoint-1", provider.sourceFenceRequest.CheckpointID)
	require.Equal(t, "target-cluster", provider.sourceFenceRequest.TargetClusterID)
	require.Equal(t, "target-generation-1", provider.sourceFenceRequest.TargetGeneration)
}

func TestManagerBackupCheckpointsSupportsStablePageAndExactQuery(t *testing.T) {
	provider := &fakeBackupManagement{
		checkpointPage: backupusecase.CheckpointPage{
			CatalogHeadToken: "opaque-catalog-head",
			Items: []backupusecase.CheckpointSummary{{
				ID: "checkpoint-2", CreatedAtUnixMillis: 200,
				EffectiveAtUnixMillis: 190,
			}},
			NextCursor: "opaque-next", Total: 2,
		},
		checkpointDetail: backupusecase.CheckpointDetail{
			CheckpointSummary: backupusecase.CheckpointSummary{
				ID: "checkpoint-2", CreatedAtUnixMillis: 200,
				EffectiveAtUnixMillis: 190,
			},
			SourceClusterID:  "cluster-a",
			SourceGeneration: "generation-1", HashSlotCount: 256,
			ErasureHeads: []backupartifact.ErasureStreamHead{{
				HashSlot: 17, Sequence: 4,
				CommitKey: backupartifact.ErasureLedgerCommitKey(
					strings.Repeat("e", 64), 17, 4,
				),
				CommitSHA256: strings.Repeat("f", 64),
			}},
		},
	}
	srv := New(Options{Backup: provider})
	recorder := httptest.NewRecorder()
	srv.Engine().ServeHTTP(
		recorder,
		httptest.NewRequest(
			http.MethodGet,
			"/manager/backups/checkpoints?limit=999&cursor=opaque&id=POINT",
			nil,
		),
	)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, backupusecase.MaxCheckpointPageSize,
		provider.checkpointListRequest.Limit)
	require.Equal(t, "opaque", provider.checkpointListRequest.Cursor)
	require.Contains(t, recorder.Body.String(),
		`"catalog_head_token":"opaque-catalog-head"`)
	require.NotContains(t, recorder.Body.String(), "catalog/pages/")

	recorder = httptest.NewRecorder()
	srv.Engine().ServeHTTP(
		recorder,
		httptest.NewRequest(
			http.MethodGet,
			"/manager/backups/checkpoints/checkpoint-2", nil,
		),
	)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"hash_slot_count":256`)
	require.NotContains(t, recorder.Body.String(), "commit_sha256")
}

func TestManagerCheckpointHoldUsesBoundedContinuousSurface(t *testing.T) {
	provider := &fakeBackupManagement{}
	srv := New(Options{Backup: provider})

	for _, test := range []struct {
		held bool
	}{
		{held: true},
		{held: false},
	} {
		recorder := httptest.NewRecorder()
		body := `{"held":false}`
		if test.held {
			body = `{"held":true}`
		}
		srv.Engine().ServeHTTP(
			recorder,
			httptest.NewRequest(
				http.MethodPost,
				"/manager/backups/checkpoints/checkpoint-7/hold",
				bytes.NewBufferString(body),
			),
		)
		require.Equal(t, http.StatusOK, recorder.Code)
		require.Equal(t, "checkpoint-7", provider.heldCheckpointID)
		require.Equal(t, test.held, provider.held)
		require.Contains(t, recorder.Body.String(),
			`"id":"checkpoint-7"`)
		require.NotContains(t, recorder.Body.String(), "catalog")
	}
}

func TestManagerOldBackupRoutesAreAbsent(t *testing.T) {
	srv := New(Options{Backup: &fakeBackupManagement{}})
	for _, target := range []string{
		"/manager/backups/restore-points",
		"/manager/backups/trigger",
		"/manager/backups/jobs/job-1/cancel",
		"/manager/backups/restore-points/rp-1/verify",
	} {
		recorder := httptest.NewRecorder()
		srv.Engine().ServeHTTP(
			recorder, httptest.NewRequest(http.MethodPost, target, nil),
		)
		require.Equal(t, http.StatusNotFound, recorder.Code, target)
	}
}

type fakeBackupManagement struct {
	status                backupusecase.StatusSnapshot
	checkpointPage        backupusecase.CheckpointPage
	checkpointDetail      backupusecase.CheckpointDetail
	checkpointListRequest backupusecase.CheckpointListRequest
	checkpointID          string
	sourceFenceRequest    backupusecase.SourceFenceRequest
	sourceFenceReceipt    backupusecase.SourceFenceReceipt
	publication           backupusecase.CheckpointPublication
	published             bool
	statusErr             error
	heldCheckpointID      string
	held                  bool
}

func (f *fakeBackupManagement) Status(context.Context) (backupusecase.StatusSnapshot, error) {
	return f.status, f.statusErr
}

func (f *fakeBackupManagement) ListCheckpointsPage(
	_ context.Context,
	request backupusecase.CheckpointListRequest,
) (backupusecase.CheckpointPage, error) {
	f.checkpointListRequest = request
	return f.checkpointPage, nil
}

func (f *fakeBackupManagement) CheckpointByID(
	_ context.Context,
	checkpointID string,
) (backupusecase.CheckpointDetail, error) {
	f.checkpointID = checkpointID
	return f.checkpointDetail, nil
}

func (f *fakeBackupManagement) PublishCheckpoint(
	context.Context,
) (backupusecase.CheckpointPublication, error) {
	f.published = true
	return f.publication, nil
}

func (f *fakeBackupManagement) SetCheckpointHold(
	_ context.Context,
	checkpointID string,
	held bool,
) (backupusecase.CheckpointSummary, error) {
	f.heldCheckpointID = checkpointID
	f.held = held
	return backupusecase.CheckpointSummary{
		ID: checkpointID, Held: held,
	}, nil
}

func (f *fakeBackupManagement) FenceSource(
	_ context.Context,
	request backupusecase.SourceFenceRequest,
) (backupusecase.SourceFenceReceipt, error) {
	f.sourceFenceRequest = request
	return f.sourceFenceReceipt, nil
}
