# Conversation Sync Cutover Runbook

## Goal

在默认开放 `/conversation/sync` 之前，先完成 `UserConversationState` backfill、抽样校验，并确认 active hint working-set 链路已接入，避免未回填用户直接承接同步流量。

## Preconditions

- 当前部署形态按“单节点集群”或多节点集群统一处理
- 新版本已经开始写入 / 暴露：
  - `UserConversationState`
  - UID-owner `ActiveHintCache` 覆盖层
  - committed message -> conversation projector -> active hint routing
- 若是新部署且不存在历史数据，可跳过 backfill，直接进入抽样校验

## Backfill Source Priority

优先级必须固定，避免状态覆盖错误：

1. 旧会话目录
2. 订阅关系 / 单聊关系

规则：

- 旧会话目录是以下字段的唯一优先来源：
  - `read_seq`
  - `deleted_to_seq`
  - `active_at`
  - `updated_at`
- 订阅关系 / 单聊关系只用于补齐缺失的 `UserConversationState` 行
- 如果旧会话目录已有该行，订阅关系 / 单聊关系不得覆盖上述进度字段

## Required Backfill Fields

对每条 `UserConversationState` 必须回填：

- `read_seq`
- `deleted_to_seq`
- `active_at`
- `updated_at`

补齐缺失行时默认值：

- `read_seq = 0`
- `deleted_to_seq = 0`
- `active_at = 0`
- `updated_at = 0`

幂等要求：

- 主键按 `(uid, channel_type, channel_id)` upsert
- 重复执行同一批 backfill 不得放大字段值
- 不得生成重复行

## Active Hint Policy

- 不再维护持久化频道更新投影；Channel Log 是最新消息事实源
- `active_at` 是 best-effort working-set hint，允许丢失、延迟和批量落盘
- `ListUserConversationActive` 必须在 UID owner 合并持久化 active rows 与 hot active hints
- 删除会话必须先持久化 `DeletedToSeq` 并清空 `ActiveAt`，再删除 hot hint / 安装 delete barrier
- 新消息若 `MessageSeq > DeletedToSeq`，会话应重新进入 working set

## Cutover Gate

只有同时满足以下条件，才允许上线默认开放的 `/conversation/sync`：

1. `UserConversationState` backfill 已完成
2. 抽样校验通过
3. Active hint cache 已注册到 store overlay
4. projector 能从 committed message 提交 best-effort active hints
5. 回滚方案已确认

若任一条件不满足：

- 不允许对外提供 `/conversation/sync`

## Sampling Checklist

至少执行以下抽样：

1. 随机抽样用户，校验 `UserConversationState` 行数与旧目录是否一致或在预期补齐范围内
2. 随机抽样最近活跃会话，校验 `active_at` 与旧目录最近活跃时间是否一致或符合迁移规则
3. 随机抽样存在删除/已读进度的会话，校验：
   - `read_seq`
   - `deleted_to_seq`
   - `updated_at`
4. 对抽样用户发起 brand-new 请求：
   - `version=0`
   - `last_msg_seqs=""`
   - 验证结果非空且不过量，只落在服务端 working set 窗口内
5. 对抽样频道发送 cutover 后新消息，确认：
   - `ListUserConversationActive` 可看到未 flush 的 hot hint
   - `/conversation/sync` 返回值来自 Channel Log 最新消息事实
6. 对抽样会话执行 delete，确认：
   - 会话立即从 hot working set 消失
   - `UserConversationState.deleted_to_seq` 推进且 `active_at=0`
   - 后续更新消息可让会话重新出现

## Rollout Steps

1. 部署新版本
2. 执行 `UserConversationState` backfill
3. 完成抽样校验
4. 观察 active hint cache 命中、flush 失败率、drop 计数和 sync 延迟
5. 验证 `/conversation/sync` 错误率、返回量和延迟
6. 持续观察并确认稳定

## Failure Handling

若出现以下任一情况，立即停止对外暴露 `/conversation/sync` 或回滚到不包含该接口的稳定版本：

- backfill 未完成
- 抽样用户 `UserConversationState` 行数明显缺失
- `active_at` 明显异常，working set 结果大面积为空
- active hint overlay 未接入或 projector 无法提交 hot hints
- delete 后 stale hint 重新激活旧消息
- `/conversation/sync` brand-new 请求结果异常为空或异常过量

处理原则：

- 先修复数据或 active hint/projector 链路
- 重新执行抽样校验
- 校验再次通过后才能重新开放 `/conversation/sync`
