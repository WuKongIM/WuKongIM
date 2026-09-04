export type ProductHTTPExplanationLocale = 'zh' | 'en';

interface LocalizedExplanation {
  zh: string;
  en: string;
}

interface SchemaExplanations {
  description: LocalizedExplanation;
  properties?: Record<string, LocalizedExplanation>;
}

interface OpenAPIRecord {
  description?: string;
  components?: {
    parameters?: Record<string, OpenAPIRecord>;
    requestBodies?: Record<string, OpenAPIRecord>;
    schemas?: Record<
      string,
      OpenAPIRecord & { properties?: Record<string, OpenAPIRecord> }
    >;
  };
}

const explanation = (zh: string, en: string): LocalizedExplanation => ({ zh, en });

const uid = explanation('目标用户的唯一标识。', 'Unique identifier of the target user.');
const loginUID = explanation(
  '发起读取且必须拥有对应 Channel 成员关系的用户标识。',
  'User identity performing the read and required to own the corresponding Channel membership.',
);
const channelID = explanation(
  '目标 Channel 的逻辑标识；个人 Channel 场景通常填写对方 UID。',
  'Logical identifier of the target Channel; for a person Channel this is normally the other UID.',
);
const channelType = explanation(
  'Channel 类型编号；1 表示个人 Channel，2 表示群组 Channel，其他值属于扩展类型。',
  'Numeric Channel type; 1 is a person Channel, 2 is a group Channel, and other values are extension types.',
);
const compatibilityFlag = (nameZh: string, nameEn: string) =>
  explanation(
    `${nameZh}兼容标志；仅 1 表示启用，其他整数均表示关闭。`,
    `${nameEn} compatibility flag; exactly 1 enables it and every other integer disables it.`,
  );
const startMessageSeq = explanation(
  '读取起点的 Channel 消息序号；0 表示未指定显式起点。',
  'Channel message sequence used as the read starting point; 0 means no explicit starting point.',
);
const endMessageSeq = explanation(
  '读取终点的 Channel 消息序号；0 表示未指定显式终点。',
  'Channel message sequence used as the read ending point; 0 means no explicit ending point.',
);
const syncLimit = explanation(
  '本次最多返回的消息数；小于等于 0 时使用 100，大于 10000 时截断为 10000。',
  'Maximum messages returned by this read; values at or below 0 use 100 and values above 10000 are capped at 10000.',
);
const pullMode = explanation(
  '拉取方向；1 表示向更新消息拉取，其他 uint8 值走向更旧消息拉取的兼容分支。',
  'Pull direction; 1 reads toward newer messages and every other uint8 value uses the compatibility branch for older messages.',
);
const includeEventMeta = explanation(
  '消息事件摘要开关；非 0 时请求事件元数据，且 event_summary_mode 为空时默认使用 full。',
  'Message-event summary switch; a non-zero value requests event metadata and defaults an empty event_summary_mode to full.',
);
const eventSummaryMode = explanation(
  '事件摘要模式；空字符串不补充摘要，full 包含完整快照，其他非空值返回不含快照的基础摘要。',
  'Event-summary mode; empty disables enrichment, full includes snapshots, and any other non-empty value returns the basic summary without snapshots.',
);

/**
 * Reviewed, bilingual explanations for every Product HTTP input parameter.
 *
 * The structural OpenAPI contract remains the source of names, types, and
 * constraints. This overlay owns prose only so repeated compatibility DTOs do
 * not need to duplicate Chinese and English text throughout the JSON file.
 */
