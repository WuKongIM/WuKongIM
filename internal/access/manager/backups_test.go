package manager

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

func TestManagerBackupDashboardNeverReturnsStoredCredentials(t *testing.T) {
	provider := &fakeBackupManagement{
		dashboard: backupusecase.Dashboard{
			State: backupcontract.SystemState{
				Revision: 4,
				Plan: &backupcontract.Plan{
					Revision: 2,
					Enabled:  true,
					Store: backupcontract.StoreConfig{
						Kind:                 backupcontract.StoreKindS3,
						Endpoint:             "https://s3.example.com",
						Bucket:               "archive",
						Prefix:               "prod",
						CredentialCiphertext: []byte("secret-ciphertext"),
					},
					Cron:     "0 1 * * *",
					TimeZone: "Asia/Shanghai",
				},
			},
			CredentialsConfigured: true,
			Archives:              []backupusecase.ArchiveSummary{},
		},
	}
	server := New(Options{Backup: provider})
	recorder := httptest.NewRecorder()
	server.Engine().ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodGet, "/manager/backups", nil),
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body)
	}
	if bytes.Contains(recorder.Body.Bytes(), []byte("secret-ciphertext")) ||
		bytes.Contains(recorder.Body.Bytes(), []byte("credential_ciphertext")) {
		t.Fatalf("response exposes credential: %s", recorder.Body)
	}
}

func TestManagerBackupWritesRequireAuthenticationAndPermission(t *testing.T) {
	body := []byte(`{
		"expected_revision":0,
		"enabled":true,
		"store":{"kind":"file"},
		"cron":"0 1 * * *",
		"time_zone":"Asia/Shanghai",
		"retention_count":7,
		"rate_mib_per_second":50,
		"workers_per_node":1,
		"max_duration_hours":12
	}`)
	unauthenticated := New(Options{Backup: &fakeBackupManagement{}})
	recorder := performBackupRequest(
		unauthenticated, http.MethodPut, "/manager/backups/plan", body, "",
	)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("auth-disabled status = %d body=%s", recorder.Code, recorder.Body)
	}

	provider := &fakeBackupManagement{}
	server := New(Options{
		Auth: testAuthConfig([]UserConfig{
			{
				Username: "reader", Password: "secret",
				Permissions: []PermissionConfig{{
					Resource: "cluster.backup", Actions: []string{"r"},
				}},
			},
			{
				Username: "writer", Password: "secret",
				Permissions: []PermissionConfig{{
					Resource: "cluster.backup", Actions: []string{"w"},
				}},
			},
		}),
		Backup: provider,
	})
	reader := performBackupRequest(
		server, http.MethodPut, "/manager/backups/plan", body,
		mustIssueTestToken(t, server, "reader"),
	)
	if reader.Code != http.StatusForbidden {
		t.Fatalf("reader status = %d body=%s", reader.Code, reader.Body)
	}
	writer := performBackupRequest(
		server, http.MethodPut, "/manager/backups/plan", body,
		mustIssueTestToken(t, server, "writer"),
	)
	if writer.Code != http.StatusOK {
		t.Fatalf("writer status = %d body=%s", writer.Code, writer.Body)
	}
	if provider.configure.RateBytesPerSec != 50<<20 ||
		provider.configure.MaxDuration.Hours() != 12 ||
		provider.configure.Cron != "0 1 * * *" {
		t.Fatalf("configure = %#v", provider.configure)
	}
}

func TestManagerBackupRejectsInvalidCloudRepositoryShape(t *testing.T) {
	testCases := []struct {
		name  string
		store string
	}{
		{
			name: "OSS path style",
			store: `{
				"kind":"oss",
				"region":"cn-hangzhou",
				"bucket":"wukongim-backups",
				"prefix":"cluster-a",
				"path_style":true
			}`,
		},
		{
			name: "COS bucket without APPID",
			store: `{
				"kind":"cos",
				"region":"ap-shanghai",
				"bucket":"wukongim-backups",
				"prefix":"cluster-a"
			}`,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			provider := &fakeBackupManagement{}
			server := New(Options{
				Auth: testAuthConfig([]UserConfig{{
					Username: "writer", Password: "secret",
					Permissions: []PermissionConfig{{
						Resource: "cluster.backup", Actions: []string{"w"},
					}},
				}}),
				Backup: provider,
			})
			body := []byte(`{
				"expected_revision":0,
				"enabled":true,
				"store":` + testCase.store + `,
				"cron":"0 1 * * *",
				"time_zone":"Asia/Shanghai",
				"retention_count":7,
				"rate_mib_per_second":50,
				"workers_per_node":1,
				"max_duration_hours":12
			}`)
			recorder := performBackupRequest(
				server, http.MethodPut, "/manager/backups/plan", body,
				mustIssueTestToken(t, server, "writer"),
			)
			if recorder.Code != http.StatusBadRequest ||
				!bytes.Contains(
					recorder.Body.Bytes(),
					[]byte(`"error":"backup_bad_request"`),
				) {
				t.Fatalf(
					"status = %d body=%s",
					recorder.Code, recorder.Body,
				)
			}
			if provider.configure.Store.Kind != "" {
				t.Fatalf("Configure() called with %#v", provider.configure)
			}
		})
	}
}

