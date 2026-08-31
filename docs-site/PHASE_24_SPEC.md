# Phase 24: Kubernetes deployment reference

## Goal

Publish the bilingual `/server/deployment/kubernetes` reference architecture.
It is a source-aligned Beta guide, not an official Helm chart or a production
manifest.

SDK documentation is governed separately by `SDK_DOCUMENTATION_SPEC.md`.

## Kubernetes contract

The page must:

- treat one Pod as a single-node cluster and preserve full cluster semantics;
- build an immutable image from reviewed source and deploy by digest;
- use a StatefulSet, Headless Service, stable DNS, deterministic unique node
  IDs, the same complete static member list, and one independent PVC per node;
- keep `hash_slot_count = 256` and make replica counts fit the member count and
  real failure domains;
- use `/healthz` for process liveness/startup and `/readyz` for traffic
  admission;
- keep node transport, Manager, metrics, diagnostics, and client entry points
  in separate network boundaries;
- disable Kubernetes service-link injection or otherwise prove that no unknown
  `WK_*` environment variable reaches the fail-closed configuration loader;
- explain that PodDisruptionBudgets cover only some voluntary evictions and
  topology spread does not prove replica health;
- make clear that changing StatefulSet replicas is not a WuKongIM membership
  operation;
- use controlled node-by-node upgrades and retain backup, data-compatibility,
  stop, and rollback procedures.

The guide may link official Kubernetes StatefulSet, probe, disruption, and
topology-spread documentation. It must not copy a legacy Helm repository,
floating image tag, stale health path, or direct PVC deletion procedure.

## Publication integration

- Both language variants must exist before the route is published.
- Navigation, search, sitemap, LLM output, and generated navigation must all
  derive from `lib/navigation.ts`.
- Beta wording must remain visible after publication.

## Validation

The focused Kubernetes content contract and the normal `bun run verify` gate
must pass, including navigation parity, internal links, MDX, TypeScript, lint,
static export, search, sitemap, and machine-readable output.
