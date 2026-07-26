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
		ID: "plan-1", CheckpointID: "restore-1", CheckpointSHA256: string(bytes.Repeat([]byte("a"), 64)),
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
		"checkpoint_id":"restore-1","invalidate_tokens":true,
		"catalog_head_token":"opaque-head"
	}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	server.Engine().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated ||
		provider.request.CheckpointID != "restore-1" ||
		!provider.request.InvalidateTokens ||
		provider.request.CatalogHeadToken != "opaque-head" {
		t.Fatalf("restore plan status=%d body=%s request=%#v", recorder.Code, recorder.Body.String(), provider.request)
	}
	if bytes.Contains(recorder.Body.Bytes(), []byte(`"repository"`)) ||
		bytes.Contains(recorder.Body.Bytes(), []byte(`"manifest_sha256"`)) ||
		!bytes.Contains(recorder.Body.Bytes(), []byte(`"checkpoint_id":"restore-1"`)) {
		t.Fatalf("restore plan leaked legacy or repository detail: %s", recorder.Body.String())
	}
}

func TestOrdinaryModeExposesActivatedRestoreStatusReadOnly(t *testing.T) {
	provider := &fakeRestoreManagement{plan: backupusecase.RestorePlan{
		ID:               "plan-activated",
		CheckpointID:     "checkpoint-1",
		TargetGeneration: "target-generation-1",
		Status:           backupusecase.RestoreStatusActivated,
		HashSlotCount:    1,
		Partitions: []backupusecase.RestorePartition{{
			HashSlot: 0, Installed: true, Verified: true,
		}},
	}}
	server := New(Options{Restore: provider})

	recorder := httptest.NewRecorder()
	server.Engine().ServeHTTP(
		recorder,
		httptest.NewRequest(
			http.MethodGet, "/manager/restore/status", nil,
		),
	)
	if recorder.Code != http.StatusOK ||
		!bytes.Contains(
			recorder.Body.Bytes(),
			[]byte(`"target_generation":"target-generation-1"`),
		) ||
		!bytes.Contains(
			recorder.Body.Bytes(),
			[]byte(`"status":"activated"`),
		) {
		t.Fatalf(
			"ordinary restore status=%d body=%s",
			recorder.Code, recorder.Body.String(),
		)
	}
}

func TestRestoreActivationRequiresExplicitNonWildcardGrant(t *testing.T) {
	provider := &fakeRestoreManagement{plan: backupusecase.RestorePlan{
		ID: "plan-1", CheckpointID: "restore-1", CheckpointSHA256: string(bytes.Repeat([]byte("a"), 64)),
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
		request := httptest.NewRequest(http.MethodPost, "/manager/restore/plan-1/activate", bytes.NewBufferString(`{"break_glass":{"reason":"the source Controller disks are permanently unavailable"}}`))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, server, username))
		server.Engine().ServeHTTP(recorder, request)
		if recorder.Code != http.StatusForbidden {
			t.Fatalf("%s activation status = %d, want 403", username, recorder.Code)
		}
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/manager/restore/plan-1/activate", bytes.NewBufferString(`{"break_glass":{"reason":"the source Controller disks are permanently unavailable"}}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, server, "activator"))
	server.Engine().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("explicit activation status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	if provider.activation.Operator != "activator" ||
		provider.activation.BreakGlassReason == "" {
		t.Fatalf("activation request = %#v", provider.activation)
	}
}

func TestRestoreStatusExposesConvergenceThroughputAndETA(t *testing.T) {
	eta := uint64(42)
	provider := &fakeRestoreManagement{
		progress: &backupusecase.RestoreProgress{
			PlanID: "plan-progress", Status: backupusecase.RestoreStatusInstalling,
			TotalSlots: 2, InstallingSlots: 1, ConvergedSlots: 1,
			DownloadedBytes: 1_024, ReplicatedBytes: 2_048,
			ThroughputBytesPerSecond: 512, ETASeconds: &eta,
			Partitions: []backupusecase.RestorePartition{{
				HashSlot:     0,
				Status:       backupusecase.RestorePartitionConverging,
				ReplicaCount: 3, ConvergedReplicas: 2,
				DownloadedBytes: 1_024, ReplicatedBytes: 2_048,
			}},
		},
	}
	server := New(Options{RestoreMode: true, Restore: provider})
	recorder := httptest.NewRecorder()
	server.Engine().ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodGet, "/manager/restore/status", nil),
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf("restore status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	for _, expected := range [][]byte{
		[]byte(`"status":"installing"`),
		[]byte(`"converged_replicas":2`),
		[]byte(`"throughput_bytes_per_second":512`),
		[]byte(`"eta_seconds":42`),
	} {
		if !bytes.Contains(recorder.Body.Bytes(), expected) {
			t.Fatalf(
				"restore status body=%s missing %s",
				recorder.Body.String(), expected,
			)
		}
	}
}

type fakeRestoreManagement struct {
	plan       backupusecase.RestorePlan
	request    backupusecase.RestorePlanRequest
	progress   *backupusecase.RestoreProgress
	activation backupusecase.RestoreActivationRequest
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
	plan.Partitions = append(
		[]backupusecase.RestorePartition(nil), f.plan.Partitions...,
	)
	return &plan, nil
}

func (f *fakeRestoreManagement) RestoreProgress(context.Context) (*backupusecase.RestoreProgress, error) {
	if f.progress != nil {
		copy := *f.progress
		copy.Partitions = append(
			[]backupusecase.RestorePartition(nil), f.progress.Partitions...,
		)
		return &copy, nil
	}
	return &backupusecase.RestoreProgress{
		PlanID: f.plan.ID, Status: f.plan.Status,
		TotalSlots: f.plan.HashSlotCount,
		Partitions: append([]backupusecase.RestorePartition(nil), f.plan.Partitions...),
	}, nil
}

func (f *fakeRestoreManagement) VerifyRestore(context.Context, string) (backupusecase.RestorePlan, error) {
	return f.plan, nil
}

func (f *fakeRestoreManagement) ActivateRestore(
	_ context.Context,
	_ string,
	request backupusecase.RestoreActivationRequest,
) (backupusecase.RestorePlan, error) {
	f.activation = request
	return f.plan, nil
}