func TestManagerBackupMapsCloudRepositoryCredentials(t *testing.T) {
	provider := &fakeBackupManagement{
		configureResult: backupusecase.ConfigureResult{
			Plan: backupcontract.Plan{
				Revision: 3,
				Store: backupcontract.StoreConfig{
					Kind:                 backupcontract.StoreKindOSS,
					CredentialCiphertext: []byte("sealed-credential"),
				},
			},
		},
	}
	server := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "writer", Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.backup", Actions: []string{"w"},
			}},
		}}),
		Backup: provider,
	})
	body := []byte(`{
		"expected_revision":2,
		"enabled":true,
		"store":{
			"kind":"oss",
			"region":"cn-hangzhou",
			"bucket":"wukongim-backups",
			"prefix":"cluster-a",
			"access_key":"access-key-id",
			"secret_key":"access-key-secret"
		},
		"cron":"0 1 * * *",
		"time_zone":"Asia/Shanghai",
		"retention_count":7,
		"rate_mib_per_second":50,
		"workers_per_node":1,
		"max_duration_hours":12
	}`)
	recorder := performBackupRequest(
		server, http.MethodPut, "/manager/backups/plan", body,
		mustIssueTestToken(t, server, "writer"),
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body)
	}
	request := provider.managementConfigure
	if request.Store.Kind != backupcontract.StoreKindOSS ||
		request.Store.Region != "cn-hangzhou" ||
		request.Store.Bucket != "wukongim-backups" ||
		request.Store.Prefix != "cluster-a" ||
		request.AccessKey != "access-key-id" ||
		request.SecretKey != "access-key-secret" {
		t.Fatalf("configure request = %#v", request)
	}
	if bytes.Contains(recorder.Body.Bytes(), []byte("sealed-credential")) ||
		bytes.Contains(
			recorder.Body.Bytes(),
			[]byte("credential_ciphertext"),
		) ||
		!bytes.Contains(
			recorder.Body.Bytes(),
			[]byte(`"credentials_configured":true`),
		) {
		t.Fatalf("configure response = %s", recorder.Body)
	}
}

func TestManagerBackupRepositoryTestUsesOnlySavedPlanRevision(t *testing.T) {
	provider := &fakeBackupManagement{
		testRepositoryPlan: backupcontract.Plan{
			Revision: 4,
			Store: backupcontract.StoreConfig{
				Kind:                 backupcontract.StoreKindOSS,
				CredentialCiphertext: []byte("secret-ciphertext"),
			},
			RepositoryVerification: &backupcontract.RepositoryVerification{
				Status: backupcontract.RepositoryVerificationVerified,
			},
		},
	}
	server := newBackupWriterServer(provider)
	token := mustIssueTestToken(t, server, "writer")

	recorder := performBackupRequest(
		server, http.MethodPost, "/manager/backups/repository/test",
		[]byte(`{"expected_plan_revision":4}`), token,
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body)
	}
	if provider.testRepositoryRequest.ExpectedPlanRevision != 4 ||
		provider.testRepositoryCalls != 1 {
		t.Fatalf(
			"request=%#v calls=%d",
			provider.testRepositoryRequest,
			provider.testRepositoryCalls,
		)
	}
	if bytes.Contains(recorder.Body.Bytes(), []byte("secret-ciphertext")) ||
		bytes.Contains(
			recorder.Body.Bytes(),
			[]byte("credential_ciphertext"),
		) ||
		!bytes.Contains(recorder.Body.Bytes(), []byte(`"ok":true`)) ||
		!bytes.Contains(recorder.Body.Bytes(), []byte(`"revision":4`)) ||
		!bytes.Contains(recorder.Body.Bytes(), []byte(`"status":"verified"`)) {
		t.Fatalf("response = %s", recorder.Body)
	}

	for _, body := range [][]byte{
		[]byte(`{"expected_plan_revision":0}`),
		[]byte(`{
			"expected_plan_revision":4,
			"store":{
				"kind":"oss",
				"access_key":"must-not-be-accepted",
				"secret_key":"must-not-be-accepted"
			}
		}`),
	} {
		rejected := performBackupRequest(
			server, http.MethodPost, "/manager/backups/repository/test",
			body, token,
		)
		if rejected.Code != http.StatusBadRequest {
			t.Fatalf(
				"body=%s status=%d response=%s",
				body, rejected.Code, rejected.Body,
			)
		}
	}
	if provider.testRepositoryCalls != 1 {
		t.Fatalf("rejected request reached usecase: calls=%d", provider.testRepositoryCalls)
	}
}

