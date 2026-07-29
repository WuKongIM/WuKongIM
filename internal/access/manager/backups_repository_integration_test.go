//go:build integration

package manager

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupinfra "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	"github.com/WuKongIM/WuKongIM/pkg/controller"
)

type managerCloudRepositoryEnvironment struct {
	kind       backupcontract.StoreKind
	endpoint   string
	region     string
	bucket     string
	accessKey  string
	secretKey  string
	provider   string
	prefixName string
}

func TestManagerBackupRepositoryWorkflowAgainstOSS(t *testing.T) {
	environment := managerCloudRepositoryEnvironment{
		kind:       backupcontract.StoreKindOSS,
		endpoint:   strings.TrimSpace(os.Getenv("WK_TEST_OSS_ENDPOINT")),
		region:     strings.TrimSpace(os.Getenv("WK_TEST_OSS_REGION")),
		bucket:     strings.TrimSpace(os.Getenv("WK_TEST_OSS_BUCKET")),
		accessKey:  strings.TrimSpace(os.Getenv("WK_TEST_OSS_ACCESS_KEY_ID")),
		secretKey:  os.Getenv("WK_TEST_OSS_ACCESS_KEY_SECRET"),
		provider:   "Alibaba Cloud OSS",
		prefixName: "oss-manager",
	}
	if environment.region == "" || environment.bucket == "" ||
		environment.accessKey == "" || environment.secretKey == "" {
		t.Skip("WK_TEST_OSS_REGION, WK_TEST_OSS_BUCKET, WK_TEST_OSS_ACCESS_KEY_ID, and WK_TEST_OSS_ACCESS_KEY_SECRET are required")
	}
	exerciseManagerBackupRepositoryWorkflow(t, environment)
}

func TestManagerBackupRepositoryWorkflowAgainstCOS(t *testing.T) {
	environment := managerCloudRepositoryEnvironment{
		kind:       backupcontract.StoreKindCOS,
		endpoint:   strings.TrimSpace(os.Getenv("WK_TEST_COS_ENDPOINT")),
		region:     strings.TrimSpace(os.Getenv("WK_TEST_COS_REGION")),
		bucket:     strings.TrimSpace(os.Getenv("WK_TEST_COS_BUCKET")),
		accessKey:  strings.TrimSpace(os.Getenv("WK_TEST_COS_SECRET_ID")),
		secretKey:  os.Getenv("WK_TEST_COS_SECRET_KEY"),
		provider:   "Tencent Cloud COS",
		prefixName: "cos-manager",
	}
	if environment.region == "" || environment.bucket == "" ||
		environment.accessKey == "" || environment.secretKey == "" {
		t.Skip("WK_TEST_COS_REGION, WK_TEST_COS_BUCKET, WK_TEST_COS_SECRET_ID, and WK_TEST_COS_SECRET_KEY are required")
	}
	exerciseManagerBackupRepositoryWorkflow(t, environment)
}

