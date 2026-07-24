package manager

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

func TestManagerBackupStatusSanitizesControllerAndRepositoryDetails(t *testing.T) {
	age := int64(42)
	verificationAge := int64(3600)
	provider := &fakeBackupManagement{status: backupusecase.StatusSnapshot{
		Enabled:                 true,
		Health:                  backupusecase.HealthHealthy,
		RecoveryPointAgeSeconds: &age,
		VerificationAgeSeconds:  &verificationAge,
		PendingGarbageCount:     7,
		FailureCategory:         "retention",
		Active: &backupusecase.Job{
			ID: "job-1", Epoch: 3, Kind: backupartifact.RestorePointIncremental,
			Status: backupusecase.JobStatusCapturing, HashSlotCount: 256,
			RestorePointID:    "restore-job-1",
			ConfigFingerprint: "secret-config-fingerprint",
			Partitions:        []backupusecase.PartitionReport{{ManifestKey: "objects/private/key"}},
		},
	}}
	srv := New(Options{Backup: provider})
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/backups/status", nil)
	srv.Engine().ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	for _, forbidden := range []string{"secret-config-fingerprint", "objects/private/key", "manifest_key", "config_fingerprint"} {
		if bytes.Contains(recorder.Body.Bytes(), []byte(forbidden)) {
			t.Fatalf("backup response leaked %q: %s", forbidden, body)
		}
	}
	var decoded map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("Unmarshal(): %v", err)
	}
	active := decoded["active"].(map[string]any)
	if active["completed_partitions"].(float64) != 1 || active["hash_slot_count"].(float64) != 256 {
		t.Fatalf("active = %#v", active)
	}
	if active["restore_point_id"] != "restore-job-1" {
		t.Fatalf("active restore_point_id = %#v", active["restore_point_id"])
	}
	if decoded["verification_age_seconds"].(float64) != 3600 || decoded["pending_garbage_count"].(float64) != 7 || decoded["failure_category"] != "retention" {
		t.Fatalf("operational backup status = %#v", decoded)
	}
}

func TestManagerBackupRoutesEnforceReadWritePermissions(t *testing.T) {
	provider := &fakeBackupManagement{}
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{
			{Username: "reader", Password: "secret", Permissions: []PermissionConfig{{Resource: "cluster.backup", Actions: []string{"r"}}}},
			{Username: "writer", Password: "secret", Permissions: []PermissionConfig{{Resource: "cluster.backup", Actions: []string{"w"}}}},
		}),
		Backup: provider,
	})

	denied := httptest.NewRecorder()
	deniedReq := httptest.NewRequest(http.MethodPost, "/manager/backups/trigger", bytes.NewBufferString(`{"kind":"materialized_full"}`))
	deniedReq.Header.Set("Content-Type", "application/json")
	deniedReq.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "reader"))
	srv.Engine().ServeHTTP(denied, deniedReq)
	if denied.Code != http.StatusForbidden || provider.triggered {
		t.Fatalf("denied status=%d triggered=%v", denied.Code, provider.triggered)
	}

	allowed := httptest.NewRecorder()
	allowedReq := httptest.NewRequest(http.MethodPost, "/manager/backups/trigger", bytes.NewBufferString(`{"kind":"materialized_full"}`))
	allowedReq.Header.Set("Content-Type", "application/json")
	allowedReq.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "writer"))
	srv.Engine().ServeHTTP(allowed, allowedReq)
	if allowed.Code != http.StatusAccepted || !provider.triggered || !bytes.Contains(allowed.Body.Bytes(), []byte(`"restore_point_id":"restore-job-1"`)) {
		t.Fatalf("allowed status=%d body=%s triggered=%v", allowed.Code, allowed.Body.String(), provider.triggered)
	}

	readDenied := httptest.NewRecorder()
	readDeniedReq := httptest.NewRequest(http.MethodGet, "/manager/backups/status", nil)
	readDeniedReq.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "writer"))
	srv.Engine().ServeHTTP(readDenied, readDeniedReq)
	if readDenied.Code != http.StatusForbidden {
		t.Fatalf("read denied status=%d", readDenied.Code)
	}
}

