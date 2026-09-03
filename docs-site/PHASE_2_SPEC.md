# WuKongIM v3 Documentation — Phase 2 Specification

## Goal

Publish the first complete bilingual onboarding journey without broad legacy
content migration. A new reader must be able to understand the product boundary,
run a Linux-package single-node cluster, verify two-way messaging, and choose
the next documentation path.

## Published routes

- Product overview and “What is WuKongIM?”
- Quick Start overview
- Prerequisites
- Start a Single-node Cluster
- Send the First Message
- Run the Chat Demo
- Next Steps and FAQ
- Core Concepts overview
- Configuration overview

Every route above has matching Chinese and English MDX and is included in
search, sitemap, LLM outputs, and per-page Markdown.

## Source-of-truth boundaries

- Linux support, installation commands, and local ports follow the signed
  Preview package path published by the Linux deployment guide.
- Configuration initialization, validation, service lifecycle, and data paths
  follow the package runtime contract under `/etc/wukongim`,
  `/var/lib/wukongim`, and systemd.
- Chat Demo steps follow the embedded Demo implementation.
- Every deployment is described as a cluster; a one-node deployment is a
  single-node cluster.
- Remote browser access uses SSH port forwarding so the quick start does not
  require exposing Manager or the Demo ports to the Internet.
- Example credentials and `/user/token` are explicitly development-only.

## Validation

- Unit tests freeze the bilingual publication registry and require both MDX
  variants for every published route.
- Static-output validation compares sitemap and search content against the
  registry and confirms planned pages remain excluded.
- Local and review-time validation runs the complete `bun run verify` workflow.
  A remote docs-site fixed suite requires a separate change to the repository's
  protected Agent validation protocol; ordinary automatic PR workflows are not
  allowed.

## Excluded

- Production deployment, DNS, hosting, or cutover.
- Full SDK, HTTP API, protocol, operations, or architecture content.
- Complete legacy content migration and redirect audit.
- Treating the Linux Preview quick start or repository Compose stack as a
  production deployment template.
