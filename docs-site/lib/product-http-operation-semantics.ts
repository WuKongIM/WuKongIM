import type {
  ProductHTTPOpenAPILocalizedText,
  ProductHTTPOpenAPILocale,
} from './product-http-openapi';

export interface ProductHTTPOperationSemantics {
  scope: ProductHTTPOpenAPILocalizedText;
  atomicity?: ProductHTTPOpenAPILocalizedText;
  success?: ProductHTTPOpenAPILocalizedText;
  recovery?: ProductHTTPOpenAPILocalizedText;
}

export interface LocalizedProductHTTPOperationSemantics {
  scope: string;
  atomicity?: string;
  success?: string;
  recovery?: string;
}

const text = (zh: string, en: string): ProductHTTPOpenAPILocalizedText => ({
  zh,
  en,
});

/**
 * Reviewed runtime semantics that cannot be expressed precisely with JSON
 * Schema alone. Keys are stable `METHOD path` pairs from the complete contract.
 */
export const productHTTPOperationSemantics = {
  'POST /user/token': {
    scope: text(
      'Token 持久化到集群；Master 设备的旧连接由请求处理节点在约 10 秒后发起关闭。',
      'The token is persisted cluster-wide; for a Master device, the handling node starts closing old sessions after about 10 seconds.',
    ),
    success: text(
      '200 表示 Token 写入已完成，不表示旧连接已经关闭。',
      'A 200 response means the token write completed, not that old sessions are already closed.',
    ),
  },
  'POST /user/device_quit': {
    scope: text(
      '设备关闭由请求处理节点在约 2 秒后执行；device_flag=-1 会依次处理 APP、Web、PC。',
      'The handling node performs session closure after about 2 seconds; device_flag=-1 processes APP, Web, and PC sequentially.',
    ),
    atomicity: text(
      '多设备退出不是事务；较早的设备可能已退出，而较后的设备失败。',
      'Multi-device sign-out is not transactional; earlier device classes may be signed out before a later one fails.',
    ),
    success: text(
      '目标设备不存在时仍返回 200。',
      'A missing target device still returns 200.',
    ),
  },
  'POST /user/systemuids_add': {
    scope: text(
      'UID 集合先持久化，再只更新请求处理进程的权限缓存。其他节点不会被本请求同步刷新。',
      'The UID set is persisted first, then only the handling process permission cache is updated. This request does not refresh other nodes.',
    ),
    recovery: text(
      '在每个服务节点刷新本地缓存，或滚动重启节点，使实际权限与持久集合一致。',
      'Refresh the local cache on every server node, or roll the nodes, so effective permission state matches the persisted set.',
    ),
  },
  'POST /user/systemuids_remove': {
    scope: text(
      'UID 集合先从持久状态移除，再只更新请求处理进程的权限缓存。',
      'UIDs are removed from durable state first, then only from the handling process permission cache.',
    ),
    recovery: text(
      '在每个服务节点刷新本地缓存，或滚动重启节点。',
      'Refresh the local cache on every server node, or roll the nodes.',
    ),
  },
  'GET /user/systemuids': {
    scope: text(
      '返回持久化的系统 UID 集合，不证明每个节点当前内存中的权限缓存已经一致。',
      'Returns the durable system-UID set; it does not prove that every node currently has the same in-memory permission cache.',
    ),
  },
  'POST /user/systemuids_add_to_cache': {
    scope: text(
      '只修改请求处理进程的内存缓存；不持久化，也不广播到其他节点。',
      'Changes only the handling process in-memory cache; it is neither persisted nor broadcast to other nodes.',
    ),
  },
  'POST /user/systemuids_remove_from_cache': {
    scope: text(
      '只修改请求处理进程的内存缓存；不持久化，也不广播到其他节点。',
      'Changes only the handling process in-memory cache; it is neither persisted nor broadcast to other nodes.',
    ),
  },
  'POST /channel': {
    scope: text(
      'Channel 元数据与成员写入集群存储；large 会在成员变更后按当前成员数重新计算。',
      'Channel metadata and membership are written to cluster storage; large is recomputed from the current member count after membership changes.',
    ),
    atomicity: text(
      '元数据、清空旧成员、分批加入成员和刷新 large 是多个阶段，不构成事务。',
      'Metadata update, old-member removal, chunked member insertion, and large refresh are separate stages, not one transaction.',
    ),
    recovery: text(
      '超时或 400/5xx 后按业务期望状态重放，并通过受保护的 Manager 查询核对成员。',
      'After a timeout or 400/5xx, replay the desired business state and verify membership through the protected Manager query.',
    ),
  },
  'POST /channel/subscriber_add': {
    scope: text(
      '目标 Channel 不存在时会被隐式创建；省略或传 0 的 channel_type 会按群组类型 2 处理。',
      'A missing target Channel is created implicitly; an omitted or zero channel_type is treated as group type 2.',
    ),
    atomicity: text(
      'reset 清空、分批加入和 large 刷新是多阶段操作，可能部分完成。',
      'Reset removal, chunked insertion, and large refresh are separate stages and may partially complete.',
    ),
  },
  'POST /channel/subscriber_remove': {
    scope: text(
      '只移除现有成员，不会隐式创建 Channel；channel_type=0 会原样传入。',
      'Removes existing members without implicitly creating the Channel; channel_type=0 is passed through unchanged.',
    ),
    atomicity: text(
      '成员移除与 large 刷新是两个阶段，可能部分完成。',
      'Member removal and large refresh are separate stages and may partially complete.',
    ),
  },
  'POST /channel/subscriber_remove_all': {
    scope: text(
      '清空普通订阅者后再刷新 large；不支持个人 Channel。',
      'Clears ordinary subscribers and then refreshes large; person Channels are not supported.',
    ),
    atomicity: text(
      '清空与 large 刷新不是同一事务。',
      'Removal and large refresh are not one transaction.',
    ),
  },
  'POST /tmpchannel/subscriber_set': {
    scope: text(
      '临时订阅者采用全量替换语义。',
      'Temporary subscribers use full-replacement semantics.',
    ),
    atomicity: text(
      '先清空再分批加入；失败时可能只留下部分新成员。',
      'The service removes all members before chunked insertion; failure may leave only part of the new set.',
    ),
  },
  'POST /channel/blacklist_set': {
    scope: text('拒绝列表采用全量替换语义。', 'The denylist uses full-replacement semantics.'),
    atomicity: text(
      '先清空再加入；两个阶段不构成事务。',
      'The list is removed and then inserted; the two stages are not transactional.',
    ),
  },
  'POST /channel/blacklist_add': {
    scope: text(
      '入口不验证父 Channel 是否存在；派生拒绝列表可以先于父 Channel 建立，并在父 Channel 后续出现时生效。',
      'The entry does not verify that the parent Channel exists; derived denylist state can be created first and become effective if the parent Channel appears later.',
    ),
  },
  'POST /channel/whitelist_set': {
    scope: text('允许列表采用全量替换语义。', 'The allowlist uses full-replacement semantics.'),
    atomicity: text(
      '先清空再加入；两个阶段不构成事务。',
      'The list is removed and then inserted; the two stages are not transactional.',
    ),
  },
  'POST /channel/whitelist_add': {
    scope: text(
      '入口不验证父 Channel 是否存在；派生允许列表可以先于父 Channel 建立，并在父 Channel 后续出现时生效。',
      'The entry does not verify that the parent Channel exists; derived allowlist state can be created first and become effective if the parent Channel appears later.',
    ),
  },
  'POST /message/send': {
    scope: text(
      '消息由受信后端提交；可选 X-WK-Trace-ID 必须是 32 位十六进制文本，否则服务端忽略并生成新值。',
      'Messages are submitted by a trusted backend; optional X-WK-Trace-ID must be 32 hexadecimal characters or the server ignores it and generates a new value.',
    ),
    success: text(
      'HTTP 200 仍必须检查 reason；只有成功 Reason Code 才表示消息被接受。',
      'Even with HTTP 200, inspect reason; only a success Reason Code means the message was accepted.',
    ),
  },
  'POST /message/event': {
    scope: text(
      'visibility 作为事件元数据保存；当前 Product HTTP 消息同步不会用它做访问控制过滤。',
      'visibility is stored as event metadata; current Product HTTP message synchronization does not use it as an access-control filter.',
    ),
  },
  'POST /channel/messagesync': {
    scope: text(
      '读取前必须存在 login_uid 的普通成员关系；join_seq 与 deleted_to_seq 共同形成最低可见序号。返回消息始终按 message_seq 升序排列。',
      'The login_uid must have an ordinary membership before reading; join_seq and deleted_to_seq establish the lowest visible sequence. Returned messages are always ordered by ascending message_seq.',
    ),
    success: text(
      'pull_mode=1 读取 [start_message_seq, end_message_seq)；其他值向旧消息读取 (end_message_seq, start_message_seq]。边界为 0 时使用开放端。',
      'pull_mode=1 reads [start_message_seq, end_message_seq); other values read older messages in (end_message_seq, start_message_seq]. A zero boundary selects the open end.',
    ),
  },
  'POST /message/sync': {
    scope: text(
      '消息来自持久存储，但本次最新确认 generation 只保存在请求处理进程内存中，默认约 5 分钟且最多 4096 个 UID。',
      'Messages come from durable storage, but the latest acknowledgement generation is kept only in the handling process memory, by default for about 5 minutes and at most 4,096 UIDs.',
    ),
    success: text(
      'limit 限制返回数量，不限制服务端枚举命令 Channel 与扫描消息的总成本。',
      'limit bounds returned messages, not the total cost of enumerating command Channels and scanning messages.',
    ),
    recovery: text(
      'sync 与 syncack 必须保持节点亲和；进程重启、过期或驱逐后应重新 sync。',
      'Keep sync and syncack node-affine; run sync again after process restart, expiry, or eviction.',
    ),
  },
  'POST /message/syncack': {
    scope: text(
      '确认只消费当前进程内最近一次 sync 记录的 generation；last_message_seq 仅校验为正数，实际不会参与确认。',
      'Acknowledgement consumes only the generation recorded by the latest sync in this process; last_message_seq is only validated as positive and is not used for acknowledgement.',
    ),
    success: text(
      '找不到本地 generation 时仍返回 200，且不会确认任何消息。',
      'If no local generation exists, the endpoint still returns 200 and acknowledges nothing.',
    ),
  },
  'POST /message/cmd/bind': {
    scope: text(
      '持久化用户与命令 Channel 的发现绑定；后续 /message/sync 仍依赖当前进程中的确认 generation。',
      'Persists discovery binding between a user and a command Channel; later /message/sync acknowledgement still depends on the current-process generation record.',
    ),
  },
  'POST /channel/messagesyncbatch': {
    scope: text(
      '每个 items 条目独立执行；响应顺序与请求一致。',
      'Each items entry runs independently; response order matches request order.',
    ),
    success: text(
      'HTTP 200 仍需逐项检查 error；一个条目失败不代表整个批次使用非 2xx。',
      'Even with HTTP 200, inspect each item error; one failed item does not make the whole batch non-2xx.',
    ),
  },
  'POST /conversation/list': {
    scope: text(
      'limit 限制本页扫描的 membership 条目数，不保证 conversations 数组达到该数量；墓碑进入 deletes，暂时无法补水的条目进入 unresolved。',
      'limit bounds membership rows scanned, not the number of returned conversations; tombstones go to deletes and temporarily unhydrated entries go to unresolved.',
    ),
    success: text(
      '只以 done=true 结束完整轮次并保存 coverage；reset_required=true 时必须重建本地会话目录。',
      'Only done=true completes a full pass and permits saving coverage; reset_required=true requires rebuilding the local Conversation directory.',
    ),
  },
  'POST /conversation/retry': {
    scope: text(
      '只重新解析上一轮 unresolved 中的最多 200 个 Channel Key，不替代 list 的完整游标扫描。',
      'Re-resolves at most 200 Channel keys from a previous unresolved result; it does not replace the complete cursor scan performed by list.',
    ),
  },
} as const satisfies Record<string, ProductHTTPOperationSemantics>;

