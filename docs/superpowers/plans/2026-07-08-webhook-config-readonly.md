# Webhook Config Readonly Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a read-only manager API and web page that show the current node's normalized webhook startup configuration.

**Architecture:** `internal/app` owns the effective `WebhookConfig` and exposes it through a narrow provider wired into `internal/access/manager`. The manager HTTP layer owns auth, routing, and JSON response mapping; the React page reads `/manager/webhooks/config` and renders a compact read-only operations view. No task mutates webhook runtime state, writes config files, sends test callbacks, or adds delivery-log retention.

**Tech Stack:** Go manager HTTP with Gin, `internal/app` composition root, React 19 + TypeScript, react-intl, Vitest, Bun.

---

## File Structure

- Create: `internal/access/manager/webhooks.go`
  - Owns webhook config DTOs, provider interface, and `GET /manager/webhooks/config` handler.
- Create: `internal/access/manager/webhooks_test.go`
  - Tests route response shape, missing provider, and `cluster.webhook:r` permission.
- Create: `internal/access/manager/permissions.go`
  - Adds the existing frontend's permission snapshot route and includes `cluster.webhook`.
- Create: `internal/access/manager/permissions_test.go`
  - Tests permission snapshot response and permission guard.
- Modify: `internal/access/manager/server.go`
  - Adds `WebhookConfigProvider` to `Options` and registers webhook/permission routes.
- Create: `internal/app/webhook_config.go`
  - Adapts normalized `app.WebhookConfig` into the manager snapshot without exposing app config to HTTP.
- Modify: `internal/app/webhook_test.go`
  - Adds provider snapshot tests.
- Modify: `internal/app/wiring.go`
  - Wires `WebhookConfig: a` into the manager server.
- Modify: `wukongim.conf.example`
  - Adds documented `WK_WEBHOOK_*` startup keys.
- Modify: `web/src/lib/manager-api.types.ts`
  - Adds `ManagerWebhookConfigResponse`.
- Modify: `web/src/lib/manager-api.ts`
  - Adds `getWebhookConfig()`.
- Modify: `web/src/lib/manager-api.test.ts`
  - Covers the new API client request.
- Modify: `web/src/pages/settings/webhooks/page.tsx`
  - Replaces the coming-soon surface with the read-only config view.
- Modify: `web/src/pages/settings/webhooks/page.test.tsx`
  - Covers enabled, disabled, all-events, and error states.
- Modify: `web/src/pages/page-shells.test.tsx`
  - Updates the Webhook page assertions away from "Coming Soon".
- Modify: `web/src/i18n/messages/en.ts`
  - Adds English UI copy.
- Modify: `web/src/i18n/messages/zh-CN.ts`
  - Adds Chinese UI copy.

## Task 1: Manager Webhook Config Route

**Files:**
- Create: `internal/access/manager/webhooks.go`
- Create: `internal/access/manager/webhooks_test.go`
- Modify: `internal/access/manager/server.go`

- [ ] **Step 1: Write failing route tests**

Create `internal/access/manager/webhooks_test.go` with:

```go
package manager

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestManagerWebhookConfigReturnsStartupSnapshot(t *testing.T) {
	srv := New(Options{
		WebhookConfig: webhookConfigProviderFunc(func(context.Context) (WebhookConfigSnapshot, error) {
			return WebhookConfigSnapshot{
				Enabled:                   true,
				HTTPAddr:                  "http://127.0.0.1:19090/webhook",
				FocusEvents:               []string{"msg.notify", "msg.offline"},
				SupportedEvents:           []string{"msg.notify", "msg.offline", "user.onlinestatus"},
				QueueSize:                 1024,
				Workers:                   16,
				MsgNotifyBatchMaxItems:    100,
				MsgNotifyBatchMaxWait:     "500ms",
				OnlineStatusBatchMaxItems: 512,
				OnlineStatusBatchMaxWait:  "2s",
				OfflineUIDBatchSize:       512,
				RequestTimeout:            "5s",
				RetryMaxAttempts:          3,
				Source:                    "startup_config",
				RequiresRestart:           true,
			}, nil
		}),
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/webhooks/config", nil)
	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var body WebhookConfigSnapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !body.Enabled || body.HTTPAddr != "http://127.0.0.1:19090/webhook" {
		t.Fatalf("body = %#v", body)
	}
	if body.Source != "startup_config" || !body.RequiresRestart {
		t.Fatalf("source/restart = %q/%v", body.Source, body.RequiresRestart)
	}
	if len(body.SupportedEvents) != 3 {
		t.Fatalf("supported events = %#v", body.SupportedEvents)
	}
}

func TestManagerWebhookConfigUnavailableWithoutProvider(t *testing.T) {
	srv := New(Options{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/webhooks/config", nil)
	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}
}

func TestManagerWebhookConfigRequiresWebhookReadPermission(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "viewer",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.node",
				Actions:  []string{"r"},
			}},
		}, {
			Username: "webhook-reader",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.webhook",
				Actions:  []string{"r"},
			}},
		}}),
		WebhookConfig: webhookConfigProviderFunc(func(context.Context) (WebhookConfigSnapshot, error) {
			return WebhookConfigSnapshot{Enabled: false, Source: "startup_config", RequiresRestart: true}, nil
		}),
	})

	forbidden := httptest.NewRecorder()
	forbiddenReq := httptest.NewRequest(http.MethodGet, "/manager/webhooks/config", nil)
	forbiddenReq.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "viewer"))
	srv.Engine().ServeHTTP(forbidden, forbiddenReq)
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("forbidden status = %d, want %d", forbidden.Code, http.StatusForbidden)
	}

	ok := httptest.NewRecorder()
	okReq := httptest.NewRequest(http.MethodGet, "/manager/webhooks/config", nil)
	okReq.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "webhook-reader"))
	srv.Engine().ServeHTTP(ok, okReq)
	if ok.Code != http.StatusOK {
		t.Fatalf("ok status = %d, want %d; body=%s", ok.Code, http.StatusOK, ok.Body.String())
	}
}

type webhookConfigProviderFunc func(context.Context) (WebhookConfigSnapshot, error)

func (f webhookConfigProviderFunc) WebhookConfigSnapshot(ctx context.Context) (WebhookConfigSnapshot, error) {
	return f(ctx)
}
```

