# WuKongIM MQTT IM 接入设计

状态：设计及表级方案已于 2026-09-23 确认；codec、网关接口及 session/subscription/cursor 三张表和 Slot 命令已建立并验证；MQTT 产品入口、完整持久运行时和可靠消息源尚未实现。

实施依据：[实施计划](../superpowers/plans/2026-09-23-mqtt-im-access.md)。本文件描述目标合同，不表示当前版本已提供这些能力。应用编码约定已冻结为 [wire contract](mqtt-wire-contract.md)。

源码核对基线：`64f73d99b3b0cb8960d40825f76053f5a0000dbd`。此前调研过的相关源码在该基线未发生变化。

## 1. 已确认的产品范围

| 决策 | 约定 |
| --- | --- |
| 产品定位 | MQTT 是现有 IM 的入口，与 WK 客户端互通，共用身份、频道、权限和消息 |
| 客户端 | 使用标准 MQTT 客户端；认证、topic、payload 的应用约定由项目定义 |
| 协议 | MQTT 5.0；QoS 0、1；精确主题订阅 |
| 会话 | 支持持久会话，跨节点重连恢复订阅、未确认投递及符合条件的离线消息 |
| 多端 | 不同 ClientID 共存；相同 ClientID 接管；不套用 WK 主从设备互踢规则 |
| 离线期限 | 服务端允许的离线 Session Expiry 上限初始为 24 小时，可配置 |
| 资源上限 | 同时限制积压条数和字节；超限显式终止会话，客户端重新订阅并经业务后端同步 |
| 内容保留 | 有效期限和配额内的待投递内容独立于普通 IM 历史清理 |
| 权限撤销 | 当前权限优先；被踢、解散后停止后续授权投递 |
| 附加能力 | 支持 Will；关闭 Retain、通配符和共享订阅 |
| 业务管理 | 建群、成员管理、历史查询等经鉴权后的业务后端完成 |

Session Expiry=0、Clean Start、客户端要求的更短期限仍按 MQTT 5.0 处理。24 小时是允许的上限，不是强制把所有连接改成持久会话。

## 2. 复用的边界

业务入口在 `SendCommand` 汇合，协议报文保持独立：

```text
WK 客户端   → WK packet   → WK access   ─┐
                                        ├→ message usecase → Channel commit
MQTT 客户端 → MQTT packet → MQTT access ─┘

Channel commit → 现有 WK Online Delivery
              → 可恢复的 MQTT 消息源 → 共享待投递内容 → MQTT 会话投递
```

- 复用用户鉴权、频道标识、发送权限、消息 ID、幂等机制、Channel 顺序、复制和集群路由。
- MQTT 的订阅表示客户端的接收选择；群成员关系表示用户的访问资格。SUBSCRIBE 不加群，UNSUBSCRIBE 不退群。
- WK `frame.Frame` 保留现有职责。MQTT 有独立 packet；网关中认证、工作分类和会话写入的必要接口从具体 WK 报文解耦。
- MQTT 使用独立的 QoS 状态机。现有 WK RECVACK tracker 不同时管理 MQTT 的 PUBACK 和重发。
- 所有持久状态使用已有集群基础，默认 256 个 hash slots；单节点集群遵循相同语义。

现有实现依据：

- [网关协议接口](../../pkg/gateway/protocol/protocol.go)仍以 WK frame 收发。
- [业务发送命令](../../internal/contracts/channelappend/types.go)已独立于入口报文，但仍包含 IM 业务语义。
- [在线连接身份](../../internal/runtime/online/types.go)属于某个节点、进程代次和具体连接，不是持久 MQTT Session。
- 当前提交后回调是尽力执行；没有可直接接入的生产级可靠 MQTT replay worker。

## 3. 模块职责（拟新增或调整）

