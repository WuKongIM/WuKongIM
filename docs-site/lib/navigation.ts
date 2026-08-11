export const locales = ['zh', 'en'] as const;

export type Locale = (typeof locales)[number];
export type PublicationStatus = 'published' | 'planned';

export interface LocalizedText {
  zh: string;
  en: string;
}

export interface NavigationPage {
  slug: string;
  label: LocalizedText;
  description: LocalizedText;
  status: PublicationStatus;
}

export interface NavigationGroup extends NavigationPage {
  children: NavigationPage[];
}

export interface DocumentationDomain extends Omit<NavigationPage, 'slug'> {
  key: 'guide' | 'server' | 'sdk' | 'api';
  pages: NavigationPage[];
  groups: NavigationGroup[];
}

/** Localized, addressable menu entry consumed by routing and publishing outputs. */
export interface NavigationEntry {
  locale: Locale;
  domain: DocumentationDomain['key'];
  slugs: string[];
  url: string;
  label: string;
  description: string;
  status: PublicationStatus;
  kind: 'domain' | 'page' | 'group';
}

function text(zh: string, en: string): LocalizedText {
  return { zh, en };
}

function navigationPage(
  status: PublicationStatus,
  slug: string,
  zhLabel: string,
  enLabel: string,
  zhDescription: string,
  enDescription: string,
): NavigationPage {
  return {
    slug,
    label: text(zhLabel, enLabel),
    description: text(zhDescription, enDescription),
    status,
  };
}

function plannedPage(
  slug: string,
  zhLabel: string,
  enLabel: string,
  zhDescription: string,
  enDescription: string,
): NavigationPage {
  return navigationPage(
    'planned',
    slug,
    zhLabel,
    enLabel,
    zhDescription,
    enDescription,
  );
}

function publishedPage(
  slug: string,
  zhLabel: string,
  enLabel: string,
  zhDescription: string,
  enDescription: string,
): NavigationPage {
  return navigationPage(
    'published',
    slug,
    zhLabel,
    enLabel,
    zhDescription,
    enDescription,
  );
}

function navigationGroup(
  status: PublicationStatus,
  slug: string,
  zhLabel: string,
  enLabel: string,
  zhDescription: string,
  enDescription: string,
  children: NavigationPage[],
): NavigationGroup {
  return {
    ...navigationPage(status, slug, zhLabel, enLabel, zhDescription, enDescription),
    children,
  };
}

function plannedGroup(
  slug: string,
  zhLabel: string,
  enLabel: string,
  zhDescription: string,
  enDescription: string,
  children: NavigationPage[],
): NavigationGroup {
  return navigationGroup(
    'planned',
    slug,
    zhLabel,
    enLabel,
    zhDescription,
    enDescription,
    children,
  );
}

function publishedGroup(
  slug: string,
  zhLabel: string,
  enLabel: string,
  zhDescription: string,
  enDescription: string,
  children: NavigationPage[],
): NavigationGroup {
  return navigationGroup(
    'published',
    slug,
    zhLabel,
    enLabel,
    zhDescription,
    enDescription,
    children,
  );
}

function platformGroup(
  slug: string,
  label: string,
  zhPlatformDescription: string,
  enPlatformDescription: string,
): NavigationGroup {
  return plannedGroup(slug, label, label, zhPlatformDescription, enPlatformDescription, [
    plannedPage(
      'installation',
      '安装与配置',
      'Installation',
      `${label} SDK 的依赖、权限和构建配置。`,
      `Dependencies, permissions, and build configuration for the ${label} SDK.`,
    ),
    plannedPage(
      'quickstart',
      '快速接入',
      'Quickstart',
      `在 ${label} 应用中完成首次连接和消息收发。`,
      `Connect and exchange the first messages with the ${label} SDK.`,
    ),
    plannedPage(
      'platform-capabilities',
      '平台专属能力',
      'Platform Capabilities',
      `${label} 平台的生命周期、后台运行和推送等差异。`,
      `Lifecycle, background execution, push, and other ${label}-specific behavior.`,
    ),
    plannedPage(
      'api-reference',
      'API 参考',
      'API Reference',
      `${label} SDK 的类、方法、事件、参数和错误定义。`,
      `Classes, methods, events, parameters, and errors for the ${label} SDK.`,
    ),
    plannedPage(
      'upgrade',
      '升级指南',
      'Upgrade Guide',
      `${label} SDK 的破坏性变更、迁移步骤和发布记录。`,
      `Breaking changes, migration steps, and release history for the ${label} SDK.`,
    ),
  ]);
}

