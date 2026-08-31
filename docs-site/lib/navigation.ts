import {
  productHTTPOpenAPIReferenceGroups,
  type ProductHTTPOpenAPIMethod,
} from './product-http-openapi';

export const locales = ['zh', 'en'] as const;

export type Locale = (typeof locales)[number];
export type PublicationStatus = 'published' | 'planned';
export type NavigationHTTPMethod = Uppercase<ProductHTTPOpenAPIMethod>;

export interface LocalizedText {
  zh: string;
  en: string;
}

export interface NavigationPage {
  /** Relative route path below its parent; nested pages use slash-separated segments. */
  slug: string;
  label: LocalizedText;
  description: LocalizedText;
  status: PublicationStatus;
  /** HTTP method shown beside one OpenAPI operation in the sidebar. */
  method?: NavigationHTTPMethod;
}

export interface NavigationGroup extends NavigationPage {
  children: NavigationNode[];
  /** Keeps direct child routes at the domain root while grouping them in the sidebar. */
  childrenAtDomainRoot?: boolean;
}

export type NavigationNode = NavigationPage | NavigationGroup;

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
  children: NavigationNode[],
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
  children: NavigationNode[],
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
  children: NavigationNode[],
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

interface FullSDKAdvancedPage {
  slug: string;
  label: LocalizedText;
  description: LocalizedText;
}

interface FullSDKPlatform {
  slug: 'android' | 'ios' | 'javascript' | 'flutter' | 'harmonyos';
  label: string;
  packageName: string;
  version: string;
  language: string;
  advanced: FullSDKAdvancedPage[];
}

function advancedPage(
  slug: string,
  zhLabel: string,
  enLabel: string,
  zhDescription: string,
  enDescription: string,
): FullSDKAdvancedPage {
  return {
    slug,
    label: text(zhLabel, enLabel),
    description: text(zhDescription, enDescription),
  };
}

function publishedFullSDKPlatformGroup(platform: FullSDKPlatform): NavigationGroup {
  const identity = `${platform.packageName} ${platform.version}`;
  return publishedGroup(
    platform.slug,
    platform.label,
    platform.label,
    `从快速开始到常用管理器，使用 ${identity} 完成清晰、可查找的 ${platform.label} 接入。`,
    `Integrate ${identity} on ${platform.label} through a clear quickstart and task-based manager guides.`,
    [
      publishedPage(
        'quickstart',
        '快速开始',
        'Quickstart',
        `安装 ${identity}，连接一个用户，并用 ${platform.language} 完成第一条在线文本消息。`,
        `Install ${identity}, connect one user, and exchange the first online text message in ${platform.language}.`,
      ),
      publishedPage(
        'connection',
        '连接管理',
        'Connection',
        '配置 UID、Token 与连接地址，监听连接状态，并正确处理断开和退出。',
        'Configure the UID, token, and endpoint; observe connection state; and disconnect or log out correctly.',
      ),
      publishedPage(
        'messages',
        '消息管理',
        'Messages',
        '发送、接收和查询消息，并理解发送中、发送成功与发送失败。',
        'Send, receive, and query messages while understanding sending, success, and failure states.',
      ),
      publishedPage(
        'conversations',
        '会话管理',
        'Conversations',
        '读取聊天列表、监听会话变化，并管理未读数。',
        'Read the chat list, observe conversation changes, and manage unread counts.',
      ),
      publishedPage(
        'channels',
        '频道管理',
        'Channels',
        '获取单聊或群聊资料，监听资料变化，并连接业务数据源。',
        'Load direct or group chat profiles, observe changes, and connect product data providers.',
      ),
      publishedGroup(
        'advanced',
        '高级功能',
        'Advanced',
        '按当前平台确实提供的 API 学习自定义消息、媒体和离线能力。',
        'Use only the custom-content, media, and offline APIs actually provided by this platform.',
        platform.advanced.map((item) =>
          publishedPage(
            item.slug,
            item.label.zh,
            item.label.en,
            item.description.zh,
            item.description.en,
          ),
        ),
      ),
      publishedPage(
        'api-reference',
        'API 参考',
        'API Reference',
        '按管理器查找常用入口、监听器、Provider、模型和状态。',
        'Find common manager entry points, listeners, providers, models, and states.',
      ),
    ],
  );
}