func TestManagerBackupRepositoryTestRejectsStaleSavedRevision(t *testing.T) {
	provider := &fakeBackupManagement{
		testRepositoryErr: backupusecase.ErrStateConflict,
	}
	server := newBackupWriterServer(provider)
	recorder := performBackupRequest(
		server, http.MethodPost, "/manager/backups/repository/test",
		[]byte(`{"expected_plan_revision":3}`),
		mustIssueTestToken(t, server, "writer"),
	)
	if recorder.Code != http.StatusConflict ||
		!bytes.Contains(
			recorder.Body.Bytes(),
			[]byte(`"error":"backup_plan_conflict"`),
		) ||
		provider.testRepositoryRequest.ExpectedPlanRevision != 3 {
		t.Fatalf(
			"status=%d request=%#v body=%s",
			recorder.Code,
			provider.testRepositoryRequest,
			recorder.Body,
		)
	}
}

func TestManagerBackupRepositoryFailureIsActionableAndSecretSafe(t *testing.T) {
	provider := &fakeBackupManagement{
		testRepositoryErr: errors.Join(
			backupusecase.ErrStoreUnreachable,
			&backupcontract.RepositoryAccessError{
				Reason:       backupcontract.RepositoryAccessInvalidAccessKey,
				Stage:        backupcontract.RepositoryAccessWriteMarker,
				Provider:     backupcontract.StoreKindOSS,
				ProviderCode: "InvalidAccessKeyId",
				RequestID:    "request-1",
				NodeID:       1,
				Cause: errors.New(
					"Authorization=secret-access-key must never be returned",
				),
			},
		),
	}
	server := newBackupWriterServer(provider)
	recorder := performBackupRequest(
		server, http.MethodPost, "/manager/backups/repository/test",
		[]byte(`{"expected_plan_revision":4}`),
		mustIssueTestToken(t, server, "writer"),
	)
	if recorder.Code != http.StatusServiceUnavailable ||
		!bytes.Contains(
			recorder.Body.Bytes(),
			[]byte(`"error":"backup_repository_auth_failed"`),
		) ||
		!bytes.Contains(
			recorder.Body.Bytes(),
			[]byte(`"message":"Alibaba Cloud OSS rejected the AccessKey ID."`),
		) ||
		!bytes.Contains(
			recorder.Body.Bytes(),
			[]byte(`"provider":"oss"`),
		) ||
		!bytes.Contains(
			recorder.Body.Bytes(),
			[]byte(`"stage":"write_marker"`),
		) ||
		!bytes.Contains(
			recorder.Body.Bytes(),
			[]byte(`"reason":"invalid_access_key"`),
		) ||
		!bytes.Contains(
			recorder.Body.Bytes(),
			[]byte(`"provider_code":"InvalidAccessKeyId"`),
		) ||
		!bytes.Contains(
			recorder.Body.Bytes(),
			[]byte(`"request_id":"request-1"`),
		) ||
		!bytes.Contains(recorder.Body.Bytes(), []byte(`"node_id":1`)) ||
		bytes.Contains(recorder.Body.Bytes(), []byte("secret-access-key")) {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body)
	}
}

