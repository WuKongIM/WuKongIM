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

在不同客户端平台接入 WuKongIM。 / Integrate WuKongIM across client platforms.

- **WuKongIMSDK / WuKongIMSDK** `/{lang}/sdk/wukongim` — 完整版客户端 SDK：管理连接、消息、本地会话、未读数与离线数据。 / Full client SDKs that manage connections, messages, local conversations, unread counts, and offline data.
  - **核心概念 / Core Concepts** `/{lang}/sdk/wukongim/concepts` — 用简单语言理解 UID、Token、频道、消息状态、会话和 Provider。 / Understand UIDs, tokens, Channels, message states, Conversations, and providers in plain language.
  - **Android / Android** `/{lang}/sdk/android` — 从快速开始到常用管理器，使用 WuKongIMAndroidSDK 1.5.5 完成清晰、可查找的 Android 接入。 / Integrate WuKongIMAndroidSDK 1.5.5 on Android through a clear quickstart and task-based manager guides.
    - **快速开始 / Quickstart** `/{lang}/sdk/android/quickstart` — 安装 WuKongIMAndroidSDK 1.5.5，连接一个用户，并用 Java 完成第一条在线文本消息。 / Install WuKongIMAndroidSDK 1.5.5, connect one user, and exchange the first online text message in Java.
    - **连接管理 / Connection** `/{lang}/sdk/android/connection` — 配置 UID、Token 与连接地址，监听连接状态，并正确处理断开和退出。 / Configure the UID, token, and endpoint; observe connection state; and disconnect or log out correctly.
    - **消息管理 / Messages** `/{lang}/sdk/android/messages` — 发送、接收和查询消息，并理解发送中、发送成功与发送失败。 / Send, receive, and query messages while understanding sending, success, and failure states.
    - **会话管理 / Conversations** `/{lang}/sdk/android/conversations` — 读取聊天列表、监听会话变化，并管理未读数。 / Read the chat list, observe conversation changes, and manage unread counts.
    - **频道管理 / Channels** `/{lang}/sdk/android/channels` — 获取单聊或群聊资料，监听资料变化，并连接业务数据源。 / Load direct or group chat profiles, observe changes, and connect product data providers.
    - **高级功能 / Advanced** `/{lang}/sdk/android/advanced` — 按当前平台确实提供的 API 学习自定义消息、媒体和离线能力。 / Use only the custom-content, media, and offline APIs actually provided by this platform.
      - **自定义消息 / Custom Messages** `/{lang}/sdk/android/advanced/custom-messages` — 定义、注册并发送自己的业务消息类型。 / Define, register, and send product-specific message content.
      - **媒体与历史消息 / Media & History** `/{lang}/sdk/android/advanced/media-and-history` — 接入媒体上传，并在本地消息不足时补齐历史消息。 / Connect media upload and fill message history when local data is incomplete.
    - **API 参考 / API Reference** `/{lang}/sdk/android/api-reference` — 按管理器查找常用入口、监听器、Provider、模型和状态。 / Find common manager entry points, listeners, providers, models, and states.
  - **iOS / iOS** `/{lang}/sdk/ios` — 从快速开始到常用管理器，使用 WuKongIMSDK 1.1.1 完成清晰、可查找的 iOS 接入。 / Integrate WuKongIMSDK 1.1.1 on iOS through a clear quickstart and task-based manager guides.
    - **快速开始 / Quickstart** `/{lang}/sdk/ios/quickstart` — 安装 WuKongIMSDK 1.1.1，连接一个用户，并用 Objective-C 完成第一条在线文本消息。 / Install WuKongIMSDK 1.1.1, connect one user, and exchange the first online text message in Objective-C.
    - **连接管理 / Connection** `/{lang}/sdk/ios/connection` — 配置 UID、Token 与连接地址，监听连接状态，并正确处理断开和退出。 / Configure the UID, token, and endpoint; observe connection state; and disconnect or log out correctly.
    - **消息管理 / Messages** `/{lang}/sdk/ios/messages` — 发送、接收和查询消息，并理解发送中、发送成功与发送失败。 / Send, receive, and query messages while understanding sending, success, and failure states.
    - **会话管理 / Conversations** `/{lang}/sdk/ios/conversations` — 读取聊天列表、监听会话变化，并管理未读数。 / Read the chat list, observe conversation changes, and manage unread counts.
    - **频道管理 / Channels** `/{lang}/sdk/ios/channels` — 获取单聊或群聊资料，监听资料变化，并连接业务数据源。 / Load direct or group chat profiles, observe changes, and connect product data providers.
    - **高级功能 / Advanced** `/{lang}/sdk/ios/advanced` — 按当前平台确实提供的 API 学习自定义消息、媒体和离线能力。 / Use only the custom-content, media, and offline APIs actually provided by this platform.
      - **自定义消息 / Custom Messages** `/{lang}/sdk/ios/advanced/custom-messages` — 继承消息正文、注册类型并发送自己的业务消息。 / Subclass message content, register its type, and send product-specific messages.
      - **媒体与历史消息 / Media & History** `/{lang}/sdk/ios/advanced/media-and-history` — 接入图片和语音上传，并在本地消息不足时同步历史消息。 / Connect image and voice upload and synchronize history when local messages are incomplete.
    - **API 参考 / API Reference** `/{lang}/sdk/ios/api-reference` — 按管理器查找常用入口、监听器、Provider、模型和状态。 / Find common manager entry points, listeners, providers, models, and states.
  - **JavaScript / Web / JavaScript / Web** `/{lang}/sdk/javascript` — 从快速开始到常用管理器，使用 wukongimjssdk 1.3.5 完成清晰、可查找的 JavaScript / Web 接入。 / Integrate wukongimjssdk 1.3.5 on JavaScript / Web through a clear quickstart and task-based manager guides.
    - **快速开始 / Quickstart** `/{lang}/sdk/javascript/quickstart` — 安装 wukongimjssdk 1.3.5，连接一个用户，并用 TypeScript 完成第一条在线文本消息。 / Install wukongimjssdk 1.3.5, connect one user, and exchange the first online text message in TypeScript.
    - **连接管理 / Connection** `/{lang}/sdk/javascript/connection` — 配置 UID、Token 与连接地址，监听连接状态，并正确处理断开和退出。 / Configure the UID, token, and endpoint; observe connection state; and disconnect or log out correctly.
    - **消息管理 / Messages** `/{lang}/sdk/javascript/messages` — 发送、接收和查询消息，并理解发送中、发送成功与发送失败。 / Send, receive, and query messages while understanding sending, success, and failure states.
    - **会话管理 / Conversations** `/{lang}/sdk/javascript/conversations` — 读取聊天列表、监听会话变化，并管理未读数。 / Read the chat list, observe conversation changes, and manage unread counts.
    - **频道管理 / Channels** `/{lang}/sdk/javascript/channels` — 获取单聊或群聊资料，监听资料变化，并连接业务数据源。 / Load direct or group chat profiles, observe changes, and connect product data providers.
    - **高级功能 / Advanced** `/{lang}/sdk/javascript/advanced` — 按当前平台确实提供的 API 学习自定义消息、媒体和离线能力。 / Use only the custom-content, media, and offline APIs actually provided by this platform.
      - **自定义消息 / Custom Messages** `/{lang}/sdk/javascript/advanced/custom-messages` — 定义、注册并发送浏览器业务需要的消息正文。 / Define, register, and send message content required by the browser product.
      - **离线恢复与 UniApp 迁移 / Offline Recovery & UniApp Migration** `/{lang}/sdk/javascript/advanced/offline-and-uniapp` — 接入离线消息同步，并把旧 UniApp SDK 迁移到 JavaScript SDK。 / Connect offline synchronization and migrate the retired UniApp SDK to the JavaScript SDK.
    - **API 参考 / API Reference** `/{lang}/sdk/javascript/api-reference` — 按管理器查找常用入口、监听器、Provider、模型和状态。 / Find common manager entry points, listeners, providers, models, and states.
  - **Flutter / Flutter** `/{lang}/sdk/flutter` — 从快速开始到常用管理器，使用 wukongimfluttersdk 1.7.9 完成清晰、可查找的 Flutter 接入。 / Integrate wukongimfluttersdk 1.7.9 on Flutter through a clear quickstart and task-based manager guides.
    - **快速开始 / Quickstart** `/{lang}/sdk/flutter/quickstart` — 安装 wukongimfluttersdk 1.7.9，连接一个用户，并用 Dart 完成第一条在线文本消息。 / Install wukongimfluttersdk 1.7.9, connect one user, and exchange the first online text message in Dart.
    - **连接管理 / Connection** `/{lang}/sdk/flutter/connection` — 配置 UID、Token 与连接地址，监听连接状态，并正确处理断开和退出。 / Configure the UID, token, and endpoint; observe connection state; and disconnect or log out correctly.
    - **消息管理 / Messages** `/{lang}/sdk/flutter/messages` — 发送、接收和查询消息，并理解发送中、发送成功与发送失败。 / Send, receive, and query messages while understanding sending, success, and failure states.
    - **会话管理 / Conversations** `/{lang}/sdk/flutter/conversations` — 读取聊天列表、监听会话变化，并管理未读数。 / Read the chat list, observe conversation changes, and manage unread counts.
    - **频道管理 / Channels** `/{lang}/sdk/flutter/channels` — 获取单聊或群聊资料，监听资料变化，并连接业务数据源。 / Load direct or group chat profiles, observe changes, and connect product data providers.
    - **高级功能 / Advanced** `/{lang}/sdk/flutter/advanced` — 按当前平台确实提供的 API 学习自定义消息、媒体和离线能力。 / Use only the custom-content, media, and offline APIs actually provided by this platform.
      - **自定义消息 / Custom Messages** `/{lang}/sdk/flutter/advanced/custom-messages` — 定义、注册并发送 Flutter 业务消息类型。 / Define, register, and send product-specific Flutter message content.
      - **媒体与历史消息 / Media & History** `/{lang}/sdk/flutter/advanced/media-and-history` — 接入媒体上传，并在本地消息不足时补齐历史消息。 / Connect media upload and fill message history when local data is incomplete.
    - **API 参考 / API Reference** `/{lang}/sdk/flutter/api-reference` — 按管理器查找常用入口、监听器、Provider、模型和状态。 / Find common manager entry points, listeners, providers, models, and states.
  - **HarmonyOS / HarmonyOS** `/{lang}/sdk/harmonyos` — 从快速开始到常用管理器，使用 @wukong/wkim 1.1.7 完成清晰、可查找的 HarmonyOS 接入。 / Integrate @wukong/wkim 1.1.7 on HarmonyOS through a clear quickstart and task-based manager guides.
    - **快速开始 / Quickstart** `/{lang}/sdk/harmonyos/quickstart` — 安装 @wukong/wkim 1.1.7，连接一个用户，并用 ArkTS 完成第一条在线文本消息。 / Install @wukong/wkim 1.1.7, connect one user, and exchange the first online text message in ArkTS.
    - **连接管理 / Connection** `/{lang}/sdk/harmonyos/connection` — 配置 UID、Token 与连接地址，监听连接状态，并正确处理断开和退出。 / Configure the UID, token, and endpoint; observe connection state; and disconnect or log out correctly.
    - **消息管理 / Messages** `/{lang}/sdk/harmonyos/messages` — 发送、接收和查询消息，并理解发送中、发送成功与发送失败。 / Send, receive, and query messages while understanding sending, success, and failure states.
    - **会话管理 / Conversations** `/{lang}/sdk/harmonyos/conversations` — 读取聊天列表、监听会话变化，并管理未读数。 / Read the chat list, observe conversation changes, and manage unread counts.
    - **频道管理 / Channels** `/{lang}/sdk/harmonyos/channels` — 获取单聊或群聊资料，监听资料变化，并连接业务数据源。 / Load direct or group chat profiles, observe changes, and connect product data providers.
    - **高级功能 / Advanced** `/{lang}/sdk/harmonyos/advanced` — 按当前平台确实提供的 API 学习自定义消息、媒体和离线能力。 / Use only the custom-content, media, and offline APIs actually provided by this platform.
      - **自定义消息 / Custom Messages** `/{lang}/sdk/harmonyos/advanced/custom-messages` — 定义、注册并发送 HarmonyOS 业务消息类型。 / Define, register, and send product-specific HarmonyOS message content.
      - **媒体与历史消息 / Media & History** `/{lang}/sdk/harmonyos/advanced/media-and-history` — 接入图片或语音消息，并在本地数据不足时补齐历史消息。 / Connect image or voice messages and fill history when local data is incomplete.
    - **API 参考 / API Reference** `/{lang}/sdk/harmonyos/api-reference` — 按管理器查找常用入口、监听器、Provider、模型和状态。 / Find common manager entry points, listeners, providers, models, and states.
  - **升级 SDK / Upgrade SDKs** `/{lang}/sdk/wukongim/upgrade` — 用一套简洁流程升级依赖、检查数据兼容并准备回滚。 / Upgrade dependencies, check data compatibility, and prepare rollback with one concise workflow.
