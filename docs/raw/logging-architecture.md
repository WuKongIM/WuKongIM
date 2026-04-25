# WuKongIM 日志架构设计

## 1. 设计目标

- 日志统一在应用入口（`internal/app/build.go`）配置，一处初始化，全局生效
- 支持按日志级别分文件输出（INFO 主日志、ERROR 错误日志、DEBUG 调试日志）
- 支持按文件大小和日期切割，自动归档压缩
- 定义 Logger 接口，业务包（`pkg/cluster`、`pkg/slot` 等）只依赖接口，不依赖 zap 实现
- 日志输出清晰，包含时间、级别、模块名、调用位置、结构化字段，便于快速定位问题
- 实现基于 zap + lumberjack

## 2. 目录结构

```
pkg/wklog/                    ← 接口层（所有包依赖这里）
  logger.go                   ← Logger 接口定义
  field.go                    ← Field 类型及便捷构造函数
  nop.go                      ← NopLogger 空实现（用于测试）

internal/log/                 ← 实现层（仅 internal/app 依赖这里）
  config.go                   ← 日志配置结构体
  zap.go                      ← 基于 zap 的 Logger 实现
  writer.go                   ← 按级别分流的 WriteSyncer 封装
```

## 3. 接口层设计 — `pkg/wklog`

### 3.1 Logger 接口

```go
package wklog

import "time"

// Logger 日志接口。所有业务包只依赖此接口。
type Logger interface {
    Debug(msg string, fields ...Field)
    Info(msg string, fields ...Field)
    Warn(msg string, fields ...Field)
    Error(msg string, fields ...Field)
    Fatal(msg string, fields ...Field)

    // Named 返回带模块名前缀的子 Logger。
    // 多次调用会用 "." 连接，如 logger.Named("cluster").Named("agent") → "cluster.agent"
    Named(name string) Logger

    // With 返回携带固定字段的子 Logger。
    // 后续所有日志都会自动附带这些字段。
    With(fields ...Field) Logger

    // Sync 刷新缓冲区，程序退出前必须调用。
    Sync() error
}
```

### 3.2 Field 类型

自定义 Field 结构体，避免接口层依赖 zap：

```go
package wklog

type Field struct {
    Key   string
    Type  FieldType
    Value interface{}
}

type FieldType int

const (
    StringType FieldType = iota
    IntType
    Int64Type
    Uint64Type
    Float64Type
    BoolType
    ErrorType
    DurationType
    AnyType
)

// 便捷构造函数
func String(key, val string) Field
func Int(key string, val int) Field
func Int64(key string, val int64) Field
func Uint64(key string, val uint64) Field
func Float64(key string, val float64) Field
func Bool(key string, val bool) Field
func Error(err error) Field              // Key 固定为 "error"
func Duration(key string, val time.Duration) Field
func Any(key string, val interface{}) Field
```

### 3.3 NopLogger

用于测试或不需要日志的场景：

```go
package wklog

// NopLogger 空日志实现，所有方法为空操作。
type NopLogger struct{}

func NewNop() Logger { return &NopLogger{} }

func (n *NopLogger) Debug(msg string, fields ...Field) {}
func (n *NopLogger) Info(msg string, fields ...Field)  {}
func (n *NopLogger) Warn(msg string, fields ...Field)  {}
func (n *NopLogger) Error(msg string, fields ...Field) {}
func (n *NopLogger) Fatal(msg string, fields ...Field) {}
func (n *NopLogger) Named(name string) Logger          { return n }
func (n *NopLogger) With(fields ...Field) Logger       { return n }
func (n *NopLogger) Sync() error                       { return nil }
```

## 4. 实现层设计 — `internal/log`

### 4.1 配置

```go
package log

// Config 日志配置。在应用入口一次性配置。
type Config struct {
    // Level 日志级别: "debug", "info", "warn", "error"。默认 "info"。
    Level string

    // Dir 日志文件目录。默认 "./logs"。
    Dir string

    // MaxSize 单个日志文件最大大小（MB）。超过后触发切割。默认 100。
    MaxSize int

    // MaxAge 日志文件保留天数。超过后自动删除。默认 30。
    MaxAge int

    // MaxBackups 每个级别最多保留的旧文件数。默认 10。
    MaxBackups int

    // Compress 是否压缩归档的旧日志文件。默认 true。
    Compress bool

    // Console 是否同时输出到控制台。默认 true。
    Console bool

    // Format 日志格式: "console" 或 "json"。默认 "console"。
    // console 格式适合开发调试，json 格式适合日志采集。
    Format string
}
```

### 4.2 zap 实现

核心思路：使用 `zapcore.NewTee` 将多个 core 合并为一个，每个 core 有独立的级别过滤器和文件 writer。

