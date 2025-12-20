# 消息删除日志功能手动测试指南

## 前提条件

1. **已启动 3 节点集群**
   ```bash
   # 终端 1
   go run main.go --config exampleconfig/cluster1.yaml
   
   # 终端 2
   go run main.go --config exampleconfig/cluster2.yaml
   
   # 终端 3
   go run main.go --config exampleconfig/cluster3.yaml
   ```

2. **安装必要工具**
   ```bash
   # macOS
   brew install jq
   
   # 或使用已有的 curl
   ```

3. **API 端口确认**
   - Node1: http://localhost:5001
   - Node2: http://localhost:5002
   - Node3: http://localhost:5003

## 测试场景 1: 基本功能测试

### 1.1 发送测试消息

```bash
# 发送 100 条消息到测试频道
for i in {1..100}; do
  curl -X POST "http://localhost:5001/message/send" \
    -H "Content-Type: application/json" \
    -d "{
      \"header\":{\"no_persist\":0},
      \"from_uid\":\"test_user\",
      \"channel_id\":\"test_channel\",
      \"channel_type\":2,
      \"payload\":\"message $i\"
    }"
done
```

### 1.2 验证消息存在

```bash
# 查询消息（Node1）
curl -X POST "http://localhost:5001/channel/messagesync" \
  -H "Content-Type: application/json" \
  -d '{
    "login_uid":"test_user",
    "channel_id":"test_channel",
    "channel_type":2,
    "start_message_seq":1,
    "end_message_seq":100,
    "limit": 100,
    "pull_mode": 1
  }' | jq '.messages | length'

# 应该返回接近 100（实际消息数量）
```

### 1.3 执行范围删除

```bash
# 删除消息 seq 50-80
curl -X POST "http://localhost:5001/messages/deleteRange" \
  -H "Content-Type: application/json" \
  -d '{
    "login_uid":"test_user",
    "channel_id":"test_channel",
    "channel_type":2,
    "start_msg_seq":50,
    "end_msg_seq":80
  }'

# 等待同步
sleep 3
```

### 1.4 验证删除成功

```bash
# 验证 Node1
curl -X POST "http://localhost:5001/channel/messagesync" \
  -H "Content-Type: application/json" \
  -d '{
    "login_uid":"test_user",
    "channel_id":"test_channel",
    "channel_type":2,
    "start_message_seq":50,
    "end_message_seq":80,
    "limit": 100,
    "pull_mode": 1
  }' | jq '.messages | length'

# 应该返回 0

# 验证 Node2
curl -X POST "http://localhost:5002/channel/messagesync" \
  -H "Content-Type: application/json" \
  -d '{
    "login_uid":"test_user",
    "channel_id":"test_channel",
    "channel_type":2,
    "start_message_seq":50,
    "end_message_seq":80,
    "limit": 100,
    "pull_mode": 1
  }' | jq '.messages | length'

# 应该返回 0

# 验证 Node3
curl -X POST "http://localhost:5003/channel/messagesync" \
  -H "Content-Type: application/json" \
  -d '{
    "login_uid":"test_user",
    "channel_id":"test_channel",
    "channel_type":2,
    "start_message_seq":50,
    "end_message_seq":80,
    "limit": 100,
    "pull_mode": 1
  }' | jq '.messages | length'

# 应该返回 0
```

### 1.5 检查删除日志

```bash
# 查看日志文件，确认删除日志已记录
grep "已保存删除日志" wukongimdata/logs/*.log | tail -10

# 或者查看最新的日志
tail -f wukongimdata/logs/*.log | grep "删除"
```

**预期结果**:
- ✅ 所有节点的消息 seq 50-80 都已删除
- ✅ 日志中有 "已保存删除日志" 的记录
- ✅ 日志中记录了 channelId, startSeq, endSeq, logIndex

---

## 测试场景 2: 节点故障后的数据一致性

### 2.1 准备环境

```bash
# 发送新一批消息 (seq 101-200)
for i in {101..200}; do
  curl -X POST "http://localhost:5001/message/send" \
    -H "Content-Type: application/json" \
    -d "{
      \"header\":{\"no_persist\":0},
      \"from_uid\":\"test_user\",
      \"channel_id\":\"test_channel\",
      \"channel_type\":2,
      \"payload\":\"message $i\"
    }"
done
```

