```mermaid
sequenceDiagram
    participant App as 应用层
    participant Node as Node (node.go/raft_propose.go)
    participant RaftCore as Raft (raft.go)
    participant Storage as Storage
    participant Transport as Transport

    App->>+Node: 调用 Propose(data) # Node 激活
    Node->>Node: 封装数据为 MsgProp
    Node->>Node: 发送 MsgProp 到 propc channel
    Note right of Node: Node 主循环监听 channel

    Node->>RaftCore: 从 propc 收到 MsgProp, 调用 Step(MsgProp)
    activate RaftCore # 明确激活 RaftCore
    RaftCore->>RaftCore: 判断当前是否为 Leader
    alt 是 Leader
        RaftCore->>RaftCore: 追加提案到 Leader 日志 (entries)
        RaftCore->>RaftCore: 生成 MsgApp (AppendEntries) 消息给 Followers
        RaftCore-->>Node: 返回包含待发送 MsgApp 的 Ready 结构
    else 不是 Leader
        RaftCore-->>Node: 返回错误或无操作 (例如 ErrProposalDropped)
        Note right of Node: 应用层需处理非 Leader 情况 (通常重试或转发)
    end
    deactivate RaftCore # 明确停用 RaftCore

    Node->>Node: 从 readyc 发送 Ready 结构 # Node 保持激活
    App->>Node: 从 Ready() channel 接收 Ready 结构 # 应用层接收到 Ready
    activate App # 应用层开始处理 Ready
    Note right of App: 处理 Ready 结构
    App->>Storage: 持久化 Ready.Entries (新日志条目)
    activate Storage # 激活 Storage
    Storage-->>App: 持久化完成
    deactivate Storage # 停用 Storage

    App->>Transport: 发送 Ready.Messages (MsgApp) 给 Followers
    activate Transport # 激活 Transport
    Transport-->>App: 发送完成
    deactivate Transport # 停用 Transport

    App->>Node: 调用 Advance() 通知处理完毕 # Node 保持激活
    deactivate App # 应用层处理完此轮 Ready

    Note right of Transport: Followers 收到 MsgApp 后处理并回复 MsgAppResp
```