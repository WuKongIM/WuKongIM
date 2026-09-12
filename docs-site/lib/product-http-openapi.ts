import goldenPathDocument from '../contracts/javascript-web-quickstart.openapi.json';
import managementDocument from '../contracts/product-http-management.openapi.json';
import messagingDocument from '../contracts/product-http-messaging.openapi.json';
import completeDocument from '../contracts/product-http.openapi.json';
import { applyProductHTTPParameterExplanations } from './product-http-parameter-explanations';
import {
  applyProductHTTPOperationSemantics,
  getProductHTTPOperationSemantics,
  type ProductHTTPOperationSemantics,
} from './product-http-operation-semantics';

export type ProductHTTPOpenAPILocale = 'zh' | 'en';
export const productHTTPOpenAPIContractNames = [
  'complete',
  'golden-path',
  'management',
  'messaging',
] as const;
export const productHTTPOpenAPIReferenceContractNames = ['complete'] as const;
export type ProductHTTPOpenAPIContract =
  (typeof productHTTPOpenAPIContractNames)[number];
export type ProductHTTPOpenAPIMethod = 'get' | 'post';

export interface ProductHTTPOpenAPILocalizedText {
  zh: string;
  en: string;
}

type LocalizedText = ProductHTTPOpenAPILocalizedText;

interface OperationObject {
  operationId?: string;
  summary?: string;
  description?: string;
  tags?: string[];
  'x-codeSamples'?: unknown[];
  'x-wukongim-trust'?: string;
  'x-i18n'?: {
    zh?: {
      summary?: string;
      description?: string;
    };
  };
}

interface ContractDocument {
  paths: Record<string, Record<string, OperationObject>>;
  'x-wukongim-scope'?: string;
}

export interface ProductHTTPOpenAPIContractDescriptor {
  /** Imported OpenAPI document used by generation, rendering, and search exports. */
  document: { paths: Record<string, unknown> };
  /** Repository-relative source path used by edit links. */
  source: string;
  /** Public static-export URL for the downloadable contract. */
  download: string;
  /** Stable base schema ID; the locale suffix is added by the loader. */
  documentId: string;
  /** Localized short name used by machine-readable Markdown exports. */
  label: LocalizedText;
  /** Localized trust and publication boundary used by Markdown exports. */
  llmScope: LocalizedText;
}

/** Single source of truth for every published Product HTTP OpenAPI contract. */
export const productHTTPOpenAPIContracts = {
  complete: {
    document: completeDocument,
    source: 'docs-site/contracts/product-http.openapi.json',
    download: '/contracts/product-http.openapi.json',
    documentId: 'wukongim-product-http-complete-beta',
    label: { zh: '完整运行时合同', en: 'complete runtime contract' },
    llmScope: {
      zh: '此机器可读摘要与页面中的 Fumadocs 参考来自完整的 42 操作运行时合同；Product HTTP 没有内建鉴权，只能由受信后端或运维边界调用。',
      en: 'This machine-readable summary and the Fumadocs reference on the page come from the complete 42-operation runtime contract. Product HTTP has no built-in authentication and is callable only from a trusted backend or operator boundary.',
    },
  },
  'golden-path': {
    document: goldenPathDocument,
    source: 'docs-site/contracts/javascript-web-quickstart.openapi.json',
    download: '/contracts/javascript-web-quickstart.openapi.json',
    documentId: 'wukongim-product-http-beta',
    label: { zh: '黄金路径子集', en: 'golden-path subset' },
    llmScope: {
      zh: '此机器可读摘要与页面中的 Fumadocs 参考来自同一份黄金路径 Beta 子集合同；Product HTTP 只能由受信后端调用。',
      en: 'This machine-readable summary and the Fumadocs reference on the page come from the same golden-path Beta subset contract. Product HTTP is callable only from a trusted backend.',
    },
  },
  management: {
    document: managementDocument,
    source: 'docs-site/contracts/product-http-management.openapi.json',
    download: '/contracts/product-http-management.openapi.json',
    documentId: 'wukongim-product-http-management-beta',
    label: { zh: '管理子集', en: 'management subset' },
    llmScope: {
      zh: '此机器可读摘要与页面中的 Fumadocs 参考来自同一份非穷举管理 Beta 子集合同；这些无内建鉴权的 Product HTTP 入口只能由受信后端或运维边界调用。',
      en: 'This machine-readable summary and the Fumadocs reference on the page come from the same non-exhaustive management Beta subset contract. These Product HTTP routes have no built-in authentication and are callable only from a trusted backend or operator boundary.',
    },
  },
  messaging: {
    document: messagingDocument,
    source: 'docs-site/contracts/product-http-messaging.openapi.json',
    download: '/contracts/product-http-messaging.openapi.json',
    documentId: 'wukongim-product-http-messaging-beta',
    label: { zh: '消息发送子集', en: 'message-sending subset' },
    llmScope: {
      zh: '此机器可读摘要与页面中的 Fumadocs 参考来自同一份非穷举消息发送 Beta 子集合同；该无内建鉴权的 Product HTTP 入口只能由受信后端调用。',
      en: 'This machine-readable summary and the Fumadocs reference on the page come from the same non-exhaustive message-sending Beta subset contract. This Product HTTP route has no built-in authentication and is callable only from a trusted backend.',
    },
  },
} as const satisfies Record<
  ProductHTTPOpenAPIContract,
  ProductHTTPOpenAPIContractDescriptor