- **WuKongEasySDK / WuKongEasySDK** `/{lang}/sdk/easy` — 固定版本的 iOS、Android、Flutter 与 Web 教程已发布，并具有 JSON-RPC CONNECT 与在线双向收发的服务端线协议凭据；平台构建、设备运行和生产 Token 校验仍需单独验收。 / Pinned iOS, Android, Flutter, and Web tutorials are published with a server-side wire receipt for JSON-RPC CONNECT and online bidirectional messaging; platform builds, device runs, and production token verification still require separate acceptance.
  - **5 分钟集成 iOS / 5-minute iOS integration** `/{lang}/sdk/easy/ios/getting-started` — 已发布 v1.0.3 源码教程；JSON-RPC CONNECT 与在线双向收发已有服务端线协议验证，iOS 构建和设备运行仍需自验。 / Published for v1.0.3; JSON-RPC CONNECT and online bidirectional messaging have a server-side wire receipt, while iOS build and device execution remain deployment-owned.
  - **5 分钟集成 Android / 5-minute Android integration** `/{lang}/sdk/easy/android/getting-started` — 已发布 v1.0.3 源码教程；JSON-RPC CONNECT 与在线双向收发已有服务端线协议验证，Android 构建和设备运行仍需自验。 / Published for v1.0.3; JSON-RPC CONNECT and online bidirectional messaging have a server-side wire receipt, while Android build and device execution remain deployment-owned.
  - **5 分钟集成 Flutter / 5-minute Flutter integration** `/{lang}/sdk/easy/flutter/getting-started` — 已发布 v1.0.4 源码教程；JSON-RPC CONNECT 与在线双向收发已有服务端线协议验证，Flutter 构建和设备运行仍需自验。 / Published for v1.0.4; JSON-RPC CONNECT and online bidirectional messaging have a server-side wire receipt, while Flutter build and device execution remain deployment-owned.
  - **5 分钟集成 Web / 5-minute Web integration** `/{lang}/sdk/easy/javascript/getting-started` — 已发布 v2.0.2 源码教程；JSON-RPC CONNECT 与在线双向收发已有服务端线协议验证，浏览器产物运行仍需自验。 / Published for v2.0.2; JSON-RPC CONNECT and online bidirectional messaging have a server-side wire receipt, while browser artifact execution remains deployment-owned.