func exerciseManagerBackupRepositoryWorkflow(
	t *testing.T,
	environment managerCloudRepositoryEnvironment,
) {
	t.Helper()
	const clusterID = "backup-manager-integration-cluster"
	cipher, err := backupinfra.NewCredentialCipher(
		"backup-manager-integration-secret",
		clusterID,
	)
	if err != nil {
		t.Fatal("create Manager integration credential cipher")
	}
	provider, err := backupinfra.NewRepositoryProvider(t.TempDir(), cipher)
	if err != nil {
		t.Fatal("create Manager integration repository provider")
	}
	stateStore := &managerIntegrationBackupStateStore{}
	var nextID atomic.Uint64
	scheduled, err := backupusecase.NewScheduledService(
		backupusecase.ScheduledOptions{
			StateStore: stateStore,
			Now:        time.Now,
			NewID: func() string {
				return fmt.Sprintf(
					"manager-integration-%d",
					nextID.Add(1),
				)
			},
		},
	)
	if err != nil {
		t.Fatal("create Manager integration scheduled service")
	}
	probe, err := backupinfra.NewClusterRepositoryProbe(
		managerIntegrationProbeCluster{},
		provider,
		managerIntegrationProbeRemote{},
	)
	if err != nil {
		t.Fatal("create Manager integration cluster probe")
	}
	management, err := backupusecase.NewManagementService(
		backupusecase.ManagementOptions{
			Scheduled:  scheduled,
			Repository: provider,
			Sealer:     provider,
			Probe:      probe,
			ClusterID:  clusterID,
			Now:        time.Now,
		},
	)
	if err != nil {
		t.Fatal("create Manager integration backup service")
	}
	server := New(Options{
		Auth: AuthConfig{
			On:        true,
			JWTSecret: "backup-manager-integration-jwt-secret",
			JWTIssuer: "wukongim-manager-integration",
			JWTExpire: time.Hour,
			Users: []UserConfig{{
				Username: "integration-admin",
				Password: "integration-password",
				Permissions: []PermissionConfig{{
					Resource: "cluster.backup",
					Actions:  []string{"r", "w"},
				}},
			}},
		},
		Backup: management,
	})
	token, err := server.issueToken("integration-admin", time.Now())
	if err != nil {
		t.Fatal("issue Manager integration token")
	}
	t.Cleanup(func() {
		managerIntegrationCleanupRepository(
			t,
			provider,
			stateStore,
			environment.provider,
		)
	})

	prefix := managerIntegrationRepositoryPrefix(environment.prefixName)
	save := managerIntegrationBackupRequest(
		t,
		server,
		token,
		http.MethodPut,
		"/manager/backups/plan",
		backupPlanRequest{
			ExpectedRevision: 0,
			Enabled:          false,
			Store: backupStoreRequest{
				Kind:      environment.kind,
				Endpoint:  environment.endpoint,
				Region:    environment.region,
				Bucket:    environment.bucket,
				Prefix:    prefix,
				PathStyle: false,
				AccessKey: environment.accessKey,
				SecretKey: environment.secretKey,
			},
			Cron:             "0 1 * * *",
			TimeZone:         "UTC",
			RetentionCount:   2,
			RateMiBPerSecond: 1,
			WorkersPerNode:   1,
			MaxDurationHours: 1,
		},
	)
	if save.Code != http.StatusOK {
		t.Fatalf(
			"%s save failed: status=%d response=%s",
			environment.provider,
			save.Code,
			save.Body,
		)
	}
	managerIntegrationAssertNoCredentials(
		t,
		save.Body.Bytes(),
		environment,
	)
	var saved backupConfigureResponse
	managerIntegrationDecode(t, save, &saved)
	if !saved.CredentialsConfigured ||
		saved.Plan.Revision == 0 ||
		saved.Plan.Enabled ||
		saved.Plan.Store.Kind != environment.kind ||
		saved.Plan.Store.Endpoint != environment.endpoint ||
		saved.Plan.Store.Region != environment.region ||
		saved.Plan.Store.Bucket != environment.bucket ||
		saved.Plan.Store.Prefix != prefix ||
		saved.Plan.RepositoryVerification == nil ||
		saved.Plan.RepositoryVerification.Status !=
			backupcontract.RepositoryVerificationUnverified {
		t.Fatalf("%s saved plan did not round trip", environment.provider)
	}

	dashboardResponse := managerIntegrationBackupRequest(
		t,
		server,
		token,
		http.MethodGet,
		"/manager/backups",
		nil,
	)
	if dashboardResponse.Code != http.StatusOK {
		t.Fatalf(
			"%s dashboard reload failed: status=%d response=%s",
			environment.provider,
			dashboardResponse.Code,
			dashboardResponse.Body,
		)
	}
	managerIntegrationAssertNoCredentials(
		t,
		dashboardResponse.Body.Bytes(),
		environment,
	)
	var dashboard backupusecase.Dashboard
	managerIntegrationDecode(t, dashboardResponse, &dashboard)
	if !dashboard.CredentialsConfigured ||
		dashboard.State.Plan == nil ||
		dashboard.State.Plan.Revision != saved.Plan.Revision ||
		dashboard.State.Plan.Store.Kind != environment.kind ||
		dashboard.State.Plan.Store.Region != environment.region ||
		dashboard.State.Plan.Store.Bucket != environment.bucket ||
		dashboard.State.Plan.Store.Prefix != prefix ||
		dashboard.State.Plan.RepositoryVerification == nil ||
		dashboard.State.Plan.RepositoryVerification.Status !=
			backupcontract.RepositoryVerificationUnverified {
		t.Fatalf("%s dashboard did not preserve the saved plan", environment.provider)
	}

	blocked := managerIntegrationBackupRequest(
		t,
		server,
		token,
		http.MethodPost,
		"/manager/backups/jobs",
		map[string]any{},
	)
	var blockedResponse errorResponse
	managerIntegrationDecode(t, blocked, &blockedResponse)
	if blocked.Code != http.StatusConflict ||
		blockedResponse.Error != "backup_repository_unverified" {
		t.Fatalf(
			"%s unverified backup admission was not blocked",
			environment.provider,
		)
	}

	testResponse := managerIntegrationBackupRequest(
		t,
		server,
		token,
		http.MethodPost,
		"/manager/backups/repository/test",
		backupRepositoryTestRequest{
			ExpectedPlanRevision: saved.Plan.Revision,
		},
	)
	if testResponse.Code != http.StatusOK {
		t.Fatalf(
			"%s repository test failed: status=%d response=%s",
			environment.provider,
			testResponse.Code,
			testResponse.Body,
		)
	}
	managerIntegrationAssertNoCredentials(
		t,
		testResponse.Body.Bytes(),
		environment,
	)
	var tested struct {
		OK   bool                `json:"ok"`
		Plan backupcontract.Plan `json:"plan"`
	}
	managerIntegrationDecode(t, testResponse, &tested)
	if !tested.OK ||
		tested.Plan.Revision != saved.Plan.Revision ||
		tested.Plan.RepositoryVerification == nil ||
		tested.Plan.RepositoryVerification.Status !=
			backupcontract.RepositoryVerificationVerified {
		t.Fatalf("%s repository was not marked verified", environment.provider)
	}

	verifiedDashboardResponse := managerIntegrationBackupRequest(
		t,
		server,
		token,
		http.MethodGet,
		"/manager/backups",
		nil,
	)
	var verifiedDashboard backupusecase.Dashboard
	managerIntegrationDecode(t, verifiedDashboardResponse, &verifiedDashboard)
	if verifiedDashboardResponse.Code != http.StatusOK ||
		verifiedDashboard.State.Plan == nil ||
		verifiedDashboard.State.Plan.Revision != saved.Plan.Revision ||
		verifiedDashboard.State.Plan.RepositoryVerification == nil ||
		verifiedDashboard.State.Plan.RepositoryVerification.Status !=
			backupcontract.RepositoryVerificationVerified {
		t.Fatalf(
			"%s verified dashboard state was not durable",
			environment.provider,
		)
	}

	enable := managerIntegrationBackupRequest(
		t,
		server,
		token,
		http.MethodPut,
		"/manager/backups/plan",
		backupPlanRequest{
			ExpectedRevision: saved.Plan.Revision,
			Enabled:          true,
			Store: backupStoreRequest{
				Kind:      environment.kind,
				Endpoint:  environment.endpoint,
				Region:    environment.region,
				Bucket:    environment.bucket,
				Prefix:    prefix,
				PathStyle: false,
			},
			Cron:             "0 1 * * *",
			TimeZone:         "UTC",
			RetentionCount:   2,
			RateMiBPerSecond: 1,
			WorkersPerNode:   1,
			MaxDurationHours: 1,
		},
	)
	if enable.Code != http.StatusOK {
		t.Fatalf(
			"%s enable failed: status=%d response=%s",
			environment.provider,
			enable.Code,
			enable.Body,
		)
	}
	managerIntegrationAssertNoCredentials(
		t,
		enable.Body.Bytes(),
		environment,
	)
	var enabled backupConfigureResponse
	managerIntegrationDecode(t, enable, &enabled)
	if !enabled.CredentialsConfigured ||
		!enabled.Plan.Enabled ||
		enabled.Plan.RepositoryVerification == nil ||
		enabled.Plan.RepositoryVerification.Status !=
			backupcontract.RepositoryVerificationVerified {
		t.Fatalf(
			"%s verified credential reuse was not preserved",
			environment.provider,
		)
	}
	state, err := scheduled.State(context.Background())
	if err != nil ||
		state.Plan == nil ||
		len(state.Plan.Store.CredentialCiphertext) == 0 ||
		state.Plan.Store.CredentialRevision != 1 ||
		state.Plan.RepositoryVerification == nil ||
		state.Plan.RepositoryVerification.Status !=
			backupcontract.RepositoryVerificationVerified {
		t.Fatalf(
			"%s durable credential reuse state is invalid",
			environment.provider,
		)
	}
}

