import { describe, expect, test } from 'bun:test';

const sdkRoot = new URL('../content/docs/sdk/easy/', import.meta.url);
const docsRoot = new URL('../content/docs/', import.meta.url);

async function content(fileName: string) {
  return Bun.file(new URL(fileName, sdkRoot)).text();
}

async function doc(fileName: string) {
  return Bun.file(new URL(fileName, docsRoot)).text();
}

const platforms = [
  {
    path: 'ios/getting-started',
    repository: 'WuKongEasySDK-iOS',
    tag: 'v1.0.3',
    revision: '643848f85be70e3e3f2be22fceb86ae428b6cc38',
    distribution: 'https://cocoapods.org/pods/WuKongEasySDK',
    install: ["pod 'WuKongEasySDK', '1.0.3'", 'exact: "1.0.3"'],
    api: ['WuKongConfig', 'onConnect', 'onMessage', 'removeListener', 'sdk.connect()', 'sdk.send('],
    bounded: ['connectionTimeout: 15', 'requestTimeout: 15'],
    cleanup: ['sdk.disconnect()', 'listeners.forEach { sdk.removeListener($0) }'],
  },
  {
    path: 'android/getting-started',
    repository: 'WuKongEasySDK-Android',
    tag: 'v1.0.3',
    revision: '62084632cd8d1f26c751b053b0fb82d6aaa63892',
    distribution: 'https://central.sonatype.com/artifact/com.githubim/easysdk-android/1.0.3',
    install: ['implementation("com.githubim:easysdk-android:1.0.3")'],
    api: [
      'WuKongConfig.Builder()',
      'addEventListener',
      'removeEventListener',
      'easySDK.connect()',
      'easySDK.send(',
    ],
    bounded: ['withTimeout(20_000)', '.connectionTimeout(15_000)'],
    cleanup: ['easySDK.disconnect()', 'removeEventListener'],
  },
  {
    path: 'flutter/getting-started',
    repository: 'WuKongEasySDK-Flutter',
    tag: 'v1.0.4',
    revision: '6179251b49414401fe0eac4bfa3fec3f9b13a9fc',
    distribution: 'https://pub.dev/packages/wukong_easy_sdk/versions/1.0.4',
    install: ['wukong_easy_sdk: 1.0.4'],
    api: [
      'WuKongEasySDK.getInstance()',
      'addEventListener',
      'removeEventListener',
      'easySDK.connect()',
      'easySDK.send(',
    ],
    bounded: ['.timeout(', 'Duration(seconds: 20)'],
    cleanup: ['easySDK.disconnect()', 'easySDK.dispose()'],
  },
  {
    path: 'javascript/getting-started',
    repository: 'WuKongEasySDK-JS',
    tag: 'v2.0.2',
    revision: 'c59c80551944c9e5d9b4a902ebd2629d3defb2e6',
    distribution: 'https://www.npmjs.com/package/easyjssdk/v/2.0.2',
    install: ['npm install --save-exact easyjssdk@2.0.2'],
    api: ['WKIM.init', 'im.on', 'im.off', 'im.connect()', 'im.send('],
    bounded: ['Promise.race', '10_000'],
    cleanup: ['im.off', 'im.destroy()'],
  },
] as const;

