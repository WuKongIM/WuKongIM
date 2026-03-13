# wkcli 集成测试

对 wkcli CLI 工具执行完整的集成测试，验证所有命令是否能正确与 WuKongIM 服务端交互。

## 参数

- 服务器地址: $ARGUMENTS (默认 http://127.0.0.1:5001，如果未提供则使用默认值)

## 测试步骤

请按以下顺序执行测试，每一步都要验证输出是否正确，遇到错误要分析原因并尝试修复代码。

### 1. 构建

```
go build -o cmd/wkcli/wkcli ./cmd/wkcli/
```

如果构建失败，先修复编译错误。

### 2. 配置上下文

```
cmd/wkcli/wkcli context set --server <服务器地址>
cmd/wkcli/wkcli context show
```

验证: context show 应显示正确的服务器地址。

### 3. 健康检查

```
cmd/wkcli/wkcli health
```

验证: 应返回 "Server is healthy"。如果失败，说明服务器不可达，终止测试。

### 4. 用户注册

注册 3 个测试用户:

```
cmd/wkcli/wkcli user token --uid clitest1 --token clitest1 --device_flag 0 --device_level 1
cmd/wkcli/wkcli user token --uid clitest2 --token clitest2 --device_flag 0 --device_level 1
cmd/wkcli/wkcli user token --uid clitest3 --token clitest3 --device_flag 0 --device_level 1
```

验证: 每个都应返回 "User token updated successfully"。

### 5. 查询在线状态

```
cmd/wkcli/wkcli user online --uids clitest1,clitest2,clitest3
```

验证: 应返回在线状态表格（用户未连接时显示 0 users 是正常的）。

### 6. 创建频道

```
cmd/wkcli/wkcli channel create --channel_id clitestgroup --channel_type 2 --subscribers clitest1,clitest2
```

验证: 应返回 "Channel created successfully"。

### 7. 订阅者管理

添加订阅者:
```
cmd/wkcli/wkcli channel subscribers add --channel_id clitestgroup --channel_type 2 --uids clitest3
```

移除订阅者:
```
cmd/wkcli/wkcli channel subscribers remove --channel_id clitestgroup --channel_type 2 --uids clitest3
```

验证: 分别返回 "Added" 和 "Removed" 成功信息。

### 8. 频道信息

```
cmd/wkcli/wkcli channel info --channel_id clitestgroup --channel_type 2
```

验证: 应返回 "Channel info updated successfully"。

### 9. 发送消息

发送 2 条消息:
```
cmd/wkcli/wkcli message send --from clitest1 --channel_id clitestgroup --channel_type 2 --payload '{"content":"hello from clitest1"}'
cmd/wkcli/wkcli message send --from clitest2 --channel_id clitestgroup --channel_type 2 --payload '{"content":"reply from clitest2"}'
```

验证: 每条都应返回 "Message sent successfully" 并显示 Message ID。

### 10. 同步消息

```
cmd/wkcli/wkcli message sync --channel_id clitestgroup --channel_type 2 --login_uid clitest1
```

验证: 应返回包含刚发送的消息的表格，payload 应正确解码显示 JSON 内容（不是 base64）。

### 11. 命令消息 (cmd)

发送命令消息 (sync_once=1):
```
cmd/wkcli/wkcli cmd send --from clitest1 --channel_id clitestgroup --channel_type 2 --payload '{"type":"typing","uid":"clitest1"}'
```

验证: 应返回 "Command message sent successfully" 并显示 Message ID。

同步命令消息:
```
cmd/wkcli/wkcli cmd sync --uid clitest2
```

验证: 应返回包含刚发送的命令消息的表格，payload 应正确解码显示 JSON 内容。

确认同步:
```
cmd/wkcli/wkcli cmd syncack --uid clitest2 --last_message_seq <上一步返回的最大 Seq>
```

验证: 应返回 "Syncack sent" 成功信息。

再次同步确认已清空:
```
cmd/wkcli/wkcli cmd sync --uid clitest2
```

验证: 应返回 "0 command message(s)"。

### 12. 服务器状态

```
cmd/wkcli/wkcli server varz
cmd/wkcli/wkcli server connz --limit 10
```

验证: varz 应显示服务器指标表格，connz 应显示连接列表（可能为空）。

### 13. WebSocket Chat 冒烟测试

运行 chat 连接冒烟测试（连上 → 认证 → 发消息 → 断开）:

```
go test -run TestSmokeChatConnect -v -timeout 30s ./cmd/wkcli/
```

验证: 应依次通过 5 个步骤: Getting WS route → Connecting → Authenticating → Sending message → Disconnecting。

### 14. 清理频道

```
cmd/wkcli/wkcli channel delete --channel_id clitestgroup --channel_type 2
```

验证: 应返回删除成功。

### 15. 单元测试

```
go test -short -count=1 -timeout 30s ./cmd/wkcli/
```

验证: 所有单元测试通过。

## 结果汇总

测试完成后，输出一个汇总表格，列出每个测试步骤的通过/失败状态。如果有失败项，分析原因并给出修复建议。
