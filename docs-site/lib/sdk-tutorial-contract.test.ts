import { describe, expect, test } from 'bun:test';

const sdkRoot = new URL('../content/docs/sdk/', import.meta.url);

async function content(fileName: string) {
  return Bun.file(new URL(fileName, sdkRoot)).text();
}

describe('SDK tutorial content contract', () => {
  test('starts from SDK choice and a reader-oriented integration path', async () => {
    const [zh, en] = await Promise.all([content('index.mdx'), content('index.en.mdx')]);

    for (const [locale, page] of [
      ['zh', zh],
      ['en', en],
    ] as const) {
      expect(page).toContain(`/${locale}/sdk/choose-sdk`);
      expect(page).toContain(`/${locale}/sdk/javascript/installation`);
      expect(page).toContain(`/${locale}/sdk/javascript/quickstart`);
      expect(page).toContain(`/${locale}/sdk/common-guides`);
      expect(page).not.toMatch(/Phase 1[234]/u);
    }

    expect(zh).toContain('## 一次完整接入会经历什么');
    expect(en).toContain('## What a complete integration looks like');
  });

  test('publishes official source discovery without turning it into compatibility proof', async () => {
    const [zh, en] = await Promise.all([
      content('choose-sdk.mdx'),
      content('choose-sdk.en.mdx'),
    ]);
    const repositories = [
      'WuKongEasySDK-JS',
      'WuKongEasySDK-iOS',
      'WuKongEasySDK-Android',
      'WuKongEasySDK-Flutter',
      'WuKongIMJSSDK',
      'WuKongIMiOSSDK',
      'WuKongIMAndroidSDK',
      'WuKongIMFlutterSDK',
      'WuKongIMHarmonyOSSDK',
    ];

    for (const page of [zh, en]) {
      for (const repository of repositories) {
        expect(page).toContain(`https://github.com/WuKongIM/${repository}`);
      }
      expect(page).toContain('wukongimjssdk@1.3.5');
      expect(page).not.toMatch(/5\s*分钟|5-minute|five-minute|全平台|all platforms|zero[- ]config/iu);
    }

    expect(zh).toContain('源码存在不等于本站已验证');
    expect(en).toContain('Source availability is not verification');
    expect(zh).toContain('系列名称只用于找到候选仓库');
    expect(en).toContain('Family names are only discovery labels');
    expect(zh).not.toContain('EasySDK 通常');
    expect(en).not.toContain('EasySDK repositories generally');
    expect(zh).not.toContain('比较 EasySDK');
    expect(en).not.toContain('Compare EasySDK');
  });

  test('keeps the Web tutorial runnable and useful inside an existing application', async () => {
    const [installationZh, installationEn, quickstartZh, quickstartEn] = await Promise.all([
      content('javascript/installation.mdx'),
      content('javascript/installation.en.mdx'),
      content('javascript/quickstart.mdx'),
      content('javascript/quickstart.en.mdx'),
    ]);

    for (const page of [installationZh, installationEn]) {
      expect(page).toContain('npm ci');
      expect(page).toContain('npm install --save-exact wukongimjssdk@1.3.5');
    }
    const runtime = await Bun.file(
      new URL(
        '../examples/javascript-web-quickstart/src/client/sdk-runtime.ts',
        import.meta.url,
      ),
    ).text();
    for (const api of ['addConnectStatusListener', 'addMessageListener', 'chatManager.send']) {
      expect(runtime).toContain(api);
      expect(quickstartZh).toContain(api);
      expect(quickstartEn).toContain(api);
    }
    for (const page of [quickstartZh, quickstartEn]) {
      expect(page).toContain('sdk-runtime.ts');
    }
  });
});
