import { describe, expect, test } from 'bun:test';
import { isValidElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import {
  domains,
  getAllNavigationEntries,
  getIndexedNavigationEntries,
  getNavigationEntry,
  isNavigationGroup,
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
      'wukongim',
      'easy',
      'common-guides',
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
      'published',
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
    expect(getNavigationEntry('en', 'sdk', ['javascript'])?.status).toBe('published');
  });

  test('publishes the Phase 14 acceptance loop and later JavaScript reference chapters without broadening runtime support', () => {
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
      'published',
    );
    expect(getNavigationEntry('en', 'sdk', ['javascript', 'upgrade'])?.status).toBe(
      'published',
    );
  });

  test('retains the Phase 19 iOS baseline and publishes later source-aligned chapters', () => {
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
      ['platform-capabilities', 'published'],
      ['api-reference', 'published'],
      ['upgrade', 'published'],
    ]);
  });

  test('retains the Phase 20 Android baseline and publishes later source-aligned chapters', () => {
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
      ['platform-capabilities', 'published'],
      ['api-reference', 'published'],
      ['upgrade', 'published'],
    ]);
  });

  test('retains the Phase 21 Flutter baseline and publishes later source-aligned chapters', () => {
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
      ['platform-capabilities', 'published'],
      ['api-reference', 'published'],
      ['upgrade', 'published'],
    ]);
  });

  test('retains the Phase 22 HarmonyOS baseline and publishes later source-aligned chapters', () => {
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
      ['platform-capabilities', 'published'],
      ['api-reference', 'published'],
      ['upgrade', 'published'],
    ]);
  });

  test('publishes the Phase 23 UniApp retirement path without reviving the old SDK', () => {
    const published = getIndexedNavigationEntries('en').map((entry) => entry.url);
    const sdk = domains.find((domain) => domain.key === 'sdk');
    const wukongim = sdk?.groups.find((group) => group.slug === 'wukongim');
    const uniapp = wukongim?.children
      .filter(isNavigationGroup)
      .find((group) => group.slug === 'uniapp');

    expect(published).toEqual(
      expect.arrayContaining([
        '/en/sdk/uniapp',
        '/en/sdk/uniapp/migrate-to-jssdk',
      ]),
    );
    expect(uniapp?.status).toBe('published');
    expect(uniapp?.children.map((page) => [page.slug, page.status])).toEqual([
      ['migrate-to-jssdk', 'published'],
    ]);
    expect(uniapp?.description.en).toContain('deprecated');
  });

  test('publishes source-aligned EasySDK tutorials with a bounded server wire receipt', () => {
    const sdk = domains.find((domain) => domain.key === 'sdk');
    const easy = sdk?.groups.find((group) => group.slug === 'easy');
    const published = getIndexedNavigationEntries('en').map((entry) => entry.url);
    const snapshots = new Map([
      ['ios/getting-started', 'v1.1.0'],
      ['android/getting-started', 'v1.0.4'],
      ['flutter/getting-started', 'v1.1.0'],
      ['javascript/getting-started', 'v2.0.3'],
    ]);

    expect(easy?.status).toBe('published');
    expect(easy?.children.map((page) => [page.slug, page.status])).toEqual([
      ['ios/getting-started', 'published'],
      ['android/getting-started', 'published'],
      ['flutter/getting-started', 'published'],
      ['javascript/getting-started', 'published'],
    ]);
    for (const page of easy?.children ?? []) {
      const snapshot = snapshots.get(page.slug);
      expect(snapshot).toBeDefined();
      expect(page.description.zh).toContain(snapshot!);
      expect(page.description.en).toContain(snapshot!);
      expect(page.description.zh).toContain('JSON-RPC CONNECT');
      expect(page.description.en).toContain('JSON-RPC CONNECT');
    }
    expect(easy?.description.zh).toContain('服务端线协议');
    expect(easy?.description.en).toContain('server-side wire');
    for (const url of [
      '/en/sdk/easy',
      '/en/sdk/easy/ios/getting-started',
      '/en/sdk/easy/android/getting-started',
      '/en/sdk/easy/flutter/getting-started',
      '/en/sdk/easy/javascript/getting-started',
    ]) {
      expect(published).toContain(url);
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

  test('renders the published EasySDK group and tutorials without planned badges', () => {
    const tree = buildPageTree('zh', 'sdk');
    const easy = tree.children.find(
      (node) => node.type === 'folder' && node.index?.url === '/zh/sdk/easy',
    );

    expect(easy?.type).toBe('folder');
    if (easy?.type !== 'folder') return;
    expect(isValidElement(easy.name)).toBe(false);
    expect(easy.name).toBe('WuKongEasySDK');
    for (const page of easy.children) {
      expect(page.type).toBe('page');
      if (page.type === 'page') expect(isValidElement(page.name)).toBe(false);
    }
  });

  test('groups the full SDK platform guides under WuKongIMSDK', () => {
    const sdk = domains.find((domain) => domain.key === 'sdk');
    const wukongim = sdk?.groups.find((group) => group.slug === 'wukongim');

    expect(wukongim?.status).toBe('published');
    expect(wukongim?.childrenAtDomainRoot).toBe(true);
    expect(
      wukongim?.children.map((group) => [group.slug, group.status]),
    ).toEqual([
      ['android', 'published'],
      ['ios', 'published'],
      ['javascript', 'published'],
      ['flutter', 'published'],
      ['uniapp', 'published'],
      ['harmonyos', 'published'],
    ]);
    expect(
      getNavigationEntry('en', 'sdk', ['ios', 'quickstart'])
        ?.status,
    ).toBe('published');
    expect(
      getNavigationEntry('en', 'sdk', ['wukongim', 'ios', 'quickstart']),
    ).toBeUndefined();

    const tree = buildPageTree('zh', 'sdk');
    const folder = tree.children.find(
      (node) =>
        node.type === 'folder' && node.index?.url === '/zh/sdk/wukongim',
    );

    expect(folder?.type).toBe('folder');
    if (folder?.type !== 'folder') return;
    expect(folder.name).toBe('WuKongIMSDK');
    expect(
      folder.children.map((node) =>
        node.type === 'folder'
          ? node.index?.url
          : node.type === 'page'
            ? node.url
            : undefined,
      ),
    ).toEqual([
      '/zh/sdk/android',
      '/zh/sdk/ios',
      '/zh/sdk/javascript',
      '/zh/sdk/flutter',
      '/zh/sdk/uniapp',
      '/zh/sdk/harmonyos',
    ]);
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

  test('indexes every currently published route in canonical order', () => {
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
        `/${locale}/server/deployment/kubernetes`,
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
        `/${locale}/sdk/wukongim`,
        `/${locale}/sdk/android`,
        `/${locale}/sdk/android/installation`,
        `/${locale}/sdk/android/quickstart`,
        `/${locale}/sdk/android/platform-capabilities`,
        `/${locale}/sdk/android/api-reference`,
        `/${locale}/sdk/android/upgrade`,
        `/${locale}/sdk/ios`,
        `/${locale}/sdk/ios/installation`,
        `/${locale}/sdk/ios/quickstart`,
        `/${locale}/sdk/ios/platform-capabilities`,
        `/${locale}/sdk/ios/api-reference`,
        `/${locale}/sdk/ios/upgrade`,
        `/${locale}/sdk/javascript`,
        `/${locale}/sdk/javascript/installation`,
        `/${locale}/sdk/javascript/quickstart`,
        `/${locale}/sdk/javascript/platform-capabilities`,
        `/${locale}/sdk/javascript/api-reference`,
        `/${locale}/sdk/javascript/upgrade`,
        `/${locale}/sdk/flutter`,
        `/${locale}/sdk/flutter/installation`,
        `/${locale}/sdk/flutter/quickstart`,
        `/${locale}/sdk/flutter/platform-capabilities`,
        `/${locale}/sdk/flutter/api-reference`,
        `/${locale}/sdk/flutter/upgrade`,
        `/${locale}/sdk/uniapp`,
        `/${locale}/sdk/uniapp/migrate-to-jssdk`,
        `/${locale}/sdk/harmonyos`,
        `/${locale}/sdk/harmonyos/installation`,
        `/${locale}/sdk/harmonyos/quickstart`,
        `/${locale}/sdk/harmonyos/platform-capabilities`,
        `/${locale}/sdk/harmonyos/api-reference`,
        `/${locale}/sdk/harmonyos/upgrade`,
        `/${locale}/sdk/easy`,
        `/${locale}/sdk/easy/ios/getting-started`,
        `/${locale}/sdk/easy/android/getting-started`,
        `/${locale}/sdk/easy/flutter/getting-started`,
        `/${locale}/sdk/easy/javascript/getting-started`,
        `/${locale}/sdk/common-guides`,
        `/${locale}/sdk/common-guides/identity-and-token`,
        `/${locale}/sdk/common-guides/initialization-and-connection`,
        `/${locale}/sdk/common-guides/messaging`,
        `/${locale}/sdk/common-guides/custom-messages`,
        `/${locale}/sdk/common-guides/conversations-and-unread`,
        `/${locale}/sdk/common-guides/offline-and-push`,
        `/${locale}/sdk/common-guides/multi-device`,
        `/${locale}/sdk/common-guides/reconnect-and-errors`,
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

  test('publishes every maintained route after the planned-document backlog is complete', () => {
    for (const locale of locales) {
      expect(
        getAllNavigationEntries(locale)
          .filter((entry) => entry.status === 'planned')
          .map((entry) => entry.url),
      ).toEqual([]);
    }
  });

  test('pins every Phase 24 upgrade guide to an exact source interval', async () => {
    const specification = await Bun.file(
      new URL('../PHASE_24_SPEC.md', import.meta.url),
    ).text();

    for (const row of [
      '| Android | [`1.5.4 → 1.5.5`](https://github.com/WuKongIM/WuKongIMAndroidSDK/compare/1.5.4...1.5.5) | Android 8/8.1 VPN capability failure gains the `NetworkInfo` fallback; public Java signatures do not change |',
      '| iOS | [`1.1.0 → 1.1.1`](https://github.com/WuKongIM/WuKongIMiOSSDK/compare/1.1.0...1.1.1) | `filterNoCMDAndNoStreamMessages` stops filtering `isDeleted != 0`; public headers do not change |',
      '| Flutter | [`d99990f41ecb31166af82b9d20c121f33ff8385d → de1024276523119e38305c49a3a873caae4d5c59`](https://github.com/WuKongIM/WuKongIMFlutterSDK/compare/d99990f41ecb31166af82b9d20c121f33ff8385d...de1024276523119e38305c49a3a873caae4d5c59) | async sender/member lookup, awaited reaction persistence, maximum reaction sequence, and populated conversation results |',
      '| HarmonyOS | [`a79df83f2794c581096850f0f77d34b95566a9ae → 0c41810a1e0a5fc2936929d63ca32a50ffb11bec`](https://github.com/WuKongIM/WuKongIMHarmonyOSSDK/compare/a79df83f2794c581096850f0f77d34b95566a9ae...0c41810a1e0a5fc2936929d63ca32a50ffb11bec) | new channel/message/conversation queries, connection-generation changes, failed-sending initialization, and extra/reaction persistence |',
      '| JavaScript direct | [`533a60cdd1b9229fc4a87d7d22b5b860eb4aa43c → 3c507ea3ebc08eae9d74fc1f76b150c380752008`](https://github.com/WuKongIM/WuKongIMJSSDK/compare/533a60cdd1b9229fc4a87d7d22b5b860eb4aa43c...3c507ea3ebc08eae9d74fc1f76b150c380752008) | `WKEvent.dataText → dataJson` with JSON parsing |',
      '| JavaScript wide | [`3747f4477829cf87d9003725038506aa5591b1ab → 3c507ea3ebc08eae9d74fc1f76b150c380752008`](https://github.com/WuKongIM/WuKongIMJSSDK/compare/3747f4477829cf87d9003725038506aa5591b1ab...3c507ea3ebc08eae9d74fc1f76b150c380752008) | protocol-version, stream-removal, event-manager, and build-version changes in addition to the direct delta |',
    ]) {
      expect(specification).toContain(row);
    }

    expect(specification).toContain(
      'JitPack `com.github.WuKongIM:WuKongIMAndroidSDK:1.5.5`',
    );
    expect(specification).toContain(
      'framework podspec conflicts between iOS platform `11.0` and deployment target `13.0`',
    );
    expect(specification).toContain(
      'generated compatibility state remains `verified: false` / `verification.status: missing`',
    );
    expect(specification).not.toContain('The existing receipt');
  });

  test('backs every published route with matching Chinese and English MDX', async () => {
    const missing: string[] = [];

    for (const entry of getIndexedNavigationEntries('zh')) {
      const segments = [entry.domain, ...entry.slugs];
      if (entry.kind !== 'page') segments.push('index');
      const stem = segments.join('/');

      for (const suffix of ['.mdx', '.en.mdx']) {
        if (!(await Bun.file(new URL(`../content/docs/${stem}${suffix}`, import.meta.url)).exists())) {
          missing.push(`${stem}${suffix}`);
        }
      }
    }

    expect(missing).toEqual([]);
  });

  test('never hides an existing MDX page behind planned or unknown navigation', async () => {
    const hidden: string[] = [];
    const content = new Bun.Glob('../content/docs/**/*.mdx');

    for await (const file of content.scan({ cwd: import.meta.dir, absolute: true })) {
      const relative = file.split('/content/docs/').at(1);
      if (!relative || isPublishedContentPath(relative)) continue;
      hidden.push(relative);
    }

    expect(hidden.sort()).toEqual([]);
  });

  test('does not leave obsolete planned-backlog claims in published pages', async () => {
    const staleClaims = [
      '仍保持规划状态',
      '在获得对应证据前仍保持规划状态',
      '规划中的升级文档',
      '侧栏仍会显示后续规划页面',
      '标记为“规划中”',
      'remain planned',
      'sidebar still shows planned pages',
      'marked “Planned”',
    ];
    const findings: string[] = [];
    const content = new Bun.Glob('../content/docs/**/*.mdx');

    for await (const file of content.scan({ cwd: import.meta.dir, absolute: true })) {
      const body = await Bun.file(file).text();
      for (const claim of staleClaims) {
        if (body.toLocaleLowerCase().includes(claim.toLocaleLowerCase())) {
          findings.push(`${file.split('/content/docs/').at(1)}: ${claim}`);
        }
      }
    }

    expect(findings.sort()).toEqual([]);
  });

  test('marks historical planned-route statements as phase boundaries', async () => {
    const historicalBoundaries = new Map<string, RegExp>([
      [
        'PHASE_1_SPEC.md',
        /all descendant menu entries\s+are visible as planned pages\.[\s\S]{0,500}This list records the Phase 1 skeleton boundary,[\s\S]{0,200}Phase 24 leaves/,
      ],
      [
        'PHASE_6_SPEC.md',
        /Troubleshooting remains a planned route\.[\s\S]{0,200}This is the Phase 6 boundary\. Phase 7 later publishes/,
      ],
      [
        'PHASE_7_SPEC.md',
        /Architecture remains planned for\s+a later phase\.[\s\S]{0,200}This is the Phase 7 boundary\. Phase 8 later publishes/,
      ],
      [
        'PHASE_8_SPEC.md',
        /keep Kubernetes, SDK, API, tutorials, and remaining guide pages\s+planned\.[\s\S]{0,1000}This is the Phase 8 boundary\.[\s\S]{0,200}Phase 24 leaves/,
      ],
      [
        'PHASE_9_SPEC.md',
        /keep tutorials, SDK, API, Kubernetes, and all other\s+planned routes excluded from public indexes\.[\s\S]{0,1000}This is the Phase 9 boundary\.[\s\S]{0,200}Phase 24 leaves/,
      ],
      [
        'PHASE_12_SPEC.md',
        /All other SDK platforms,[\s\S]{0,300}remain planned\.[\s\S]{0,100}This is the Phase 12 boundary, not the current publication status\. Phase 24/,
      ],
      [
        'PHASE_13_SPEC.md',
        /At the Phase 13 boundary,[\s\S]{0,200}remained\s+planned\.[\s\S]{0,200}Phases\s+19 through 24 published/,
      ],
      [
        'PHASE_14_SPEC.md',
        /The complete JavaScript API reference and upgrade guide remain planned\.[\s\S]{0,200}This paragraph records the Phase 14 boundary\. Phase 24 later publishes/,
      ],
      [
        'PHASE_15_SPEC.md',
        /At the Phase 15 boundary,[\s\S]{0,200}remained planned\. Phases 19 through 24 later published/,
      ],
      [
        'PHASE_17_SPEC.md',
        /These routes remain planned:[\s\S]{0,1500}This is the Phase 17 boundary\. Phase 18 later publishes[\s\S]{0,200}not current publication status/,
      ],
      [
        'PHASE_19_SPEC.md',
        /The following iOS routes remain planned and excluded from indexed output:[\s\S]{0,800}This is the Phase 19 publication boundary\. Phase 24 later publishes/,
      ],
      [
        'PHASE_20_SPEC.md',
        /The following routes remain planned and excluded from indexed output:[\s\S]{0,800}This is the Phase 20 publication boundary\. Phase 24 later publishes/,
      ],
      [
        'PHASE_21_SPEC.md',
        /Flutter platform capabilities, a complete API reference, and an upgrade guide\s+remain planned\.[\s\S]{0,100}This is the Phase 21 publication boundary\. Phase 24 later publishes/,
      ],
      [
        'PHASE_22_SPEC.md',
        /HarmonyOS platform capabilities, a complete API reference, and an upgrade guide\s+remain planned\.[\s\S]{0,200}This is the Phase 22 publication boundary\. Phase 24 later publishes/,
      ],
    ]);

    for (const [file, boundary] of historicalBoundaries) {
      const body = await Bun.file(new URL(`../${file}`, import.meta.url)).text();
      expect(body).toMatch(boundary);
    }
  });

  test('keeps localized links in published pages on maintained routes', async () => {
    const publishedUrls = new Set(
      locales.flatMap((locale) =>
        getIndexedNavigationEntries(locale).map((entry) => entry.url),
      ),
    );
    publishedUrls.add('/zh');
    publishedUrls.add('/en');

    const findings: string[] = [];
    const content = new Bun.Glob('../content/docs/**/*.mdx');

    for await (const file of content.scan({ cwd: import.meta.dir, absolute: true })) {
      const body = await Bun.file(file).text();
      const targets = [
        ...body.matchAll(/\]\((\/(?:zh|en)(?:\/[^)\s?#]*)?)(?:[?#][^)\s]*)?(?:\s+"[^"]*")?\)/g),
        ...body.matchAll(/href=["'](\/(?:zh|en)(?:\/[^"'?#]*)?)(?:[?#][^"']*)?["']/g),
      ].map((match) => match[1]?.replace(/\/$/, ''));

      for (const target of new Set(targets)) {
        if (!target || publishedUrls.has(target)) continue;
        findings.push(`${file.split('/content/docs/').at(1)}: ${target}`);
      }
    }

    expect(findings.sort()).toEqual([]);
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
      expect(isValidElement(kubernetes.name)).toBe(false);
      expect(kubernetes.name).toBe('Kubernetes 部署（Beta）');
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
    expect(isPublishedContentPath('sdk/easy/index.mdx')).toBe(true);
    expect(isPublishedContentPath('sdk/easy/index.en.mdx')).toBe(true);
    expect(isPublishedContentPath('sdk/easy/ios/getting-started.mdx')).toBe(true);
    expect(isPublishedContentPath('sdk/easy/ios/getting-started.en.mdx')).toBe(true);
    expect(isPublishedContentPath('sdk/easy/android/getting-started.mdx')).toBe(true);
    expect(isPublishedContentPath('sdk/easy/android/getting-started.en.mdx')).toBe(true);
    expect(isPublishedContentPath('sdk/easy/flutter/getting-started.mdx')).toBe(true);
    expect(isPublishedContentPath('sdk/easy/flutter/getting-started.en.mdx')).toBe(true);
    expect(isPublishedContentPath('sdk/easy/javascript/getting-started.mdx')).toBe(true);
    expect(isPublishedContentPath('sdk/easy/javascript/getting-started.en.mdx')).toBe(true);
    expect(isPublishedContentPath('sdk/wukongim/index.mdx')).toBe(true);
    expect(isPublishedContentPath('sdk/wukongim/index.en.mdx')).toBe(true);
    expect(isPublishedContentPath('sdk/javascript/platform-capabilities.mdx')).toBe(true);
    expect(isPublishedContentPath('sdk/javascript/platform-capabilities.en.mdx')).toBe(true);
    for (const platform of ['android', 'ios', 'flutter', 'harmonyos', 'javascript']) {
      for (const chapter of ['platform-capabilities', 'api-reference', 'upgrade']) {
        if (platform === 'javascript' && chapter === 'platform-capabilities') continue;
        expect(isPublishedContentPath(`sdk/${platform}/${chapter}.mdx`)).toBe(true);
        expect(isPublishedContentPath(`sdk/${platform}/${chapter}.en.mdx`)).toBe(true);
      }
    }
    expect(isPublishedContentPath('server/deployment/docker.mdx')).toBe(true);
    expect(isPublishedContentPath('server/deployment/docker.en.mdx')).toBe(true);
    expect(isPublishedContentPath('server/deployment/kubernetes.mdx')).toBe(true);
    expect(isPublishedContentPath('server/deployment/kubernetes.en.mdx')).toBe(true);
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
