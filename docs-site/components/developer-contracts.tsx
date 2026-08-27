import {
  channelTypes,
  compatibilitySnapshot,
  deviceFlags,
  deviceLevels,
  goldenPathHTTPPaths,
  messageHeaderFlags,
  messageSettings,
  reasonCodes,
  type ProtocolValueDefinition,
  type ProtocolValueScope,
  type ReasonReachability,
  type ReasonRetryGuidance,
} from '@/lib/developer-contracts';

type Locale = 'zh' | 'en';

const retryLabels: Record<Locale, Record<ReasonRetryGuidance, string>> = {
  zh: {
    'not-applicable': '不适用',
    'do-not-retry': '修复请求后再操作',
    'refresh-route': '刷新路由后复用幂等键',
    'retry-with-backoff': '有界退避重试',
    'application-policy': '由应用策略决定',
    'upgrade-client': '升级客户端后重试',
  },
  en: {
    'not-applicable': 'Not applicable',
    'do-not-retry': 'Repair the request first',
    'refresh-route': 'Refresh route; reuse idempotency key',
    'retry-with-backoff': 'Retry with bounded backoff',
    'application-policy': 'Application policy decides',
    'upgrade-client': 'Upgrade the client first',
  },
};

const reachabilityLabels: Record<Locale, Record<ReasonReachability, string>> = {
  zh: {
    active: '当前路径可达',
    compatibility: '兼容路径',
    reserved: '保留',
  },
  en: {
    active: 'Active path',
    compatibility: 'Compatibility path',
    reserved: 'Reserved',
  },
};

const verificationLabels = {
  zh: {
    verified: '已验证：receipt 与当前构建精确匹配',
    missing: '未验证：未提供验证 receipt',
    malformed: '未验证：验证 receipt 无法读取或格式错误',
    mismatch: '未验证：验证 receipt 与当前构建不匹配',
  },
  en: {
    verified: 'Verified: exact receipt matches this build',
    missing: 'Unverified: no verification receipt supplied',
    malformed: 'Unverified: verification receipt is unreadable or malformed',
    mismatch: 'Unverified: verification receipt does not match this build',
  },
} as const;

const protocolScopeLabels: Record<Locale, Record<ProtocolValueScope, string>> = {
  zh: {
    baseline: '基础接入',
    specialized: '专用类型',
    legacy: '兼容 / 旧类型',
    client: '客户端',
    internal: '服务端保留',
    wire: 'Wire 标志',
  },
  en: {
    baseline: 'Integration baseline',
    specialized: 'Specialized',
    legacy: 'Compatibility / legacy',
    client: 'Client',
    internal: 'Server-reserved',
    wire: 'Wire flag',
  },
};

/** Renders the human-readable form of the public compatibility.json artifact. */
export function CompatibilitySnapshot({ locale = 'en' }: { locale?: Locale }) {
  const isZh = locale === 'zh';

  return (
    <div className="not-prose my-6 overflow-hidden rounded-xl border bg-fd-card text-sm">
      <div className="flex flex-wrap items-center justify-between gap-2 border-b px-4 py-3">
        <strong>{isZh ? '兼容目标' : 'Compatibility target'}</strong>
        <span
          className={
            compatibilitySnapshot.verified
              ? 'rounded-full bg-emerald-500/15 px-2 py-1 text-xs text-emerald-700 dark:text-emerald-300'
              : 'rounded-full bg-amber-500/15 px-2 py-1 text-xs text-amber-800 dark:text-amber-300'
          }
        >
          {verificationLabels[locale][compatibilitySnapshot.verification.status]}
        </span>
      </div>
      <dl className="grid gap-x-6 gap-y-3 p-4 sm:grid-cols-2">
        <SnapshotFact label={isZh ? '文档频道' : 'Documentation channel'}>
          {compatibilitySnapshot.channel}
        </SnapshotFact>
        <SnapshotFact label={isZh ? '服务端源码修订' : 'Server source revision'}>
          <code>{compatibilitySnapshot.source_revision}</code>
        </SnapshotFact>
        <SnapshotFact label="Receipt schema">
          <code>{compatibilitySnapshot.verification.receipt_schema}</code>
        </SnapshotFact>
        <SnapshotFact label={isZh ? '部署拓扑' : 'Deployment topology'}>
          {compatibilitySnapshot.topology}
        </SnapshotFact>
        <SnapshotFact label={isZh ? '物理哈希槽' : 'Physical hash slots'}>
          {compatibilitySnapshot.hash_slot_count}
        </SnapshotFact>
        <SnapshotFact label="JavaScript SDK">
          <code>
            {compatibilitySnapshot.sdk.package}@{compatibilitySnapshot.sdk.version}
          </code>
        </SnapshotFact>
        <SnapshotFact label={isZh ? '黄金场景 / 锁文件' : 'Golden scenario / lockfile'}>
          <code>{compatibilitySnapshot.sample.scenario}</code>
          <br />
          Node.js <code>{compatibilitySnapshot.sample.node_requirement}</code>
          <br />
          SHA-256 <code>{compatibilitySnapshot.sample.package_lock_sha256}</code>
        </SnapshotFact>
        <SnapshotFact label={isZh ? '测试运行时目标' : 'Test runtime target'}>
          Node.js {compatibilitySnapshot.runtime.node} · {compatibilitySnapshot.runtime.package_manager}{' '}
          · {compatibilitySnapshot.runtime.browser.engine}{' '}
          {compatibilitySnapshot.runtime.browser.browser_version}
          <br />
          <code>
            {compatibilitySnapshot.runtime.browser.playwright_package}@
            {compatibilitySnapshot.runtime.browser.playwright_version}
          </code>{' '}
          · revision {compatibilitySnapshot.runtime.browser.revision} ·{' '}
          {isZh ? '其他浏览器未验证' : 'other browsers unverified'}
        </SnapshotFact>
      </dl>
      <div className="flex flex-wrap gap-4 border-t px-4 py-3">
        <a className="underline underline-offset-4" href={compatibilitySnapshot.contracts.compatibility}>
          compatibility.json
        </a>
        <a className="underline underline-offset-4" href={compatibilitySnapshot.contracts.openapi}>
          OpenAPI {isZh ? '子集' : 'subset'}
        </a>
      </div>
    </div>
  );
}