func managerIntegrationBackupRequest(
	t *testing.T,
	server *Server,
	token string,
	method string,
	path string,
	body any,
) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatal("encode Manager integration request")
		}
		reader = bytes.NewReader(payload)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, reader)
	request.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	server.Engine().ServeHTTP(recorder, request)
	return recorder
}

func managerIntegrationDecode(
	t *testing.T,
	recorder *httptest.ResponseRecorder,
	target any,
) {
	t.Helper()
	if err := json.Unmarshal(recorder.Body.Bytes(), target); err != nil {
		t.Fatal("decode Manager integration response")
	}
}

func managerIntegrationAssertNoCredentials(
	t *testing.T,
	body []byte,
	environment managerCloudRepositoryEnvironment,
) {
	t.Helper()
	if bytes.Contains(body, []byte(environment.accessKey)) ||
		bytes.Contains(body, []byte(environment.secretKey)) ||
		bytes.Contains(body, []byte("credential_ciphertext")) {
		t.Fatal("Manager integration response exposed repository credentials")
	}
}

func managerIntegrationCleanupRepository(
	t *testing.T,
	provider *backupinfra.RepositoryProvider,
	stateStore *managerIntegrationBackupStateStore,
	providerName string,
) {
	t.Helper()
	state, err := stateStore.Load(context.Background())
	if err != nil || state.Plan == nil {
		return
	}
	cleanupCtx, cancel := context.WithTimeout(
		context.Background(),
		30*time.Second,
	)
	defer cancel()
	store, err := provider.Open(cleanupCtx, state.Plan.Store)
	if err != nil {
		t.Errorf("%s integration cleanup could not open repository", providerName)
		return
	}
	if err := store.DeletePrefix(cleanupCtx, "probes"); err != nil {
		t.Errorf("%s integration cleanup could not remove probes", providerName)
	}
	if err := store.DeletePrefix(cleanupCtx, "backups"); err != nil {
		t.Errorf("%s integration cleanup could not remove backups", providerName)
	}
	if err := store.Delete(
		cleanupCtx,
		backupartifact.RepositoryMarkerKey,
	); err != nil {
		t.Errorf(
			"%s integration cleanup could not remove repository identity",
			providerName,
		)
	}
}