func TestManagerBackupRepositoryErrorFamilies(t *testing.T) {
	testCases := []struct {
		reason backupcontract.RepositoryAccessReason
		code   string
		status int
	}{
		{backupcontract.RepositoryAccessInvalidAccessKey, "backup_repository_auth_failed", http.StatusServiceUnavailable},
		{backupcontract.RepositoryAccessSignatureMismatch, "backup_repository_auth_failed", http.StatusServiceUnavailable},
		{backupcontract.RepositoryAccessDenied, "backup_repository_permission_denied", http.StatusServiceUnavailable},
		{backupcontract.RepositoryAccessBucketNotFound, "backup_repository_bucket_not_found", http.StatusServiceUnavailable},
		{backupcontract.RepositoryAccessRegionMismatch, "backup_repository_region_mismatch", http.StatusServiceUnavailable},
		{backupcontract.RepositoryAccessEndpointUnreachable, "backup_repository_endpoint_unreachable", http.StatusServiceUnavailable},
		{backupcontract.RepositoryAccessTLSFailure, "backup_repository_tls_failed", http.StatusServiceUnavailable},
		{backupcontract.RepositoryAccessTimeout, "backup_repository_timeout", http.StatusServiceUnavailable},
		{backupcontract.RepositoryAccessReadFailed, "backup_repository_operation_failed", http.StatusServiceUnavailable},
		{backupcontract.RepositoryAccessWriteFailed, "backup_repository_operation_failed", http.StatusServiceUnavailable},
		{backupcontract.RepositoryAccessListFailed, "backup_repository_operation_failed", http.StatusServiceUnavailable},
		{backupcontract.RepositoryAccessDeleteFailed, "backup_repository_operation_failed", http.StatusServiceUnavailable},
		{backupcontract.RepositoryAccessNodeUnreachable, "backup_repository_node_unreachable", http.StatusServiceUnavailable},
		{backupcontract.RepositoryAccessRepositoryInUse, "backup_repository_identity_conflict", http.StatusConflict},
		{backupcontract.RepositoryAccessUnknown, "backup_repository_unknown", http.StatusServiceUnavailable},
	}
	for _, testCase := range testCases {
		t.Run(string(testCase.reason), func(t *testing.T) {
			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)
			writeBackupError(context, errors.Join(
				backupusecase.ErrStoreUnreachable,
				&backupcontract.RepositoryAccessError{
					Reason:   testCase.reason,
					Stage:    backupcontract.RepositoryAccessReadMarker,
					Provider: backupcontract.StoreKindCOS,
				},
			))
			var response errorResponse
			if err := json.Unmarshal(
				recorder.Body.Bytes(),
				&response,
			); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if recorder.Code != testCase.status ||
				response.Error != testCase.code ||
				response.Detail == nil {
				t.Fatalf(
					"status=%d response=%#v",
					recorder.Code,
					response,
				)
			}
		})
	}
}

func TestManagerBackupRepositoryUnverifiedIsConflict(t *testing.T) {
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	writeBackupError(context, backupusecase.ErrRepositoryUnverified)
	if recorder.Code != http.StatusConflict ||
		!bytes.Contains(
			recorder.Body.Bytes(),
			[]byte(`"error":"backup_repository_unverified"`),
		) ||
		!bytes.Contains(
			recorder.Body.Bytes(),
			[]byte("Save and test the repository"),
		) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body)
	}
}

func TestManagerBackupRepositoryAuditKeepsOnlyStableSafeFields(t *testing.T) {
	logger := &recordingBackupAuditLogger{}
	provider := &fakeBackupManagement{
		testRepositoryErr: errors.Join(
			backupusecase.ErrStoreUnreachable,
			&backupcontract.RepositoryAccessError{
				Reason:       backupcontract.RepositoryAccessDenied,
				Stage:        backupcontract.RepositoryAccessWriteReceipt,
				Provider:     backupcontract.StoreKindCOS,
				ProviderCode: "AccessDenied",
				RequestID:    "request-secret-adjacent",
				NodeID:       3,
				Cause:        errors.New("SecretKey=never-log-this"),
			},
		),
	}
	server := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "writer", Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.backup", Actions: []string{"w"},
			}},
		}}),
		Backup: provider,
		Logger: logger,
	})
	recorder := performBackupRequest(
		server, http.MethodPost, "/manager/backups/repository/test",
		[]byte(`{"expected_plan_revision":4}`),
		mustIssueTestToken(t, server, "writer"),
	)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body)
	}
	fields := logger.stringFields()
	for _, expected := range []string{
		"error_code=backup_repository_permission_denied",
		"provider=cos",
		"stage=write_receipt",
	} {
		if !strings.Contains(fields, expected) {
			t.Fatalf("audit fields missing %q:\n%s", expected, fields)
		}
	}
	for _, forbidden := range []string{
		"never-log-this",
		"AccessDenied",
		"request-secret-adjacent",
	} {
		if strings.Contains(fields, forbidden) {
			t.Fatalf("audit fields leaked %q:\n%s", forbidden, fields)
		}
	}
	nodeFound := false
	for _, field := range logger.fields {
		if field.Key == "node_id" && field.Value == uint64(3) {
			nodeFound = true
		}
	}
	if !nodeFound {
		t.Fatalf("audit fields missing node_id=3: %#v", logger.fields)
	}
}

