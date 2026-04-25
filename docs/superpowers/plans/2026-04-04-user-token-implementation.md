# User Token API Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement old-compatible `POST /user/token` with per-device token storage, local master-device kicking, and current-repo layering.

**Architecture:** Add a dedicated `internal/usecase/user` package that validates the old request semantics, ensures user existence, upserts per-device token state, and kicks local same-device sessions for master logins. Back it with a new replicated `metadb.Device` path through `metafsm` and `metastore`, then expose it through `internal/access/api` and wire it from `internal/app`.

**Tech Stack:** Go 1.23, Gin, `internal/access/api`, `internal/usecase/user`, `internal/runtime/online`, `pkg/storage/metadb`, `pkg/storage/metafsm`, `pkg/storage/metastore`, `testing`, `testify`.

**Spec:** `docs/superpowers/specs/2026-04-04-user-token-design.md`

---

## Execution Notes

- Use `@superpowers/test-driven-development` for every task: test first, confirm the right failure, then implement the minimum code to pass.
- Keep commits scoped to the task listed below.
- Do not expand scope into `/user/device_quit`, gateway verifier wiring, system-uid rejection, or permission checks.
- Preserve existing `metadb.User` and `metastore.GetUser(uid)` semantics. The new work adds `Device`; it does not repurpose `User`.
- Append new table ids and command ids. Do not renumber existing ids in `catalog.go` or `command.go`.
- Before final handoff, use `@superpowers/verification-before-completion`.

## File Map

| Path | Responsibility |
|------|----------------|
| `internal/usecase/user/app.go` | user use case construction, defaults, injected collaborators |
| `internal/usecase/user/command.go` | `UpdateTokenCommand` and request validation |
| `internal/usecase/user/deps.go` | store and runtime dependency interfaces for the user use case |
| `internal/usecase/user/token.go` | update-token orchestration and local kick behavior |
| `internal/usecase/user/token_test.go` | use case tests for validation, ensure-user, device upsert, and kicking |
| `internal/access/api/server.go` | API server options/interfaces extended with the user use case |
| `internal/access/api/routes.go` | route registration for `POST /user/token` |
| `internal/access/api/user_token.go` | request binding and old-style response envelope for `/user/token` |
| `internal/access/api/server_test.go` | handler tests locking request mapping and response compatibility |
| `pkg/storage/metadb/catalog.go` | new `Device` table id, column ids, and table descriptor |
| `pkg/storage/metadb/codec.go` | device primary-key and family-value encode/decode helpers |
| `pkg/storage/metadb/batch.go` | batched `UpsertDevice` support for replicated applies |
| `pkg/storage/metadb/device.go` | shard-scoped `GetDevice` and `UpsertDevice` implementation |
| `pkg/storage/metadb/device_test.go` | slot-scoped device storage tests |
| `pkg/storage/metafsm/command.go` | `cmdTypeUpsertDevice`, encode/decode/apply path |
| `pkg/storage/metafsm/state_machine_test.go` | device command round-trip and state-machine apply coverage |
| `pkg/storage/metastore/store.go` | cluster-routed `GetDevice` and `UpsertDevice` facade methods |
| `pkg/storage/metastore/integration_test.go` | store-level device replication tests |
| `internal/app/build.go` | composition-root wiring of the new user use case into the API server |
| `internal/app/integration_test.go` | started-app coverage for `/user/token` and persisted device metadata |

## Task 1: Create the `internal/usecase/user` update-token flow

**Files:**
- Create: `internal/usecase/user/app.go`
- Create: `internal/usecase/user/command.go`
- Create: `internal/usecase/user/deps.go`
- Create: `internal/usecase/user/token.go`
- Create: `internal/usecase/user/token_test.go`

- [ ] **Step 1: Write the failing use case tests**

Create `internal/usecase/user/token_test.go` with focused tests:

- `TestUpdateTokenCreatesUserWhenMissingAndUpsertsDevice`
- `TestUpdateTokenDoesNotRewriteExistingUser`
- `TestUpdateTokenRejectsEmptyUID`
- `TestUpdateTokenRejectsEmptyToken`
- `TestUpdateTokenRejectsLegacyForbiddenUIDChars`
- `TestUpdateTokenReturnsDependencyErrorWhenStoresMissing`
- `TestUpdateTokenMasterKicksOnlySameDeviceFlagSessions`
- `TestUpdateTokenSlaveDoesNotKickSessions`

Test sketch:

```go
func TestUpdateTokenMasterKicksOnlySameDeviceFlagSessions(t *testing.T) {
	reg := online.NewRegistry()
	sameDevice := newRecordingSession(11, "tcp")
	otherDevice := newRecordingSession(12, "tcp")
	require.NoError(t, reg.Register(online.OnlineConn{
		SessionID:   11,
		UID:         "u1",
		DeviceFlag:  wkframe.APP,
		DeviceLevel: wkframe.DeviceLevelMaster,
		Session:     sameDevice,
	}))
	require.NoError(t, reg.Register(online.OnlineConn{
		SessionID:   12,
		UID:         "u1",
		DeviceFlag:  wkframe.WEB,
		DeviceLevel: wkframe.DeviceLevelMaster,
		Session:     otherDevice,
	}))

	var scheduled []time.Duration
	app := New(Options{
		Users:   &fakeUserStore{getErr: metadb.ErrNotFound},
		Devices: &fakeDeviceStore{},
		Online:  reg,
		AfterFunc: func(d time.Duration, fn func()) {
			scheduled = append(scheduled, d)
			fn()
		},
	})

	err := app.UpdateToken(context.Background(), UpdateTokenCommand{
		UID:         "u1",
		Token:       "token-1",
		DeviceFlag:  wkframe.APP,
		DeviceLevel: wkframe.DeviceLevelMaster,
	})

	require.NoError(t, err)
	require.Len(t, sameDevice.WrittenFrames(), 1)
	require.True(t, sameDevice.closed)
	require.Empty(t, otherDevice.WrittenFrames())
	require.Equal(t, []time.Duration{10 * time.Second}, scheduled)
}
```

- [ ] **Step 2: Run the new use case tests to verify they fail**

Run: `go test ./internal/usecase/user -run "TestUpdateToken" -count=1`

Expected: FAIL because the package and implementation do not exist yet.

- [ ] **Step 3: Write the minimal use case implementation**

Create `internal/usecase/user/app.go` and `deps.go` with narrow dependencies:

```go
var (
	ErrUserStoreRequired   = errors.New("usecase/user: user store required")
	ErrDeviceStoreRequired = errors.New("usecase/user: device store required")
)

type UserStore interface {
	GetUser(ctx context.Context, uid string) (metadb.User, error)
	UpsertUser(ctx context.Context, u metadb.User) error
}

type DeviceStore interface {
	UpsertDevice(ctx context.Context, d metadb.Device) error
}

type Options struct {
	Users     UserStore
	Devices   DeviceStore
	Online    online.Registry
	AfterFunc func(time.Duration, func())
}

func New(opts Options) *App {
	if opts.AfterFunc == nil {
		opts.AfterFunc = func(d time.Duration, fn func()) { time.AfterFunc(d, fn) }
	}
	if opts.Online == nil {
		opts.Online = online.NewRegistry()
	}
	return &App{
		users:     opts.Users,
		devices:   opts.Devices,
		online:    opts.Online,
		afterFunc: opts.AfterFunc,
	}
}
```

Create `command.go` with exact legacy validation:

```go
func (c UpdateTokenCommand) Validate() error {
	switch {
	case c.UID == "":
		return errors.New("uid不能为空！")
	case c.Token == "":
		return errors.New("token不能为空！")
	case strings.Contains(c.UID, "@"), strings.Contains(c.UID, "#"), strings.Contains(c.UID, "&"):
		return errors.New("uid不能包含特殊字符！")
	default:
		return nil
	}
}
```

