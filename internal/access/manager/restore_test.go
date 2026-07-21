package manager

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
)

func TestRestoreModeRegistersOnlyRecoveryManagerSurface(t *testing.T) {
	provider := &fakeRestoreManagement{plan: backupusecase.RestorePlan{
		ID: "plan-1", RestorePointID: "restore-1", ManifestSHA256: string(bytes.Repeat([]byte("a"), 64)),
		Status: backupusecase.RestoreStatusPlanned, HashSlotCount: 1,
		Partitions: []backupusecase.RestorePartition{{HashSlot: 0}},
	}}
	server := New(Options{RestoreMode: true, Restore: provider})

	ordinary := httptest.NewRecorder()
	server.Engine().ServeHTTP(ordinary, httptest.NewRequest(http.MethodGet, "/manager/nodes", nil))
	if ordinary.Code != http.StatusNotFound {
		t.Fatalf("ordinary manager route status = %d, want 404", ordinary.Code)
	}

	request := httptest.NewRequest(http.MethodPost, "/manager/restore/plan", bytes.NewBufferString(`{
		"restore_point_id":"restore-1","repository":"secondary","invalidate_tokens":true
	}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	server.Engine().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated || provider.request.Repository != "secondary" || !provider.request.InvalidateTokens {
		t.Fatalf("restore plan status=%d body=%s request=%#v", recorder.Code, recorder.Body.String(), provider.request)
	}
}

func TestRestoreActivationRequiresExplicitNonWildcardGrant(t *testing.T) {
	provider := &fakeRestoreManagement{plan: backupusecase.RestorePlan{
		ID: "plan-1", RestorePointID: "restore-1", ManifestSHA256: string(bytes.Repeat([]byte("a"), 64)),
		Status: backupusecase.RestoreStatusVerified, HashSlotCount: 1,
	}}
	server := New(Options{
		RestoreMode: true,
		Restore:     provider,
		Auth: testAuthConfig([]UserConfig{
			{Username: "backup-writer", Password: "secret", Permissions: []PermissionConfig{{Resource: "cluster.backup", Actions: []string{"w"}}}},
			{Username: "wildcard-admin", Password: "secret", Permissions: []PermissionConfig{{Resource: "*", Actions: []string{"*"}}}},
			{Username: "activator", Password: "secret", Permissions: []PermissionConfig{{Resource: "cluster.restore.activation", Actions: []string{"w"}}}},
		}),
	})

	for _, username := range []string{"backup-writer", "wildcard-admin"} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/manager/restore/plan-1/activate", bytes.NewBufferString(`{"old_cluster_fence_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, server, username))
		server.Engine().ServeHTTP(recorder, request)
		if recorder.Code != http.StatusForbidden {
			t.Fatalf("%s activation status = %d, want 403", username, recorder.Code)
		}
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/manager/restore/plan-1/activate", bytes.NewBufferString(`{"old_cluster_fence_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, server, "activator"))
	server.Engine().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("explicit activation status = %d body=%s", recorder.Code, recorder.Body.String())
	}
}

type fakeRestoreManagement struct {
	plan    backupusecase.RestorePlan
	request backupusecase.RestorePlanRequest
}

func (f *fakeRestoreManagement) PlanRestore(_ context.Context, request backupusecase.RestorePlanRequest) (backupusecase.RestorePlan, error) {
	f.request = request
	return f.plan, nil
}

func (f *fakeRestoreManagement) StartRestore(context.Context, string) (backupusecase.RestorePlan, error) {
	return f.plan, nil
}

func (f *fakeRestoreManagement) RestoreStatus(context.Context) (*backupusecase.RestorePlan, error) {
	plan := f.plan
	return &plan, nil
}

func (f *fakeRestoreManagement) VerifyRestore(context.Context, string) (backupusecase.RestorePlan, error) {
	return f.plan, nil
}

func (f *fakeRestoreManagement) ActivateRestore(context.Context, string, string) (backupusecase.RestorePlan, error) {
	return f.plan, nil
}
