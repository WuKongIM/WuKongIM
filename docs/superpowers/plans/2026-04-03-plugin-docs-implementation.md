# Plugin Docs Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在 `docs-site` 中补齐服务端插件文档、侧边栏配置和静态资源，使 `server/plugin/*` 可直接访问。

**Architecture:** 直接迁移旧站 `intro/market/use/dev` 的正文结构到 `docs-site/content/docs/server/plugin`，并把旧图片同步到 `docs-site/public/images/plugin`。使用一个 Node 原生测试文件校验页面和资源是否齐备，避免后续内容回退或资源失链。

**Tech Stack:** MDX, Node.js test runner, React Router docs content tree

---

### Task 1: 先写回归测试

**Files:**
- Create: `docs-site/app/lib/plugin-docs.test.mjs`

- [ ] **Step 1: 写一个失败中的测试**
- [ ] **Step 2: 运行测试确认因为插件文档缺失而失败**

### Task 2: 迁移插件文档与导航

**Files:**
- Create: `docs-site/content/docs/server/plugin/index.mdx`
- Create: `docs-site/content/docs/server/plugin/market.mdx`
- Create: `docs-site/content/docs/server/plugin/use.mdx`
- Create: `docs-site/content/docs/server/plugin/dev.mdx`
- Create: `docs-site/content/docs/server/plugin/meta.json`
- Modify: `docs-site/content/docs/server/meta.json`

- [ ] **Step 1: 新建插件分组文档**
- [ ] **Step 2: 更新服务端导航把 `plugin` 插入侧边栏**

### Task 3: 同步图片资源

**Files:**
- Create: `docs-site/public/images/plugin/*`

- [ ] **Step 1: 复制旧站插件图片到新站 public 目录**
- [ ] **Step 2: 确认文档引用都改为 `/images/plugin/*`**

### Task 4: 验证

**Files:**
- Test: `docs-site/app/lib/plugin-docs.test.mjs`

- [ ] **Step 1: 运行 `node --test app/lib/plugin-docs.test.mjs`**
- [ ] **Step 2: 运行 `npm run types:check`**
- [ ] **Step 3: 如开发服务器在线，检查 `/server/plugin` 页面可访问**
