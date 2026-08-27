# WuKongIM v3 Documentation Navigation

> Generated from `lib/navigation.ts`. Run `bun run navigation:write` after changing the registry.

The Chinese and English sites share this information architecture. Replace `{lang}` with `zh` or `en`. Publication is controlled per route: published pages have complete bilingual MDX; planned pages stay visible in navigation but remain outside public indexes.

## 指南 / Guides

Route: `/{lang}/guide`

从认识 WuKongIM 到完成第一个业务集成。 / Learn WuKongIM and complete your first product integration.

- **产品概览 / Product Overview** `/{lang}/guide/product-overview` — 建立产品定位、能力边界和适用场景的整体认识。 / Understand the product position, capability boundaries, and use cases.

  - **WuKongIM 是什么 / What is WuKongIM?** `/{lang}/guide/product-overview/what-is-wukongim` — 介绍频道式消息模型、集群语义，以及它与网关和消息队列的区别。 / Introduces the channel model, cluster semantics, and how WuKongIM differs from gateways and queues.
  - **核心能力 / Core Capabilities** `/{lang}/guide/product-overview/capabilities` — 概览高并发消息、超大群、持久化、多设备、故障转移和扩容能力。 / Surveys high-throughput messaging, large groups, persistence, multi-device, failover, and scaling.
  - **适用场景 / Use Cases** `/{lang}/guide/product-overview/use-cases` — 说明聊天、推送、客服、直播、IoT、信令和 AI 通信等用途。 / Explains chat, push, support, live interaction, IoT, signaling, and AI communication use cases.

- **快速开始 / Quick Start** `/{lang}/guide/quick-start` — 沿最短路径启动集群、发送消息并验证结果。 / Follow the shortest path to start a cluster, send a message, and verify the result.

  - **环境准备 / Prerequisites** `/{lang}/guide/quick-start/prerequisites` — 列出 Git、Go、端口、本地目录和测试工具要求。 / Lists Git, Go, ports, local directories, and test tool requirements.
  - **启动单节点集群 / Start a Single-node Cluster** `/{lang}/guide/quick-start/single-node-cluster` — 启动单节点集群并验证就绪状态与 Manager。 / Starts a single-node cluster and verifies readiness and Manager access.
  - **发送第一条消息 / Send the First Message** `/{lang}/guide/quick-start/first-message` — 创建测试身份并完成一次最小消息收发。 / Creates test identities and completes a minimal message exchange.
  - **运行聊天演示 / Run the Chat Demo** `/{lang}/guide/quick-start/chat-demo` — 使用内置聊天演示验证两个测试用户之间的通信。 / Uses the embedded chat demo to verify communication between two test users.
  - **下一步 / Next Steps** `/{lang}/guide/quick-start/next-steps` — 按接入、部署、运维和参考需求引导后续阅读。 / Routes readers to integration, deployment, operations, and reference material.

- **核心概念 / Core Concepts** `/{lang}/guide/core-concepts` — 用消息、频道、用户、设备和会话理解 WuKongIM 如何组织即时通信。 / Explains how WuKongIM organizes communication through messages, channels, users, devices, and conversations.

  - **消息 / Message** `/{lang}/guide/core-concepts/messages` — 消息是什么、如何找到接收范围，以及发送成功、送达和已读的区别。 / Explains what a message is, how it finds recipients, and why sent, delivered, and read are different outcomes.
  - **频道 / Channel** `/{lang}/guide/core-concepts/channels` — 频道如何表示单聊、群聊等消息目标，并组织参与者和消息历史。 / Explains how a Channel represents direct and group targets and organizes participants and message history.
  - **用户 / User** `/{lang}/guide/core-concepts/users` — 用户如何通过稳定 UID 接入，以及 WuKongIM 与业务账号系统的职责边界。 / Explains how a stable UID enters WuKongIM and what remains the responsibility of the product account system.
  - **设备 / Device** `/{lang}/guide/core-concepts/devices` — 设备、连接与多端在线的区别，以及哪些状态会跨设备共享。 / Separates devices from connections and explains multi-endpoint presence and shared state.
  - **会话 / Conversation** `/{lang}/guide/core-concepts/conversations` — 会话如何把频道呈现为聊天列表，并管理未读和个人可见状态。 / Explains how a Conversation presents a Channel in a chat list with unread and personal visibility state.