export const domains: DocumentationDomain[] = [
  {
    key: 'guide',
    label: text('指南', 'Guides'),
    description: text(
      '从认识 WuKongIM 到完成第一个业务集成。',
      'Learn WuKongIM and complete your first product integration.',
    ),
    status: 'published',
    pages: [],
    groups: [
      publishedGroup(
        'product-overview',
        '产品概览',
        'Product Overview',
        '建立产品定位、能力边界和适用场景的整体认识。',
        'Understand the product position, capability boundaries, and use cases.',
        [
          publishedPage(
            'what-is-wukongim',
            'WuKongIM 是什么',
            'What is WuKongIM?',
            '介绍频道式消息模型、集群语义，以及它与网关和消息队列的区别。',
            'Introduces the channel model, cluster semantics, and how WuKongIM differs from gateways and queues.',
          ),
          publishedPage(
            'capabilities',
            '核心能力',
            'Core Capabilities',
            '概览高并发消息、超大群、持久化、多设备、故障转移和扩容能力。',
            'Surveys high-throughput messaging, large groups, persistence, multi-device, failover, and scaling.',
          ),
          publishedPage(
            'use-cases',
            '适用场景',
            'Use Cases',
            '说明聊天、推送、客服、直播、IoT、信令和 AI 通信等用途。',
            'Explains chat, push, support, live interaction, IoT, signaling, and AI communication use cases.',
          ),
        ],
      ),
      publishedGroup(
        'quick-start',
        '快速开始',
        'Quick Start',
        '沿最短路径启动集群、发送消息并验证结果。',
        'Follow the shortest path to start a cluster, send a message, and verify the result.',
        [
          publishedPage(
            'prerequisites',
            '环境准备',
            'Prerequisites',
            '列出 Git、Go、端口、本地目录和测试工具要求。',
            'Lists Git, Go, ports, local directories, and test tool requirements.',
          ),
          publishedPage(
            'single-node-cluster',
            '启动单节点集群',
            'Start a Single-node Cluster',
            '启动单节点集群并验证就绪状态与 Manager。',
            'Starts a single-node cluster and verifies readiness and Manager access.',
          ),
          publishedPage(
            'first-message',
            '发送第一条消息',
            'Send the First Message',
            '创建测试身份并完成一次最小消息收发。',
            'Creates test identities and completes a minimal message exchange.',
          ),
          publishedPage(
            'chat-demo',
            '运行聊天演示',
            'Run the Chat Demo',
            '使用内置聊天演示验证两个测试用户之间的通信。',
            'Uses the embedded chat demo to verify communication between two test users.',
          ),
          publishedPage(
            'next-steps',
            '下一步',
            'Next Steps',
            '按接入、部署、运维和参考需求引导后续阅读。',
            'Routes readers to integration, deployment, operations, and reference material.',
          ),
        ],
      ),
      publishedGroup(
        'core-concepts',
        '核心概念',
        'Core Concepts',
        '建立应用开发所需的统一业务术语。',
        'Establishes the shared product vocabulary needed by application developers.',
        [
          publishedPage(
            'cluster-and-nodes',
            '集群与节点',
            'Clusters & Nodes',
            '解释所有部署都是集群，以及节点、Slot、副本和 Leader 的关系。',
            'Explains cluster-only deployment semantics and the relationship among nodes, slots, replicas, and leaders.',
          ),
          publishedPage(
            'messages',
            '消息',
            'Messages',
            '解释消息标识、序号、顺序、持久化、去重和离线生命周期。',
            'Explains identifiers, sequence, ordering, persistence, deduplication, and offline lifecycle.',
          ),
          publishedPage(
            'channels',
            '频道',
            'Channels',
            '解释频道作为消息路由和存储核心单位的职责。',
            'Explains channels as the core unit for message routing and storage.',
          ),
          publishedPage(
            'users-and-devices',
            '用户与设备',
            'Users & Devices',
            '区分用户、设备、连接、登录状态和多端在线。',
            'Distinguishes users, devices, connections, login state, and multi-device presence.',
          ),
          publishedPage(
            'conversations',
            '会话',
            'Conversations',
            '解释会话列表、最近消息、未读数和状态同步。',
            'Explains conversation lists, latest messages, unread counts, and state synchronization.',
          ),
        ],
      ),
      publishedGroup(
        'integration',
        '集成指南',
        'Integration',
        '从业务系统视角完成 WuKongIM 接入。',
        'Integrates WuKongIM from the perspective of an existing product system.',
        [
          publishedPage(
            'architecture',
            '集成架构',
            'Integration Architecture',
            '说明业务服务、WuKongIM 服务端和客户端 SDK 的职责与数据流。',
            'Defines responsibilities and data flow across the business service, WuKongIM server, and client SDK.',
          ),
          publishedPage(
            'authentication',
            '身份认证',
            'Authentication',
            '说明身份、Token、设备标识、连接鉴权和撤销策略。',
            'Covers identities, tokens, device identifiers, connection authentication, and revocation.',
          ),
          publishedPage(
            'messaging',
            '消息收发',
            'Messaging',
            '串联连接、发送、接收、确认、重连和离线补偿。',
            'Connects sending, receiving, acknowledgements, reconnects, and offline recovery.',
          ),
          publishedPage(
            'webhooks',
            'Webhook',
            'Webhooks',
            '介绍事件回调、签名、重试、幂等和失败处理。',
            'Introduces event callbacks, signatures, retries, idempotency, and failure handling.',
          ),
          publishedPage(
            'plugins',
            '插件扩展',
            'Plugin Extensions',
            '说明插件的适用问题、生命周期和安全边界。',
            'Explains suitable plugin use cases, lifecycle, and security boundaries.',
          ),
        ],
      ),
      publishedGroup(
        'tutorials',
        '场景教程',
        'Tutorials',
        '提供面向典型业务场景的端到端方案。',
        'Provides end-to-end solutions for representative product scenarios.',
        [
          publishedPage(
            'direct-chat',
            '单聊',
            'Direct Chat',
            '实现用户、单聊频道、消息、未读数和多设备同步。',
            'Implements users, direct channels, messages, unread counts, and multi-device sync.',
          ),
          publishedPage(
            'large-groups',
            '群聊与超大群',
            'Groups & Large Groups',
            '实现群成员维护和群消息，并说明十万级成员约束。',
            'Implements group membership and messaging with constraints for 100,000-member groups.',
          ),
          publishedPage(
            'push',
            '消息推送',
            'Message Push',
            '实现通知、系统消息、离线设备处理和失败恢复。',
            'Implements notifications, system messages, offline-device handling, and recovery.',
          ),
          publishedPage(
            'ai-and-iot',
            'AI 与 IoT 通信',
            'AI & IoT Communication',
            '展示流式 AI 回复、设备上报和服务端指令。',
            'Demonstrates streaming AI replies, device telemetry, and server commands.',
          ),
        ],
      ),
    ],
  },
  {
    key: 'server',
    label: text('服务端', 'Server'),
    description: text(
      '部署、配置、运维和理解 WuKongIM 集群。',
      'Deploy, configure, operate, and understand a WuKongIM cluster.',
    ),
    status: 'published',
    pages: [],
    groups: [
      publishedGroup(
        'deployment',
        '部署',
        'Deployment',
        '选择并实施适合环境的服务端部署方式。',
        'Choose and implement the server deployment method appropriate for the environment.',
        [
          publishedPage(
            'choosing',
            '部署方式选择',
            'Choose a Deployment',
            '比较 Docker、Linux 二进制和 Kubernetes 的适用边界。',
            'Compares the suitability of Docker, Linux binaries, and Kubernetes.',
          ),
          publishedPage(
            'docker',
            'Docker 部署',
            'Docker',
            '使用镜像部署单节点集群或多节点集群。',
            'Deploys single-node clusters or multi-node clusters from container images.',
          ),
          publishedPage(
            'linux',
            'Linux 部署',
            'Linux',
            '使用二进制、配置文件和 systemd 运行服务。',
            'Runs the server with a binary, configuration file, and systemd.',
          ),
          plannedPage(
            'kubernetes',
            'Kubernetes 部署（Beta）',
            'Kubernetes (Beta)',
            '说明持久化、服务发现、资源规划和 Beta 边界。',
            'Covers persistence, discovery, resource planning, and Beta limitations.',
          ),
          publishedPage(
            'multi-node',
            '多节点集群',
            'Multi-node Cluster',
            '规划并引导多节点集群完成启动和就绪检查。',
            'Plans and bootstraps a multi-node cluster through readiness verification.',
          ),
          publishedPage(
            'production-checklist',
            '生产检查清单',
            'Production Checklist',
            '汇总资源、磁盘、安全、监控、备份和容量检查。',
            'Checks resources, disks, security, monitoring, backups, and capacity.',
          ),
        ],
      ),
      publishedGroup(
        'configuration',
        '配置',
        'Configuration',
        '解释配置来源、覆盖规则和各领域配置。',
        'Explains configuration sources, override rules, and domain settings.',
        [
          publishedPage(
            'cluster',
            '节点与集群',
            'Nodes & Cluster',
            '节点身份、集群地址、Slot、副本和节点发现配置。',
            'Node identity, cluster addresses, slots, replicas, and discovery settings.',
          ),
          publishedPage(
            'networking',
            '网络与客户端接入',
            'Networking & Client Access',
            'TCP、WebSocket、HTTP、Manager 和节点通信监听配置。',
            'Listener settings for TCP, WebSocket, HTTP, Manager, and inter-node traffic.',
          ),
          publishedPage(
            'storage',
            '消息与存储',
            'Messages & Storage',
            '消息保留、存储路径、队列、批处理和性能配置。',
            'Message retention, storage paths, queues, batching, and performance settings.',
          ),
          publishedPage(
            'security',
            '安全与权限',
            'Security & Access',
            '认证、接口访问、Token、TLS 和敏感配置建议。',
            'Authentication, API access, tokens, TLS, and sensitive-setting guidance.',
          ),
          publishedPage(
            'observability',
            '日志与可观测性',
            'Logs & Observability',
            '日志、指标、Prometheus、Top 和诊断接口配置。',
            'Logging, metrics, Prometheus, Top, and diagnostic endpoint settings.',
          ),
          publishedPage(
            'reference',
            '配置参考',
            'Configuration Reference',
            '列出 TOML 键、类型、环境变量、脱敏边界和约束。',
            'Lists TOML keys, types, environment variables, redaction boundaries, and constraints.',
          ),
        ],
      ),
      publishedGroup(
        'operations',
        '运维',
        'Operations',
        '管理、观察和安全变更生产集群。',
        'Manage, observe, and safely change production clusters.',
        [
          publishedPage(
            'manager',
            'Manager 管理后台',
            'Manager',
            '介绍后台权限、集群状态、业务查询和运维操作。',
            'Introduces permissions, cluster state, business queries, and operations.',
          ),
          publishedPage(
            'health-and-monitoring',
            '健康检查与监控',
            'Health & Monitoring',
            '解释就绪状态、核心指标、Prometheus、Grafana 和告警。',
            'Explains readiness, key metrics, Prometheus, Grafana, and alerts.',
          ),
          publishedPage(
            'scaling',
            '扩容与缩容',
            'Scaling',
            '说明节点加入、平衡、安全缩容和 Leader 迁移。',
            'Covers node joins, balancing, safe scale-in, and leader transfer.',
          ),
          publishedPage(
            'backup-and-restore',
            '备份与恢复',
            'Backup & Restore',
            '说明备份计划、验证、恢复和灾难演练。',
            'Covers backup schedules, verification, restoration, and recovery drills.',
          ),
          publishedPage(
            'upgrade-and-migration',
            '升级与迁移',
            'Upgrade & Migration',
            '说明兼容性、滚动升级、回滚和 v2 到 v3 迁移。',
            'Covers compatibility, rolling upgrades, rollback, and v2-to-v3 migration.',
          ),
          publishedPage(
            'troubleshooting',
            '故障排查',
            'Troubleshooting',
            '按现象、指标、日志和诊断工具定位问题。',
            'Diagnoses issues through symptoms, metrics, logs, and diagnostic tools.',
          ),
        ],
      ),
      publishedGroup(
        'tools',
        '工具',
        'Tools',
        '使用官方工具观察、验证和评估集群。',
        'Use official tools to inspect, verify, and evaluate clusters.',
        [
          publishedPage(
            'wkcli',
            'wkcli',
            'wkcli',
            '查看集群状态并执行受控运维操作。',
            'Inspects cluster state and performs controlled operations.',
          ),
          publishedPage(
            'wkdb',
            'wkdb',
            'wkdb',
            '执行本地只读存储诊断和离线导入导出。',
            'Performs node-local read-only storage diagnostics and offline import/export.',
          ),
          publishedPage(
            'wkbench',
            'wkbench',
            'wkbench',
            '执行黑盒压力测试、容量评估和回归验证。',
            'Runs black-box load tests, capacity evaluations, and regression checks.',
          ),
          publishedPage(
            'diagnostics',
            '诊断能力',
            'Diagnostics',
            '选择日志、指标、Top、pprof 和只读 Operations MCP。',
            'Selects among logs, metrics, Top, pprof, and the read-only Operations MCP.',
          ),
        ],
      ),
      publishedGroup(
        'architecture',
        '架构',
        'Architecture',
        '从控制、元数据、消息和网络层理解系统。',
        'Understand the system through control, metadata, messaging, and network layers.',
        [
          publishedPage(
            'controller',
            'Controller 控制层',
            'Controller Layer',
            '解释集群元数据、节点管理、任务和一致性控制。',
            'Explains cluster metadata, node management, tasks, and consistency control.',
          ),
          publishedPage(
            'slots',
            'Slot 元数据层',
            'Slot Metadata Layer',
            '解释默认 256 个 Hash Slot、归属、副本和 Leader 路由。',
            'Explains the default 256 hash slots, ownership, replicas, and leader routing.',
          ),
          publishedPage(
            'channels',
            'Channel 消息层',
            'Channel Messaging Layer',
            '解释频道副本、消息日志、Leader 和故障切换。',
            'Explains channel replicas, message logs, leaders, and failover.',
          ),
          publishedPage(
            'transport',
            'Transport 网络层',
            'Transport Layer',
            '解释节点连接、RPC、消息传输和背压。',
            'Explains node connections, RPC, message transport, and backpressure.',
          ),
          publishedPage(
            'message-flow',
            '消息发送链路',
            'Message Send Flow',
            '跟踪消息进入、复制、持久化和投递的完整过程。',
            'Traces message ingress, replication, persistence, and delivery.',
          ),
          publishedPage(
            'user-routing',
            '用户连接路由',
            'User Connection Routing',
            '解释在线状态、连接归属和跨节点投递。',
            'Explains presence, connection ownership, and cross-node delivery.',
          ),
        ],
      ),
    ],
  },
  {
    key: 'sdk',
    label: text('SDK', 'SDK'),
    description: text(
      '在不同客户端平台接入 WuKongIM。',
      'Integrate WuKongIM across supported client platforms.',
    ),
    status: 'published',
    pages: [
      plannedPage(
        'choose-sdk',
        '选择 SDK',
        'Choose an SDK',
        '根据应用平台、框架和运行环境选择客户端 SDK。',
        'Choose a client SDK by platform, framework, and runtime.',
      ),
      plannedPage(
        'compatibility',
        '版本与兼容性',
        'Versions & Compatibility',
        '汇总 SDK 版本、服务端兼容范围、系统要求和维护状态。',
        'Lists SDK versions, server compatibility, system requirements, and maintenance status.',
      ),
    ],
    groups: [
      plannedGroup(
        'common-guides',
        '公共指南',
        'Common Guides',
        '统一说明所有客户端 SDK 共有的接入行为。',
        'Explains integration behavior shared by all client SDKs.',
        [
          plannedPage(
            'identity-and-token',
            '身份与 Token',
            'Identity & Token',
            '说明用户、设备、Token 获取和失效处理。',
            'Explains users, devices, token acquisition, and invalidation.',
          ),
          plannedPage(
            'initialization-and-connection',
            '初始化与连接',
            'Initialization & Connection',
            '说明 SDK 初始化、连接状态、生命周期和退出。',
            'Covers SDK initialization, connection state, lifecycle, and logout.',
          ),
          plannedPage(
            'messaging',
            '消息收发',
            'Messaging',
            '解释发送、接收、确认、消息状态和错误处理。',
            'Explains send, receive, acknowledgement, message state, and error handling.',
          ),
          plannedPage(
            'custom-messages',
            '自定义消息',
            'Custom Messages',
            '定义自定义消息的编码、注册、兼容和降级。',
            'Defines encoding, registration, compatibility, and fallback for custom messages.',
          ),
          plannedPage(
            'conversations-and-unread',
            '会话与未读数',
            'Conversations & Unread Counts',
            '说明会话、最近消息、未读数和已读状态。',
            'Explains conversations, latest messages, unread counts, and read state.',
          ),
          plannedPage(
            'offline-and-push',
            '离线消息与推送',
            'Offline Messages & Push',
            '区分离线同步和系统推送，并说明协作方式。',
            'Distinguishes offline synchronization from system push and explains how they cooperate.',
          ),
          plannedPage(
            'multi-device',
            '多设备同步',
            'Multi-device Sync',
            '说明多端登录、消息同步和设备状态一致性。',
            'Explains multi-device login, message sync, and device-state consistency.',
          ),
          plannedPage(
            'reconnect-and-errors',
            '重连与异常处理',
            'Reconnect & Errors',
            '说明断线、网络切换、超时、重试和常见错误。',
            'Covers disconnects, network changes, timeouts, retries, and common errors.',
          ),
        ],
      ),
      platformGroup(
        'android',
        'Android',
        'Android SDK 的支持范围、系统要求和接入入口。',
        'Support scope, system requirements, and entry points for the Android SDK.',
      ),
      platformGroup(
        'ios',
        'iOS',
        'iOS SDK 的支持范围、系统要求和接入入口。',
        'Support scope, system requirements, and entry points for the iOS SDK.',
      ),
      platformGroup(
        'javascript',
        'JavaScript / Web',
        'JavaScript SDK 的浏览器支持范围和接入入口。',
        'Browser support and entry points for the JavaScript SDK.',
      ),
      platformGroup(
        'flutter',
        'Flutter',
        'Flutter SDK 的支持范围、系统要求和接入入口。',
        'Support scope, system requirements, and entry points for the Flutter SDK.',
      ),
      platformGroup(
        'uniapp',
        'UniApp',
        'UniApp SDK 的支持范围、平台差异和接入入口。',
        'Support scope, platform differences, and entry points for the UniApp SDK.',
      ),
      platformGroup(
        'harmonyos',
        'HarmonyOS',
        'HarmonyOS SDK 的支持范围、系统要求和接入入口。',
        'Support scope, system requirements, and entry points for the HarmonyOS SDK.',
      ),
    ],
  },
  {
    key: 'api',
    label: text('API 与协议', 'API & Protocols'),
    description: text(
      '查阅 HTTP API、Webhook 和客户端协议。',
      'Reference HTTP APIs, webhooks, and client protocols.',
    ),
    status: 'published',
    pages: [
      plannedPage(
        'conventions',
        '通用约定',
        'Conventions',
        '定义 Base URL、JSON、时间、ID、分页、幂等和响应结构。',
        'Defines base URLs, JSON, time, identifiers, pagination, idempotency, and response envelopes.',
      ),
      plannedPage(
        'authentication',
        '认证与安全',
        'Authentication & Security',
        '说明 API 凭证、Token、请求保护和生产安全要求。',
        'Explains API credentials, tokens, request protection, and production security.',
      ),
      plannedPage(
        'compatibility',
        '版本与兼容性',
        'Versions & Compatibility',
        '说明 API、客户端协议与服务端版本的兼容规则。',
        'Defines compatibility among APIs, client protocols, and server versions.',
      ),
    ],
    groups: [
      plannedGroup(
        'product-http',
        '产品 HTTP API',
        'Product HTTP API',
        '面向稳定产品能力的规范驱动 HTTP 参考。',
        'Specification-driven HTTP reference for stable product capabilities.',
        [
          plannedPage(
            'users',
            '用户',
            'Users',
            'Token、设备退出、在线状态和系统用户接口。',
            'Token, device logout, online status, and system-user endpoints.',
          ),
          plannedPage(
            'channels',
            '频道',
            'Channels',
            '频道、订阅者、黑名单、白名单和临时频道接口。',
            'Channel, subscriber, blacklist, whitelist, and temporary-channel endpoints.',
          ),
          plannedPage(
            'messages',
            '消息',
            'Messages',
            '消息发送、同步、确认和消息事件接口。',
            'Message send, sync, acknowledgement, and event endpoints.',
          ),
          plannedPage(
            'conversations',
            '会话',
            'Conversations',
            '会话列表、同步、未读数和删除接口。',
            'Conversation list, sync, unread-count, and deletion endpoints.',
          ),
          plannedPage(
            'routing',
            '路由发现',
            'Route Discovery',
            '获取目标节点 TCP 和 WebSocket 接入地址。',
            'Discovers TCP and WebSocket addresses for the target node.',
          ),
          plannedPage(
            'errors',
            '错误响应',
            'Error Responses',
            '解释 HTTP 状态、业务状态和 Reason Code 的关系。',
            'Relates HTTP status, business status, and protocol reason codes.',
          ),
        ],
      ),
      plannedGroup(
        'operations-http',
        '运维 HTTP API',
        'Operations HTTP API',
        '发布稳定且受支持的运维接口。',
        'Publishes stable and supported operations endpoints.',
        [
          plannedPage(
            'health-and-readiness',
            '健康与就绪',
            'Health & Readiness',
            '说明健康检查、就绪检查和负载均衡使用方式。',
            'Covers health checks, readiness checks, and load-balancer usage.',
          ),
          plannedPage(
            'metrics',
            'Metrics',
            'Metrics',
            '说明 Prometheus 指标入口、访问控制和抓取建议。',
            'Explains the Prometheus endpoint, access control, and scrape guidance.',
          ),
          plannedPage(
            'read-only',
            '只读运维接口',
            'Read-only Operations',
            '记录正式支持的节点状态和资源快照查询。',
            'Documents supported node-state and resource-snapshot queries.',
          ),
          plannedPage(
            'stability',
            '接口稳定性',
            'API Stability',
            '标明稳定、实验性和条件启用的运维接口。',
            'Marks stable, experimental, and conditionally enabled operations endpoints.',
          ),
        ],
      ),
      plannedGroup(
        'webhooks',
        'Webhook',
        'Webhooks',
        '说明服务端向业务系统投递事件的契约。',
        'Defines how the server delivers events to business systems.',
        [
          plannedPage(
            'events',
            '事件类型',
            'Event Types',
            '列出消息、在线状态和其他受支持事件。',
            'Lists messages, presence, and other supported events.',
          ),
          plannedPage(
            'payloads',
            '请求结构',
            'Payloads',
            '定义通用信封、事件负载和示例。',
            'Defines the common envelope, event payloads, and examples.',
          ),
          plannedPage(
            'reliability-and-security',
            '安全与可靠性',
            'Security & Reliability',
            '说明签名、重试、顺序、幂等和失败处理。',
            'Covers signatures, retries, ordering, idempotency, and failure handling.',
          ),
        ],
      ),
      plannedGroup(
        'client-protocols',
        '客户端协议',
        'Client Protocols',
        '说明 TCP 二进制协议与 WebSocket JSON-RPC。',
        'Documents the TCP binary protocol and WebSocket JSON-RPC.',
        [
          plannedPage(
            'connection-lifecycle',
            '连接生命周期',
            'Connection Lifecycle',
            '说明 Connect、认证、心跳、断开和重连。',
            'Covers connect, authentication, heartbeat, disconnect, and reconnect.',
          ),
          plannedPage(
            'tcp-binary',
            'TCP 二进制协议',
            'TCP Binary Protocol',
            '定义帧格式、编码、标志位和包边界。',
            'Defines frame format, encoding, flags, and packet boundaries.',
          ),
          plannedPage(
            'json-rpc',
            'WebSocket JSON-RPC',
            'WebSocket JSON-RPC',
            '定义方法、参数、结果、通知和请求关联。',
            'Defines methods, parameters, results, notifications, and request correlation.',
          ),
          plannedPage(
            'packet-types',
            '数据包类型',
            'Packet Types',
            '说明 Connect、Send、Recv、Ack 和 Ping/Pong 字段。',
            'Documents Connect, Send, Recv, Ack, and Ping/Pong fields.',
          ),
          plannedPage(
            'encryption',
            '加密与安全',
            'Encryption & Security',
            '说明握手密钥、负载保护和协议安全约束。',
            'Covers handshake keys, payload protection, and protocol security constraints.',
          ),
        ],
      ),
      plannedGroup(
        'dictionaries',
        '公共数据字典',
        'Shared Dictionaries',
        '集中定义跨 API 和协议复用的常量。',
        'Centralizes constants shared across APIs and protocols.',
        [
          plannedPage(
            'channel-types',
            'Channel Type',
            'Channel Type',
            '定义单聊、群聊和其他频道类型。',
            'Defines direct, group, and other channel types.',
          ),
          plannedPage(
            'device-flags',
            'Device Flag',
            'Device Flag',
            '定义 App、Web、System 等设备标识。',
            'Defines App, Web, System, and other device identifiers.',
          ),
          plannedPage(
            'message-flags',
            'Message Flags',
            'Message Flags',
            '定义持久化、红点、回执和流式消息标志。',
            'Defines persistence, red-dot, receipt, and streaming-message flags.',
          ),
          plannedPage(
            'reason-codes',
            'Reason Code',
            'Reason Code',
            '定义成功、鉴权失败、重试和拒绝等原因码。',
            'Defines success, authentication failure, retry, refusal, and other reason codes.',
          ),
        ],
      ),
      plannedGroup(
        'specifications',
        '规范下载',
        'Specifications',
        '提供校准后、可机器读取的接口与协议规范。',
        'Provides aligned, machine-readable API and protocol specifications.',
        [
          plannedPage(
            'openapi',
            'OpenAPI',
            'OpenAPI',
            '在线浏览并下载校准后的 v3 HTTP API 规范。',
            'Browse and download the aligned v3 HTTP API specification.',
          ),
          plannedPage(
            'json-rpc-schema',
            'JSON-RPC Schema',
            'JSON-RPC Schema',
            '浏览并下载 WebSocket JSON-RPC Schema。',
            'Browse and download the WebSocket JSON-RPC schema.',
          ),
          plannedPage(
            'protocol-changelog',
            '协议变更记录',
            'Protocol Changelog',
            '记录破坏性变化、兼容范围和迁移方式。',
            'Records breaking changes, compatibility ranges, and migrations.',
          ),
        ],
      ),
    ],
  },
];