| 位置 | 职责 |
| --- | --- |
| `pkg/protocol/mqtt` | 报文、编解码、格式验证；不包含 UID、群权限或存储调用 |
| `pkg/gateway` | 复用监听、连接、串行写入与有界工作调度；提供协议握手和派发接口 |
| `internal/access/mqtt` | 报文与用例命令互转、MQTT 错误码、应用 topic 和属性映射 |
| `internal/usecase/mqttsession` | ClientID 绑定、订阅与 Will 授权、期限及超限策略；依赖窄端口，不依赖 packet |
| `internal/runtime/mqttsession` | 本节点承载的会话执行、投递窗口、包标识和恢复调度；通过端口使用持久权威状态 |
| `internal/contracts/mqttsession` | 会话身份、订阅、消息引用、交付状态等跨层值对象 |
| `internal/infra/mqttsession` | 适配 Slot 状态、共享内容存储、消息源读取及集群路由 |
| `internal/app` | 唯一装配和生命周期入口；消费者与恢复能力就绪后开放 MQTT 连接 |

`pkg/slot` 和 `pkg/db/meta` 仅扩展有版本的持久状态与条件写契约，不承载 topic 鉴权或 IM 业务规则。节点 RPC 仍通过现有入口注册；不建立第二套集群通信。

## 4. 身份、订阅与协议约定

### 身份

区分四种标识：

1. UID：通过已有身份验证得到的业务用户。
2. ClientID：当前 broker 命名空间内的稳定 MQTT 客户端标识，持久绑定到 UID。
3. Session generation：一次持久会话的代次；Clean Start、失效或过期后改变。
4. Connection owner generation：某次连接所有权；每次接管改变，并携带节点及进程身份。

会话权威键采用 `(broker namespace, ClientID)`，经逻辑 hash slot 路由。不能仅按 UID 分隔 ClientID，从而让同一个 broker 中的重复 ClientID 悄悄同时在线。认证成功且身份绑定匹配后，才能尝试接管。

复用现有 Token verifier。当前凭据按 `(UID, DeviceFlag)` 管理，首版不额外承诺每个 ClientID 独立签发和吊销凭据。认证适配层可从约定的用户名或 CONNECT User Properties 中提取 UID 和设备类别，密码字段承载 Token；客户端不能提供 DeviceLevel 或借 ClientID 获得系统设备权限。

MQTT 是接入协议，不是设备类别。既有设备凭据类别可以复用，互踢策略则使用明确的会话策略，避免在各层散落协议字符串判断。

### 应用主题

| 精确主题 | 发布含义 | 订阅资格 |
| --- | --- | --- |
| `wk/v1/groups/{group}/messages` | 向群频道发送消息 | 当前群成员 |
| `wk/v1/users/{uid}/messages` | 向该用户发送单聊消息 | 仅该 UID 自己 |

ID 片段使用无歧义的可逆编码，禁止原始斜杠、通配符等改变主题结构。发送仍经过已有单聊/群聊权限检查。个人收件主题可以聚合来自多个对话的消息；只保证各源 Channel 内顺序，不声明跨频道总顺序。

消息正文沿用现有 IM payload 字节。稳定的 `client_msg_no` 通过应用定义的 MQTT 5 User Property 提交；输出携带 MessageID、MessageSeq、发送者及频道身份等必要元数据。服务端元数据名称保留，客户端不能伪造。转发中的 MQTT 属性、顺序、Message Expiry 和 No Local 按标准保存与处理，不能只保留 payload 后丢失协议语义。

原始发布 QoS、稳定的发布者 ClientID 及其命名空间、协议属性、原始过期依据等会改变恢复结果的元数据，必须通过不依赖 packet 的有界值对象进入同一可靠复制边界，或建立同等可靠的可重建关联。当前发送和日志合同并未完整提供这些字段，需要显式扩展并验证复制、备份和恢复；不能等到回调或转存时才从内存会话补齐。

同一 MQTT 发布在转发时，其有效 QoS 不超过发布 QoS 和订阅授予 QoS。WK 等其他入口的普通持久消息可映射为最高 QoS 1 的发布。QoS 0 不自动等于 NoPersist；QoS 0 消息首版不提供离线积压保证。既有 NoPersist 消息保持在线限定，不因 MQTT 会话而落盘，可映射为在线 QoS 0。

## 5. 可靠消息源与共享内容保留

### 为什么需要扩展现有提交链路

