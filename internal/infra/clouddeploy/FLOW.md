---
scope: package
summary: Provides a root-anchored filesystem adapter for cloud deployment files, inventory, and digests.
---

# Cloud Deployment Infrastructure Flow

## Responsibility

This package provides the production filesystem adapter used by cloud
deployment orchestration to read, write, inventory, and digest files beneath
one configured root.
It does not build deployment plans, call cloud providers, or manage services.

## Boundaries

- It owns filesystem mechanics only, not deployment planning, cloud-provider
  behavior, or service lifecycle policy.
- Paths are root-relative and no-follow; callers cannot escape the configured
  deployment tree or traverse symlinks.
- Reads are bounded and writes are atomic with explicit file modes.

## Main Flows

1. Resolve a caller path beneath the configured root and reject traversal or
   symlink ambiguity.
2. Read or atomically replace the bounded file using the requested safe mode.
3. Walk the anchored tree to produce deterministic inventory and digest data.

## Invariants and Failure Semantics

- No operation follows symlinks or accepts an absolute path outside the root.
- Partial writes must not replace a previously valid file.
- Inventory and digest output is deterministic for the same filesystem state.
- Invalid paths, unsafe file types, and oversized reads fail closed.

## Read First

- [Directory adapter](directory.go)

## Update Triggers

Update this file when path resolution, file-type policy, size bounds, atomic
write behavior, inventory, or digest semantics change.
