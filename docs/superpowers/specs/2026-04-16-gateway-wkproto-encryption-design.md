# Gateway WKProto Encryption Design

**Context**

The current gateway authentication path in `internal/gateway/auth.go` only covers token verification, ban checks, and protocol version negotiation. It does not implement the older wkproto connect-time key exchange or the per-session payload encryption and `MsgKey` validation that existed in the legacy `handleConnect` / send / push flow.

We want to restore the old wkproto encryption semantics without pushing protocol-specific crypto logic down into `internal/usecase/*` or `pkg/channel/*`. The internal business and cluster pipeline should continue to operate on plaintext messages; encryption should only exist at the client-facing gateway boundary.

**Goal**

- Restore wkproto connect-time Curve25519/AES session negotiation.
- Enable encryption by default for wkproto connections.
- Keep encryption disabled by default for jsonrpc connections.
- Reject wkproto `CONNECT` without `ClientKey` using `CONNACK(ReasonClientKeyIsEmpty)` and then close.
- Validate and decrypt encrypted wkproto `SEND` payloads before they reach the message usecase.
- Encrypt wkproto `RECV` payloads per target session before they are encoded onto the wire.
- Preserve the existing layering: access adapts protocol behavior, usecases keep plaintext business semantics, and app remains the composition root.

**Design**

1. Extend `internal/gateway/auth.go` so `WKProtoAuthOptions` includes `EncryptionEnabled bool`.
   - `internal/app/build.go` wires the default gateway authenticator with wkproto encryption enabled.
   - jsonrpc stays unencrypted by default.
   - wsmux inherits the selected subprotocol behavior: wkproto sessions use encryption, jsonrpc sessions do not.

2. Perform the wkproto encryption handshake during authentication after token/ban checks succeed.
   - For encrypted wkproto sessions, `ConnectPacket.ClientKey` is required.
   - Missing `ClientKey` returns `ConnackPacket{ReasonCode: frame.ReasonClientKeyIsEmpty}`.
   - Invalid base64 / invalid client public key / shared-key derivation failure returns `ConnackPacket{ReasonCode: frame.ReasonAuthFail}`.
   - On success, the server generates a temporary Curve25519 keypair, derives the shared key, computes `aesKey = MD5(base64(sharedKey))[:16]`, generates a 16-byte IV/salt, and returns the server public key plus salt through `ConnackPacket.ServerKey` and `ConnackPacket.Salt`.

3. Persist encryption state on the gateway session.
   - Add gateway session values for:
     - encryption enabled
     - AES key
     - AES IV / salt
   - Keep the existing session values for UID, device information, and negotiated protocol version.
   - This makes encryption available to both ingress frame adaptation and egress frame encoding without leaking crypto details into usecases.

4. Decrypt and validate wkproto `SEND` packets in `internal/access/gateway`.
   - Before `mapSendCommand()` builds the plaintext usecase command, inspect the current session:
     - if the session is not encrypted, or the frame has `SettingNoEncrypt`, pass the packet through unchanged
     - otherwise validate `MsgKey` using the old verity string rule and decrypt `Payload`
   - `MsgKey` mismatch returns `SENDACK(ReasonMsgKeyError)` without closing the connection.
   - Payload decryption failure returns `SENDACK(ReasonPayloadDecodeError)` without closing the connection.
   - After successful decryption, replace `pkt.Payload` with plaintext and clear transport-only `MsgKey` before handing the message to the usecase / storage path so the internal pipeline remains plaintext and session-agnostic.

5. Encrypt wkproto `RECV` packets in `internal/gateway/protocol/wkproto/adapter.go`.
   - Before encoding an outbound `*frame.RecvPacket` for a target session:
     - if the target session is not encrypted, or the packet has `SettingNoEncrypt`, encode it as-is
     - otherwise clone the packet, encrypt its payload with the target session key/IV, recompute `MsgKey` from the encrypted packet contents, and encode the cloned packet
   - This keeps per-recipient encryption at the final wire boundary while allowing the delivery pipeline to continue operating on plaintext `RecvPacket` values.

6. Treat outbound encryption failures as session-level write failures.
   - If an encrypted wkproto session is missing its keys or outbound payload encryption fails, do not emit a partially encoded `RECV`.
   - Instead, surface the failure as a session/protocol error and close that connection.
   - This keeps the client boundary fail-fast and avoids delivering corrupted encrypted frames.

7. Do not add a new runtime config switch in this change.
   - This design intentionally fixes the default behavior to:
     - wkproto: encryption enabled
     - jsonrpc: encryption disabled
   - If later rollout needs a toggle, that can be added as a separate config change with `wukongim.conf.example` alignment.

**Testing**

- `internal/gateway/auth_test.go`
  - successful wkproto connect returns `ServerKey` / `Salt`
  - missing `ClientKey` returns `ReasonClientKeyIsEmpty`
  - jsonrpc / non-encrypted paths do not emit encryption material
- `internal/access/gateway/handler_test.go`
  - encrypted `SEND` with valid `MsgKey` is decrypted before the message usecase sees it
  - invalid `MsgKey` returns `SENDACK(ReasonMsgKeyError)`
  - invalid encrypted payload returns `SENDACK(ReasonPayloadDecodeError)`
- `internal/gateway/protocol/wkproto/adapter_test.go`
  - encrypted sessions emit encrypted `RECV` payload plus regenerated `MsgKey`
  - `SettingNoEncrypt` bypasses encryption
- Integration coverage in `internal/access/gateway/integration_test.go` and `internal/gateway/gateway_test.go`
  - real wkproto `CONNECT` handshake returns `ServerKey` / `Salt`
  - encrypted `SEND` reaches the server as plaintext
  - plaintext committed message is delivered back to an encrypted wkproto client as encrypted `RECV`