Create `token.go` so `UpdateToken` does exactly this:

```go
func (a *App) UpdateToken(ctx context.Context, cmd UpdateTokenCommand) error {
	if err := cmd.Validate(); err != nil {
		return err
	}
	if a.users == nil {
		return ErrUserStoreRequired
	}
	if a.devices == nil {
		return ErrDeviceStoreRequired
	}

	_, err := a.users.GetUser(ctx, cmd.UID)
	if errors.Is(err, metadb.ErrNotFound) {
		if err := a.users.UpsertUser(ctx, metadb.User{UID: cmd.UID}); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	if err := a.devices.UpsertDevice(ctx, metadb.Device{
		UID:         cmd.UID,
		DeviceFlag:  int64(cmd.DeviceFlag),
		Token:       cmd.Token,
		DeviceLevel: int64(cmd.DeviceLevel),
	}); err != nil {
		return err
	}

	if cmd.DeviceLevel != wkframe.DeviceLevelMaster || a.online == nil {
		return nil
	}

	for _, conn := range a.online.ConnectionsByUID(cmd.UID) {
		if conn.DeviceFlag != cmd.DeviceFlag || conn.Session == nil {
			continue
		}
		sess := conn.Session
		_ = sess.WriteFrame(&wkframe.DisconnectPacket{
			ReasonCode: wkframe.ReasonConnectKick,
			Reason:     "账号在其他设备上登录",
		})
		a.afterFunc(10*time.Second, func() { _ = sess.Close() })
	}
	return nil
}
```

- [ ] **Step 4: Run the use case tests to verify they pass**

Run: `go test ./internal/usecase/user -count=1`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/usecase/user/app.go internal/usecase/user/command.go internal/usecase/user/deps.go internal/usecase/user/token.go internal/usecase/user/token_test.go
git commit -m "feat(usecase): add user token update flow"
```

## Task 2: Add the old-compatible `/user/token` HTTP adapter

**Files:**
- Modify: `internal/access/api/server.go`
- Modify: `internal/access/api/routes.go`
- Create: `internal/access/api/user_token.go`
- Modify: `internal/access/api/server_test.go`

- [ ] **Step 1: Write the failing API handler tests**

Extend `internal/access/api/server_test.go` with:

- `TestUpdateTokenMapsJSONToUsecaseCommand`
- `TestUpdateTokenRejectsInvalidJSONWithLegacyEnvelope`
- `TestUpdateTokenReturnsLegacyValidationErrorEnvelope`
- `TestUpdateTokenReturnsLegacyBusinessErrorEnvelope`

Test sketch:

```go
func TestUpdateTokenMapsJSONToUsecaseCommand(t *testing.T) {
	users := &recordingUserUsecase{}
	srv := New(Options{Users: users})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/user/token", bytes.NewBufferString(`{"uid":"u1","token":"t1","device_flag":0,"device_level":1}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{"status":200}`, rec.Body.String())
	require.Len(t, users.calls, 1)
	require.Equal(t, user.UpdateTokenCommand{
		UID:         "u1",
		Token:       "t1",
		DeviceFlag:  wkframe.APP,
		DeviceLevel: wkframe.DeviceLevelMaster,
	}, users.calls[0])
}
```

- [ ] **Step 2: Run the API tests to verify they fail**

Run: `go test ./internal/access/api -run "TestUpdateToken" -count=1`

Expected: FAIL because the route, handler, and `Users` server dependency do not exist yet.

- [ ] **Step 3: Write the minimal HTTP adapter**

In `internal/access/api/server.go`, extend the server contract:

```go
type UserUsecase interface {
	UpdateToken(ctx context.Context, cmd user.UpdateTokenCommand) error
}

type Options struct {
	ListenAddr string
	Messages   MessageUsecase
	Users      UserUsecase
}

