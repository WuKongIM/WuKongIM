# cluster AGENTS

This file is for agents working inside `test/e2e/cluster`.

## Domain Purpose

This domain covers black-box cluster membership and control-plane behavior that is observable through real node processes and public APIs.

## Scenarios

| Scenario | Purpose | Run |
| --- | --- | --- |
| `dynamic_node_join` | Prove a fourth data node can join a running three-node cluster through seed config, exchange WKProto person messages with an existing node, and receive Slot resources through manager onboarding. | `go test -tags=e2e ./test/e2e/cluster/dynamic_node_join -count=1` |

## Maintenance Rules

- When adding a new cluster scenario, create `test/e2e/cluster/<scenario>/`.
- Give each scenario its own `AGENTS.md` and one primary `<scenario>_test.go`.
- If a scenario is added, removed, renamed, or its run command, steps, or diagnostics change, update this file and the scenario's `AGENTS.md` in the same change.
- Keep one-off helpers inside the scenario directory first. Only promote them to `test/e2e/suite` after real multi-scenario reuse appears.
