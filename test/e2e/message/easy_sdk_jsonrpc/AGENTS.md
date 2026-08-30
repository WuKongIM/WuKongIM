# EasySDK JSON-RPC E2E Scenario

## Purpose

This scenario proves that a real `cmd/wukongim` single-node cluster accepts the
released EasySDK iOS and Android JSON-RPC wire contracts over the public WSMux
WebSocket endpoint. It is a source-aligned wire harness, not a claim that an
iOS or Android SDK binary ran on a device.

The wire shapes are pinned to:

- EasySDK iOS v1.0.3 at commit
  `643848f85be70e3e3f2be22fceb86ae428b6cc38`.
- EasySDK Android v1.0.3 at commit
  `62084632cd8d1f26c751b053b0fb82d6aaa63892`.

## Required Closure

- Start the real product with `suite.StartSingleNodeCluster`, the public
  WebSocket Gateway, and an explicit `WK_CLUSTER_HASH_SLOT_COUNT=256` override.
- Use the public WSMux `/ws` endpoint only.
- Send every iOS frame as a binary WebSocket message with camelCase JSON-RPC
  fields and direct JSON-object message payloads. Its RECVACK carries both
  `messageId` and `messageSeq`, matching the released client.
- Send every Android frame as a text WebSocket message with snake_case
  JSON-RPC fields and the JSON-text-string payload shown by the released
  README. Require server replies to Android to be text messages, matching the
  released client.
- Connect Alice and Bob, then prove bidirectional
  `SEND -> SENDACK -> RECV -> RECVACK` and correlated `ping` responses.
- Use only public `/user/onlinestatus` polling to prove disconnect cleanup and
  same-device reconnect. Keep all polling and I/O deadlines bounded.

## Boundaries

- Do not inspect storage, in-process state, logs as an assertion surface, or
  package-private runtime state.
- Do not import product protocol or gateway packages. JSON-RPC field names and
  public numeric values belong to this external-client contract.
- Keep the source-aligned client and assertions scenario-local until another
  E2E scenario genuinely reuses them.
- On failure, include only the suite's bounded process diagnostics.

## Run

```bash
GOWORK=off go test -tags=e2e ./test/e2e/message/easy_sdk_jsonrpc -count=1 -timeout 2m -p=1
```
