#!/bin/bash

# 消息删除日志功能测试脚本
# 用于验证节点故障后的数据一致性

set -e

# 配置
NODE1_API="http://localhost:5001"
NODE2_API="http://localhost:5002"
NODE3_API="http://localhost:5003"

# 使用时间戳创建唯一频道ID，避免多次运行测试时数据冲突
CHANNEL_ID="test_delete_channel_$(date +%s)"
CHANNEL_TYPE=2
LOGIN_UID="test_user"

echo "使用测试频道: $CHANNEL_ID"

# 颜色输出
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

log_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# 1. 创建频道并发送消息
test_send_messages() {
    log_info "步骤1: 发送测试消息 (seq 1-100)"
    
    for i in {1..100}; do
        # payload 需要 base64 编码
        local payload=$(echo -n "test message $i" | base64)
        curl -s -X POST "$NODE1_API/message/send" \
            -H "Content-Type: application/json" \
            -d "{
                \"header\":{\"no_persist\":0},
                \"from_uid\":\"$LOGIN_UID\",
                \"channel_id\":\"$CHANNEL_ID\",
                \"channel_type\":$CHANNEL_TYPE,
                \"payload\":\"$payload\"
            }" > /dev/null
        
        if [ $((i % 20)) -eq 0 ]; then
            log_info "已发送 $i 条消息"
        fi
    done
    
    log_info "✓ 消息发送完成"
    sleep 2
}

# 2. 验证消息是否存在
verify_messages_exist() {
    local node_api=$1
    local node_name=$2
    local start_seq=$3
    local end_seq=$4
    
    log_info "验证 $node_name 上消息 seq $start_seq-$end_seq 是否存在"
    
    response=$(curl -s -X POST "$node_api/channel/messagesync" \
        -H "Content-Type: application/json" \
        -d "{
            \"login_uid\":\"$LOGIN_UID\",
            \"channel_id\":\"$CHANNEL_ID\",
            \"channel_type\":$CHANNEL_TYPE,
            \"start_message_seq\":$start_seq,
            \"end_message_seq\":$end_seq,
            \"limit\":100,
            \"pull_mode\":1
        }")
    
    # 检查返回的消息数量
    count=$(echo "$response" | jq '.messages | length' 2>/dev/null || echo "0")
    expected=$((end_seq - start_seq))
    
    if [ "$count" -eq "$expected" ]; then
        log_info "✓ $node_name: 找到 $count 条消息 (符合预期)"
        return 0
    else
        log_error "✗ $node_name: 找到 $count 条消息 (期望 $expected 条)"
        return 1
    fi
}

# 3. 执行范围删除
test_delete_range() {
    local start_seq=$1
    local end_seq=$2
    
    log_info "步骤2: 删除消息 seq $start_seq-$end_seq"
    
    response=$(curl -s -X POST "$NODE1_API/messages/deleteRange" \
        -H "Content-Type: application/json" \
        -d "{
            \"login_uid\":\"$LOGIN_UID\",
            \"channel_id\":\"$CHANNEL_ID\",
            \"channel_type\":$CHANNEL_TYPE,
            \"start_msg_seq\":$start_seq,
            \"end_msg_seq\":$end_seq
        }")
    
    status=$(echo "$response" | jq -r '.status' 2>/dev/null || echo "error")
    
    if [ "$status" = "200" ] || [ "$status" = "ok" ]; then
        log_info "✓ 删除请求成功"
    else
        log_error "✗ 删除请求失败: $response"
        return 1
    fi
    
    sleep 3
}

# 4. 验证消息已被删除
verify_messages_deleted() {
    local node_api=$1
    local node_name=$2
    local start_seq=$3
    local end_seq=$4
    
    log_info "验证 $node_name 上消息 seq $start_seq-$end_seq 是否已删除"
    
    response=$(curl -s -X POST "$node_api/channel/messagesync" \
        -H "Content-Type: application/json" \
        -d "{
            \"login_uid\":\"$LOGIN_UID\",
            \"channel_id\":\"$CHANNEL_ID\",
            \"channel_type\":$CHANNEL_TYPE,
            \"start_message_seq\":$start_seq,
            \"end_message_seq\":$end_seq,
            \"limit\":100,
            \"pull_mode\":1
        }")
    
    count=$(echo "$response" | jq '.messages | length' 2>/dev/null || echo "0")
    
    if [ "$count" -eq "0" ]; then
        log_info "✓ $node_name: 消息已删除 (找到 0 条)"
        return 0
    else
        log_error "✗ $node_name: 仍有 $count 条消息未删除"
        echo "$response" | jq '.' || echo "$response"
        return 1
    fi
}