function publishedAndroidSDKGroup(): NavigationGroup {
  return publishedFullSDKPlatformGroup({
    slug: 'android',
    label: 'Android',
    packageName: 'WuKongIMAndroidSDK',
    version: '1.5.5',
    language: 'Java',
    advanced: [
      advancedPage(
        'custom-messages',
        '自定义消息',
        'Custom Messages',
        '定义、注册并发送自己的业务消息类型。',
        'Define, register, and send product-specific message content.',
      ),
      advancedPage(
        'media-and-history',
        '媒体与历史消息',
        'Media & History',
        '接入媒体上传，并在本地消息不足时补齐历史消息。',
        'Connect media upload and fill message history when local data is incomplete.',
      ),
    ],
  });
}

function publishedIOSSDKGroup(): NavigationGroup {
  return publishedFullSDKPlatformGroup({
    slug: 'ios',
    label: 'iOS',
    packageName: 'WuKongIMSDK',
    version: '1.1.1',
    language: 'Objective-C',
    advanced: [
      advancedPage(
        'custom-messages',
        '自定义消息',
        'Custom Messages',
        '继承消息正文、注册类型并发送自己的业务消息。',
        'Subclass message content, register its type, and send product-specific messages.',
      ),
      advancedPage(
        'media-and-history',
        '媒体与历史消息',
        'Media & History',
        '接入图片和语音上传，并在本地消息不足时同步历史消息。',
        'Connect image and voice upload and synchronize history when local messages are incomplete.',
      ),
    ],
  });
}

function publishedJavaScriptSDKGroup(): NavigationGroup {
  return publishedFullSDKPlatformGroup({
    slug: 'javascript',
    label: 'JavaScript / Web',
    packageName: 'wukongimjssdk',
    version: '1.3.5',
    language: 'TypeScript',
    advanced: [
      advancedPage(
        'custom-messages',
        '自定义消息',
        'Custom Messages',
        '定义、注册并发送浏览器业务需要的消息正文。',
        'Define, register, and send message content required by the browser product.',
      ),
      advancedPage(
        'offline-and-uniapp',
        '离线恢复与 UniApp 迁移',
        'Offline Recovery & UniApp Migration',
        '接入离线消息同步，并把旧 UniApp SDK 迁移到 JavaScript SDK。',
        'Connect offline synchronization and migrate the retired UniApp SDK to the JavaScript SDK.',
      ),
    ],
  });
}

function publishedFlutterSDKGroup(): NavigationGroup {
  return publishedFullSDKPlatformGroup({
    slug: 'flutter',
    label: 'Flutter',
    packageName: 'wukongimfluttersdk',
    version: '1.7.9',
    language: 'Dart',
    advanced: [
      advancedPage(
        'custom-messages',
        '自定义消息',
        'Custom Messages',
        '定义、注册并发送 Flutter 业务消息类型。',
        'Define, register, and send product-specific Flutter message content.',
      ),
      advancedPage(
        'media-and-history',
        '媒体与历史消息',
        'Media & History',
        '接入媒体上传，并在本地消息不足时补齐历史消息。',
        'Connect media upload and fill message history when local data is incomplete.',
      ),
    ],
  });
}

function publishedHarmonyOSSDKGroup(): NavigationGroup {
  return publishedFullSDKPlatformGroup({
    slug: 'harmonyos',
    label: 'HarmonyOS',
    packageName: '@wukong/wkim',
    version: '1.1.7',
    language: 'ArkTS',
    advanced: [
      advancedPage(
        'custom-messages',
        '自定义消息',
        'Custom Messages',
        '定义、注册并发送 HarmonyOS 业务消息类型。',
        'Define, register, and send product-specific HarmonyOS message content.',
      ),
      advancedPage(
        'media-and-history',
        '媒体与历史消息',
        'Media & History',
        '接入图片或语音消息，并在本地数据不足时补齐历史消息。',
        'Connect image or voice messages and fill history when local data is incomplete.',
      ),
    ],
  });
}