export function operationKey(method: string, path: string) {
  return `${method.toUpperCase()} ${path}`;
}

export function getProductHTTPOperationSemantics(method: string, path: string) {
  return productHTTPOperationSemantics[
    operationKey(method, path) as keyof typeof productHTTPOperationSemantics
  ];
}

export function localizeProductHTTPOperationSemantics(
  semantics: ProductHTTPOperationSemantics,
  locale: ProductHTTPOpenAPILocale,
): LocalizedProductHTTPOperationSemantics {
  return Object.fromEntries(
    Object.entries(semantics).map(([name, value]) => [name, value[locale]]),
  ) as unknown as LocalizedProductHTTPOperationSemantics;
}

export function applyProductHTTPOperationSemantics(
  document: unknown,
  locale: ProductHTTPOpenAPILocale,
) {
  if (!document || typeof document !== 'object') return;
  const paths = (document as { paths?: Record<string, Record<string, unknown>> }).paths;
  if (!paths) return;

  for (const [path, pathItem] of Object.entries(paths)) {
    for (const [method, value] of Object.entries(pathItem)) {
      if (!value || typeof value !== 'object') continue;
      const semantics = getProductHTTPOperationSemantics(method, path);
      if (!semantics) continue;
      (value as Record<string, unknown>)['x-wukongim-semantics'] =
        localizeProductHTTPOperationSemantics(semantics, locale);
    }
  }
}