## API 与协议 / API & Protocols

Route: `/{lang}/api`

查阅源码校准的 HTTP、Webhook、客户端协议与私有接口边界。 / Reference source-aligned HTTP, webhook, client-protocol, and private-interface boundaries.

- **通用约定 / Conventions** `/{lang}/api/conventions` — Product HTTP 的地址、格式、标识和重试规则。 / Product HTTP addressing, formats, identifiers, and retry rules.

- **认证与安全 / Authentication & Security** `/{lang}/api/authentication` — Product HTTP 与 Gateway 的鉴权边界。 / Authentication boundaries for Product HTTP and Gateway.

- **版本与兼容性 / Versions & Compatibility** `/{lang}/api/compatibility` — 查看构建快照和接口覆盖状态。 / View the build snapshot and API coverage status.

- **接口清单与信任边界 / Interface Inventory & Trust Boundaries** `/{lang}/api/interface-inventory` — 盘点 Manager、Node transport、MCP、插件与 Agent 私有合同。 / Inventories Manager, node transport, MCP, plugin, and agent-private contracts.

- **Product HTTP API / Product HTTP API** `/{lang}/api/product-http` — 浏览当前源码注册的全部 41 条 Product HTTP 操作。 / Browse all 41 Product HTTP operations registered by the current source.
  - **用户 / Users** `/{lang}/api/product-http/users` — 设备 Token、在线状态与系统身份。 / Device tokens, presence, and system identities.
    - **创建或更新设备 Token / Create or update a device token** **POST** `/{lang}/api/product-http/users/setQuickstartUserToken` — 创建缺失的 UID 元数据并更新一个设备 Token；当前产品装配未启用 CONNECT Token 鉴权。 / Creates missing UID metadata and upserts one device token; the current product composition does not enable CONNECT token authentication.
    - **退出用户设备 / Clear a user device token** **POST** `/{lang}/api/product-http/users/quitUserDevice` — 清空一个已存设备 Token 并调度 owner-local Session 关闭；device_flag=-1 选择 APP、Web 与 PC。 / Clears one stored device token and schedules owner-local Session closure; device_flag -1 selects APP, Web, and PC.
    - **查询用户在线路由 / List active user routes** **POST** `/{lang}/api/product-http/users/listUserOnlineStatus` — 每条活跃权威路由返回一行；空 UID 数组返回旧式 status 对象而不是数组。 / Returns one row per active authority route; an empty UID array returns the legacy status object instead of an array.
    - **添加系统 UID / Add system UIDs** **POST** `/{lang}/api/product-http/users/addSystemUIDs` — 持久化系统身份并加入当前进程缓存。 / Persists system identities and adds them to the current process cache.
    - **移除系统 UID / Remove system UIDs** **POST** `/{lang}/api/product-http/users/removeSystemUIDs` — 移除持久化系统身份与当前进程缓存项。 / Removes persisted system identities and current-process cache entries.
    - **列出全部系统 UID / List all system UIDs** **GET** `/{lang}/api/product-http/users/listSystemUIDs` — 把完整持久化系统 UID 集合聚合为一个无界响应。 / Aggregates the complete persisted system UID set into one unbounded response.
    - **添加节点本地系统 UID 缓存 / Add node-local system UID cache entries** **POST** `/{lang}/api/product-http/users/addSystemUIDsToLocalCache` — 只修改当前进程缓存，不持久化也不复制该变更。 / Mutates only the current process cache and does not persist or replicate the change.
    - **移除节点本地系统 UID 缓存 / Remove node-local system UID cache entries** **POST** `/{lang}/api/product-http/users/removeSystemUIDsFromLocalCache` — 只修改当前进程缓存，不改变持久化系统身份。 / Mutates only the current process cache and does not change durable system identities.
  - **路由发现 / Route Discovery** `/{lang}/api/product-http/routing` — 客户端 Gateway 公网或内网地址。 / Public or intranet client Gateway addresses.
    - **解析 Gateway 地址 / Resolve Gateway addresses** **GET** `/{lang}/api/product-http/routing/getQuickstartGatewayRoute` — 返回已配置的公网或内网地址；节点别名按 node_id、nodeId、nodeID 顺序读取。 / Returns the configured public or intranet addresses; node aliases are checked in node_id, nodeId, nodeID order.
    - **批量解析 UID 地址组 / Resolve one address group for UIDs** **POST** `/{lang}/api/product-http/routing/getGatewayRoutesBatch` — 在所选地址组中回显无上限 UID 数组；该接口仅为兼容保留。 / Echoes an unbounded UID array inside the selected address group; retained for compatibility.
  - **消息 / Messages** `/{lang}/api/product-http/messages` — 消息恢复、事件与命令消息兼容接口。 / Message recovery, events, and command-message compatibility.
    - **追加消息事件 / Append a message event** **POST** `/{lang}/api/product-http/messages/appendMessageEvent` — 校验并应用一次消息级事件投影；不支持非 null headers。 / Validates and applies one message-scoped event projection; non-null headers are unsupported.
    - **同步命令消息 / Synchronize command messages** **POST** `/{lang}/api/product-http/messages/syncCommandMessages` — 返回最新持久 CMD generation；message_seq 仅兼容接收但会被忽略。 / Returns the latest durable CMD generation; message_seq is accepted but ignored.
    - **确认最新命令消息同步 / Acknowledge the latest command sync** **POST** `/{lang}/api/product-http/messages/ackCommandMessages` — 要求 last_message_seq 为正数，但确认的是服务端最近记录的 generation，而不是该输入值。 / Requires a positive last_message_seq but acknowledges the server's latest recorded generation, not that supplied value.
    - **绑定命令 Channel 发现 / Bind command-channel discovery** **POST** `/{lang}/api/product-http/messages/bindCommandChannel` — 从当前命令 Channel tail 之后开始持久离线发现。 / Starts durable offline discovery after the current command-channel tail.
    - **解绑命令 Channel 发现 / Unbind command-channel discovery** **POST** `/{lang}/api/product-http/messages/unbindCommandChannel` — 为持久发现写入 tombstone，但不删除命令消息。 / Tombstones durable discovery without deleting command messages.
    - **同步一个 Channel 的已提交消息 / Synchronize one Channel's committed messages** **POST** `/{lang}/api/product-http/messages/syncQuickstartChannelMessages` — 校验成员可见性并返回升序分页；limit 默认 100，最大 10000。 / Checks membership visibility and returns an ascending page; limit is 100 by default and capped at 10000.
    - **同步最多 200 个 Channel / Synchronize up to 200 Channels** **POST** `/{lang}/api/product-http/messages/syncChannelMessagesBatch` — 批量读取前校验全部成员关系；单项失败嵌入 HTTP 200 响应。 / Validates all memberships before one aligned batch read; item failures are embedded in the HTTP-200 response.
  - **消息发送 / Message Sending** `/{lang}/api/product-http/message-send` — 由受信后端提交消息。 / Submit messages from a trusted backend.
    - **提交消息 / Submit a message** **POST** `/{lang}/api/product-http/message-send/sendChannelMessage` — 接受完整兼容 parser，包括旧别名、瞬时标志与请求级订阅者；HTTP 200 时仍需检查 reason。 / Accepts the complete compatibility parser, including legacy aliases, transient flags, and request-scoped subscribers; inspect reason on HTTP 200.
  - **Channel / Channels** `/{lang}/api/product-http/channels` — Channel 元数据、订阅者与名单管理。 / Channel metadata, subscribers, and list administration.
    - **创建或更新 Channel 元数据 / Create or update Channel metadata** **POST** `/{lang}/api/product-http/channels/upsertChannel` — 全量更新元数据标志并可重置订阅者；disband 为终态。 / Upserts all metadata flags and optionally resets subscribers; disband is terminal.
    - **替换 Channel 元数据 / Replace Channel metadata** **POST** `/{lang}/api/product-http/channels/updateChannelInfo` — 字段省略时仍把零值完整记录交给 UpdateInfo；该接口仅为兼容保留。 / Passes the zero-valued full record to UpdateInfo when fields are omitted; retained for compatibility.
    - **终态解散 Channel / Terminally disband a Channel** **POST** `/{lang}/api/product-http/channels/disbandChannel` — 设置持久化 disband 标志但不删除 Channel 身份；请求 Key 校验较弱。 / Sets the durable disband flag without deleting Channel identity; request-key validation is weak.
    - **添加或替换持久订阅者 / Add or replace durable subscribers** **POST** `/{lang}/api/product-http/channels/addChannelSubscribers` — 添加非空白订阅者；channel_type=0 转为群组类型 2，reset=1 替换快照。 / Adds non-blank subscribers; channel_type 0 becomes group type 2 and reset=1 replaces the snapshot.
    - **移除持久订阅者 / Remove durable subscribers** **POST** `/{lang}/api/product-http/channels/removeChannelSubscribers` — 移除非空白订阅者；与 subscriber_add 不同，channel_type=0 不会被归一化。 / Removes non-blank subscribers; unlike subscriber_add, channel_type 0 is not normalized.
    - **移除全部持久订阅者 / Remove all durable subscribers** **POST** `/{lang}/api/product-http/channels/removeAllChannelSubscribers` — 通过内部有界分页清空普通订阅者；个人 Channel 会被拒绝。 / Clears ordinary subscribers through bounded internal pages; person Channels are rejected.
    - **替换临时 Channel 订阅者 / Replace temporary Channel subscribers** **POST** `/{lang}/api/product-http/channels/setTemporaryChannelSubscribers` — 替换派生 Type-8 临时订阅者列表；入口适配器不校验 UID 元素。 / Replaces the derived Type-8 temporary subscriber list; UID elements are not validated by the entry adapter.
    - **添加 Channel 拒绝列表成员 / Add Channel denylist members** **POST** `/{lang}/api/product-http/channels/addChannelDenylistMembers` — 把给定 UID 字符串加入派生 Channel 拒绝列表。 / Adds the supplied UID strings to the derived Channel denylist.
    - **替换 Channel 拒绝列表 / Replace Channel denylist members** **POST** `/{lang}/api/product-http/channels/setChannelDenylistMembers` — 先移除旧列表再添加给定值；入口只校验 channel_id。 / Removes the old list before adding the supplied values; only channel_id is validated at entry.
    - **移除 Channel 拒绝列表成员 / Remove Channel denylist members** **POST** `/{lang}/api/product-http/channels/removeChannelDenylistMembers` — 从派生 Channel 拒绝列表移除给定 UID 字符串。 / Removes the supplied UID strings from the derived Channel denylist.
    - **移除全部 Channel 拒绝列表成员 / Remove all Channel denylist members** **POST** `/{lang}/api/product-http/channels/removeAllChannelDenylistMembers` — 通过内部有界遍历清空派生拒绝列表。 / Clears the derived denylist through bounded internal traversal.
    - **添加 Channel 允许列表成员 / Add Channel allowlist members** **POST** `/{lang}/api/product-http/channels/addChannelAllowlistMembers` — 把非空白 UID 加入派生 Channel 允许列表。 / Adds non-blank UIDs to the derived Channel allowlist.
    - **替换 Channel 允许列表 / Replace Channel allowlist members** **POST** `/{lang}/api/product-http/channels/setChannelAllowlistMembers` — 先移除旧列表再添加给定值；入口只校验 channel_id。 / Removes the old list before adding supplied values; only channel_id is validated at entry.
    - **移除 Channel 允许列表成员 / Remove Channel allowlist members** **POST** `/{lang}/api/product-http/channels/removeChannelAllowlistMembers` — 从派生 Channel 允许列表移除非空白 UID。 / Removes non-blank UIDs from the derived Channel allowlist.
    - **移除全部 Channel 允许列表成员 / Remove all Channel allowlist members** **POST** `/{lang}/api/product-http/channels/removeAllChannelAllowlistMembers` — 通过内部有界遍历清空派生允许列表。 / Clears the derived allowlist through bounded internal traversal.
    - **列出全部 Channel 允许列表成员 / List all Channel allowlist members** **GET** `/{lang}/api/product-http/channels/listChannelAllowlistMembers` — 返回无界完整列表；channel_type 缺失或无效时会静默视为 0。 / Returns an unbounded full list; missing or invalid channel_type is silently treated as 0.
  - **会话 / Conversations** `/{lang}/api/product-http/conversations` — 会话同步、未读、隐藏与激活状态。 / Conversation sync, unread, hide, and activation state.
    - **同步一页会话 / Synchronize a Conversation page** **POST** `/{lang}/api/product-http/conversations/listConversations` — 返回一页有界 membership；只有 done=true 才表示本轮完成。 / Returns one bounded membership page; only done=true completes the pass.
    - **重试未解析会话 / Retry unresolved Conversations** **POST** `/{lang}/api/product-http/conversations/retryConversations` — 重新 hydrate 最多 200 个 Key，不回退已完成 coverage；重复 Key 会合并。 / Rehydrates up to 200 keys without rewinding completed coverage; duplicate keys are merged.
    - **同步旧式会话 / Synchronize legacy Conversations** **POST** `/{lang}/api/product-http/conversations/syncConversationsLegacy` — 解析分隔式逐 Channel 游标并返回没有完成标记的旧式裸数组。 / Parses delimited per-Channel cursors and returns the old bare array without a completion signal.
    - **清除会话未读 / Clear Conversation unread** **POST** `/{lang}/api/product-http/conversations/clearConversationUnread` — 把 read_seq 推进到当前已提交 head；message_seq 等未知旧字段会被忽略。 / Advances read_seq to the current committed head; unknown legacy fields such as message_seq are ignored.
    - **设置会话最大未读数 / Set maximum Conversation unread** **POST** `/{lang}/api/product-http/conversations/setConversationUnread` — 单调推进 read_seq，使剩余未读消息不超过 unread。 / Monotonically advances read_seq so no more than unread messages remain.
    - **把会话隐藏到当前 head / Hide Conversation through the current head** **POST** `/{lang}/api/product-http/conversations/hideConversation` — 把 deleted_to_seq 推进到当前已提交 head，但不删除 Channel 成员关系。 / Advances deleted_to_seq to the current committed head without deleting Channel membership.
    - **激活会话 / Activate a Conversation** **POST** `/{lang}/api/product-http/conversations/activateConversation` — 记录显式打开、切换或恢复动作以参与排序；消息路径不会隐式激活。 / Records an explicit open, switch, or resume action for ordering; message paths do not activate it.
  - **错误响应 / Error Responses** `/{lang}/api/product-http/errors` — 解释 HTTP 状态、业务状态和 Reason Code 的关系。 / Relates HTTP status, business status, and protocol reason codes.