/** Parses a route or content locale without widening it to an arbitrary string. */
export function parseLocale(value: string): Locale | undefined {
  return locales.find((locale) => locale === value);
}

function entryFromPage(
  locale: Locale,
  domain: DocumentationDomain,
  page: Omit<NavigationPage, 'slug'>,
  slugs: string[],
  kind: NavigationEntry['kind'],
): NavigationEntry {
  return {
    locale,
    domain: domain.key,
    slugs,
    url: `/${[locale, domain.key, ...slugs].join('/')}`,
    label: page.label[locale],
    description: page.description[locale],
    status: page.status,
    kind,
  };
}

/** Returns every domain, folder index, and leaf page in display order. */
export function getAllNavigationEntries(locale: Locale): NavigationEntry[] {
  return domains.flatMap((domain) => {
    const entries = [entryFromPage(locale, domain, domain, [], 'domain')];

    for (const page of domain.pages) {
      entries.push(entryFromPage(locale, domain, page, [page.slug], 'page'));
    }

    for (const group of domain.groups) {
      entries.push(entryFromPage(locale, domain, group, [group.slug], 'group'));
      for (const page of group.children) {
        entries.push(entryFromPage(locale, domain, page, [group.slug, page.slug], 'page'));
      }
    }

    return entries;
  });
}

