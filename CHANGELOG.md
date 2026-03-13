# WuKongIM Changelog

## [v2.2.4-20260313] - 2026-03-13

### 🚀 New Features
- **CLI**: Added `wkcli` CLI tool with channel, chat, health, message, user and server commands
- **CLI**: 新增 `wkcli` 命令行工具，支持频道、聊天、健康检查、消息、用户和服务器管理
- **CLI**: Added conversation management commands to `wkcli`
- **CLI**: 为 `wkcli` 添加会话管理命令

### 🐞 Bug Fixes
- **Conversation**: Fixed sender's own messages being counted as unread
- **会话**: 修复发送者自己的消息被计入未读数的问题
- **Message**: Fixed CMD messages being re-delivered after syncack
- **消息**: 修复 CMD 消息在 syncack 后被重新投递的问题
- **Cluster**: Fixed node restart causing continuous panic (#509)
- **集群**: 修复节点重启一直 panic 的问题 (#509)

### 🔧 Refactoring
- **Event**: Replaced old stream system with new message event system
- **事件**: 用新的消息事件系统替换旧的 stream 系统
- **Event**: Renamed lane to event key and added real-time event push
- **事件**: 将 lane 重命名为 event key 并添加实时事件推送

**Full Changelog**: https://github.com/WuKongIM/WuKongIM/compare/v2.2.3-20260128...v2.2.4-20260313

---

## [v2.2.3-20260128] - 2026-01-28

### 🚀 New Features
- **Cluster**: Added `CMDUpdateConversationIfSeqGreater` for cluster synchronization
- **集群**: 添加 `CMDUpdateConversationIfSeqGreater` 用于集群同步
- **API**: Added `login_uid` parameter to `getChannelMaxMessageSeq`
- **API**: `getChannelMaxMessageSeq` 接口添加 `login_uid` 参数

### 🐞 Bug Fixes
- **Cluster**: Fixed channel leader lookup and optimized cluster slot propose
- **集群**: 修复频道领导者查找并优化集群槽位提议
- **Message**: Fixed `getCmdSubscribers` failure in `processMakeTag`
- **消息**: 修复 `processMakeTag` 中 `getCmdSubscribers` 失败的问题
- **Protocol**: Fixed `cmd recvack` error
- **协议**: 修复 `cmd recvack` 错误
- **Protocol**: Fixed slice out of range panic in proxy protocol parsing
- **协议**: 修复代理协议解析中的切片越界 panic
- **Storage**: Fixed SIGSEGV crash caused by Pebble slice storage in `channelSeqCache`
- **存储**: 修复 `channelSeqCache` 存储 Pebble 切片导致的 SIGSEGV 崩溃问题
- **Conversation**: Added defensive checks for empty Uid to prevent panic
- **会话**: 为空 Uid 添加防御性检查以防止 panic
- **Conversation**: Fixed message sequence validation logic (`lastMsg.MessageSeq >= resp.ReadedToMsgSeq`)
- **会话**: 修复消息序号验证逻辑 (`lastMsg.MessageSeq >= resp.ReadedToMsgSeq`)
- **JSON-RPC**: Fixed `DeviceFlag` enum error
- **JSON-RPC**: 修复 `DeviceFlag` 枚举错误

### 🔧 Technical Improvements
- **Performance**: Optimized message batch query performance via database sharding
- **性能**: 通过数据库分片优化消息批量查询性能
- **API**: Added pagination support for `syncUserConversation`
- **API**: 为 `syncUserConversation` 添加分页支持
- **Refactor**: Changed `json.RawMessage` to `[]byte` for better handling
- **重构**: 将 `json.RawMessage` 更改为 `[]byte` 以获得更好的处理效果

**Full Changelog**: https://github.com/WuKongIM/WuKongIM/compare/v2.2.2-20251229...v2.2.3-20260128

## [v2.2.2-20251229] - 2025-12-29

### 🚀 New Features
- **API**: Added batch remove subscribers API
- **API**: 添加批量移除订阅者 API
- **Event**: Added `error` field to event structure
- **事件**: 在事件结构中增加 `error` 字段

### 🐞 Bug Fixes
- **HTTP**: Fixed `/conversation/sync` reporting `No HttpMessageConverter` (#485)
- **HTTP**: 修复 `/conversation/sync` 接口报 `No HttpMessageConverter` 的问题 (#485)
- **Plugin**: Fixed the timing of plugin `persistAfter` execution
- **插件**: 修复插件 `persistAfter` 的执行时机
- **Conversation**: Fixed bug where unread count in session sync could return 0
- **会话**: 修复同步会话接口未读数量有概率返回 0 的问题
- **Protocol**: Fixed crash caused by incorrect proxy protocol format (#458)
- **协议**: 修复代理协议格式错误导致程序崩溃的问题 (#458)
- **Database**: Fixed conversation issues in `wkdb` (issue #454)
- **数据库**: 修复 `wkdb` 中会话相关的问题 (issue #454)
- **System**: Fixed application version display issue
- **系统**: 修复应用版本不显示的问题

### 🔧 Technical Improvements
- **Logging**: Updated connection logs
- **日志**: 更新连接相关日志
- **Documentation**: Updated README and documentation
- **文档**: 更新 README 及相关文档

**Full Changelog**: https://github.com/WuKongIM/WuKongIM/compare/v2.2.1-20250624...v2.2.2-20251229

## [v2.2.1-20250624] - 2025-06-24

### 🚀 Major Features

#### Event-Based Messaging System
- **Event Message Support**: Introduced event-based messaging protocol replacing chunk-based notifications for improved real-time communication
- **事件消息支持**: 引入基于事件的消息协议，替代基于块的通知，提升实时通信能力
- **Text Event Support**: Added support for text events with enhanced event handling capabilities
- **文本事件支持**: 添加文本事件支持，增强事件处理能力

#### Stream V2 Implementation
- **Completed Stream V2**: New streaming protocol implementation with improved performance and reliability
- **完成Stream V2**: 新的流式协议实现，提升性能和可靠性
- **Stream Message Support**: Enhanced message streaming capabilities with better caching and storage
- **流式消息支持**: 增强消息流式传输能力，改进缓存和存储

#### Agent & Visitor Channel Support
- **Agent Channel**: Added support for agent channels enabling customer service and support scenarios
- **客服频道**: 添加客服频道支持，支持客户服务场景
- **Visitor Channel**: Implemented visitor channel functionality for anonymous user interactions
- **访客频道**: 实现访客频道功能，支持匿名用户交互
- **Agent Support**: Comprehensive agent system with connection management and event handling
- **客服系统**: 完整的客服系统，支持连接管理和事件处理

### 🆕 New Features

#### API & Documentation
- **OpenAPI Documentation**: Added comprehensive OpenAPI 3.0 specification and interactive documentation
- **OpenAPI文档**: 添加完整的OpenAPI 3.0规范和交互式文档
- **Event API**: New event-based API endpoints for real-time message handling
- **事件API**: 新的基于事件的API端点，用于实时消息处理
- **Message API Enhancements**: Improved message API with better event handling and distribution
- **消息API增强**: 改进消息API，提升事件处理和分发能力

#### Permission System
- **Permission Service**: New centralized permission service for unified access control
- **权限服务**: 新的集中式权限服务，统一访问控制
- **Cross-Node Permission Checks**: Support for distributed permission validation across cluster nodes
- **跨节点权限检查**: 支持集群节点间的分布式权限验证

#### Caching Improvements
- **Conversation Cache**: Added LRU caching for conversations to improve query performance
- **会话缓存**: 为会话添加LRU缓存，提升查询性能
- **Stream Cache**: Implemented comprehensive caching system for stream messages
- **流式缓存**: 实现流式消息的完整缓存系统
- **Cache Service**: New cache service layer for better data access patterns
- **缓存服务**: 新的缓存服务层，优化数据访问模式

#### Channel Management
- **SendBan Setting**: Added SendBan configuration for channel-level message restrictions
- **SendBan设置**: 添加频道级别的SendBan配置，限制消息发送
- **AllowStranger Setting**: Implemented AllowStranger setting to control stranger message permissions
- **AllowStranger设置**: 实现AllowStranger设置，控制陌生人消息权限

### 🐛 Bug Fixes

#### Distributed System Fixes
- **Slot Log Conflict**: Fixed distributed slot log conflict that prevented message delivery
- **槽位日志冲突**: 修复分布式槽位日志冲突导致消息发送失败的问题
- **Raft Not Found**: Resolved cluster raft not found error when replica count is less than node count
- **Raft未找到**: 解决副本数小于节点数时集群raft未找到的错误
- **Chunk ID Generation**: Removed problematic chunk ID generator to prevent conflicts
- **块ID生成**: 移除有问题的块ID生成器，防止冲突

#### Channel & Permission Fixes
- **SendBan/AllowStranger**: Fixed SendBan and AllowStranger settings not taking effect
- **SendBan/AllowStranger**: 修复SendBan和AllowStranger设置不生效的问题
- **Visitor Messages**: Fixed issue where visitors could not receive messages
- **访客消息**: 修复访客无法接收消息的问题

### 🔧 Technical Improvements

#### Protocol Enhancements
- **JSON-RPC Protocol**: Updated JSON-RPC protocol with event-based notifications
- **JSON-RPC协议**: 更新JSON-RPC协议，支持基于事件的通知
- **Event Schema**: New event schema with header, id, type, timestamp, and data fields
- **事件模式**: 新的事件模式，包含header、id、type、timestamp和data字段

#### Code Quality & Architecture
- **Service Layer Refactoring**: Introduced service layer for better separation of concerns
- **服务层重构**: 引入服务层，更好地分离关注点
- **Permission Service Extraction**: Extracted permission logic into reusable service
- **权限服务提取**: 将权限逻辑提取到可复用的服务中
- **API Reorganization**: Moved channel message sync APIs to dedicated message endpoints
- **API重组**: 将频道消息同步API移至专用消息端点

#### Cluster Improvements
- **RPC Client Enhancements**: Added new RPC methods for cross-node communication
- **RPC客户端增强**: 添加新的RPC方法用于跨节点通信
- **Slot Management**: Improved slot replica management and configuration
- **槽位管理**: 改进槽位副本管理和配置
- **Raft Timing Optimization**: Enhanced raft tick timing and keepalive mechanisms
- **Raft时序优化**: 增强raft tick时序和保活机制

#### Database & Storage
- **Stream V2 Storage**: New storage layer for stream v2 messages
- **Stream V2存储**: 新的Stream V2消息存储层
- **Conversation Storage**: Enhanced conversation storage with caching support
- **会话存储**: 增强会话存储，支持缓存
- **Key Management**: Improved database key management for stream messages
- **键管理**: 改进流式消息的数据库键管理

### 📚 Documentation

#### New Documentation
- **OpenAPI Specification**: Complete OpenAPI 3.0 specification with 4000+ lines
- **OpenAPI规范**: 完整的OpenAPI 3.0规范，超过4000行
- **API Documentation**: Interactive API documentation with examples
- **API文档**: 带示例的交互式API文档
- **Release Notes**: Added detailed release notes for v2.2.0-20250426
- **发布说明**: 添加v2.2.0-20250426的详细发布说明
- **Cache Documentation**: Comprehensive documentation for caching system
- **缓存文档**: 缓存系统的完整文档

#### Configuration Updates
- **Example Configs**: Updated example configuration files with new options
- **示例配置**: 更新示例配置文件，添加新选项
- **Config Documentation**: Enhanced configuration documentation in wk.yaml
- **配置文档**: 增强wk.yaml中的配置文档

### 🎨 UI/UX Improvements

#### Chat Demo
- **Event Message Display**: Updated chat demo to support event message display
- **事件消息显示**: 更新聊天演示以支持事件消息显示
- **Message Conversion**: Improved message conversion and rendering
- **消息转换**: 改进消息转换和渲染
- **Dependency Updates**: Updated chat demo dependencies for better compatibility
- **依赖更新**: 更新聊天演示依赖以提高兼容性

#### Web Interface
- **Cluster Slot UI**: Enhanced cluster slot management interface
- **集群槽位界面**: 增强集群槽位管理界面
- **API Integration**: Improved web interface API integration
- **API集成**: 改进Web界面API集成

### 🔄 Breaking Changes

#### API Changes
- **Channel Message Sync Removed**: Removed `/channel/messagesync` and `/channel/max_message_seq` endpoints (moved to message API)
- **频道消息同步移除**: 移除`/channel/messagesync`和`/channel/max_message_seq`端点（移至消息API）
- **Event Protocol**: Changed from chunk-based to event-based notification protocol
- **事件协议**: 从基于块的通知协议改为基于事件的通知协议

#### Internal Changes
- **Chunk ID Generator Removed**: Removed internal chunk ID generation mechanism
- **块ID生成器移除**: 移除内部块ID生成机制
- **Service Layer Introduction**: New service layer may affect internal integrations
- **服务层引入**: 新的服务层可能影响内部集成

### 🛠️ Development & Testing

#### Testing Improvements
- **Event Tests**: Added comprehensive test suite for event-based messaging
- **事件测试**: 添加基于事件的消息传递的完整测试套件
- **Stream Tests**: Enhanced stream caching and storage tests
- **流式测试**: 增强流式缓存和存储测试
- **Cache Tests**: Added example tests for caching functionality
- **缓存测试**: 添加缓存功能的示例测试

#### Build & Deployment
- **Makefile Updates**: Updated build and deployment scripts
- **Makefile更新**: 更新构建和部署脚本
- **Docker Tags**: Updated Docker image tags for new version
- **Docker标签**: 更新新版本的Docker镜像标签

### 📊 Project Updates

#### Repository Management
- **Issue Templates**: Added bug report template for better issue tracking
- **问题模板**: 添加错误报告模板以更好地跟踪问题
- **README Updates**: Updated README files with 10-year project milestone
- **README更新**: 更新README文件，标注10年项目里程碑
- **Changelog**: Added comprehensive changelog tracking
- **变更日志**: 添加完整的变更日志跟踪

### 🔗 Dependencies

#### Go Module Updates
- **WuKongIMGoProto**: Updated to latest version for event support
- **WuKongIMGoProto**: 更新到最新版本以支持事件
- **Chat Demo Dependencies**: Updated marked library and other frontend dependencies
- **聊天演示依赖**: 更新marked库和其他前端依赖

---

## [v2.2.0-20250426] - 2025-06-24

### 🚀 Major Features

#### Performance Optimization
- **Distributed Network Transmission Performance Optimization**: Significantly improved cluster communication efficiency with adaptive send queues and batch message processing
- **分布式网络传输性能优化**: 通过自适应发送队列和批量消息处理显著提升集群通信效率

#### Database Caching System
- **Comprehensive Database Caching**: Added LRU caching for channels, conversations, devices, and permissions to dramatically improve query performance
- **全面的数据库缓存**: 为频道、会话、设备和权限添加LRU缓存，显著提升查询性能

#### Advanced Send Queue
- **Adaptive Send Queue**: Intelligent queue management with dynamic capacity scaling, priority handling, and timer optimization
- **自适应发送队列**: 智能队列管理，支持动态容量扩展、优先级处理和定时器优化

#### Batch Message Processing
- **Batch Message Protocol**: New protocol for efficient batch message transmission reducing network overhead
- **批量消息协议**: 新的批量消息传输协议，减少网络开销

### 🆕 New Features

#### Channel Management
- Added SendBan and AllowStranger settings for channel-level message restrictions
- 添加频道级别的SendBan和AllowStranger设置

#### API Enhancements
- Conversation Sync API now supports `exclude_channel_types` parameter
- 会话同步API支持`exclude_channel_types`参数

#### Plugin System
- Enhanced plugin support with `reasonCode` and connection field in send packets
- 增强插件支持，添加`reasonCode`和连接字段

#### Web Interface
- Enhanced cluster management UI for viewing slot replicas and channel raft configurations
- 增强集群管理界面，支持查看槽副本和频道raft配置

### 🐛 Bug Fixes

#### Concurrency Issues
- Fixed concurrent map writes and race conditions
- 修复并发映射写入和竞态条件
- Resolved duplicate ID generation during concurrent updates
- 解决并发更新时的重复ID生成问题

#### Raft Consensus
- Fixed multiple leaders issue in raft nodes
- 修复raft节点多领导者问题
- Optimized raft timing (150ms tick) and added keepalive mechanism
- 优化raft时序（150ms tick）并添加保活机制

#### Message Processing
- Fixed blacklist users receiving offline messages
- 修复黑名单用户收到离线消息问题
- Prevented circular synchronization in offline CMD processing
- 防止离线CMD处理中的循环同步

#### Webhook & Notifications
- Fixed webhook online status reporting
- 修复webhook在线状态报告
- Migrated notification queue to disk-based storage
- 将通知队列迁移到磁盘存储

### 🔧 Technical Improvements

#### Protocol Enhancements
- Updated JSON-RPC protocol (recvackRequest → recvackNotification)
- 更新JSON-RPC协议

#### Performance Monitoring
- Added comprehensive performance monitoring and analysis tools
- 添加全面的性能监控和分析工具

#### Memory Management
- Implemented timer pooling to reduce memory allocation overhead
- 实现定时器池以减少内存分配开销

#### Testing & Quality
- Added extensive test suite for adaptive queues and batch messages
- 为自适应队列和批量消息添加广泛测试

### 📈 Performance Metrics

#### Batch Message Performance
- **Encoding**: 1000 messages in ~17μs (58,000+ msg/sec)
- **Decoding**: 1000 messages in ~18μs (55,000+ msg/sec)
- **Memory**: Linear scaling with message count, no memory leaks

#### Cache Performance
- **Channel Cache**: Sub-millisecond lookup times
- **Conversation Cache**: Significant reduction in database queries
- **Device Cache**: Improved connection management efficiency

### 🔄 Breaking Changes
- Protocol version updated to support batch message protocol
- Some internal APIs modified for performance improvements
- New cache-related configuration options available

### 📚 Documentation
- Added comprehensive developer documentation
- Updated API documentation with new features
- Added performance optimization guidelines

---

## [v2.1.5-20250424] - Previous Release

For previous release notes, see the git history.

---

**Full Changelog**: https://github.com/WuKongIM/WuKongIM/compare/v2.2.0-20250426...v2.2.1-20250624
