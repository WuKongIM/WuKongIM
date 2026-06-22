# wkdb 本地排查与离线导入工具使用手册

## 适用范围

`wkdb` 是一个节点本地离线 CLI，用于查看或导入 WuKongIM 某个节点的数据目录里的 metadata 和 message 存储。

它包含两类命令：

- `query` / `repl`：read-only inspection，只读查看本地 Pebble 存储。
- `import`：offline import，显式离线写入一个目标数据目录。

`query` / `repl` 只做观察：

- 以只读模式打开本地 Pebble 存储。
- 不连接其他集群节点。
- 不提供全局集群视图。
- 不修复、不 compact、不重写 key，不执行 insert/update/delete。

`import` 不是只读命令。它不连接集群节点，只对本地离线目标目录写入，并要求操作者显式执行 `import` 子命令。

适合在事故排查、迁移核对、存储布局调试时，用来查看某一个节点文件里的真实本地状态。

## 安全前提

优先使用以下数据源：

- 已停止节点的数据目录。
- 文件系统快照。
- 从节点目录拷贝出来的一份副本。

除非已经明确验证过当前环境支持在线并发只读打开，否则不要直接对线上运行中的生产节点目录执行排查命令。

不要把一个节点的本地扫描结果当成全局集群事实。涉及集群决策时，需要结合其他节点结果或 manager/cluster API。

执行 `wkdb import` 时，建议目标目录是 fresh/offline target。当前 import 不支持在线导入，不支持把 bundle merge 到已有数据目录，也不提供事务回滚。

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

离线校验 import bundle：

```bash
wkdb --data-dir ./node-new --hash-slot-count 256 import --input ./wkdb-dump --dry-run
```

真实离线导入：

```bash
wkdb --data-dir ./node-new --hash-slot-count 256 import --input ./wkdb-dump --require-empty
```

注意：`wkdb` 的全局 flags 必须放在 command 前面，例如 `--data-dir`、`--hash-slot-count`、`--config`；`import` 自己的 flags 放在 `import` 后面，例如 `--input`、`--dry-run`、`--require-empty`。

## 离线 import

`wkdb import` 用于把 WKDB Import Bundle v1 写入一个离线目标目录。它是本工具当前唯一会写本地存储的命令。

安全边界：

- 不连接集群节点，不通过 manager 或节点间 RPC 协调。
- 不支持在线 import；不要对正在运行的节点数据目录执行。
- 不支持 merge；真实导入要求 `--require-empty`，目标 metadata/message 存储必须为空。
- 失败不是事务性的；如果真实导入中途失败，目标目录应视为 partial import，丢弃或从备份恢复后重试。

Dry-run 只校验 bundle，不打开可写 NodeStore，也不需要 `--data-dir`、`--meta-path` 或 `--message-path` 这类 storage path：

```bash
wkdb --hash-slot-count 256 import --input ./wkdb-dump --dry-run
```

如果传了 `--hash-slot-count`，dry-run 会校验目标 hash-slot 数与 manifest 一致；如果不传，则使用 manifest 的 `hash_slot_count` 做 bundle 内部校验。

真实导入必须传 `--require-empty`，并且必须能解析出 metadata 和 message 两个存储路径：

```bash
wkdb --data-dir ./node-new --hash-slot-count 256 import --input ./wkdb-dump --require-empty
```

可选批量参数：

- `--subscriber-batch-size`：每批写入 subscriber UID 数量。
- `--message-batch-size`：每批写入 message record 数量。
- `--message-batch-bytes`：每批 message payload 近似字节上限。

导入成功后输出一行摘要：

```text
validated=5 written=4 messages=1 subscribers=1 channels=1 files=5 bytes=1234
```

## WKDB Import Bundle v1

bundle 根目录由 `manifest.json` 和若干 JSONL 文件组成。目录名和文件名可以自定义，但必须通过 manifest 的 `files[].path` 声明：

```text
wkdb-dump/
  manifest.json
  meta/
    users.jsonl
    devices.jsonl
    channels.jsonl
    subscribers.jsonl
    user_channel_memberships.jsonl
    conversations.jsonl
    channel_latest.jsonl
  message/
    channels.jsonl
    messages-000001.jsonl
```

`manifest.json` 当前真实支持的字段：

