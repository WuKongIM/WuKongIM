---
scope: package
summary: Exposes Review Agent control operations and model-result normalization through strict bounded JSON process contracts.
---

# Review Agent CLI Flow

## Responsibility

`internal/access/reviewagentcli` validates one fixed Review Agent command and
bounded JSON input, dispatches the app operation, and emits one JSON response.
It does not own Review Agent policy, GitHub effects, verification, or model work.

## Boundaries

- Control commands are strict JSON-only; the sole advisory exception normalizes
  one bounded model-authored Review result.
- The CLI accepts no shell plan, arbitrary command, path, URL, secret, or model
  instruction.
- Lifecycle and GitHub behavior remain behind app operations.

## Main Flows

1. Decode one exact control command request and delegate to app.
2. For result normalization, extract exactly one unambiguous bounded JSON object
   and validate the canonical Review contract.
3. Emit one JSON response or generic non-echoing error.

## Invariants and Failure Semantics

- Unknown fields, trailing data, competing JSON containers, and ambiguous prose
  fail closed.
- Advisory model output never gains control or publication authority through
  normalization.
- Errors never echo input or credential material.

## Read First

- [Command boundary](command.go)
- [Command tests](command_test.go)

## Update Triggers

Update this file when commands, model-result extraction, JSON bounds, dispatch,
output, or error-redaction semantics change.
