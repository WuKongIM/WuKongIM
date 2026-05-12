# Send Permission Convergence P2 Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restore the remaining send permission compatibility rules for person allowlists, system-device channel-permission bypass, gateway device identity propagation, and legacy public/special channel sends.

**Architecture:** Keep authorization in `internal/usecase/message`, use `internal/runtime/channelid` only for channel-ID derivation helpers, and keep `internal/access/*` as DTO/session adapters. Durable sends still go through the channel log/cluster path; permission denials return `SendResult.Reason` before durable append.

**Tech Stack:** Go, `testing`, `stretchr/testify/require`, existing WuKongIM usecase/runtime packages.

---

## File Structure

- Create `internal/runtime/channelid/agent.go`: agent channel encode/decode helpers and error value.
- Create `internal/runtime/channelid/agent_test.go`: unit tests for helper behavior.
- Modify `internal/usecase/message/command.go`: add trusted session-derived `DeviceID` and pass-through `DeviceFlag` fields with English comments.
- Modify `internal/usecase/message/app.go`: add `PersonWhitelistEnabled` and `SystemDeviceID` options and private app fields.
- Modify `internal/usecase/message/send.go`: expand supported send types, normalize agent channel IDs, preserve command-channel append behavior.
- Modify `internal/usecase/message/permission.go`: enforce optional person allowlist, receiver system UID bypass, system-device channel-permission bypass after `SendBan`, and special-channel permission rules.
- Modify `internal/usecase/message/send_test.go`: add focused RED/GREEN tests for every business rule.
- Modify `internal/access/gateway/mapper.go`: map session `DeviceID` and `DeviceFlag` into `SendCommand`.
- Modify `internal/access/gateway/handler_test.go`: test gateway session identity propagation into send usecase.
- Modify `internal/access/gateway/error_map.go` and `internal/access/api/error_map.go`: map invalid agent channel IDs like invalid person channel IDs.
- Modify `internal/app/config.go`: add `MessageConfig` and defaults.
- Modify `cmd/wukongim/config.go`: parse `WK_MESSAGE_PERSON_WHITELIST_ENABLED` and `WK_MESSAGE_SYSTEM_DEVICE_ID`.
- Modify `cmd/wukongim/config_test.go`: test config parsing and environment override.
- Modify `internal/app/build.go` and `internal/app/lifecycle_test.go`: wire message config into `message.New` and verify app wiring.
- Modify `wukongim.conf.example`: document the two new config keys.

---

### Task 1: Agent Channel ID Helpers

**Files:**
- Create: `internal/runtime/channelid/agent.go`
- Create: `internal/runtime/channelid/agent_test.go`

- [ ] **Step 1: Write failing agent helper tests**

Add tests:

```go
func TestEncodeAgentChannel(t *testing.T) {
    require.Equal(t, "u1@agent-a", EncodeAgentChannel("u1", "agent-a"))
}

func TestDecodeAgentChannel(t *testing.T) {
    left, right, err := DecodeAgentChannel("u1@agent-a")
    require.NoError(t, err)
    require.Equal(t, "u1", left)
    require.Equal(t, "agent-a", right)
}

func TestDecodeAgentChannelRejectsInvalidShape(t *testing.T) {
    _, _, err := DecodeAgentChannel("u1")
    require.ErrorIs(t, err, ErrInvalidAgentChannel)
    _, _, err = DecodeAgentChannel("u1@")
    require.ErrorIs(t, err, ErrInvalidAgentChannel)
}
```

- [ ] **Step 2: Run RED test**

Run:

```bash
GOWORK=off go test ./internal/runtime/channelid -run 'Test(Encode|Decode)AgentChannel' -count=1
```

Expected: FAIL because helpers are undefined.

- [ ] **Step 3: Implement minimal helper**

Add `ErrInvalidAgentChannel`, `EncodeAgentChannel(uid, agentUID string) string`, and `DecodeAgentChannel(channelID string) (string, string, error)` using a strict two-part `@` shape with non-empty sides.

- [ ] **Step 4: Run GREEN test**

Run:

```bash
GOWORK=off go test ./internal/runtime/channelid -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/runtime/channelid/agent.go internal/runtime/channelid/agent_test.go
git commit -m "feat: add agent channel id helpers"
```

