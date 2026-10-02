# 小悟新品发布会 · 直播弹幕 Demo

开发者用两位独立观众体验真实消息接入：进入发布会、互发弹幕和点赞，再由主播
发布置顶公告、禁言其中一位观众、解除禁言。舞台是内置演示画面，消息交互经过
WuKongIM；不需要摄像头、推流服务或模型账号。

## 一分钟体验

1. 仓库根目录执行 `node demo/start.mjs`，在首页打开“直播新品发布会”。
2. 点击“进入直播间”，连接和当前状态恢复完成后发送弹幕。
3. 点击“打开第二位观众”，新标签页使用新的 UID 加入同一房间。两页互发消息、点赞。
4. 在主播管理中发布公告、选择第二位观众禁言；该观众仍能接收，刷新也不能绕过禁言。
5. 解除禁言恢复互动。断开观众连接、更新公告后重连，只恢复最新状态，不补播旧弹幕。

桌面显示舞台、现场聊天与主播管理；手机通过“观众 / 主播”标签切换。第二位观众
页面没有管理权限。可以关闭弹幕动画，仍能阅读聊天；顶部“返回首页”保留实际首页地址。
点赞只显示此刻的互动，不推算全场累计数或在线人数。

## 独立运行

已有 WuKongIM 集群须开启 wsmux WebSocket，并由 Product HTTP `/route` 发布浏览器可达的
`ws_addr` 或 `wss_addr`。使用可信的本机测试集群：

```bash
cd demo/livedemo
npm ci
npm run build
WK_DEMO_API_URL=http://127.0.0.1:5001 npm start
```

打开 **http://127.0.0.1:5180/livedemo/**。Go 二进制内嵌 `/livedemo/` 的只读界面；
其“演示设置”可填写上述 Node 服务地址。该后端负责身份、入房和管理动作；
观众弹幕和点赞从浏览器直接通过 SDK 发送。

| 环境变量 | 默认值 |
| --- | --- |
| `WK_DEMO_PORT` | `5180`，演示服务仅监听 `127.0.0.1` |
| `WK_DEMO_API_URL` | `http://127.0.0.1:5001`，可信 Product HTTP 根地址 |

Product 管理接口只能暴露给可信业务后端。此演示采用本机 Host/Origin 限制和随机角色凭据，
没有生产账号系统；跨设备或公网接入需要应用自己的认证和部署方案。

## 接入与恢复合同

- 固定 `easyjssdk@2.0.5`，普通群 `channel_type=2`。业务后端准备成员，SDK SUB 不用作入房。
- 弹幕/点赞载荷为 `{type:1,content,live_demo:{kind,roomId,eventId}}`，发送选项
  `{header:{noPersist:true,syncOnce:false},clientMsgNo:eventId}`。它们不存普通历史、
  不补离线消息；本人成功 ACK 后展示与对方真实 Message 接收分别记录。
- 发送结果未知时保留原事件与消息编号，可手动重试；不自动重发，不承诺瞬时消息 exactly-once。
  接收端有界去重并校验频道、房间、实际发送人和载荷格式，正文以纯文本渲染。
- 每标签页保存自己的恢复凭据，刷新恢复同一 UID。第二观众只携带邀请信息，
  不复制主播或原观众凭据。显式离开、结束、过期和后端重启使旧恢复失效。
- 后端以真实主播 UID 广播完整 `room_state`；加入/重连读取版本化当前快照，
  较旧通知或延迟快照不能回滚状态。观众伪造正文中的主播身份不产生权限。
- 房间禁言调用群 blacklist add/remove，保留成员接收。产品写入结果不确定时，
  保留上一确认值与“待确认”状态；重试同一动作确认后才允许相反动作。
  该目标观众需先确认原操作再离开，避免待确认状态引用已删除的成员；主播仍可结束整场演示。
- 状态保存与通知发送分开确认。通知失败显示待确认并允许手动重试，当前状态仍可查询。
  新操作可发布更新的完整快照，旧通知不会覆盖较新版本。

业务接口位于 `/livedemo/api`：`health`、`rooms`、`join`、`resume`、`state`、`control`、
`retry`、`heartbeat`、`leave`、`close`。管理请求使用独立 owner capability，
观众使用自己的 viewer capability。`retry` 的 operation 阶段保持原控制 requestId；
notification 阶段使用返回的 `notification.requestId`，每个公开快照版本拥有独立通知身份。
完整实施合同见[设计文档](../../docs/superpowers/specs/2026-10-02-live-barrage-demo-design.md)。

每进程最多 8 房间、每房间 16 观众、8 个排队控制任务、64 个操作结果，闲置一小时过期；
清理失败保留有界记录。HTTP 正文限 4 KiB、上游并发限 8。客户端聊天、去重、日志分别限
100 / 512 / 100 项，最多 4 条轨道、12 条可见动画、30 条待播；后台页面丢弃积压动画。
这些上限服务于可重复的小型接入演示，不构成大群性能资格验证。

## 开发与真实进程验收

需要 Go 1.25+、Node.js 22.12+（或 20.19+）和 npm。前端修改后运行 `npm run build`，
输出 `internal/access/api/demoui/livedist/`，需随源码提交；然后重新构建 Go 二进制。
后端为纯 `.mjs`，无需编译 TypeScript 运行文件。

浏览器验收固定 Playwright **1.62.1**，可在仓库根目录安装到忽略的 `tmp/`：

```bash
npm install --prefix tmp/live-demo-playwright --save-exact playwright@1.62.1
tmp/live-demo-playwright/node_modules/.bin/playwright install chromium
GOWORK=off go build -o tmp/wukongim-live-demo ./cmd/wukongim
WK_DEMO_SERVER_BIN="$PWD/tmp/wukongim-live-demo" \
WK_DEMO_PLAYWRIGHT="$PWD/tmp/live-demo-playwright/node_modules/playwright" \
node demo/livedemo/test/live.integration.mjs
```

测试使用真实 **256 hash slots 单节点集群**、Demo 后端与 Chromium。失败清单先于实现
保存在 [failure-inventory.md](test/failure-inventory.md)。默认产物位于忽略的
`tmp/live-demo-acceptance/run-*`，可用 `WK_LIVE_DEMO_REPORT_DIR` 指定目录。
报告保存源码/指令与候选二进制 SHA256、实际接收事件关联、截图、日志和 owned 进程清理结果；
不记录身份 Token 或 CONNECT 帧。运行前请先在 `demo/livedemo` 执行 `npm ci && npm run build`。