func TestManagerBackupRestorePointsUsesBoundedCursorPagination(t *testing.T) {
	provider := &fakeBackupManagement{page: backupusecase.RestorePointPage{
		Items:      []backupusecase.RestorePoint{{ID: "rp-2", Held: true}},
		NextCursor: "next-page",
		Total:      7,
	}}
	srv := New(Options{Backup: provider})
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/backups/restore-points?limit=999&cursor=opaque&id=RP&held=true", nil)
	srv.Engine().ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	if provider.listRequest.Limit != backupusecase.MaxRestorePointPageSize ||
		provider.listRequest.Cursor != "opaque" ||
		provider.listRequest.IDQuery != "RP" ||
		!provider.listRequest.HeldOnly {
		t.Fatalf("list request = %#v", provider.listRequest)
	}
	var decoded struct {
		Items      []backupRestorePointDTO `json:"items"`
		NextCursor string                  `json:"next_cursor"`
		Total      int                     `json:"total"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("Unmarshal(): %v", err)
	}
	if len(decoded.Items) != 1 || decoded.Items[0].ID != "rp-2" || decoded.NextCursor != "next-page" || decoded.Total != 7 {
		t.Fatalf("response = %#v", decoded)
	}
}

func TestManagerBackupVerificationStartsDurableAsyncTask(t *testing.T) {
	provider := &fakeBackupManagement{verificationTask: backupusecase.VerificationTask{
		ID: "verification-1", RestorePointID: "rp-1",
		VerificationEvidence: backupusecase.VerificationEvidence{
			Status: backupusecase.VerificationTaskPending, StartedAtUnixMillis: 123,
		},
	}}
	srv := New(Options{Backup: provider})
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/backups/restore-points/rp-1/verify", nil)
	srv.Engine().ServeHTTP(recorder, req)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	var decoded backupVerificationTaskDTO
	if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("Unmarshal(): %v", err)
	}
	if decoded.ID != "verification-1" || decoded.RestorePointID != "rp-1" || decoded.Status != backupusecase.VerificationTaskPending {
		t.Fatalf("task = %#v", decoded)
	}
}

func TestManagerBackupUsesStableMachineErrors(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		err        error
		status     int
		code       string
		retryAfter string
	}{
		{name: "leader unavailable", err: backupusecase.ErrControllerLeaderUnavailable, status: http.StatusServiceUnavailable, code: "controller_leader_unavailable", retryAfter: "1"},
		{name: "backup active", err: backupusecase.ErrJobActive, status: http.StatusConflict, code: "backup_job_active"},
		{name: "verification active", err: backupusecase.ErrVerificationJobActive, status: http.StatusConflict, code: "verification_job_active"},
		{name: "restore point missing", err: backupusecase.ErrRestorePointNotFound, status: http.StatusNotFound, code: "restore_point_not_found"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			srv := New(Options{Backup: &fakeBackupManagement{statusErr: testCase.err}})
			recorder := httptest.NewRecorder()
			srv.Engine().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/manager/backups/status", nil))
			if recorder.Code != testCase.status || !bytes.Contains(recorder.Body.Bytes(), []byte(`"error":"`+testCase.code+`"`)) || recorder.Header().Get("Retry-After") != testCase.retryAfter {
				t.Fatalf("status=%d headers=%v body=%s", recorder.Code, recorder.Header(), recorder.Body.String())
			}
		})
	}
}

type fakeBackupManagement struct {
	status           backupusecase.StatusSnapshot
	page             backupusecase.RestorePointPage
	listRequest      backupusecase.RestorePointListRequest
	verificationTask backupusecase.VerificationTask
	triggered        bool
	statusErr        error
}

func (f *fakeBackupManagement) Status(context.Context) (backupusecase.StatusSnapshot, error) {
	return f.status, f.statusErr
}

func (f *fakeBackupManagement) ListRestorePointsPage(_ context.Context, request backupusecase.RestorePointListRequest) (backupusecase.RestorePointPage, error) {
	f.listRequest = request
	return f.page, nil
}

func (f *fakeBackupManagement) Trigger(_ context.Context, kind backupartifact.RestorePointKind) (backupusecase.Job, error) {
	f.triggered = true
	return backupusecase.Job{ID: "job-1", Epoch: 1, Kind: kind, Status: backupusecase.JobStatusCapturing, HashSlotCount: 256, RestorePointID: "restore-job-1"}, nil
}

func (f *fakeBackupManagement) Cancel(context.Context, string, uint64) (backupusecase.Job, error) {
	return backupusecase.Job{}, nil
}

func (f *fakeBackupManagement) Hold(context.Context, string) (backupusecase.RestorePoint, error) {
	return backupusecase.RestorePoint{}, nil
}

func (f *fakeBackupManagement) Release(context.Context, string) (backupusecase.RestorePoint, error) {
	return backupusecase.RestorePoint{}, nil
}

func (f *fakeBackupManagement) StartVerification(context.Context, string) (backupusecase.VerificationTask, error) {
	return f.verificationTask, nil
}
