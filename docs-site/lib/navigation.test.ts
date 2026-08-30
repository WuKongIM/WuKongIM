import { describe, expect, test } from 'bun:test';
import { isValidElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import {
  domains,
  getAllNavigationEntries,
  getIndexedNavigationEntries,
  getNavigationEntry,
  isPublishedContentPath,
  locales,
  parseLocale,
} from './navigation';
import { buildLayoutTabs, buildPageTree } from './navigation-tree';
import { productHTTPOpenAPIReferenceGroups } from './product-http-openapi';

describe('documentation navigation contract', () => {
  test('exposes the agreed locales and documentation domains in order', () => {
    expect(locales).toEqual(['zh', 'en']);
    expect(domains.map((domain) => domain.key)).toEqual(['guide', 'server', 'sdk', 'api']);
    expect(domains.map((domain) => domain.label.zh)).toEqual([
      '指南',
      '服务端',
      'SDK',
      'API 与协议',
    ]);
    expect(domains.map((domain) => domain.label.en)).toEqual([
      'Guides',
      'Server',
      'SDK',
      'API & Protocols',
    ]);
  });

  test('keeps the agreed first-level menu groups for every domain', () => {
    const byKey = Object.fromEntries(domains.map((domain) => [domain.key, domain]));

    expect(byKey.guide.groups.map((group) => group.slug)).toEqual([
      'product-overview',
      'quick-start',
      'core-concepts',
      'integration',
      'tutorials',
    ]);
    expect(byKey.server.groups.map((group) => group.slug)).toEqual([
      'deployment',
      'configuration',
      'operations',
      'tools',
      'architecture',
    ]);
    expect(byKey.sdk.groups.map((group) => group.slug)).toEqual([
      'easy',
      'common-guides',
      'android',
      'ios',
      'javascript',
      'flutter',
      'uniapp',
      'harmonyos',
    ]);
    expect(byKey.api.groups.map((group) => group.slug)).toEqual([
      'product-http',
      'operations-http',
      'webhooks',
      'client-protocols',
      'dictionaries',
      'specifications',
    ]);
    expect(byKey.sdk.pages.map((page) => page.slug)).toEqual(['choose-sdk', 'compatibility']);
    expect(byKey.api.pages.map((page) => page.slug)).toEqual([
      'conventions',
      'authentication',
      'compatibility',
      'interface-inventory',
    ]);
  });

  test('publishes the complete server architecture path', () => {
    const server = domains.find((domain) => domain.key === 'server');
    const architecture = server?.groups.find((group) => group.slug === 'architecture');

    expect(architecture?.status).toBe('published');
    expect(architecture?.children.map((page) => page.slug)).toEqual([
      'controller',
      'slots',
      'channels',
      'transport',
      'message-flow',
      'user-routing',
    ]);
    expect(architecture?.children.every((page) => page.status === 'published')).toBe(true);
  });

  test('publishes the complete guide foundation path', () => {
    const guide = domains.find((domain) => domain.key === 'guide');
    const productOverview = guide?.groups.find((group) => group.slug === 'product-overview');
    const coreConcepts = guide?.groups.find((group) => group.slug === 'core-concepts');
    const integration = guide?.groups.find((group) => group.slug === 'integration');

    expect(
      productOverview?.children
        .filter((page) => ['capabilities', 'use-cases'].includes(page.slug))
        .map((page) => [page.slug, page.status]),
    ).toEqual([
      ['capabilities', 'published'],
      ['use-cases', 'published'],
    ]);
    expect(coreConcepts?.children.map((page) => [page.slug, page.status])).toEqual([
      ['messages', 'published'],
      ['channels', 'published'],
      ['users', 'published'],
      ['devices', 'published'],
      ['conversations', 'published'],
    ]);
    expect(
      integration?.children
        .filter((page) => ['plugins', 'acceptance'].includes(page.slug))
        .map((page) => [page.slug, page.status]),
    ).toEqual([
      ['plugins', 'published'],
      ['acceptance', 'published'],
    ]);
  });

  test('publishes the complete scenario tutorial set', () => {
    const guide = domains.find((domain) => domain.key === 'guide');
    const tutorials = guide?.groups.find((group) => group.slug === 'tutorials');

    expect(tutorials?.status).toBe('published');
    expect(tutorials?.children.map((page) => [page.slug, page.status])).toEqual([
      ['direct-chat', 'published'],
      ['large-groups', 'published'],
      ['push', 'published'],
      ['ai-and-iot', 'published'],
    ]);
  });

  test('keeps the Phase 12 JavaScript golden path published as a narrow profile', () => {
    const published = getIndexedNavigationEntries('en').map((entry) => entry.url);
    const phase12Routes = [
      '/en/sdk/compatibility',
      '/en/sdk/javascript',
      '/en/sdk/javascript/installation',
      '/en/sdk/javascript/quickstart',
      '/en/api/conventions',
      '/en/api/authentication',
      '/en/api/compatibility',
      '/en/api/product-http',
      '/en/api/product-http/users',
      '/en/api/product-http/messages',
      '/en/api/product-http/routing',
      '/en/api/product-http/errors',
      '/en/api/dictionaries',
      '/en/api/dictionaries/reason-codes',
    ];

    expect(published).toEqual(expect.arrayContaining(phase12Routes));
    expect(getNavigationEntry('en', 'sdk', ['javascript', 'api-reference'])?.status).toBe(
      'planned',
    );
    expect(getNavigationEntry('en', 'api', ['specifications', 'openapi'])?.status).toBe(
      'published',
    );
  });

  test('keeps the Phase 13 integrator foundations without claiming unrelated platform support', () => {
    const published = getIndexedNavigationEntries('en').map((entry) => entry.url);
    const phase13Routes = [
      '/en/sdk/choose-sdk',
      '/en/sdk/common-guides',
      '/en/sdk/common-guides/identity-and-token',
      '/en/sdk/common-guides/initialization-and-connection',
      '/en/sdk/common-guides/messaging',
      '/en/sdk/common-guides/custom-messages',
      '/en/sdk/common-guides/conversations-and-unread',
      '/en/sdk/common-guides/offline-and-push',
      '/en/sdk/common-guides/multi-device',
      '/en/sdk/common-guides/reconnect-and-errors',
      '/en/api/dictionaries/channel-types',
      '/en/api/dictionaries/device-flags',
      '/en/api/dictionaries/message-flags',
    ];

    expect(published).toEqual(expect.arrayContaining(phase13Routes));
    expect(getNavigationEntry('en', 'sdk', ['choose-sdk'])?.status).toBe('published');
    expect(getNavigationEntry('en', 'sdk', ['uniapp'])?.status).toBe('planned');
    expect(getNavigationEntry('en', 'sdk', ['javascript'])?.status).toBe('published');
  });

  test('publishes the Phase 14 acceptance loop without broadening SDK support', () => {
    const published = getIndexedNavigationEntries('en').map((entry) => entry.url);

    expect(published).toEqual(
      expect.arrayContaining([
        '/en/guide/integration/acceptance',
        '/en/sdk/javascript/platform-capabilities',
      ]),
    );
    expect(
      getNavigationEntry('en', 'sdk', ['javascript', 'platform-capabilities'])?.status,
    ).toBe('published');
    expect(getNavigationEntry('en', 'sdk', ['javascript', 'api-reference'])?.status).toBe(
      'planned',
    );
    expect(getNavigationEntry('en', 'sdk', ['javascript', 'upgrade'])?.status).toBe('planned');
    expect(getNavigationEntry('en', 'sdk', ['uniapp'])?.status).toBe('planned');
  });

  test('publishes the narrow Phase 19 iOS path without publishing unverified chapters', () => {
    const published = getIndexedNavigationEntries('en').map((entry) => entry.url);

    expect(published).toEqual(
      expect.arrayContaining([
        '/en/sdk/ios',
        '/en/sdk/ios/installation',
        '/en/sdk/ios/quickstart',
      ]),
    );
    expect(
      ['platform-capabilities', 'api-reference', 'upgrade'].map((slug) => [
        slug,
        getNavigationEntry('en', 'sdk', ['ios', slug])?.status,
      ]),
    ).toEqual([
      ['platform-capabilities', 'planned'],
      ['api-reference', 'planned'],
      ['upgrade', 'planned'],
    ]);
  });

  test('publishes the narrow Phase 20 Android path without claiming runtime verification', () => {
    const published = getIndexedNavigationEntries('en').map((entry) => entry.url);

    expect(published).toEqual(
      expect.arrayContaining([
        '/en/sdk/android',
        '/en/sdk/android/installation',
        '/en/sdk/android/quickstart',
      ]),
    );
    expect(
      ['platform-capabilities', 'api-reference', 'upgrade'].map((slug) => [
        slug,
        getNavigationEntry('en', 'sdk', ['android', slug])?.status,
      ]),
    ).toEqual([
      ['platform-capabilities', 'planned'],
      ['api-reference', 'planned'],
      ['upgrade', 'planned'],
    ]);
  });

  test('publishes the narrow Phase 21 Flutter path without claiming runtime verification', () => {
    const published = getIndexedNavigationEntries('en').map((entry) => entry.url);

    expect(published).toEqual(
      expect.arrayContaining([
        '/en/sdk/flutter',
        '/en/sdk/flutter/installation',
        '/en/sdk/flutter/quickstart',
      ]),
    );
    expect(
      ['platform-capabilities', 'api-reference', 'upgrade'].map((slug) => [
        slug,
        getNavigationEntry('en', 'sdk', ['flutter', slug])?.status,
      ]),
    ).toEqual([
      ['platform-capabilities', 'planned'],
      ['api-reference', 'planned'],
      ['upgrade', 'planned'],
    ]);
  });

  test('publishes the narrow Phase 22 HarmonyOS path without claiming a site build', () => {
    const published = getIndexedNavigationEntries('en').map((entry) => entry.url);

    expect(published).toEqual(
      expect.arrayContaining([
        '/en/sdk/harmonyos',
        '/en/sdk/harmonyos/installation',
        '/en/sdk/harmonyos/quickstart',
      ]),
    );
    expect(
      ['platform-capabilities', 'api-reference', 'upgrade'].map((slug) => [
        slug,
        getNavigationEntry('en', 'sdk', ['harmonyos', slug])?.status,
      ]),
    ).toEqual([
      ['platform-capabilities', 'planned'],
      ['api-reference', 'planned'],
      ['upgrade', 'planned'],
    ]);
  });

  test('keeps EasySDK planned while Product JSON-RPC CONNECT is unsupported', () => {
    const sdk = domains.find((domain) => domain.key === 'sdk');
    const easy = sdk?.groups.find((group) => group.slug === 'easy');
    const published = getIndexedNavigationEntries('en').map((entry) => entry.url);
    const snapshots = new Map([
      ['ios/getting-started', 'v1.0.3'],
      ['android/getting-started', 'v1.0.3'],
      ['flutter/getting-started', 'v1.0.4'],
      ['javascript/getting-started', 'v2.0.2'],
    ]);

    expect(easy?.status).toBe('planned');
    expect(easy?.children.map((page) => [page.slug, page.status])).toEqual([
      ['ios/getting-started', 'planned'],
      ['android/getting-started', 'planned'],
      ['flutter/getting-started', 'planned'],
      ['javascript/getting-started', 'planned'],
    ]);
    for (const page of easy?.children ?? []) {
      const snapshot = snapshots.get(page.slug);
      expect(snapshot).toBeDefined();
      expect(page.description.zh).toContain(snapshot!);
      expect(page.description.en).toContain(snapshot!);
    }
    for (const url of [
      '/en/sdk/easy',
      '/en/sdk/easy/ios/getting-started',
      '/en/sdk/easy/android/getting-started',
      '/en/sdk/easy/flutter/getting-started',
      '/en/sdk/easy/javascript/getting-started',
    ]) {
      expect(published).not.toContain(url);
    }
    expect(getNavigationEntry('en', 'sdk', ['easy', 'ios', 'getting-started'])?.slugs).toEqual([
      'easy',
      'ios',
      'getting-started',
    ]);

    expect(getNavigationEntry('en', 'sdk', ['flutter'])?.status).toBe(
      'published',
    );
  });

  test('keeps the Phase 16 trusted Product HTTP management pages published', () => {
    expect(getNavigationEntry('en', 'api', ['product-http', 'channels'])?.status).toBe(
      'published',
    );
    expect(
      getNavigationEntry('en', 'api', ['product-http', 'conversations'])?.status,
    ).toBe('published');
    expect(getNavigationEntry('en', 'api', ['specifications', 'openapi'])?.status).toBe(
      'published',
    );
    expect(
      getNavigationEntry('en', 'api', ['operations-http', 'health-and-readiness'])
        ?.status,
    ).toBe('published');
  });

  test('publishes the complete Phase 18 API and protocol reference', () => {
    expect(
      getNavigationEntry('en', 'api', [
        'product-http',
        'message-send',
        'sendChannelMessage',
      ])?.status,
    ).toBe('published');
    expect(getNavigationEntry('en', 'api', ['client-protocols'])?.status).toBe(
      'published',
    );
    for (const page of ['connection-lifecycle', 'packet-types']) {
      expect(getNavigationEntry('en', 'api', ['client-protocols', page])?.status).toBe(
        'published',
      );
    }
    for (const page of ['tcp-binary', 'json-rpc', 'encryption']) {
      expect(getNavigationEntry('en', 'api', ['client-protocols', page])?.status).toBe(
        'published',
      );
    }
    for (const page of ['openapi', 'json-rpc-schema', 'protocol-changelog']) {
      expect(getNavigationEntry('en', 'api', ['specifications', page])?.status).toBe(
        'published',
      );
    }
    for (const page of ['health-and-readiness', 'metrics', 'read-only', 'stability']) {
      expect(getNavigationEntry('en', 'api', ['operations-http', page])?.status).toBe(
        'published',
      );
    }
    for (const page of ['events', 'payloads', 'reliability-and-security']) {
      expect(getNavigationEntry('en', 'api', ['webhooks', page])?.status).toBe(
        'published',
      );
    }
    expect(getNavigationEntry('en', 'api', ['interface-inventory'])?.status).toBe(
      'published',
    );
  });

  test('gives every bilingual menu item a unique canonical route', () => {
    for (const locale of locales) {
      const entries = getAllNavigationEntries(locale);
      const urls = entries.map((entry) => entry.url);

      expect(new Set(urls).size).toBe(urls.length);
      expect(urls.every((url) => url.startsWith(`/${locale}/`))).toBe(true);
      expect(getNavigationEntry(locale, 'guide', ['quick-start', 'first-message'])?.label).toBe(
        locale === 'zh' ? '发送第一条消息' : 'Send the First Message',
      );
    }
  });

  test('keeps planned pages out of public indexes', () => {
    for (const locale of locales) {
      const indexed = getIndexedNavigationEntries(locale);

      expect(indexed.map((entry) => entry.url)).toEqual([
        `/${locale}/guide`,
        `/${locale}/guide/product-overview`,
        `/${locale}/guide/product-overview/what-is-wukongim`,
        `/${locale}/guide/product-overview/capabilities`,
        `/${locale}/guide/product-overview/use-cases`,
        `/${locale}/guide/quick-start`,
        `/${locale}/guide/quick-start/prerequisites`,
        `/${locale}/guide/quick-start/single-node-cluster`,
        `/${locale}/guide/quick-start/first-message`,
        `/${locale}/guide/quick-start/chat-demo`,
        `/${locale}/guide/quick-start/next-steps`,
        `/${locale}/guide/core-concepts`,
        `/${locale}/guide/core-concepts/messages`,
        `/${locale}/guide/core-concepts/channels`,
        `/${locale}/guide/core-concepts/users`,
        `/${locale}/guide/core-concepts/devices`,
        `/${locale}/guide/core-concepts/conversations`,
        `/${locale}/guide/integration`,
        `/${locale}/guide/integration/architecture`,
        `/${locale}/guide/integration/authentication`,
        `/${locale}/guide/integration/messaging`,
        `/${locale}/guide/integration/webhooks`,
        `/${locale}/guide/integration/plugins`,
        `/${locale}/guide/integration/acceptance`,
        `/${locale}/guide/tutorials`,
        `/${locale}/guide/tutorials/direct-chat`,
        `/${locale}/guide/tutorials/large-groups`,
        `/${locale}/guide/tutorials/push`,
        `/${locale}/guide/tutorials/ai-and-iot`,
        `/${locale}/server`,
        `/${locale}/server/deployment`,
        `/${locale}/server/deployment/choosing`,
        `/${locale}/server/deployment/docker`,
        `/${locale}/server/deployment/linux`,
        `/${locale}/server/deployment/multi-node`,
        `/${locale}/server/deployment/production-checklist`,
        `/${locale}/server/configuration`,
        `/${locale}/server/configuration/cluster`,
        `/${locale}/server/configuration/networking`,
        `/${locale}/server/configuration/storage`,
        `/${locale}/server/configuration/security`,
        `/${locale}/server/configuration/observability`,
        `/${locale}/server/configuration/reference`,
        `/${locale}/server/operations`,
        `/${locale}/server/operations/manager`,
        `/${locale}/server/operations/health-and-monitoring`,
        `/${locale}/server/operations/scaling`,
        `/${locale}/server/operations/backup-and-restore`,
        `/${locale}/server/operations/upgrade-and-migration`,
        `/${locale}/server/operations/troubleshooting`,
        `/${locale}/server/tools`,
        `/${locale}/server/tools/wkcli`,
        `/${locale}/server/tools/wkdb`,
        `/${locale}/server/tools/wkbench`,
        `/${locale}/server/tools/diagnostics`,
        `/${locale}/server/architecture`,
        `/${locale}/server/architecture/controller`,
        `/${locale}/server/architecture/slots`,
        `/${locale}/server/architecture/channels`,
        `/${locale}/server/architecture/transport`,
        `/${locale}/server/architecture/message-flow`,
        `/${locale}/server/architecture/user-routing`,
        `/${locale}/sdk`,
        `/${locale}/sdk/choose-sdk`,
        `/${locale}/sdk/compatibility`,
        `/${locale}/sdk/common-guides`,
        `/${locale}/sdk/common-guides/identity-and-token`,
        `/${locale}/sdk/common-guides/initialization-and-connection`,
        `/${locale}/sdk/common-guides/messaging`,
        `/${locale}/sdk/common-guides/custom-messages`,
        `/${locale}/sdk/common-guides/conversations-and-unread`,
        `/${locale}/sdk/common-guides/offline-and-push`,
        `/${locale}/sdk/common-guides/multi-device`,
        `/${locale}/sdk/common-guides/reconnect-and-errors`,
        `/${locale}/sdk/android`,
        `/${locale}/sdk/android/installation`,
        `/${locale}/sdk/android/quickstart`,
        `/${locale}/sdk/ios`,
        `/${locale}/sdk/ios/installation`,
        `/${locale}/sdk/ios/quickstart`,
        `/${locale}/sdk/javascript`,
        `/${locale}/sdk/javascript/installation`,
        `/${locale}/sdk/javascript/quickstart`,
        `/${locale}/sdk/javascript/platform-capabilities`,
        `/${locale}/sdk/flutter`,
        `/${locale}/sdk/flutter/installation`,
        `/${locale}/sdk/flutter/quickstart`,
        `/${locale}/sdk/harmonyos`,
        `/${locale}/sdk/harmonyos/installation`,
        `/${locale}/sdk/harmonyos/quickstart`,
        `/${locale}/api`,
        `/${locale}/api/conventions`,
        `/${locale}/api/authentication`,
        `/${locale}/api/compatibility`,
        `/${locale}/api/interface-inventory`,
        `/${locale}/api/product-http`,
        ...productHTTPOpenAPIReferenceGroups.flatMap((group) => [
          `/${locale}/api/product-http/${group.slug}`,
          ...group.operations.map(
            (operation) =>
              `/${locale}/api/product-http/${group.slug}/${operation.slug}`,
          ),
        ]),
        `/${locale}/api/product-http/errors`,
        `/${locale}/api/operations-http`,
        `/${locale}/api/operations-http/health-and-readiness`,
        `/${locale}/api/operations-http/metrics`,
        `/${locale}/api/operations-http/read-only`,
        `/${locale}/api/operations-http/stability`,
        `/${locale}/api/webhooks`,
        `/${locale}/api/webhooks/events`,
        `/${locale}/api/webhooks/payloads`,
        `/${locale}/api/webhooks/reliability-and-security`,
        `/${locale}/api/client-protocols`,
        `/${locale}/api/client-protocols/connection-lifecycle`,
        `/${locale}/api/client-protocols/packet-types`,
        `/${locale}/api/client-protocols/tcp-binary`,
        `/${locale}/api/client-protocols/json-rpc`,
        `/${locale}/api/client-protocols/encryption`,
        `/${locale}/api/dictionaries`,
        `/${locale}/api/dictionaries/channel-types`,
        `/${locale}/api/dictionaries/device-flags`,
        `/${locale}/api/dictionaries/message-flags`,
        `/${locale}/api/dictionaries/reason-codes`,
        `/${locale}/api/specifications`,
        `/${locale}/api/specifications/openapi`,
        `/${locale}/api/specifications/json-rpc-schema`,
        `/${locale}/api/specifications/protocol-changelog`,
      ]);
      expect(indexed.every((entry) => entry.status === 'published')).toBe(true);
    }
  });

  test('backs every published route with matching Chinese and English MDX', async () => {
    for (const entry of getIndexedNavigationEntries('zh')) {
      const segments = [entry.domain, ...entry.slugs];
      if (entry.kind !== 'page') segments.push('index');
      const stem = segments.join('/');

      expect(await Bun.file(new URL(`../content/docs/${stem}.mdx`, import.meta.url)).exists()).toBe(
        true,
      );
      expect(
        await Bun.file(new URL(`../content/docs/${stem}.en.mdx`, import.meta.url)).exists(),
      ).toBe(true);
    }
  });

  test('builds a Fumadocs tree and top tabs from the same registry', () => {
    const tree = buildPageTree('zh', 'guide');
    const overview = tree.children[0];
    const folders = tree.children.filter((node) => node.type === 'folder');
    const serverTree = buildPageTree('zh', 'server');
    const deployment = serverTree.children.find(
      (node) => node.type === 'folder' && node.index?.url === '/zh/server/deployment',
    );
    const kubernetes =
      deployment?.type === 'folder'
        ? deployment.children.find(
            (node) =>
              node.type === 'page' && node.url === '/zh/server/deployment/kubernetes',
          )
        : undefined;

    expect(overview.type).toBe('page');
    if (overview.type === 'page') expect(overview.url).toBe('/zh/guide');
    expect(folders.map((folder) => folder.index?.url)).toEqual([
      '/zh/guide/product-overview',
      '/zh/guide/quick-start',
      '/zh/guide/core-concepts',
      '/zh/guide/integration',
      '/zh/guide/tutorials',
    ]);
    expect(buildLayoutTabs('en').map((tab) => tab.url)).toEqual([
      '/en/guide',
      '/en/server',
      '/en/sdk',
      '/en/api',
    ]);
    expect(kubernetes?.type).toBe('page');
    if (kubernetes?.type === 'page') {
      expect(isValidElement(kubernetes.name)).toBe(true);
      if (isValidElement(kubernetes.name)) {
        expect(kubernetes.name.key).toBe('planned:zh:Kubernetes 部署（Beta）');
      }
    }
  });

  test('groups Product HTTP operations by tag and shows their methods', () => {
    const tree = buildPageTree('en', 'api');
    const productHTTP = tree.children.find(
      (node) => node.type === 'folder' && node.index?.url === '/en/api/product-http',
    );
    expect(productHTTP?.type).toBe('folder');
    if (productHTTP?.type !== 'folder') return;

    const tagFolders = productHTTP.children.filter((node) => node.type === 'folder');
    expect(tagFolders.map((folder) => folder.index?.url)).toEqual(
      productHTTPOpenAPIReferenceGroups.map(
        (group) => `/en/api/product-http/${group.slug}`,
      ),
    );
    const users = tagFolders.find(
      (folder) => folder.index?.url === '/en/api/product-http/users',
    );
    const operation = users?.children[0];
    expect(operation?.type).toBe('page');
    if (operation?.type === 'page' && isValidElement(operation.name)) {
      expect(renderToStaticMarkup(operation.name)).toContain('POST');
    }
  });

  test('fails closed when MDX content is not marked as published', () => {
    expect(parseLocale('zh')).toBe('zh');
    expect(parseLocale('fr')).toBeUndefined();
    expect(isPublishedContentPath('guide/index.mdx')).toBe(true);
    expect(isPublishedContentPath('guide/index.en.mdx')).toBe(true);
    expect(isPublishedContentPath('guide/quick-start/index.mdx')).toBe(true);
    expect(isPublishedContentPath('guide/quick-start/index.en.mdx')).toBe(true);
    expect(isPublishedContentPath('guide/product-overview/capabilities.mdx')).toBe(true);
    expect(isPublishedContentPath('guide/product-overview/capabilities.en.mdx')).toBe(true);
    expect(isPublishedContentPath('guide/product-overview/use-cases.mdx')).toBe(true);
    expect(isPublishedContentPath('guide/product-overview/use-cases.en.mdx')).toBe(true);
    expect(isPublishedContentPath('guide/core-concepts/messages.mdx')).toBe(true);
    expect(isPublishedContentPath('guide/core-concepts/messages.en.mdx')).toBe(true);
    expect(isPublishedContentPath('guide/core-concepts/channels.mdx')).toBe(true);
    expect(isPublishedContentPath('guide/core-concepts/channels.en.mdx')).toBe(true);
    expect(isPublishedContentPath('guide/core-concepts/users.mdx')).toBe(true);
    expect(isPublishedContentPath('guide/core-concepts/users.en.mdx')).toBe(true);
    expect(isPublishedContentPath('guide/core-concepts/devices.mdx')).toBe(true);
    expect(isPublishedContentPath('guide/core-concepts/devices.en.mdx')).toBe(true);
    expect(isPublishedContentPath('guide/core-concepts/conversations.mdx')).toBe(true);
    expect(isPublishedContentPath('guide/core-concepts/conversations.en.mdx')).toBe(true);
    expect(isPublishedContentPath('guide/core-concepts/cluster-and-nodes.mdx')).toBe(false);
    expect(isPublishedContentPath('guide/core-concepts/users-and-devices.mdx')).toBe(false);
    expect(isPublishedContentPath('guide/integration/plugins.mdx')).toBe(true);
    expect(isPublishedContentPath('guide/integration/plugins.en.mdx')).toBe(true);
    expect(isPublishedContentPath('guide/integration/acceptance.mdx')).toBe(true);
    expect(isPublishedContentPath('guide/integration/acceptance.en.mdx')).toBe(true);
    expect(isPublishedContentPath('sdk/choose-sdk.mdx')).toBe(true);
    expect(isPublishedContentPath('sdk/choose-sdk.en.mdx')).toBe(true);
    expect(isPublishedContentPath('sdk/javascript/platform-capabilities.mdx')).toBe(true);
    expect(isPublishedContentPath('sdk/javascript/platform-capabilities.en.mdx')).toBe(true);
    expect(isPublishedContentPath('server/deployment/docker.mdx')).toBe(true);
    expect(isPublishedContentPath('server/deployment/docker.en.mdx')).toBe(true);
    expect(isPublishedContentPath('server/deployment/kubernetes.mdx')).toBe(false);
    expect(isPublishedContentPath('server/deployment/kubernetes.en.mdx')).toBe(false);
    expect(isPublishedContentPath('server/configuration/cluster.mdx')).toBe(true);
    expect(isPublishedContentPath('server/configuration/cluster.en.mdx')).toBe(true);
    expect(isPublishedContentPath('server/configuration/reference.mdx')).toBe(true);
    expect(isPublishedContentPath('server/configuration/reference.en.mdx')).toBe(true);
    expect(isPublishedContentPath('server/operations/manager.mdx')).toBe(true);
    expect(isPublishedContentPath('server/operations/manager.en.mdx')).toBe(true);
    expect(isPublishedContentPath('server/operations/backup-and-restore.mdx')).toBe(true);
    expect(isPublishedContentPath('server/operations/backup-and-restore.en.mdx')).toBe(true);
    expect(isPublishedContentPath('server/operations/troubleshooting.mdx')).toBe(true);
    expect(isPublishedContentPath('server/operations/troubleshooting.en.mdx')).toBe(true);
    expect(isPublishedContentPath('server/tools/index.mdx')).toBe(true);
    expect(isPublishedContentPath('server/tools/index.en.mdx')).toBe(true);
    expect(isPublishedContentPath('server/tools/wkcli.mdx')).toBe(true);
    expect(isPublishedContentPath('server/tools/wkcli.en.mdx')).toBe(true);
    expect(isPublishedContentPath('server/tools/wkdb.mdx')).toBe(true);
    expect(isPublishedContentPath('server/tools/wkdb.en.mdx')).toBe(true);
    expect(isPublishedContentPath('server/tools/wkbench.mdx')).toBe(true);
    expect(isPublishedContentPath('server/tools/wkbench.en.mdx')).toBe(true);
    expect(isPublishedContentPath('server/tools/diagnostics.mdx')).toBe(true);
    expect(isPublishedContentPath('server/tools/diagnostics.en.mdx')).toBe(true);
    expect(isPublishedContentPath('server/architecture/index.mdx')).toBe(true);
    expect(isPublishedContentPath('server/architecture/index.en.mdx')).toBe(true);
    expect(isPublishedContentPath('server/architecture/controller.mdx')).toBe(true);
    expect(isPublishedContentPath('server/architecture/controller.en.mdx')).toBe(true);
    expect(isPublishedContentPath('server/architecture/slots.mdx')).toBe(true);
    expect(isPublishedContentPath('server/architecture/slots.en.mdx')).toBe(true);
    expect(isPublishedContentPath('server/architecture/channels.mdx')).toBe(true);
    expect(isPublishedContentPath('server/architecture/channels.en.mdx')).toBe(true);
    expect(isPublishedContentPath('server/architecture/transport.mdx')).toBe(true);
    expect(isPublishedContentPath('server/architecture/transport.en.mdx')).toBe(true);
    expect(isPublishedContentPath('server/architecture/message-flow.mdx')).toBe(true);
    expect(isPublishedContentPath('server/architecture/message-flow.en.mdx')).toBe(true);
    expect(isPublishedContentPath('server/architecture/user-routing.mdx')).toBe(true);
    expect(isPublishedContentPath('server/architecture/user-routing.en.mdx')).toBe(true);
    expect(isPublishedContentPath('api/client-protocols/index.mdx')).toBe(true);
    expect(isPublishedContentPath('api/client-protocols/index.en.mdx')).toBe(true);
    expect(isPublishedContentPath('api/client-protocols/connection-lifecycle.mdx')).toBe(
      true,
    );
    expect(isPublishedContentPath('api/client-protocols/packet-types.en.mdx')).toBe(true);
    expect(isPublishedContentPath('api/client-protocols/tcp-binary.mdx')).toBe(true);
    expect(isPublishedContentPath('api/client-protocols/json-rpc.en.mdx')).toBe(true);
    expect(isPublishedContentPath('api/client-protocols/encryption.mdx')).toBe(true);
    expect(isPublishedContentPath('api/operations-http/metrics.mdx')).toBe(true);
    expect(isPublishedContentPath('api/webhooks/payloads.en.mdx')).toBe(true);
    expect(isPublishedContentPath('api/specifications/openapi.mdx')).toBe(true);
    expect(isPublishedContentPath('guide/tutorials/index.mdx')).toBe(true);
    expect(isPublishedContentPath('guide/tutorials/index.en.mdx')).toBe(true);
    expect(isPublishedContentPath('guide/tutorials/direct-chat.mdx')).toBe(true);
    expect(isPublishedContentPath('guide/tutorials/direct-chat.en.mdx')).toBe(true);
    expect(isPublishedContentPath('guide/tutorials/large-groups.mdx')).toBe(true);
    expect(isPublishedContentPath('guide/tutorials/large-groups.en.mdx')).toBe(true);
    expect(isPublishedContentPath('guide/tutorials/push.mdx')).toBe(true);
    expect(isPublishedContentPath('guide/tutorials/push.en.mdx')).toBe(true);
    expect(isPublishedContentPath('guide/tutorials/ai-and-iot.mdx')).toBe(true);
    expect(isPublishedContentPath('guide/tutorials/ai-and-iot.en.mdx')).toBe(true);
    expect(isPublishedContentPath('unknown/index.mdx')).toBe(false);
  });
});