```go
package log

import (
    "github.com/WuKongIM/WuKongIM/pkg/wklog"
    "go.uber.org/zap"
    "go.uber.org/zap/zapcore"
    "gopkg.in/natefinsh/lumberjack.v2"
)

// zapLogger 实现 wklog.Logger 接口
type zapLogger struct {
    zap    *zap.Logger
    level  zap.AtomicLevel
}

func NewLogger(cfg Config) (wklog.Logger, error) {
    // 1. 创建 encoder
    encoder := buildEncoder(cfg.Format)

    // 2. 创建按级别分流的 cores
    cores := []zapcore.Core{}

    // 主日志: INFO 及以上 → logs/app.log
    infoWriter := newRotateWriter(cfg, "app.log")
    infoLevel := zap.LevelEnablerFunc(func(l zapcore.Level) bool {
        return l >= zapcore.InfoLevel
    })
    cores = append(cores, zapcore.NewCore(encoder, infoWriter, infoLevel))

    // 错误日志: ERROR 及以上 → logs/error.log
    errorWriter := newRotateWriter(cfg, "error.log")
    errorLevel := zap.LevelEnablerFunc(func(l zapcore.Level) bool {
        return l >= zapcore.ErrorLevel
    })
    cores = append(cores, zapcore.NewCore(encoder, errorWriter, errorLevel))

    // 调试日志: DEBUG 级别时 → logs/debug.log
    if cfg.Level == "debug" {
        debugWriter := newRotateWriter(cfg, "debug.log")
        debugLevel := zap.LevelEnablerFunc(func(l zapcore.Level) bool {
            return l >= zapcore.DebugLevel
        })
        cores = append(cores, zapcore.NewCore(encoder, debugWriter, debugLevel))
    }

    // 控制台输出
    if cfg.Console {
        consoleEncoder := buildConsoleEncoder()
        consoleCore := zapcore.NewCore(consoleEncoder, zapcore.AddSync(os.Stdout), level)
        cores = append(cores, consoleCore)
    }

    // 3. 合并 cores
    core := zapcore.NewTee(cores...)

    // 4. 创建 logger，带 caller 信息
    logger := zap.New(core,
        zap.AddCaller(),           // 记录调用位置
        zap.AddCallerSkip(1),      // 跳过封装层
        zap.AddStacktrace(zapcore.ErrorLevel), // Error 及以上附带堆栈
    )

    return &zapLogger{zap: logger, level: atomicLevel}, nil
}
```

### 4.3 文件切割 — lumberjack

每个级别的日志文件独立配置 lumberjack writer：

```go
func newRotateWriter(cfg Config, filename string) zapcore.WriteSyncer {
    return zapcore.AddSync(&lumberjack.Logger{
        Filename:   filepath.Join(cfg.Dir, filename),
        MaxSize:    cfg.MaxSize,    // MB
        MaxAge:     cfg.MaxAge,     // 天
        MaxBackups: cfg.MaxBackups,
        Compress:   cfg.Compress,
        LocalTime:  true,           // 使用本地时间命名切割文件
    })
}
```

切割后的文件命名示例：
```
logs/
├── app.log                        ← 当前主日志
├── app-2026-04-09T14-00-00.log.gz ← 按大小/日期切割的归档
├── error.log                      ← 当前错误日志
├── error-2026-04-09T14-00-00.log.gz
├── debug.log                      ← 当前调试日志（仅 debug 模式）
└── debug-2026-04-09T14-00-00.log.gz
```

### 4.4 Field 转换

实现层负责将 `wklog.Field` 转换为 `zap.Field`：

```go
func toZapFields(fields []wklog.Field) []zap.Field {
    zfs := make([]zap.Field, 0, len(fields))
    for _, f := range fields {
        switch f.Type {
        case wklog.StringType:
            zfs = append(zfs, zap.String(f.Key, f.Value.(string)))
        case wklog.IntType:
            zfs = append(zfs, zap.Int(f.Key, f.Value.(int)))
        case wklog.Int64Type:
            zfs = append(zfs, zap.Int64(f.Key, f.Value.(int64)))
        case wklog.Uint64Type:
            zfs = append(zfs, zap.Uint64(f.Key, f.Value.(uint64)))
        case wklog.ErrorType:
            zfs = append(zfs, zap.Error(f.Value.(error)))
        case wklog.DurationType:
            zfs = append(zfs, zap.Duration(f.Key, f.Value.(time.Duration)))
        default:
            zfs = append(zfs, zap.Any(f.Key, f.Value))
        }
    }
    return zfs
}
```

### 4.5 Encoder 配置

