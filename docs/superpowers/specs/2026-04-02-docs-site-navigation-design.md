# Docs Site Navigation Design

**Date:** 2026-04-02
**Scope:** Reorganize the new `docs-site` around first-time-user task flows instead of legacy content buckets.

## Goal

Make the new WuKongIM docs immediately understandable for first-time visitors by using a clear top-level navigation:

1. `认识 WuKongIM`
2. `快速开始`
3. `客户端接入`
4. `服务端集成`
5. `部署与运维`
6. `参考与资源`

## Information Architecture

The site should guide readers through this path:

`先了解 -> 先跑起来 -> 接客户端 -> 接服务端 -> 上生产 -> 查资料`

Top-level navigation uses task-oriented labels. Technical groupings such as SDK, API, and configuration move into secondary navigation within each section.

## Content Mapping Principles

- Legacy overview and concepts pages move into `认识 WuKongIM`.
- Fastest-path setup content moves into `快速开始`.
- SDK content moves into `客户端接入`.
- Server API and webhook material moves into `服务端集成`.
- Deployment, configuration, monitoring, scaling, and upgrade content moves into `部署与运维`.
- Protocol, stress reports, releases, demos, and FAQ move into `参考与资源`.

## Implementation Notes

- Use the existing Fumadocs content tree under `docs-site/content/docs`.
- Add `meta.json` files to control sidebar order and folder labels.
- Keep the current `/getting-started` URL as a short routing page to avoid a dead-end during migration.
- Update `/` to act as a clear distribution page rather than a placeholder.