```json
{
  "format": "wkdb-import-bundle",
  "version": 1,
  "hash_slot_count": 256,
  "files": [
    {
      "path": "meta/users.jsonl",
      "kind": "meta.users",
      "rows": 1,
      "sha256": "lowercase-hex-sha256"
    }
  ]
}
```

manifest 规则：

- `format` 必须是 `wkdb-import-bundle`。
- `version` 必须是 `1`。
- `hash_slot_count` 必须大于 0，并且导入时要能放入 uint16。
- `files[].path` 是 bundle 根目录内的 clean relative path，使用 `/`，不能是绝对路径、反斜杠路径、`.`、`..` 或逃逸根目录的路径。
- `files[].kind` 必须是当前支持的 kind。
- `files[].rows` 是对应 JSONL 文件的非空 record 行数。
- `files[].sha256` 是对应文件原始字节的 lowercase hex SHA256。
- manifest 和 record 都使用严格 JSON 解码；不要输出当前格式未声明的额外字段。

支持的 `files[].kind`：

- `meta.users`
- `meta.devices`
- `meta.channels`
- `meta.subscribers`
- `meta.user_channel_memberships`
- `meta.conversations`
- `meta.channel_latest`
- `message.channels`
- `message.messages`

所有数据文件都是 JSONL：每个非空行是一个 JSON object。二进制字段使用 base64 字符串并以 `_b64` 结尾。uint64 字段可以写成 JSON 安全 number，也可以写成 decimal string；建议 exporter 对超过 JavaScript 安全整数范围的 uint64 统一写 decimal string。

record 字段如下。未特别说明时，数值字段缺省为 0，字符串缺省为空字符串；但用于定位的 key 字段必须提供非空值，`hash_slot` 必须按当前 `hash_slot_count` 重算。

- `meta.users`：`hash_slot`, `uid`, `token`, `device_flag`, `device_level`。
- `meta.devices`：`hash_slot`, `uid`, `device_flag`, `token`, `device_level`。
- `meta.channels`：`hash_slot`, `channel_id`, `channel_type`, `ban`, `disband`, `send_ban`, `allow_stranger`, `large`, `subscriber_mutation_version`。
- `meta.subscribers`：`hash_slot`, `channel_id`, `channel_type`, `uid`。
- `meta.user_channel_memberships`：`hash_slot`, `uid`, `channel_id`, `channel_type`, `join_seq`, `updated_at_ms`。
- `meta.conversations`：`hash_slot`, `uid`, `kind`, `channel_id`, `channel_type`, `read_seq`, `deleted_to_seq`, `active_at`, `updated_at`, `sparse_active`。`kind` 只能是 `normal` 或 `cmd`。
- `meta.channel_latest`：`hash_slot`, `channel_id`, `channel_type`, `last_message_id`, `last_message_seq`, `last_at`, `from_uid`, `client_msg_no`, `last_payload_b64`, `updated_at`。当前实现要求字段名是 `last_payload_b64`。
- `message.channels`：`channel_key`, `channel_id`, `channel_type`。
- `message.messages`：`channel_key`, `message_seq`, `message_id`, `client_msg_no`, `from_uid`, `server_timestamp_ms`, `payload_b64`。

hash-slot 校验规则：

- `meta.users` 和 `meta.devices` 按 `uid` 重算 `hash_slot`。
- `meta.channels`、`meta.subscribers`、`meta.channel_latest` 按 `channel_id` 重算 `hash_slot`。
- `meta.user_channel_memberships` 和 `meta.conversations` 按 `uid` 重算 `hash_slot`。

排序和引用规则：

- `meta.subscribers` 在同一 logical stream 中必须按 `(hash_slot, channel_id, channel_type, uid)` 升序。
- `message.channels` 在同一 logical stream 中必须按 `channel_key` 严格升序，重复 `channel_key` 会被拒绝。
- `message.messages` 在同一 logical stream 中必须全局按 `(channel_key, message_seq)` 严格升序。
- 每个 channel 的 `message_seq` 必须从 1 开始连续递增。
- 每条 `message.messages` 必须引用一个已在 `message.channels` 中声明的 `channel_key`。
- 同一 channel 内重复 `message_id` 会被拒绝；同一 channel 内相同 `(from_uid, client_msg_no)` 的非空幂等键也会被拒绝。

当一个 kind 被拆成多个文件时，校验会按 manifest 中同 kind 文件出现的顺序组成一个 logical stream，因此 exporter 要保证跨文件边界也满足上述排序规则。

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
