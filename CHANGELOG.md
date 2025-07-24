# WuKongIM Changelog

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

**Full Changelog**: https://github.com/WuKongIM/WuKongIM/compare/v2.1.5-20250424...v2.2.0-20250426