function publishedEasySDKGroup(): NavigationGroup {
  return publishedGroup(
    'easy',
    'WuKongEasySDK',
    'WuKongEasySDK',
    '固定版本的 iOS、Android、Flutter 与 Web 教程已发布，并具有 JSON-RPC CONNECT 与在线双向收发的服务端线协议凭据；平台构建、设备运行和生产 Token 校验仍需单独验收。',
    'Pinned iOS, Android, Flutter, and Web tutorials are published with a server-side wire receipt for JSON-RPC CONNECT and online bidirectional messaging; platform builds, device runs, and production token verification still require separate acceptance.',
    [
      publishedPage(
        'ios/getting-started',
        '5 分钟集成 iOS',
        '5-minute iOS integration',
        '已发布 v1.0.3 源码教程；JSON-RPC CONNECT 与在线双向收发已有服务端线协议验证，iOS 构建和设备运行仍需自验。',
        'Published for v1.0.3; JSON-RPC CONNECT and online bidirectional messaging have a server-side wire receipt, while iOS build and device execution remain deployment-owned.',
      ),
      publishedPage(
        'android/getting-started',
        '5 分钟集成 Android',
        '5-minute Android integration',
        '已发布 v1.0.3 源码教程；JSON-RPC CONNECT 与在线双向收发已有服务端线协议验证，Android 构建和设备运行仍需自验。',
        'Published for v1.0.3; JSON-RPC CONNECT and online bidirectional messaging have a server-side wire receipt, while Android build and device execution remain deployment-owned.',
      ),
      publishedPage(
        'flutter/getting-started',
        '5 分钟集成 Flutter',
        '5-minute Flutter integration',
        '已发布 v1.0.4 源码教程；JSON-RPC CONNECT 与在线双向收发已有服务端线协议验证，Flutter 构建和设备运行仍需自验。',
        'Published for v1.0.4; JSON-RPC CONNECT and online bidirectional messaging have a server-side wire receipt, while Flutter build and device execution remain deployment-owned.',
      ),
      publishedPage(
        'javascript/getting-started',
        '5 分钟集成 Web',
        '5-minute Web integration',
        '已发布 v2.0.2 源码教程；JSON-RPC CONNECT 与在线双向收发已有服务端线协议验证，浏览器产物运行仍需自验。',
        'Published for v2.0.2; JSON-RPC CONNECT and online bidirectional messaging have a server-side wire receipt, while browser artifact execution remains deployment-owned.',
      ),
    ],
  );
}

function publishedWuKongIMSDKGroup(): NavigationGroup {
  return {
    ...publishedGroup(
      'wukongim',
      'WuKongIMSDK',
      'WuKongIMSDK',
      '完整版客户端 SDK：管理连接、消息、本地会话、未读数与离线数据。',
      'Full client SDKs that manage connections, messages, local conversations, unread counts, and offline data.',
      [
        publishedPage(
          'wukongim/concepts',
          '核心概念',
          'Core Concepts',
          '用简单语言理解 UID、Token、频道、消息状态、会话和 Provider。',
          'Understand UIDs, tokens, Channels, message states, Conversations, and providers in plain language.',
        ),
        publishedAndroidSDKGroup(),
        publishedIOSSDKGroup(),
        publishedJavaScriptSDKGroup(),
        publishedFlutterSDKGroup(),
        publishedHarmonyOSSDKGroup(),
        publishedPage(
          'wukongim/upgrade',
          '升级 SDK',
          'Upgrade SDKs',
          '用一套简洁流程升级依赖、检查数据兼容并准备回滚。',
          'Upgrade dependencies, check data compatibility, and prepare rollback with one concise workflow.',
        ),
      ],
    ),
    childrenAtDomainRoot: true,
  };
}

