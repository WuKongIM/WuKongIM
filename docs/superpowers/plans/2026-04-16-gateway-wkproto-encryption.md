# Gateway WKProto Encryption Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restore legacy wkproto encryption semantics in the new gateway stack: handshake on `CONNECT`, encrypted wkproto `SEND` ingress, encrypted wkproto `RECV` egress, and default-on wkproto / default-off jsonrpc behavior.

**Architecture:** Keep handshake and reusable crypto helpers in `internal/gateway`, because the gateway owns session lifecycle and protocol state. Decrypt wkproto `SEND` packets in `internal/access/gateway` so errors can still return `SENDACK` without tearing down the session, and encrypt wkproto `RECV` packets in `internal/gateway/protocol/wkproto` so the internal delivery pipeline stays plaintext and each target session gets its own wire payload.

**Tech Stack:** Go, `internal/gateway`, `internal/access/gateway`, `internal/app`, `pkg/protocol/frame`, `pkg/protocol/codec`, `golang.org/x/crypto/curve25519`, stdlib `crypto/aes` / `crypto/cipher` / `crypto/md5` / `encoding/base64`.

---

## File Map

- `internal/gateway/types/session_values.go`
  - add session keys for wkproto encryption state, AES key, and AES IV/salt
- `internal/gateway/crypto.go`
  - gateway-scoped crypto helpers: Curve25519 key exchange, AES encrypt/decrypt, msg-key generation/validation, session encryption state helpers
- `internal/gateway/crypto_test.go`
  - focused unit coverage for helper behavior
- `internal/gateway/auth.go`
  - extend `WKProtoAuthOptions` and populate handshake material during `CONNECT`
- `internal/gateway/auth_test.go`
  - cover handshake success/failure and jsonrpc bypass
- `internal/app/build.go`
  - wire default app gateway authenticator with wkproto encryption enabled
- `internal/access/gateway/encryption.go`
  - access-layer helper that turns an encrypted wkproto `SendPacket` into plaintext or a specific `ReasonCode`
- `internal/access/gateway/frame_router.go`
  - call the helper before building the usecase command; write `SENDACK` for msg-key/payload failures
- `internal/access/gateway/handler_test.go`
  - unit coverage for ingress decrypt / validation behavior
- `internal/gateway/protocol/wkproto/adapter.go`
  - encrypt outbound `RecvPacket` payloads and regenerate `MsgKey` before wire encoding
- `internal/gateway/protocol/wkproto/adapter_test.go`
  - unit coverage for encrypted and bypassed outbound frames
- `internal/gateway/testkit/wkproto_crypto.go`
  - reusable test-only client helper for generating `ClientKey`, applying `Connack`, encrypting `SendPacket`, and decrypting `RecvPacket`
- `internal/gateway/gateway_test.go`
  - gateway listener-level handshake and wkproto wire behavior
- `internal/access/gateway/integration_test.go`
  - end-to-end gateway send/recv encryption behavior
- `internal/gateway/core/server_test.go`
  - keep existing auth/session tests targeted by either supplying a valid `ClientKey` or disabling encryption where the test is about non-crypto auth flow
- `internal/app/comm_test.go`
  - app-level wkproto connect helpers must send a valid `ClientKey` and expose decrypt helpers for recv assertions
- `internal/app/integration_test.go`
  - app-level single-node cluster handshake / send / recv coverage
- `internal/app/multinode_integration_test.go`
  - direct `ConnectPacket` construction must be updated for default-on encryption
- `internal/app/send_stress_test.go`
  - connect helper must include `ClientKey` so the package still compiles and focused tests keep passing

### Task 1: Add Reusable Gateway Crypto Primitives

**Files:**
- Create: `internal/gateway/crypto.go`
- Create: `internal/gateway/crypto_test.go`
- Modify: `internal/gateway/types/session_values.go`

- [ ] **Step 1: Write the failing tests**

Add focused helper tests for:
- deriving AES key + IV from a client public key and server private key
- AES encrypt/decrypt round-trip for wkproto payloads
- msg-key generation/validation for `SendPacket.VerityString()`
- sealing a `RecvPacket` copy with encrypted payload plus regenerated `MsgKey`

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/gateway -run 'TestGatewayEncryption' -v`
Expected: FAIL because the crypto helpers and session value constants do not exist yet.

- [ ] **Step 3: Write minimal implementation**

Implement the smallest helper surface that both ingress and egress code can share:
- derive session keys using Curve25519 + `MD5(base64(sharedKey))[:16]`
- generate a 16-byte random IV/salt
- encrypt/decrypt AES-CBC PKCS7 payloads encoded as base64
- generate and validate msg-key values
- expose session-value helpers/constants for encryption state

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/gateway -run 'TestGatewayEncryption' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/gateway/crypto.go internal/gateway/crypto_test.go internal/gateway/types/session_values.go
git commit -m "feat: add gateway wkproto crypto primitives"
```

