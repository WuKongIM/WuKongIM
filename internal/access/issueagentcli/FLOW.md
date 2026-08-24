---
scope: package
summary: Exposes the fixed Issue Agent command set as strict bounded JSON process operations with generic credential-safe diagnostics.
---

# Issue Agent CLI Flow

## Responsibility

`internal/access/issueagentcli` validates and dispatches the fixed Issue Agent
process commands and emits exactly one JSON value per invocation.
It does not own Issue Agent lifecycle, GitHub effects, verification, or model work.

## Boundaries

- Business lifecycle, GitHub authority, verification, filesystem capture, and
  publication live behind injected app operations.
- The CLI accepts only stdin or one input file and owns no shell plan or model
  execution.
- The command catalog is closed and changes require contract tests.

## Main Flows

1. Select one exact allowlisted command and one bounded JSON input source.
2. Strictly decode the operation request and delegate it once.
3. Encode one bounded JSON response; write generic diagnostics to stderr only.

## Invariants and Failure Semantics

- Unknown commands, flags, fields, trailing JSON, and oversized inputs fail
  closed.
- Errors never echo request content, credentials, tokens, or candidate data.
- Process exit and JSON output remain deterministic for workflow callers.

## Read First

- [Command boundary](command.go)
- [Command tests](command_test.go)

## Update Triggers

Update this file when the command catalog, input sources, JSON bounds, dispatch
ownership, output contract, or diagnostic secrecy changes.