func managerIntegrationRepositoryPrefix(provider string) string {
	random := make([]byte, 8)
	if _, err := io.ReadFull(rand.Reader, random); err != nil {
		panic("backup Manager integration random source unavailable")
	}
	return "wukongim-integration/" + provider + "/" +
		time.Now().UTC().Format("20060102T150405.000000000") + "-" +
		hex.EncodeToString(random)
}

type managerIntegrationBackupStateStore struct {
	mu    sync.Mutex
	state backupcontract.SystemState
}

func (s *managerIntegrationBackupStateStore) Load(
	context.Context,
) (backupcontract.SystemState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.Clone(), nil
}

func (s *managerIntegrationBackupStateStore) CompareAndSwap(
	_ context.Context,
	expectedRevision uint64,
	next backupcontract.SystemState,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Revision != expectedRevision {
		return backupusecase.ErrStateConflict
	}
	s.state = next.Clone()
	return nil
}

type managerIntegrationProbeCluster struct{}

func (managerIntegrationProbeCluster) NodeID() uint64 {
	return 1
}

func (managerIntegrationProbeCluster) LocalState(
	context.Context,
) (controller.ClusterState, error) {
	return controller.ClusterState{
		Nodes: []controller.Node{{
			NodeID:    1,
			Roles:     []controller.NodeRole{controller.NodeRoleData},
			JoinState: controller.NodeJoinStateActive,
		}},
	}, nil
}

type managerIntegrationProbeRemote struct{}

func (managerIntegrationProbeRemote) ProbeBackupRepository(
	context.Context,
	uint64,
	backupcontract.RepositoryProbeCommand,
) error {
	return fmt.Errorf(
		"single-node cluster unexpectedly attempted a remote repository probe",
	)
}
