import { describe, expect, test } from 'bun:test';
import {
  channelTypes,
  deviceFlags,
  deviceLevels,
  messageHeaderFlags,
  messageSettings,
  renderProtocolDictionaryMarkdown,
} from './developer-contracts';

const contentRoot = new URL('../content/docs/', import.meta.url);
const commonGuideSlugs = [
  'index',
  'identity-and-token',
  'initialization-and-connection',
  'messaging',
  'custom-messages',
  'conversations-and-unread',
  'offline-and-push',
  'multi-device',
  'reconnect-and-errors',
] as const;

async function content(path: string) {
  return Bun.file(new URL(path, contentRoot)).text();
}

describe('Phase 13 integrator contracts', () => {
  test('publishes independently written bilingual common-guide pages with one shared boundary', async () => {
    for (const slug of commonGuideSlugs) {
      const stem = `sdk/common-guides/${slug}`;
      const [zh, en] = await Promise.all([content(`${stem}.mdx`), content(`${stem}.en.mdx`)]);

      expect(zh).toContain('跨 SDK 行为指南');
      expect(en).toContain('cross-SDK behavior guide');
      expect(zh).toContain('/zh/sdk/compatibility');
      expect(en).toContain('/en/sdk/compatibility');
      expect(zh).not.toBe(en);
    }
  });

  test('keeps exact Channel Type values aligned with the Go wire authority', async () => {
    expect(channelTypes.map(({ name, value }) => ({ name, value }))).toEqual([
      { name: 'ChannelTypePerson', value: 1 },
      { name: 'ChannelTypeGroup', value: 2 },
      { name: 'ChannelTypeCustomerService', value: 3 },
      { name: 'ChannelTypeCommunity', value: 4 },
      { name: 'ChannelTypeCommunityTopic', value: 5 },
      { name: 'ChannelTypeInfo', value: 6 },
      { name: 'ChannelTypeData', value: 7 },
      { name: 'ChannelTypeTemp', value: 8 },
      { name: 'ChannelTypeLive', value: 9 },
      { name: 'ChannelTypeVisitors', value: 10 },
      { name: 'ChannelTypeAgent', value: 11 },
      { name: 'ChannelTypeAgentGroup', value: 12 },
    ]);

    const goSource = await Bun.file(
      new URL('../../pkg/protocol/frame/common.go', import.meta.url),
    ).text();
    const authority = [...goSource.matchAll(/^\s*(ChannelType[A-Za-z0-9]+)\s+uint8\s*=\s*(\d+)/gmu)].map(
      ([, name, value]) => ({ name, value: Number(value) }),
    );
    expect(authority).toEqual(channelTypes.map(({ name, value }) => ({ name, value })));
  });

  test('keeps device categories and levels aligned with protocol constants', async () => {
    expect(deviceFlags.map(({ name, value }) => ({ name, value }))).toEqual([
      { name: 'APP', value: 0 },
      { name: 'WEB', value: 1 },
      { name: 'PC', value: 2 },
      { name: 'SYSTEM', value: 99 },
    ]);
    expect(deviceLevels.map(({ name, value }) => ({ name, value }))).toEqual([
      { name: 'DeviceLevelSlave', value: 0 },
      { name: 'DeviceLevelMaster', value: 1 },
    ]);

    const goSource = await Bun.file(
      new URL('../../internal/contracts/protocolmeta/types.go', import.meta.url),
    ).text();
    for (const fact of [
      'DeviceFlagApp DeviceFlag = iota',
      'DeviceFlagWeb',
      'DeviceFlagPC',
      'DeviceFlagSystem DeviceFlag = 99',
      'DeviceLevelSlave DeviceLevel = iota',
      'DeviceLevelMaster',
    ]) {
      expect(goSource).toContain(fact);
    }
  });

  test('keeps message header and setting bits aligned with codec authorities', async () => {
    expect(messageHeaderFlags.map(({ name, bit }) => ({ name, bit }))).toEqual([
      { name: 'NoPersist', bit: 0 },
      { name: 'RedDot', bit: 1 },
      { name: 'SyncOnce', bit: 2 },
      { name: 'DUP', bit: 3 },
    ]);
    expect(messageSettings.map(({ name, value }) => ({ name, value }))).toEqual([
      { name: 'SettingReceiptEnabled', value: 128 },
      { name: 'SettingSignal', value: 32 },
      { name: 'SettingNoEncrypt', value: 16 },
      { name: 'SettingTopic', value: 8 },
      { name: 'SettingStream', value: 2 },
    ]);

    const [codec, settings] = await Promise.all([
      Bun.file(new URL('../../pkg/protocol/codec/common.go', import.meta.url)).text(),
      Bun.file(new URL('../../pkg/protocol/frame/setting.go', import.meta.url)).text(),
    ]);
    expect(codec.replaceAll(/\s+/gu, '')).toContain(
      'encodeBool(f.GetDUP())<<3|encodeBool(f.GetsyncOnce())<<2|encodeBool(f.GetRedDot())<<1|encodeBool(f.GetNoPersist())',
    );
    for (const expression of [
      'SettingReceiptEnabled Setting = 1 << 7',
      'SettingSignal         Setting = 1 << 5',
      'SettingNoEncrypt      Setting = 1 << 4',
      'SettingTopic          Setting = 1 << 3',
      'SettingStream         Setting = 1 << 1',
    ]) {
      expect(settings).toContain(expression);
    }
  });

  test('renders source-checked dictionaries into machine-readable Markdown', () => {
    const channels = renderProtocolDictionaryMarkdown('en', 'channel-types');
    const devices = renderProtocolDictionaryMarkdown('zh', 'device-flags');
    const flags = renderProtocolDictionaryMarkdown('en', 'message-flags');

    expect(channels).toContain('| 12 | `ChannelTypeAgentGroup` |');
    expect(devices).toContain('| 99 | `SYSTEM` |');
    expect(devices).toContain('| 1 | `DeviceLevelMaster` |');
    expect(flags).toContain('| 0 | `NoPersist` |');
    expect(flags).toContain('| 128 | `SettingReceiptEnabled` |');
  });
});