type Server struct {
	// existing fields...
	users UserUsecase
}
```

In `internal/access/api/routes.go`, register:

```go
s.engine.POST("/user/token", s.handleUpdateToken)
```

Create `internal/access/api/user_token.go` with the old envelope:

```go
type updateTokenRequest struct {
	UID         string `json:"uid"`
	Token       string `json:"token"`
	DeviceFlag  uint8  `json:"device_flag"`
	DeviceLevel uint8  `json:"device_level"`
}

func (s *Server) handleUpdateToken(c *gin.Context) {
	var req updateTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"msg": "invalid request", "status": http.StatusBadRequest})
		return
	}
	if s == nil || s.users == nil {
		c.JSON(http.StatusBadRequest, gin.H{"msg": "user usecase not configured", "status": http.StatusBadRequest})
		return
	}
	err := s.users.UpdateToken(c.Request.Context(), user.UpdateTokenCommand{
		UID:         req.UID,
		Token:       req.Token,
		DeviceFlag:  wkframe.DeviceFlag(req.DeviceFlag),
		DeviceLevel: wkframe.DeviceLevel(req.DeviceLevel),
	})
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"msg": err.Error(), "status": http.StatusBadRequest})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": http.StatusOK})
}
```

- [ ] **Step 4: Run the API tests to verify they pass**

Run: `go test ./internal/access/api -run "TestUpdateToken" -count=1`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/access/api/server.go internal/access/api/routes.go internal/access/api/user_token.go internal/access/api/server_test.go
git commit -m "feat(access): add user token endpoint"
```

## Task 3: Add per-device metadata storage in `pkg/storage/metadb`

**Files:**
- Modify: `pkg/storage/metadb/catalog.go`
- Modify: `pkg/storage/metadb/codec.go`
- Modify: `pkg/storage/metadb/batch.go`
- Create: `pkg/storage/metadb/device.go`
- Create: `pkg/storage/metadb/device_test.go`

- [ ] **Step 1: Write the failing device-storage tests**

Create `pkg/storage/metadb/device_test.go` with:

- `TestDeviceUpsertAndGetAreSlotScoped`
- `TestDeviceCoexistsForSameUIDAcrossDifferentDeviceFlags`
- `TestDeviceUpsertUpdatesOnlyTargetedDeviceFlag`
- `TestGetDeviceHonorsCanceledContext`

Test sketch:

```go
func TestDeviceCoexistsForSameUIDAcrossDifferentDeviceFlags(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	shard := db.ForSlot(1)

	require.NoError(t, shard.UpsertDevice(ctx, Device{UID: "u1", DeviceFlag: 1, Token: "app", DeviceLevel: 1}))
	require.NoError(t, shard.UpsertDevice(ctx, Device{UID: "u1", DeviceFlag: 2, Token: "web", DeviceLevel: 0}))

	appDevice, err := shard.GetDevice(ctx, "u1", 1)
	require.NoError(t, err)
	webDevice, err := shard.GetDevice(ctx, "u1", 2)
	require.NoError(t, err)
	require.Equal(t, "app", appDevice.Token)
	require.Equal(t, "web", webDevice.Token)
}
```

- [ ] **Step 2: Run the metadb tests to verify they fail**

Run: `go test ./pkg/storage/metadb -run "TestDevice" -count=1`

Expected: FAIL because the `Device` model and APIs do not exist yet.

- [ ] **Step 3: Write the minimal metadb implementation**

In `pkg/storage/metadb/catalog.go`, append new ids without renumbering current tables:

```go
const (
	TableIDUser               uint32 = 1
	TableIDChannel            uint32 = 2
	TableIDChannelRuntimeMeta uint32 = 3
	TableIDDevice             uint32 = 4
)
```

Create `pkg/storage/metadb/device.go`:

```go
type Device struct {
	UID         string
	DeviceFlag  int64
	Token       string
	DeviceLevel int64
}

func (s *ShardStore) GetDevice(ctx context.Context, uid string, deviceFlag int64) (Device, error) { /* composite key lookup */ }
func (s *ShardStore) UpsertDevice(ctx context.Context, d Device) error { /* batch.Set(primaryKey, value, nil) */ }
```

