---
scope: package
summary: Exposes only protected named Review Agent checks and trusted generation-bound evidence through local stdio MCP.
---

# Review Agent Check MCP Flow

## Responsibility

`internal/access/reviewagentcheckmcp` is the credential-free local stdio MCP
adapter for the trusted named-check catalog, runner, and evidence ledger.
It does not own PR lifecycle, GitHub publication, or Review Agent decisions.

## Boundaries

- Callers select only a catalog name; no command, arguments, path, environment,
  URL, ref, pattern, output target, or network selector is accepted.
- Only a resolved protected check enters the pre-built private-network
  namespace and disposable checkout.
- GitHub, state mutation, publication, and general MCP capabilities are absent.

## Main Flows

1. List sorted protected check names from the fixed catalog.
2. Resolve one exact name, enter the sealed runner boundary, and append trusted
   generation-bound evidence.
3. Return the latest ledger result for the exact name and generation.

## Invariants and Failure Semantics

- The adapter starts on the credential-free host solely for MCP handshake.
- Arbitrary command execution and caller-shaped checkout/network behavior are
  impossible by contract.
- Evidence is append-only, bounded, and tied to the exact generation.

## Read First

- [Stdio server](server.go)
- [Server tests](server_test.go)

## Update Triggers

Update this file when tool inventory, named-check selection, namespace entry,
runner authority, or evidence lookup semantics change.
