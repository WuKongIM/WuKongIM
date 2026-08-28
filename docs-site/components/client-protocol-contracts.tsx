import {
  clientProtocolDirectionLabels,
  clientProtocolFrames,
  clientProtocolScopeLabels,
  type ClientProtocolFrameScope,
  type ClientProtocolLocale,
} from '@/lib/client-protocol-contracts';

const scopeClasses: Record<ClientProtocolFrameScope, string> = {
  'public-core': 'bg-emerald-500/15 text-emerald-800 dark:text-emerald-200',
  'codec-only': 'bg-amber-500/15 text-amber-800 dark:text-amber-200',
  reserved: 'bg-fd-muted text-fd-muted-foreground',
};

/** Renders the complete source-aligned WKProto FrameType catalog. */
export function ClientProtocolPacketTable({
  locale = 'en',
}: {
  locale?: ClientProtocolLocale;
}) {
  const isZh = locale === 'zh';

  return (
    <div
      aria-label={isZh ? 'WKProto 数据包目录' : 'WKProto packet catalog'}
      className="not-prose my-6 overflow-x-auto rounded-xl border"
      role="region"
      tabIndex={0}
    >
      <table className="w-full min-w-[760px] border-collapse text-left text-sm">
        <caption className="sr-only">
          {isZh ? 'WKProto FrameType 值、方向与发布范围' : 'WKProto FrameType values, directions, and publication scope'}
        </caption>
        <thead className="bg-fd-muted/60">
          <tr>
            <th className="border-b px-3 py-2" scope="col">
              {isZh ? '值 / 名称' : 'Value / name'}
            </th>
            <th className="border-b px-3 py-2" scope="col">
              {isZh ? '方向' : 'Direction'}
            </th>
            <th className="border-b px-3 py-2" scope="col">
              {isZh ? '范围' : 'Scope'}
            </th>
            <th className="border-b px-3 py-2" scope="col">
              {isZh ? '说明' : 'Meaning'}
            </th>
          </tr>
        </thead>
        <tbody>
          {clientProtocolFrames.map((item) => (
            <tr className="align-top odd:bg-fd-muted/20" key={item.value}>
              <th className="border-b px-3 py-2 font-normal" scope="row">
                <code>
                  {item.value} · {item.name}
                </code>
              </th>
              <td className="border-b px-3 py-2">
                {clientProtocolDirectionLabels[locale][item.direction]}
              </td>
              <td className="border-b px-3 py-2">
                <span className={`rounded-full px-2 py-1 text-xs ${scopeClasses[item.scope]}`}>
                  {clientProtocolScopeLabels[locale][item.scope]}
                </span>
              </td>
              <td className="border-b px-3 py-2">{item.summary[locale]}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