>;

/** Backward-compatible source/download projection used by edit links. */
export const productHTTPOpenAPIContractFiles = Object.fromEntries(
  productHTTPOpenAPIContractNames.map((contract) => {
    const { source, download } = productHTTPOpenAPIContracts[contract];
    return [contract, { source, download }];
  }),
) as {
  [Contract in ProductHTTPOpenAPIContract]: Pick<
    (typeof productHTTPOpenAPIContracts)[Contract],
    'source' | 'download'
  >;
};

export interface ProductHTTPOpenAPIOperation {
  /** Contract document that owns the operation. */
  contract: ProductHTTPOpenAPIContract;
  /** Route segment of the parent OpenAPI tag group. */
  groupSlug: string;
  /** Stable operation route segment, sourced from operationId. */
  slug: string;
  /** Supported HTTP method used by rendering and navigation badges. */
  method: ProductHTTPOpenAPIMethod;
  /** OpenAPI path template matched by the operation. */
  path: string;
  /** Localized operation title used by pages and navigation. */
  title: LocalizedText;
  /** Localized concise operation description. */
  description: LocalizedText;
  /** Required caller boundary declared by the complete contract. */
  trust: string;
  /** Reviewed runtime behavior that JSON Schema cannot express. */
  semantics?: ProductHTTPOperationSemantics;
}

export interface ProductHTTPOpenAPIDeferral {
  /** Exact HTTP methods and paths excluded from the published beta subset. */
  routes: string[];
  /** Localized concise reason the routes remain unpublished. */
  reason: LocalizedText;
}

export interface ProductHTTPOpenAPIDeferrals {
  /** Localized heading shown after the published operation cards. */
  title: LocalizedText;
  /** Deferred route groups and their publication blockers. */
  items: ProductHTTPOpenAPIDeferral[];
}

export interface ProductHTTPOpenAPIGroup {
  /** Contract document that owns every operation in the group. */
  contract: ProductHTTPOpenAPIContract;
  /** Stable route segment for the tag index. */
  slug: string;
  /** Exact OpenAPI tag used to select operations. */
  tag: string;
  /** Localized tag-index title. */
  title: LocalizedText;
  /** Localized concise tag-index description. */
  description: LocalizedText;
  /** Published operations rendered as one page per operation. */
  operations: ProductHTTPOpenAPIOperation[];
  /** Optional explicit boundary for related routes that are not yet published. */
  deferrals?: ProductHTTPOpenAPIDeferrals;
}

interface GroupDefinition {
  contract: ProductHTTPOpenAPIContract;
  slug: string;
  tag: string;
  title: LocalizedText;
  description: LocalizedText;
  deferrals?: ProductHTTPOpenAPIDeferrals;
}

const referenceGroupDefinitions: GroupDefinition[] = [
  {
    contract: 'complete',
    slug: 'users',
    tag: 'Users',
    title: { zh: '用户', en: 'Users' },
    description: {
      zh: '设备 Token、在线状态与系统身份。',
      en: 'Device tokens, presence, and system identities.',
    },
  },
  {
    contract: 'complete',
    slug: 'routing',
    tag: 'Routing',
    title: { zh: '路由发现', en: 'Route Discovery' },
    description: {
      zh: '客户端 Gateway 公网或内网地址。',
      en: 'Public or intranet client Gateway addresses.',
    },
  },
  {
    contract: 'complete',
    slug: 'messages',
    tag: 'Messages',
    title: { zh: '消息', en: 'Messages' },
    description: {
      zh: '消息恢复、事件与命令消息兼容接口。',
      en: 'Message recovery, events, and command-message compatibility.',
    },
  },
  {
    contract: 'complete',
    slug: 'message-send',
    tag: 'Message Sending',
    title: { zh: '消息发送', en: 'Message Sending' },
    description: {
      zh: '由受信后端提交消息。',
      en: 'Submit messages from a trusted backend.',
    },
  },
  {
    contract: 'complete',
    slug: 'channels',
    tag: 'Channels',
    title: { zh: 'Channel', en: 'Channels' },
    description: {
      zh: 'Channel 元数据、订阅者与名单管理。',
      en: 'Channel metadata, subscribers, and list administration.',
    },
  },
  {
    contract: 'complete',
    slug: 'conversations',
    tag: 'Conversations',
    title: { zh: '会话', en: 'Conversations' },
    description: {
      zh: '会话同步、未读、隐藏与激活状态。',
      en: 'Conversation sync, unread, hide, and activation state.',
    },
  },
];

