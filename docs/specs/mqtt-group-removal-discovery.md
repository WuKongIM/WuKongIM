# MQTT group removal discovery

`SourceDrain.SealGroup` resolves one current closed group subscription before
calling the existing exact-source drain. The original subscription is the
recovery intent when protection completed before the first binding write.

## Failure inventory before implementation

- Failed protection/first registration leaves no key supplied to unsubscribe.
- A fabricated, active, inbox, replaced or foreign-owner intent creates cleanup.
- Existing cursors are reset, their source is replaced by today's generation,
  or missing bindings behind proven cursors are mistaken for empty preparation.
- A late registration after closure revives delivery or loses the original
  preparation revision needed to enter Removing.
- Ambiguous registration, lost replies, owner resume, revoked permission,
  conflicting metadata or dependency cancellation leaks responsibility or scope.
- Group discovery scans subscribers or requires source availability when an
  already initialized cursor identifies its exact binding.

## Contract

Read the exact current Removing/Removed group intent and at most two cursors
under the live owner. An existing cursor names the original source directly.
With no cursor, resolve replicated protection; recover the exact binding or
register unknown preparation with the original subscription generation as its
initial intent revision. Reread exact intent before registration, then use the
existing drain to choose a protected cancellation boundary and seal it. No
receive permission is needed for removal. Conflicts yield without retries.

This completes live-owner discovery for cancellation before the first binding.
It does not solve unattended ended-Session discovery, native source deactivation,
full establishment/shared-content readiness, inbox admission or product wiring.
The final projection and background recovery must still compose these paths.

Tests use the already approved usecase/metadata and app/real-cluster seams.

## Frozen context

Source `dc49297bfb56b640260c80d8af38f6b9d929e2c4`; SHA-256:

- `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `internal/usecase/mqttsession/FLOW.md`: `f55cacc6901572fe9f3cd5b6d25f5df51ada8f6c86cd5ccf26eccd2db3b3953b`
- `internal/app/FLOW.md`: `46781cef6b51ed21436e99301db9904a45e03eaf14ca4ac113b1073408492d0e`