- **集成指南 / Integration** `/{lang}/guide/integration` — 从业务系统视角完成 WuKongIM 接入。 / Integrates WuKongIM from the perspective of an existing product system.

  - **集成架构 / Integration Architecture** `/{lang}/guide/integration/architecture` — 说明业务服务、WuKongIM 服务端和客户端 SDK 的职责与数据流。 / Defines responsibilities and data flow across the business service, WuKongIM server, and client SDK.
  - **身份认证 / Authentication** `/{lang}/guide/integration/authentication` — 说明身份、Token、设备标识、连接鉴权和撤销策略。 / Covers identities, tokens, device identifiers, connection authentication, and revocation.
  - **消息收发 / Messaging** `/{lang}/guide/integration/messaging` — 串联连接、发送、接收、确认、重连和离线补偿。 / Connects sending, receiving, acknowledgements, reconnects, and offline recovery.
  - **Webhook / Webhooks** `/{lang}/guide/integration/webhooks` — 介绍事件回调、签名、重试、幂等和失败处理。 / Introduces event callbacks, signatures, retries, idempotency, and failure handling.
  - **插件扩展 / Plugin Extensions** `/{lang}/guide/integration/plugins` — 说明插件的适用问题、生命周期和安全边界。 / Explains suitable plugin use cases, lifecycle, and security boundaries.
  - **上线验收 / Integration Acceptance** `/{lang}/guide/integration/acceptance` — 把可执行兼容性证据与生产身份、网络、回调、容量和回滚门禁分开。 / Separates executable compatibility evidence from production identity, network, callback, capacity, and rollback gates.

- **场景教程 / Tutorials** `/{lang}/guide/tutorials` — 提供面向典型业务场景的端到端方案。 / Provides end-to-end solutions for representative product scenarios.

  - **单聊 / Direct Chat** `/{lang}/guide/tutorials/direct-chat` — 实现用户、单聊频道、消息、未读数和多设备同步。 / Implements users, direct channels, messages, unread counts, and multi-device sync.
  - **群聊与超大群 / Groups & Large Groups** `/{lang}/guide/tutorials/large-groups` — 实现群成员维护和群消息，并说明十万级成员约束。 / Implements group membership and messaging with constraints for 100,000-member groups.
  - **消息推送 / Message Push** `/{lang}/guide/tutorials/push` — 实现通知、系统消息、离线设备处理和失败恢复。 / Implements notifications, system messages, offline-device handling, and recovery.
  - **AI 与 IoT 通信 / AI & IoT Communication** `/{lang}/guide/tutorials/ai-and-iot` — 展示流式 AI 回复、设备上报和服务端指令。 / Demonstrates streaming AI replies, device telemetry, and server commands.

## 服务端 / Server

Route: `/{lang}/server`

部署、配置、运维和理解 WuKongIM 集群。 / Deploy, configure, operate, and understand a WuKongIM cluster.

- **部署 / Deployment** `/{lang}/server/deployment` — 选择并实施适合环境的服务端部署方式。 / Choose and implement the server deployment method appropriate for the environment.

  - **部署方式选择 / Choose a Deployment** `/{lang}/server/deployment/choosing` — 比较 Docker、Linux 二进制和 Kubernetes 的适用边界。 / Compares the suitability of Docker, Linux binaries, and Kubernetes.
  - **Docker 部署 / Docker** `/{lang}/server/deployment/docker` — 使用镜像部署单节点集群或多节点集群。 / Deploys single-node clusters or multi-node clusters from container images.
  - **Linux 部署 / Linux** `/{lang}/server/deployment/linux` — 使用二进制、配置文件和 systemd 运行服务。 / Runs the server with a binary, configuration file, and systemd.
  - **Kubernetes 部署（Beta） / Kubernetes (Beta)** `/{lang}/server/deployment/kubernetes` — 说明持久化、服务发现、资源规划和 Beta 边界。 / Covers persistence, discovery, resource planning, and Beta limitations.
  - **多节点集群 / Multi-node Cluster** `/{lang}/server/deployment/multi-node` — 规划并引导多节点集群完成启动和就绪检查。 / Plans and bootstraps a multi-node cluster through readiness verification.
  - **生产检查清单 / Production Checklist** `/{lang}/server/deployment/production-checklist` — 汇总资源、磁盘、安全、监控、备份和容量检查。 / Checks resources, disks, security, monitoring, backups, and capacity.

