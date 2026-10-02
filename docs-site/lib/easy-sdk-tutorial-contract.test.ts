import { easySdkReleases, resolveEasySdkText } from './easy-sdk-version';
import { describe, expect, test } from 'bun:test';
import { getIndexedNavigationEntries } from './navigation';

const root = new URL('../content/docs/sdk/easy/', import.meta.url);
const archivePath = 'docs/superpowers/reports/2026-09-08-easysdk-validation-history.md';
const content = async (name: string) => resolveEasySdkText(await Bun.file(new URL(name, root)).text());
const platforms = [
  { key: 'ios', install: `exact: "${easySdkReleases.ios.version}"`, api: ['WuKongConfig', 'sdk.onMessage', 'sdk.connect()', 'sdk.send('], cleanup: ['sdk.removeListener', 'sdk.disconnect()'], bounds: ['connectionTimeout: 15', 'requestTimeout: 15'] },
  { key: 'android', install: `implementation("com.githubim:easysdk-android:${easySdkReleases.android.version}")`, api: ['WuKongConfig.Builder()', 'addEventListener', 'easySDK.connect()', 'easySDK.send('], cleanup: ['removeEventListener', 'easySDK.disconnect()'], bounds: ['withTimeout(20_000)', '.connectionTimeout(15_000)'] },
  { key: 'flutter', install: `wukong_easy_sdk: ${easySdkReleases.flutter.version}`, api: ['WuKongEasySDK.getInstance()', 'addEventListener', 'easySDK.connect()', 'easySDK.send('], cleanup: ['removeEventListener', 'easySDK.disconnect()', 'easySDK.dispose()'], bounds: ['Duration(seconds: 20)', '.timeout('] },
  { key: 'javascript', install: `npm install --save-exact easyjssdk@${easySdkReleases.javascript.version}`, api: ['WKIM.init', 'im.on', 'im.connect()', 'im.send('], cleanup: ['im.off', 'im.destroy()'], bounds: ['Promise.race', '10_000'] },
  { key: 'rust', install: `wukong-easy-sdk = "=${easySdkReleases.rust.version}"`, api: ['client.subscribe()', 'client.connect().await', 'client.send(', 'Event::Message'], cleanup: ['client.destroy().await', 'listener.abort()'], bounds: ['Backpressure', 'Lagged'] },
  { key: 'csharp', install: `package WuKongEasySDK --version ${easySdkReleases.csharp.version} --source https://api.nuget.org/v3/index.json`, api: ['new WKIM', 'im.Message +=', 'im.ConnectAsync()', 'im.SendAsync('], cleanup: ['im.Message -=', 'await using', 'im.DisconnectAsync()'], bounds: ['TimeSpan.FromSeconds(10)', 'WKIMBackpressureException'] },
  { key: 'cpp', install: `find_package(WuKongEasySDK ${easySdkReleases.cpp.cmakeVersion} CONFIG REQUIRED)`, api: ['im.on(', 'im.connect().get()', 'im.send('], cleanup: ['im.off(', 'im.destroy().get()'], bounds: ['ErrorCode::QueueFull', 'std::chrono::seconds(15)'] },
  { key: 'python', install: `"wukong-easy-sdk==${easySdkReleases.python.version}"`, api: ['WKIM.init', 'im.on(', 'async with im:', 'await im.send('], cleanup: ['im.off(', 'destroy()'], bounds: ['ErrorCode.QUEUE_FULL', 'client_msg_no'] },
];
const taskNames = {
  zh: ['准备接入', '安装 SDK', '连接与监听', '收发第一条消息', '清理连接', '常见问题'],
  en: ['Prepare', 'Install the SDK', 'Connect and listen', 'Exchange the first message', 'Clean up', 'Troubleshooting'],
};

