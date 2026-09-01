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
    tag: 'v1.1.1',
    revision: 'ca688fcac2c4cd8d6f8e8163faf165376b520ba9',
    exampleRevision: '40014c16c0becd390c105098d359048901f4d87c',
    fixRevision: 'b7ec4440b940539bee213f95a3be74948f4b9fb8',
    fixPullRequest: 'https://github.com/WuKongIM/WuKongEasySDK-iOS/pull/3',
    release: 'https://github.com/WuKongIM/WuKongEasySDK-iOS/releases/tag/v1.1.1',
    distribution: 'https://cocoapods.org/pods/WuKongEasySDK',
    install: ["pod 'WuKongEasySDK', '1.1.1'", 'exact: "1.1.1"'],
    api: ['WuKongConfig', 'onConnect', 'onMessage', 'removeListener', 'sdk.connect()', 'sdk.send('],
    bounded: ['connectionTimeout: 15', 'requestTimeout: 15'],
    cleanup: ['sdk.disconnect()', 'listeners.forEach { sdk.removeListener($0) }'],
  },
  {
    path: 'android/getting-started',
    repository: 'WuKongEasySDK-Android',
    tag: 'v1.0.5',
    revision: '61ae6dc6d0077b15e47cda1fd530296b97a06a7a',
    exampleRevision: '7134bbd0263fd01d9e7f71b7bd05b226f75b2292',
    fixRevision: 'e984c7374a0e11f5d109ad3dbfdea599907735ff',
    fixPullRequest: 'https://github.com/WuKongIM/WuKongEasySDK-Android/pull/3',
    release: 'https://github.com/WuKongIM/WuKongEasySDK-Android/releases/tag/v1.0.5',
    distribution: 'https://central.sonatype.com/artifact/com.githubim/easysdk-android/1.0.5',
    install: ['implementation("com.githubim:easysdk-android:1.0.5")'],
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
    tag: 'v1.1.0',
    revision: '98ab8f3d9a1ad53f40c32caef0979845a37ae9a6',
    exampleRevision: '98ab8f3d9a1ad53f40c32caef0979845a37ae9a6',
    fixRevision: 'd7758c301e5289ddfa09cd09b6976c2479584b1c',
    fixPullRequest: 'https://github.com/WuKongIM/WuKongEasySDK-Flutter/pull/3',
    release: 'https://github.com/WuKongIM/WuKongEasySDK-Flutter/releases/tag/v1.1.0',
    distribution: 'https://pub.dev/packages/wukong_easy_sdk/versions/1.1.0',
    install: ['wukong_easy_sdk: 1.1.0'],
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
    tag: 'v2.0.4',
    revision: '9c03c98c725982fac224cd1d3b52456eae983975',
    exampleRevision: 'a055b3667247333b6b3183249f5d5929673dfd53',
    fixRevision: '3ebf505734c5b6764b30eac011f0b7a5024c89e8',
    fixPullRequest: 'https://github.com/WuKongIM/WuKongEasySDK-JS/pull/6',
    release: 'https://github.com/WuKongIM/WuKongEasySDK-JS/releases/tag/v2.0.4',
    distribution: 'https://www.npmjs.com/package/easyjssdk/v/2.0.4',
    install: ['npm install --save-exact easyjssdk@2.0.4'],
    api: ['WKIM.init', 'im.on', 'im.off', 'im.connect()', 'im.send('],
    bounded: ['Promise.race', '10_000'],
    cleanup: ['im.off', 'im.destroy()'],
  },
] as const;

const releasedPackageAcceptance = {
  sourceServerRevision: '5676700d2dc966fa6fc9b2f0620a6ae429adad5a',
  releasedServerRevision: '35f314cc2512f3f0f5d55d9677e817cb64129985',
  candidateHead: '1c9430f15fc8844e7025df07d54ab6e80e026414',
  workflowRun: 'https://github.com/WuKongIM/WuKongIM/actions/runs/33484491015',
} as const;