export const productHTTPParameterExplanations = {
  parameters: {
    NodeIDSnake: explanation(
      '首选节点选择参数；填写时必须是已配置且大于 0 的节点 ID。',
      'Preferred node selector; when supplied it must identify a configured node with an ID greater than 0.',
    ),
    NodeIDCamel: explanation(
      '旧版 nodeId 别名；仅在未提供 node_id 时读取。',
      'Legacy nodeId alias, read only when node_id is absent.',
    ),
    NodeIDUpperCamel: explanation(
      '旧版 nodeID 别名；仅在 node_id 与 nodeId 都未提供时读取。',
      'Legacy nodeID alias, read only when both node_id and nodeId are absent.',
    ),
    Intranet: explanation(
      '地址范围选择参数；任何可解析的非 0 整数选择内网地址，无效文本按 0 处理并选择公网地址。',
      'Address-scope selector; any parseable non-zero integer selects intranet addresses, while invalid text behaves as 0 and selects public addresses.',
    ),
    ChannelIDQuery: explanation(
      '要读取允许列表的 Channel ID；此旧接口不会校验该查询参数。',
      'Channel ID whose allowlist is read; this legacy endpoint does not validate the query parameter.',
    ),
    ChannelTypeQuery: explanation(
      '要读取允许列表的 Channel 类型；按 uint8 解析，缺失、无效或越界文本会静默按 0 处理。',
      'Channel type whose allowlist is read; parsed as uint8, with missing, invalid, or out-of-range text silently treated as 0.',
    ),
  },
  requestBodies: {
    RouteBatch: explanation('要查询路由的一组 UID JSON 数组。', 'JSON array of UIDs whose route group is requested.'),
    UpdateToken: explanation('要保存的用户、设备与 Token 元数据。', 'User, device, and token metadata to store.'),
    DeviceQuit: explanation('要退出的用户与设备类别。', 'User and device category to sign out.'),
    OnlineStatus: explanation('要查询在线路由的一组 UID JSON 数组。', 'JSON array of UIDs whose active routes are queried.'),
    SystemUIDs: explanation('当前系统 UID 变更所使用的 UID 集合。', 'UID set used by the current system-identity mutation.'),
    ChannelInfo: explanation('要全量替换的 Channel 元数据；所有字段均保留旧式零值行为。', 'Channel metadata to replace in full; every field preserves legacy zero-value behavior.'),
    ChannelUpsert: explanation('要创建或更新的 Channel 元数据及可选订阅者快照。', 'Channel metadata to create or update plus an optional subscriber snapshot.'),
    WeakChannelKey: explanation('旧式弱校验 Channel Key。', 'Legacy, weakly validated Channel key.'),
    ChannelSubscriberAdd: explanation('要添加或替换的普通订阅者。', 'Ordinary subscribers to add or replace.'),
    ChannelSubscriberRemove: explanation('要移除的普通订阅者。', 'Ordinary subscribers to remove.'),
    NonPersonChannelKey: explanation('要清空订阅者的非个人 Channel Key。', 'Non-person Channel key whose subscribers are cleared.'),
    TemporarySubscribers: explanation('临时 Channel ID 与替换后的 UID 集合。', 'Temporary Channel ID and replacement UID set.'),
    ChannelMembers: explanation('要添加或移除的拒绝列表成员。', 'Denylist members to add or remove.'),
    ChannelMemberSet: explanation('要写入的完整允许列表或拒绝列表快照。', 'Complete allowlist or denylist snapshot to write.'),
    ChannelAllowlistMembers: explanation('要添加或移除的允许列表成员。', 'Allowlist members to add or remove.'),
    ChannelKey: explanation('要清空派生成员列表的 Channel Key。', 'Channel key whose derived member list is cleared.'),
    SendMessage: explanation('受信后端要提交的消息及其投递选项。', 'Message and delivery options submitted by the trusted backend.'),
    AppendMessageEvent: explanation('要投影到一条消息上的事件。', 'Event to project onto one message.'),
    MessageSync: explanation('命令消息同步的用户与分页上限。', 'User and page limit for command-message synchronization.'),
    MessageSyncAck: explanation('要确认最新命令消息 generation 的用户。', 'User acknowledging the latest command-message generation.'),
    MessageCMDBinding: explanation('用户与命令 Channel 的持久发现绑定。', 'Persistent discovery binding between a user and command Channel.'),
    ChannelMessageSync: explanation('单个 Channel 的已提交消息读取条件。', 'Committed-message read criteria for one Channel.'),
    ChannelMessageSyncBatch: explanation('一个用户及最多 200 个 Channel 的消息读取条件。', 'One user and message-read criteria for up to 200 Channels.'),
    ConversationList: explanation('会话目录分页与删除覆盖参数。', 'Conversation-directory pagination and deletion-coverage inputs.'),
    ConversationRetry: explanation('要重新解析的有界会话 Key 集合。', 'Bounded set of Conversation keys to resolve again.'),
    ConversationMutation: explanation('要清未读、隐藏或激活的用户会话 Key。', 'User Conversation key to clear, hide, or activate.'),
    ConversationSetUnread: explanation('用户会话 Key 与期望保留的最大未读数。', 'User Conversation key and maximum unread count to retain.'),
    ConversationSyncLegacy: explanation('旧式会话同步的游标、过滤与分页参数。', 'Cursor, filter, and pagination inputs for legacy Conversation sync.'),
  },
  schemas: {
    RouteBatchRequest: {
      description: explanation('UID 字符串数组；JSON null 也会按空数组兼容接收，当前入口不限制数量。', 'Array of UID strings; JSON null is also accepted as an empty array, and the current entry does not bound its size.'),
    },
    UpdateTokenRequest: {
      description: explanation('设备 Token 更新参数。', 'Device-token update parameters.'),
      properties: {
        uid: explanation('要创建或更新设备 Token 的用户 ID；不得包含 @、# 或 &。', 'User ID whose device token is created or updated; it must not contain @, #, or &.'),
        token: explanation('与该用户和设备类别一起持久化的不透明 Token；默认 Gateway 要求后续 CONNECT 凭据与它完全匹配。', 'Opaque token persisted for this user and device category; the default Gateway requires later CONNECT credentials to match it exactly.'),
        device_flag: explanation('设备类别；0=APP、1=Web、2=PC、99=系统设备，省略时为 0。', 'Device category; 0=APP, 1=Web, 2=PC, and 99=system, defaulting to 0 when omitted.'),
        device_level: explanation('同类设备冲突级别；0=Slave、1=Master，省略时为 0。', 'Same-category device conflict level; 0=Slave and 1=Master, defaulting to 0 when omitted.'),
      },
    },
    DeviceQuitRequest: {
      description: explanation('设备退出参数；入口保留旧式弱校验。', 'Device sign-out parameters with legacy weak validation.'),
      properties: {
        uid: explanation('要退出设备的用户 ID；入口本身不校验空值。', 'User ID whose device is signed out; the entry itself does not reject an empty value.'),
        device_flag: explanation('要退出的设备类别；-1 同时选择 APP、Web 与 PC，其他整数在用例层转为 uint8。', 'Device category to sign out; -1 selects APP, Web, and PC together, while other integers are converted to uint8 by the use case.'),
      },
    },
    OnlineStatusRequest: {
      description: explanation('UID 字符串数组；JSON null 或空数组返回旧式 status 对象，当前入口不限制数量。', 'Array of UID strings; JSON null or an empty array returns the legacy status object, and the current entry does not bound its size.'),
    },
    SystemUIDsRequest: {
      description: explanation('系统身份变更参数。', 'System-identity mutation parameters.'),
      properties: {
        uids: explanation('要添加或移除的系统 UID；可省略、为空或为 null，入口不校验元素。', 'System UIDs to add or remove; the field may be omitted, empty, or null, and elements are not validated at entry.'),
      },
    },
    CompatibilityFlag: {
      description: explanation('旧式整数布尔值；仅 1 表示 true，其他整数均表示 false。', 'Legacy integer boolean; exactly 1 means true and every other integer means false.'),
    },
    ChannelInfoRequest: {
      description: explanation('无必填字段且采用全量零值覆盖语义的旧式 Channel 元数据。', 'Legacy Channel metadata with no required fields and full zero-value replacement semantics.'),
      properties: {
        channel_id: explanation('要更新的 Channel ID；省略时为空字符串，入口不校验。', 'Channel ID to update; omission produces an empty string and the entry does not validate it.'),
        channel_type: explanation('要更新的 Channel 类型；省略时为 0，入口不校验。', 'Channel type to update; omission produces 0 and the entry does not validate it.'),
        large: compatibilityFlag('大群组', 'Large-group'),
        ban: compatibilityFlag('全 Channel 禁用', 'Channel-ban'),
        disband: compatibilityFlag('终态解散', 'Terminal-disband'),
        send_ban: compatibilityFlag('禁止发送', 'Send-ban'),
        allow_stranger: compatibilityFlag('允许陌生人发送', 'Allow-stranger'),
      },
    },
    ChannelUpsertRequest: {
      description: explanation('Channel 元数据全量更新及可选订阅者替换参数。', 'Full Channel metadata update plus optional subscriber replacement parameters.'),
      properties: {
        channel_id: explanation('要创建或更新的非空白 Channel ID；不得包含 # 或 @。', 'Non-blank Channel ID to create or update; it must not contain # or @.'),
        channel_type: channelType,
        large: compatibilityFlag('大群组', 'Large-group'),
        ban: compatibilityFlag('全 Channel 禁用', 'Channel-ban'),
        disband: compatibilityFlag('终态解散', 'Terminal-disband'),
        send_ban: compatibilityFlag('禁止发送', 'Send-ban'),
        allow_stranger: compatibilityFlag('允许陌生人发送', 'Allow-stranger'),
        reset: compatibilityFlag('订阅者快照替换', 'Subscriber-snapshot replacement'),
        subscribers: explanation('要添加的普通订阅者 UID；可省略、为空或为 null，个人 Channel 不允许非空列表，入口不校验元素。', 'Ordinary subscriber UIDs to add; it may be omitted, empty, or null, a person Channel rejects a non-empty list, and elements are not validated at entry.'),
      },
    },
    WeakChannelKeyRequest: {
      description: explanation('兼容保留的弱校验 Channel Key。', 'Compatibility Channel key with deliberately weak validation.'),
      properties: {
        channel_id: explanation('要解散的 Channel ID；省略时为空字符串，入口不校验。', 'Channel ID to disband; omission produces an empty string and the entry does not validate it.'),
        channel_type: explanation('要解散的 Channel 类型；1 会被拒绝，其他值（包括 0）继续交给用例。', 'Channel type to disband; 1 is rejected while every other value, including 0, is passed to the use case.'),
      },
    },
    ChannelSubscriberAddRequest: {
      description: explanation('普通订阅者添加或替换参数。', 'Ordinary-subscriber add or replacement parameters.'),
      properties: {
        channel_id: explanation('目标非空白 Channel ID；不得包含 # 或 @。', 'Target non-blank Channel ID; it must not contain # or @.'),
        channel_type: explanation('目标 Channel 类型；省略或为 0 时转为群组类型 2，类型 1 会被拒绝。', 'Target Channel type; omission or 0 becomes group type 2, while type 1 is rejected.'),
        reset: compatibilityFlag('订阅者快照替换', 'Subscriber-snapshot replacement'),
        temp_subscriber: explanation('已废弃的临时订阅者标志；值 1 会被拒绝，其他值被忽略。', 'Deprecated temporary-subscriber flag; value 1 is rejected and every other value is ignored.'),
        subscribers: explanation('一个或多个要添加的非空白订阅者 UID。', 'One or more non-blank subscriber UIDs to add.'),
      },
    },
    ChannelSubscriberRemoveRequest: {
      description: explanation('普通订阅者移除参数。', 'Ordinary-subscriber removal parameters.'),
      properties: {
        channel_id: explanation('目标非空白 Channel ID；不得包含 # 或 @。', 'Target non-blank Channel ID; it must not contain # or @.'),
        channel_type: explanation('目标 Channel 类型；类型 1 会被拒绝，省略或为 0 时不会归一化而是原样传入。', 'Target Channel type; type 1 is rejected, while omission or 0 is passed through without normalization.'),
        reset: explanation('兼容接收的整数标志；当前移除操作不会使用它。', 'Integer flag accepted for compatibility but not used by the removal operation.'),
        temp_subscriber: explanation('兼容接收但被忽略的旧临时订阅者标志。', 'Legacy temporary-subscriber flag accepted and ignored for compatibility.'),
        subscribers: explanation('一个或多个要移除的非空白订阅者 UID。', 'One or more non-blank subscriber UIDs to remove.'),
      },
    },
    NonPersonChannelKeyRequest: {
      description: explanation('非个人 Channel Key。', 'Non-person Channel key.'),
      properties: { channel_id: channelID, channel_type: explanation('非个人 Channel 类型，因此允许范围为 2–255。', 'Non-person Channel type, so the accepted range is 2–255.') },
    },
    TemporarySubscriberSetRequest: {
      description: explanation('临时订阅者完整快照。', 'Complete temporary-subscriber snapshot.'),
      properties: {
        channel_id: explanation('临时 Channel ID；不得为空，也不得包含 # 或 @。', 'Temporary Channel ID; it must not be empty or contain # or @.'),
        uids: explanation('替换后的临时订阅者 UID；至少一个，入口不校验元素内容。', 'Replacement temporary-subscriber UIDs; at least one is required and entry does not validate element content.'),
      },
    },
    ChannelMemberMutationRequest: {
      description: explanation('拒绝列表增删参数。', 'Denylist add or remove parameters.'),
      properties: { channel_id: channelID, channel_type: channelType, uids: explanation('一个或多个要添加或移除的 UID；入口不校验元素内容。', 'One or more UIDs to add or remove; entry does not validate element content.') },
    },
    ChannelMemberSetRequest: {
      description: explanation('允许列表或拒绝列表的完整替换参数，保留旧式弱校验。', 'Complete allowlist or denylist replacement parameters with legacy weak validation.'),
      properties: {
        channel_id: explanation('目标非空白 Channel ID。', 'Target non-blank Channel ID.'),
        channel_type: explanation('目标 Channel 类型；可省略或为 0，入口不会拒绝。', 'Target Channel type; it may be omitted or 0 and is not rejected at entry.'),
        uids: explanation('替换后的完整 UID 集合；可省略、为空或为 null，入口不校验元素。', 'Complete replacement UID set; it may be omitted, empty, or null, and elements are not validated at entry.'),
      },
    },
    ChannelAllowlistMutationRequest: {
      description: explanation('允许列表增删参数。', 'Allowlist add or remove parameters.'),
      properties: {
        channel_id: explanation('目标 Channel ID；不得为空，也不得包含 # 或 @。', 'Target Channel ID; it must not be empty or contain # or @.'),
        channel_type: channelType,
        uids: explanation('一个或多个要添加或移除的非空白 UID。', 'One or more non-blank UIDs to add or remove.'),
      },
    },
    ChannelKeyRequest: {
      description: explanation('经基本校验的 Channel Key。', 'Channel key with basic validation.'),
      properties: { channel_id: channelID, channel_type: channelType },
    },
    SendMessageHeaderRequest: {
      description: explanation('消息固定 Header 的兼容标志；同名顶层字段会与这些值执行逻辑或。', 'Compatibility flags for the message fixed header; same-named top-level fields are ORed with these values.'),
      properties: {
        no_persist: explanation('非 0 表示不持久化该消息。', 'A non-zero value makes the message non-durable.'),
        sync_once: explanation('非 0 表示按一次性命令消息处理。', 'A non-zero value treats the message as a one-shot command message.'),
      },
    },
    SendMessageRequest: {
      description: explanation('完整的兼容消息发送参数。', 'Complete compatibility message-send parameters.'),
      properties: {
        from_uid: explanation('消息发送者 UID；为空时才回退读取 sender_uid。', 'Message sender UID; sender_uid is consulted only when this value is empty.'),
        sender_uid: explanation('已废弃的发送者 UID 别名；仅在 from_uid 为空时使用。', 'Deprecated sender-UID alias used only when from_uid is empty.'),
        device_id: explanation('调用方提供的发送设备标识；可为空。', 'Caller-supplied sending-device identifier; it may be empty.'),
        channel_id: explanation('目标 Channel ID；普通发送时必须非空，请求级 subscribers 发送时必须为空。', 'Target Channel ID; required for an ordinary send and required to be empty for a request-scoped subscribers send.'),
        channel_type: explanation('目标 Channel 类型；普通发送时必须大于 0，请求级 subscribers 发送时使用 0。', 'Target Channel type; it must be greater than 0 for an ordinary send and is 0 for a request-scoped subscribers send.'),
        client_msg_no: explanation('可选的客户端幂等键；空字符串会关闭幂等查找。', 'Optional client idempotency key; an empty string disables idempotency lookup.'),
        setting: explanation('WKProto Setting 位图；应使用 SDK 常量组合，不要手写未知位。', 'WKProto Setting bitset; combine SDK constants instead of writing unknown bits by hand.'),
        topic: explanation('可选 Topic；只有相应 Setting 位和客户端能力匹配时才有协议含义。', 'Optional Topic; it has protocol meaning only when the matching Setting bit and client capability are present.'),
        expire: explanation('调用方提供的消息过期秒数；0 表示不设置过期。', 'Caller-supplied message expiration in seconds; 0 means no expiration is set.'),
        payload: explanation('Base64 编码后的消息 Payload；解码失败会返回 400。', 'Base64-encoded message payload; decoding failure returns 400.'),
        subscribers: explanation('请求级投递 UID；非空时 channel_id 必须为空且 sync_once 必须启用，可省略、为空或为 null。', 'Request-scoped delivery UIDs; when non-empty, channel_id must be empty and sync_once must be enabled; the field may be omitted, empty, or null.'),
        header: explanation('可选兼容 Header 标志；可为对象、null 或省略。', 'Optional compatibility header flags; it may be an object, null, or omitted.'),
        no_persist: explanation('顶层兼容标志；非 0 表示不持久化，并与 header.no_persist 执行逻辑或。', 'Top-level compatibility flag; non-zero means non-durable and is ORed with header.no_persist.'),
        sync_once: explanation('顶层兼容标志；非 0 表示一次性命令消息，并与 header.sync_once 执行逻辑或。', 'Top-level compatibility flag; non-zero means a one-shot command message and is ORed with header.sync_once.'),
      },
    },
    AppendMessageEventRequest: {
      description: explanation('消息事件投影参数。', 'Message-event projection parameters.'),
      properties: {
        channel_id: explanation('目标非空白 Channel ID；个人和 Agent Channel 会在用例层归一化。', 'Target non-blank Channel ID; person and Agent Channels are normalized by the use case.'),
        channel_type: channelType,
        from_uid: explanation('事件发起者 UID；个人或未编码 Agent Channel 的归一化可能需要它。', 'Event actor UID; person or unencoded Agent Channel normalization may require it.'),
        message_id: explanation('可选服务端消息 ID；正数转为 uint64，0 或负数不会传入事件命令。', 'Optional server message ID; positive values become uint64, while 0 or negative values are omitted from the event command.'),
        client_msg_no: explanation('事件所对应消息的非空白客户端消息编号。', 'Non-blank client message number of the message receiving the event.'),
        event_id: explanation('该事件的非空白唯一标识。', 'Non-blank unique identifier of this event.'),
        event_type: explanation('事件类型；会去除首尾空白并转为小写。', 'Event type; surrounding whitespace is removed and the value is lowercased.'),
        event_key: explanation('事件 lane Key；空值使用默认 lane，stream.finish 强制使用 finish lane。', 'Event lane key; empty selects the default lane and stream.finish forces the finish lane.'),
        visibility: explanation('调用方提供的可见性标记；会去除首尾空白。', 'Caller-supplied visibility marker; surrounding whitespace is removed.'),
        occurred_at: explanation('调用方提供的事件发生时间整数；入口不解释时间单位。', 'Caller-supplied integer event time; the entry does not interpret its unit.'),
        payload: explanation('任意 JSON 事件 Payload；服务端按原始 JSON 字节传给事件存储。', 'Arbitrary JSON event payload passed to event storage as raw JSON bytes.'),
        headers: explanation('预留字段；只能省略或传 null，任何非 null JSON 值都会被拒绝。', 'Reserved field; it must be omitted or null, and every non-null JSON value is rejected.'),
      },
    },
    MessageSyncRequest: {
      description: explanation('旧式命令消息同步参数。', 'Legacy command-message synchronization parameters.'),
      properties: {
        uid: explanation('要同步命令消息的非空白用户 ID。', 'Non-blank user ID whose command messages are synchronized.'),
        message_seq: explanation('已废弃的兼容输入；服务端接收但忽略。', 'Deprecated compatibility input accepted and ignored by the server.'),
        limit: explanation('本次最多返回的消息数；0 使用 200，负数被拒绝，大于 10000 时截断为 10000。', 'Maximum messages returned; 0 uses 200, negative values are rejected, and values above 10000 are capped at 10000.'),
      },
    },
    MessageSyncAckRequest: {
      description: explanation('旧式命令消息同步确认参数。', 'Legacy command-message synchronization acknowledgement parameters.'),
      properties: {
        uid: explanation('确认命令消息同步的非空白用户 ID。', 'Non-blank user ID acknowledging command-message synchronization.'),
        last_message_seq: explanation('必须为正数的兼容字段；确认逻辑实际使用服务端记录的最新 generation，而不使用该输入值。', 'Compatibility field that must be positive; acknowledgement uses the latest server-recorded generation rather than this supplied value.'),
      },
    },
    MessageCMDBindingRequest: {
      description: explanation('命令 Channel 离线发现绑定参数。', 'Command-Channel offline-discovery binding parameters.'),
      properties: { uid, channel_id: channelID, channel_type: channelType },
    },
    ChannelMessageSyncRequest: {
      description: explanation('单 Channel 已提交消息同步参数。', 'Single-Channel committed-message synchronization parameters.'),
      properties: {
        login_uid: loginUID,
        channel_id: channelID,
        channel_type: channelType,
        start_message_seq: startMessageSeq,
        end_message_seq: endMessageSeq,
        limit: syncLimit,
        pull_mode: pullMode,
        include_event_meta: includeEventMeta,
        event_summary_mode: eventSummaryMode,
      },
    },
    ChannelMessageSyncBatchItemRequest: {
      description: explanation('批量同步中的单个 Channel 读取条件。', 'Read criteria for one Channel inside a batch synchronization request.'),
      properties: {
        login_uid: explanation('兼容接收但忽略；批量请求始终使用顶层 login_uid。', 'Accepted for compatibility but ignored; batch requests always use the top-level login_uid.'),
        channel_id: channelID,
        channel_type: channelType,
        start_message_seq: startMessageSeq,
        end_message_seq: endMessageSeq,
        limit: syncLimit,
        pull_mode: pullMode,
        include_event_meta: includeEventMeta,
        event_summary_mode: eventSummaryMode,
      },
    },
    ChannelMessageSyncBatchRequest: {
      description: explanation('批量 Channel 已提交消息同步参数。', 'Batch Channel committed-message synchronization parameters.'),
      properties: {
        login_uid: loginUID,
        items: explanation('要同步的 Channel 条目；至少 1 个，最多 200 个，并保持请求顺序返回。', 'Channels to synchronize; between 1 and 200 items are accepted and response order matches request order.'),
      },
    },
    ConversationListRequest: {
      description: explanation('会话目录分页参数。', 'Conversation-directory pagination parameters.'),
      properties: {
        uid: explanation('要读取会话目录的用户 ID。', 'User ID whose Conversation directory is read.'),
        cursor: explanation('上页返回的不透明 next_cursor；首轮请求省略或传空字符串。', 'Opaque next_cursor from the previous page; omit it or send an empty string for the first page.'),
        limit: explanation('本页最多扫描的 membership 条目数；0 使用默认值 50，最大 200。', 'Maximum membership entries scanned for this page; 0 uses the default 50 and the maximum is 200.'),
        completed_coverage: explanation('客户端最近一次 done=true 完整轮次保存的 coverage；首次同步传 0。', 'Coverage saved from the client\'s latest pass that ended with done=true; send 0 for the first synchronization.'),
      },
    },
    ConversationKey: {
      description: explanation('一个会话的 Channel Key。', 'Channel key for one Conversation.'),
      properties: { channel_id: channelID, channel_type: channelType },
    },
    ConversationRetryRequest: {
      description: explanation('未解析会话的有界重试参数。', 'Bounded retry parameters for unresolved Conversations.'),
      properties: {
        uid: explanation('拥有这些会话 membership 的用户 ID。', 'User ID that owns the memberships for these Conversations.'),
        channels: explanation('上一轮 unresolved 中要重试的 Channel Key；至少 1 个，最多 200 个，重复 Key 会合并。', 'Channel keys from a previous unresolved result to retry; between 1 and 200 are accepted and duplicates are merged.'),
      },
    },
    ConversationMutationRequest: {
      description: explanation('会话清未读、隐藏或激活操作共用的 Key。', 'Shared key for Conversation clear-unread, hide, or activate operations.'),
      properties: { uid, channel_id: channelID, channel_type: channelType },
    },
    ConversationSetUnreadRequest: {
      description: explanation('设置会话最大未读数的参数。', 'Parameters for setting a Conversation maximum unread count.'),
      properties: {
        uid,
        channel_id: channelID,
        channel_type: channelType,
        unread: explanation('希望最多保留的未读消息数量；必须大于等于 0，服务端只会单调推进 read_seq。', 'Maximum unread messages to retain; it must be at least 0 and the server only advances read_seq monotonically.'),
      },
    },
    ConversationSyncLegacyRequest: {
      description: explanation('v2.2 兼容会话同步参数。', 'v2.2-compatible legacy Conversation synchronization parameters.'),
      properties: {
        uid: explanation('要同步旧式会话的非空白用户 ID。', 'Non-blank user ID whose legacy Conversations are synchronized.'),
        version: explanation('客户端已知的会话版本；0 表示不按版本过滤。', 'Conversation version known by the client; 0 disables version filtering.'),
        last_msg_seqs: explanation('以 | 分隔的 channel_id:channel_type:message_seq 游标；格式错误的条目会被跳过。', 'Pipe-delimited channel_id:channel_type:message_seq cursors; malformed entries are skipped.'),
        msg_count: explanation('每个会话读取的最近消息数；小于等于 0 返回空数组，正数最大按 10000 处理。', 'Recent messages read per Conversation; values at or below 0 return an empty array and positive values are capped at 10000.'),
        only_unread: explanation('未读过滤兼容标志；仅 1 表示只读取未读会话。', 'Unread-filter compatibility flag; exactly 1 selects unread Conversations only.'),
        exclude_channel_types: explanation('要排除的 Channel 类型数组；可省略、为空或为 null；显式 Channel 游标优先于该过滤。', 'Channel types to exclude; it may be omitted, empty, or null, and an explicit Channel cursor overrides this filter.'),
        page: explanation('旧式页码；小于等于 0 时禁用旧式分页切片。', 'Legacy page number; values at or below 0 disable legacy page slicing.'),
        page_size: explanation('旧式每页数量；page>0 时，小于等于 0 使用 100，大于 500 截断为 500。', 'Legacy page size; when page>0, values at or below 0 use 100 and values above 500 are capped at 500.'),
      },
    },
  } satisfies Record<string, SchemaExplanations>,
} as const;

/** Adds the reviewed explanation text to one already-cloned OpenAPI document. */
export function applyProductHTTPParameterExplanations(
  document: unknown,
  locale: ProductHTTPExplanationLocale,
) {
  if (!document || typeof document !== 'object') return;
  const components = (document as OpenAPIRecord).components;
  if (!components) return;

  for (const [name, text] of Object.entries(productHTTPParameterExplanations.parameters)) {
    const parameter = components.parameters?.[name];
    if (parameter) parameter.description = text[locale];
  }
  for (const [name, text] of Object.entries(productHTTPParameterExplanations.requestBodies)) {
    const requestBody = components.requestBodies?.[name];
    if (requestBody) requestBody.description = text[locale];
  }
  for (const [name, rawText] of Object.entries(productHTTPParameterExplanations.schemas)) {
    const text = rawText as SchemaExplanations;
    const schema = components.schemas?.[name];
    if (!schema) continue;
    schema.description = text.description[locale];
    for (const [propertyName, propertyText] of Object.entries(text.properties ?? {})) {
      const property = schema.properties?.[propertyName];
      if (property) property.description = propertyText[locale];
    }
  }
}
