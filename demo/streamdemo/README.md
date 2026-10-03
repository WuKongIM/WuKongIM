# 流式聊天 Demo

独立演示入口：`http://127.0.0.1:5001/streamdemo/`。用户问题是普通消息，
助手回复是流式消息；两者均通过真实 WuKongIM 服务端。可以使用模拟内容，
也可以填写模型 URL 和 API Key，接入真实聊天模型。

点击“创建演示账号并连接”，然后在底部输入消息。Enter 发送，Shift+Enter 换行。
用户消息在右侧黄色气泡显示，助手的实时回复在左侧灰色气泡逐段显示。
顶部“演示设置”可修改模拟回复、段大小和间隔，并单独生成回复、断开接收端、
模拟失败。生成时底部提供“取消生成”，日志中分别记录 HTTP 回执和 SDK 事件。
演示账号、Token 和群频道每次新会话随机创建；凭据只存当前标签页的
`sessionStorage`，刷新后重连并恢复最近 40 条消息，Token 不写入日志。

连接时显示“连接中”，接收端与生成端均完成连接后才显示“在线”并启用发送。
旧标签页的测试凭据失效或网络失败时，可以“重试连接”；“重新创建演示会话”只清除
当前标签页的演示身份和消息视图，再点击“创建演示账号并连接”创建新账号。
它不会修改旧账号 Token、删除服务端历史或清除模型设置。无效的已保存会话会给出
提示并恢复创建入口；WebSocket 握手最多等待 15 秒，失败的 SDK 实例会关闭。

## 真实聊天模型

推荐用 demo 自带的本地代理，模型服务无需配置浏览器 CORS：

```bash
# 先启动 WuKongIM 服务端，再在 demo/streamdemo 目录执行
npm ci
npm run build
WK_DEMO_API_URL=http://127.0.0.1:5001 npm start
```

打开 `http://127.0.0.1:5175/streamdemo/`，创建演示账号并连接，然后在“演示设置”中：

1. 将“回复来源”切换为“真实模型”。
2. 填写“模型 URL”，可用兼容接口的基础地址（如 `https://provider.example/v1`），
   或完整 `/chat/completions` 地址。完整地址的查询参数会保留。
3. 填写 API Key；不需要鉴权的本地模型可留空。
4. “模型名称”可留空，首次请求会从 `/models` 获取一个聊天模型；也可直接填写
   服务商提供的模型 ID。如果服务不支持模型列表，必须手动填写名称。