/** Narrow profile groups retained for profile-specific tests and downloads. */
const profileGroupDefinitions: GroupDefinition[] = [
  {
    contract: 'golden-path',
    slug: 'users',
    tag: 'Users',
    title: { zh: '用户', en: 'Users' },
    description: {
      zh: '开发身份的设备 Token 元数据。',
      en: 'Device-token metadata for development identities.',
    },
  },
  {
    contract: 'golden-path',
    slug: 'routing',
    tag: 'Routing',
    title: { zh: '路由发现', en: 'Route Discovery' },
    description: {
      zh: '客户端 Gateway 接入地址。',
      en: 'Client Gateway ingress addresses.',
    },
  },
  {
    contract: 'golden-path',
    slug: 'messages',
    tag: 'Messages',
    title: { zh: '消息', en: 'Messages' },
    description: {
      zh: '已提交 Channel 消息的有界同步。',
      en: 'Bounded synchronization of committed Channel messages.',
    },
  },
  {
    contract: 'messaging',
    slug: 'message-send',
    tag: 'Message Sending',
    title: { zh: '消息发送', en: 'Message Sending' },
    description: {
      zh: '向 Channel 提交普通持久消息。',
      en: 'Submit ordinary persistent messages to a Channel.',
    },
  },
  {
    contract: 'management',
    slug: 'channels',
    tag: 'Channels',
    title: { zh: 'Channel', en: 'Channels' },
    description: {
      zh: 'Channel 元数据、订阅者与允许或拒绝名单。',
      en: 'Channel metadata, subscribers, and allow or deny lists.',
    },
    deferrals: {
      title: { zh: '本 Beta 子集暂不发布', en: 'Not published in this beta subset' },
      items: [
        {
          routes: ['POST /channel/info'],
          reason: {
            zh: '缺少字段校验，且采用全量替换而非局部更新。',
            en: 'It lacks field validation and replaces the full record instead of patching it.',
          },
        },
        {
          routes: ['POST /channel/delete'],
          reason: {
            zh: 'Key 校验较弱，且解散为终态。',
            en: 'It has weak key validation and terminal disband semantics.',
          },
        },
        {
          routes: ['POST /channel/subscriber_remove'],
          reason: {
            zh: 'channel_type 为 0 时的行为与添加订阅者不一致。',
            en: 'Type-zero behavior differs from subscriber add.',
          },
        },
        {
          routes: ['POST /channel/blacklist_set', 'POST /channel/whitelist_set'],
          reason: {
            zh: '全量替换的输入校验较弱。',
            en: 'Full-replacement input validation is weak.',
          },
        },
        {
          routes: ['GET /channel/whitelist'],
          reason: {
            zh: '查询参数未校验，且返回无界完整列表。',
            en: 'Its query is unvalidated and returns an unbounded full list.',
          },
        },
      ],
    },
  },
  {
    contract: 'management',
    slug: 'conversations',
    tag: 'Conversations',
    title: { zh: '会话', en: 'Conversations' },
    description: {
      zh: '会话同步、未读、隐藏与激活状态。',
      en: 'Conversation sync, unread, hide, and activation state.',
    },
    deferrals: {
      title: { zh: '本 Beta 子集暂不发布', en: 'Not published in this beta subset' },
      items: [
        {
          routes: ['POST /conversation/sync'],
          reason: {
            zh: '旧式分隔输入、裸数组响应、旧消息投影且无完成标记，需另行定义兼容合同。',
            en: 'Delimited input, a bare-array response, the old message projection, and no completion signal require a separate compatibility contract.',
          },
        },
      ],
    },
  },
];

function documentFor(contract: ProductHTTPOpenAPIContract): ContractDocument {
  return productHTTPOpenAPIContracts[contract].document as ContractDocument;
}

function isProductHTTPOpenAPIMethod(method: string): method is ProductHTTPOpenAPIMethod {
  return method === 'get' || method === 'post';
}