In `pkg/storage/metadb/codec.go`, use a composite primary key:

```go
func encodeDevicePrimaryKey(slot uint64, uid string, deviceFlag int64, familyID uint16) []byte {
	key := encodeStatePrefix(slot, DeviceTable.ID)
	key = appendKeyString(key, uid)
	key = appendKeyInt64Ordered(key, deviceFlag)
	key = binary.AppendUvarint(key, uint64(familyID))
	return key
}
```

In `pkg/storage/metadb/batch.go`, add:

```go
func (b *WriteBatch) UpsertDevice(slot uint64, d Device) error {
	key := encodeDevicePrimaryKey(slot, d.UID, d.DeviceFlag, devicePrimaryFamilyID)
	value := encodeDeviceFamilyValue(d.Token, d.DeviceLevel, key)
	return b.batch.Set(key, value, nil)
}
```

- [ ] **Step 4: Run the metadb tests to verify they pass**

Run: `go test ./pkg/storage/metadb -run "TestDevice" -count=1`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add pkg/storage/metadb/catalog.go pkg/storage/metadb/codec.go pkg/storage/metadb/batch.go pkg/storage/metadb/device.go pkg/storage/metadb/device_test.go
git commit -m "feat(metadb): add per-device token storage"
```

## Task 4: Add the replicated `Device` path through `metafsm` and `metastore`

**Files:**
- Modify: `pkg/storage/metafsm/command.go`
- Modify: `pkg/storage/metafsm/state_machine_test.go`
- Modify: `pkg/storage/metastore/store.go`
- Modify: `pkg/storage/metastore/integration_test.go`

- [ ] **Step 1: Write the failing FSM and metastore tests**

Extend `pkg/storage/metafsm/state_machine_test.go` with:

- a command round-trip assertion for `EncodeUpsertDeviceCommand`
- a state-machine apply assertion that `GetDevice` returns persisted device data

Extend `pkg/storage/metastore/integration_test.go` with:

- `TestStoreUpsertAndGetDevice`

Test sketch:

```go
func TestStoreUpsertAndGetDevice(t *testing.T) {
	ctx := context.Background()
	// mirror the existing single-node cluster setup in this file
	store := New(cluster, bizDB)

	require.NoError(t, store.UpsertDevice(ctx, metadb.Device{
		UID:         "u1",
		DeviceFlag:  int64(wkframe.APP),
		Token:       "token-1",
		DeviceLevel: int64(wkframe.DeviceLevelMaster),
	}))

	got, err := store.GetDevice(ctx, "u1", int64(wkframe.APP))
	require.NoError(t, err)
	require.Equal(t, "token-1", got.Token)
}
```

- [ ] **Step 2: Run the replicated-storage tests to verify they fail**

Run: `go test ./pkg/storage/metafsm ./pkg/storage/metastore -run "TestStoreUpsertAndGetDevice|TestStateMachine|TestDecodeCommand" -count=1`

Expected: FAIL because there is no device command type and no metastore device facade.

- [ ] **Step 3: Write the minimal replicated device implementation**

In `pkg/storage/metafsm/command.go`, append a new command id instead of reusing existing ones:

```go
const (
	cmdTypeUpsertUser               uint8 = 1
	cmdTypeUpsertChannel            uint8 = 2
	cmdTypeDeleteChannel            uint8 = 3
	cmdTypeUpsertChannelRuntimeMeta uint8 = 4
	cmdTypeDeleteChannelRuntimeMeta uint8 = 5
	cmdTypeUpsertDevice             uint8 = 6
)
```

Add:

```go
type upsertDeviceCmd struct{ device metadb.Device }

func (c *upsertDeviceCmd) apply(wb *metadb.WriteBatch, slot uint64) error {
	return wb.UpsertDevice(slot, c.device)
}