### Task 2: Add WKProto Handshake Support to Authentication

**Files:**
- Modify: `internal/gateway/auth.go`
- Modify: `internal/gateway/auth_test.go`
- Modify: `internal/app/build.go`

- [ ] **Step 1: Write the failing tests**

Add auth tests for:
- wkproto auth success returns `ConnackPacket.ServerKey` and `ConnackPacket.Salt`
- wkproto auth with encryption enabled and missing `ClientKey` returns `ReasonClientKeyIsEmpty`
- invalid `ClientKey` returns `ReasonAuthFail`
- jsonrpc / non-encrypted sessions do not emit encryption material

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/gateway -run 'TestAuthenticator' -v`
Expected: FAIL because `WKProtoAuthOptions` does not negotiate encryption yet.

- [ ] **Step 3: Write minimal implementation**

Update `NewWKProtoAuthenticator()` to:
- accept `EncryptionEnabled bool`
- require `ClientKey` for encrypted wkproto sessions
- negotiate `ServerKey` / `Salt`
- store encryption session values in `AuthResult.SessionValues`
- keep existing token / ban / version behavior intact

Wire app startup in `internal/app/build.go` with `EncryptionEnabled: true`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/gateway -run 'TestAuthenticator' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/gateway/auth.go internal/gateway/auth_test.go internal/app/build.go
git commit -m "feat: add wkproto connect encryption handshake"
```

### Task 3: Decrypt and Validate Encrypted WKProto SEND Frames

**Files:**
- Create: `internal/access/gateway/encryption.go`
- Modify: `internal/access/gateway/frame_router.go`
- Modify: `internal/access/gateway/handler_test.go`

- [ ] **Step 1: Write the failing tests**

Add handler tests for:
- valid encrypted `SendPacket` payload is decrypted before `message.Send()` sees it
- invalid `MsgKey` returns `SendackPacket{ReasonCode: frame.ReasonMsgKeyError}`
- invalid encrypted payload returns `SendackPacket{ReasonCode: frame.ReasonPayloadDecodeError}`
- `SettingNoEncrypt` bypasses decrypt/validation and still reaches the usecase

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/access/gateway -run 'TestHandlerOnFrameSend' -v`
Expected: FAIL because ingress send handling currently forwards encrypted payloads unchanged.

- [ ] **Step 3: Write minimal implementation**

Implement a small access-layer helper that:
- inspects session encryption state from `coregateway.Context`
- bypasses non-encrypted or `SettingNoEncrypt` traffic
- validates msg-key and decrypts payload for encrypted wkproto traffic
- clears transport-only `MsgKey` before the plaintext packet is mapped into a usecase command
- returns specific `ReasonCode` values so `frame_router.go` can write `SENDACK` without closing the connection

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/access/gateway -run 'TestHandlerOnFrameSend' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/access/gateway/encryption.go internal/access/gateway/frame_router.go internal/access/gateway/handler_test.go
git commit -m "feat: decrypt encrypted wkproto send frames"
```

### Task 4: Encrypt Outbound WKProto RECV Frames Per Session

**Files:**
- Modify: `internal/gateway/protocol/wkproto/adapter.go`
- Modify: `internal/gateway/protocol/wkproto/adapter_test.go`

- [ ] **Step 1: Write the failing tests**

Add adapter tests for:
- encrypted session encodes `RecvPacket` with encrypted payload and regenerated `MsgKey`
- `SettingNoEncrypt` leaves the outbound `RecvPacket` untouched
- missing session keys on an encrypted session return an error instead of encoding a partial frame

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/gateway/protocol/wkproto -run 'TestAdapter' -v`
Expected: FAIL because outbound wkproto encode currently writes plaintext `RecvPacket` frames.

- [ ] **Step 3: Write minimal implementation**

Update `Adapter.Encode()` so that for outbound `*frame.RecvPacket` frames it:
- checks whether the target session is encrypted
- clones the packet before mutation
- encrypts the payload and recomputes `MsgKey`
- preserves plaintext behavior for non-encrypted sessions and `SettingNoEncrypt`

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/gateway/protocol/wkproto -run 'TestAdapter' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/gateway/protocol/wkproto/adapter.go internal/gateway/protocol/wkproto/adapter_test.go
git commit -m "feat: encrypt outbound wkproto recv frames"
```