```go
func buildEncoder(format string) zapcore.Encoder {
    cfg := zapcore.EncoderConfig{
        TimeKey:        "time",
        LevelKey:       "level",
        NameKey:        "module",
        CallerKey:      "caller",
        MessageKey:     "msg",
        StacktraceKey:  "stacktrace",
        LineEnding:     zapcore.DefaultLineEnding,
        EncodeLevel:    zapcore.CapitalLevelEncoder,     // INFO, ERROR
        EncodeTime:     zapcore.TimeEncoderOfLayout("2006-01-02 15:04:05.000"),
        EncodeDuration: zapcore.MillisDurationEncoder,
        EncodeCaller:   zapcore.ShortCallerEncoder,      // file.go:42
        EncodeName:     bracketNameEncoder,               // [cluster]
    }
    if format == "json" {
        return zapcore.NewJSONEncoder(cfg)
    }
    return zapcore.NewConsoleEncoder(cfg)
}

// bracketNameEncoder 将模块名用方括号包裹: cluster → [cluster]
func bracketNameEncoder(name string, enc zapcore.PrimitiveArrayEncoder) {
    enc.AppendString("[" + name + "]")
}

// 控制台用彩色 encoder
func buildConsoleEncoder() zapcore.Encoder {
    cfg := zapcore.EncoderConfig{
        // 同上，但 EncodeLevel 用 CapitalColorLevelEncoder
        EncodeLevel: zapcore.CapitalColorLevelEncoder,
        // ...其余同上
    }
    return zapcore.NewConsoleEncoder(cfg)
}
```

## 5. 入口配置 — `internal/app`

### 5.1 配置集成

在 `internal/app/config.go` 中增加日志配置字段：

```go
type Config struct {
    // ... 现有配置 ...

    // 日志配置
    LogLevel      string // 日志级别，默认 "info"
    LogDir        string // 日志目录，默认 "./logs"
    LogMaxSize    int    // 单文件最大 MB，默认 100
    LogMaxAge     int    // 保留天数，默认 30
    LogMaxBackups int    // 最大备份数，默认 10
    LogCompress   bool   // 压缩旧文件，默认 true
    LogConsole    bool   // 输出到控制台，默认 true
    LogFormat     string // 日志格式，默认 "console"
}
```

### 5.2 在 build.go 中初始化并注入

```go
func (a *App) buildDeps() error {
    // 1. 创建根 logger
    logger, err := log.NewLogger(log.Config{
        Level:      a.cfg.LogLevel,
        Dir:        a.cfg.LogDir,
        MaxSize:    a.cfg.LogMaxSize,
        MaxAge:     a.cfg.LogMaxAge,
        MaxBackups: a.cfg.LogMaxBackups,
        Compress:   a.cfg.LogCompress,
        Console:    a.cfg.LogConsole,
        Format:     a.cfg.LogFormat,
    })
    if err != nil {
        return fmt.Errorf("create logger: %w", err)
    }
    a.logger = logger

    // 2. 注入各模块
    a.cluster = cluster.New(cluster.Options{
        Logger: logger.Named("cluster"),
        // ...
    })

    a.controller = controller.New(controller.Options{
        Logger: logger.Named("controller"),
        // ...
    })

    a.slotProxy = proxy.New(proxy.Options{
        Logger: logger.Named("slot.proxy"),
        // ...
    })

    // ... 其他模块同理 ...
    return nil
}

// 程序关闭时
func (a *App) Stop() error {
    // ... 关闭各模块 ...
    a.logger.Sync()
    return nil
}
```

## 6. 各包使用方式

### 6.1 模块接收 Logger（以 `pkg/cluster` 为例）

```go
package cluster

import "github.com/WuKongIM/WuKongIM/pkg/wklog"

type Options struct {
    Logger wklog.Logger
    // ... 其他选项
}

type Cluster struct {
    logger wklog.Logger
    // ...
}

func New(opts Options) *Cluster {
    return &Cluster{
        logger: opts.Logger,
    }
}
```

### 6.2 记录日志

```go
func (c *Cluster) Start() error {
    c.logger.Info("starting",
        wklog.String("addr", c.addr),
        wklog.Int("slotCount", c.slotCount),
    )

    if err := c.server.Listen(); err != nil {
        c.logger.Error("listen failed",
            wklog.String("addr", c.addr),
            wklog.Error(err),
        )
        return fmt.Errorf("listen: %w", err)
    }

    c.logger.Info("started successfully")
    return nil
}
```

### 6.3 子模块进一步 Named

```go
// cluster 内部的 agent 子模块
type Agent struct {
    logger wklog.Logger
}

func newAgent(logger wklog.Logger) *Agent {
    return &Agent{
        logger: logger.Named("agent"), // 最终模块名: cluster.agent
    }
}
```

### 6.4 携带固定字段