- [ ] **Step 2: Run route tests to verify RED**

Run:

```bash
GOWORK=off go test ./internal/access/manager -run 'TestManagerWebhookConfig' -count=1
```

Expected: fails to compile because `WebhookConfigSnapshot`, `WebhookConfig` option, and the route do not exist.

- [ ] **Step 3: Implement the manager route**

Add `internal/access/manager/webhooks.go`:

```go
package manager

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
)

// WebhookConfigProvider exposes the current node's effective webhook startup configuration.
type WebhookConfigProvider interface {
	WebhookConfigSnapshot(context.Context) (WebhookConfigSnapshot, error)
}

// WebhookConfigSnapshot is the manager-facing read-only webhook configuration response.
type WebhookConfigSnapshot struct {
	Enabled                   bool     `json:"enabled"`
	HTTPAddr                  string   `json:"http_addr"`
	FocusEvents               []string `json:"focus_events"`
	SupportedEvents           []string `json:"supported_events"`
	QueueSize                 int      `json:"queue_size"`
	Workers                   int      `json:"workers"`
	MsgNotifyBatchMaxItems    int      `json:"msg_notify_batch_max_items"`
	MsgNotifyBatchMaxWait     string   `json:"msg_notify_batch_max_wait"`
	OnlineStatusBatchMaxItems int      `json:"online_status_batch_max_items"`
	OnlineStatusBatchMaxWait  string   `json:"online_status_batch_max_wait"`
	OfflineUIDBatchSize       int      `json:"offline_uid_batch_size"`
	RequestTimeout            string   `json:"request_timeout"`
	RetryMaxAttempts          int      `json:"retry_max_attempts"`
	Source                    string   `json:"source"`
	RequiresRestart           bool     `json:"requires_restart"`
}

func (s *Server) handleWebhookConfig(c *gin.Context) {
	if s == nil || s.webhookConfig == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "webhook config provider is unavailable")
		return
	}
	snapshot, err := s.webhookConfig.WebhookConfigSnapshot(c.Request.Context())
	if err != nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", err.Error())
		return
	}
	c.JSON(http.StatusOK, snapshot)
}
```

Modify `internal/access/manager/server.go`:

```go
type Options struct {
	// ListenAddr is the manager server listen address.
	ListenAddr string
	// Auth configures manager JWT login.
	Auth AuthConfig
	// Management provides manager read usecases.
	Management Management
	// WebhookConfig exposes the current node's effective webhook startup configuration.
	WebhookConfig WebhookConfigProvider
	// RealtimeMonitor provides unified realtime monitor snapshots.
	RealtimeMonitor RealtimeMonitorProvider
	// Top provides local runtime pressure snapshots for read-only runtime views.
	Top accessapi.TopSnapshotProvider
	// Logger is the logger used by the manager server.
	Logger wklog.Logger
}
```

```go
type Server struct {
	mu              sync.RWMutex
	engine          *gin.Engine
	httpServer      *http.Server
	listener        net.Listener
	listenAddr      string
	addr            string
	management      Management
	webhookConfig   WebhookConfigProvider
	realtimeMonitor RealtimeMonitorProvider
	top             accessapi.TopSnapshotProvider
	auth            authState
	logger          wklog.Logger
	started         bool
}
```

```go
srv := &Server{
	engine:          engine,
	listenAddr:      strings.TrimSpace(opts.ListenAddr),
	management:      opts.Management,
	webhookConfig:   opts.WebhookConfig,
	realtimeMonitor: opts.RealtimeMonitor,
	top:             opts.Top,
	auth:            newAuthState(opts.Auth),
	logger:          opts.Logger,
}
```

Register the route in `registerRoutes`:

```go
	webhookReads := s.engine.Group("/manager")
	if s.auth.enabled() {
		webhookReads.Use(s.requirePermission("cluster.webhook", "r"))
	}
	webhookReads.GET("/webhooks/config", s.handleWebhookConfig)
```

- [ ] **Step 4: Run route tests to verify GREEN**

Run:

```bash
GOWORK=off go test ./internal/access/manager -run 'TestManagerWebhookConfig' -count=1
```

Expected: tests pass.

- [ ] **Step 5: Commit backend route**

Run:

```bash
git add internal/access/manager/server.go internal/access/manager/webhooks.go internal/access/manager/webhooks_test.go
git commit -m "feat(manager): expose readonly webhook config"
```

Expected: commit succeeds and unrelated local drafts remain unstaged.

## Task 2: Manager Permission Snapshot Catalog

**Files:**
- Create: `internal/access/manager/permissions.go`
- Create: `internal/access/manager/permissions_test.go`
- Modify: `internal/access/manager/server.go`

- [ ] **Step 1: Write failing permission catalog tests**

Create `internal/access/manager/permissions_test.go` with:

```go
package manager

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestManagerPermissionsReturnsConfiguredUsersAndCatalog(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.permission",
				Actions:  []string{"r"},
			}, {
				Resource: "cluster.webhook",
				Actions:  []string{"r"},
			}},
		}}),
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/permissions", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))
	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var body managerPermissionsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !body.AuthEnabled || body.CurrentUser != "admin" {
		t.Fatalf("auth fields = %#v", body)
	}
	if len(body.Users) != 1 || body.Users[0].Username != "admin" {
		t.Fatalf("users = %#v", body.Users)
	}
	if !permissionCatalogContains(body.Resources, "cluster.webhook", "r") {
		t.Fatalf("resources missing cluster.webhook:r: %#v", body.Resources)
	}
}

func TestManagerPermissionsRequiresPermissionRead(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "viewer",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.webhook",
				Actions:  []string{"r"},
			}},
		}}),
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/permissions", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "viewer"))
	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func permissionCatalogContains(resources []managerPermissionResourceDTO, resource string, action string) bool {
	for _, item := range resources {
		if item.Resource != resource {
			continue
		}
		for _, got := range item.Actions {
			if got == action {
				return true
			}
		}
	}
	return false
}
```

