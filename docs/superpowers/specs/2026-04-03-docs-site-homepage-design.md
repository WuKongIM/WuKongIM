# Docs Site Homepage Design

**Date:** 2026-04-03
**Scope:** Refresh the `docs-site` root homepage so it behaves like a real documentation homepage instead of a simple routing page.

## Goal

Make `/` in `docs-site` work as a complete docs homepage for first-time visitors:

1. Explain what WuKongIM is.
2. Help readers judge whether it fits their scenario.
3. Give a clear next step into the main documentation paths.

The homepage should still feel like documentation, not a marketing landing page.

## Chosen Direction

Use a mixed homepage:

- The first screen explains the product value and usage context.
- The next sections give direct entry cards into the main docs paths.
- The page ends with a recommended reading sequence for first-time readers.

`AI Agent` remains a secondary recommendation rather than a primary first-screen path.

## Information Architecture

The homepage should be organized in this order:

1. `Hero`
2. `Primary entry cards`
3. `Capability summary`
4. `Secondary entry cards`
5. `Recommended reading path`

## Section Details

### Hero

- Title: `WuKongIM 文档`
- Subtitle: explain that WuKongIM is a high-performance, distributed IM infrastructure.
- Supporting copy: two short sentences describing the kind of systems it supports and how readers should use this docs site.

This section should immediately answer:

- What is WuKongIM?
- Who is this documentation for?
- What should I do next?

### Primary Entry Cards

Show four primary paths with one-line explanations:

1. `认识 WuKongIM`
2. `快速开始`
3. `客户端接入`
4. `服务端集成`

These are the main first-step actions and should carry the strongest visual weight after the hero.

### Capability Summary

Summarize WuKongIM with 3 to 4 short capability blocks. The approved set is:

- `分布式架构`
- `高并发长连接`
- `多端消息同步`
- `可扩展业务集成`

Each block should use a single concise sentence. This section exists to reinforce product understanding without turning the page into a long overview article.

### Secondary Entry Cards

Show three lighter-weight recommendations:

1. `部署与运维`
2. `参考与资源`
3. `AI Agent 场景`

`AI Agent 场景` should explicitly frame itself as a scenario-specific path for users building agent, event-stream, or orchestration systems.

### Recommended Reading Path

Provide a simple numbered flow:

1. 认识产品
2. 跑通单机
3. 接客户端或服务端
4. 进入部署与运维

This section should lower decision cost for new readers and make the first successful path obvious.

## Interaction and Tone Constraints

- Keep interactions simple and documentation-oriented.
- Do not add carousel, rotating banners, or search-heavy hero treatment.
- Do not turn the page into a marketing homepage.
- Keep the visual hierarchy stronger than a normal docs page, but keep copy precise and utilitarian.

## Implementation Notes

- Reuse the existing Fumadocs MDX homepage file at `docs-site/content/docs/index.mdx`.
- Prefer MDX-native components already supported by the docs site such as cards and lists.
- Keep top-level routing unchanged. This is a content and presentation refresh, not a route restructuring task.

## Verification

The work is complete when:

1. `/` renders the new homepage content locally.
2. The homepage clearly distinguishes primary vs secondary navigation.
3. The homepage copy reflects the approved mixed direction.
4. `npm run types:check` succeeds in `docs-site`.
5. `npm run build` succeeds in `docs-site`.
