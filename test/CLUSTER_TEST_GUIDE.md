# 集群环境测试指南

## 目标
验证消息删除日志功能在集群环境下的正确性，特别是节点故障恢复场景。

## 前置条件

### 1. 确保单节点测试通过
```bash
# 先运行快速验证
./test/quick_verify.sh

# 应该看到：
# ✅ 发送 30 条测试消息
# ✅ 找到 XX 条消息
# ✅ 删除请求已发送
# ✅✅✅ 删除成功
```

### 2. 准备集群配置
检查集群配置文件：
```bash
ls -l exampleconfig/cluster*.yaml

# 应该有：
# cluster1.yaml - 节点1 (端口 5001, 5100, 6300)
# cluster2.yaml - 节点2 (端口 5002, 5101, 6301)
# cluster3.yaml - 节点3 (端口 5003, 5102, 6302)
```

## 测试步骤

### 阶段 1: 启动集群

#### 1.1 启动第一个节点 (Leader候选)
```bash
# 终端 1
go run main.go --config exampleconfig/cluster1.yaml

# 等待看到类似输出：
# [INFO] 节点启动成功
# [INFO] HTTP API 服务: :5001
# [INFO] 集群 TCP 服务: 127.0.0.1:5100
```

#### 1.2 启动第二个节点
```bash
# 终端 2
go run main.go --config exampleconfig/cluster2.yaml

# 等待节点加入集群：
# [INFO] 加入集群成功
```

#### 1.3 启动第三个节点
```bash
# 终端 3
go run main.go --config exampleconfig/cluster3.yaml
```

#### 1.4 验证集群状态
```bash
# 查看节点1的集群信息
curl -s http://localhost:5001/cluster/status | jq '.'

# 应该看到 3 个节点都在线
```

### 阶段 2: 基础功能测试

#### 2.1 发送测试消息
```bash
# 发送 50 条消息到节点1
for i in {1..50}; do
  PAYLOAD=$(echo -n "cluster test message $i" | base64)
  curl -s -X POST "http://localhost:5001/message/send" \
    -H "Content-Type: application/json" \
    -d "{
      \"header\":{\"no_persist\":0},
      \"from_uid\":\"cluster_user\",
      \"channel_id\":\"cluster_channel\",
      \"channel_type\":2,
      \"payload\":\"$PAYLOAD\"
    }" > /dev/null
  
  if [ $((i % 10)) -eq 0 ]; then
    echo "已发送 $i 条消息"
  fi
done

echo "✓ 完成，等待同步..."
sleep 3
```

#### 2.2 验证所有节点都有消息
```bash
# 节点1
echo "=== 节点1 ==="
curl -s -X POST "http://localhost:5001/channel/messagesync" \
  -H "Content-Type: application/json" \
  -d '{
    "login_uid":"cluster_user",
    "channel_id":"cluster_channel",
    "channel_type":2,
    "start_message_seq":0,
    "end_message_seq":100,
    "limit":100,
    "pull_mode":1
  }' | jq '{count: (.messages | length), first: .messages[0].message_seq, last: .messages[-1].message_seq}'

# 节点2
echo "=== 节点2 ==="
curl -s -X POST "http://localhost:5002/channel/messagesync" \
  -H "Content-Type: application/json" \
  -d '{
    "login_uid":"cluster_user",
    "channel_id":"cluster_channel",
    "channel_type":2,
    "start_message_seq":0,
    "end_message_seq":100,
    "limit":100,
    "pull_mode":1
  }' | jq '{count: (.messages | length), first: .messages[0].message_seq, last: .messages[-1].message_seq}'

# 节点3
echo "=== 节点3 ==="
curl -s -X POST "http://localhost:5003/channel/messagesync" \
  -H "Content-Type: application/json" \
  -d '{
    "login_uid":"cluster_user",
    "channel_id":"cluster_channel",
    "channel_type":2,
    "start_message_seq":0,
    "end_message_seq":100,
    "limit":100,
    "pull_mode":1
  }' | jq '{count: (.messages | length), first: .messages[0].message_seq, last: .messages[-1].message_seq}'

# 三个节点应该返回相同的消息数量
```

#### 2.3 删除一部分消息
```bash
echo "删除 seq 10-20 的消息..."
curl -s -X POST "http://localhost:5001/messages/deleteRange" \
  -H "Content-Type: application/json" \
  -d '{
    "login_uid":"cluster_user",
    "channel_id":"cluster_channel",
    "channel_type":2,
    "start_msg_seq":10,
    "end_msg_seq":20
  }' | jq '.'

# 应该返回 {"status": 200}

sleep 3
```

