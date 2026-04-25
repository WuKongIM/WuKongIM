# wkdb Pebble MVP Design

## Goal

在仓库根目录新增一个 `wkdb` 包，基于 Pebble 实现文档定义的静态表 MVP。

本次范围严格限制为两张表：

- `user`
- `channel`

对外只提供 Go 层类型化 CRUD API，不提供运行时建表能力，也不暴露通用表级 CRUD 接口。

## Scope

### In Scope

- `wkdb.Open(path string) (*DB, error)`
- `(*DB).Close() error`
- `user` 的增删改查
- `channel` 的增删改查
- `channel` 基于二级索引 `channel_id` 的查询
- 静态 `TableDesc`、`ColumnDesc`、`IndexDesc`、`ColumnFamilyDesc`
- 主记录 key 编码
- 二级索引 key 编码
- Cockroach 风格 value-side family payload 编码
- `checksum + tag + payload` 外层包装
- 主键冲突检测
- 二级索引维护
- 单元测试和基本集成测试

### Out of Scope

- 运行时 schema metadata 表
- 通用 `Insert/Get/ScanByIndex` 接口
- MVCC
- 事务语义扩展
- 多表联查
- 全表扫描接口
- 动态新增表/列/索引
- 索引回填
- 多 family 优化查询

## API

### Public Types

```go
type DB struct { ... }

type User struct {
    UID         string
    Token       string
    DeviceFlag  int64
    DeviceLevel int64
}

type Channel struct {
    ChannelID   string
    ChannelType int64
    Ban         int64
}
```

### Public Functions

```go
func Open(path string) (*DB, error)
func (db *DB) Close() error

func (db *DB) CreateUser(ctx context.Context, u User) error
func (db *DB) GetUser(ctx context.Context, uid string) (User, error)
func (db *DB) UpdateUser(ctx context.Context, u User) error
func (db *DB) DeleteUser(ctx context.Context, uid string) error

func (db *DB) CreateChannel(ctx context.Context, ch Channel) error
func (db *DB) GetChannel(ctx context.Context, channelID string, channelType int64) (Channel, error)
func (db *DB) ListChannelsByChannelID(ctx context.Context, channelID string) ([]Channel, error)
func (db *DB) UpdateChannel(ctx context.Context, ch Channel) error
func (db *DB) DeleteChannel(ctx context.Context, channelID string, channelType int64) error
```

### Error Contract

- `ErrNotFound`: 主键或索引回表后不存在
- `ErrAlreadyExists`: `CreateUser/CreateChannel` 时主键已存在
- `ErrChecksumMismatch`: value 校验失败
- `ErrInvalidArgument`: 空主键等非法输入

## Storage Model

遵循 `docs/pebble-static-user-channel-crud.md`。

### Record Kinds

- `0x01`: 主记录
- `0x02`: 二级索引

### Primary Record Key

格式：

```text
[kind:1B][tableID:4B][indexID:2B][encoded primary key columns...][familyID:uvarint]
```

### Secondary Index Key

格式：

```text
[kind:1B][tableID:4B][indexID:2B][encoded index columns...][encoded primary key suffix...]
```

二级索引 value 为空。

### Value Format

主记录 value：

```text
[checksum:4B][tag:1B][familyPayload...]
```

固定：

- `tag = 0x0A`
- `checksum = CRC32-IEEE(key + tag + familyPayload)`
- 如果校验和结果为 `0`，折叠为 `1`

### Family Payload

格式：

```text
[valueTag(colIDDelta, type)][payload]...
```

支持类型：

- string -> bytes tag
- int64 -> int tag

约束：

- family 内按 `columnID` 升序编码
- `NULL`/缺失列不落盘

## Static Catalog

### IDs

```go
const (
    TableIDUser    uint32 = 1
    TableIDChannel uint32 = 2
)
```

### user

- 主键：`uid`
- family 0：`token`, `device_flag`, `device_level`
- 无二级索引

### channel

- 主键：`channel_id`, `channel_type`
- family 0：`ban`
- 二级索引：`idx_channel_id(channel_id)`

## Package Layout

```text
wkdb/
  db.go
  errors.go
  catalog.go
  codec.go
  user.go
  channel.go
  codec_test.go
  user_test.go
  channel_test.go
```

职责：

- `db.go`: Pebble 生命周期、基础 batch 操作、内部辅助函数
- `errors.go`: 包级错误
- `catalog.go`: 静态表描述和 ID 定义
- `codec.go`: key/value 编码、解码、校验
- `user.go`: `user` 结构与 CRUD
- `channel.go`: `channel` 结构与 CRUD、索引扫描

## Behavior

### CreateUser / CreateChannel

- 校验输入
- 编码主记录 family value
- 先检查主键是否已存在
- `channel` 额外写二级索引 key
- 使用单个 Pebble batch 原子写入

### GetUser / GetChannel

- 按主键读 family 0
- 校验 checksum
- 从 key 还原主键列
- 从 value 解码非主键列

### ListChannelsByChannelID

- 通过 `idx_channel_id` 前缀扫描
- 从索引 key 还原 `channel_type`
- 逐条回表读取主记录
- 返回按 Pebble key 顺序排列的结果

### UpdateUser / UpdateChannel

- 先读取旧记录
- 重新编码 family value
- 目前两张表都只有 `family 0`，所以直接重写 `family 0`
- `channel` 的索引列未变化，不需要重写二级索引

### DeleteUser / DeleteChannel

- 先读取旧记录
- 删除 family 记录
- `channel` 额外删除二级索引记录

## Validation Rules

- `uid != ""`
- `channel_id != ""`
- `channel_type` 允许任意 `int64`
- `token` 允许空字符串

## Testing Strategy

### codec tests

- `orderedInt64` 编码有序性
- `user` 主记录 key 编码与文档示例一致
- `channel` 主记录 key 编码与文档示例一致
- `channel` 二级索引 key 编码与文档示例一致
- family payload/value 编码与文档示例一致
- checksum 校验失败返回 `ErrChecksumMismatch`

### CRUD tests

- create/get/update/delete `user`
- create/get/update/delete `channel`
- `Create*` 冲突返回 `ErrAlreadyExists`
- `Get*` 查不到返回 `ErrNotFound`
- `ListChannelsByChannelID` 通过索引返回多条记录
- 删除 `channel` 后索引记录同步删除

## Implementation Notes

- 先按 MVP 需要提供内部通用编码辅助，不对外暴露通用表引擎
- 优先保持代码可读和测试可验证，不做提前抽象
- 所有实现遵守 TDD：先测试失败，再写最小实现

## Review Notes

当前环境策略禁止在未获用户明确授权时自动使用子代理，因此文档审阅改为本地人工审阅。
