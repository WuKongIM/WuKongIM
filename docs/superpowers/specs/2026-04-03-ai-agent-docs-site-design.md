# AI Agent Docs-Site Design

## Goal

把 `docs/design/Message_Event_Stream_Design.md` 和 `docs/design/Event_Merge_Rules.md` 整理成适合公开阅读的 docs-site 文档，并把该能力从通用服务端消息接口中独立出来，明确其主要面向 AI Agent 场景。

## Scope

- 在 `docs-site` 中新增顶级 `AI Agent` 栏目。
- 将原始设计稿拆成面向阅读的两篇文档：
  - 消息事件流
  - 事件合并规则
- 补充一篇面向工程团队的架构落地建议页面。
- 补充一篇按完整调用链展开的端到端 walkthrough 页面。
- 保留与服务端消息接口的关联关系，但不把主内容放回 `server/message` 目录。

## Information Architecture

新增目录：

- `docs-site/content/docs/ai-agent/meta.json`
- `docs-site/content/docs/ai-agent/index.mdx`
- `docs-site/content/docs/ai-agent/message-event-stream.mdx`
- `docs-site/content/docs/ai-agent/event-merge-rules.mdx`
- `docs-site/content/docs/ai-agent/architecture-guidance.mdx`
- `docs-site/content/docs/ai-agent/end-to-end-walkthrough.mdx`

同时更新：

- `docs-site/content/docs/meta.json`
- `docs-site/content/docs/index.mdx`
- `docs-site/content/docs/server/message/index.mdx`
- `docs-site/app/lib/icons.ts`

## Content Strategy

- `AI Agent` 首页负责解释这个栏目解决什么问题，以及推荐阅读顺序。
- `消息事件流` 页面负责解释目标、模型、事件类型、接口设计、`lane_id` 角色和典型场景。
- `事件合并规则` 页面负责解释排序、幂等、状态机、`payload.kind` 合并策略、离线接口映射和伪代码。
- `架构落地建议` 页面负责解释推荐分层、推荐读写路径、客户端接入顺序、灰度策略和常见误区。
- `端到端 walkthrough` 页面负责把锚点消息、事件追加、在线展示、离线恢复和事件补拉串成一条完整链路。
- 文风从“设计提案”改为“文档说明”，但保留关键约束和示例。

## Navigation Strategy

- `AI Agent` 作为顶级栏目加入根目录导航。
- 首页增加 `AI Agent` 入口卡片。
- `server/message` 页面增加一个交叉链接，引导需要做 AI 流式输出和工具事件的读者进入 `AI Agent` 栏目。

## Verification

- 运行 `cd docs-site && npm run types:check`
- 运行 `cd docs-site && npm run build`

## Notes

- 对话中已确认将该能力独立成顶级 `AI Agent` 栏目，因此这里不再单独拆回 `server/message`。
- 原技能流程要求的独立审阅子代理在当前会话约束下不适用，本次采用本地自检替代。