#### 2.4 验证所有节点删除成功
```bash
# 检查所有节点 seq 10-20 是否都被删除
for port in 5001 5002 5003; do
  echo "=== 节点 $port ==="
  count=$(curl -s -X POST "http://localhost:$port/channel/messagesync" \
    -H "Content-Type: application/json" \
    -d '{
      "login_uid":"cluster_user",
      "channel_id":"cluster_channel",
      "channel_type":2,
      "start_message_seq":10,
      "end_message_seq":21,
      "limit":20,
      "pull_mode":1
    }' | jq -r '.messages | length')
  
  if [ "$count" -eq "0" ]; then
    echo "✅ 消息已删除"
  else
    echo "❌ 仍有 $count 条消息"
  fi
done
```

### 阶段 3: 节点故障恢复测试 ⭐

这是**核心测试场景**，验证节点故障后的数据一致性。

#### 3.1 停止节点3
```bash
# 在节点3的终端按 Ctrl+C 停止
# 或者如果是后台运行：
kill -TERM $(lsof -ti:6302)

echo "✓ 节点3 已停止"
```

#### 3.2 发送更多消息
```bash
# 节点3 离线期间，发送新消息
for i in {51..80}; do
  PAYLOAD=$(echo -n "during node3 down: message $i" | base64)
  curl -s -X POST "http://localhost:5001/message/send" \
    -H "Content-Type: application/json" \
    -d "{
      \"header\":{\"no_persist\":0},
      \"from_uid\":\"cluster_user\",
      \"channel_id\":\"cluster_channel\",
      \"channel_type\":2,
      \"payload\":\"$PAYLOAD\"
    }" > /dev/null
done

echo "✓ 发送 30 条新消息 (seq 51-80)"
sleep 3
```

#### 3.3 删除部分新消息
```bash
echo "删除 seq 55-65 的消息 (节点3 仍然离线)..."
curl -s -X POST "http://localhost:5001/messages/deleteRange" \
  -H "Content-Type: application/json" \
  -d '{
    "login_uid":"cluster_user",
    "channel_id":"cluster_channel",
    "channel_type":2,
    "start_msg_seq":55,
    "end_msg_seq":65
  }' | jq '.'

sleep 3

# 验证节点1和2已删除
for port in 5001 5002; do
  echo "节点 $port:"
  curl -s -X POST "http://localhost:$port/channel/messagesync" \
    -H "Content-Type: application/json" \
    -d '{
      "login_uid":"cluster_user",
      "channel_id":"cluster_channel",
      "channel_type":2,
      "start_message_seq":55,
      "end_message_seq":66,
      "limit":20,
      "pull_mode":1
    }' | jq -r '.messages | length'
done

# 应该都返回 0
```

#### 3.4 重启节点3
```bash
# 终端 3 (重新启动)
go run main.go --config exampleconfig/cluster3.yaml

# 等待节点3重新加入集群并同步数据
# 观察日志中是否有：
# [INFO] 加入集群成功
# [INFO] 开始同步数据...
# [DEBUG] 已保存删除日志...
# [INFO] RecoverChannelFromDeleteLogs... (如果有)
```

#### 3.5 等待数据同步
```bash
echo "等待节点3同步数据 (可能需要 10-30 秒)..."
sleep 15
```

#### 3.6 **关键验证**: 检查节点3的数据一致性
```bash
echo "=== 验证节点3的数据一致性 ==="

# 1. 检查节点3是否有 seq 51-80 的消息
echo "1. 检查新消息是否同步:"
total_count=$(curl -s -X POST "http://localhost:5003/channel/messagesync" \
  -H "Content-Type: application/json" \
  -d '{
    "login_uid":"cluster_user",
    "channel_id":"cluster_channel",
    "channel_type":2,
    "start_message_seq":51,
    "end_message_seq":81,
    "limit":100,
    "pull_mode":1
  }' | jq -r '.messages | length')

echo "   节点3 找到 $total_count 条消息 (期望 ~19 条，因为删除了 55-65)"

# 2. ⭐ 核心测试：验证节点3是否正确删除了 seq 55-65
echo ""
echo "2. ⭐ 核心测试：验证 seq 55-65 是否被删除:"
deleted_range_count=$(curl -s -X POST "http://localhost:5003/channel/messagesync" \
  -H "Content-Type: application/json" \
  -d '{
    "login_uid":"cluster_user",
    "channel_id":"cluster_channel",
    "channel_type":2,
    "start_message_seq":55,
    "end_message_seq":66,
    "limit":20,
    "pull_mode":1
  }' | jq -r '.messages | length')

if [ "$deleted_range_count" -eq "0" ]; then
  echo "   ✅✅✅ 成功！节点3 正确地删除了 seq 55-65 的消息"
  echo "   这证明了删除日志补偿机制工作正常！"
else
  echo "   ❌ 失败！节点3 仍有 $deleted_range_count 条消息未删除"
  echo "   这表示删除日志补偿机制可能有问题"
fi

# 3. 对比三个节点的数据
echo ""
echo "3. 对比所有节点的数据一致性:"
for port in 5001 5002 5003; do
  count=$(curl -s -X POST "http://localhost:$port/channel/messagesync" \
    -H "Content-Type: application/json" \
    -d '{
      "login_uid":"cluster_user",
      "channel_id":"cluster_channel",
      "channel_type":2,
      "start_message_seq":0,
      "end_message_seq":100,
      "limit":100,
      "pull_mode":1
    }' | jq -r '.messages | length')
  
  echo "   节点 $port: $count 条消息"
done

echo ""
echo "   三个节点的消息数量应该完全一致！"
```