function operationsFor(definition: GroupDefinition): ProductHTTPOpenAPIOperation[] {
  const operations: ProductHTTPOpenAPIOperation[] = [];

  for (const [path, pathItem] of Object.entries(documentFor(definition.contract).paths)) {
    for (const [method, operation] of Object.entries(pathItem)) {
      if (!operation.tags?.includes(definition.tag)) continue;
      if (!isProductHTTPOpenAPIMethod(method)) {
        throw new Error(`Unsupported Product HTTP method: ${method.toUpperCase()} ${path}`);
      }
      if (!operation.operationId || !operation.summary || !operation.description) {
        throw new Error(`OpenAPI operation metadata is incomplete: ${method.toUpperCase()} ${path}`);
      }
      if (!operation['x-wukongim-trust']) {
        throw new Error(`OpenAPI operation trust is missing: ${method.toUpperCase()} ${path}`);
      }
      operations.push({
        contract: definition.contract,
        groupSlug: definition.slug,
        slug: operation.operationId,
        method,
        path,
        title: {
          zh: operation['x-i18n']?.zh?.summary ?? operation.summary,
          en: operation.summary,
        },
        description: {
          zh: operation['x-i18n']?.zh?.description ?? operation.description,
          en: operation.description,
        },
        trust: operation['x-wukongim-trust'],
        semantics: getProductHTTPOperationSemantics(method, path),
      });
    }
  }

  if (operations.length === 0) {
    throw new Error(`OpenAPI tag has no operations: ${definition.tag}`);
  }
  return operations;
}

/** Route and navigation registry derived from the complete Product HTTP contract. */
export const productHTTPOpenAPIReferenceGroups: ProductHTTPOpenAPIGroup[] =
  referenceGroupDefinitions.map((definition) => ({
    ...definition,
    operations: operationsFor(definition),
  }));

export const productHTTPOpenAPIReferenceOperations =
  productHTTPOpenAPIReferenceGroups.flatMap((group) => group.operations);

const productHTTPOpenAPIProfileGroups: ProductHTTPOpenAPIGroup[] =
  profileGroupDefinitions.map((definition) => ({
    ...definition,
    operations: operationsFor(definition),
  }));

export const productHTTPManagementOpenAPIGroups =
  productHTTPOpenAPIProfileGroups.filter((group) => group.contract === 'management');

export const productHTTPMessagingOpenAPIGroups =
  productHTTPOpenAPIProfileGroups.filter((group) => group.contract === 'messaging');

/** Applies reviewed x-i18n text without duplicating the OpenAPI structure. */
export function localizeOpenAPIDocument<T>(
  document: T,
  locale: ProductHTTPOpenAPILocale,
): T {
  const localized = structuredClone(document);

  function visit(value: unknown) {
    if (Array.isArray(value)) {
      for (const item of value) visit(item);
      return;
    }
    if (!value || typeof value !== 'object') return;

    const record = value as Record<string, unknown>;
    const translations = record['x-i18n'];
    if (translations && typeof translations === 'object' && !Array.isArray(translations)) {
      const selected = (translations as Record<string, unknown>)[locale];
      if (selected && typeof selected === 'object' && !Array.isArray(selected)) {
        Object.assign(record, selected);
      }
      delete record['x-i18n'];
    }
    for (const child of Object.values(record)) visit(child);
  }

  visit(localized);
  if (
    (document as ContractDocument)['x-wukongim-scope'] ===
    'complete-source-aligned-product-http-runtime'
  ) {
    const profileOperations = new Map<string, OperationObject>();
    for (const profile of [goldenPathDocument, managementDocument, messagingDocument]) {
      for (const [path, pathItem] of Object.entries(
        (profile as ContractDocument).paths,
      )) {
        for (const [method, operation] of Object.entries(pathItem)) {
          profileOperations.set(`${method.toUpperCase()} ${path}`, operation);
        }
      }
    }
    for (const [path, pathItem] of Object.entries(
      (localized as ContractDocument).paths,
    )) {
      for (const [method, operation] of Object.entries(pathItem)) {
        if (operation['x-codeSamples']) continue;
        const sampleSource = profileOperations.get(`${method.toUpperCase()} ${path}`);
        if (sampleSource?.['x-codeSamples']) {
          operation['x-codeSamples'] = structuredClone(sampleSource['x-codeSamples']);
        }
      }
    }
    visit(localized);
  }
  applyProductHTTPParameterExplanations(localized, locale);
  applyProductHTTPOperationSemantics(localized, locale);
  return localized;
}
