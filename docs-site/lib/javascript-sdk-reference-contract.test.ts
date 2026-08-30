import { describe, expect, test } from 'bun:test';

const sdkRoot = new URL('../content/docs/sdk/javascript/', import.meta.url);

async function page(path: string) {
  return Bun.file(new URL(path, sdkRoot)).text();
}

describe('JavaScript SDK reference publication contract', () => {
  test('documents the exact 1.3.5 public surface in both locales', async () => {
    const pages = await Promise.all([page('api-reference.mdx'), page('api-reference.en.mdx')]);

    for (const content of pages) {
      expect(content).toContain('wukongimjssdk@1.3.5');
      expect(content).toContain('3c507ea3ebc08eae9d74fc1f76b150c380752008');
      expect(content).toContain('lib/*.d.ts');
      expect(content).toContain("from 'wukongimjssdk'");
      expect(content).toMatch(
        /export map.*只公开.*`\.`.*`\.\/package\.json`|export map exposes only.*`\.`.*`\.\/package\.json`/iu,
      );
      expect(content).toMatch(
        /不能依赖 `wukongimjssdk\/lib\/\*`|must not depend on `wukongimjssdk\/lib\/\*`/iu,
      );
      expect(content).not.toMatch(
        /(?:import|from)\s*(?:[^'"\n]*\sfrom\s*)?['"]wukongimjssdk\/lib\//u,
      );
      for (const symbol of [
        'WKSDK.shared()',
        'connectManager',
        'chatManager',
        'channelManager',
        'conversationManager',
        'eventManager',
        'messageContentManager',
        'securityManager',
        'taskManager',
        'receiptManager',
        'reminderManager',
        'connectAddrCallback',
        'syncMessagesCallback',
        'addConnectStatusListener',
        'removeConnectStatusListener',
        'addMessageListener',
        'removeMessageListener',
        'addMessageStatusListener',
        'removeMessageStatusListener',
        'sendWithOptions',
        'registerFactor',
        'getMessageContent',
        'isSystemMessage',
        'newMessageText',
        'newChannelInfo',
        'newMediaMessageContent',
        'addTask',
        'removeTask',
        'addReceiptMessages',
        'maxReminderVersion',
        'updateConversations',
        'notifyListeners',
        'signalEncrypt',
        'encryption2',
        'registrationID',
        'deviceID',
        'TaskStatus',
        'ChannelTypePerson',
        'ChannelTypeGroup',
        'ReasonCode.success',
      ]) {
        expect(content).toContain(symbol);
      }
      expect(content).toContain('Promise<Message>');
      expect(content).toMatch(/(?:原样返回输入字节|returns the input bytes unchanged)/iu);
      expect(content).toMatch(
        /(?:没有公开的.*(?:reset|destroy)|no public [`]*(?:reset|destroy))/iu,
      );
      expect(content).not.toMatch(/npm (?:i|install)[^\n]*\blatest\b/u);
    }
  });

  test('treats the 1.3.5 migration as an evidence-gated change', async () => {
    const pages = await Promise.all([page('upgrade.mdx'), page('upgrade.en.mdx')]);
    const targetRevision = '3c507ea3ebc08eae9d74fc1f76b150c380752008';
    const directRevision = '533a60cdd1b9229fc4a87d7d22b5b860eb4aa43c';
    const wideRevision = '3747f44';
    const wideCommit =
      'https://github.com/WuKongIM/WuKongIMJSSDK/commit/3747f44';
    const directComparison =
      `https://github.com/WuKongIM/WuKongIMJSSDK/compare/${directRevision}...${targetRevision}`;

    for (const content of pages) {
      expect(content).toContain('npm install --save-exact wukongimjssdk@1.3.5');
      expect(content).toContain(targetRevision);
      expect(content).toContain('protoVersion');
      expect(content).toContain('eventManager');
      expect(content).toContain(directRevision);
      expect(content).toContain(wideRevision);
      expect(content).toContain(wideCommit);
      expect(content).toContain(directComparison);
      expect(content).toContain('dataText');
      expect(content).toContain('dataJson');
      expect(content).toContain('streamManager');
      expect(content).toContain('npm run check');
      expect(content).toContain('npm run verify:acceptance');
      expect(content).toContain('production_readiness.result=not_assessed');
      expect(content).toContain('package-lock.json');
      expect(content).not.toMatch(/npm (?:i|install)[^\n]*\blatest\b/u);

      const directStart = content.indexOf('### `1.3.4 → 1.3.5`');
      const wideStart = content.indexOf('### `1.3.0 → 1.3.5`');
      expect(directStart).toBeGreaterThanOrEqual(0);
      expect(wideStart).toBeGreaterThan(directStart);

      const directDiff = content.slice(directStart, wideStart);
      expect(directDiff).toContain(directRevision);
      expect(directDiff).toContain(targetRevision);
      expect(directDiff).toContain(directComparison);
      expect(directDiff).toMatch(/dataText[\s\S]*dataJson/u);
      expect(directDiff).toMatch(/直接执行 JSON 解析|parses JSON immediately/iu);
      expect(directDiff).toMatch(/非 JSON|non-JSON/iu);
      expect(directDiff).toMatch(
        /没有从 package root\/export map 暴露|neither exposed from the package root\/export map/iu,
      );
      expect(directDiff).toMatch(
        /不应被描述成根公共 API 删除|not a root public-API deletion/iu,
      );
      expect(directDiff).not.toContain('streamManager');
      expect(directDiff).not.toContain('protoVersion');

      const wideEnd = content.indexOf('\n### ', wideStart + 4);
      expect(wideEnd).toBeGreaterThan(wideStart);
      const wideDiff = content.slice(wideStart, wideEnd);
      expect(wideDiff).toContain(wideRevision);
      expect(wideDiff).toContain(wideCommit);
      expect(wideDiff).toContain(targetRevision);
      expect(wideDiff).toMatch(/protoVersion[\s\S]*`4`[\s\S]*`5`/u);
      expect(wideDiff).toMatch(
        /streamManager[\s\S]*(?:导出的|exported) `Stream`[\s\S]*(?:被移除|were removed)/iu,
      );
      expect(wideDiff).toMatch(
        /Setting\.streamNo[\s\S]*Message\.streamNo[\s\S]*streamId[\s\S]*streamFlag[\s\S]*(?:被移除|were removed)/iu,
      );
      expect(wideDiff).toMatch(
        /新增[\s\S]*eventManager[\s\S]*WKEvent[\s\S]*EventType|eventManager[\s\S]*WKEvent[\s\S]*EventType[\s\S]*were added/iu,
      );
      expect(wideDiff).toMatch(/dataText[\s\S]*dataJson/u);
      expect(wideDiff).toMatch(/sdkVersion[\s\S]*(?:构建生成|build-generated)/iu);
    }
  });
});