- **配置 / Configuration** `/{lang}/server/configuration` — 解释配置来源、覆盖规则和各领域配置。 / Explains configuration sources, override rules, and domain settings.

  - **节点与集群 / Nodes & Cluster** `/{lang}/server/configuration/cluster` — 节点身份、集群地址、Slot、副本和节点发现配置。 / Node identity, cluster addresses, slots, replicas, and discovery settings.
  - **网络与客户端接入 / Networking & Client Access** `/{lang}/server/configuration/networking` — TCP、WebSocket、HTTP、Manager 和节点通信监听配置。 / Listener settings for TCP, WebSocket, HTTP, Manager, and inter-node traffic.
  - **消息与存储 / Messages & Storage** `/{lang}/server/configuration/storage` — 消息保留、存储路径、队列、批处理和性能配置。 / Message retention, storage paths, queues, batching, and performance settings.
  - **安全与权限 / Security & Access** `/{lang}/server/configuration/security` — 认证、接口访问、Token、TLS 和敏感配置建议。 / Authentication, API access, tokens, TLS, and sensitive-setting guidance.
  - **日志与可观测性 / Logs & Observability** `/{lang}/server/configuration/observability` — 日志、指标、Prometheus、Top 和诊断接口配置。 / Logging, metrics, Prometheus, Top, and diagnostic endpoint settings.
  - **配置参考 / Configuration Reference** `/{lang}/server/configuration/reference` — 列出 TOML 键、类型、环境变量、脱敏边界和约束。 / Lists TOML keys, types, environment variables, redaction boundaries, and constraints.

- **运维 / Operations** `/{lang}/server/operations` — 管理、观察和安全变更生产集群。 / Manage, observe, and safely change production clusters.

  - **Manager 管理后台 / Manager** `/{lang}/server/operations/manager` — 介绍后台权限、集群状态、业务查询和运维操作。 / Introduces permissions, cluster state, business queries, and operations.
  - **健康检查与监控 / Health & Monitoring** `/{lang}/server/operations/health-and-monitoring` — 解释就绪状态、核心指标、Prometheus、Grafana 和告警。 / Explains readiness, key metrics, Prometheus, Grafana, and alerts.
  - **扩容与缩容 / Scaling** `/{lang}/server/operations/scaling` — 说明节点加入、平衡、安全缩容和 Leader 迁移。 / Covers node joins, balancing, safe scale-in, and leader transfer.
  - **备份与恢复 / Backup & Restore** `/{lang}/server/operations/backup-and-restore` — 说明备份计划、验证、恢复和灾难演练。 / Covers backup schedules, verification, restoration, and recovery drills.
  - **升级与迁移 / Upgrade & Migration** `/{lang}/server/operations/upgrade-and-migration` — 说明兼容性、滚动升级、回滚和 v2 到 v3 迁移。 / Covers compatibility, rolling upgrades, rollback, and v2-to-v3 migration.
  - **故障排查 / Troubleshooting** `/{lang}/server/operations/troubleshooting` — 按现象、指标、日志和诊断工具定位问题。 / Diagnoses issues through symptoms, metrics, logs, and diagnostic tools.

- **工具 / Tools** `/{lang}/server/tools` — 使用官方工具观察、验证和评估集群。 / Use official tools to inspect, verify, and evaluate clusters.

  - **wkcli / wkcli** `/{lang}/server/tools/wkcli` — 查看集群状态并执行受控运维操作。 / Inspects cluster state and performs controlled operations.
  - **wkdb / wkdb** `/{lang}/server/tools/wkdb` — 执行本地只读存储诊断和离线导入导出。 / Performs node-local read-only storage diagnostics and offline import/export.
  - **wkbench / wkbench** `/{lang}/server/tools/wkbench` — 执行黑盒压力测试、容量评估和回归验证。 / Runs black-box load tests, capacity evaluations, and regression checks.
  - **诊断能力 / Diagnostics** `/{lang}/server/tools/diagnostics` — 选择日志、指标、Top、pprof 和只读 Operations MCP。 / Selects among logs, metrics, Top, pprof, and the read-only Operations MCP.