/** Returns only content that is allowed into search, SEO, sitemap, and LLM outputs. */
export function getIndexedNavigationEntries(locale: Locale): NavigationEntry[] {
  return getAllNavigationEntries(locale).filter((entry) => entry.status === 'published');
}

/** Resolves the canonical route registry entry for a locale and domain path. */
export function getNavigationEntry(
  locale: Locale,
  domain: DocumentationDomain['key'],
  slugs: string[],
): NavigationEntry | undefined {
  return getAllNavigationEntries(locale).find(
    (entry) => entry.domain === domain && entry.slugs.join('/') === slugs.join('/'),
  );
}

/**
 * Resolves a Fumadocs MDX path against the navigation publication registry.
 * Unknown paths fail closed so adding content cannot publish it accidentally.
 */
export function isPublishedContentPath(filePath: string): boolean {
  const extensionless = filePath.replace(/\.mdx?$/, '');
  if (extensionless === filePath) return false;

  const localeSuffix = extensionless.match(/\.(zh|en)$/);
  const locale = parseLocale(localeSuffix?.[1] ?? 'zh');
  if (!locale) return false;

  const routePath = localeSuffix ? extensionless.slice(0, -localeSuffix[0].length) : extensionless;
  const segments = routePath.split('/').filter(Boolean);
  if (segments.at(-1) === 'index') segments.pop();

  const [domainKey, ...slugs] = segments;
  const domain = domains.find((candidate) => candidate.key === domainKey);
  if (!domain) return false;

  return getNavigationEntry(locale, domain.key, slugs)?.status === 'published';
}