describe('EasySDK first-message reader contract', () => {
  for (const locale of ['zh', 'en'] as const) {
    const suffix = locale === 'en' ? '.en' : '';
    test(`${locale}: eight tutorials share discoverable, executable task paths`, async () => {
      const overview = await content(`index${suffix}.mdx`);
      const examples = await content(`examples${suffix}.mdx`);
      const urls = getIndexedNavigationEntries(locale).map((entry) => entry.url);
      for (const platform of platforms) {
        const url = `/${locale}/sdk/easy/${platform.key}/getting-started`;
        expect(urls).toContain(url);
        expect(overview).toContain(url);
        expect(examples).toContain(url);
        expect(examples).toContain(`[#${platform.key}]`);
        const page = await content(`${platform.key}/getting-started${suffix}.mdx`);
        expect([...page.matchAll(/^## \d\. (.+)$/gm)].map((m) => m[1])).toEqual(taskNames[locale]);
        for (const token of [platform.install, ...platform.api, ...platform.cleanup, ...platform.bounds]) expect(page).toContain(token);
        expect(page).toContain(`/${locale}/guide/integration/authentication`);
        expect(page).toContain(`/${locale}/guide/integration/messaging`);
        expect(page).toContain(`${archivePath}#${platform.key}`);
        expect(page).toContain('Alice');
        expect(page).toContain('Bob');
        expect(page).toContain('WuKongIM HTTP API');
        expect(page).not.toMatch(/gh workflow run|actions\/runs\/|\.acceptance\/|registry-cluster-.*\.json/);
        expect(page).not.toMatch(/@latest|\^1\.0|~>\s*1\.0/);
      }
      expect(overview).toContain('JSON-RPC CONNECT');
      expect(overview).toContain('Payload');
      for (const token of ['/readyz', 'device_flag', '10.0.2.2', `git checkout v${easySdkReleases.javascript.version}`]) expect(examples).toContain(token);
      expect(examples).not.toContain('gh workflow run');
      expect(examples).not.toContain('git checkout 5676700');
    });
    test(`${locale}: examples retain ownership, safe output and bounded displays`, async () => {
      const ios = await content(`ios/getting-started${suffix}.mdx`);
      const android = await content(`android/getting-started${suffix}.mdx`);
      const flutter = await content(`flutter/getting-started${suffix}.mdx`);
      const web = await content(`javascript/getting-started${suffix}.mdx`);
      for (const token of ['enableDebugLogging: false', 'enableJsonLogging: false', 'messages.count > 100']) expect(ios).toContain(token);
      expect(ios).not.toContain('error.localizedDescription');
      for (const token of ['current.token == bootstrap.token', 'current.serverUrl == bootstrap.websocketUrl', 'debugLogging(false)']) expect(android).toContain(token);
      expect(flutter).toContain('messagesById.length > 100');
      expect(flutter).toContain('debugLogging: false');
      for (const token of ['singleton: false', 'debugLogging: false', 'type RecvMessage', 'output.textContent', 'button.disabled = true', 'button.onclick']) expect(web).toContain(token);
      expect(web).not.toContain('new Map');
      expect(web).not.toContain('2.0.4');
      expect(web).not.toMatch(/console\.(?:log|info|error)\([^\n]*(?:bootstrap\.token|message\.payload|, error)/);
      expect(web.indexOf("addEventListener('pagehide'")).toBeLessThan(web.indexOf('await chat.start(await fetchIMBootstrap())'));
    });
    test(`${locale}: alternatives follow the main path and known server limits remain`, async () => {
      for (const key of ['ios', 'csharp', 'cpp']) {
        const page = await content(`${key}/getting-started${suffix}.mdx`);
        expect(page.search(/^## (?:其他安装方式|Alternative installation)/m)).toBeGreaterThan(page.indexOf(`## 6. ${taskNames[locale][5]}`));
      }
      const cpp = await content(`cpp/getting-started${suffix}.mdx`);
      for (const token of ['add_executable(my_app main.cpp)', './build/my_app bob', 'im.send(argv[1]']) expect(cpp).toContain(token);
      const python = await content(`python/getting-started${suffix}.mdx`);
      for (const token of ['uid=os.environ["WKIM_UID"]', 'os.environ["WKIM_PEER"]', '2a295e0d9881ef5356728a85d56b052c4b0d9c86']) expect(python).toContain(token);
      expect(await content(`csharp/getting-started${suffix}.mdx`)).toContain('issues/927');
    });
  }
  test('bilingual pages retain the same code-block sequence', async () => {
    for (const name of ['index', 'examples', ...platforms.map((platform) => `${platform.key}/getting-started`)]) {
      const [zh, en] = await Promise.all([content(`${name}.mdx`), content(`${name}.en.mdx`)]);
      const languages = (page: string) => [...page.matchAll(/^```([^\n]*)/gm)].map((match) => match[1]);
      expect(languages(zh)).toEqual(languages(en));
    }
  });
  test('historical results retain independent package/server/harness identities', async () => {
    const history = await Bun.file(new URL(`../../${archivePath}`, import.meta.url)).text();
    for (const identity of [
      '5676700d2dc966fa6fc9b2f0620a6ae429adad5a', '35f314cc2512f3f0f5d55d9677e817cb64129985',
      '1c9430f15fc8844e7025df07d54ab6e80e026414', '33484491015', 'easyjssdk@2.0.4',
      'b6d0bbe822b9c5b6f95a10d55b593d30184414f6', '34198683155',
      '0029747f10b86f566e2d659535df0954114769a90962e562fb522a95e5508719',
      '6b533a25ff0c61548a3f90dd36fa2562118f8f21', 'ec2c62c73eca29be99ac15ba76ff7466c13617d5',
      'a9be49e69f4b7155a9a6244ef63f9cb6baf00cb9', '4d39f9e43265f88415f2fe01fc5418182e405c52',
      'abba280187987195614e2e25c89089475dca1979', '8a1525dce74ad23cf8f7f47d91adaa9fc5b459f1',
      'not 2.0.5 runs', 'issues/927', 'fault_to_stable_routes_ms', '35,380',
    ]) expect(history).toContain(identity);
  });
});