- **架构 / Architecture** `/{lang}/server/architecture` — 从控制、元数据、消息和网络层理解系统。 / Understand the system through control, metadata, messaging, and network layers.

  - **Controller 控制层 / Controller Layer** `/{lang}/server/architecture/controller` — 解释集群元数据、节点管理、任务和一致性控制。 / Explains cluster metadata, node management, tasks, and consistency control.
  - **Slot 元数据层 / Slot Metadata Layer** `/{lang}/server/architecture/slots` — 解释默认 256 个 Hash Slot、归属、副本和 Leader 路由。 / Explains the default 256 hash slots, ownership, replicas, and leader routing.
  - **Channel 消息层 / Channel Messaging Layer** `/{lang}/server/architecture/channels` — 解释频道副本、消息日志、Leader 和故障切换。 / Explains channel replicas, message logs, leaders, and failover.
  - **Transport 网络层 / Transport Layer** `/{lang}/server/architecture/transport` — 解释节点连接、RPC、消息传输和背压。 / Explains node connections, RPC, message transport, and backpressure.
  - **消息发送链路 / Message Send Flow** `/{lang}/server/architecture/message-flow` — 跟踪消息进入、复制、持久化和投递的完整过程。 / Traces message ingress, replication, persistence, and delivery.
  - **用户连接路由 / User Connection Routing** `/{lang}/server/architecture/user-routing` — 解释在线状态、连接归属和跨节点投递。 / Explains presence, connection ownership, and cross-node delivery.

## SDK / SDK

Route: `/{lang}/sdk`

在不同客户端平台接入 WuKongIM。 / Integrate WuKongIM across supported client platforms.

- **选择 SDK / Choose an SDK** `/{lang}/sdk/choose-sdk` — 根据应用平台、框架和运行环境选择客户端 SDK。 / Choose a client SDK by platform, framework, and runtime.

- **版本与兼容性 / Versions & Compatibility** `/{lang}/sdk/compatibility` — 记录 v3 Beta 黄金路径的服务端 revision、SDK、Node、浏览器兼容目标与 receipt 状态。 / Records the server revision, SDK, Node, and browser compatibility target plus receipt status for the v3 Beta golden path.

- **公共指南 / Common Guides** `/{lang}/sdk/common-guides` — 以服务端可证明语义说明跨 SDK 接入行为，不替代平台 API 文档。 / Explains cross-SDK integration behavior through server-proven semantics without replacing platform API docs.

  - **身份与 Token / Identity & Token** `/{lang}/sdk/common-guides/identity-and-token` — 设计 UID、设备、Token 获取、轮换和失效边界。 / Designs UID, device, token acquisition, rotation, and invalidation boundaries.
  - **初始化与连接 / Initialization & Connection** `/{lang}/sdk/common-guides/initialization-and-connection` — 组织 SDK 实例、路由、连接状态、恢复门和退出生命周期。 / Organizes SDK instances, routing, connection states, recovery gates, and logout lifecycle.
  - **消息收发 / Messaging** `/{lang}/sdk/common-guides/messaging` — 解释发送、接收、确认、幂等、消息状态与瞬时分支。 / Explains send, receive, acknowledgements, idempotency, message state, and transient branches.
  - **自定义消息 / Custom Messages** `/{lang}/sdk/common-guides/custom-messages` — 设计应用 Payload 的版本、编码、兼容、降级和安全边界。 / Designs application payload versioning, encoding, compatibility, fallback, and security boundaries.
  - **会话与未读数 / Conversations & Unread Counts** `/{lang}/sdk/common-guides/conversations-and-unread` — 区分会话投影、最近消息、Badge floor、已读状态和拉取游标。 / Separates conversation projections, latest messages, badge floors, read state, and pull cursors.
  - **离线消息与推送 / Offline Messages & Push** `/{lang}/sdk/common-guides/offline-and-push` — 区分持久消息恢复、离线候选 Webhook 和厂商通知。 / Separates durable message recovery, offline-candidate webhooks, and provider notifications.
  - **多设备同步 / Multi-device Sync** `/{lang}/sdk/common-guides/multi-device` — 说明设备类别、冲突等级、多端连接、共享投影和产品设备状态。 / Explains device categories, conflict levels, concurrent sessions, shared projections, and product device state.
  - **重连与异常处理 / Reconnect & Errors** `/{lang}/sdk/common-guides/reconnect-and-errors` — 按网络、路由、连接、发送和同步阶段处理重连与错误。 / Handles reconnects and errors across network, route, connection, send, and synchronization phases.