func TestWriteBackupErrorUsesStableBackupDomainCodes(t *testing.T) {
	testCases := []struct {
		err      error
		wantCode string
	}{
		{backupusecase.ErrInvalidRequest, "backup_bad_request"},
		{backupusecase.ErrDisabled, "backup_not_configured"},
		{backupusecase.ErrBackupJobActive, "backup_job_active"},
		{backupusecase.ErrRestoreJobActive, "backup_restore_active"},
		{backupusecase.ErrStateConflict, "backup_plan_conflict"},
		{backupusecase.ErrArchiveOperationActive, "backup_archive_operation_active"},
		{backupusecase.ErrArchiveHeld, "backup_archive_held"},
		{backupusecase.ErrArchiveInUse, "backup_archive_in_use"},
		{backupusecase.ErrLastUsableArchive, "backup_last_archive"},
		{backupusecase.ErrArchiveNotFound, "backup_archive_not_found"},
		{backupusecase.ErrArchiveCorrupt, "backup_archive_corrupt"},
		{backupusecase.ErrStoreUnreachable, "backup_store_unreachable"},
		{backupusecase.ErrRepositoryUnverified, "backup_repository_unverified"},
		{errors.New("unknown"), "backup_service_unavailable"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.wantCode, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)
			writeBackupError(context, testCase.err)
			var response struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if response.Error != testCase.wantCode {
				t.Fatalf("error code = %q, want %q", response.Error, testCase.wantCode)
			}
		})
	}
}

func newBackupWriterServer(provider BackupManagement) *Server {
	return New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "writer", Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.backup", Actions: []string{"w"},
			}},
		}}),
		Backup: provider,
	})
}

func TestManagerBackupManualJobAndArchiveOperations(t *testing.T) {
	provider := &fakeBackupManagement{
		job: backupcontract.BackupJob{ID: "backup-1"},
		archive: backupusecase.ArchiveDetail{
			Archive: backupusecase.ArchiveSummary{ID: "backup-1"},
		},
	}
	server := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin", Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.backup", Actions: []string{"r", "w"},
			}},
		}}),
		Backup: provider,
	})
	token := mustIssueTestToken(t, server, "admin")
	start := performBackupRequest(
		server, http.MethodPost, "/manager/backups/jobs", nil, token,
	)
	if start.Code != http.StatusAccepted {
		t.Fatalf("start status = %d body=%s", start.Code, start.Body)
	}
	detail := performBackupRequest(
		server, http.MethodGet,
		"/manager/backups/archives/backup-1", nil, token,
	)
	if detail.Code != http.StatusOK {
		t.Fatalf("detail status = %d body=%s", detail.Code, detail.Body)
	}
	cancel := performBackupRequest(
		server, http.MethodPost,
		"/manager/backups/jobs/backup-1/cancel", nil, token,
	)
	if cancel.Code != http.StatusNoContent ||
		provider.canceled != "backup-1" {
		t.Fatalf("cancel status=%d id=%q", cancel.Code, provider.canceled)
	}
	rejectedDelete := performBackupRequest(
		server, http.MethodDelete,
		"/manager/backups/archives/backup-1",
		[]byte(`{"confirmation":"DELETE another-backup"}`), token,
	)
	if rejectedDelete.Code != http.StatusBadRequest ||
		!bytes.Contains(
			rejectedDelete.Body.Bytes(),
			[]byte(`"error":"backup_confirmation_mismatch"`),
		) ||
		provider.deleted != "" {
		t.Fatalf(
			"rejected delete status=%d deleted=%q body=%s",
			rejectedDelete.Code, provider.deleted, rejectedDelete.Body,
		)
	}
	acceptedDelete := performBackupRequest(
		server, http.MethodDelete,
		"/manager/backups/archives/backup-1",
		[]byte(`{"confirmation":"DELETE backup-1"}`), token,
	)
	if acceptedDelete.Code != http.StatusNoContent ||
		provider.deleted != "backup-1" {
		t.Fatalf(
			"accepted delete status=%d deleted=%q body=%s",
			acceptedDelete.Code, provider.deleted, acceptedDelete.Body,
		)
	}
}

