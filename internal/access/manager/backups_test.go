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
	if allowed.Code != http.StatusAccepted || !provider.triggered {
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

type fakeBackupManagement struct {
	status    backupusecase.StatusSnapshot
	triggered bool
}

func (f *fakeBackupManagement) Status(context.Context) (backupusecase.StatusSnapshot, error) {
	return f.status, nil
}

func (f *fakeBackupManagement) ListRestorePoints(context.Context) ([]backupusecase.RestorePoint, error) {
	return nil, nil
}

func (f *fakeBackupManagement) Trigger(_ context.Context, kind backupartifact.RestorePointKind) (backupusecase.Job, error) {
	f.triggered = true
	return backupusecase.Job{ID: "job-1", Epoch: 1, Kind: kind, Status: backupusecase.JobStatusCapturing, HashSlotCount: 256}, nil
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

func (f *fakeBackupManagement) Verify(context.Context, string) (backupusecase.Verification, error) {
	return backupusecase.Verification{}, nil
}