---

### Task 2: Message Configuration Parsing and Wiring

**Files:**
- Modify: `internal/app/config.go`
- Modify: `cmd/wukongim/config.go`
- Modify: `cmd/wukongim/config_test.go`
- Modify: `internal/app/build.go`
- Modify: `internal/app/lifecycle_test.go`
- Modify: `wukongim.conf.example`
- Later consumed by: `internal/usecase/message/app.go`

- [ ] **Step 1: Write failing config tests**

In `cmd/wukongim/config_test.go`, add:

```go
func TestLoadConfigParsesMessagePermissionConfig(t *testing.T) {
    dir := t.TempDir()
    configPath := writeConf(t, dir, "wukongim.conf",
        "WK_NODE_ID=1",
        "WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
        "WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
        "WK_CLUSTER_SLOT_COUNT=1",
        `WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
        "WK_MESSAGE_PERSON_WHITELIST_ENABLED=true",
        "WK_MESSAGE_SYSTEM_DEVICE_ID=custom-device",
    )

    cfg, err := loadConfig(configPath)
    require.NoError(t, err)
    require.True(t, cfg.Message.PersonWhitelistEnabled)
    require.Equal(t, "custom-device", cfg.Message.SystemDeviceID)
}

func TestLoadConfigPrefersEnvironmentVariablesForMessagePermissionConfig(t *testing.T) {
    dir := t.TempDir()
    configPath := writeConf(t, dir, "wukongim.conf",
        "WK_NODE_ID=1",
        "WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
        "WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
        "WK_CLUSTER_SLOT_COUNT=1",
        `WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
        "WK_MESSAGE_PERSON_WHITELIST_ENABLED=false",
        "WK_MESSAGE_SYSTEM_DEVICE_ID=file-device",
    )
    t.Setenv("WK_MESSAGE_PERSON_WHITELIST_ENABLED", "true")
    t.Setenv("WK_MESSAGE_SYSTEM_DEVICE_ID", "env-device")

    cfg, err := loadConfig(configPath)
    require.NoError(t, err)
    require.True(t, cfg.Message.PersonWhitelistEnabled)
    require.Equal(t, "env-device", cfg.Message.SystemDeviceID)
}
```

- [ ] **Step 2: Write failing app wiring test**

In `internal/app/lifecycle_test.go`, add a package-local test after the build tests:

```go
func TestNewWiresMessagePermissionConfig(t *testing.T) {
    cfg := testConfig(t)
    cfg.Message.PersonWhitelistEnabled = true
    cfg.Message.SystemDeviceID = "custom-device"

    app, err := New(cfg)
    require.NoError(t, err)
    t.Cleanup(func() {
        require.NoError(t, app.RaftDB().Close())
        require.NoError(t, app.DB().Close())
    })

    msg := reflect.ValueOf(app.messageApp).Elem()
    require.True(t, msg.FieldByName("personWhitelistEnabled").Bool())
    require.Equal(t, "custom-device", msg.FieldByName("systemDeviceID").String())
}
```

- [ ] **Step 3: Run RED tests**

Run:

```bash
GOWORK=off go test ./cmd/wukongim -run 'TestLoadConfig(Parses|PrefersEnvironmentVariablesFor)MessagePermissionConfig' -count=1
GOWORK=off go test ./internal/app -run TestNewWiresMessagePermissionConfig -count=1
```

Expected: FAIL because `Config.Message` and message app fields do not exist.

- [ ] **Step 4: Implement config structs and parsing**

In `internal/usecase/message/app.go`, first add the options and private app fields so Task 2 can compile before any permission rule consumes them:

```go
PersonWhitelistEnabled bool
SystemDeviceID string
```

and:

```go
personWhitelistEnabled bool
systemDeviceID string
```

Assign both fields in `New`. Do not change permission behavior in this task.

In `internal/app/config.go`:

```go
// Message configures message send business rules.
Message MessageConfig

type MessageConfig struct {
    // PersonWhitelistEnabled enables receiver-side personal allowlist enforcement for sends.
    // It is disabled by default to match legacy WhitelistOffOfPerson=true compatibility.
    PersonWhitelistEnabled bool
    // SystemDeviceID identifies trusted gateway sessions that bypass channel-type-specific
    // send permissions after sender SendBan has passed.
    SystemDeviceID string
}
```

In `ApplyDefaultsAndValidate`, default empty `SystemDeviceID` to `defaultMessageSystemDeviceID`. Add:

```go
const defaultMessageSystemDeviceID = "____device"
```

In `cmd/wukongim/config.go`, parse:

```go
messagePersonWhitelistEnabled, err := parseBool(v, "WK_MESSAGE_PERSON_WHITELIST_ENABLED")
messageSystemDeviceID := stringValue(v, "WK_MESSAGE_SYSTEM_DEVICE_ID")
```

Then set `cfg.Message = app.MessageConfig{...}`.

- [ ] **Step 5: Wire to message usecase**

In `internal/app/build.go`, pass:

```go
PersonWhitelistEnabled: cfg.Message.PersonWhitelistEnabled,
SystemDeviceID:         cfg.Message.SystemDeviceID,
```

into `message.Options`.

- [ ] **Step 6: Update example config**

Add to `wukongim.conf.example` near message/conversation settings:

```conf
# Enables receiver-side personal allowlist checks for person-channel sends.
# Disabled by default to preserve legacy WhitelistOffOfPerson=true compatibility.
WK_MESSAGE_PERSON_WHITELIST_ENABLED=false
# Trusted gateway sessions with this device ID bypass channel-type-specific send permissions
# after sender SendBan has passed. Keep the legacy default unless all system senders use another ID.
WK_MESSAGE_SYSTEM_DEVICE_ID=____device
```

- [ ] **Step 7: Run GREEN tests**

Run:

```bash
GOWORK=off go test ./cmd/wukongim -run 'TestLoadConfig(Parses|PrefersEnvironmentVariablesFor)MessagePermissionConfig' -count=1
GOWORK=off go test ./internal/app -run TestNewWiresMessagePermissionConfig -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/usecase/message/app.go internal/app/config.go cmd/wukongim/config.go cmd/wukongim/config_test.go internal/app/build.go internal/app/lifecycle_test.go wukongim.conf.example
git commit -m "feat: add message permission config"
```

---

### Task 3: Gateway Device Identity Mapping

**Files:**
- Modify: `internal/usecase/message/command.go`
- Modify: `internal/access/gateway/mapper.go`
- Modify: `internal/access/gateway/handler_test.go`

- [ ] **Step 1: Write failing gateway send mapping test**

In `internal/access/gateway/handler_test.go`, add:

```go
func TestHandlerOnFrameSendMapsDeviceIdentityToUsecase(t *testing.T) {
    sender := newOptionRecordingSession(1, "tcp")
    sender.SetValue(coregateway.SessionValueUID, "u1")
    sender.SetValue(coregateway.SessionValueDeviceID, "system-device")
    sender.SetValue(coregateway.SessionValueDeviceFlag, frame.SYSTEM)
    msgs := &fakeMessageUsecase{sendResult: message.SendResult{Reason: frame.ReasonSuccess}}
    handler := New(Options{Messages: msgs})

    ctx := &coregateway.Context{Session: sender, Listener: "tcp", RequestContext: context.Background()}
    require.NoError(t, handler.OnFrame(ctx, &frame.SendPacket{ChannelID: "u2", ChannelType: frame.ChannelTypePerson}))

    require.Len(t, msgs.sendCommands, 1)
    require.Equal(t, "system-device", msgs.sendCommands[0].DeviceID)
    require.Equal(t, frame.SYSTEM, msgs.sendCommands[0].DeviceFlag)
}
```

- [ ] **Step 2: Run RED test**

Run:

```bash
GOWORK=off go test ./internal/access/gateway -run TestHandlerOnFrameSendMapsDeviceIdentityToUsecase -count=1
```

Expected: FAIL because `SendCommand` lacks `DeviceID` / `DeviceFlag` or mapper does not set them.

- [ ] **Step 3: Add command fields and mapper logic**

In `internal/usecase/message/command.go`, add English comments:

```go
// DeviceID is the trusted gateway session device ID used for legacy system-device permission bypass.
DeviceID string
// DeviceFlag is the trusted gateway session device flag. It is pass-through for future device-aware rules.
DeviceFlag frame.DeviceFlag
```

In `internal/access/gateway/mapper.go`, read session values with helpers like existing lifecycle code:

```go
deviceID, _ := ctx.Session.Value(coregateway.SessionValueDeviceID).(string)
deviceFlag := deviceFlagFromValue(ctx.Session.Value(coregateway.SessionValueDeviceFlag))
```

Set both fields in nil-packet and normal-packet `SendCommand` returns.

- [ ] **Step 4: Run GREEN test**

Run:

```bash
GOWORK=off go test ./internal/access/gateway -run TestHandlerOnFrameSendMapsDeviceIdentityToUsecase -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/usecase/message/command.go internal/access/gateway/mapper.go internal/access/gateway/handler_test.go
git commit -m "feat: pass gateway device identity to send usecase"
```

---

### Task 4: Person Permission Convergence

**Files:**
- Modify: `internal/usecase/message/app.go`
- Modify: `internal/usecase/message/permission.go`
- Modify: `internal/usecase/message/send_test.go`

- [ ] **Step 1: Write failing person allowlist/default tests**

Add tests to `internal/usecase/message/send_test.go`:

```go
func TestSendAllowsPersonSenderWhenPersonWhitelistDisabledByDefault(t *testing.T) { ... }
func TestSendRejectsPersonSenderNotInReceiverAllowlistWhenPersonWhitelistEnabled(t *testing.T) { ... }
func TestSendAllowsPersonSenderInReceiverAllowlistWhenPersonWhitelistEnabled(t *testing.T) { ... }
func TestSendAllowsPersonWhenReceiverIsSystemUID(t *testing.T) { ... }
```

Use receiver-dimensional list IDs:

```go
allowID := channelmembers.AllowlistChannelID(channelmembers.ChannelKey{ChannelID: "u2", ChannelType: frame.ChannelTypePerson})
```

Expected reasons:

- default disabled -> `ReasonSuccess` and durable append;
- enabled and missing sender -> `ReasonNotInWhitelist`, no durable append;
- enabled and sender present -> `ReasonSuccess`;
- receiver system UID -> `ReasonSuccess` even if deny/allow lists would otherwise reject.

- [ ] **Step 2: Run RED tests**

Run:

```bash
GOWORK=off go test ./internal/usecase/message -run 'TestSend(AllowsPersonSenderWhenPersonWhitelistDisabledByDefault|RejectsPersonSenderNotInReceiverAllowlistWhenPersonWhitelistEnabled|AllowsPersonSenderInReceiverAllowlistWhenPersonWhitelistEnabled|AllowsPersonWhenReceiverIsSystemUID)' -count=1
```

Expected: FAIL because person allowlist and receiver system UID permission rules are missing.

- [ ] **Step 3: Implement person permission rules**

Use the `personWhitelistEnabled` field added in Task 2. Do not add another default here; `internal/app.Config.ApplyDefaultsAndValidate` owns the app-level default, and permission checks should require a non-empty configured `systemDeviceID` only where system-device rules are consumed in Task 5.

In `checkPersonSendPermission`:

1. decode canonical channel;
2. derive receiver;
3. if `systemUIDs.IsSystemUID(receiver)`, return success;
4. check receiver denylist;
5. if `!personWhitelistEnabled`, return success;
6. check receiver allowlist and return `ReasonNotInWhitelist` when absent.

- [ ] **Step 4: Run GREEN tests**

Run:

```bash
GOWORK=off go test ./internal/usecase/message -run 'TestSend(AllowsPersonSenderWhenPersonWhitelistDisabledByDefault|RejectsPersonSenderNotInReceiverAllowlistWhenPersonWhitelistEnabled|AllowsPersonSenderInReceiverAllowlistWhenPersonWhitelistEnabled|AllowsPersonWhenReceiverIsSystemUID)' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/usecase/message/app.go internal/usecase/message/permission.go internal/usecase/message/send_test.go
git commit -m "feat: enforce optional person send allowlist"
```

---

### Task 5: System Device Permission Bypass

**Files:**
- Modify: `internal/usecase/message/permission.go`
- Modify: `internal/usecase/message/send_test.go`

- [ ] **Step 1: Write failing system-device tests**

Add tests:

```go
func TestSendSystemDeviceBypassesChannelPermissionChecks(t *testing.T) { ... }
func TestSendSystemDeviceDoesNotBypassSenderSendBan(t *testing.T) { ... }
```

The first test should set `SystemDeviceID: "____device"`, `DeviceID: "____device"`, a group `Disband`/denylist/non-subscriber state, and expect durable append success.

The second test should also set sender personal `SendBan=1` and expect `ReasonSendBan` with no durable append.

- [ ] **Step 2: Run RED tests**

Run:

```bash
GOWORK=off go test ./internal/usecase/message -run 'TestSendSystemDevice(BypassesChannelPermissionChecks|DoesNotBypassSenderSendBan)' -count=1
```

Expected: FAIL because system-device bypass is not implemented.

- [ ] **Step 3: Implement bypass after sender SendBan**

In `checkSendPermission`:

```go
if reason, err := a.checkSenderSendPermission(ctx, cmd.FromUID); reason != frame.ReasonSuccess || err != nil {
    return reason, err
}
if a.systemDeviceID != "" && cmd.DeviceID == a.systemDeviceID {
    return frame.ReasonSuccess, nil
}
```

Keep this after system UID bypass and after sender `SendBan`.

- [ ] **Step 4: Run GREEN tests**

Run:

```bash
GOWORK=off go test ./internal/usecase/message -run 'TestSendSystemDevice(BypassesChannelPermissionChecks|DoesNotBypassSenderSendBan)' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/usecase/message/permission.go internal/usecase/message/send_test.go
git commit -m "feat: add system device send permission bypass"
```

---

### Task 6: Public and Special Channel Send Permission

**Files:**
- Modify: `internal/usecase/message/send.go`
- Modify: `internal/usecase/message/permission.go`
- Modify: `internal/usecase/message/send_test.go`
- Modify: `internal/access/gateway/error_map.go`
- Modify: `internal/access/api/error_map.go`

- [ ] **Step 1: Write failing supported-type tests**

Add message usecase tests:

```go
func TestSendAllowsInfoChannel(t *testing.T) { ... }
func TestSendAllowsCustomerServiceChannel(t *testing.T) { ... }
func TestSendAllowsAgentChannelParticipantAndNormalizesBareAgentID(t *testing.T) { ... }
func TestSendRejectsAgentChannelNonParticipant(t *testing.T) { ... }
func TestSendAllowsVisitorsSelfSender(t *testing.T) { ... }
func TestSendVisitorsNonSelfUsesCustomerServicePermissionLists(t *testing.T) { ... }
func TestSendAlreadyCommandChannelAppendsToCommandChannelWithoutSyncOnce(t *testing.T) { ... }
```

Key expectations:

- `Info` and `CustomerService` append successfully despite permission store containing no channel metadata/subscribers.
- bare agent channel input `agent-a` from `u1` appends to `u1@agent-a`.
- agent non-participant `u3` sending to `u1@agent-a` returns `ReasonNotAllowSend` and does not append.
- visitor self sender `FromUID == ChannelID` appends without subscriber checks.
- visitor non-self sender checks `DenylistChannelID(ChannelKey{ChannelID: visitorID, ChannelType: CustomerService})`, base subscribers `(visitorID, CustomerService)`, and allowlist with the same key/type.
- already command input `g1____cmd` with `SyncOnce=false` appends to `g1____cmd`, while permission checks use original `g1`.

- [ ] **Step 2: Write failing invalid-agent mapping tests**

Add small tests if absent:

- `internal/access/gateway/error_map.go`: `ErrInvalidAgentChannel` maps to `ReasonChannelIDError`.
- `internal/access/api/error_map.go`: `ErrInvalidAgentChannel` maps to HTTP 400 `invalid channel id`.

- [ ] **Step 3: Run RED tests**

Run:

```bash
GOWORK=off go test ./internal/usecase/message -run 'TestSend(AllowsInfoChannel|AllowsCustomerServiceChannel|AllowsAgentChannelParticipantAndNormalizesBareAgentID|RejectsAgentChannelNonParticipant|AllowsVisitorsSelfSender|VisitorsNonSelfUsesCustomerServicePermissionLists|AlreadyCommandChannelAppendsToCommandChannelWithoutSyncOnce)' -count=1
GOWORK=off go test ./internal/access/gateway -run TestMapSendErrorReasonInvalidAgentChannel -count=1
GOWORK=off go test ./internal/access/api -run TestMapSendErrorInvalidAgentChannel -count=1
```

Expected: FAIL because special send types are still unsupported or mapping does not know agent errors.

- [ ] **Step 4: Expand supported channel types**

In `send.go`, replace the two-type guard with helper:

```go
func isSupportedSendChannelType(channelType uint8) bool {
    switch channelType {
    case frame.ChannelTypePerson, frame.ChannelTypeGroup, frame.ChannelTypeCustomerService, frame.ChannelTypeInfo, frame.ChannelTypeVisitors, frame.ChannelTypeAgent:
        return true
    default:
        return false
    }
}
```

After command suffix removal, normalize:

```go
case frame.ChannelTypePerson:
    cmd.ChannelID, err = runtimechannelid.NormalizePersonChannel(cmd.FromUID, cmd.ChannelID)
case frame.ChannelTypeAgent:
    cmd.ChannelID, err = normalizeAgentSendChannel(cmd.FromUID, cmd.ChannelID)
```

`normalizeAgentSendChannel` should use `runtimechannelid.EncodeAgentChannel` for bare IDs and `DecodeAgentChannel` for precomposed IDs.

- [ ] **Step 5: Implement special permission rules**

In `permission.go`:

- keep `Info` and `CustomerService` as success after sender `SendBan`;
- `Agent` decodes channel and permits only participants;
- split common member checks:

```go
func (a *App) checkCommonMemberPermission(ctx context.Context, key channelmembers.ChannelKey, fromUID string) (frame.ReasonCode, error) { ... }
```

Use this for group after metadata checks and for visitors fallback with:

```go
key := channelmembers.ChannelKey{ChannelID: cmd.ChannelID, ChannelType: frame.ChannelTypeCustomerService}
```

- [ ] **Step 6: Update error maps**

Map `runtimechannelid.ErrInvalidAgentChannel` alongside `ErrInvalidPersonChannel` in gateway and API error mappers.

- [ ] **Step 7: Run GREEN tests**

Run the same commands from Step 3. Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/usecase/message/send.go internal/usecase/message/permission.go internal/usecase/message/send_test.go internal/access/gateway/error_map.go internal/access/api/error_map.go
git commit -m "feat: support legacy special channel send permissions"
```

---

### Task 7: Focused Regression Suite

**Files:**
- No new files unless a failure requires a fix.

- [ ] **Step 1: Run focused package tests**

Run:

```bash
GOWORK=off go test ./internal/runtime/channelid ./internal/usecase/message ./internal/access/gateway ./internal/access/api ./internal/app ./cmd/wukongim -count=1
```

Expected: PASS.

- [ ] **Step 2: Fix any focused regressions with TDD**

If a regression appears, use `superpowers:systematic-debugging` before changing code. Add or adjust the smallest test that exposes the behavior, then fix.

- [ ] **Step 3: Run internal boundary test if imports changed unexpectedly**

Run:

```bash
GOWORK=off go test ./internal -run TestInternalImportBoundaries -count=1
```

Expected: PASS.

- [ ] **Step 4: Commit fixes if needed**

```bash
git status --short
git add <changed-files>
git commit -m "fix: stabilize send permission convergence"
```

---

### Task 8: Final Verification and Handoff

**Files:**
- Possibly update `docs/development/PROJECT_KNOWLEDGE.md` only if a concise, durable project rule was discovered. Do not add verbose notes.

- [ ] **Step 1: Use verification-before-completion**

Before claiming completion, invoke `superpowers:verification-before-completion` and run the exact verification command.

- [ ] **Step 2: Run final verification**

Run:

```bash
GOWORK=off go test ./internal/runtime/channelid ./internal/usecase/message ./internal/access/gateway ./internal/access/api ./internal/app ./cmd/wukongim -count=1
```

Expected: PASS.

- [ ] **Step 3: Check git status and log**

Run:

```bash
git status --short --branch
git log --oneline -8
```

Expected: clean worktree on `feature/send-permission-convergence-p2` with plan/spec/implementation commits.

- [ ] **Step 4: Report concise result**

In Chinese, report:

- branch/worktree path;
- main behavior restored;
- exact verification command and result;
- next options: review diff, merge to `main`, or continue with next remaining send-path gap.