func performBackupRequest(
	server *Server,
	method string,
	path string,
	body []byte,
	token string,
) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	if len(body) > 0 {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	server.Engine().ServeHTTP(recorder, request)
	return recorder
}

type fakeBackupManagement struct {
	dashboard             backupusecase.Dashboard
	configure             backupusecase.ConfigureRequest
	managementConfigure   backupusecase.ConfigureManagementRequest
	job                   backupcontract.BackupJob
	archive               backupusecase.ArchiveDetail
	canceled              string
	deleted               string
	configureResult       backupusecase.ConfigureResult
	testRepositoryPlan    backupcontract.Plan
	testRepositoryRequest backupusecase.TestRepositoryRequest
	testRepositoryCalls   int
	testRepositoryErr     error
}

func (f *fakeBackupManagement) Dashboard(
	context.Context,
) (backupusecase.Dashboard, error) {
	return f.dashboard, nil
}

func (f *fakeBackupManagement) Configure(
	_ context.Context,
	request backupusecase.ConfigureManagementRequest,
) (backupusecase.ConfigureResult, error) {
	f.configure = request.ConfigureRequest
	f.managementConfigure = request
	if f.configureResult.Plan.Revision != 0 {
		return f.configureResult, nil
	}
	return backupusecase.ConfigureResult{
		Plan: backupcontract.Plan{Revision: 1},
	}, nil
}

func (f *fakeBackupManagement) TestRepository(
	_ context.Context,
	request backupusecase.TestRepositoryRequest,
) (backupcontract.Plan, error) {
	f.testRepositoryRequest = request
	f.testRepositoryCalls++
	return f.testRepositoryPlan, f.testRepositoryErr
}

func (f *fakeBackupManagement) StartBackup(
	context.Context,
) (backupcontract.BackupJob, error) {
	return f.job, nil
}

func (f *fakeBackupManagement) CancelBackup(
	_ context.Context,
	jobID string,
) error {
	f.canceled = jobID
	return nil
}

func (f *fakeBackupManagement) Archive(
	context.Context,
	string,
) (backupusecase.ArchiveDetail, error) {
	return f.archive, nil
}

func (f *fakeBackupManagement) VerifyArchive(
	context.Context,
	string,
) (backupusecase.ArchiveDetail, error) {
	return f.archive, nil
}

func (f *fakeBackupManagement) HoldArchive(
	context.Context,
	string,
	bool,
	string,
) (backupusecase.ArchiveSummary, error) {
	return f.archive.Archive, nil
}

func (f *fakeBackupManagement) DeleteArchive(
	_ context.Context,
	archiveID string,
) error {
	f.deleted = archiveID
	return nil
}

type recordingBackupAuditLogger struct {
	fields []wklog.Field
}

func (l *recordingBackupAuditLogger) Debug(string, ...wklog.Field) {}
func (l *recordingBackupAuditLogger) Info(
	_ string,
	fields ...wklog.Field,
) {
	l.fields = append([]wklog.Field(nil), fields...)
}
func (l *recordingBackupAuditLogger) Warn(string, ...wklog.Field)  {}
func (l *recordingBackupAuditLogger) Error(string, ...wklog.Field) {}
func (l *recordingBackupAuditLogger) Fatal(string, ...wklog.Field) {}
func (l *recordingBackupAuditLogger) Named(string) wklog.Logger    { return l }
func (l *recordingBackupAuditLogger) With(...wklog.Field) wklog.Logger {
	return l
}
func (l *recordingBackupAuditLogger) Sync() error { return nil }

func (l *recordingBackupAuditLogger) stringFields() string {
	var result strings.Builder
	for _, field := range l.fields {
		if value, ok := field.Value.(string); ok {
			result.WriteString(field.Key)
			result.WriteByte('=')
			result.WriteString(value)
			result.WriteByte('\n')
		}
	}
	return result.String()
}