现有 Channel 提交成功后再把 envelope 放入内存回调队列；此时崩溃可能丢失回调。现有 named replay cursor 还可能被普通 retention floor 向前推进，不能拿来证明 MQTT 已接收消息。

因此，可靠性来源必须是可重放的已提交记录及其持久保护状态。回调只唤醒工作，不决定消息是否存在。

### 推荐的存储形态

- Channel 原日志保存业务消息；为 MQTT 建立独立的、带代次的源起点与复制水位。
- 共享 replay 存储按稳定消息身份保存一份不可变投递内容及必要协议元数据，独立于普通历史的逻辑可见性与清理。
- 会话保存订阅生效区间、恢复进度及交付状态。大群离线积压优先用共享消息范围和游标表示，避免每条消息为所有离线会话复制正文或同步创建逐消息记录。
- 实际进入 QoS 1 投递窗口时，持久化该会话的 Packet Identifier、消息身份、精确内容引用和交换状态。已发送内容不能在消息编辑后被替换成另一份正文重发。
- 索引、游标与范围压缩是存储优化；必须能准确计算会话积压、发现缺口并完成配额处理，不能以压缩为由丢掉投递责任。

共享内容回收由持久的消费者索引和完成证明控制：源端可重建有效订阅区间，Session 权威保存恢复进度与未完成交换；只有相关责任已确认、撤销、终止或过期，才允许释放对应内容。跨 Slot 汇总必须保守，缺少某个权威的证明不能当作无消费者。24 小时从断线计算，不是消息固定 TTL；长期在线且未确认的消息不能仅因消息年龄而删除。范围累计条数/字节索引、有界配额检查及可恢复的回收进度均属于新增能力。

### 提交与转存的顺序约束

1. 首个需要可靠恢复的订阅生效前，持久建立源保护与订阅起始边界；SUBACK 成功前这些条件必须成立。
2. 消息的复制记录及其确定性持久状态保证：即使没有回调，恢复扫描也能发现该消息的 MQTT 处理责任。
3. 将内容幂等写入共享 replay 存储，取得可跨节点恢复的持久完成证明。
4. 此后才能推进独立的源复制水位、允许释放对应源数据的物理保留。
5. 普通历史可以先逻辑隐藏这些消息；受保护的内部源读取使用独立契约，不能走会跳过 retention floor 的普通历史接口。

个人收件主题还必须覆盖未来新对话：订阅建立的是持久的 UID 级接收资格，不能只枚举当时已有的单聊 Channel。新 person Channel 的首次相关消息提交前，必须可靠纳入源保护，或以同等提交保证写入可重放的收件索引；发现和纳入过程要带代次且可恢复。在线通知和当前会话列表都不能成为新对话唯一的发现方式。

原日志保护只持续到可靠转存完成；慢 MQTT 客户端不应长期阻止普通 Channel 整段前缀回收。若转存存储或全局配额饱和，在产生新的不可承受责任前实施有界背压；已经提交的消息不得因为回调队列满而丢失。

跨 Slot 的订阅建立、索引更新或投递状态更新使用可恢复操作、稳定幂等键、代次和条件进度推进，不假定存在跨 Slot 原子事务。WK、HTTP 等入口产生的匹配消息同样覆盖在这条可靠来源中。

## 6. 三条关键链路

### 发布

```text
PUBLISH → 解码及身份检查 → topic 映射 → message.Send
        → Channel quorum commit + 可恢复的 MQTT 来源保证
        → QoS 1 成功 PUBACK
```

PUBACK 表示服务器承担了此次应用消息的处理责任，不表示所有订阅者收到，更不表示用户已读。正常持久 IM 发布采用提交后确认策略。Packet Identifier 只标识当前协议交换，不直接成为永久幂等键；应用的 `client_msg_no` 在业务重试中保持稳定。

### 下行与 PUBACK

```text
恢复订阅和共享消息 → 检查当前权限与会话代次
→ 持久建立本次 in-flight 记录 → 发 PUBLISH
→ 收到 PUBACK → 持久确认状态及安全进度 → 释放窗口
```

ACK 绑定连接上下文中的 session generation、owner generation、方向和 Packet Identifier。不能只凭 16 位包号释放状态，也不能以收到的最大 MessageSeq 直接越过较早的未完成消息。