- **Android / Android** `/{lang}/sdk/android` — Android SDK 的支持范围、系统要求和接入入口。 / Support scope, system requirements, and entry points for the Android SDK.

  - **安装与配置 / Installation** `/{lang}/sdk/android/installation` — Android SDK 的依赖、权限和构建配置。 / Dependencies, permissions, and build configuration for the Android SDK.
  - **快速接入 / Quickstart** `/{lang}/sdk/android/quickstart` — 在 Android 应用中完成首次连接和消息收发。 / Connect and exchange the first messages with the Android SDK.
  - **平台专属能力 / Platform Capabilities** `/{lang}/sdk/android/platform-capabilities` — Android 平台的生命周期、后台运行和推送等差异。 / Lifecycle, background execution, push, and other Android-specific behavior.
  - **API 参考 / API Reference** `/{lang}/sdk/android/api-reference` — Android SDK 的类、方法、事件、参数和错误定义。 / Classes, methods, events, parameters, and errors for the Android SDK.
  - **升级指南 / Upgrade Guide** `/{lang}/sdk/android/upgrade` — Android SDK 的破坏性变更、迁移步骤和发布记录。 / Breaking changes, migration steps, and release history for the Android SDK.

- **iOS / iOS** `/{lang}/sdk/ios` — iOS SDK 的支持范围、系统要求和接入入口。 / Support scope, system requirements, and entry points for the iOS SDK.

  - **安装与配置 / Installation** `/{lang}/sdk/ios/installation` — iOS SDK 的依赖、权限和构建配置。 / Dependencies, permissions, and build configuration for the iOS SDK.
  - **快速接入 / Quickstart** `/{lang}/sdk/ios/quickstart` — 在 iOS 应用中完成首次连接和消息收发。 / Connect and exchange the first messages with the iOS SDK.
  - **平台专属能力 / Platform Capabilities** `/{lang}/sdk/ios/platform-capabilities` — iOS 平台的生命周期、后台运行和推送等差异。 / Lifecycle, background execution, push, and other iOS-specific behavior.
  - **API 参考 / API Reference** `/{lang}/sdk/ios/api-reference` — iOS SDK 的类、方法、事件、参数和错误定义。 / Classes, methods, events, parameters, and errors for the iOS SDK.
  - **升级指南 / Upgrade Guide** `/{lang}/sdk/ios/upgrade` — iOS SDK 的破坏性变更、迁移步骤和发布记录。 / Breaking changes, migration steps, and release history for the iOS SDK.

- **JavaScript / Web / JavaScript / Web** `/{lang}/sdk/javascript` — 使用固定的 SDK 兼容目标完成浏览器安装、连接、双向消息、离线恢复、能力核对和验收报告；完整 API 与升级仍在规划中。 / Complete browser installation, connection, two-way messaging, offline recovery, capability review, and acceptance reporting with the pinned SDK compatibility target; complete API and upgrade material remain planned.

  - **安装与配置 / Installation** `/{lang}/sdk/javascript/installation` — 安装精确版本的 JavaScript SDK，并配置框架无关的 TypeScript 黄金样例。 / Install the exact JavaScript SDK version and configure the framework-neutral TypeScript golden sample.
  - **快速接入 / Quickstart** `/{lang}/sdk/javascript/quickstart` — 通过 localhost BFF 完成连接、双向消息、断开、重连和离线同步。 / Use the localhost BFF to connect, exchange messages, disconnect, reconnect, and recover offline messages.
  - **平台专属能力 / Platform Capabilities** `/{lang}/sdk/javascript/platform-capabilities` — 按真实 Chromium 场景区分场景覆盖能力、安全边界和未验证范围。 / Separates scenario-covered capabilities, security boundaries, and unverified scope through the real Chromium scenario.
  - **API 参考 / API Reference** `/{lang}/sdk/javascript/api-reference` — JavaScript / Web SDK 的类、方法、事件、参数和错误定义。 / Classes, methods, events, parameters, and errors for the JavaScript / Web SDK.
  - **升级指南 / Upgrade Guide** `/{lang}/sdk/javascript/upgrade` — JavaScript / Web SDK 的破坏性变更、迁移步骤和发布记录。 / Breaking changes, migration steps, and release history for the JavaScript / Web SDK.