### 2.2 停止 Node3

```bash
# 在运行 Node3 的终端按 Ctrl+C 停止
# 或者
kill -9 $(pgrep -f "cluster3.yaml")
```

### 2.3 Node3 离线时执行删除

```bash
# 删除消息 seq 101-150
curl -X POST "http://localhost:5001/messages/deleteRange" \
  -H "Content-Type: application/json" \
  -d '{
    "login_uid":"test_user",
    "channel_id":"test_channel",
    "channel_type":2,
    "start_msg_seq":101,
    "end_msg_seq":150
  }'

# 等待同步
sleep 3
```

### 2.4 验证 Node1 和 Node2 已删除

```bash
# Node1
curl -X POST "http://localhost:5001/channel/messagesync" \
  -H "Content-Type: application/json" \
  -d '{
    "login_uid":"test_user",
    "channel_id":"test_channel",
    "channel_type":2,
    "start_message_seq":101,
    "end_message_seq":150,
    "limit": 100,
    "pull_mode": 1
  }' | jq '.messages | length'
# 应返回 0

# Node2
curl -X POST "http://localhost:5002/channel/messagesync" \
  -H "Content-Type: application/json" \
  -d '{
    "login_uid":"test_user",
    "channel_id":"test_channel",
    "channel_type":2,
    "start_message_seq":101,
    "end_message_seq":150,
    "limit": 100,
    "pull_mode": 1
  }' | jq '.messages | length'
# 应返回 0
```

### 2.5 重启 Node3

```bash
# 在新终端重启 Node3
go run main.go --config exampleconfig/cluster3.yaml

# 等待启动完成（观察日志）
# 应该看到类似 "Raft 节点已启动" 的日志
```

### 2.6 关键验证 - Node3 数据一致性

```bash
# 等待 5-10 秒，让 Raft 同步完成
sleep 10

# 验证 Node3 的消息是否已删除
curl -X POST "http://localhost:5003/channel/messagesync" \
  -H "Content-Type: application/json" \
  -d '{
    "login_uid":"test_user",
    "channel_id":"test_channel",
    "channel_type":2,
    "start_message_seq":101,
    "end_message_seq":150,
    "limit": 100,
    "pull_mode": 1
  }' | jq '.messages | length'
```

**判断标准**:

✅ **测试通过** (返回 0):
- Node3 通过 Raft 日志保留机制自动恢复
- 说明日志保留策略生效，Node3 在重启后同步到了删除日志

⚠️ **需要进一步验证** (返回 > 0):
- 如果返回 50（有消息残留），说明 Raft 日志已被截断
- 需要检查删除元数据日志是否记录
- 可能需要手动触发恢复（如果实现了恢复 API）

### 2.7 检查删除元数据日志

```bash
# 查看 Node3 的日志，看是否有恢复相关信息
tail -100 wukongimdata/logs/wukongim.log | grep -i "恢复\|recovery\|删除日志"

# 查询删除日志数量（通过数据库或 API）
# 注意：需要根据实际实现调整
```

---

## 测试场景 3: Raft 日志截断后的恢复

### 3.1 模拟日志截断

```bash
# 这个场景比较复杂，需要等待 Raft 日志自然截断
# 或者手动触发截断（需要修改配置降低保留阈值）

# 临时方案：修改配置，降低日志保留数量
# 在 exampleconfig/cluster*.yaml 中调整（如果支持配置）
raft_log_retention:
  keep_applied_logs: 100  # 降低到 100 条
  min_keep_logs: 50
```

### 3.2 触发大量操作

```bash
# 发送大量消息，触发日志截断
for i in {1..500}; do
  curl -X POST "http://localhost:5001/message/send" \
    -H "Content-Type: application/json" \
    -d "{
      \"header\":{\"no_persist\":0},
      \"from_uid\":\"test_user\",
      \"channel_id\":\"test_channel\",
      \"channel_type\":2,
      \"payload\":\"bulk message $i\"
    }"
done
```

### 3.3 检查日志截断

```bash
# 查看日志，确认截断操作
grep "完成日志截断" wukongimdata/logs/*.log | tail -10

# 查看保留的日志数量
grep "keptLogsCount" wukongimdata/logs/*.log | tail -5
```

---

## 测试场景 4: 监控指标验证