- **运维 HTTP API / Operations HTTP API** `/{lang}/api/operations-http` — 发布四个运维观测接口，并逐项标明稳定性。 / Publishes four operations observation endpoints with per-operation stability.
  - **健康与就绪 / Health & Readiness** `/{lang}/api/operations-http/health-and-readiness` — 说明健康检查、就绪检查和负载均衡使用方式。 / Covers health checks, readiness checks, and load-balancer usage.
  - **Metrics / Metrics** `/{lang}/api/operations-http/metrics` — 说明 Prometheus 指标入口、访问控制和抓取建议。 / Explains the Prometheus endpoint, access control, and scrape guidance.
  - **只读运维接口 / Read-only Operations** `/{lang}/api/operations-http/read-only` — 记录节点本地 Top 快照以及条件启用的 Debug、Bench 清单。 / Documents node-local Top snapshots and conditional Debug and Bench inventories.
  - **接口稳定性 / API Stability** `/{lang}/api/operations-http/stability` — 标明稳定、实验性和条件启用的运维接口。 / Marks stable, experimental, and conditionally enabled operations endpoints.
- **Webhook / Webhooks** `/{lang}/api/webhooks` — 说明服务端向业务系统投递事件的契约。 / Defines how the server delivers events to business systems.
  - **事件类型 / Event Types** `/{lang}/api/webhooks/events` — 列出消息、在线状态和其他受支持事件。 / Lists messages, presence, and other supported events.
  - **请求结构 / Payloads** `/{lang}/api/webhooks/payloads` — 定义三种事件负载，并明确请求体没有通用信封。 / Defines the three event payloads and the absence of a common envelope.
  - **安全与可靠性 / Security & Reliability** `/{lang}/api/webhooks/reliability-and-security` — 说明签名、重试、顺序、幂等和失败处理。 / Covers signatures, retries, ordering, idempotency, and failure handling.
- **客户端协议 / Client Protocols** `/{lang}/api/client-protocols` — 说明当前连接生命周期与 WKProto 数据包范围。 / Documents the current connection lifecycle and WKProto packet scope.
  - **连接生命周期 / Connection Lifecycle** `/{lang}/api/client-protocols/connection-lifecycle` — 说明 CONNECT 认证、CONNACK、心跳、关闭和恢复边界。 / Covers CONNECT authentication, CONNACK, heartbeat, close, and recovery boundaries.
  - **数据包类型 / Packet Types** `/{lang}/api/client-protocols/packet-types` — 列出当前 Frame Type、方向、支持范围和版本差异。 / Lists current Frame Types, directions, support scope, and version differences.
  - **TCP 二进制协议 / TCP Binary Protocol** `/{lang}/api/client-protocols/tcp-binary` — 定义帧格式、编码、标志位和包边界。 / Defines frame format, encoding, flags, and packet boundaries.
  - **WebSocket JSON-RPC / WebSocket JSON-RPC** `/{lang}/api/client-protocols/json-rpc` — 定义方法、参数、结果、通知和请求关联。 / Defines methods, parameters, results, notifications, and request correlation.
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