- [ ] **Step 2: Run permission tests to verify RED**

Run:

```bash
GOWORK=off go test ./internal/access/manager -run 'TestManagerPermissions' -count=1
```

Expected: fails because `/manager/permissions` and DTOs are not implemented.

- [ ] **Step 3: Implement permission snapshot route and catalog**

Add `internal/access/manager/permissions.go`:

```go
package manager

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

type managerPermissionsResponse struct {
	AuthEnabled bool                           `json:"auth_enabled"`
	CurrentUser string                         `json:"current_user"`
	Users       []managerPermissionUserDTO     `json:"users"`
	Resources   []managerPermissionResourceDTO `json:"resources"`
}

type managerPermissionUserDTO struct {
	Username    string               `json:"username"`
	Permissions []loginPermissionDTO `json:"permissions"`
}

type managerPermissionResourceDTO struct {
	Resource    string   `json:"resource"`
	Actions     []string `json:"actions"`
	Description string   `json:"description"`
}

var managerPermissionCatalog = []managerPermissionResourceDTO{
	{Resource: "*", Actions: []string{"*"}, Description: "Wildcard access to all manager resources and actions."},
	{Resource: "cluster.permission", Actions: []string{"r"}, Description: "Read manager authentication and permission configuration snapshots."},
	{Resource: "cluster.node", Actions: []string{"r", "w"}, Description: "Read node inventory and perform node lifecycle actions."},
	{Resource: "cluster.slot", Actions: []string{"r", "w"}, Description: "Read Slot inventory and perform Slot operations."},
	{Resource: "cluster.controller", Actions: []string{"r", "w"}, Description: "Read and operate Controller tasks and Raft state."},
	{Resource: "cluster.diagnostics", Actions: []string{"r", "w"}, Description: "Read diagnostics and manage temporary tracking rules."},
	{Resource: "cluster.log", Actions: []string{"r"}, Description: "Read ordinary application logs."},
	{Resource: "cluster.db", Actions: []string{"r"}, Description: "Run bounded read-only DB inspection queries."},
	{Resource: "cluster.channel", Actions: []string{"r", "w"}, Description: "Read channel data and perform channel operations."},
	{Resource: "cluster.connection", Actions: []string{"r"}, Description: "Read gateway connection inventory."},
	{Resource: "cluster.plugin", Actions: []string{"r", "w"}, Description: "Read and operate node-local plugins and plugin bindings."},
	{Resource: "cluster.user", Actions: []string{"r", "w"}, Description: "Read and operate user metadata and system UIDs."},
	{Resource: "cluster.webhook", Actions: []string{"r"}, Description: "Read webhook startup configuration snapshots."},
}

func (s *Server) handlePermissions(c *gin.Context) {
	currentUser := ""
	if s.auth.enabled() {
		currentUser = c.GetString(managerUsernameContextKey)
	}
	c.JSON(http.StatusOK, managerPermissionsResponse{
		AuthEnabled: s.auth.enabled(),
		CurrentUser: currentUser,
		Users:       s.permissionUsers(),
		Resources:   clonePermissionCatalog(),
	})
}

func (s *Server) permissionUsers() []managerPermissionUserDTO {
	if s == nil || !s.auth.enabled() {
		return nil
	}
	users := make([]managerPermissionUserDTO, 0, len(s.auth.users))
	for username := range s.auth.users {
		users = append(users, managerPermissionUserDTO{
			Username:    username,
			Permissions: loginPermissionDTOs(s.auth.permissionsFor(username)),
		})
	}
	return users
}

func clonePermissionCatalog() []managerPermissionResourceDTO {
	out := make([]managerPermissionResourceDTO, 0, len(managerPermissionCatalog))
	for _, item := range managerPermissionCatalog {
		out = append(out, managerPermissionResourceDTO{
			Resource:    item.Resource,
			Actions:     append([]string(nil), item.Actions...),
			Description: item.Description,
		})
	}
	return out
}
```

Register the route near the start of `registerRoutes` in `internal/access/manager/server.go`:

```go
	permissions := s.engine.Group("/manager")
	if s.auth.enabled() {
		permissions.Use(s.requirePermission("cluster.permission", "r"))
	}
	permissions.GET("/permissions", s.handlePermissions)
```

- [ ] **Step 4: Run permission tests to verify GREEN**

Run:

```bash
GOWORK=off go test ./internal/access/manager -run 'TestManagerPermissions' -count=1
```

Expected: tests pass.

- [ ] **Step 5: Commit permission catalog**

Run:

```bash
git add internal/access/manager/server.go internal/access/manager/permissions.go internal/access/manager/permissions_test.go
git commit -m "feat(manager): expose permission catalog"
```

Expected: commit succeeds and the catalog includes `cluster.webhook`.

## Task 3: App Webhook Config Provider And Wiring

**Files:**
- Create: `internal/app/webhook_config.go`
- Modify: `internal/app/webhook_test.go`
- Modify: `internal/app/wiring.go`

- [ ] **Step 1: Write failing app provider tests**

Append to `internal/app/webhook_test.go`:

```go
func TestWebhookConfigSnapshotUsesNormalizedStartupConfig(t *testing.T) {
	app, err := newTestApp(t, Config{
		DataDir: t.TempDir(),
		Webhook: WebhookConfig{
			HTTPAddr:    "http://127.0.0.1:19090/webhook",
			FocusEvents: []string{"msg.notify"},
		},
	}, WithCluster(&fakeCluster{}))
	require.NoError(t, err)

	snapshot, err := app.WebhookConfigSnapshot(context.Background())

	require.NoError(t, err)
	require.True(t, snapshot.Enabled)
	require.Equal(t, "http://127.0.0.1:19090/webhook", snapshot.HTTPAddr)
	require.Equal(t, []string{"msg.notify"}, snapshot.FocusEvents)
	require.Equal(t, []string{"msg.notify", "msg.offline", "user.onlinestatus"}, snapshot.SupportedEvents)
	require.Equal(t, 1024, snapshot.QueueSize)
	require.Equal(t, 16, snapshot.Workers)
	require.Equal(t, 100, snapshot.MsgNotifyBatchMaxItems)
	require.Equal(t, "500ms", snapshot.MsgNotifyBatchMaxWait)
	require.Equal(t, 512, snapshot.OnlineStatusBatchMaxItems)
	require.Equal(t, "2s", snapshot.OnlineStatusBatchMaxWait)
	require.Equal(t, 512, snapshot.OfflineUIDBatchSize)
	require.Equal(t, "5s", snapshot.RequestTimeout)
	require.Equal(t, 3, snapshot.RetryMaxAttempts)
	require.Equal(t, "startup_config", snapshot.Source)
	require.True(t, snapshot.RequiresRestart)
}

func TestWebhookConfigSnapshotReportsDisabledDefaults(t *testing.T) {
	app, err := newTestApp(t, Config{DataDir: t.TempDir()}, WithCluster(&fakeCluster{}))
	require.NoError(t, err)

	snapshot, err := app.WebhookConfigSnapshot(context.Background())

	require.NoError(t, err)
	require.False(t, snapshot.Enabled)
	require.Empty(t, snapshot.HTTPAddr)
	require.Empty(t, snapshot.FocusEvents)
	require.Equal(t, []string{"msg.notify", "msg.offline", "user.onlinestatus"}, snapshot.SupportedEvents)
	require.Equal(t, "startup_config", snapshot.Source)
	require.True(t, snapshot.RequiresRestart)
}
```

- [ ] **Step 2: Run app provider tests to verify RED**

Run:

```bash
GOWORK=off go test ./internal/app -run 'TestWebhookConfigSnapshot' -count=1
```

Expected: fails to compile because `WebhookConfigSnapshot` is not implemented on `*App`.

- [ ] **Step 3: Implement app provider**

Add `internal/app/webhook_config.go`:

```go
package app

import (
	"context"

	accessmanager "github.com/WuKongIM/WuKongIM/internal/access/manager"
	runtimewebhook "github.com/WuKongIM/WuKongIM/internal/runtime/webhook"
)

const webhookConfigSourceStartup = "startup_config"

// WebhookConfigSnapshot returns the current node's normalized startup webhook configuration.
func (a *App) WebhookConfigSnapshot(context.Context) (accessmanager.WebhookConfigSnapshot, error) {
	if a == nil {
		return accessmanager.WebhookConfigSnapshot{}, nil
	}
	cfg := a.cfg.Webhook
	return accessmanager.WebhookConfigSnapshot{
		Enabled:                   cfg.Enabled,
		HTTPAddr:                  cfg.HTTPAddr,
		FocusEvents:               append([]string(nil), cfg.FocusEvents...),
		SupportedEvents:           supportedWebhookEvents(),
		QueueSize:                 cfg.QueueSize,
		Workers:                   cfg.Workers,
		MsgNotifyBatchMaxItems:    cfg.NotifyBatchMaxItems,
		MsgNotifyBatchMaxWait:     cfg.NotifyBatchMaxWait.String(),
		OnlineStatusBatchMaxItems: cfg.OnlineBatchMaxItems,
		OnlineStatusBatchMaxWait:  cfg.OnlineBatchMaxWait.String(),
		OfflineUIDBatchSize:       cfg.OfflineUIDBatchSize,
		RequestTimeout:            cfg.RequestTimeout.String(),
		RetryMaxAttempts:          cfg.RetryMaxAttempts,
		Source:                    webhookConfigSourceStartup,
		RequiresRestart:           true,
	}, nil
}

func supportedWebhookEvents() []string {
	return []string{
		runtimewebhook.EventMsgNotify,
		runtimewebhook.EventMsgOffline,
		runtimewebhook.EventUserOnlineStatus,
	}
}
```

Modify `internal/app/wiring.go` in `wireManager`:

```go
			Management:      management,
			WebhookConfig:   a,
			RealtimeMonitor: a.newManagerMonitorProvider(management),
```

- [ ] **Step 4: Run app provider tests to verify GREEN**

Run:

```bash
GOWORK=off go test ./internal/app -run 'TestWebhookConfigSnapshot' -count=1
```

Expected: tests pass.

- [ ] **Step 5: Run combined backend tests**

Run:

```bash
GOWORK=off go test ./internal/app ./internal/access/manager -run 'Webhook|ManagerPermissions' -count=1
```

Expected: tests pass.

- [ ] **Step 6: Commit app provider wiring**

Run:

```bash
git add internal/app/webhook_config.go internal/app/webhook_test.go internal/app/wiring.go
git commit -m "feat(app): provide webhook config snapshot"
```

Expected: commit succeeds.

## Task 4: Webhook Example Config

**Files:**
- Modify: `wukongim.conf.example`

- [ ] **Step 1: Add webhook config example section**

Insert this section after delivery settings or before plugin settings in `wukongim.conf.example`:

```text
# Node-local best-effort webhook delivery. Configure an endpoint to enable it.
# Webhook queues are bounded to protect high-throughput SEND paths; failures,
# retry exhaustion, and queue-full drops never change SENDACK or durable append success.
# Changes to WK_WEBHOOK_* keys require a process restart.
# WK_WEBHOOK_HTTP_ADDR=http://127.0.0.1:19090/webhook

# Optional event filter. Empty list means all supported events are delivered.
# Supported events: msg.notify, msg.offline, user.onlinestatus. List fields use one JSON string value.
WK_WEBHOOK_FOCUS_EVENTS=[]

# Maximum queued webhook events retained in memory per event queue before new events are dropped.
WK_WEBHOOK_QUEUE_SIZE=1024

# Maximum concurrent webhook sender workers per event queue.
WK_WEBHOOK_WORKERS=16

# Maximum msg.notify messages sent in one webhook request.
WK_WEBHOOK_MSG_NOTIFY_BATCH_MAX_ITEMS=100

# Maximum wait before sending a partial msg.notify batch.
WK_WEBHOOK_MSG_NOTIFY_BATCH_MAX_WAIT=500ms

# Maximum user.onlinestatus records sent in one webhook request.
WK_WEBHOOK_ONLINE_STATUS_BATCH_MAX_ITEMS=512

# Maximum wait before sending a partial user.onlinestatus batch.
WK_WEBHOOK_ONLINE_STATUS_BATCH_MAX_WAIT=2s

# Maximum offline recipient UIDs sent in one msg.offline request.
WK_WEBHOOK_OFFLINE_UID_BATCH_SIZE=512

# Timeout for one outbound webhook request attempt.
WK_WEBHOOK_REQUEST_TIMEOUT=5s

# Maximum attempts for one admitted webhook batch before it is dropped.
WK_WEBHOOK_RETRY_MAX_ATTEMPTS=3
```

- [ ] **Step 2: Verify config parser tests still pass**

Run:

```bash
GOWORK=off go test ./cmd/wukongim -run 'Webhook|Config' -count=1
```

Expected: tests pass because the parser already supports these keys.

- [ ] **Step 3: Commit config example**

Run:

```bash
git add wukongim.conf.example
git commit -m "docs(config): document webhook startup settings"
```

Expected: commit succeeds.

## Task 5: Web Manager API Client

**Files:**
- Modify: `web/src/lib/manager-api.types.ts`
- Modify: `web/src/lib/manager-api.ts`
- Modify: `web/src/lib/manager-api.test.ts`

- [ ] **Step 1: Write failing API client test**

Modify the import list in `web/src/lib/manager-api.test.ts` to include:

```ts
  getWebhookConfig,
```

Add this test near the permissions test:

```ts
  it("fetches webhook config", async () => {
    const payload = {
      enabled: true,
      http_addr: "http://127.0.0.1:19090/webhook",
      focus_events: ["msg.notify"],
      supported_events: ["msg.notify", "msg.offline", "user.onlinestatus"],
      queue_size: 1024,
      workers: 16,
      msg_notify_batch_max_items: 100,
      msg_notify_batch_max_wait: "500ms",
      online_status_batch_max_items: 512,
      online_status_batch_max_wait: "2s",
      offline_uid_batch_size: 512,
      request_timeout: "5s",
      retry_max_attempts: 3,
      source: "startup_config",
      requires_restart: true,
    }
    fetchMock.mockResolvedValue(new Response(JSON.stringify(payload), { status: 200 }))

    await expect(getWebhookConfig()).resolves.toEqual(payload)

    expect(fetchMock).toHaveBeenCalledWith(
      "/manager/webhooks/config",
      expect.objectContaining({ headers: expect.any(Headers) }),
    )
  })
```

- [ ] **Step 2: Run API client test to verify RED**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/lib/manager-api.test.ts
```

Expected: fails because `getWebhookConfig` is not exported.

- [ ] **Step 3: Add API type and function**

Append to `web/src/lib/manager-api.types.ts` near other manager response types:

```ts
export type ManagerWebhookConfigResponse = {
  enabled: boolean
  http_addr: string
  focus_events: string[]
  supported_events: string[]
  queue_size: number
  workers: number
  msg_notify_batch_max_items: number
  msg_notify_batch_max_wait: string
  online_status_batch_max_items: number
  online_status_batch_max_wait: string
  offline_uid_batch_size: number
  request_timeout: string
  retry_max_attempts: number
  source: string
  requires_restart: boolean
}
```

Modify the type import in `web/src/lib/manager-api.ts`:

```ts
  ManagerWebhookConfigResponse,
```

Add the API function near `getPermissions()`:

```ts
export function getWebhookConfig() {
  return jsonManagerFetch<ManagerWebhookConfigResponse>("/manager/webhooks/config")
}
```

- [ ] **Step 4: Run API client test to verify GREEN**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/lib/manager-api.test.ts
```

Expected: tests pass.

- [ ] **Step 5: Commit API client**

Run:

```bash
git add web/src/lib/manager-api.types.ts web/src/lib/manager-api.ts web/src/lib/manager-api.test.ts
git commit -m "feat(web): add webhook config api client"
```

Expected: commit succeeds.

## Task 6: Webhook Configuration Page

**Files:**
- Modify: `web/src/pages/settings/webhooks/page.tsx`
- Modify: `web/src/pages/settings/webhooks/page.test.tsx`
- Modify: `web/src/pages/page-shells.test.tsx`
- Modify: `web/src/i18n/messages/en.ts`
- Modify: `web/src/i18n/messages/zh-CN.ts`

- [ ] **Step 1: Write failing page tests**

Replace `web/src/pages/settings/webhooks/page.test.tsx` with:

```tsx
import { render, screen, within } from "@testing-library/react"
import { beforeEach, expect, test, vi } from "vitest"

import { resetLocale } from "@/i18n/locale-store"
import { I18nProvider } from "@/i18n/provider"
import { ManagerApiError } from "@/lib/manager-api"
import { WebhooksPage } from "@/pages/settings/webhooks/page"

const getWebhookConfigMock = vi.fn()

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return {
    ...actual,
    getWebhookConfig: (...args: unknown[]) => getWebhookConfigMock(...args),
  }
})

beforeEach(() => {
  localStorage.clear()
  resetLocale()
  getWebhookConfigMock.mockReset()
})

function renderWebhooksPage() {
  return render(
    <I18nProvider>
      <WebhooksPage />
    </I18nProvider>,
  )
}

test("renders enabled startup webhook configuration", async () => {
  getWebhookConfigMock.mockResolvedValueOnce({
    enabled: true,
    http_addr: "http://127.0.0.1:19090/webhook",
    focus_events: ["msg.notify", "msg.offline"],
    supported_events: ["msg.notify", "msg.offline", "user.onlinestatus"],
    queue_size: 1024,
    workers: 16,
    msg_notify_batch_max_items: 100,
    msg_notify_batch_max_wait: "500ms",
    online_status_batch_max_items: 512,
    online_status_batch_max_wait: "2s",
    offline_uid_batch_size: 512,
    request_timeout: "5s",
    retry_max_attempts: 3,
    source: "startup_config",
    requires_restart: true,
  })

  renderWebhooksPage()

  expect(await screen.findByText("Enabled")).toBeInTheDocument()
  expect(screen.getByText("startup_config")).toBeInTheDocument()
  expect(screen.getByText("Restart required")).toBeInTheDocument()
  expect(screen.getByText("http://127.0.0.1:19090/webhook")).toBeInTheDocument()
  expect(screen.getByText("msg.notify")).toBeInTheDocument()
  expect(screen.getByText("msg.offline")).toBeInTheDocument()
  expect(screen.getByText("user.onlinestatus")).toBeInTheDocument()

  const runtime = screen.getByTestId("webhooks-runtime-table")
  expect(within(runtime).getByText("Queue size")).toBeInTheDocument()
  expect(within(runtime).getByText("1024")).toBeInTheDocument()
  expect(within(runtime).getByText("Workers")).toBeInTheDocument()
  expect(within(runtime).getByText("16")).toBeInTheDocument()
  expect(screen.queryByRole("button", { name: /save/i })).not.toBeInTheDocument()
  expect(screen.queryByRole("switch")).not.toBeInTheDocument()
})

test("renders disabled webhook configuration and all-event focus", async () => {
  getWebhookConfigMock.mockResolvedValueOnce({
    enabled: false,
    http_addr: "",
    focus_events: [],
    supported_events: ["msg.notify", "msg.offline", "user.onlinestatus"],
    queue_size: 1024,
    workers: 16,
    msg_notify_batch_max_items: 100,
    msg_notify_batch_max_wait: "500ms",
    online_status_batch_max_items: 512,
    online_status_batch_max_wait: "2s",
    offline_uid_batch_size: 512,
    request_timeout: "5s",
    retry_max_attempts: 3,
    source: "startup_config",
    requires_restart: true,
  })

  renderWebhooksPage()

  expect(await screen.findByText("Disabled")).toBeInTheDocument()
  expect(screen.getByText("No webhook endpoint is configured.")).toBeInTheDocument()
  expect(screen.getByText("All supported events")).toBeInTheDocument()
})

test("maps forbidden and unavailable errors", async () => {
  getWebhookConfigMock.mockRejectedValueOnce(new ManagerApiError(403, "forbidden", "forbidden"))
  const { unmount } = renderWebhooksPage()

  expect(await screen.findByText("You do not have permission to view this manager resource.")).toBeInTheDocument()
  unmount()

  getWebhookConfigMock.mockRejectedValueOnce(new ManagerApiError(503, "service_unavailable", "unavailable"))
  renderWebhooksPage()

  expect(await screen.findByText("The manager service is currently unavailable.")).toBeInTheDocument()
})
```

Update `web/src/pages/page-shells.test.tsx` expectations:

```tsx
  ["/system/webhooks", "Webhook Configuration", "Webhook Status"],
```

```tsx
  ["/system/webhooks", "Webhook 配置", "Webhook 状态"],
```

- [ ] **Step 2: Run page tests to verify RED**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/settings/webhooks/page.test.tsx src/pages/page-shells.test.tsx
```

Expected: fails because the page still renders the coming-soon surface and copy keys do not exist.

- [ ] **Step 3: Implement the read-only page**

Replace `web/src/pages/settings/webhooks/page.tsx` with:

```tsx
import { useCallback, useEffect, useMemo, useState } from "react"
import { useIntl } from "react-intl"

import { ResourceState } from "@/components/manager/resource-state"
import { PageContainer } from "@/components/shell/page-container"
import { PageHeader } from "@/components/shell/page-header"
import { SectionCard } from "@/components/shell/section-card"
import { ManagerApiError, getWebhookConfig } from "@/lib/manager-api"
import type { ManagerWebhookConfigResponse } from "@/lib/manager-api.types"

type WebhookConfigState = {
  config: ManagerWebhookConfigResponse | null
  loading: boolean
  error: Error | null
}

type RuntimeRow = {
  label: string
  value: string | number
}

function emptyState(): WebhookConfigState {
  return {
    config: null,
    loading: true,
    error: null,
  }
}

function mapErrorKind(error: Error | null) {
  if (!(error instanceof ManagerApiError)) {
    return "error" as const
  }
  if (error.status === 403) {
    return "forbidden" as const
  }
  if (error.status === 503) {
    return "unavailable" as const
  }
  return "error" as const
}

function activeEventSet(config: ManagerWebhookConfigResponse) {
  if (config.focus_events.length === 0) {
    return new Set(config.supported_events)
  }
  return new Set(config.focus_events)
}

function runtimeRows(config: ManagerWebhookConfigResponse, labels: Record<string, string>): RuntimeRow[] {
  return [
    { label: labels.queueSize, value: config.queue_size },
    { label: labels.workers, value: config.workers },
    { label: labels.notifyItems, value: config.msg_notify_batch_max_items },
    { label: labels.notifyWait, value: config.msg_notify_batch_max_wait },
    { label: labels.onlineItems, value: config.online_status_batch_max_items },
    { label: labels.onlineWait, value: config.online_status_batch_max_wait },
    { label: labels.offlineUIDs, value: config.offline_uid_batch_size },
    { label: labels.timeout, value: config.request_timeout },
    { label: labels.retries, value: config.retry_max_attempts },
  ]
}

