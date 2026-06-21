# wkdb 只读排查工具使用手册

## 适用范围

`wkdb` 是一个节点本地只读排查 CLI，用于离线查看 WuKongIM 某个节点的数据目录里的 metadata 和 message 存储。

它只做观察：

- 以只读模式打开本地 Pebble 存储。
- 不连接其他集群节点。
- 不提供全局集群视图。
- 不修复、不 compact、不重写 key，不执行 insert/update/delete。

适合在事故排查、迁移核对、存储布局调试时，用来查看某一个节点文件里的真实本地状态。

## 安全前提

优先使用以下数据源：

- 已停止节点的数据目录。
- 文件系统快照。
- 从节点目录拷贝出来的一份副本。

除非已经明确验证过当前环境支持在线并发只读打开，否则不要直接对线上运行中的生产节点目录执行排查命令。

不要把一个节点的本地扫描结果当成全局集群事实。涉及集群决策时，需要结合其他节点结果或 manager/cluster API。

## 构建与运行

在仓库根目录直接运行：

```bash
GOWORK=off go run ./cmd/wkdb --data-dir ./data/node-1 query "show tables"
```

构建二进制：

```bash
go build -o ./bin/wkdb ./cmd/wkdb
./bin/wkdb --data-dir ./data/node-1 query "show tables"
```

## 路径解析

最常用方式是传 `--data-dir`：

```bash
wkdb --data-dir ./data/node-1 query "show tables"
```

它会派生：

- metadata 存储：`./data/node-1/data`
- message 存储：`./data/node-1/channellog`

也可以显式传存储路径：

```bash
wkdb --meta-path ./data/node-1/data query "describe meta.user"
wkdb --message-path ./data/node-1/channellog query "select * from message.channels limit 20"
```

或者从 `wukongim.conf` 读取：

```bash
wkdb --config ./wukongim.conf query "show tables"
```

相关配置键：

- `WK_NODE_DATA_DIR`
- `WK_STORAGE_DB_PATH`
- `WK_STORAGE_CHANNEL_LOG_PATH`
- `WK_CLUSTER_HASH_SLOT_COUNT`

优先级：

1. CLI 显式参数。
2. `WK_` 环境变量。
3. 配置文件。

## 命令形式

执行单条查询：

```bash
wkdb --data-dir ./data/node-1 --hash-slot-count 256 query "select * from meta.user where uid='u1'"
```

进入简单 REPL：

```bash
wkdb --data-dir ./data/node-1 --hash-slot-count 256 repl
```

退出 REPL：

```text
exit
```

或：

```text
quit
```

## 查询语法

支持的语句示例：

```sql
show tables
describe meta.user
select * from meta.user where uid='u1' limit 20
select uid, token from meta.user where hash_slot=12 limit 20
select * from message.channels limit 20
select * from message.message where channel_key='g1:2' limit 50
```

支持范围：

- 表名必须带 domain：`meta.<table>` 或 `message.<table>`。
- 投影支持 `*` 或明确列名。
- `where` 支持等值条件，多个条件用 `and` 连接。
- `limit` 限制返回行数。
- `cursor '<next_cursor>'` 用于翻页。

暂不支持：

- `insert`、`update`、`delete`
- `join`、`group by`、`order by`
- `offset`
- 表达式和范围条件

## hash-slot 语义

metadata 表按 hash-slot 分区。普通点查不建议手动传 `hash_slot`，而是传表的分区键，让 `wkdb` 自动计算 hash-slot：

```bash
wkdb --data-dir ./data/node-1 --hash-slot-count 256 query "select * from meta.user where uid='u1'"
```

常见分区键：

- `meta.user`：`uid`
- `meta.device`：`uid`
- `meta.channel`：`channel_id`
- `meta.channel_runtime_meta`：`channel_id`
- `meta.subscriber`：`channel_id`
- `meta.conversation`：`uid`（普通会话与 CMD 同步共用，通过 `kind` 区分）
- `meta.plugin_binding`：`uid`
- `meta.channel_migration`：`channel_id`

如果 metadata 查询没有分区键，`wkdb` 会在当前节点文件内做有界本地扫描：

```bash
wkdb --data-dir ./data/node-1 --hash-slot-count 256 query "select * from meta.user limit 100"
```

本地有界扫描需要 `--hash-slot-count` 或 `WK_CLUSTER_HASH_SLOT_COUNT`。

如果确实要看某个具体分区，也可以显式传 `hash_slot`：

```bash
wkdb --data-dir ./data/node-1 query "select * from meta.user where hash_slot=12 limit 20"
```

## limit 与 cursor

`limit` 是本次查询总返回行数，不是每个 hash-slot 各返回这么多。

例如下面最多返回 100 行 user：

```bash
wkdb --data-dir ./data/node-1 --hash-slot-count 256 query "select * from meta.user limit 100"
```