MQTT 5.0 在恢复已有会话时按标准重发未确认报文；不照搬 WK 的连接内定时重发机制。已写网络、ACK 到达但尚未持久化等故障窗口允许 QoS 1 重复，MessageID 保持稳定。

### 跨节点接管

新连接先鉴权、验证 ClientID 绑定，再通过会话权威取得新的所有权代次。旧连接关闭后才开放新连接的投递。

接管不能只依赖尽力发送的踢人 RPC。旧 owner 无法联系时，需要可验证的执行租约/隔离证明；无法证明旧连接已失效，就等待其执行权限过期或失败本次连接。旧连接的迟到 ACK、关闭通知、Will 定时器和写请求均不得修改新 owner 状态。

具体网络写入仍在接入节点进行；会话状态的权威所有者与持有 socket 的节点不要求相同。

## 7. 权限、期限、配额和 Will

- 订阅时检查权限，恢复和后续投递也检查权威版本。群成员重新加入不能复活旧订阅代次和旧积压。
- 权限撤销后不得新授权投递；已被接收方取得或已进入不可撤回网络发送的数据无法追回。撤权与投递需要明确可测试的排序点，不能仅靠异步缓存失效承诺即时隔离。
- 普通取消订阅与权限撤销分别处理。正常 UNSUBSCRIBE 仍遵守 MQTT 对已开始交换的完成规则；若撤权要求禁止既有未确认交换继续重发，则显式终止受影响的会话，不能保留 Session Present=1 却悄悄删除必须恢复的交换。
- 隐藏/删除会话历史不自动等同退群。它与已经进入 MQTT 交换的消息是不同状态；不能用历史读取的可见性游标替代投递 ACK。显式安全删除或权限撤销则走对应终止策略。
- 初始容量候选：每会话最多 10,000 条或 64 MiB 逻辑积压，以先达到者为准；每会话投递窗口候选上限 64，并遵守对端 Receive Maximum。这些是待压测的初始配置值，不是已经验证的容量结论。
- 另设连接输入/输出、订阅数、恢复批量、节点和集群共享存储上限。全局存储不足不能形成无限排队。
- 超限终止会话时持久记录失效代次及原因，并释放责任。在线时发送标准断连反馈；离线时保留有界的失效记录。下次连接以 Session Present=0 表示原会话不再存在，应用约定要求重新订阅并通过业务后端同步。不能把 0 本身当成唯一的超限原因码。
- Will 与会话一起持久化，保存已验证的身份、目标、正文、属性和代次；建立时及发布时均检查权限。
- Will Delay、Clean Start、会话到期、正常/请求发布 Will 的 DISCONNECT 和接管均按 MQTT 5.0 判定。使用可恢复的待发布状态和稳定业务幂等键，避免崩溃窗口导致任务丢失或重复生成业务消息。不能把任意重连都当作取消 Will。

## 8. 配置、性能与验证

配置进入 `wukongim.toml` 的域分组 snake_case 项，支持 `WK_` 环境变量覆盖；更新示例配置和字段英文注释。MQTT 初始默认关闭，开启时验证所有持久能力和资源上限齐备。TLS 使用既有部署设施或明确的接入终止点；不能复用 WK 专用报文加密握手。

大群成本仍包含接收者网络投递与在线交付状态，不能声称 fanout 变成 O(1)。设计避免同步逐成员写正文、逐会话 goroutine、全连接扫描和无界恢复；按 source/session 分片、限额分页、公平轮转、批量持久化，慢客户端只消耗自己的窗口与配额。

实施前先写出失败场景，再增加进程级 E2E；按 `test/e2e/AGENTS.md` 执行并留下可重复核验的报告。关键场景：