- **Flutter / Flutter** `/{lang}/sdk/flutter` — Flutter SDK 的支持范围、系统要求和接入入口。 / Support scope, system requirements, and entry points for the Flutter SDK.

  - **安装与配置 / Installation** `/{lang}/sdk/flutter/installation` — Flutter SDK 的依赖、权限和构建配置。 / Dependencies, permissions, and build configuration for the Flutter SDK.
  - **快速接入 / Quickstart** `/{lang}/sdk/flutter/quickstart` — 在 Flutter 应用中完成首次连接和消息收发。 / Connect and exchange the first messages with the Flutter SDK.
  - **平台专属能力 / Platform Capabilities** `/{lang}/sdk/flutter/platform-capabilities` — Flutter 平台的生命周期、后台运行和推送等差异。 / Lifecycle, background execution, push, and other Flutter-specific behavior.
  - **API 参考 / API Reference** `/{lang}/sdk/flutter/api-reference` — Flutter SDK 的类、方法、事件、参数和错误定义。 / Classes, methods, events, parameters, and errors for the Flutter SDK.
  - **升级指南 / Upgrade Guide** `/{lang}/sdk/flutter/upgrade` — Flutter SDK 的破坏性变更、迁移步骤和发布记录。 / Breaking changes, migration steps, and release history for the Flutter SDK.

- **UniApp / UniApp** `/{lang}/sdk/uniapp` — UniApp SDK 的支持范围、平台差异和接入入口。 / Support scope, platform differences, and entry points for the UniApp SDK.

  - **安装与配置 / Installation** `/{lang}/sdk/uniapp/installation` — UniApp SDK 的依赖、权限和构建配置。 / Dependencies, permissions, and build configuration for the UniApp SDK.
  - **快速接入 / Quickstart** `/{lang}/sdk/uniapp/quickstart` — 在 UniApp 应用中完成首次连接和消息收发。 / Connect and exchange the first messages with the UniApp SDK.
  - **平台专属能力 / Platform Capabilities** `/{lang}/sdk/uniapp/platform-capabilities` — UniApp 平台的生命周期、后台运行和推送等差异。 / Lifecycle, background execution, push, and other UniApp-specific behavior.
  - **API 参考 / API Reference** `/{lang}/sdk/uniapp/api-reference` — UniApp SDK 的类、方法、事件、参数和错误定义。 / Classes, methods, events, parameters, and errors for the UniApp SDK.
  - **升级指南 / Upgrade Guide** `/{lang}/sdk/uniapp/upgrade` — UniApp SDK 的破坏性变更、迁移步骤和发布记录。 / Breaking changes, migration steps, and release history for the UniApp SDK.

- **HarmonyOS / HarmonyOS** `/{lang}/sdk/harmonyos` — HarmonyOS SDK 的支持范围、系统要求和接入入口。 / Support scope, system requirements, and entry points for the HarmonyOS SDK.

  - **安装与配置 / Installation** `/{lang}/sdk/harmonyos/installation` — HarmonyOS SDK 的依赖、权限和构建配置。 / Dependencies, permissions, and build configuration for the HarmonyOS SDK.
  - **快速接入 / Quickstart** `/{lang}/sdk/harmonyos/quickstart` — 在 HarmonyOS 应用中完成首次连接和消息收发。 / Connect and exchange the first messages with the HarmonyOS SDK.
  - **平台专属能力 / Platform Capabilities** `/{lang}/sdk/harmonyos/platform-capabilities` — HarmonyOS 平台的生命周期、后台运行和推送等差异。 / Lifecycle, background execution, push, and other HarmonyOS-specific behavior.
  - **API 参考 / API Reference** `/{lang}/sdk/harmonyos/api-reference` — HarmonyOS SDK 的类、方法、事件、参数和错误定义。 / Classes, methods, events, parameters, and errors for the HarmonyOS SDK.
  - **升级指南 / Upgrade Guide** `/{lang}/sdk/harmonyos/upgrade` — HarmonyOS SDK 的破坏性变更、迁移步骤和发布记录。 / Breaking changes, migration steps, and release history for the HarmonyOS SDK.

