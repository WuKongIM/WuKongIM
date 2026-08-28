import goldenPathDocument from '../contracts/javascript-web-quickstart.openapi.json';
import managementDocument from '../contracts/product-http-management.openapi.json';

export type ProductHTTPOpenAPILocale = 'zh' | 'en';
export type ProductHTTPOpenAPIContract = 'golden-path' | 'management';
export type ProductHTTPOpenAPIMethod = 'get' | 'post';

interface LocalizedText {
  zh: string;
  en: string;
}

interface OperationObject {
  operationId?: string;
  summary?: string;
  description?: string;
  tags?: string[];
  'x-i18n'?: {
    zh?: {
      summary?: string;
      description?: string;
    };
  };
}

interface ContractDocument {
  paths: Record<string, Record<string, OperationObject>>;
}

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

const groupDefinitions: GroupDefinition[] = [
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
  return (contract === 'golden-path' ? goldenPathDocument : managementDocument) as ContractDocument;
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
      });
    }
  }

  if (operations.length === 0) {
    throw new Error(`OpenAPI tag has no operations: ${definition.tag}`);
  }
  return operations;
}

/** Route and navigation registry derived from the two published OpenAPI contracts. */
export const productHTTPOpenAPIReferenceGroups: ProductHTTPOpenAPIGroup[] =
  groupDefinitions.map((definition) => ({
    ...definition,
    operations: operationsFor(definition),
  }));

export const productHTTPOpenAPIReferenceOperations =
  productHTTPOpenAPIReferenceGroups.flatMap((group) => group.operations);

export const productHTTPManagementOpenAPIGroups =
  productHTTPOpenAPIReferenceGroups.filter((group) => group.contract === 'management');

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
  return localized;
}