### 阶段 4: 监控和日志检查

#### 4.1 检查删除日志计数
```bash
echo "=== 检查各节点的删除日志 ==="

# 如果实现了 GetDeleteLogsCount API
for port in 5001 5002 5003; do
  echo "节点 $port:"
  # TODO: 需要实现一个 API 来查询删除日志数量
  # curl -s http://localhost:$port/debug/delete_logs_count
done
```

#### 4.2 查看日志文件
```bash
# 查找删除相关的日志
echo "=== 查看删除操作日志 ==="
tail -100 wukongimdata/logs/*.log | grep -i "已保存删除日志\|DeleteLog\|Recovery" | tail -10

# 查找 Raft 日志截断相关的日志
echo ""
echo "=== 查看 Raft 日志保留策略 ==="
tail -100 wukongimdata/logs/*.log | grep -i "truncate\|保留策略\|KeepApplied" | tail -5
```

## 成功标准

### ✅ 测试通过的标志

1. **基础功能** 
   - ✅ 所有节点都能发送/接收消息
   - ✅ 所有节点都能正确删除消息
   - ✅ 删除操作在所有节点同步

2. **故障恢复** ⭐⭐⭐
   - ✅ 节点3离线期间，节点1和2正常删除消息
   - ✅ 节点3重启后，能通过 Raft 日志或删除日志补偿缺失的删除操作
   - ✅ 节点3恢复后，seq 55-65 的消息查询返回 0 条
   - ✅ 三个节点的消息数量和内容完全一致

3. **数据一致性**
   - ✅ 任意时刻查询任意节点，相同 seq 范围的消息数量和内容一致
   - ✅ 删除后的 seq 在所有节点上都查询不到

## 故障排查

### 问题 1: 节点3恢复后仍有已删除的消息

**可能原因**:
- Raft 日志已被截断，且删除日志未保存
- RecoveryManager 未被调用
- 删除日志查询失败

**排查步骤**:
```bash
# 1. 检查日志中是否有 "RecoverChannelFromDeleteLogs"
grep -i "RecoverChannel" wukongimdata/logs/*.log

# 2. 检查删除日志是否被保存
grep -i "SaveDeleteLog\|已保存删除日志" wukongimdata/logs/*.log | tail -5

# 3. 检查 Raft 日志截断情况
grep -i "TruncateLog\|截断" wukongimdata/logs/*.log | tail -5
```

### 问题 2: 节点无法加入集群

**检查**:
```bash
# 确保端口未被占用
lsof -i:5001
lsof -i:5002
lsof -i:5003

# 检查集群配置
diff exampleconfig/cluster1.yaml exampleconfig/cluster2.yaml
```

### 问题 3: 消息查询返回空

**检查**:
- 是否添加了 `"pull_mode": 1`
- payload 是否正确 base64 编码
- channel_id 和 channel_type 是否匹配

## 清理环境

测试完成后：

```bash
# 1. 停止所有节点
killall WuKongIM
# 或按 Ctrl+C 停止每个终端

# 2. 清理数据（可选）
rm -rf wukongimdata/*

# 3. 清理日志
rm -rf wukongimdata/logs/*
```

## 下一步

测试通过后:
1. 📝 记录测试结果
2. 📊 性能测试：大量消息删除的性能
3. 🔄 压力测试：高并发删除
4. 📚 完善文档

## 参考

- **快速验证**: `./test/quick_verify.sh`
- **API 文档**: `test/API_PATHS.md`
- **故障排查**: `test/TROUBLESHOOTING.md`
- **实现总结**: `docs/api/short_term_solution_implementation_summary.md`