1. 标准 MQTT 5 客户端与 WK 客户端双向单聊/群聊，鉴权和主题授权正确。
2. Channel 提交后、回调前崩溃，恢复无漏投；特别覆盖用户离线后新联系人第一次发送，不能依赖旧的 Channel 枚举。
3. 共享内容已持久、源水位未推进时崩溃，恢复只允许幂等重做。
4. 普通历史清理与源转存、接管、故障切换并发，内容不提前删除。
5. 发包前后、PUBACK 持久化前后宕机，包标识和内容恢复一致。
6. 相同 ClientID 跨节点竞争和网络分区，旧 owner 不继续授权写入。
7. 离群、重新入群、解散、取消订阅与恢复并发，不越权也不错误释放新会话状态。
8. 会话过期、条数/字节超限、全局存储背压及客户端恢复流程。
9. Will Delay、正常断连、接管、节点宕机、权限撤销与重复执行。
10. 长期在线但未 PUBACK、跨 Slot 回收证明缺失及消费范围压缩，不能按消息年龄错误回收；崩溃后 QoS、No Local 和发布属性保持一致。
11. 十万人群、高消息率、多频道、多在线和离线会话；记录 CPU、内存、分配、队列、存储放大、恢复延迟及吞吐，必要时用 pprof。

实施交付还需更新受影响 FLOW、PROJECT_KNOWLEDGE、CHANGELOG 与相关配置文档；新增协议及存储格式需要版本和兼容性验证。上述验证尚未执行，本设计不构成性能或协议一致性测试通过的声明。

## 9. 实施顺序

1. 固定协议能力矩阵、主题/属性/身份合同、故障场景和 E2E 验收。
2. 调整网关必要接口，接入 MQTT codec、认证和基本 IM 映射。
3. 实现 Slot 会话状态、订阅生效操作、所有权隔离与恢复。
4. 实现可靠消息源保护、共享 replay 内容、QoS 状态和配额；覆盖所有业务发送入口。
5. 完成 Will、撤权、资源回收、配置、观测与压力验收。

前面的阶段只是实现里程碑，不能把仅支持在线收发的阶段报告为本次持久 MQTT 能力已经完成。