describe('EasySDK tutorial content contract', () => {
  test('keeps the Phase 15 snapshot contract aligned with maintained tutorials', async () => {
    const specification = await Bun.file(
      new URL('../PHASE_15_SPEC.md', import.meta.url),
    ).text();

    for (const platform of platforms) {
      expect(specification).toContain(platform.tag);
      expect(specification).toContain(platform.revision);
      expect(specification).toContain(platform.fixRevision);
    }
    expect(specification).toMatch(/republished as source-aligned\s+tutorials/u);
    expect(specification).toContain('Superseding current state');
    expect(specification).toContain('Codec fixtures cover iOS, Android, Flutter, and Web');
    expect(specification).toContain('E2E runs the iOS and Android');
    expect(specification).toContain('cross-repository run');
    for (const platform of platforms) {
      expect(specification).toContain(platform.exampleRevision);
    }
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
        expect(page).toContain(platform.fixRevision);
        expect(page).toContain(platform.fixPullRequest);
        expect(page).toContain(platform.release);
        expect(page).toContain(platform.distribution);
        expect(page).toContain(platform.exampleRevision);
      }
      expect(page).toContain(`/${locale}/sdk/easy/examples`);
      expect(page).toContain(`/${locale}/guide/integration/authentication`);
      expect(page).toContain(`/${locale}/guide/integration/messaging`);
      expect(page).not.toMatch(/@latest|\^1\.0|~>\s*1\.0/u);
    }

    expect(zh).toContain('四端源码 example 与正式发布包均已跑通');
    expect(en).toContain('All four source examples and released packages have run successfully');
    expect(zh).toContain('## 选择平台，发送第一条消息');
    expect(en).toContain('## Choose a platform and send the first message');
    expect(zh).toContain('## Alice 与 Bob 验收闭环');
    expect(en).toContain('## Alice/Bob acceptance loop');
    expect(zh).toContain('## 版本与证据');
    expect(en).toContain('## Versions and evidence');
    expect(zh).not.toContain('5 分钟');
    expect(en).not.toContain('5-minute');
  });

  test('centralizes legacy calibration and source provenance below the task-first overview', async () => {
    const [zh, en] = await Promise.all([content('index.mdx'), content('index.en.mdx')]);

    expect(zh).toContain('https://wukong.mintlify.app/zh/sdk/easy/overview');
    expect(en).toContain('https://wukong.mintlify.app/en/sdk/easy/overview');
    expect(zh).toContain('任务顺序校准');
    expect(en).toContain('task sequence was calibrated');

    for (const platform of platforms) {
      const [platformZh, platformEn] = await Promise.all([
        content(`${platform.path}.mdx`),
        content(`${platform.path}.en.mdx`),
      ]);

      expect(zh).toContain(`https://wukong.mintlify.app/zh/sdk/easy/${platform.path}`);
      expect(en).toContain(`https://wukong.mintlify.app/en/sdk/easy/${platform.path}`);
      expect(platformZh).not.toContain('wukong.mintlify.app');
      expect(platformEn).not.toContain('wukong.mintlify.app');
      if (platform.exampleRevision !== platform.revision) {
        expect(platformZh).not.toContain(platform.revision);
        expect(platformEn).not.toContain(platform.revision);
      }
      expect(platformZh).not.toContain(platform.fixPullRequest);
      expect(platformEn).not.toContain(platform.fixPullRequest);
    }
  });

  test('publishes tutorial discovery with the supported pinned EasySDK path explicit', async () => {
    const [sdkZh, sdkEn, easyZh, easyEn] = await Promise.all([
      doc('sdk/index.mdx'),
      doc('sdk/index.en.mdx'),
      content('index.mdx'),
      content('index.en.mdx'),
    ]);
    for (const page of [easyZh, easyEn]) {
      expect(page).toContain('JSON-RPC CONNECT');
    }
    expect(sdkZh).toContain('/zh/sdk/easy');
    expect(sdkEn).toContain('/en/sdk/easy');
    expect(sdkZh).toContain('/zh/sdk/wukongim');
    expect(sdkEn).toContain('/en/sdk/wukongim');
  });

  test('separates server profile E2E, source example runs, and released-package runs', async () => {
    const protocolPages = await Promise.all([
      doc('api/client-protocols/json-rpc.mdx'),
      doc('api/client-protocols/json-rpc.en.mdx'),
    ]);

    for (const page of protocolPages) {
      expect(page).toMatch(/四端|四个固定|all four|four pinned|iOS `v1\.1\.0`/u);
      expect(page).toMatch(/iOS 与 Android profile|iOS and Android profiles/u);
    }

    const [examplesZh, examplesEn] = await Promise.all([
      content('examples.mdx'),
      content('examples.en.mdx'),
    ]);
    for (const page of [examplesZh, examplesEn]) {
      expect(page).toContain(releasedPackageAcceptance.sourceServerRevision);
      expect(page).toContain(releasedPackageAcceptance.releasedServerRevision);
      expect(page).toContain(releasedPackageAcceptance.candidateHead);
      expect(page).toContain(releasedPackageAcceptance.workflowRun);
      for (const platform of platforms) {
        expect(page).toContain(platform.revision);
        expect(page).toContain(platform.exampleRevision);
      }
      expect(page).toMatch(/发布包|released packages/u);
      expect(page).toMatch(/物理真机|physical devices/u);
      expect(page).toMatch(/生产 Token|production-token|production token/u);
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
        expect(page).toContain(platform.release);
        expect(page).toContain(platform.distribution);
        for (const command of platform.install) expect(page).toContain(command);
        for (const api of platform.api) expect(page).toContain(api);
        for (const boundary of platform.bounded) expect(page).toContain(boundary);
        for (const cleanup of platform.cleanup) expect(page).toContain(cleanup);
        expect(page).toContain(`/${locale}/guide/integration/authentication`);
        expect(page).toContain(`/${locale}/guide/integration/messaging`);
        expect(page).toContain(`/${locale}/sdk/easy`);
        expect(page).toMatch(/上线前检查|Before production/u);
        expect(page).not.toMatch(/@latest|\^1\.0|~>\s*1\.0/u);
      }

      expect(zh).toContain(platform.exampleRevision);
      expect(en).toContain(platform.exampleRevision);
      expect(zh).toContain('/zh/sdk/easy/examples');
      expect(en).toContain('/en/sdk/easy/examples');
      expect(zh).toMatch(/Alice/u);
      expect(zh).toMatch(/Bob/u);
      expect(en).toMatch(/Alice/u);
      expect(en).toMatch(/Bob/u);
    }
  });

  test('publishes a bilingual runnable example runbook', async () => {
    const [zh, en] = await Promise.all([content('examples.mdx'), content('examples.en.mdx')]);

    for (const page of [zh, en]) {
      expect(page).toContain('go run ./cmd/wukongim -config ./wukongim.toml');
      expect(page).toContain('GOWORK=off go test -tags=e2e');
      expect(page).toContain('ws://127.0.0.1:5200');
      expect(page).toContain('ws://10.0.2.2:5200');
      expect(page).toContain('npm test');
      expect(page).toContain('./gradlew test :example:assembleDebug');
      expect(page).toContain('swift test');
      expect(page).toContain('flutter test');
      for (const platform of platforms) expect(page).toContain(platform.exampleRevision);
    }

    expect(zh).toContain('/zh/guide/integration/messaging');
    expect(en).toContain('/en/guide/integration/messaging');
    expect(zh).toContain('源码 example 与正式发布包是两类证据');
    expect(en).toContain('Source examples and released packages are separate evidence');
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

  test('publishes the released protocol boundaries and released logging fixes', async () => {
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
      expect(page).toContain('enableDebugLogging');
      expect(page).toContain('enableJsonLogging');
      expect(page).toMatch(/Base64/u);
      expect(page).toMatch(/APP[^\n.]*`0`|`0`[^\n.]*APP/u);
      expect(page).not.toMatch(/\.app(?:\.rawValue)?\s*(?:==|is|为)\s*`?1`?/u);
    }
    for (const page of [androidZh, androidEn]) {
      expect(page).toMatch(/下划线字段|snake_case(?: fields?)?|underscore fields?/u);
      expect(page).toMatch(/驼峰字段|camel-case fields?/u);
      expect(page).toMatch(/Base64/u);
      expect(page).toMatch(/APP[^\n.]*`0`|`0`[^\n.]*APP/u);
      expect(page).not.toMatch(/APP (?:device )?value `?1`?|APP 设备值 `?1`?/u);
      expect(page).toContain('debugLogging(false)');
      expect(page).toMatch(/默认静默|default-silent/u);
    }
    for (const page of [flutterZh, flutterEn]) {
      expect(page).toContain('debugLogging');
      expect(page).toContain('logHandler');
      expect(page).toContain('base64Decode');
      expect(page).toMatch(/Token|token/u);
      expect(page).toMatch(/Payload|payload/u);
    }
    for (const page of [webZh, webEn]) {
      expect(page).toContain('debugLogging');
      expect(page).toMatch(/Token|token/u);
      expect(page).toMatch(/Payload|payload/u);
    }

    for (const page of [iosZh, iosEn, androidZh, androidEn, flutterZh, flutterEn, webZh, webEn]) {
      expect(page).toMatch(/APP[^\n.]*`0`[^\n.]*WEB[^\n.]*`1`[^\n.]*PC[^\n.]*`2`/u);
    }

    const [overviewZh, overviewEn] = await Promise.all([
      content('index.mdx'),
      content('index.en.mdx'),
    ]);

    for (const [index, [zh, en]] of [
      [iosZh, iosEn],
      [androidZh, androidEn],
      [flutterZh, flutterEn],
      [webZh, webEn],
    ].entries()) {
      const platform = platforms[index];
      for (const page of [zh, en]) {
        expect(page).toContain(platform.release);
      }
      for (const overview of [overviewZh, overviewEn]) {
        expect(overview).toContain(platform.fixPullRequest);
        expect(overview).toContain(platform.fixRevision);
        expect(overview).toContain(platform.release);
      }
      expect(zh).toMatch(/默认关闭|默认静默/u);
      expect(zh).not.toContain('尚未发布');
      expect(zh).not.toContain('等待下一官方版本');
      expect(en).toMatch(/defaults? to `?false`?|default-off|disabled by default|off by default|default-silent/iu);
      expect(en).not.toMatch(/not (?:yet )?released|wait for the next official release|does not include (?:it|that fix)/iu);
    }

    for (const page of [iosZh, iosEn]) {
      expect(page).not.toContain('print("Disconnected: \\(info.code) \\(info.reason)")');
      expect(page).not.toContain('error.localizedDescription');
      expect(page).not.toContain('result.messageId), seq=');
      expect(page).not.toContain('code=\\(info.code)');
      expect(page).not.toContain('code=\\(sdkError.code)');
      expect(page).not.toContain('seq=\\(result.messageSeq)');
      expect(page).toMatch(/RECVACK[^\n]*`messageId`[^\n]*`messageSeq`/u);
      expect(page).toContain('print("WuKongEasySDK operation failed")');
      expect(page).toContain('print("SEND completed")');
    }
    for (const page of [androidZh, androidEn]) {
      expect(page).not.toContain('message.messageId} from ${message.fromUid}');
      expect(page).not.toContain('${info.code} ${info.reason}');
      expect(page).not.toContain('${error.code}: ${error.message}');
      expect(page).not.toContain('"connect failed", it');
      expect(page).not.toContain('${result.messageId}');
      expect(page).not.toContain('"send failed", error');
      expect(page).not.toContain('connected: reason=');
      expect(page).not.toContain('message received: seq=');
      expect(page).not.toContain('disconnected: code=');
      expect(page).not.toContain('SDK operation failed: code=');
      expect(page).not.toContain('SEND completed: seq=');
      expect(page).toContain('Log.i("EasySDK", "message received")');
      expect(page).toContain('Log.e("EasySDK", "SDK operation failed")');
    }
    for (const page of [flutterZh, flutterEn]) {
      expect(page).not.toContain('${info.code} ${info.reason}');
      expect(page).not.toContain('${error.code} ${error.message}');
      expect(page).not.toContain('connect failed: $error');
      expect(page).not.toContain('${result.messageId}');
      expect(page).not.toContain('connected: reason=');
      expect(page).not.toContain('disconnected: code=');
      expect(page).not.toContain('EasySDK operation failed: code=');
      expect(page).not.toContain('SEND completed: seq=');
      expect(page).toContain("debugPrint('EasySDK operation failed')");
      expect(page).toContain("debugPrint('SEND completed')");
    }
    for (const page of [webZh, webEn]) {
      expect(page).not.toContain("console.info('EasySDK connected', result)");
      expect(page).not.toContain("console.info('EasySDK disconnected', info)");
      expect(page).not.toContain("console.error('EasySDK error', error)");
      expect(page).not.toContain('result.messageId');
      expect(page).not.toContain("console.info('SEND completed',");
      expect(page).toContain("console.info('EasySDK connected')");
      expect(page).toContain("console.error('EasySDK operation failed')");
      expect(page).toContain("console.info('SEND completed')");
    }
  });
});