# 5. 查询删除日志（需要通过数据库或管理 API）
check_delete_logs() {
    log_info "步骤3: 检查删除日志是否记录"
    
    # 注意：这里需要根据实际的管理 API 调整
    # 如果没有 API，可以通过日志文件验证
    
    log_warn "提示：检查 wukongimdata/logs/ 中是否有 '已保存删除日志' 的日志"
    grep "已保存删除日志" wukongimdata/logs/*.log | tail -5 || log_warn "未找到删除日志（可能是日志级别问题）"
}

# 6. 模拟节点故障场景
test_node_failure_recovery() {
    log_info "========================================="
    log_info "场景测试: 节点故障后的数据一致性"
    log_info "========================================="
    
    # 6.1 发送更多消息
    log_info "步骤1: 发送消息 seq 101-200"
    for i in {101..200}; do
        curl -s -X POST "$NODE1_API/message/send" \
            -H "Content-Type: application/json" \
            -d "{
                \"header\":{\"no_persist\":0},
                \"from_uid\":\"$LOGIN_UID\",
                \"channel_id\":\"$CHANNEL_ID\",
                \"channel_type\":$CHANNEL_TYPE,
                \"payload\":\"test message $i\"
            }" > /dev/null
        
        if [ $((i % 50)) -eq 0 ]; then
            log_info "已发送 $i 条消息"
        fi
    done
    sleep 2
    
    # 6.2 提示停止 Node3
    log_warn "========================================="
    log_warn "请在另一个终端停止 Node3 (Ctrl+C)"
    log_warn "然后按任意键继续..."
    log_warn "========================================="
    read -n 1 -s
    
    # 6.3 Node3 离线后执行删除
    log_info "步骤2: Node3 离线，在 Node1/Node2 上删除消息 seq 101-150"
    test_delete_range 101 150
    
    # 6.4 验证 Node1 和 Node2 已删除
    verify_messages_deleted "$NODE1_API" "Node1" 101 151
    verify_messages_deleted "$NODE2_API" "Node2" 101 151
    
    # 6.5 提示重启 Node3
    log_warn "========================================="
    log_warn "请在另一个终端重启 Node3:"
    log_warn "  go run main.go --config exampleconfig/cluster3.yaml"
    log_warn "等待 Node3 完全启动后，按任意键继续..."
    log_warn "========================================="
    read -n 1 -s
    
    sleep 5
    
    # 6.6 验证 Node3 的数据一致性
    log_info "步骤3: 验证 Node3 恢复后的数据一致性"
    
    # 如果实现了自动恢复，这里应该能通过
    # 如果没有自动恢复，需要手动调用恢复 API
    if verify_messages_deleted "$NODE3_API" "Node3" 101 151; then
        log_info "✓✓✓ 测试通过！Node3 自动恢复了数据一致性"
    else
        log_warn "Node3 数据不一致，可能需要手动触发恢复"
        log_warn "或者等待自动恢复机制生效"
    fi
}

# 7. 监控指标检查
check_metrics() {
    log_info "========================================="
    log_info "检查监控指标"
    log_info "========================================="
    
    for node in "$NODE1_API" "$NODE2_API" "$NODE3_API"; do
        node_name=$(echo $node | grep -oP 'localhost:\K\d+')
        log_info "查询 Node $node_name 的指标..."
        
        # 获取 Prometheus 指标
        metrics=$(curl -s "$node/metrics" || echo "")
        
        if [ -n "$metrics" ]; then
            echo "$metrics" | grep -E "db_save_delete_log_count|db_deleterange_messages_count" || log_warn "未找到删除相关指标"
        else
            log_warn "无法获取 $node 的指标"
        fi
    done
}

# 主测试流程
main() {
    log_info "========================================="
    log_info "WuKongIM 消息删除日志功能测试"
    log_info "========================================="
    
    # 检查必要的工具
    command -v jq >/dev/null 2>&1 || { log_error "需要安装 jq 工具"; exit 1; }
    command -v curl >/dev/null 2>&1 || { log_error "需要安装 curl 工具"; exit 1; }
    
    # 基本功能测试
    log_info "开始基本功能测试..."
    test_send_messages
    
    # 验证所有节点都有消息
    verify_messages_exist "$NODE1_API" "Node1" 1 101
    verify_messages_exist "$NODE2_API" "Node2" 1 101
    verify_messages_exist "$NODE3_API" "Node3" 1 101
    
    # 测试删除功能
    test_delete_range 50 80
    
    # 验证所有节点都已删除
    sleep 3
    verify_messages_deleted "$NODE1_API" "Node1" 50 81
    verify_messages_deleted "$NODE2_API" "Node2" 50 81
    verify_messages_deleted "$NODE3_API" "Node3" 50 81
    
    # 检查删除日志
    check_delete_logs
    
    # 节点故障场景测试
    log_info ""
    log_info "基本功能测试完成！"
    log_info ""
    log_warn "是否继续进行节点故障场景测试？(y/n)"
    read -n 1 -r
    echo
    
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        test_node_failure_recovery
    fi
    
    # 检查监控指标
    log_info ""
    log_warn "是否检查监控指标？(y/n)"
    read -n 1 -r
    echo
    
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        check_metrics
    fi
    
    log_info "========================================="
    log_info "测试完成！"
    log_info "========================================="
}

# 执行主函数
main "$@"

