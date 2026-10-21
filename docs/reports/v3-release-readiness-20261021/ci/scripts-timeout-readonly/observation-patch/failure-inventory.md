# Diagnostic observation delta — tests first

Approved seam: existing static V3 candidate workflow contract. No new backend or runner seam.

1. A scripts job with only `GOFLAGS=-p=2` provides no per-test events; the existing scripts-job assertion must require `-p=2 -json`.
2. Adding JSON to default/E2E jobs would broaden this diagnostic change; assert the default flag remains `-p=2` and E2E has no GOFLAGS override.
3. Frozen policy argv/600s/9m/parallel=2/8MiB must still be consumed unchanged; existing assertions continue to guard the policy path, original argument call, no test filter or deadline override.

The contract edit below was saved before workflow/README edits. No test was executed during the parent-requested quiet window: RED and GREEN are both **not run**, not claimed. Existing exact source remains unchanged; this is a three-file proposal in /tmp only.

Follow-up authorization: root subsequently permitted only the eight existing Python static contracts in these /tmp copies. Baseline workflow + changed test produced one expected failure; candidate produced 8/8 pass. See 01-static-red.* and 02-static-green.*. No Go/build/backend commands ran.