如果还有后续数据，输出 stats 会包含：

- `has_more=true`
- `next_cursor=<cursor>`

继续翻页：

```bash
wkdb --data-dir ./data/node-1 --hash-slot-count 256 query "select * from meta.user limit 100 cursor '<next_cursor>'"
```

cursor 与查询形状绑定。换表、换过滤条件、换投影列、换 limit、换 scan mode、换 message channel 后复用 cursor，会返回 cursor mismatch。

## 输出格式

默认是 table：

```bash
wkdb --data-dir ./data/node-1 query "show tables"
```

JSON：

```bash
wkdb --data-dir ./data/node-1 --format json query "select * from message.channels limit 5"
```

JSONL：

```bash
wkdb --data-dir ./data/node-1 --format jsonl query "select * from message.channels limit 5"
```

`jsonl` 每行输出一条 row，最后追加一条 stats 记录：

```json
{"channel_key":"g1:2","channel_id":"g1","channel_type":2}
{"type":"stats","stats":{"scan_mode":"message-catalog","has_more":false,"next_cursor":""}}
```

结构化输出里，二进制列会 base64 编码，例如 message `payload`。

## 常用查询

列出可排查表：

```bash
wkdb --data-dir ./data/node-1 query "show tables"
```

查看 user 表字段：

```bash
wkdb --data-dir ./data/node-1 query "describe meta.user"
```

按 UID 查用户：

```bash
wkdb --data-dir ./data/node-1 --hash-slot-count 256 query "select * from meta.user where uid='u1'"
```

抽样查看当前节点本地 user：

```bash
wkdb --data-dir ./data/node-1 --hash-slot-count 256 query "select uid, token from meta.user limit 100"
```

查看 channel 元数据：

```bash
wkdb --data-dir ./data/node-1 --hash-slot-count 256 query "select * from meta.channel where channel_id='g1' limit 20"
```

列出当前节点 message catalog 里的 channel：

```bash
wkdb --data-dir ./data/node-1 query "select * from message.channels limit 20"
```

读取某个 channel 的消息：

```bash
wkdb --data-dir ./data/node-1 query "select * from message.message where channel_key='g1:2' limit 50"
```

查某个消息序号：

```bash
wkdb --data-dir ./data/node-1 query "select message_seq, message_id, from_uid, payload_size from message.message where channel_key='g1:2' and message_seq=100 limit 10"
```

查看 hash-slot 迁移状态：

```bash
wkdb --data-dir ./data/node-1 query "select * from meta.hashslot_migration where hash_slot=17 limit 10"
```

## Smoke Checklist

用真实拷贝目录验收 `wkdb` 时，建议按这个顺序跑：

1. 验证表发现：

   ```bash
   wkdb --data-dir ./data/node-1 query "show tables"
   ```

2. 验证 metadata schema：

   ```bash
   wkdb --data-dir ./data/node-1 query "describe meta.user"
   ```

3. 验证 metadata 有界扫描：

   ```bash
   wkdb --data-dir ./data/node-1 --hash-slot-count 256 query "select * from meta.user limit 5"
   ```

4. 验证 message catalog 扫描：

   ```bash
   wkdb --data-dir ./data/node-1 query "select * from message.channels limit 5"
   ```

5. 如果上一步返回了 `channel_key`，验证 message 扫描：

   ```bash
   wkdb --data-dir ./data/node-1 query "select * from message.message where channel_key='<channel_key>' limit 5"
   ```

6. 如果 `has_more=true`，用返回的 `next_cursor` 验证 cursor 翻页。

## 常见错误

缺少存储路径：

```text
storage path required
```

传 `--data-dir`、`--meta-path`、`--message-path` 或 `--config`。

缺少 hash-slot 数：

```text
inspect: hash slot required
```

传 `--hash-slot-count`，或使用包含 `WK_CLUSTER_HASH_SLOT_COUNT` 的配置文件。

cursor 不匹配：

```text
inspect: cursor mismatch
```

cursor 只能和生成它的同一条查询形状一起使用。

未知表或字段：

```text
inspect: invalid query
```

先用 `show tables` 和 `describe <table>` 确认表名、字段名。

message 查询缺少 channel key：

```text
inspect: invalid query
```

`message.message` 必须带 `channel_key`：

```bash
wkdb --data-dir ./data/node-1 query "select * from message.message where channel_key='g1:2' limit 50"
```

## 注意事项

- `message.channels` 是当前节点本地 message catalog，不是全局 channel 列表。
- `message.message` 按 channel 的 `message_seq` 扫描。
- metadata 本地有界扫描适合抽样和节点本地排查，不适合当成全局计数。
- 写事故记录时，建议附上节点 id、数据目录来源、执行命令、输出格式和 cursor 状态。
