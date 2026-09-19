export type Locale = 'zh' | 'en'

type BrowserLanguages = { languages?: readonly string[], language?: string }

// A supported URL language overrides browser preferences; English is the fallback.
export function detectLocale(browser?: BrowserLanguages, languageOverride?: string | null): Locale {
  if (languageOverride === 'en' || languageOverride === 'zh') return languageOverride
  const languages = [...(browser?.languages ?? []), browser?.language ?? '']
  for (const language of languages) {
    const base = language.trim().toLowerCase().split(/[-_]/)[0]
    if (base === 'zh' || base === 'en') return base
  }
  return 'en'
}

export const locale = detectLocale(
  typeof navigator === 'undefined' ? undefined : navigator,
  typeof window === 'undefined' ? undefined : new URLSearchParams(window.location.search).get('lang'),
)

const en = {
  edit: "Edit",
  edited: "Edited",
  editingMessage: "Editing message",
  saveEdit: "Save changes",
  cancelEdit: "Cancel",
  savingEdit: "Saving…",
  retryEdit: "Retry",
  reloadEdit: "Reload",
  discardEdit: "Discard your unsaved changes? Choose Cancel to continue editing.",
  editConflict: "This message changed on another device. The latest message is shown above; your draft is kept. Review it before saving again.",
  editFailed: "Could not save. Your draft is kept. Please try again.",
  editNotFound: "This message is no longer available. Your draft is kept.",
  editOutcomeUnknown: "The result is not confirmed. Retry this same draft before changing it or leaving editing.",
  editReloadFailed: "Could not load the latest message. Reload it before saving again; your draft is kept.",
  messageSyncFailed: "Could not synchronize messages. Reopen the chat or synchronize again.",
  staleMessage: "Message needs to be reloaded",

  appTitle: 'WuKongIM Demo',
  sdkVersion: 'WuKongIM Demo, SDK version: [v{version}]',
  logo: 'WuKongIM logo',
  apiAddress: 'API base URL',
  apiAddressPlaceholder: 'Enter the API base URL',
  username: 'Account',
  usernamePlaceholder: 'Enter an existing user UID',
  password: 'Token',
  passwordPlaceholder: 'Enter the existing Web token',
  login: 'Log in',
  existingTokenHint: 'Existing-token login does not change server credentials. This tab keeps credentials for reloads; messages are synchronized again.',
  createDemoCredentials: 'Create or update demo credentials (test accounts only)',
  loginFailed: 'Check the API URL, UID and token, and allow session storage. Demo registration may also be refused by the server.',
  authenticationFailed: 'Authentication failed: check the existing Web token',
  resync: 'Synchronize again',
  loadEarlier: 'Load earlier messages',
  finishDraftBeforeSync: 'Send or save the current draft before synchronizing again.',
  logout: 'Log out',
  chatList: 'Chats',
  notConnected: '{uid} (Not connected)',
  connected: '{uid} (Connected)',
  connectedNode: '{uid} (Connected - Node: {node})',
  disconnected: '{uid} (Disconnected)',
  starProject: 'Give us a star on GitHub',
  chooseChat: 'Start a chat',
  groupChatTitle: 'Group: {id}',
  directChatTitle: 'Direct chat: {id}',
  directChat: 'Direct chat',
  groupChat: 'Group chat',
  recipientPlaceholder: 'Enter the recipient account',
  groupPlaceholder: 'Enter the group ID',
  messagePlaceholder: 'Enter a message',
  sending: 'Sending',
  customMessage: 'Custom message',
  send: 'Send',
  confirm: 'OK',
  orderNumber: 'Order: {number}',
  orderQuantity: 'Quantity: {count}',
  sampleProduct: 'Cocoa lemon milk tea',
  orderDigest: '[Order message]',
  cardDigest: '[Card message]',
  streamDigest: '[Stream message]',
  messageDigest: '[Message]',
  unknownMessageDigest: '[Unknown message]',
  imageDigest: '[Image]',
  requestNotFound: 'Request URL not found (404)',
  unknownError: 'Unknown error',
  justNow: 'Just now',
  yesterday: 'Yesterday',
  dayBeforeYesterday: '2 days ago',
}

type MessageKey = keyof typeof en