### Task 5: Add Reusable WKProto Test Client Helpers and Gateway-Level Integration Coverage

**Files:**
- Create: `internal/gateway/testkit/wkproto_crypto.go`
- Modify: `internal/gateway/gateway_test.go`
- Modify: `internal/access/gateway/integration_test.go`
- Modify: `internal/gateway/core/server_test.go`

- [ ] **Step 1: Write the failing tests**

Add or tighten gateway/integration coverage for:
- real wkproto `CONNECT` returns non-empty `ServerKey` and `Salt`
- encrypted `SEND` reaches the server as plaintext
- encrypted `RECV` arrives on the client as ciphertext that the test helper can decrypt back to the original payload

Also update core server tests that currently use `NewWKProtoAuthenticator(...)` so they either:
- pass a valid `ClientKey`, or
- explicitly disable encryption when the test is about non-crypto auth behavior only

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/gateway ./internal/access/gateway -run 'TestGateway|TestServer|TestGatewayWKProto' -v`
Expected: FAIL because current test helpers do not negotiate wkproto encryption and existing connects omit `ClientKey`.

- [ ] **Step 3: Write minimal implementation**

Create a small reusable test helper that can:
- generate a wkproto client keypair
- build encrypted `ConnectPacket` values
- apply `ConnackPacket` handshake state
- encrypt `SendPacket` payloads
- decrypt `RecvPacket` payloads

Use it to keep gateway/core/access integration tests readable and focused.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/gateway ./internal/access/gateway -run 'TestGateway|TestServer|TestGatewayWKProto' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/gateway/testkit/wkproto_crypto.go internal/gateway/gateway_test.go internal/access/gateway/integration_test.go internal/gateway/core/server_test.go
git commit -m "test: cover wkproto encryption handshake and gateway flows"
```

### Task 6: Update App-Level WKProto Helpers and Regression Coverage

**Files:**
- Modify: `internal/app/comm_test.go`
- Modify: `internal/app/integration_test.go`
- Modify: `internal/app/multinode_integration_test.go`
- Modify: `internal/app/send_stress_test.go`

- [ ] **Step 1: Write the failing tests**

Tighten app-level tests so default wkproto behavior is explicit:
- single-node cluster connect helper must send a valid `ClientKey`
- app integration tests that assert recv payloads must decrypt them before comparison
- multinode / stress helper paths must compile and connect with default-on encryption

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app -run 'TestConnectToApp|TestApp|TestMultinode' -v`
Expected: FAIL because app test helpers currently construct wkproto `ConnectPacket` values without `ClientKey`.

- [ ] **Step 3: Write minimal implementation**

Update the shared app test helpers in `internal/app/comm_test.go` first, then fix the direct `ConnectPacket` call sites in app integration / multinode / stress tests to use the same encrypted connect semantics.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/app -run 'TestConnectToApp|TestApp|TestMultinode' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/app/comm_test.go internal/app/integration_test.go internal/app/multinode_integration_test.go internal/app/send_stress_test.go
git commit -m "test: update app wkproto helpers for encryption"
```

### Task 7: Final Verification

**Files:**
- Modify: `internal/gateway/auth.go`
- Modify: `internal/access/gateway/frame_router.go`
- Modify: `internal/gateway/protocol/wkproto/adapter.go`
- Modify: `internal/app/build.go`

- [ ] **Step 1: Run focused gateway and access verification**

Run: `go test ./internal/gateway/... ./internal/access/gateway -v`
Expected: PASS

- [ ] **Step 2: Run app-level regression coverage**

Run: `go test ./internal/app -v`
Expected: PASS

- [ ] **Step 3: Run shared protocol package regression**

Run: `go test ./pkg/protocol/... -v`
Expected: PASS

- [ ] **Step 4: Review docs / flow impact**

Confirm no relevant `FLOW.md` under the touched packages needs updating. If implementation changes any package flow docs later, update them in the same task before completion.

- [ ] **Step 5: Commit the final integration batch if anything remains uncommitted**

```bash
git status --short
git add <remaining files>
git commit -m "feat: restore wkproto encryption flow"
```
