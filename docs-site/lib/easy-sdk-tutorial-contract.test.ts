import { describe, expect, test } from 'bun:test';

const sdkRoot = new URL('../content/docs/sdk/easy/', import.meta.url);

async function content(fileName: string) {
  return Bun.file(new URL(fileName, sdkRoot)).text();
}

const platforms = [
  {
    path: 'ios/getting-started',
    repository: 'WuKongEasySDK-iOS',
    tag: 'v1.0.2',
    revision: '6257d9ddcc2872e20ff23446a2f368c2c2c1f481',
    install: ["pod 'WuKongEasySDK', '1.0.2'", 'exact: "1.0.2"'],
    api: ['WuKongConfig', 'onConnect', 'onMessage', 'removeListener', 'sdk.connect()', 'sdk.send('],
    bounded: ['connectionTimeout: 15', 'requestTimeout: 15'],
    cleanup: ['sdk.disconnect()', 'listeners.forEach { sdk.removeListener($0) }'],
  },
  {
    path: 'android/getting-started',
    repository: 'WuKongEasySDK-Android',
    tag: 'v1.0.2',
    revision: '2e9c9023428571b56eeddc053608aefe5d6a9a5f',
    install: ['implementation("com.githubim:easysdk-android:1.0.2")'],
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
    tag: 'v1.0.3',
    revision: '7888867a9f22ec22d768dcfe0c6c95b418fcb458',
    install: ['wukong_easy_sdk: 1.0.3'],
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
    tag: 'v2.0.1',
    revision: 'f13b7fb911fdb2912025e289dcd7749350a54469',
    install: ['npm install --save-exact easyjssdk@2.0.1'],
    api: ['WKIM.init', 'im.on', 'im.off', 'im.connect()', 'im.send('],
    bounded: ['Promise.race', '10_000'],
    cleanup: ['im.off', 'im.destroy()'],
  },
] as const;

describe('EasySDK tutorial content contract', () => {
  test('publishes a bilingual overview with four platform paths and exact source snapshots', async () => {
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
      }
      expect(page).toContain(`/${locale}/sdk/common-guides/identity-and-token`);
      expect(page).toContain(`/${locale}/sdk/common-guides/messaging`);
      expect(page).not.toMatch(/@latest|\^1\.0|~>\s*1\.0/u);
    }

    expect(zh).toContain('源码校对不等于本站运行验证');
    expect(en).toContain('Source alignment is not runtime verification');
    expect(zh).toContain('“5 分钟”描述的是阅读路径');
    expect(en).toContain('“5 minutes” describes the shape of the path');
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
    }
    for (const page of [androidZh, androidEn]) {
      expect(page).toMatch(/下划线字段|underscore fields?/u);
      expect(page).toMatch(/驼峰字段|camel-case fields?/u);
      expect(page).toMatch(/Base64/u);
      expect(page).toMatch(/APP[^\n.]*`0`|`0`[^\n.]*APP/u);
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
  });
});
