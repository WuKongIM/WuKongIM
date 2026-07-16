
## 内嵌运行

生产构建已内嵌到 `wukongim` 二进制的业务 API 监听器。启动服务后直接访问：

```text
http://127.0.0.1:5001/demo/
```

内嵌 Demo 默认使用页面同源的 WuKongIM HTTP API，并通过 `/route` 获取客户端 WebSocket 地址。开发服务器默认连接 `http://127.0.0.1:5001`；两种模式都可使用 `?apiurl=http://host:port` 覆盖 API 基地址。

## 本地开发

```bash
corepack yarn install --frozen-lockfile
corepack yarn dev
```

生产构建输出到 `internal/access/api/demoui/dist`，该完整目录需要随源码提交：

```bash
corepack yarn build
```
