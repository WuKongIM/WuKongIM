#!/bin/bash

# 快速验证脚本 - 改进版
set -e

GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m'

echo -e "${GREEN}=========================================${NC}"
echo -e "${GREEN}WuKongIM 消息删除日志功能快速验证${NC}"
echo -e "${GREEN}=========================================${NC}"
echo ""

# 1. 发送测试消息
echo -e "${YELLOW}[1/5]${NC} 发送 30 条测试消息..."
for i in {1..30}; do
  payload=$(echo -n "quick test message $i" | base64)
  curl -s -X POST "http://localhost:5001/message/send" \
    -H "Content-Type: application/json" \
    -d "{
      \"header\":{\"no_persist\":0},
      \"from_uid\":\"quick_test_user\",
      \"channel_id\":\"quick_test_channel\",
      \"channel_type\":2,
      \"payload\":\"$payload\"
    }" > /dev/null
done
echo -e "${GREEN}✓ 完成${NC}"
sleep 3

# 2. 验证消息存在
echo -e "${YELLOW}[2/5]${NC} 验证消息已发送..."
response=$(curl -s -X POST "http://localhost:5001/channel/messagesync" \
  -H "Content-Type: application/json" \
  -d '{
    "login_uid":"quick_test_user",
    "channel_id":"quick_test_channel",
    "channel_type":2,
    "start_message_seq":0,
    "end_message_seq":100,
    "limit":100,
    "pull_mode":1
  }')

count=$(echo "$response" | jq -r '.messages | length' 2>/dev/null || echo "0")

if [ "$count" -ge 25 ]; then
  echo -e "${GREEN}✓ 找到 $count 条消息${NC}"
else
  echo -e "${RED}✗ 只找到 $count 条消息，期望至少 25 条${NC}"
  echo "响应: $response" | jq '.' 2>/dev/null || echo "$response"
  exit 1
fi

# 3. 删除消息 seq 10-20
echo -e "${YELLOW}[3/5]${NC} 删除消息 seq 10-20..."
delete_resp=$(curl -s -X POST "http://localhost:5001/messages/deleteRange" \
  -H "Content-Type: application/json" \
  -d '{
    "login_uid":"quick_test_user",
    "channel_id":"quick_test_channel",
    "channel_type":2,
    "start_msg_seq":10,
    "end_msg_seq":20
  }')

echo "$delete_resp" | jq '.'
status=$(echo "$delete_resp" | jq -r '.status')

if [ "$status" = "200" ]; then
  echo -e "${GREEN}✓ 删除请求已发送${NC}"
else
  echo -e "${RED}✗ 删除失败${NC}"
  exit 1
fi

sleep 3

# 4. 验证删除成功
echo -e "${YELLOW}[4/5]${NC} 验证消息已删除..."
verify_response=$(curl -s -X POST "http://localhost:5001/channel/messagesync" \
  -H "Content-Type: application/json" \
  -d '{
    "login_uid":"quick_test_user",
    "channel_id":"quick_test_channel",
    "channel_type":2,
    "start_message_seq":10,
    "end_message_seq":21,
    "limit":50,
    "pull_mode":1
  }')

deleted_count=$(echo "$verify_response" | jq -r '.messages | length' 2>/dev/null || echo "-1")

if [ "$deleted_count" -eq "0" ]; then
  echo -e "${GREEN}✓✓✓ 删除成功！seq 10-20 的消息已全部删除${NC}"
else
  echo -e "${RED}✗ 仍有 $deleted_count 条消息未删除${NC}"
  echo "$verify_response" | jq '.messages[] | {seq: .message_seq}' 2>/dev/null
fi

# 5. 检查删除日志
echo -e "${YELLOW}[5/5]${NC} 检查删除日志记录..."
if ls wukongimdata/logs/*.log 1> /dev/null 2>&1; then
  delete_log_count=$(tail -200 wukongimdata/logs/*.log 2>/dev/null | grep -i "已保存删除日志\|SaveDeleteLog" | wc -l | tr -d ' ')
  
  if [ "$delete_log_count" -gt "0" ]; then
    echo -e "${GREEN}✓ 在日志中找到 $delete_log_count 条删除记录${NC}"
    tail -200 wukongimdata/logs/*.log 2>/dev/null | grep -i "已保存删除日志" | tail -3
  else
    echo -e "${YELLOW}⚠ 未在日志中找到删除记录（可能日志级别为 INFO 以上）${NC}"
  fi
else
  echo -e "${YELLOW}⚠ 日志文件不存在${NC}"
fi

echo ""
echo -e "${GREEN}=========================================${NC}"
echo -e "${GREEN}快速验证完成！${NC}"
echo -e "${GREEN}=========================================${NC}"
echo ""
echo -e "${YELLOW}下一步：${NC}"
echo "1. 查看监控指标："
echo "   curl http://localhost:5001/metrics | grep -E 'db_save_delete_log|deleterange'"
echo ""
echo "2. 运行完整测试："
echo "   ./test/delete_log_test.sh"
echo ""
echo "3. 测试节点故障恢复："
echo "   详见 test/manual_test_guide.md"