func EncodeUpsertDeviceCommand(d metadb.Device) []byte { /* TLV encode uid/device_flag/token/device_level */ }
func decodeUpsertDevice(data []byte) (command, error) { /* TLV decode */ }
```

In `pkg/storage/metastore/store.go`, add:

```go
func (s *Store) UpsertDevice(ctx context.Context, d metadb.Device) error {
	groupID := s.cluster.SlotForKey(d.UID)
	return s.cluster.Propose(ctx, groupID, metafsm.EncodeUpsertDeviceCommand(d))
}

func (s *Store) GetDevice(ctx context.Context, uid string, deviceFlag int64) (metadb.Device, error) {
	groupID := s.cluster.SlotForKey(uid)
	return s.db.ForSlot(uint64(groupID)).GetDevice(ctx, uid, deviceFlag)
}
```

- [ ] **Step 4: Run the replicated-storage tests to verify they pass**

Run: `go test ./pkg/storage/metafsm ./pkg/storage/metastore -run "TestStoreUpsertAndGetDevice|TestStateMachine|TestDecodeCommand" -count=1`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add pkg/storage/metafsm/command.go pkg/storage/metafsm/state_machine_test.go pkg/storage/metastore/store.go pkg/storage/metastore/integration_test.go
git commit -m "feat(metastore): add replicated device metadata"
```

## Task 5: Wire the endpoint through `internal/app` and verify it end to end

**Files:**
- Modify: `internal/app/build.go`
- Modify: `internal/app/integration_test.go`

- [ ] **Step 1: Write the failing app integration test**

Add `TestAppStartServesUserTokenAPIAndPersistsDeviceMetadata` to `internal/app/integration_test.go`.

Test sketch:

```go
func TestAppStartServesUserTokenAPIAndPersistsDeviceMetadata(t *testing.T) {
	cfg := testConfig(t)
	cfg.Cluster.ListenAddr = "127.0.0.1:0"
	cfg.API.ListenAddr = "127.0.0.1:0"

	app, err := New(cfg)
	require.NoError(t, err)
	require.NoError(t, app.Start())
	t.Cleanup(func() { require.NoError(t, app.Stop()) })

	resp, err := http.Post("http://"+app.API().Addr()+"/user/token", "application/json",
		bytes.NewBufferString(`{"uid":"api-user","token":"token-1","device_flag":0,"device_level":1}`))
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)

	device, err := app.Store().GetDevice(context.Background(), "api-user", int64(wkframe.APP))
	require.NoError(t, err)
	require.Equal(t, "token-1", device.Token)
}
```

- [ ] **Step 2: Run the app integration test to verify it fails**

Run: `go test ./internal/app -run "TestAppStartServesUserTokenAPIAndPersistsDeviceMetadata" -count=1`

Expected: FAIL because `internal/app/build.go` does not yet construct and inject the user use case into `accessapi.New(...)`.

- [ ] **Step 3: Write the minimal composition-root wiring**

In `internal/app/build.go`, add the user use case and inject it into the API server:

```go
userApp := user.New(user.Options{
	Users:     app.store,
	Devices:   app.store,
	Online:    onlineRegistry,
	AfterFunc: func(d time.Duration, fn func()) { time.AfterFunc(d, fn) },
})

if cfg.API.ListenAddr != "" {
	app.api = accessapi.New(accessapi.Options{
		ListenAddr: cfg.API.ListenAddr,
		Messages:   app.messageApp,
		Users:      userApp,
	})
}
```

Do not add a new aggregate service object. Keep the use case local to the composition root.

- [ ] **Step 4: Run the targeted app integration test to verify it passes**

Run: `go test ./internal/app -run "TestAppStartServesUserTokenAPIAndPersistsDeviceMetadata" -count=1`

Expected: PASS

- [ ] **Step 5: Run the broader related verification suite**

Run:

```bash
go test ./internal/access/api ./internal/usecase/user ./pkg/storage/metadb ./pkg/storage/metafsm ./pkg/storage/metastore ./internal/app -count=1
```

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/app/build.go internal/app/integration_test.go
git commit -m "feat(app): wire user token api"
```