## API 与协议 / API & Protocols

Route: `/{lang}/api`

查阅 HTTP API、Webhook 和客户端协议。 / Reference HTTP APIs, webhooks, and client protocols.

- **通用约定 / Conventions** `/{lang}/api/conventions` — 定义 v3 Beta 黄金路径子集使用的 Base URL、JSON、标识和兼容响应结构。 / Defines base URLs, JSON, identifiers, and compatible response envelopes used by the v3 Beta golden-path subset.

- **认证与安全 / Authentication & Security** `/{lang}/api/authentication` — 说明开发身份、受信 BFF，以及默认组合尚未提供的生产鉴权保证。 / Explains development identities, the trusted BFF, and the production authentication guarantees absent from the default composition.

- **版本与兼容性 / Versions & Compatibility** `/{lang}/api/compatibility` — 记录黄金路径 HTTP 子集、客户端协议与服务端快照的兼容目标和 receipt 状态。 / Records the compatibility target and receipt status for the golden-path HTTP subset, client protocol, and server snapshot.

- **产品 HTTP API（Beta 子集） / Product HTTP API (Beta subset)** `/{lang}/api/product-http` — 仅发布 JavaScript 黄金路径声明使用的受信服务端接口。 / Publishes only the trusted server-side endpoints declared by the JavaScript golden path.

  - **用户 / Users** `/{lang}/api/product-http/users` — 记录黄金路径用于开发身份准备的 `/user/token` 合同与安全边界。 / Documents the `/user/token` contract and security boundary used to prepare golden-path development identities.
  - **频道 / Channels** `/{lang}/api/product-http/channels` — 频道、订阅者、黑名单、白名单和临时频道接口。 / Channel, subscriber, blacklist, whitelist, and temporary-channel endpoints.
  - **消息 / Messages** `/{lang}/api/product-http/messages` — 记录黄金路径用于离线恢复的 `/channel/messagesync` 合同。 / Documents the `/channel/messagesync` contract used for golden-path offline recovery.
  - **会话 / Conversations** `/{lang}/api/product-http/conversations` — 会话列表、同步、未读数和删除接口。 / Conversation list, sync, unread-count, and deletion endpoints.
  - **路由发现 / Route Discovery** `/{lang}/api/product-http/routing` — 获取服务端配置的 TCP 和 WebSocket 客户端入口地址。 / Discovers the configured TCP and WebSocket client-ingress addresses.
  - **错误响应 / Error Responses** `/{lang}/api/product-http/errors` — 解释 HTTP 状态、业务状态和 Reason Code 的关系。 / Relates HTTP status, business status, and protocol reason codes.

- **运维 HTTP API / Operations HTTP API** `/{lang}/api/operations-http` — 发布稳定且受支持的运维接口。 / Publishes stable and supported operations endpoints.

  - **健康与就绪 / Health & Readiness** `/{lang}/api/operations-http/health-and-readiness` — 说明健康检查、就绪检查和负载均衡使用方式。 / Covers health checks, readiness checks, and load-balancer usage.
  - **Metrics / Metrics** `/{lang}/api/operations-http/metrics` — 说明 Prometheus 指标入口、访问控制和抓取建议。 / Explains the Prometheus endpoint, access control, and scrape guidance.
  - **只读运维接口 / Read-only Operations** `/{lang}/api/operations-http/read-only` — 记录正式支持的节点状态和资源快照查询。 / Documents supported node-state and resource-snapshot queries.
  - **接口稳定性 / API Stability** `/{lang}/api/operations-http/stability` — 标明稳定、实验性和条件启用的运维接口。 / Marks stable, experimental, and conditionally enabled operations endpoints.