function SnapshotFact({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <dt className="text-xs font-medium text-fd-muted-foreground">{label}</dt>
      <dd className="mt-1 break-words">{children}</dd>
    </div>
  );
}

/** Renders the three-endpoint, non-exhaustive Product HTTP golden-path boundary. */
export function GoldenPathContract({ locale = 'en' }: { locale?: Locale }) {
  const isZh = locale === 'zh';

  return (
    <div className="not-prose my-6 rounded-xl border bg-fd-card p-4 text-sm">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <strong>{isZh ? 'JavaScript Web 黄金路径契约' : 'JavaScript Web golden-path contract'}</strong>
        <span className="rounded-full bg-blue-500/15 px-2 py-1 text-xs text-blue-700 dark:text-blue-300">
          {isZh ? '非完整 Beta 子集' : 'Non-exhaustive Beta subset'}
        </span>
      </div>
      <ul className="mt-3 space-y-1 font-mono text-xs">
        {goldenPathHTTPPaths.map((path) => (
          <li key={path}>{path}</li>
        ))}
      </ul>
      <p className="mt-3 text-fd-muted-foreground">
        {isZh
          ? '这些调用只允许出现在受信任的 localhost BFF 中；浏览器不能直接调用 Product HTTP API。'
          : 'These calls belong only in the trusted localhost BFF; the browser must not call the Product HTTP API directly.'}
      </p>
      <a
        className="mt-3 inline-block underline underline-offset-4"
        href={compatibilitySnapshot.contracts.openapi}
      >
        {isZh ? '下载 OpenAPI 3.1 子集' : 'Download the OpenAPI 3.1 subset'}
      </a>
    </div>
  );
}

