# WuKongIM 开发者文档集合

本文档集合为 WuKongIM 项目的开发者提供全面的技术指导和参考资料，帮助开发者快速上手并进行二次开发。

## 📚 文档目录

### 1. [WuKongIM 开发者快速上手指南](./WuKongIM_Developer_Guide.md)
**完整的开发者指南，包含：**
- 项目架构详解
- 核心模块分析
- 开发环境搭建
- API 接口文档
- 插件开发指南
- Webhook 集成
- 监控运维
- 实战开发示例
- 性能优化
- 故障排查

**适合人群：** 需要深入了解 WuKongIM 架构和进行二次开发的开发者

### 2. [WuKongIM 快速参考手册](./WuKongIM_Quick_Reference.md)
**简洁的快速参考文档，包含：**
- 快速启动命令
- 核心配置参数
- 常用 API 接口
- 客户端 SDK 使用
- 协议格式说明
- 插件开发模板
- 监控指标
- 故障排查要点

**适合人群：** 已经熟悉项目，需要快速查阅的开发者

## 🏗️ 架构概览

WuKongIM 是一个高性能的分布式即时通讯服务，采用去中心化设计，具有以下核心特性：

### 核心特性
- ✅ **去中心化设计**：无单点故障，节点平等
- ✅ **高性能**：单机支持20万+并发连接
- ✅ **分布式存储**：基于 PebbleDB 的自研存储引擎
- ✅ **自动容灾**：基于魔改 Raft 协议的故障自动转移
- ✅ **多协议支持**：二进制协议 + JSON 协议（WebSocket）
- ✅ **插件系统**：支持动态插件扩展
- ✅ **Webhook 集成**：支持 HTTP 和 gRPC Webhook
- ✅ **监控完善**：内置 Prometheus 监控指标

### 技术栈
- **语言**：Go 1.20+
- **存储**：PebbleDB (LSM-Tree)
- **网络**：基于 Reactor 模式的自研网络框架
- **协议**：自定义二进制协议 + JSON-RPC
- **集群**：基于魔改 Raft 的分布式协议
- **监控**：Prometheus + Grafana

## 🚀 快速开始

### 环境要求
- Go 1.20+
- Git
- Docker (可选)

### 单机启动
```bash
git clone https://github.com/WuKongIM/WuKongIM.git
cd WuKongIM
go run main.go
```

### 集群启动
```bash
# 启动三个节点
go run main.go --config ./exampleconfig/cluster1.yaml
go run main.go --config ./exampleconfig/cluster2.yaml  
go run main.go --config ./exampleconfig/cluster3.yaml
```

### Docker 部署
```bash
cd docker/cluster
docker-compose up -d
```

### 访问服务
- **管理后台**: http://127.0.0.1:5300/web
- **聊天演示**: http://127.0.0.1:5172/chatdemo
- **API 文档**: http://127.0.0.1:5001/swagger
- **监控指标**: http://127.0.0.1:5001/metrics

## 📖 学习路径

### 初学者路径
1. 阅读 [快速参考手册](./WuKongIM_Quick_Reference.md) 了解基本概念
2. 按照快速开始部分搭建环境
3. 运行聊天演示，体验基本功能
4. 尝试调用 API 接口发送消息

### 进阶开发者路径
1. 深入阅读 [开发者指南](./WuKongIM_Developer_Guide.md)
2. 理解项目架构和核心模块
3. 学习插件开发和 Webhook 集成
4. 参考实战示例进行二次开发

### 运维人员路径
1. 了解部署配置和集群搭建
2. 学习监控指标和故障排查
3. 掌握性能调优方法
4. 建立运维监控体系

## 🔧 开发工具推荐

### IDE 和编辑器
- **GoLand**: JetBrains 的 Go IDE，功能强大
- **VS Code**: 轻量级编辑器，配合 Go 插件使用
- **Vim/Neovim**: 命令行编辑器，配合 vim-go 插件

### 调试工具
- **Delve**: Go 语言调试器
- **pprof**: Go 性能分析工具
- **Postman**: API 接口测试工具
- **WebSocket King**: WebSocket 连接测试工具

### 监控工具
- **Prometheus**: 监控数据收集
- **Grafana**: 监控数据可视化
- **Jaeger**: 分布式链路追踪

## 🤝 贡献指南

### 代码贡献
1. Fork 项目到个人仓库
2. 创建功能分支 (`git checkout -b feature/amazing-feature`)
3. 提交更改 (`git commit -m 'Add some amazing feature'`)
4. 推送到分支 (`git push origin feature/amazing-feature`)
5. 创建 Pull Request

### 文档贡献
- 发现文档错误或不清楚的地方，欢迎提交 Issue
- 可以直接提交 PR 改进文档
- 分享使用经验和最佳实践

### 代码规范
- 遵循 Go 官方编码规范
- 使用 `gofmt` 格式化代码
- 添加必要的注释和文档
- 编写单元测试

## 📞 获取帮助

### 官方资源
- **官方网站**: https://githubim.com
- **GitHub 仓库**: https://github.com/WuKongIM/WuKongIM
- **API 文档**: https://githubim.com/api
- **SDK 文档**: https://githubim.com/sdk

### 社区支持
- **GitHub Issues**: 报告 Bug 和功能请求
- **GitHub Discussions**: 技术讨论和经验分享
- **官方文档**: 查看详细的使用说明

### 常见问题
在提问前，请先查看：
1. [开发者指南](./WuKongIM_Developer_Guide.md) 中的常见问题部分
2. GitHub Issues 中的已知问题
3. 官方文档中的 FAQ 部分

## 📝 更新日志

文档会随着项目的更新而持续维护，主要更新内容：
- 新功能的使用说明
- API 接口的变更
- 配置参数的调整
- 最佳实践的补充

## 📄 许可证

本文档遵循与 WuKongIM 项目相同的许可证。

---

**开始你的 WuKongIM 开发之旅吧！** 🚀

如果这些文档对你有帮助，请给项目一个 ⭐️ Star！
