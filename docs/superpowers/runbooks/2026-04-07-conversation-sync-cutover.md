# Conversation Sync Cutover Runbook

## Goal

在默认开放 `/conversation/sync` 之前，先完成 `UserConversationState` backfill、抽样校验，避免未回填用户直接承接同步流量。

## Preconditions

- 当前部署形态按“单节点集群”或多节点集群统一处理
- 新版本已经开始写入：
  - `UserConversationState`
  - `ChannelUpdateLog`
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

## ChannelUpdateLog Policy

- `ChannelUpdateLog` 不要求全历史 backfill
- 只要求在上线后持续正常累积新消息的频道更新索引
- 若抽样发现上线后新消息未写入 `ChannelUpdateLog`，必须先修复 projector/flush，再允许对外提供 `/conversation/sync`

## Cutover Gate
只有同时满足以下条件，才允许上线默认开放的 `/conversation/sync`：

1. `UserConversationState` backfill 已完成
2. 抽样校验通过
3. `ChannelUpdateLog` 已开始正常累积 cutover 后的新消息
4. 回滚方案已确认

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
   - `ChannelUpdateLog` 有新 entry
   - 冷转热会话能重新推进 `active_at`

## Rollout Steps

1. 部署新版本
2. 执行 `UserConversationState` backfill
3. 完成抽样校验
4. 观察上线后新消息是否正常写入 `ChannelUpdateLog`
5. 验证 `/conversation/sync` 错误率、返回量和延迟
6. 持续观察并确认稳定

## Failure Handling

若出现以下任一情况，立即停止对外暴露 `/conversation/sync` 或回滚到不包含该接口的稳定版本：

- backfill 未完成
- 抽样用户 `UserConversationState` 行数明显缺失
- `active_at` 明显异常，working set 结果大面积为空
- 上线后 `ChannelUpdateLog` 未正常累积
- `/conversation/sync` brand-new 请求结果异常为空或异常过量

处理原则：

- 先修复数据或 projector/flush 链路
- 重新执行抽样校验
- 校验再次通过后才能重新开放 `/conversation/sync`
