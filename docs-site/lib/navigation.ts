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
    '选择 iOS、Android、Flutter 或 Web 快速接入，并用已验证的正式发布包与源码 example 完成在线双向消息。',
    'Choose an iOS, Android, Flutter, or Web quickstart and use verified released packages and source examples for online bidirectional messaging.',
    [
      publishedPage(
        'examples',
        '运行官方示例',
        'Run Official Examples',
        '启动同一版 WuKongIM，复现四端正式包与源码 example 的在线双向消息、断开和清理。',
        'Start the same WuKongIM revision and reproduce online bidirectional messaging, disconnect, and cleanup with four released packages and source examples.',
      ),
      publishedPage(
        'ios/getting-started',
        'iOS 快速接入',
        'iOS quickstart',
        '精确安装 v1.1.1，完成单聊收发、监听清理和已验证的 Alice/Bob 正式包验收。',
        'Install exactly v1.1.1 for person messaging, listener cleanup, and verified released-package Alice/Bob acceptance.',
      ),
      publishedPage(
        'android/getting-started',
        'Android 快速接入',
        'Android quickstart',
        '精确安装 v1.0.5，处理单例、单聊收发、清理和已验证的 Alice/Bob 正式包验收。',
        'Install exactly v1.0.5 for singleton ownership, person messaging, cleanup, and verified released-package Alice/Bob acceptance.',
      ),
      publishedPage(
        'flutter/getting-started',
        'Flutter 快速接入',
        'Flutter quickstart',
        '精确安装并运行已验证的 v1.1.0 example，完成单聊收发、dispose 清理和 Alice/Bob 验收。',
        'Install and run the verified v1.1.0 example for person messaging, dispose cleanup, and Alice/Bob acceptance.',
      ),
      publishedPage(
        'javascript/getting-started',
        'Web 快速接入',
        'Web quickstart',
        '精确安装 easyjssdk v2.0.4，在真实浏览器与正式包对端中完成 Alice/Bob 在线消息。',
        'Install exactly easyjssdk v2.0.4 for Alice/Bob online messaging in a real browser and released-package peer runs.',
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
        '判断 WuKongIM 是什么、是否适合你的产品，以及下一步如何验证。',
        'Decide what WuKongIM is, whether it fits your product, and how to validate it.',
        [
          publishedPage(
            'capabilities',
            '核心能力',
            'Core Capabilities',
            '从产品结果理解实时接入、可靠消息、多设备、集群和运维能力。',
            'Explains real-time access, reliable messaging, multi-device, cluster, and operations outcomes.',
          ),
          publishedPage(
            'use-cases',
            '适用场景',
            'Use Cases',
            '判断聊天、通知、客服及扩展场景如何使用 WuKongIM。',
            'Maps chat, notifications, customer service, and extended scenarios to WuKongIM.',
          ),
        ],
      ),
      publishedGroup(
        'quick-start',
        '快速开始',
        'Quick Start',
        '在 Linux 上启动集群、发送消息并验证结果。',
        'Start a Linux cluster, send a message, and verify the result.',
        [
          publishedPage(
            'prerequisites',
            '环境准备',
            'Prerequisites',
            '列出 Linux、sudo、SSH、端口和目录要求。',
            'Lists Linux, sudo, SSH, port, and directory requirements.',
          ),
          publishedPage(
            'single-node-cluster',
            '启动单节点集群',
            'Start a Single-node Cluster',
            '安装软件包并通过 systemd 启动单节点集群。',
            'Installs the package and starts a single-node cluster with systemd.',
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
            '上线检查',
            'Release Checks',
            '发布前检查身份、连接、消息、离线恢复、安全、容量和回滚。',
            'Checks identity, connection, messaging, offline recovery, security, capacity, and rollback before release.',
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
        '从 Docker、Linux 或多节点路径完成服务端部署。',
        'Deploy the server through the Docker, Linux, or multi-node path.',
        [
          publishedPage(
            'docker',
            'Docker 部署',
            'Docker',
            '使用固定的官方镜像、显式配置和持久卷运行节点。',
            'Runs a node with a pinned official image, explicit configuration, and persistent storage.',
          ),
          publishedPage(
            'linux',
            'Linux 部署',
            'Linux',
            '从签名 APT/DNF Preview 软件源安装，并用安全配置和 systemd 运行服务。',
            'Installs from the signed APT/DNF preview repository and runs the service with secure configuration and systemd.',
          ),
          publishedPage(
            'multi-node',
            '多节点集群',
            'Multi-node Cluster',
            '规划成员、副本、故障域并验证集群就绪。',
            'Plans membership, replicas, failure domains, and cluster readiness.',
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
            'common-configurations',
            '常用配置',
            'Common Configurations',
            '以表格解释高频配置项及其关键边界。',
            'Explains frequently used settings and their key boundaries in a table.',
          ),
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
            '逐项说明全部公开 TOML、环境变量、关键默认值、约束和迁移方式。',
            'Explains every public TOML field, environment override, key default, constraint, and migration.',
          ),
        ],
      ),
      publishedGroup(
        'operations',
        '运维',
        'Operations',
        '从日常检查开始，安全地监控、备份、扩缩容和升级集群。',
        'Start with daily checks, then monitor, back up, scale, and upgrade the cluster safely.',
        [
          publishedPage(
            'manager',
            'Manager 管理后台',
            'Manager',
            '登录管理后台，看懂主要页面并安全执行操作。',
            'Sign in, understand the main pages, and perform administrative actions safely.',
          ),
          publishedPage(
            'health-and-monitoring',
            '健康检查与监控',
            'Health & Monitoring',
            '判断进程是否存活、节点能否接流量，以及何时需要告警。',
            'Tell whether the process is alive, the node can accept traffic, and an alert is needed.',
          ),
          publishedPage(
            'scaling',
            '扩容与缩容',
            'Scaling',
            '逐步增加节点，或安全排空并移除节点。',
            'Add a node step by step, or safely drain and remove one.',
          ),
          publishedPage(
            'backup-and-restore',
            '备份与恢复',
            'Backup & Restore',
            '创建、测试和验证备份，并在维护窗口中恢复。',
            'Create, test, and verify backups, then restore during maintenance.',
          ),
          publishedPage(
            'upgrade-and-migration',
            '升级与迁移',
            'Upgrade & Migration',
            '根据发布说明选择滚动升级或停机升级。',
            'Use release notes to choose a rolling or stopped upgrade.',
          ),
          publishedPage(
            'troubleshooting',
            '故障排查',
            'Troubleshooting',
            '从故障现象开始，用低风险检查逐步定位问题。',
            'Start from the symptom and narrow the problem with low-risk checks.',
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
