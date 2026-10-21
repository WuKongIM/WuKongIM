# systemd Job compatibility — frozen before shipping tests/code

Base: e30299fa59ea6669ea71024eabe209840f241f19. Only local Bash/Python parser and fake-adapter integration checks; no cloud/SSH/paid starts/Actions operations.

## Evidence and proposed fix

The reconstructed run 33 pre-kill state is exactly 104 bytes, SHA256 57c3e320a832417c5e803553961f261771af23813115c207218162f214488524, without a final newline. It records active/running with an explicitly empty Job. Parent has authenticated the original unit digest/exit receipts; original files will be referenced separately. Existing e302 rejects this on Linux Bash 5, while only substituting Job=0 succeeds. Post-kill replay is separately preserved. This does not explain run 32.

systemd v255 exposes Job as (uint32, object path); no queued job has ID 0. Its systemctl renderer emits an empty property for zero and --property includes empty values. Thus an explicitly present empty Job must normalize to numeric zero in both existing parsers. Presence must be distinct from value. The shell must explicitly reject invalid inputs instead of relying on Bash-version-dependent errexit. Keep all state classifications unchanged. Nonempty Job stays decimal uint32, max ten digits (existing diagnostic bound); leading zero is decimal, not octal. Process the final unterminated input line so it cannot hide a missing/extra property.

## Owned six-file scope

- scripts/chat-lifecycle/classify-stage-service-state.sh
- scripts/chat-lifecycle/pre-clock-diagnostic.py
- scripts/chat_lifecycle_stage_state_test.go (new default parser CLI tests)
- scripts/chat_lifecycle_preclock_capture_integration_test.go (existing actual-loop fake adapters)
- CHANGELOG.md (operator fix)
- docs/development/PROJECT_KNOWLEDGE.md (systemd boundary)

Do not change stage orchestration, state policy, deadlines, retry/continuation admission, cost/permissions/workflows, schemas, or protected agent controls. Diagnostic enums may include maintenance/refreshing even though control classification rejects them; this intentional distinction is unchanged.

## Complete planned failure/regression matrix

1. Both parsers: explicit empty/zero/no-job, queued positive, uint32 max, leading-zero zero and positive, final line with no newline, reverse property order. Preserve running (active/activating/reloading), terminal (failed/deactivating/inactive no job), pending (inactive queued).
2. Reject: missing Job or ActiveState, duplicate empty Job or ActiveState, malformed/nondecimal/negative/overflow/overlength Job, unknown property including an unterminated third line, empty ActiveState, blank line. Explicit shell exit 2 with no output; diagnostic valid=false without raw value disclosure. Empty Job remains raw SHA256(empty) while typed value is 0; absent stays missing; duplicate stays invalid.
3. Actual e302 stage loop: exact 104-byte initial observation reaches existing readiness deadline rather than parse failure; inactive empty Job stays terminal; missing/duplicate Job fail closed. Every branch preserves original command/classifier exit and before-kill receipt/cleanup. Existing queued/Job=0/SSH/start/summary branches remain. Integration tier only; local adapters, no network or load.
4. Verify both local /bin/bash 3.2.57 and separately pinned local bash 5.2.21 with frozen path/hash/version, not shell aliases. All shipping tests written/frozen before either parser changes. Record red and green separately.

## Validation boundary

Focused parser/default and tagged actual-loop tests plus static shell/Python syntax and changed-file formatting. Relevant policy checks selected from frozen policy: go-unit/go-vet/go-format/go-mod-tidy, scripts-integration, flow-doc-contracts; full Linux CI is later parent delivery, not this diagnosis. No test pass here proves a two-hour rehearsal or formal soak.