const zh: Record<MessageKey, string> = {
  edit: "编辑",
  edited: "已编辑",
  editingMessage: "正在编辑消息",
  saveEdit: "保存修改",
  cancelEdit: "取消",
  savingEdit: "保存中…",
  retryEdit: "重试",
  reloadEdit: "重新读取",
  discardEdit: "放弃尚未保存的修改？选择“取消”可继续编辑。",
  editConflict: "消息已在另一台设备修改，上方已展示最新正文，当前草稿已保留。请检查后再次保存。",
  editFailed: "保存失败，编辑草稿已保留，请重试。",
  editNotFound: "这条消息已不存在，编辑草稿已保留。",
  editOutcomeUnknown: "尚未确认保存结果。请先重试同一份修改，再修改草稿或离开编辑。",
  editReloadFailed: "读取最新消息失败。请先重新读取，再保存修改；草稿已保留。",
  messageSyncFailed: "消息同步失败，请重新打开会话或重新同步。",
  staleMessage: "消息需重新加载",

  appTitle: '悟空IM演示程序',
  sdkVersion: '悟空IM演示程序，当前SDK版本：[v{version}]',
  logo: '悟空IM标志',
  apiAddress: 'API基地址',
  apiAddressPlaceholder: '请输入API基地址',
  username: '登录账号',
  usernamePlaceholder: '请输入已有用户 UID',
  password: '登录 Token',
  passwordPlaceholder: '请输入原有 Web Token',
  login: '登录',
  existingTokenHint: '默认使用已有 Token，不修改服务端凭据。本标签页保留凭据，刷新后重新同步消息。',
  createDemoCredentials: '创建或更新演示凭据（仅测试账号）',
  loginFailed: '请检查 API 地址、UID、Token 及浏览器会话存储权限；创建演示凭据时还需确认服务端允许该操作。',
  authenticationFailed: '凭据校验失败，请检查原有 Web Token',
  resync: '重新同步',
  loadEarlier: '加载更早消息',
  finishDraftBeforeSync: '请先发送或保存当前草稿，再重新同步。',
  logout: '退出',
  chatList: '聊天列表',
  notConnected: '{uid}(未连接)',
  connected: '{uid}(连接成功)',
  connectedNode: '{uid}(连接成功-节点:{node})',
  disconnected: '{uid}(断开)',
  starProject: '吴彦祖，点个Star呗',
  chooseChat: '与谁会话？',
  groupChatTitle: '群{id}',
  directChatTitle: '单聊{id}',
  directChat: '单聊',
  groupChat: '群聊',
  recipientPlaceholder: '请输入对方登录名',
  groupPlaceholder: '请输入群组ID',
  messagePlaceholder: '请输入消息',
  sending: '发送中',
  customMessage: '自定义消息',
  send: '发送',
  confirm: '确定',
  orderNumber: '订单号：{number}',
  orderQuantity: '共{count}件',
  sampleProduct: '可可柠檬鲜美奶茶',
  orderDigest: '[订单消息]',
  cardDigest: '[卡片消息]',
  streamDigest: '[流消息]',
  messageDigest: '[消息]',
  unknownMessageDigest: '[未知消息]',
  imageDigest: '[图片]',
  requestNotFound: '请求地址没有找到（404）',
  unknownError: '未知错误',
  justNow: '刚刚',
  yesterday: '昨天',
  dayBeforeYesterday: '前天',
}

export const messages: Record<Locale, Record<MessageKey, string>> = { en, zh }

// Interpolate only catalog placeholders; user values are never translated or evaluated.
export function translate(language: Locale, key: MessageKey, params: Record<string, string | number> = {}): string {
  return messages[language][key].replace(/\{(\w+)\}/g, (placeholder, name: string) =>
    Object.prototype.hasOwnProperty.call(params, name) ? String(params[name]) : placeholder)
}

export function t(key: MessageKey, params?: Record<string, string | number>): string {
  return translate(locale, key, params)
}

const dateFormatters = Object.fromEntries((['zh', 'en'] as const).map(language => [language, {
  time: new Intl.DateTimeFormat(language, { hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }),
  weekday: new Intl.DateTimeFormat(language, { weekday: 'long' }),
  date: new Intl.DateTimeFormat(language, { year: 'numeric', month: 'numeric', day: 'numeric' }),
}])) as Record<Locale, Record<'time' | 'weekday' | 'date', Intl.DateTimeFormat>>

// Compare calendar days in the user's time zone, including month/year and DST boundaries.
export function formatConversationTime(timestamp: number, language: Locale = locale, now = new Date()): string {
  const date = new Date(timestamp)
  const sameDay = (other: Date) => date.getFullYear() === other.getFullYear()
    && date.getMonth() === other.getMonth() && date.getDate() === other.getDate()
  const format = dateFormatters[language]
  const time = format.time.format(date)
  const elapsed = now.getTime() - timestamp
  if (sameDay(now)) {
    return elapsed >= 0 && elapsed < 60_000 ? translate(language, 'justNow') : time
  }
  for (const days of [1, 2]) {
    const previous = new Date(now)
    previous.setDate(previous.getDate() - days)
    if (sameDay(previous)) {
      return `${translate(language, days === 1 ? 'yesterday' : 'dayBeforeYesterday')} ${time}`
    }
  }
  const day = elapsed > 0 && elapsed <= 7 * 24 * 60 * 60 * 1000
    ? format.weekday.format(date) : format.date.format(date)
  return `${day} ${time}`
}