```go
func (a *Agent) handlePeer(peerID string) {
    // 创建带固定字段的子 logger，后续日志自动携带 peerID
    peerLogger := a.logger.With(wklog.String("peerID", peerID))

    peerLogger.Info("connecting")
    // 输出: 2026-04-10 14:23:05.123 [INFO] [cluster.agent] connecting {"peerID": "node-2"}

    peerLogger.Error("connection failed", wklog.Error(err))
    // 输出: 2026-04-10 14:23:05.456 [ERROR] [cluster.agent] connection failed {"peerID": "node-2", "error": "dial timeout"}
}
```

## 7. 日志输出格式

### 7.1 Console 格式（开发环境）

```
2026-04-10 14:23:05.123 INFO  [cluster]       server started  {"nodeID": "node-1", "addr": ":9090"}
2026-04-10 14:23:05.456 ERROR [cluster.agent]  connect failed  {"peerID": "node-2", "error": "dial timeout"}  caller=agent.go:87
2026-04-10 14:23:05.789 DEBUG [raftlog]        write batch     {"entries": 128, "bytes": 65536}
```

### 7.2 JSON 格式（生产环境/日志采集）

```json
{"time":"2026-04-10 14:23:05.123","level":"INFO","module":"cluster","caller":"cluster.go:142","msg":"server started","nodeID":"node-1","addr":":9090"}
{"time":"2026-04-10 14:23:05.456","level":"ERROR","module":"cluster.agent","caller":"agent.go:87","msg":"connect failed","peerID":"node-2","error":"dial timeout","stacktrace":"..."}
```

## 8. 日志最佳实践规范

### 8.1 级别使用规范

| 级别    | 使用场景                                         |
|---------|--------------------------------------------------|
| `Debug` | 详细调试信息，如 raft 日志条目、消息收发细节       |
| `Info`  | 重要状态变更，如启动/停止、连接建立/断开、配置加载 |
| `Warn`  | 可恢复的异常，如重试、降级、配置使用默认值         |
| `Error` | 不可恢复的错误，需要人工介入                       |
| `Fatal` | 程序无法继续运行的致命错误，仅在入口使用           |

### 8.2 日志规范

1. **消息用小写动词短语**：`"starting"`, `"connect failed"`, `"slot migrated"`
2. **关键信息放字段，不拼字符串**：
   - 好: `logger.Info("slot migrated", wklog.Uint64("slotID", id), wklog.String("from", from))`
   - 坏: `logger.Info(fmt.Sprintf("slot %d migrated from %s", id, from))`
3. **Error 级别必须带 error 字段**：`logger.Error("...", wklog.Error(err))`
4. **避免在循环中打 Info 日志**，用 Debug 或采样
5. **Fatal 仅在 `main()` 或 `app.Start()` 中使用**

## 9. 模块命名约定

各模块的 Named 层级：

| 模块路径                        | Logger 名称          |
|---------------------------------|----------------------|
| `pkg/cluster`                   | `cluster`            |
| `pkg/cluster` 内部 agent        | `cluster.agent`      |
| `pkg/cluster` 内部 router       | `cluster.router`     |
| `pkg/controller/raft`           | `controller.raft`    |
| `pkg/slot/proxy`                | `slot.proxy`         |
| `pkg/slot/fsm`                  | `slot.fsm`           |
| `pkg/slot/multiraft`            | `slot.multiraft`     |
| `pkg/channel`                   | `channel`            |
| `pkg/channel/replica`           | `channel.replica`    |
| `pkg/channel/runtime`           | `channel.runtime`    |
| `pkg/channel/store`             | `channel.store`      |
| `pkg/channel/handler`           | `channel.handler`    |
| `pkg/channel/transport`         | `channel.transport`  |
| `pkg/raftlog`                   | `raftlog`            |
| `pkg/transport`                 | `transport`          |
| `internal/app`                  | `app`                |

## 10. 依赖关系

```
pkg/wklog           ← 零外部依赖，纯接口
    ↑
    │  import
    │
pkg/cluster          仅依赖 pkg/wklog 接口
pkg/controller
pkg/slot
pkg/channel
pkg/raftlog
pkg/transport
    ↑
    │  import
    │
internal/log         依赖 pkg/wklog + zap + lumberjack
    ↑
    │  import
    │
internal/app         依赖 internal/log，创建实现并注入各 pkg
```

## 11. 测试策略

- **单元测试**：传入 `wklog.NewNop()` 作为 Logger，不产生实际日志 IO
- **集成测试**：可使用 `zap.NewDevelopment()` 或 `zaptest.NewLogger(t)` 封装为 `wklog.Logger`
- **日志断言**：需要时可实现 `TestLogger`，记录日志到内存切片，供测试断言