随后在底部正常聊天。模型调用使用
[OpenAI 兼容 Chat Completions 的 SSE 流](https://developers.openai.com/cookbook/examples/how_to_stream_completions)，
携带最近的问答上下文；失败或取消的助手回复不作为已完成答案发送。
SSE 文本增量逐段写入 `/message/event`，接收端依然通过 EasySDK 实时事件显示，
取消会中止上游模型请求，HTTP 错误、SSE 错误和意外断流会保存部分内容及失败终态。
暂不接入 Responses API、工具调用或多模态输入。

API Key 只保留在当前页面内存中，并随每次模型请求交给代理；不进入消息、日志、
URL 或 `sessionStorage`。刷新后需要重新填写 Key，URL 和模型名称会保留。
模型代理绑定 `127.0.0.1`，仅接受本机同源 JSON 请求，不跟随上游重定向，
不读取或输出模型服务的错误正文。它属于 Demo 进程，不是 Product HTTP API 的代理。
代理最多四个并发请求，正文 128 KiB、模型列表 64 KiB、流 4 MiB，
请求最长 180 秒；前端最多保留 16,384 字符的单条回复及 24,000 字符的上下文。

`WK_DEMO_API_URL` 指定实际 WuKongIM API，默认 `http://127.0.0.1:5001`；
`WK_DEMO_PORT` 指定本地 Demo 端口，默认 `5175`。
`npm run dev` 也内置同源模型代理，页面使用 `?apiurl=` 选择 WuKongIM 服务端。
二进制内嵌入口仍可使用模型模式，但它直接请求模型服务，需要服务允许浏览器 CORS；
使用本地代理入口可避免这个限制。

## 运行与构建

启动当前仓库构建的服务端后访问内嵌入口。配置需启用 `wsmux` WebSocket
监听器，并使 `/route` 返回浏览器可达的 `ws_addr` 或 `wss_addr`。
API 默认同源，开发模式默认 `http://127.0.0.1:5001`，可通过
`?apiurl=http://host:port` 指定其他服务端。

前端使用 Node.js 22.12+（或 20.19+）及 npm：

```bash
cd demo/streamdemo
npm ci
npm run dev
npm run build
```

生产构建输出到 `internal/access/api/demoui/streamdist`。该目录完整随源码提交，
随后重新构建 `cmd/wukongim`，新页面即内嵌于二进制。原 `demo/chatdemo`
继续提供普通聊天和消息编辑，已移除流式监听、渲染和旧启动/结束调用。

## SDK 与协议

使用官方 [WuKongEasySDK-JS](https://github.com/WuKongIM/WuKongEasySDK-JS)
发布包 `easyjssdk@2.0.5`。该版本已有普通消息、流消息标记和 `CustomEvent`
能力，此 Demo 无需修改 SDK。

1. 两个独立的 EasySDK 实例使用 `singleton: false`，分别作为用户和模拟助手。
   用户实例 `send()` 发送问题；助手实例 `send()` 携带 `setting: {stream: true}`
   创建持久化基础消息，等待成功 SENDACK 后才追加事件。
2. 生成端顺序 POST `/message/event`：`stream.open` → 多个 `stream.delta` →
   `stream.finish`。取消或失败先追加 `stream.cancel` / `stream.error`，再用
   完整文本快照追加 `stream.finish`。
3. 服务端把已接受的公开事件推给在线成员的准确会话。EasySDK 的
   `WKIMEvent.CustomEvent` 收到 `{id, type, timestamp, data}`；`data` 包含
   `channel_id`、`channel_type`、`client_msg_no`、`event_key`、`payload`，
   文本增量还携带 UTF-8 字节 `text_offset`。
4. 用户实例仅在成功连接或重连时请求一次 `/channel/messagesync`，以
   `event_summary_mode: "full"` 恢复消息和终态。恢复期间暂存实时事件，按
   `text_offset` 跳过已在快照中的文本；按 Event ID 去重，并按消息序号排列问答。
   在线期间不轮询历史，不调用 `/message/eventsync`。

服务器投递受四个请求并发、128 个成员每页、512 个会话每个 RPC、256 KiB
RPC 帧和五秒投递期限约束。在线事件为尽力投递，无 RECVACK 或离线增量队列；
离线及丢失增量从历史快照恢复。非公开事件不广播。生产者须按消息顺序串行写入，
重试复用原 `event_id`；完成后的迟到事件不会重新打开投影。写入结果未确认时，
界面保留错误提示，不自动用新事件编号重试。

此演示沿用直接访问 Product HTTP API 的开发方式，需可写演示账号和频道。
实际应用应由业务后端验证身份、作者和频道权限，再写入流事件。

## 可重复验证

先从仓库根目录构建嵌入最新前端的二进制，再显式运行浏览器集成测试：

```bash
GOWORK=off go build -o /tmp/wukongim-stream-demo ./cmd/wukongim
WK_DEMO_SERVER_BIN=/tmp/wukongim-stream-demo \
WK_DEMO_PLAYWRIGHT=/absolute/path/to/playwright \
node demo/streamdemo/test/stream.integration.cjs

# 凭据失效、生成端单独失败、存储损坏和新会话恢复
WK_DEMO_SERVER_BIN=/tmp/wukongim-stream-demo \
WK_DEMO_PLAYWRIGHT=/absolute/path/to/playwright \
node demo/streamdemo/test/connection.integration.cjs
```

Playwright 需已安装 Chromium。测试启动临时单节点集群，验证实际 SDK 问答、
完成、取消、失败、增量重试、离线/刷新恢复、生成中重连、移动端布局以及在线无
历史轮询。结束后关闭自建进程，并输出包含 `report.json`、截图和服务端日志的目录。

模型协议验收会启动本地 SSE 模型夹具、真实 WuKongIM 集群和 Demo 代理，
无需真实 API Key 或付费请求：

```bash
WK_DEMO_SERVER_BIN=/tmp/wukongim-stream-demo \
WK_DEMO_PLAYWRIGHT=/absolute/path/to/playwright \
node demo/streamdemo/test/model.integration.cjs

# 同一流程验证 Vite 开发模式
WK_DEMO_MODEL_DEV=1 \
WK_DEMO_SERVER_BIN=/tmp/wukongim-stream-demo \
WK_DEMO_PLAYWRIGHT=/absolute/path/to/playwright \
node demo/streamdemo/test/model.integration.cjs
```

报告覆盖 URL 归一化、自动模型选择、多轮上下文、UTF-8/多行 SSE、取消上游、
HTTP/SSE 错误、断流、Key 隔离、重连恢复和禁止跨站代理访问。
实际服务的模型权限、额度及返回协议仍需使用自己的配置验证。

服务端跨节点协议验证使用单节点和三节点集群，固定 256 个 hash slots：

```bash
WK_E2E_STREAM_REPORT=/tmp/wukongim-stream-report.json \
GOWORK=off go test -tags=e2e ./test/e2e/message/stream_online -count=1 -timeout=3m
```

该报告覆盖单聊/群聊、跨节点 EVENT、私有事件隔离、终态和历史恢复、
完成后重试及缺少基础消息时拒绝写入。
