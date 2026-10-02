# 智能门店 MQTT Demo

模拟冷柜和店员工作台使用 **MQTT.js 5.16.0**，直接连接 WuKongIM 的 MQTT 5
WebSocket 监听器。页面里的设备是模拟的，身份认证、群消息、个人指令、执行回执、
离线积压和 IM 互通均经过真实集群。

| 步骤 | 体验 |
| --- | --- |
| 开始演示 | 准备独立设备、店员、同事账号和一个门店群；设备与工作台连接 MQTT |
| 触发温度异常 | 冷柜升至 −6°C，向门店群发送告警 |
| 开启强制制冷 | 店员向设备个人 Topic 发送指令；分别显示 PUBACK 与设备执行回执 |
| 温度恢复 | 设备逐渐降至 −18°C，向门店群发送恢复消息，关闭告警 |
| 工作台离线 | 先建立订阅，再断开工作台并触发异常；重连恢复原会话与群积压 |
| 接入 IM 同事 | 使用 EasySDK 2.0.5 接收同一门店群的 MQTT 告警，并发送巡检备注 |

顶部“返回首页”保留实际首页地址。手机通过“冷柜设备 / 店员工作台”标签切换。
接入说明、实际 Topic、连接参数、示例代码与有界事件日志可展开查看。

## 运行

仓库根目录执行 `node demo/start.mjs`，首页会提供 MQTT 入口。所有服务绑定本机，
无需硬件、模型账号或付费请求。

已有 WuKongIM 集群时，先启用 MQTT TCP 与一个独立 MQTT WebSocket listener：

```toml
[mqtt]
enable = true
listen_addr = "127.0.0.1:1883"

[gateway]
token_auth_on = true
listeners = [
  {name = "ws", network = "websocket", address = "127.0.0.1:5200", transport = "gnet", protocol = "wsmux"},
  {name = "mqtt-ws", network = "websocket", address = "127.0.0.1:1884", path = "/mqtt", transport = "gnet", protocol = "mqtt"}
]
```

现有 listener 必须保留；两个协议使用不同端口。Product HTTP `/route` 应发布浏览器
可达的 wsmux `ws_addr` 或 `wss_addr`，供后置 IM 同事步骤使用。

```bash
cd demo/mqttdemo
npm ci
npm run build
WK_DEMO_API_URL=http://127.0.0.1:5001 \
WK_DEMO_MQTT_WS_URL=ws://127.0.0.1:1884/mqtt npm start
```

打开 **http://127.0.0.1:5179/mqttdemo/**。`WK_DEMO_PORT` 修改演示后端端口。
Go 二进制内嵌 `/mqttdemo/` 的只读界面；其“演示设置”填写上述 Node 服务 URL 即可。
此 Node 进程只登记随机凭据和群成员，不建立 MQTT/SDK 连接，也不代发业务消息。

| 环境变量 | 默认值 |
| --- | --- |
| `WK_DEMO_PORT` | `5179`，HTTP 演示服务仅监听 `127.0.0.1` |
| `WK_DEMO_API_URL` | `http://127.0.0.1:5001`，用于可信账号与群准备 |
| `WK_DEMO_MQTT_WS_URL` | `ws://127.0.0.1:1884/mqtt`，浏览器直连的产品 MQTT URL |

`GET /mqttdemo/api/health` 返回 `{ "ready": true }`。
`POST /mqttdemo/api/session` 接受空 JSON 对象，返回 `id`、`device`、`staff`、
`colleague`、`groupId`、`mqttWsUrl`、`wsUrl`；每个身份含 `uid`、`token`、`clientId`。
HTTP 正文限制 4 KiB、最多 8 个并发请求、每小时最多 8 个新演示；仅允许本机 Host
和演示服务自身或配置 Product API 的 Origin。

## 消息与恢复

- 所有发布使用 QoS 1、`retain=false`，并保留原 `wk.client_msg_no` 用于不确定结果重试。
  PUBACK 仅表示服务端提交确认，工作台收到设备私发的业务回执后才显示“设备已执行”。
- 消息采用 `{type:1,content:"可读文本",mqtt_demo:{kind,alertId,commandId,...}}`。
  普通 IM 可读取 `content`；设备信任 `wk.from_uid` 认证发送方，并向该 UID 回执，
  不信任正文中声称的回复地址。
- 固定 ClientID、`clean=false`、3600 秒 Session Expiry；监听在 CONNECT 之前安装。
  检查 Session Present；只有新会话建立订阅。工作台离线恢复使用 MQTT 会话积压，
  不调用 `/channel/messagesync`。相同 MessageID 按十进制字符串去重。
- `commandId` 去重记录在模拟执行前写入当前标签页 `sessionStorage`。未确认的执行
  回执保留原目标、消息体和幂等键；设备重连时补发一次，也可点击“补发执行回执”。
  重试不会再次执行制冷。告警/恢复发布结果不明确时也可重试原设备消息。
- 身份、状态和有界去重记录只存当前标签页；日志不含 Token。刷新可恢复当前演示，
  关闭标签页后开始新演示。每端最多保留 256 个 MessageID、64 个执行指令编号、
  60 条可见消息、120 条事件日志和一条待确认执行回执。

首版不含 Will、真实硬件或外部设备注册。模拟设备页面关闭时停止模拟运行；真实设备
需要持久保存业务去重和未确认回执，并实现有界退避及凭据更新。

## 开发与可重复验收

需要 Go 1.25+、Node.js 22.12+（或 20.19+）及 npm。前端修改后运行
`npm run build`，输出 `internal/access/api/demoui/mqttdist/`，需完整随源码提交。
Node 后端为纯 `.mjs`，不需要 TypeScript 运行文件编译。

浏览器验收固定 Playwright **1.62.1**，可在仓库根目录安装到忽略的 `tmp/`：

```bash
npm install --prefix tmp/mqtt-demo-playwright --save-exact playwright@1.62.1
tmp/mqtt-demo-playwright/node_modules/.bin/playwright install chromium
GOWORK=off go build -o tmp/wukongim-mqtt-demo ./cmd/wukongim
WK_DEMO_SERVER_BIN="$PWD/tmp/wukongim-mqtt-demo" \
WK_DEMO_PLAYWRIGHT="$PWD/tmp/mqtt-demo-playwright/node_modules/playwright" \
node demo/mqttdemo/test/mqtt.integration.mjs
```

先在 `demo/mqttdemo` 执行 `npm ci && npm run build`，再构建上述 Go 二进制。
测试启动真实 **256 hash slots 单节点集群**、本机 provisioning 服务和 Chromium，
覆盖告警处置、业务关联 ID、重复指令、离线积压、MQTT ↔ SDK 双向互通、刷新恢复、
手机布局、恶意/不完整业务消息、跨站与过大请求、后端不可用和执行回执网络中断重试。
网络故障只截断真实 WebSocket 帧，不模拟 broker 或接收成功。

默认证据目录为忽略的 `tmp/mqtt-demo-acceptance/run-*`；可用
`WK_MQTT_DEMO_REPORT_DIR` 指定其他目录。产物包括截图、`report.json`、消息观察
`messages.json`、实际配置与日志。报告记录基准 Git revision、未提交状态、候选二进制
SHA256 和关键源码 SHA256，便于确认实际验收对象；失败保留截图与脱敏客户端状态。