export function WebhooksPage() {
  const intl = useIntl()
  const [state, setState] = useState<WebhookConfigState>(emptyState)

  const runQuery = useCallback(async () => {
    setState((current) => ({ ...current, loading: true, error: null }))
    try {
      const config = await getWebhookConfig()
      setState({ config, loading: false, error: null })
    } catch (error) {
      setState({
        config: null,
        loading: false,
        error: error instanceof Error ? error : new Error("webhook config request failed"),
      })
    }
  }, [])

  useEffect(() => {
    void runQuery()
  }, [runQuery])

  const config = state.config
  const rows = useMemo(() => {
    if (!config) {
      return []
    }
    return runtimeRows(config, {
      queueSize: intl.formatMessage({ id: "webhooks.runtime.queueSize" }),
      workers: intl.formatMessage({ id: "webhooks.runtime.workers" }),
      notifyItems: intl.formatMessage({ id: "webhooks.runtime.notifyItems" }),
      notifyWait: intl.formatMessage({ id: "webhooks.runtime.notifyWait" }),
      onlineItems: intl.formatMessage({ id: "webhooks.runtime.onlineItems" }),
      onlineWait: intl.formatMessage({ id: "webhooks.runtime.onlineWait" }),
      offlineUIDs: intl.formatMessage({ id: "webhooks.runtime.offlineUIDs" }),
      timeout: intl.formatMessage({ id: "webhooks.runtime.timeout" }),
      retries: intl.formatMessage({ id: "webhooks.runtime.retries" }),
    })
  }, [config, intl])

  const activeEvents = config ? activeEventSet(config) : new Set<string>()

  return (
    <PageContainer>
      <PageHeader
        title={intl.formatMessage({ id: "webhooks.title" })}
        description={intl.formatMessage({ id: "webhooks.description" })}
      />

      {state.loading ? <ResourceState kind="loading" title={intl.formatMessage({ id: "webhooks.title" })} /> : null}
      {!state.loading && state.error ? (
        <ResourceState
          kind={mapErrorKind(state.error)}
          onRetry={() => {
            void runQuery()
          }}
          title={intl.formatMessage({ id: "webhooks.title" })}
        />
      ) : null}

      {!state.loading && !state.error && config ? (
        <>
          <SectionCard
            description={intl.formatMessage({ id: "webhooks.status.description" })}
            title={intl.formatMessage({ id: "webhooks.status.title" })}
          >
            <div className="grid overflow-hidden rounded-md border border-border bg-card md:grid-cols-3">
              <div className="border-b border-border px-3 py-3 text-sm md:border-r md:border-b-0">
                <div className="text-muted-foreground">{intl.formatMessage({ id: "webhooks.status.enabled" })}</div>
                <div className="mt-1 font-semibold text-foreground">
                  {config.enabled
                    ? intl.formatMessage({ id: "webhooks.status.enabledValue" })
                    : intl.formatMessage({ id: "webhooks.status.disabledValue" })}
                </div>
              </div>
              <div className="border-b border-border px-3 py-3 text-sm md:border-r md:border-b-0">
                <div className="text-muted-foreground">{intl.formatMessage({ id: "webhooks.status.source" })}</div>
                <div className="mt-1 font-semibold text-foreground">{config.source}</div>
              </div>
              <div className="px-3 py-3 text-sm">
                <div className="text-muted-foreground">{intl.formatMessage({ id: "webhooks.status.restart" })}</div>
                <div className="mt-1 font-semibold text-foreground">
                  {config.requires_restart
                    ? intl.formatMessage({ id: "webhooks.status.restartRequired" })
                    : intl.formatMessage({ id: "webhooks.status.restartNotRequired" })}
                </div>
              </div>
            </div>
          </SectionCard>

          <SectionCard
            description={intl.formatMessage({ id: "webhooks.target.description" })}
            title={intl.formatMessage({ id: "webhooks.target.title" })}
          >
            {config.http_addr ? (
              <div className="rounded-md border border-border bg-muted/30 px-3 py-3 font-mono text-sm text-foreground">
                {config.http_addr}
              </div>
            ) : (
              <ResourceState
                description={intl.formatMessage({ id: "webhooks.target.empty" })}
                kind="empty"
                title={intl.formatMessage({ id: "webhooks.target.title" })}
              />
            )}
          </SectionCard>

          <SectionCard
            description={intl.formatMessage({ id: "webhooks.events.description" })}
            title={intl.formatMessage({ id: "webhooks.events.title" })}
          >
            <div className="mb-3 text-sm font-medium text-muted-foreground">
              {config.focus_events.length === 0
                ? intl.formatMessage({ id: "webhooks.events.all" })
                : intl.formatMessage({ id: "webhooks.events.filtered" }, { count: config.focus_events.length })}
            </div>
            <div className="flex flex-wrap gap-2">
              {config.supported_events.map((event) => (
                <span
                  className={`rounded-md border px-2 py-1 text-xs font-medium ${
                    activeEvents.has(event)
                      ? "border-emerald-500/35 bg-emerald-500/10 text-emerald-600"
                      : "border-border bg-muted/30 text-muted-foreground"
                  }`}
                  key={event}
                >
                  {event}
                </span>
              ))}
            </div>
          </SectionCard>

          <SectionCard
            description={intl.formatMessage({ id: "webhooks.runtime.description" })}
            title={intl.formatMessage({ id: "webhooks.runtime.title" })}
          >
            <div className="overflow-x-auto rounded-md border border-border" data-testid="webhooks-runtime-table">
              <table aria-label={intl.formatMessage({ id: "webhooks.runtime.title" })} className="w-full border-collapse text-sm">
                <tbody>
                  {rows.map((row) => (
                    <tr className="border-t border-border first:border-t-0" key={row.label}>
                      <th className="w-1/2 px-3 py-3 text-left font-medium text-foreground">{row.label}</th>
                      <td className="px-3 py-3 font-mono text-muted-foreground">{row.value}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            <p className="mt-4 border-t border-border pt-3 text-sm text-muted-foreground">
              {intl.formatMessage({ id: "webhooks.restartNotice" })}
            </p>
          </SectionCard>
        </>
      ) : null}
    </PageContainer>
  )
}
```

- [ ] **Step 4: Add i18n messages**

Append these keys in `web/src/i18n/messages/en.ts` near existing `webhooks.*` keys:

```ts
  "webhooks.status.title": "Webhook Status",
  "webhooks.status.description": "Current startup configuration for node-local webhook delivery.",
  "webhooks.status.enabled": "State",
  "webhooks.status.enabledValue": "Enabled",
  "webhooks.status.disabledValue": "Disabled",
  "webhooks.status.source": "Source",
  "webhooks.status.restart": "Change mode",
  "webhooks.status.restartRequired": "Restart required",
  "webhooks.status.restartNotRequired": "Live update",
  "webhooks.target.title": "Callback Target",
  "webhooks.target.description": "HTTP endpoint receiving JSON webhook POST requests with the event query parameter.",
  "webhooks.target.empty": "No webhook endpoint is configured.",
  "webhooks.events.title": "Event Filter",
  "webhooks.events.description": "Supported webhook event names and the currently active filter.",
  "webhooks.events.all": "All supported events",
  "webhooks.events.filtered": "{count} filtered events",
  "webhooks.runtime.title": "Runtime Parameters",
  "webhooks.runtime.description": "Bounded queue, batching, timeout, and retry settings applied at startup.",
  "webhooks.runtime.queueSize": "Queue size",
  "webhooks.runtime.workers": "Workers",
  "webhooks.runtime.notifyItems": "msg.notify batch max items",
  "webhooks.runtime.notifyWait": "msg.notify batch max wait",
  "webhooks.runtime.onlineItems": "user.onlinestatus batch max items",
  "webhooks.runtime.onlineWait": "user.onlinestatus batch max wait",
  "webhooks.runtime.offlineUIDs": "msg.offline UID batch size",
  "webhooks.runtime.timeout": "Request timeout",
  "webhooks.runtime.retries": "Retry max attempts",
  "webhooks.restartNotice": "This is a startup configuration snapshot. Update wukongim.conf or WK_WEBHOOK_* environment variables and restart the process to apply changes.",
```

Append these keys in `web/src/i18n/messages/zh-CN.ts`:

```ts
  "webhooks.status.title": "Webhook 状态",
  "webhooks.status.description": "当前节点本地 Webhook 投递的启动配置。",
  "webhooks.status.enabled": "状态",
  "webhooks.status.enabledValue": "已启用",
  "webhooks.status.disabledValue": "未启用",
  "webhooks.status.source": "来源",
  "webhooks.status.restart": "变更方式",
  "webhooks.status.restartRequired": "需要重启",
  "webhooks.status.restartNotRequired": "实时更新",
  "webhooks.target.title": "回调目标",
  "webhooks.target.description": "接收 JSON Webhook POST 请求的 HTTP 地址，请求会携带 event 查询参数。",
  "webhooks.target.empty": "当前没有配置 Webhook 回调地址。",
  "webhooks.events.title": "事件筛选",
  "webhooks.events.description": "支持的 Webhook 事件名与当前启用的筛选范围。",
  "webhooks.events.all": "全部支持事件",
  "webhooks.events.filtered": "{count} 个筛选事件",
  "webhooks.runtime.title": "运行参数",
  "webhooks.runtime.description": "启动时生效的有界队列、批量、超时与重试配置。",
  "webhooks.runtime.queueSize": "队列大小",
  "webhooks.runtime.workers": "Worker 数",
  "webhooks.runtime.notifyItems": "msg.notify 批量上限",
  "webhooks.runtime.notifyWait": "msg.notify 最大等待",
  "webhooks.runtime.onlineItems": "user.onlinestatus 批量上限",
  "webhooks.runtime.onlineWait": "user.onlinestatus 最大等待",
  "webhooks.runtime.offlineUIDs": "msg.offline UID 批量大小",
  "webhooks.runtime.timeout": "请求超时",
  "webhooks.runtime.retries": "最大重试次数",
  "webhooks.restartNotice": "这是启动配置快照。请更新 wukongim.conf 或 WK_WEBHOOK_* 环境变量并重启进程后生效。",
```

- [ ] **Step 5: Run page tests to verify GREEN**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/settings/webhooks/page.test.tsx src/pages/page-shells.test.tsx
```

Expected: tests pass.

- [ ] **Step 6: Commit frontend page**

Run:

```bash
git add web/src/pages/settings/webhooks/page.tsx web/src/pages/settings/webhooks/page.test.tsx web/src/pages/page-shells.test.tsx web/src/i18n/messages/en.ts web/src/i18n/messages/zh-CN.ts
git commit -m "feat(web): render readonly webhook config"
```

Expected: commit succeeds.

## Task 7: Final Verification

**Files:**
- Verify all files touched by Tasks 1-6.

- [ ] **Step 1: Run focused Go tests**

Run:

```bash
GOWORK=off go test ./internal/app ./internal/access/manager -run 'Webhook|ManagerPermissions' -count=1
```

Expected: pass.

- [ ] **Step 2: Run focused web tests**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/lib/manager-api.test.ts src/pages/settings/webhooks/page.test.tsx src/pages/page-shells.test.tsx
```

Expected: pass.

- [ ] **Step 3: Run web build**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run build
```

Expected: build succeeds.

- [ ] **Step 4: Run diff whitespace check**

Run:

```bash
git diff --check
```

Expected: no output.

- [ ] **Step 5: Inspect worktree scope**

Run:

```bash
git status --short
```

Expected: only files from this plan plus pre-existing unrelated drafts are present. Do not stage unrelated drafts:

```text
 M web/src/app/layout/sidebar-nav.test.tsx
 M web/src/app/layout/sidebar-nav.tsx
?? docs/superpowers/plans/2026-07-05-legacy-app-slot-store-adapter.md
```

- [ ] **Step 6: Commit final verification fixes if needed**

If Step 1-4 require small fixes, commit only the files changed for those fixes:

```bash
git add <files changed by verification fixes>
git commit -m "fix: stabilize webhook config page"
```

Expected: no commit is created when no fixes are needed.