function publishedProductHTTPGroup(): NavigationGroup {
  return publishedGroup(
    'product-http',
    'Product HTTP API',
    'Product HTTP API',
    '浏览当前源码注册的全部 41 条 Product HTTP 操作。',
    'Browse all 41 Product HTTP operations registered by the current source.',
    [
      ...productHTTPOpenAPIReferenceGroups.map((group) =>
        publishedGroup(
          group.slug,
          group.title.zh,
          group.title.en,
          group.description.zh,
          group.description.en,
          group.operations.map((operation) => ({
            ...publishedPage(
              operation.slug,
              operation.title.zh,
              operation.title.en,
              operation.description.zh,
              operation.description.en,
            ),
            method: operation.method === 'get' ? 'GET' : 'POST',
          })),
        ),
      ),
      publishedPage(
        'errors',
        '错误响应',
        'Error Responses',
        '解释 HTTP 状态、业务状态和 Reason Code 的关系。',
        'Relates HTTP status, business status, and protocol reason codes.',
      ),
    ],
  );
}

/** Distinguishes folders from leaf pages in the recursive navigation tree. */
export function isNavigationGroup(node: NavigationNode): node is NavigationGroup {
  return 'children' in node;
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
        '用消息、频道、用户、设备和会话理解 WuKongIM 如何组织即时通信。',
        'Explains how WuKongIM organizes communication through messages, channels, users, devices, and conversations.',
        [
          publishedPage(
            'messages',
            '消息',
            'Message',
            '消息是什么、如何找到接收范围，以及发送成功、送达和已读的区别。',
            'Explains what a message is, how it finds recipients, and why sent, delivered, and read are different outcomes.',
          ),
          publishedPage(
            'channels',
            '频道',
            'Channel',
            '频道如何表示单聊、群聊等消息目标，并组织参与者和消息历史。',
            'Explains how a Channel represents direct and group targets and organizes participants and message history.',
          ),
          publishedPage(
            'users',
            '用户',
            'User',
            '用户如何通过稳定 UID 接入，以及 WuKongIM 与业务账号系统的职责边界。',
            'Explains how a stable UID enters WuKongIM and what remains the responsibility of the product account system.',
          ),
          publishedPage(
            'devices',
            '设备',
            'Device',
            '设备、连接与多端在线的区别，以及哪些状态会跨设备共享。',
            'Separates devices from connections and explains multi-endpoint presence and shared state.',
          ),
          publishedPage(
            'conversations',
            '会话',
            'Conversation',
            '会话如何把频道呈现为聊天列表，并管理未读和个人可见状态。',
            'Explains how a Conversation presents a Channel in a chat list with unread and personal visibility state.',
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
          publishedPage(
            'acceptance',
            '上线验收',
            'Integration Acceptance',
            '把可执行兼容性证据与生产身份、网络、回调、容量和回滚门禁分开。',
            'Separates executable compatibility evidence from production identity, network, callback, capacity, and rollback gates.',
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
          publishedPage(
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
      'Integrate WuKongIM across client platforms.',
    ),
    status: 'published',
    pages: [],
    groups: [publishedWuKongIMSDKGroup(), publishedEasySDKGroup()],
  },
  {
    key: 'api',
    label: text('API 与协议', 'API & Protocols'),
    description: text(
      '查阅源码校准的 HTTP、Webhook、客户端协议与私有接口边界。',
      'Reference source-aligned HTTP, webhook, client-protocol, and private-interface boundaries.',
    ),
    status: 'published',
    pages: [
      publishedPage(
        'conventions',
        '通用约定',
        'Conventions',
        'Product HTTP 的地址、格式、标识和重试规则。',
        'Product HTTP addressing, formats, identifiers, and retry rules.',
      ),
      publishedPage(
        'authentication',
        '认证与安全',
        'Authentication & Security',
        'Product HTTP 与 Gateway 的鉴权边界。',
        'Authentication boundaries for Product HTTP and Gateway.',
      ),
      publishedPage(
        'compatibility',
        '版本与兼容性',
        'Versions & Compatibility',
        '查看构建快照和接口覆盖状态。',
        'View the build snapshot and API coverage status.',
      ),
      publishedPage(
        'interface-inventory',
        '接口清单与信任边界',
        'Interface Inventory & Trust Boundaries',
        '盘点 Manager、Node transport、MCP、插件与 Agent 私有合同。',
        'Inventories Manager, node transport, MCP, plugin, and agent-private contracts.',
      ),
    ],
    groups: [
      publishedProductHTTPGroup(),
      publishedGroup(
        'operations-http',
        '运维 HTTP API',
        'Operations HTTP API',
        '发布四个运维观测接口，并逐项标明稳定性。',
        'Publishes four operations observation endpoints with per-operation stability.',
        [
          publishedPage(
            'health-and-readiness',
            '健康与就绪',
            'Health & Readiness',
            '说明健康检查、就绪检查和负载均衡使用方式。',
            'Covers health checks, readiness checks, and load-balancer usage.',
          ),
          publishedPage(
            'metrics',
            'Metrics',
            'Metrics',
            '说明 Prometheus 指标入口、访问控制和抓取建议。',
            'Explains the Prometheus endpoint, access control, and scrape guidance.',
          ),
          publishedPage(
            'read-only',
            '只读运维接口',
            'Read-only Operations',
            '记录节点本地 Top 快照以及条件启用的 Debug、Bench 清单。',
            'Documents node-local Top snapshots and conditional Debug and Bench inventories.',
          ),
          publishedPage(
            'stability',
            '接口稳定性',
            'API Stability',
            '标明稳定、实验性和条件启用的运维接口。',
            'Marks stable, experimental, and conditionally enabled operations endpoints.',
          ),
        ],
      ),
      publishedGroup(
        'webhooks',
        'Webhook',
        'Webhooks',
        '说明服务端向业务系统投递事件的契约。',
        'Defines how the server delivers events to business systems.',
        [
          publishedPage(
            'events',
            '事件类型',
            'Event Types',
            '列出消息、在线状态和其他受支持事件。',
            'Lists messages, presence, and other supported events.',
          ),
          publishedPage(
            'payloads',
            '请求结构',
            'Payloads',
            '定义三种事件负载，并明确请求体没有通用信封。',
            'Defines the three event payloads and the absence of a common envelope.',
          ),
          publishedPage(
            'reliability-and-security',
            '安全与可靠性',
            'Security & Reliability',
            '说明签名、重试、顺序、幂等和失败处理。',
            'Covers signatures, retries, ordering, idempotency, and failure handling.',
          ),
        ],
      ),
      publishedGroup(
        'client-protocols',
        '客户端协议',
        'Client Protocols',
        '说明当前连接生命周期与 WKProto 数据包范围。',
        'Documents the current connection lifecycle and WKProto packet scope.',
        [
          publishedPage(
            'connection-lifecycle',
            '连接生命周期',
            'Connection Lifecycle',
            '说明 CONNECT 认证、CONNACK、心跳、关闭和恢复边界。',
            'Covers CONNECT authentication, CONNACK, heartbeat, close, and recovery boundaries.',
          ),
          publishedPage(
            'packet-types',
            '数据包类型',
            'Packet Types',
            '列出当前 Frame Type、方向、支持范围和版本差异。',
            'Lists current Frame Types, directions, support scope, and version differences.',
          ),
          publishedPage(
            'tcp-binary',
            'TCP 二进制协议',
            'TCP Binary Protocol',
            '定义帧格式、编码、标志位和包边界。',
            'Defines frame format, encoding, flags, and packet boundaries.',
          ),
          publishedPage(
            'json-rpc',
            'WebSocket JSON-RPC',
            'WebSocket JSON-RPC',
            '定义方法、参数、结果、通知和请求关联。',
            'Defines methods, parameters, results, notifications, and request correlation.',
          ),
          publishedPage(
            'encryption',
            '加密与安全',
            'Encryption & Security',
            '说明握手密钥、负载保护和协议安全约束。',
            'Covers handshake keys, payload protection, and protocol security constraints.',
          ),
        ],
      ),
      publishedGroup(
        'dictionaries',
        '公共数据字典',
        'Shared Dictionaries',
        '发布源码校准的 Channel、设备、消息标志与 Reason Code 字典。',
        'Publishes source-aligned Channel, device, message-flag, and Reason Code dictionaries.',
        [
          publishedPage(
            'channel-types',
            'Channel Type',
            'Channel Type',
            '列出当前 1–12 Channel Type，并标注基础、专用和旧类型边界。',
            'Lists current Channel Types 1–12 with baseline, specialized, and legacy boundaries.',
          ),
          publishedPage(
            'device-flags',
            'Device Flag',
            'Device Flag',
            '列出 APP、WEB、PC、SYSTEM 与 Device Level 冲突策略。',
            'Lists APP, WEB, PC, SYSTEM, and Device Level conflict policies.',
          ),
          publishedPage(
            'message-flags',
            'Message Flags',
            'Message Flags',
            '列出固定 Header 与 Setting 位，并解释持久化、红点、命令、回执和流语义。',
            'Lists fixed-header and Setting bits for persistence, red dots, commands, receipts, and streams.',
          ),
          publishedPage(
            'reason-codes',
            'Reason Code',
            'Reason Code',
            '完整列出当前 0–29 协议枚举并标注使用阶段、重试和可达性。',
            'Lists the complete current 0–29 protocol enum with stage, retry, and reachability guidance.',
          ),
        ],
      ),
      publishedGroup(
        'specifications',
        '规范下载',
        'Specifications',
        '提供校准后、可机器读取的接口与协议规范。',
        'Provides aligned, machine-readable API and protocol specifications.',
        [
          publishedPage(
            'openapi',
            'OpenAPI',
            'OpenAPI',
            '在线浏览并下载校准后的 v3 HTTP API 规范。',
            'Browse and download the aligned v3 HTTP API specification.',
          ),
          publishedPage(
            'json-rpc-schema',
            'JSON-RPC Schema',
            'JSON-RPC Schema',
            '浏览并下载 WebSocket JSON-RPC Schema。',
            'Browse and download the WebSocket JSON-RPC schema.',
          ),
          publishedPage(
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

/** Converts one validated relative navigation path into Next.js route segments. */
export function navigationPathSegments(slug: string): string[] {
  const segments = slug.split('/');
  if (segments.some((segment) => segment.length === 0 || segment === '.' || segment === '..')) {
    throw new Error(`invalid navigation path: ${slug}`);
  }
  return segments;
}

/** Resolves the route base for a group's children independently from sidebar nesting. */
export function navigationChildParentSlugs(
  group: NavigationGroup,
  groupSlugs: string[],
): string[] {
  return group.childrenAtDomainRoot ? [] : groupSlugs;
}

/** Returns every domain, folder index, and leaf page in display order. */
export function getAllNavigationEntries(locale: Locale): NavigationEntry[] {
  return domains.flatMap((domain) => {
    const entries = [entryFromPage(locale, domain, domain, [], 'domain')];

    function appendNodes(nodes: NavigationNode[], parentSlugs: string[]) {
      for (const node of nodes) {
        const slugs = [...parentSlugs, ...navigationPathSegments(node.slug)];
        entries.push(
          entryFromPage(
            locale,
            domain,
            node,
            slugs,
            isNavigationGroup(node) ? 'group' : 'page',
          ),
        );
        if (isNavigationGroup(node)) {
          appendNodes(node.children, navigationChildParentSlugs(node, slugs));
        }
      }
    }

    for (const page of domain.pages) {
      entries.push(entryFromPage(locale, domain, page, navigationPathSegments(page.slug), 'page'));
    }

    appendNodes(domain.groups, []);

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