/** Renders all wire-level ReasonCode values from the shared source-checked catalog. */
export function ReasonCodeTable({ locale = 'en' }: { locale?: Locale }) {
  const isZh = locale === 'zh';

  return (
    <div className="not-prose my-6 overflow-x-auto rounded-xl border">
      <table className="w-full min-w-[760px] border-collapse text-left text-sm">
        <caption className="sr-only">
          {isZh ? 'WuKongIM Wire ReasonCode 完整枚举' : 'Complete WuKongIM wire ReasonCode enum'}
        </caption>
        <thead className="bg-fd-muted/60">
          <tr>
            <th className="border-b px-3 py-2" scope="col">
              {isZh ? '值 / 名称' : 'Value / name'}
            </th>
            <th className="border-b px-3 py-2" scope="col">
              {isZh ? '阶段' : 'Stage'}
            </th>
            <th className="border-b px-3 py-2" scope="col">
              {isZh ? '重试指引' : 'Retry guidance'}
            </th>
            <th className="border-b px-3 py-2" scope="col">
              {isZh ? '可达性' : 'Reachability'}
            </th>
            <th className="border-b px-3 py-2" scope="col">
              {isZh ? '说明' : 'Meaning'}
            </th>
          </tr>
        </thead>
        <tbody>
          {reasonCodes.map((reason) => (
            <tr className="align-top odd:bg-fd-muted/20" key={reason.value}>
              <th className="border-b px-3 py-2 font-normal" scope="row">
                <code>{reason.value}</code>
                <br />
                <code>{reason.name}</code>
              </th>
              <td className="border-b px-3 py-2">{reason.stage}</td>
              <td className="border-b px-3 py-2">{retryLabels[locale][reason.retry]}</td>
              <td className="border-b px-3 py-2">
                {reachabilityLabels[locale][reason.reachability]}
              </td>
              <td className="border-b px-3 py-2">{reason.summary[locale]}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function ProtocolValueTable({
  locale,
  values,
  valueLabel,
  caption,
}: {
  locale: Locale;
  values: readonly ProtocolValueDefinition[];
  valueLabel: string;
  caption: string;
}) {
  const isZh = locale === 'zh';

  return (
    <div className="not-prose my-6 overflow-x-auto rounded-xl border">
      <table className="w-full min-w-[680px] border-collapse text-left text-sm">
        <caption className="sr-only">{caption}</caption>
        <thead className="bg-fd-muted/60">
          <tr>
            <th className="border-b px-3 py-2" scope="col">
              {valueLabel}
            </th>
            <th className="border-b px-3 py-2" scope="col">
              {isZh ? '名称' : 'Name'}
            </th>
            <th className="border-b px-3 py-2" scope="col">
              {isZh ? '范围' : 'Scope'}
            </th>
            <th className="border-b px-3 py-2" scope="col">
              {isZh ? '集成说明' : 'Integrator guidance'}
            </th>
          </tr>
        </thead>
        <tbody>
          {values.map((item) => (
            <tr className="align-top odd:bg-fd-muted/20" key={item.name}>
              <th className="border-b px-3 py-2 font-normal" scope="row">
                <code>{item.value}</code>
              </th>
              <td className="border-b px-3 py-2">
                <code>{item.name}</code>
              </td>
              <td className="border-b px-3 py-2">{protocolScopeLabels[locale][item.scope]}</td>
              <td className="border-b px-3 py-2">{item.summary[locale]}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

/** Renders every current wire Channel Type from the source-checked catalog. */
export function ChannelTypeTable({ locale = 'en' }: { locale?: Locale }) {
  return (
    <ProtocolValueTable
      caption={locale === 'zh' ? 'WuKongIM Channel Type 当前枚举' : 'Current WuKongIM Channel Type enum'}
      locale={locale}
      valueLabel={locale === 'zh' ? '值' : 'Value'}
      values={channelTypes}
    />
  );
}

/** Renders device categories and their same-category connection policy. */
export function DeviceFlagTable({ locale = 'en' }: { locale?: Locale }) {
  return (
    <>
      <ProtocolValueTable
        caption={locale === 'zh' ? 'WuKongIM Device Flag 当前枚举' : 'Current WuKongIM Device Flag enum'}
        locale={locale}
        valueLabel={locale === 'zh' ? '值' : 'Value'}
        values={deviceFlags}
      />
      <h3>{locale === 'zh' ? 'Device Level' : 'Device Levels'}</h3>
      <ProtocolValueTable
        caption={locale === 'zh' ? 'WuKongIM Device Level 当前枚举' : 'Current WuKongIM Device Level enum'}
        locale={locale}
        valueLabel={locale === 'zh' ? '值' : 'Value'}
        values={deviceLevels}
      />
    </>
  );
}

/** Renders fixed-header flags and message Setting bits from shared catalogs. */
export function MessageFlagTable({ locale = 'en' }: { locale?: Locale }) {
  const isZh = locale === 'zh';
  return (
    <>
      <h3>{isZh ? '固定 Header 位' : 'Fixed-header bits'}</h3>
      <ProtocolValueTable
        caption={isZh ? 'WKProto 消息固定 Header 位' : 'WKProto message fixed-header bits'}
        locale={locale}
        valueLabel="Bit"
        values={messageHeaderFlags.map((item) => ({ ...item, value: item.bit }))}
      />
      <h3>{isZh ? 'Setting 位值' : 'Setting bit values'}</h3>
      <ProtocolValueTable
        caption={isZh ? 'WKProto 消息 Setting 位值' : 'WKProto message Setting bit values'}
        locale={locale}
        valueLabel={isZh ? '值' : 'Value'}
        values={messageSettings}
      />
    </>
  );
}
