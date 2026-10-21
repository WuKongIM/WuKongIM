# Selected plan B: bounded installed-sandbox phase diagnostics

## Scope and preserved failure

Base/head for the incremental change is `19ce29fb2233b3039e29c503963e27bd1fab972a` (tree `8275ccb92c310f488d911b5aa077a962e1faad5c`). Native run `39660717995`, attempt 1, remains failed: case 1 / command 66, Alma installed sandbox, exit 124 in 600.151 seconds; 0 complete cases passed, 1 failed, 48 not reached. No same-source repeat is selected. This collector only wrote evidence; the implementation agent owns tests-first code and validation after handoff.

The exact Alma shell order is SHA check → key import and RPM signature check → DNF install python3 and `/candidate.rpm` → installed acceptance Python with stdout redirected to `/evidence/installed-cli.json`. The entire existing 137-byte combined output is retained with recomputed SHA. It proves checksum/signature success and external AppStream progress, not the exact phase at expiry. Because Python stdout was redirected and that individual receipt was not exported, a missing console CLI line does not prove CLI never started. Host receipt validation was not reached after the nonzero runner result.

## Exact five-file delta

1. `scripts/validate-production-package-clients.py`: only add fixed literal stdout markers around existing `_installed_cli_command` stage bodies. RPM has checksum, signature (import + checksig), dependencies (DNF), and CLI phases. APT has checksum, dependencies (policy setup + apt update/install), and CLI; no invented DEB signature phase.
2. `tests/native_stable_clients_drill.py`: preserve the fixed bounded marker summary on each `installed-client` command record from the existing captured output, including failure/timeout paths. Do not use it in any acceptance predicate.
3. `tests/test_installed_cli_acceptance.py`: tests first, executing actual generated shell through controlled command fixtures. Validate phase order and stop boundaries, original exits, timeout, and applicable APT/RPM distinctions.
4. `tests/test_native_stable_clients_drill.py`: tests first, exercising real child-process output/termination and extraction into command receipts; verify >4096-byte noise does not erase earlier phase evidence, while existing byte/time bounds remain authoritative.
5. `CHANGELOG.md`: concise operator-facing diagnostic improvement only.

No production publisher, signing/archive producer, workflow, protected Agent control, keys, source-provenance policy, release flags, fixtures, image pins, resource limits, original acceptance logic, or 49-case inventory changes. The validator is a previously unchanged public-client gate and is explicitly added to this review scope; it is not a signing/archive producer. Local CODEOWNERS covers all paths. Hosted ruleset protection was not inferred or changed.

## Fixed protocol and exact receipt shape

A marker is a complete literal line, no interpolated values:

```text
WK_INSTALLED_STAGE_V1:<checksum|signature|dependencies|cli>:<begin|complete>
```

There are eight permitted phase/event combinations, not eight distinct phases. RPM emits eight events; APT emits six. Emit `begin` immediately before the existing stage commands and `complete` only after those commands return successfully under the existing strict shell. Use literal `printf` markers; preserve the original command sequence and redirections. Do not introduce recovery, retry, ignored exits, completion traps, or extra timeouts.

The additional command-record field is:

```json
{
  "installed_phase_events": {
    "schema": "wukongim/native-installed-phase-events/v1",
    "events": [
      {"phase": "checksum", "event": "begin"},
      {"phase": "checksum", "event": "complete"}
    ],
    "truncated": false
  }
}
```

Extract only these exact complete lines, in observed order, retaining duplicates; cap at 16 events and set `truncated=true` when additional valid events are observed. Unknown or incomplete marker lines are ordinary captured output, not new event values. Absent markers yield an empty list; incomplete stages stay incomplete. This is diagnostic evidence only, potentially originating from combined subprocess output, and never overrides native exit, timeout, package identity, signature, or receipt validation. No arbitrary text, environment value, path, credential, or inferred per-phase time belongs in this field.

Extract in the existing runner's finalization path from its already bounded byte buffer. Do not increase the 8 MiB stream limit or 4096-byte output tail, change hashing, replace error propagation, or alter 600-second inner / 650-second outer deadlines. The summary survives noise that pushes markers outside the final tail; data beyond the original capture limit remains unobserved and must not be invented.

## All failure tests before implementation

- Execute the actual generated RPM and APT shell with controlled PATH command fixtures: exact applicable event order on success; checksum, signature, dependency and CLI failures retain their original nonzero exit and emit no later completion/begin. For APT no signature events appear.
- Exercise a controlled bounded timeout of generated shell and preserve a partial phase boundary without fabricating completion. Record this as a fixture mechanism proof, not a reproduction of historical DNF behavior.
- Exercise actual `run_command` subprocess capture for success, nonzero exit and controlled timeout; ensure phase summaries are retained in finalization and do not change the returned/raised outcome.
- Emit more than 4096 bytes of unrelated output after early markers: the existing tail may lose them, but the fixed summary retains them. Preserve output-size/hash semantics and the existing output-limit error.
- Cover empty/partial/unknown lines, duplicate events, and >16 valid events. Parsing remains bounded and diagnostic; even a complete-looking marker sequence cannot turn a nonzero exit or missing/invalid installed receipt into success.
- Confirm no arbitrary values are copied into the summary; retain exact native commands, resource flags, timer values, identity/verification gates, and 49 scenario list. Source-string assertions alone are insufficient for marker/control-flow proof.

Freeze first red results, minimal implementation, related green checks and final source/patch hashes for independent review. Real native acceptance is the subsequent new head's first CI, not these controlled fixtures. Preserve #76/#77 original failures and their uncertainty. No cloud action, production credential, same-source CI rerun, or merge is part of this diagnostic implementation.
