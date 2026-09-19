
## 内嵌运行

生产构建已内嵌到 `wukongim` 二进制的业务 API 监听器。启动服务后直接访问：

```text
http://127.0.0.1:5001/demo/
```

内嵌 Demo 默认使用页面同源的 WuKongIM HTTP API，并通过 `/route` 获取客户端 WebSocket 地址。开发服务器默认连接 `http://127.0.0.1:5001`；两种模式都可使用 `?apiurl=http://host:port` 覆盖 API 基地址。

Demo 会按照浏览器的语言偏好顺序选择中文（`zh`，包括 `zh-CN`、`zh-TW` 等）或英文（`en`）；没有匹配语言时默认使用英文。语言在页面加载时确定，修改浏览器语言后刷新页面即可生效。页面标题、登录与聊天界面、时间、消息摘要和 Demo 自带提示均使用所选语言，用户消息及服务端返回的错误内容保持原文。

分享固定语言的 Demo 时，可使用 `http://127.0.0.1:5001/demo/?lang=en` 或 `?lang=zh`。这两个值优先于浏览器语言，其他值继续使用浏览器偏好；`lang` 放在路由的 `#` 之前，也可与 `apiurl` 参数组合。

会话和消息中的用户头像由 UID 稳定生成；不同 UID 显示不同的本地 SVG 头像，不依赖外部头像服务。

## 已有凭据与重新同步

默认填写已有 UID 和 Web Token 登录，不修改服务端凭据。只有专用测试账号需要创建或更新 Token 时，才显式勾选“创建或更新演示凭据”。

凭据只保存在当前标签页的 `sessionStorage`，不进入 URL。刷新或点击“重新同步”会保留凭据、重建 SDK 内存状态并从服务端同步；输入框还有草稿时，应先发送或保存。退出清除本 Demo 的凭据并重建页面，避免下一个账号继承 SDK 缓存。

## 本地开发

```bash
corepack yarn install --frozen-lockfile
corepack yarn test
corepack yarn dev
```

生产构建输出到 `internal/access/api/demoui/dist`，该完整目录需要随源码提交：

```bash
corepack yarn build
```

## 消息编辑

Demo 固定使用 npm `wukongimjssdk@1.4.0-beta.1`，服务端需支持消息编辑接口
与 `X-WK-Content-Epoch`（配套服务端 `v3.0.0-beta.18` 或更新版本）。
单聊与群聊中，本人发送成功的普通文本旁显示“编辑”；CMD、SyncOnce、
非持久化、流消息、已撤回消息及其他内容类型不显示编辑入口。

点击编辑会回填原文并保留原发送草稿。Enter 发送或保存，Shift+Enter 换行。服务端确认保存后，原气泡显示新正文与
“已编辑”，不会新增消息或增加未读。修改最后一条消息会更新最近会话摘要；
修改较早消息不会替换较新摘要。当前频道通过 SDK 合并更新提示并增量同步，
重新连接、返回前台及重新打开频道会补拉，不逐个查询所有非当前频道。
历史读取保留流消息字段，会话目录仍按每页 200 条完整分页。

失败保留草稿；版本冲突会读取最新正文，用户检查后再次保存。
若网络中断导致保存结果不明，输入框暂时只读，请先点击“重试”确认同一份修改，
再改写或离开编辑。有未保存改动时取消或切换频道会提示确认；结束编辑恢复原发送草稿。
草稿仅在当前页面内存中保存，刷新或关闭页面不会持久保存。

Demo 沿用直接访问 Product HTTP API 的演示方式。“仅本人编辑”是界面限制；
实际业务必须通过自己的后端验证身份、作者、频道权限和编辑时间窗口。

### 双浏览器验证

先从仓库根目录构建包含最新 Demo 产物的服务端，再显式运行集成测试：

```bash
# 仓库根目录
GOWORK=off go build -o /tmp/wukongim-demo-edit-server ./cmd/wukongim
cd demo/chatdemo
WK_DEMO_SERVER_BIN=/tmp/wukongim-demo-edit-server \
WK_DEMO_PLAYWRIGHT=/absolute/path/to/playwright \
corepack yarn test:integration
```

需要可运行的 Chromium（由指定的 Playwright 安装）。测试创建临时单节点集群、
测试账号和两个浏览器页面，验证单聊/群聊、冲突、丢失回执重试、草稿、摘要及刷新恢复，
结束后关闭自己启动的进程，并输出截图和日志目录。