### 4.1 查看 Prometheus 指标

```bash
# Node1 指标
curl -s http://localhost:5001/metrics | grep -E "db_save_delete_log_count|db_deleterange_messages_count|db_get_delete_logs"

# Node2 指标
curl -s http://localhost:5002/metrics | grep -E "db_save_delete_log_count|db_deleterange_messages_count"

# Node3 指标
curl -s http://localhost:5003/metrics | grep -E "db_save_delete_log_count|db_deleterange_messages_count"
```

### 4.2 预期指标

```
db_save_delete_log_count                      # 应该 > 0（有删除操作）
db_deleterange_messages_count                 # 删除操作计数
db_get_delete_logs_since_log_index_count      # 恢复时查询次数
db_cleanup_old_delete_logs_count              # 清理次数（可能为 0，24小时执行一次）
```

---

## 测试场景 5: 长期运行测试

### 5.1 压力测试

```bash
# 持续发送消息和删除（使用脚本）
while true; do
  # 发送 50 条消息
  for i in {1..50}; do
    curl -s -X POST "http://localhost:5001/message/send" \
      -H "Content-Type: application/json" \
      -d "{
        \"header\":{\"no_persist\":0},
        \"from_uid\":\"test_user\",
        \"channel_id\":\"pressure_test\",
        \"channel_type\":2,
        \"payload\":\"pressure test\"
      }" > /dev/null
  done
  
  # 随机删除一段
  start=$((RANDOM % 100 + 1))
  end=$((start + 20))
  
  curl -s -X POST "http://localhost:5001/messages/deleteRange" \
    -H "Content-Type: application/json" \
    -d "{
      \"login_uid\":\"test_user\",
      \"channel_id\":\"pressure_test\",
      \"channel_type\":2,
      \"start_msg_seq\":$start,
      \"end_msg_seq\":$end
    }" > /dev/null
  
  echo "已完成一轮：发送 50 条，删除 seq $start-$end"
  sleep 5
done
```

### 5.2 监控存储

```bash
# 查看删除日志占用空间
du -sh wukongimdata/data/

# 查看删除日志数量
# 通过日志或 API 查询
```

---

## 故障排查

### 问题 1: Node3 恢复后仍有消息残留

**可能原因**:
1. Raft 日志已截断，无法自动同步
2. 删除元数据日志未生效
3. 需要手动触发恢复

**排查步骤**:
```bash
# 1. 检查删除日志是否记录
grep "已保存删除日志" wukongimdata/logs/*.log

# 2. 检查 Node3 的最后应用索引
# 通过日志查看

# 3. 手动触发恢复（如果实现了 API）
# curl -X POST "http://localhost:5003/admin/recovery/channel" ...
```

### 问题 2: 监控指标为空

**可能原因**:
1. Metrics 端点未配置
2. 指标未注册

**排查步骤**:
```bash
# 查看所有可用指标
curl -s http://localhost:5001/metrics | head -50

# 确认是否有 db_ 开头的指标
curl -s http://localhost:5001/metrics | grep "^db_"
```

### 问题 3: 删除操作失败

**排查步骤**:
```bash
# 查看错误日志
tail -100 wukongimdata/logs/*.log | grep -i "error\|失败"

# 检查频道是否存在
# 检查权限配置
```

---

## 成功标准

### ✅ 基本功能测试通过
- [ ] 消息可以正常发送
- [ ] 范围删除可以正常执行
- [ ] 所有节点的删除操作一致
- [ ] 删除日志被正确记录

### ✅ 节点故障恢复测试通过
- [ ] Node3 离线期间，Node1/Node2 正常删除
- [ ] Node3 恢复后，数据与 Node1/Node2 一致
- [ ] 日志中有恢复相关记录（如果触发了恢复）

### ✅ 监控指标正常
- [ ] 可以查看到删除相关指标
- [ ] 指标数值合理

### ✅ 长期稳定性
- [ ] 压力测试下无崩溃
- [ ] 删除日志不会无限增长
- [ ] 性能无明显下降

---

## 快速自动化测试

使用提供的测试脚本：

```bash
# 给脚本执行权限
chmod +x test/delete_log_test.sh

# 运行测试
./test/delete_log_test.sh
```

脚本会自动完成大部分测试流程，只需在提示时手动停止/启动 Node3。