- **Webhook / Webhooks** `/{lang}/api/webhooks` — 说明服务端向业务系统投递事件的契约。 / Defines how the server delivers events to business systems.

  - **事件类型 / Event Types** `/{lang}/api/webhooks/events` — 列出消息、在线状态和其他受支持事件。 / Lists messages, presence, and other supported events.
  - **请求结构 / Payloads** `/{lang}/api/webhooks/payloads` — 定义通用信封、事件负载和示例。 / Defines the common envelope, event payloads, and examples.
  - **安全与可靠性 / Security & Reliability** `/{lang}/api/webhooks/reliability-and-security` — 说明签名、重试、顺序、幂等和失败处理。 / Covers signatures, retries, ordering, idempotency, and failure handling.

- **客户端协议 / Client Protocols** `/{lang}/api/client-protocols` — 说明 TCP 二进制协议与 WebSocket JSON-RPC。 / Documents the TCP binary protocol and WebSocket JSON-RPC.

  - **连接生命周期 / Connection Lifecycle** `/{lang}/api/client-protocols/connection-lifecycle` — 说明 Connect、认证、心跳、断开和重连。 / Covers connect, authentication, heartbeat, disconnect, and reconnect.
  - **TCP 二进制协议 / TCP Binary Protocol** `/{lang}/api/client-protocols/tcp-binary` — 定义帧格式、编码、标志位和包边界。 / Defines frame format, encoding, flags, and packet boundaries.
  - **WebSocket JSON-RPC / WebSocket JSON-RPC** `/{lang}/api/client-protocols/json-rpc` — 定义方法、参数、结果、通知和请求关联。 / Defines methods, parameters, results, notifications, and request correlation.
  - **数据包类型 / Packet Types** `/{lang}/api/client-protocols/packet-types` — 说明 Connect、Send、Recv、Ack 和 Ping/Pong 字段。 / Documents Connect, Send, Recv, Ack, and Ping/Pong fields.
  - **加密与安全 / Encryption & Security** `/{lang}/api/client-protocols/encryption` — 说明握手密钥、负载保护和协议安全约束。 / Covers handshake keys, payload protection, and protocol security constraints.

- **公共数据字典 / Shared Dictionaries** `/{lang}/api/dictionaries` — 发布源码校准的 Channel、设备、消息标志与 Reason Code 字典。 / Publishes source-aligned Channel, device, message-flag, and Reason Code dictionaries.

  - **Channel Type / Channel Type** `/{lang}/api/dictionaries/channel-types` — 列出当前 1–12 Channel Type，并标注基础、专用和旧类型边界。 / Lists current Channel Types 1–12 with baseline, specialized, and legacy boundaries.
  - **Device Flag / Device Flag** `/{lang}/api/dictionaries/device-flags` — 列出 APP、WEB、PC、SYSTEM 与 Device Level 冲突策略。 / Lists APP, WEB, PC, SYSTEM, and Device Level conflict policies.
  - **Message Flags / Message Flags** `/{lang}/api/dictionaries/message-flags` — 列出固定 Header 与 Setting 位，并解释持久化、红点、命令、回执和流语义。 / Lists fixed-header and Setting bits for persistence, red dots, commands, receipts, and streams.
  - **Reason Code / Reason Code** `/{lang}/api/dictionaries/reason-codes` — 完整列出当前 0–29 协议枚举并标注使用阶段、重试和可达性。 / Lists the complete current 0–29 protocol enum with stage, retry, and reachability guidance.

- **规范下载 / Specifications** `/{lang}/api/specifications` — 提供校准后、可机器读取的接口与协议规范。 / Provides aligned, machine-readable API and protocol specifications.

  - **OpenAPI / OpenAPI** `/{lang}/api/specifications/openapi` — 在线浏览并下载校准后的 v3 HTTP API 规范。 / Browse and download the aligned v3 HTTP API specification.
  - **JSON-RPC Schema / JSON-RPC Schema** `/{lang}/api/specifications/json-rpc-schema` — 浏览并下载 WebSocket JSON-RPC Schema。 / Browse and download the WebSocket JSON-RPC schema.
  - **协议变更记录 / Protocol Changelog** `/{lang}/api/specifications/protocol-changelog` — 记录破坏性变化、兼容范围和迁移方式。 / Records breaking changes, compatibility ranges, and migrations.