describe('EasySDK tutorial content contract', () => {
  test('keeps the Phase 15 snapshot contract aligned with maintained tutorials', async () => {
    const specification = await Bun.file(
      new URL('../PHASE_15_SPEC.md', import.meta.url),
    ).text();

    for (const platform of platforms) {
      expect(specification).toContain(platform.tag);
      expect(specification).toContain(platform.revision);
    }
    expect(specification).toContain('republished as source-aligned tutorials');
    expect(specification).toContain('Superseding current state');
    expect(specification).toContain('Codec fixtures cover iOS, Android, Flutter, and Web');
    expect(specification).toContain('E2E runs the iOS and Android');
    expect(specification).not.toContain('not currently executable');
  });

  test('retains a bilingual source scaffold with exact platform snapshots', async () => {
    const [zh, en] = await Promise.all([content('index.mdx'), content('index.en.mdx')]);

    for (const [locale, page] of [
      ['zh', zh],
      ['en', en],
    ] as const) {
      for (const platform of platforms) {
        expect(page).toContain(`/${locale}/sdk/easy/${platform.path}`);
        expect(page).toContain(`https://github.com/WuKongIM/${platform.repository}`);
        expect(page).toContain(platform.tag);
        expect(page).toContain(platform.revision);
        expect(page).toContain(platform.distribution);
      }
      expect(page).toContain(`/${locale}/sdk/common-guides/identity-and-token`);
      expect(page).toContain(`/${locale}/sdk/common-guides/messaging`);
      expect(page).not.toMatch(/@latest|\^1\.0|~>\s*1\.0/u);
    }

    expect(zh).toContain('服务端线协议凭据不等于平台运行验证');
    expect(en).toContain('A server-side wire receipt is not platform runtime verification');
    expect(zh).toContain('“5 分钟”描述的是阅读路径');
    expect(en).toContain('“5 minutes” describes the shape of the path');
    expect(zh).toContain('用 Alice 和 Bob 验收在线双向收发');
    expect(en).toContain('Accept online bidirectional messaging with Alice and Bob');
    expect(zh).toContain('选择上方平台完成第一条消息');
    expect(en).toContain('Choose a platform above and send the first message');
  });

  test('links the exact legacy learning paths while keeping current source authoritative', async () => {
    const [zh, en] = await Promise.all([content('index.mdx'), content('index.en.mdx')]);

    expect(zh).toContain('https://wukong.mintlify.app/zh/sdk/easy/overview');
    expect(en).toContain('https://wukong.mintlify.app/en/sdk/easy/overview');
    expect(zh).toContain('学习顺序');
    expect(en).toContain('learning sequence');

    for (const platform of platforms) {
      const [platformZh, platformEn] = await Promise.all([
        content(`${platform.path}.mdx`),
        content(`${platform.path}.en.mdx`),
      ]);

      expect(platformZh).toContain(
        `https://wukong.mintlify.app/zh/sdk/easy/${platform.path}`,
      );
      expect(platformEn).toContain(
        `https://wukong.mintlify.app/en/sdk/easy/${platform.path}`,
      );
      expect(platformZh).toContain('旧页');
      expect(platformEn).toContain('old page');
      expect(platformZh).toContain(`固定的 \`${platform.tag}\` 源码`);
      expect(platformEn).toContain(`pinned \`${platform.tag}\` source`);
    }
  });

  test('publishes tutorial discovery with the supported pinned EasySDK path explicit', async () => {
    const pages = await Promise.all([
      doc('sdk/index.mdx'),
      doc('sdk/index.en.mdx'),
      doc('sdk/choose-sdk.mdx'),
      doc('sdk/choose-sdk.en.mdx'),
      doc('sdk/compatibility.mdx'),
      doc('sdk/compatibility.en.mdx'),
    ]);
    for (const page of pages) {
      expect(page).toContain('JSON-RPC CONNECT');
    }
    expect(pages[0]).toContain('教程已发布');
    expect(pages[1]).toContain('Pinned tutorials published');
    expect(pages[0]).toContain('服务端线协议');
    expect(pages[1]).toContain('server-side wire');
  });

  test('separates four-profile fixtures from the iOS/Android real-process E2E', async () => {
    const pages = await Promise.all([
      content('index.mdx'),
      content('index.en.mdx'),
      doc('sdk/index.mdx'),
      doc('sdk/index.en.mdx'),
      doc('sdk/choose-sdk.mdx'),
      doc('sdk/choose-sdk.en.mdx'),
      doc('sdk/compatibility.mdx'),
      doc('sdk/compatibility.en.mdx'),
      doc('api/client-protocols/json-rpc.mdx'),
      doc('api/client-protocols/json-rpc.en.mdx'),
    ]);

    for (const page of pages) {
      expect(page).toMatch(/四端|四个固定|all four|four pinned|iOS `v1\.0\.3`/u);
      expect(page).toMatch(/iOS 与 Android profile|iOS and Android profiles/u);
    }
  });

  test('keeps every platform tutorial pinned, lifecycle-safe, and explicit about evidence', async () => {
    for (const platform of platforms) {
      const [zh, en] = await Promise.all([
        content(`${platform.path}.mdx`),
        content(`${platform.path}.en.mdx`),
      ]);

      for (const [locale, page] of [
        ['zh', zh],
        ['en', en],
      ] as const) {
        expect(page).toContain(`https://github.com/WuKongIM/${platform.repository}`);
        expect(page).toContain(platform.tag);
        expect(page).toContain(platform.revision);
        expect(page).toContain(platform.distribution);
        for (const command of platform.install) expect(page).toContain(command);
        for (const api of platform.api) expect(page).toContain(api);
        for (const boundary of platform.bounded) expect(page).toContain(boundary);
        for (const cleanup of platform.cleanup) expect(page).toContain(cleanup);
        expect(page).toContain(`/${locale}/sdk/common-guides/identity-and-token`);
        expect(page).toContain(`/${locale}/sdk/common-guides/messaging`);
        expect(page).toContain(`/${locale}/sdk/easy`);
        expect(page).not.toMatch(/@latest|\^1\.0|~>\s*1\.0/u);
      }

      expect(zh).toContain('不是本站运行验证');
      expect(en).toContain('not runtime verification');
      expect(zh).toMatch(/Alice/u);
      expect(zh).toMatch(/Bob/u);
      expect(en).toMatch(/Alice/u);
      expect(en).toMatch(/Bob/u);
    }
  });

  test('tells direct readers that current Product Gateway supports the pinned EasySDK core path', async () => {
    for (const platform of platforms) {
      const [zh, en] = await Promise.all([
        content(`${platform.path}.mdx`),
        content(`${platform.path}.en.mdx`),
      ]);

      expect(zh).toContain('JSON-RPC CONNECT');
      expect(zh).toContain('当前 Product Gateway 支持');
      expect(zh).toContain('在线双向收发');
      expect(en).toContain('JSON-RPC CONNECT');
      expect(en).toContain('The current Product Gateway supports');
      expect(en).toContain('online bidirectional messaging');
    }
  });

  test('publishes current platform adoption blockers instead of implying production readiness', async () => {
    const [iosZh, iosEn, androidZh, androidEn, flutterZh, flutterEn, webZh, webEn] =
      await Promise.all([
        content('ios/getting-started.mdx'),
        content('ios/getting-started.en.mdx'),
        content('android/getting-started.mdx'),
        content('android/getting-started.en.mdx'),
        content('flutter/getting-started.mdx'),
        content('flutter/getting-started.en.mdx'),
        content('javascript/getting-started.mdx'),
        content('javascript/getting-started.en.mdx'),
      ]);

    for (const page of [iosZh, iosEn]) {
      expect(page).toContain('@available(iOS 15.0');
      expect(page).toContain('enableJsonLogging');
      expect(page).toContain('LogManager.logJsonData');
      expect(page).toMatch(/Base64/u);
      expect(page).toMatch(/APP[^\n.]*`0`|`0`[^\n.]*APP/u);
      expect(page).not.toMatch(/\.app(?:\.rawValue)?\s*(?:==|is|为)\s*`?1`?/u);
    }
    for (const page of [androidZh, androidEn]) {
      expect(page).toMatch(/下划线字段|underscore fields?/u);
      expect(page).toMatch(/驼峰字段|camel-case fields?/u);
      expect(page).toMatch(/Base64/u);
      expect(page).toMatch(/APP[^\n.]*`0`|`0`[^\n.]*APP/u);
      expect(page).not.toMatch(/APP (?:device )?value `?1`?|APP 设备值 `?1`?/u);
      expect(page).toContain('debugLogging(false)');
      expect(page).toContain('Params');
      expect(page).toMatch(/Logcat/u);
    }
    for (const page of [flutterZh, flutterEn]) {
      expect(page).toContain('developer.log');
      expect(page).toContain('base64Decode');
      expect(page).toMatch(/Token|token/u);
      expect(page).toMatch(/Payload|payload/u);
    }
    for (const page of [webZh, webEn]) {
      expect(page).toContain('console.debug');
      expect(page).toContain('console.log');
      expect(page).toMatch(/Token|token/u);
      expect(page).toMatch(/Payload|payload/u);
    }

    for (const page of [iosZh, iosEn, androidZh, androidEn, flutterZh, flutterEn, webZh, webEn]) {
      expect(page).toMatch(/APP[^\n.]*`0`[^\n.]*WEB[^\n.]*`1`[^\n.]*PC[^\n.]*`2`/u);
    }
  });
});