标准依据：[OASIS MQTT 5.0](https://docs.oasis-open.org/mqtt/mqtt/v5.0/os/mqtt-v5.0-os.html)，尤其是 ClientID、Session State、QoS、重发、流控与 Will 章节。

## 10. 表级设计

新增 7 张 KV 逻辑表：6 张 Slot 元数据表、1 张共享 replay 内容表；扩展现有 `message` 表的可选发布元数据，并新增源保护/转存进度的 System 记录。逻辑表职责与拆分已经确认。具体字段编码、索引和 durable ID 按 [storage contract](mqtt-storage-contract.md)逐步冻结；当前已实现 session/subscription/cursor 三张表及其 Slot 命令，其他表和可靠链路仍待实施。

当前行与索引均带自己的 hash-slot 分区，见 [meta keys](../../pkg/db/meta/keys.go)。同 Slot 的二级索引不会自动成为另一 Slot 上的反向索引。

约定：`S = (broker_namespace, ClientID)`，`G = session_generation`，`R = source_kind + source_identity + source_generation`。Session 相关记录路由到 S 的权威 Slot；Source binding 路由到 R 或 UID 收件资格的权威所有者。消息内容使用有明确复制、迁移和恢复契约的存储，不能只写接入节点本地数据库。

### 新增表

| 表 | 建议逻辑主键 | 主要数据和职责 |
| --- | --- | --- |
| `mqtt_session` | S | 绑定 UID、G、owner/连接代次、执行租约、状态、离线到期时间、配额状态、协商参数及失效原因；索引支持有界到期扫描。会话清理与身份绑定的保留分开处理，不保留明文 Token。 |
| `mqtt_subscription` | S + G + exact topic | QoS、No Local、Subscription Identifier 等选项、订阅代次、授权版本、建立/撤销操作阶段。保存客户端的订阅意图，不改变 IM 群成员。 |
| `mqtt_delivery_cursor` | S + G + subscription_generation + R | 订阅生效边界、扫描/投递进度、连续完成水位及计量基础。个人收件主题包含多个 source，必须逐 source 记录，不能只有一个 last_seq。 |
| `mqtt_inflight` | S + G + direction + packet_id | 交换代次、消息稳定身份、内容引用、QoS、待确认阶段和发送顺序。只为进入窗口的交换建立记录，不为全部离线消息建一行。需要上行短期交换状态时复用 direction，不另建上下行两张表。 |
| `mqtt_will` | S + 原 G + will_generation | 待发布义务、执行时间、稳定幂等键、状态、目标、受限正文/内容引用及属性。必须能在旧 session 被替换或清理后继续完成已生效的发布义务。 |
| `mqtt_source_binding` | source/UID owner key + S + G + subscription_generation | 源端反向订阅关系、生效区间、建立/撤销阶段、授权代次及回收所需投影。也承载 UID 收件资格，确保未来新单聊可以发现持久订阅。跨所有者更新须幂等恢复；它不是普通本地 secondary index。 |
| `mqtt_replay_message` | R + source_position + 必要的投递内容版本 | 多会话共享的不可变正文、MessageID、必要发布元数据、计量大小及内容校验。拥有独立于普通历史清理的保留周期；按消息源范围扫描和计量的索引需一并维护。 |

`mqtt_session` 可以保存当前 Will 配置，但真正产生的发布任务仍使用 `mqtt_will` 的独立生命周期。订阅投影重试阶段先保存在 subscription/binding 行中，不额外引入泛化任务表。

`mqtt_delivery_cursor` 与 `mqtt_inflight` 放在同一 Session 权威 Slot，使 PUBACK 处理、窗口释放和安全消费进度能原子变更。源端回收进度属于保守的跨所有者投影，不能将一次 RPC 超时解释成责任已完成。

`mqtt_replay_message` 不借用原消息的全局唯一 ID 索引再次插入相同业务消息；它有独立 keyspace。共享指同一逻辑正文供多个会话引用，各存储副本仍按复制协议保留数据。

### 修改现有消息表

当前 [message schema](../../pkg/db/message/schema.go)只有现有 IM 消息列，没有完整 MQTT 发布属性。推荐新增一个可选、有版本且有大小限制的 `publication_metadata` 列（或少量等价的可选列），保存：

- 来源类型、稳定发布者客户端身份和命名空间。
- 原始发布 QoS、用于重建应用 topic 的信息。
- 有序的 MQTT 5 发布属性以及过期计算依据。

不存协议 packet 对象、socket 或进程内 session 指针，不复用 `Topic`、`Setting`、`Expire` 的旧含义塞入不相容数据。旧消息缺少扩展时按明确的既有入口默认规则读取；客户端级 Packet Identifier 留在会话交换表。

修改不仅涉及 row codec，还要贯通 SendCommand、Channel Record、复制 RPC、entry/proposal 身份校验、恢复和备份。旧节点能跳过可选列，不等于旧节点能够正确复制、重建或维护新语义；功能启用必须有版本/能力门控。

### 新增 System 记录与必要索引

在消息源分区下新增独立的源状态记录，保存 source generation、保护起点、可靠转存水位、回收证明/进度。现有 HW/checkpoint 的语义保持不变；retention 的物理删除安全检查需纳入转存完成条件。

不得复用目前会被普通 retention 强行推进的 named cursor，见 [AdoptRetentionBoundary](../../pkg/db/message/compat.go)。System key 是存储布局，不自动带有共识保证；这些状态必须进入明确的复制或权威重建协议，并覆盖快照及恢复。

到期时间、Will 执行时间、待恢复操作、source range/累计大小等使用有界扫描索引。UID/source 所有者上的反向关系由 `mqtt_source_binding` 负责；它不能用 Session 所在 Slot 内的 UID secondary index 代替。

### 保持原有职责的表

用户、设备、频道元数据、IM subscriber、`user_channel_membership` 无需为了 MQTT 增加 ClientID、PacketID 或 MQTT ACK 游标。复用它们原有的身份、Token、频道和成员语义。

首版不增加 Retain 表、QoS 2 交换表、每会话每条离线消息的完整正文表，也不为 frame 建表。额外表的必要性来自跨节点会话恢复、Will 和与历史清理独立的投递责任。

所有新增表/列/索引/System ID 采用新编号，保留现有及已废弃的编号；按 [SCHEMA_COMPATIBILITY](../../pkg/db/SCHEMA_COMPATIBILITY.md)同时覆盖 Slot 命令、快照、inspect、导入导出和滚动升级约束。
