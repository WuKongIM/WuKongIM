# 剩余“规划中”文档的来源审计

Date: 2026-08-30

Source revision of this repository: `6ccaf7f442900a3bfdf345cae2b055cf5a1e72cd`

## 研究问题

本报告回答一个发布前问题：`docs-site` 中除 WuKongEasySDK 外仍标为“规划中”的 15 个路由，哪些内容已经有当前源码或精确发布制品支撑，哪些仍缺运行证据，哪些旧站说法不能迁入新站。

审计范围如下：

- Kubernetes 部署 1 页；
- Android、iOS、Flutter、HarmonyOS 各 3 页：平台能力、API 参考、升级指南；
- JavaScript 2 页：API 参考、升级指南。

[旧站中文入口](https://docs.githubim.com/zh) 是悟空团队的一手历史文档，可用于恢复主题范围和学习顺序；但它没有高于当前锁定源码、精确发布制品或实际运行收据的事实权威。特别是版本、方法签名、配置键、端口、兼容性、安全性和可上线性结论，均不得从旧站直接继承。

## 结论

15 个路由都可以从“规划中”升级为有实质内容的文档，但发布声明必须分层：

1. **API 参考和升级页可以发布为 source/artifact-locked 文档。** Android `1.5.5`、iOS `1.1.1`、Flutter `1.7.9`、HarmonyOS `1.1.7`、JavaScript `1.3.5` 均能锁定到精确源码或发布制品。
2. **平台能力页不能写成兼容性认证。** Android、iOS、Flutter、HarmonyOS 均没有本仓库所有的设备/系统版本运行收据；JavaScript 当前兼容性产物也明确是 `verified: false`、verification `missing`。源码存在某个分支只能证明“实现了该路径”，不能证明它在某个平台矩阵中通过。
3. **Kubernetes 页只能发布 v3 对齐的 Beta 架构与操作契约。** 官方 Helm 仓库的精确 chart 仍是 v2.1.5 时代产物，其镜像、启动命令、环境变量和端口都不符合当前 v3 运行契约；不能把它包装成 v3 可复制安装方案。
4. **五端均有生产安全门禁。** 原生/Flutter/HarmonyOS 客户端源码使用裸 TCP 或未展示可认证 TLS 路径，并存在敏感明文存储或载荷日志问题；JavaScript 会输出解密后的接收包和重试发送包。文档应把这些作为采用阻断项，而不是用“加密数据库”“协议加密”或 `debug=false` 淡化。
5. **旧站值得继承的是目录顺序。** “简介/集成 → 基础或连接 → 消息 → Channel → 会话 → 数据源/高级能力”的教学骨架仍适用；旧站的浮动依赖、旧端口、旧配置、旧方法签名、自动高可用/备份和平台兼容性说法均需重写。

## 证据词汇和发布门槛

| 标记 | 含义 | 文档可以怎样说 |
| --- | --- | --- |
| `S` Source fact | 锁定 commit 中可直接观察的实现、签名、默认值或差异 | “该精确源码包含……” |
| `A` Artifact fact | 精确发布包、声明文件、podspec、AAR/HAR/tarball 中可观察的事实 | “该发布制品声明/导出……” |
| `R` Runtime evidence | 对精确制品、环境和场景的可复现执行结果 | 仅在收据存在时说“已验证” |
| `U` Unverified | 源码可能支持，但没有所需环境的运行收据 | “源码路径存在；尚未验证” |
| `X` Unsupported/disproven | 与当前源码或制品直接冲突 | 不得发布为当前事实 |
| `B` Security blocker | 在生产采用前必须解决或由安全审查接受的风险 | 必须醒目标示，不能藏在 FAQ |

缺少 `R` 不阻止发布 API 参考，但会阻止“支持 Android X”“支持后台长连”“生产就绪”“Kubernetes chart 已支持 v3”等结论。`B` 阻止生产采用声明，不阻止诚实发布参考文档。

## 15 个路由的审计结果

| 路由 | 可用证据 | 建议发布状态 | 仍需明确的边界 |
| --- | --- | --- | --- |
| `/server/deployment/kubernetes` | `S` 当前 v3 配置、探针、生命周期、扩缩容 API；`A` 历史 v2 chart | Beta 架构/运维指南 | 无 v3 chart 和集群运行收据；不得称一键生产部署 |
| `/sdk/android/platform-capabilities` | `S/A` 1.5.5 | Source-inspected | 无设备/系统版本矩阵；后台、推送、TLS 未获证 |
| `/sdk/android/api-reference` | `S/A` 1.5.5 | 可发布 | 以公开 Java 源码和 AAR 为准，不以 README 为准 |
| `/sdk/android/upgrade` | `S/A` 1.5.4→1.5.5 diff | 可发布 | 只覆盖该版本边，不能泛化为所有升级 |
| `/sdk/flutter/platform-capabilities` | `S/A` 1.7.9 | Source-inspected | 源码依赖 `dart:io`；Web 不成立；各平台未运行验证 |
| `/sdk/flutter/api-reference` | `S/A` 1.7.9 | 可发布 | 根入口未重导出所有类型，深层 import 边界需标注 |
| `/sdk/flutter/upgrade` | `S/A` 1.7.7→1.7.9 diff | 可发布 | 无 1.7.9 tag；制品哈希和匹配 commit 才是身份 |
| `/sdk/ios/platform-capabilities` | `S/A` 1.1.1 | Source-inspected | podspec 部署下限冲突；后台、推送、TLS 未获证 |
| `/sdk/ios/api-reference` | `S/A` 1.1.1 framework headers | 可发布 | umbrella 暴露了内部类；必须分 app-facing 与实现头文件 |
| `/sdk/ios/upgrade` | `S/A` 1.1.0→1.1.1 diff | 可发布 | 删除消息可见性行为改变；源码 podspec 版本落后 |
| `/sdk/harmonyos/platform-capabilities` | `S/A` 1.1.7 | Source-inspected | 无 DevEco/真机收据；后台、推送、TLS 未获证 |
| `/sdk/harmonyos/api-reference` | `S/A` 1.1.7 | 可发布 | 包根只导出 `WKIM`，深层路径不是稳定公共契约 |
| `/sdk/harmonyos/upgrade` | `S/A` 1.1.6→1.1.7 diff | 可发布 | 无 tag；必须锁 HAR、哈希和源码区间 |
| `/sdk/javascript/api-reference` | `S/A` 1.3.5 | 可发布 | 仅 `.` 与 `package.json` 是 export map；深层 `lib/*` 非稳定 API |
| `/sdk/javascript/upgrade` | `S/A` 1.3.4→1.3.5，另有 1.3.0→1.3.5 | 可发布 | 当前运行收据缺失；不同起始版本必须分开写迁移 |

## 锁定的版本与发布制品

| 平台 | 文档版本 | 源码身份 | 发布制品身份 | 审计备注 |
| --- | --- | --- | --- | --- |
| Android | `1.5.5` | tag/commit `662a559a50d181540a0448454beb57e939b0c50e` | [JitPack AAR](https://jitpack.io/com/github/WuKongIM/WuKongIMAndroidSDK/1.5.5/WuKongIMAndroidSDK-1.5.5.aar) `com.github.WuKongIM:WuKongIMAndroidSDK:1.5.5`；SHA-256 `5a797f1fac53c4fbcf015afca2686ecbeebd24b5e64dea598881b814b1322792` | 源码、tag、AAR 可互相锚定 |
| iOS | `1.1.1` | source tag `89bf9a1b95ce374caabdd8031d69cc8844d825ae` | framework tag `0cbfb99f18010fe76b7e13ed31b5d1ad4664b10c` | source podspec 仍写 `1.1.0`；分发 framework 是制品权威 |
| Flutter | `1.7.9` | matching commit `de1024276523119e38305c49a3a873caae4d5c59` | pub.dev archive SHA-256 `b6191a86cd1e4caacaa4652e95709310eb1493f159fee65e1dd53c2a3ff9e80a` | 没有 `1.7.9` tag；必须用 archive + commit，不得只写 main |
| HarmonyOS | `1.1.7` | matching commit `0c41810a1e0a5fc2936929d63ca32a50ffb11bec` | OHPM `@wukong/wkim@1.1.7`，HAR SHA-256 `d98d1523bc60ad204dd74d9cfa776935a5547fc3ab352322dfa17f5dbc7a3cd8` | 没有 tag；HAR metadata 声明 compile SDK `6.1.1.125` |
| JavaScript | `1.3.5` | tag/commit `3c507ea3ebc08eae9d74fc1f76b150c380752008` | npm integrity `sha512-Y3RY4IdkLfCB2MCJFQlamSe5EQ6SU3PGphdoV9MJjJTSUAzZTTw5gBxmMi2jbwLRDqM+cSFaIb1vhQ+Rl0ftnQ==`；tarball SHA-256 `b053c9623ac36b7ce78dfd874240ac48abaee48e20dd78d824f28881c5504cfc` | npm `lib/*.d.ts` 是已发布类型面的制品证据 |

发布包元数据可从 [npm registry 1.3.5](https://registry.npmjs.org/wukongimjssdk/1.3.5)、[pub.dev package API](https://pub.dev/api/packages/wukongimfluttersdk) 和 [OHPM HAR](https://repo.harmonyos.com/ohpm/@wukong/wkim/-/wkim-1.1.7.har) 复核。哈希是本次对下载制品的内容寻址结果，不代表运行兼容性。

## 旧站路由映射

### Kubernetes

旧站把 Kubernetes 拆成四页：

- [单节点](https://docs.githubim.com/zh/installation/k8s/single-node)
- [多节点](https://docs.githubim.com/zh/installation/k8s/multi-node)
- [扩缩容](https://docs.githubim.com/zh/installation/k8s/scaling)
- [升级](https://docs.githubim.com/zh/installation/k8s/upgrade)

新站的一页可以继承“先建立拓扑 → 安装 → 验证 → 扩缩容 → 升级/回滚”的顺序，但不能复制旧页的 chart `0.1.0`、镜像 `v2.0.0`、端口 `5172/5300`、`/health`、`WK_CLUSTER_NODEID`、`replicaCount` 即成员变更、Deployment 路径和 `/data` 打包备份等事实。旧单节点页关于“强高可用、灾备、自动副本备份”的表述也没有当前部署证据。

### Android

旧路由依次为 [intro](https://docs.githubim.com/zh/sdk/wukongim/android/intro)、[integration](https://docs.githubim.com/zh/sdk/wukongim/android/integration)、[base](https://docs.githubim.com/zh/sdk/wukongim/android/base)、[message](https://docs.githubim.com/zh/sdk/wukongim/android/message)、[channel](https://docs.githubim.com/zh/sdk/wukongim/android/channel)、[channel-member](https://docs.githubim.com/zh/sdk/wukongim/android/channel-member)、[conversation](https://docs.githubim.com/zh/sdk/wukongim/android/conversation)、[cmd](https://docs.githubim.com/zh/sdk/wukongim/android/cmd)、[datasource](https://docs.githubim.com/zh/sdk/wukongim/android/datasource)、[reminder](https://docs.githubim.com/zh/sdk/wukongim/android/reminder)、[advance](https://docs.githubim.com/zh/sdk/wukongim/android/advance)。

可以继承 manager/主题顺序和数据源、提醒、高级能力覆盖；不能复制浮动 `version`、手动列出的 SQLCipher/Curve25519 依赖、旧构建基线、旧方法签名或“数据库加密即安全”的结论。

### iOS

旧路由依次为 [intro](https://docs.githubim.com/zh/sdk/wukongim/ios/intro)、[integration](https://docs.githubim.com/zh/sdk/wukongim/ios/integration)、[connection](https://docs.githubim.com/zh/sdk/wukongim/ios/connection)、[chat](https://docs.githubim.com/zh/sdk/wukongim/ios/chat)、[channel](https://docs.githubim.com/zh/sdk/wukongim/ios/channel)、[conversation](https://docs.githubim.com/zh/sdk/wukongim/ios/conversation)、[media](https://docs.githubim.com/zh/sdk/wukongim/ios/media)、[advanced](https://docs.githubim.com/zh/sdk/wukongim/ios/advanced)。

可以继承连接、聊天、Channel、会话、媒体、高级能力的教学顺序；不能复制浮动 pod/Git main、旧签名和默认值，也不能从旧例子推导后台保活、APNs、TLS 或当前 iOS 下限。

### Flutter

旧路由依次为 [intro](https://docs.githubim.com/zh/sdk/wukongim/flutter/intro)、[integration](https://docs.githubim.com/zh/sdk/wukongim/flutter/integration)、[base](https://docs.githubim.com/zh/sdk/wukongim/flutter/base)、[message](https://docs.githubim.com/zh/sdk/wukongim/flutter/message)、[channel](https://docs.githubim.com/zh/sdk/wukongim/flutter/channel)、[channel_member](https://docs.githubim.com/zh/sdk/wukongim/flutter/channel_member)、[conversation](https://docs.githubim.com/zh/sdk/wukongim/flutter/conversation)、[cmd](https://docs.githubim.com/zh/sdk/wukongim/flutter/cmd)、[datasource](https://docs.githubim.com/zh/sdk/wukongim/flutter/datasource)、[reminder](https://docs.githubim.com/zh/sdk/wukongim/flutter/reminder)、[advance](https://docs.githubim.com/zh/sdk/wukongim/flutter/advance)。

可以继承基础、消息、Channel member、会话、CMD、数据源、提醒和高级能力的分层；不能复制 `^version`、只支持 iOS/Android 的旧平台范围、生命周期示例或旧 API 名称。

### HarmonyOS

旧路由与 Flutter 同构：[intro](https://docs.githubim.com/zh/sdk/wukongim/harmonyos/intro)、[integration](https://docs.githubim.com/zh/sdk/wukongim/harmonyos/integration)、[base](https://docs.githubim.com/zh/sdk/wukongim/harmonyos/base)、[message](https://docs.githubim.com/zh/sdk/wukongim/harmonyos/message)、[channel](https://docs.githubim.com/zh/sdk/wukongim/harmonyos/channel)、[channel_member](https://docs.githubim.com/zh/sdk/wukongim/harmonyos/channel_member)、[conversation](https://docs.githubim.com/zh/sdk/wukongim/harmonyos/conversation)、[cmd](https://docs.githubim.com/zh/sdk/wukongim/harmonyos/cmd)、[datasource](https://docs.githubim.com/zh/sdk/wukongim/harmonyos/datasource)、[reminder](https://docs.githubim.com/zh/sdk/wukongim/harmonyos/reminder)、[advance](https://docs.githubim.com/zh/sdk/wukongim/harmonyos/advance)。

只能继承主题次序；不能把旧版号、深层 ArkTS import、方法签名、后台/推送能力或设备兼容性当作当前事实。

### JavaScript

旧路由依次为 [intro](https://docs.githubim.com/zh/sdk/wukongim/javascript/intro)、[integration](https://docs.githubim.com/zh/sdk/wukongim/javascript/integration)、[base](https://docs.githubim.com/zh/sdk/wukongim/javascript/base)、[chat](https://docs.githubim.com/zh/sdk/wukongim/javascript/chat)、[channel](https://docs.githubim.com/zh/sdk/wukongim/javascript/channel)、[conversation](https://docs.githubim.com/zh/sdk/wukongim/javascript/conversation)、[datasource](https://docs.githubim.com/zh/sdk/wukongim/javascript/datasource)、[advance](https://docs.githubim.com/zh/sdk/wukongim/javascript/advance)。

可以继承“集成 → 基础 → 聊天 → Channel → 会话 → 数据源 → 高级”的学习路径；不能复制无版本 npm/yarn/pnpm/bun 命令、`@latest` CDN、旧 root API，也不能把旧页提到的浏览器、Node 服务端或小程序环境当作当前兼容性矩阵。

## Kubernetes 部署页

### 当前 v3 运行契约（`S`）

当前仓库把任何规模都建模为 cluster；一台机器是 **single-node cluster**，不存在绕开集群语义的 standalone 路径。默认必须按 256 个 hash slot 设计。[仓库规则](../../../AGENTS.md) 当前镜像在 [`6ccaf…:Dockerfile`](../../../Dockerfile) 中暴露 `5001`、`5100`、`5200`、`5301`、`7000`、`19092`，入口是 `/usr/local/bin/wukongim -config /etc/wukongim/wukongim.toml`。

当前配置使用 `WK_` 前缀覆盖 TOML；集群关键项是稳定且唯一的 `node.id`、每 Pod 持久化的 `node.data_dir`、`cluster.listen_addr`，以及配置层对应的 `WK_NODE_ID`、`WK_CLUSTER_LISTEN_ADDR`。仓库的多节点配置示例使用 `7000` 做节点间通信、`5001` 做 HTTP API、`5100` 做 TCP、`5200` 做 WebSocket、`5301` 做 Manager。[`6ccaf…:internal/config/schema.go`](../../../internal/config/schema.go) [`6ccaf…:docker/conf/node1.toml`](../../../docker/conf/node1.toml)

探针必须区分：`/healthz` 是进程存活探针；`/readyz` 会检查集群和写路由就绪，未就绪可返回 503。[`6ccaf…:internal/access/api/server.go`](../../../internal/access/api/server.go) Pod `Running` 因而不能替代 ready 判定。进程接收终止信号后会走优雅停止，当前默认停止超时为 5 秒；Kubernetes 的 `terminationGracePeriodSeconds` 必须覆盖实际 drain/flush 时间。[`6ccaf…:cmd/wukongim/main.go`](../../../cmd/wukongim/main.go)

扩容和缩容是 Controller/Manager 驱动的成员状态机，而不是只改 StatefulSet replicas。当前 Manager 暴露 join、activate、onboarding plan/start/advance，以及 scale-in plan/start/drain/remove/advance/status 等路径。[`6ccaf…:server.go`](../../../internal/access/manager/server.go) [`6ccaf…:node_lifecycle.go`](../../../internal/access/manager/node_lifecycle.go) [`6ccaf…:scale_in.go`](../../../internal/access/manager/scale_in.go)

### 历史 chart 的制品事实（`A`）与不兼容项（`X`）

官方 Helm 仓库锁定 commit `b0eddcfce07f6be8e90ba1f4fecd6fa21fc894cd` 的 [chart 目录](https://github.com/WuKongIM/helm/tree/b0eddcfce07f6be8e90ba1f4fecd6fa21fc894cd/charts/vera-byte-wkim) 是 `v2.1.5-20250424`：它使用硬编码的 `helm-wukongim` StatefulSet、headless Service、RWO PVC、NodePort 和 `externalIP`，镜像为 `registry.cn-shanghai.aliyuncs.com/wukongim/wukongim:v2.1.5-20250424`。启动命令 `/home/app --config=/root/wukongim/wk.yaml --ignoreMissingConfig=true`、`WK_CLUSTER_NODEID` 和 `WK_CLUSTER_SERVERADDR` 都属于旧运行契约。

该 chart 没有 readiness/liveness probes、PDB、NetworkPolicy、anti-affinity/topology spread、container security context 或备份编排；模板还同时声明了可疑的同名 volume 与 volumeClaimTemplate，并依赖未被 Kubernetes 自动插值的 `$(POD_NAME)` 式值。它可以作为 v2 历史考古，不能作为 v3 安装模板。

### 新文档必须覆盖的 Kubernetes 契约

- 使用 StatefulSet 获得稳定 ordinal、DNS 和 per-Pod PVC；节点 ID 与数据目录必须在重启后稳定。[StatefulSet](https://kubernetes.io/docs/concepts/workloads/controllers/statefulset/)
- 使用 headless Service 建立节点发现；客户端/API、WebSocket、Manager 和集群内部流量应分离暴露。[Headless Services](https://kubernetes.io/docs/concepts/services-networking/service/#headless-services)
- `startupProbe`/`livenessProbe` 指向 `/healthz`，`readinessProbe` 指向 `/readyz`；滚动时一次只推进一个 Pod并等待 ready。[Pod probes](https://kubernetes.io/docs/concepts/configuration/liveness-readiness-startup-probes/)
- 每 Pod 使用独立持久卷；明确 StorageClass、扩容、快照、恢复和访问模式。`ReadWriteOnce` 表示单节点挂载约束，不等于单 Pod。[PersistentVolume access modes](https://kubernetes.io/docs/concepts/storage/persistent-volumes/#access-modes)
- 给出基于实际容量测试的 requests/limits，而不是伪造通用生产值。[Container resources](https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/)
- 使用 PodDisruptionBudget、跨节点/可用区拓扑分散和受控维护，但明确 PDB 只约束自愿中断。[Disruption budgets](https://kubernetes.io/docs/concepts/workloads/pods/disruptions/#pod-disruption-budgets) [Topology spread](https://kubernetes.io/docs/concepts/scheduling-eviction/topology-spread-constraints/)
- Manager JWT、join token 和其他凭据进入 Secret；同时说明 Kubernetes Secret 默认并非静态加密保险箱。[Secret good practices](https://kubernetes.io/docs/concepts/security/secrets-good-practices/)
- 用 NetworkPolicy 限制管理面、节点间和客户端入口；先确认 CNI 实现支持策略。[NetworkPolicy](https://kubernetes.io/docs/concepts/services-networking/network-policies/)
- 固定镜像 digest 和 pull policy，配置 `preStop`/终止宽限，解释终止流程和服务摘流。[Pod termination flow](https://kubernetes.io/docs/concepts/workloads/pods/pod-lifecycle/#pod-termination-flow)
- 把扩容写成“创建 Pod/PVC → join/activate/onboarding → 等待状态稳定”，把缩容写成“plan → drain → remove → 再减少 replicas/PVC 决策”；不提供危险的一步命令。
- 升级前备份并验证恢复；一次滚动一个节点；每步检查 `/readyz` 和集群状态；保留 [Helm upgrade](https://helm.sh/docs/helm/helm_upgrade/) 与 [Helm rollback](https://helm.sh/docs/helm/helm_rollback/) 的发布历史，但不把 Helm rollback 等同于数据格式回滚。

### 未验证与发布门禁

目前没有与 v3 当前 commit 对齐的官方 chart、镜像 digest、StorageClass、Kubernetes 版本/CNI/CSI 矩阵或三节点故障演练收据。因此新页可以提供结构化模板、字段说明和操作检查表，但必须标成 Beta，并禁止“官方生产 chart”“自动高可用”“自动备份”“仅改 replicas 即可安全扩缩容”等声明。

## Android 三页

### 平台能力（`S/A/U/B`）

`1.5.5` 的 [构建配置](https://github.com/WuKongIM/WuKongIMAndroidSDK/blob/662a559a50d181540a0448454beb57e939b0c50e/wkim/build.gradle) 声明 minSdk 21、compile/target 34 和 Java/Kotlin 17；[Manifest](https://github.com/WuKongIM/WuKongIMAndroidSDK/blob/662a559a50d181540a0448454beb57e939b0c50e/wkim/src/main/AndroidManifest.xml) 只声明 `ACCESS_NETWORK_STATE`。源码有 `ConnectivityManager` 网络判断、重连和心跳，并为 Android 8/8.1 的 VPN capability 判断提供特殊回退。

这不构成系统版本运行矩阵。源码中未见 Activity/Process lifecycle observer、WorkManager/JobScheduler、foreground service 或 FCM transport；离线同步由宿主通过 provider/callback 注入。文档应写“SDK 未内置这些宿主集成，需产品自行设计并验证”，而不是“Android 不支持”。

生产阻断项：连接层使用 xSocket 裸 TCP，未展示可认证 TLS 路径；Token/UID 存入普通 SharedPreferences；SQLCipher key 由 UID 派生；解码后的 `WKReceivedMsg.toString()` 包含 payload 并进入日志。[连接实现](https://github.com/WuKongIM/WuKongIMAndroidSDK/blob/662a559a50d181540a0448454beb57e939b0c50e/wkim/src/main/java/com/xinbida/wukongim/message/WKConnection.java) [本地数据库](https://github.com/WuKongIM/WuKongIMAndroidSDK/blob/662a559a50d181540a0448454beb57e939b0c50e/wkim/src/main/java/com/xinbida/wukongim/db/WKDBHelper.java) [接收包模型](https://github.com/WuKongIM/WuKongIMAndroidSDK/blob/662a559a50d181540a0448454beb57e939b0c50e/wkim/src/main/java/com/xinbida/wukongim/protocol/WKReceivedMsg.java)

### API 参考（`S/A`）

入口 [WKIM.java](https://github.com/WuKongIM/WuKongIMAndroidSDK/blob/662a559a50d181540a0448454beb57e939b0c50e/wkim/src/main/java/com/xinbida/wukongim/WKIM.java) 提供 init、debug/log、file/device/version 配置，以及消息、连接、Channel、Channel member、会话、Reminder、CMD、Robot manager。完整页面应按以下组覆盖，而不是只列一个“常用 API”表：

- Connection：connect/disconnect、地址 provider、连接状态监听；
- Message：内容注册/解析、send/sendWithOptions、DB/历史/搜索、SENDACK/状态/新消息/刷新监听、离线与 Channel sync provider、附件、extra、reaction；
- Channel：缓存、拉取、搜索、状态、mute/follow/top/save/remark/avatar 与 refresh；
- Channel member：query/search/page/sync/add/remove/refresh；
- Conversation：列表/查询/更新/删除、未读、extra、同步和监听；
- Reminder、CMD、Robot，以及 entity、message content、protocol 和 callback/interface 类型。

公开 Java 源码树和精确 AAR 是 API 权威。README、本地 Maven 发布脚本和运行时 `getVersion()` 不是版本权威：该 tag 中 `WKIM.getVersion()` 仍返回 `V1.5.0`，本地 publication 仍写 `1.0.7`，两者都是需要在文档中避免的制品异常。[manager 源码树](https://github.com/WuKongIM/WuKongIMAndroidSDK/tree/662a559a50d181540a0448454beb57e939b0c50e/wkim/src/main/java/com/xinbida/wukongim/manager)

### 升级 1.5.4 → 1.5.5（`S/A`）

精确 diff 只有 Android 8/8.1 网络判断改动：`WKIMApplication.isNetworkConnected` 在 VPN capability 判定路径失败时回退到 `getActiveNetworkInfo().isConnected()`；公开 Java 签名没有改变。[版本比较](https://github.com/WuKongIM/WuKongIMAndroidSDK/compare/1.5.4...1.5.5)

迁移检查应是：固定 1.5.5 AAR、清理重建、在 Android 8/8.1 上分别验证 VPN/非 VPN 的连接、断线重连、网络切换与消息收发。无需编造 call-site 迁移，也不能把这个窄修复写成所有 Android 版本的 VPN 兼容性认证。

## iOS 三页

### 平台能力（`S/A/U/B`）

源码 podspec 声明 iOS 11，但 `1.1.1` framework 仓库的 podspec 同时出现 deployment target 13.0 与 platform 11.0，且 universal framework 虽含 x86_64/arm64，却排除了 simulator arm64。文档必须把这些写成制品声明冲突，并要求在目标 Xcode/模拟器/真机组合上验证，而不能拍板一个“最低支持 iOS”。[源码 podspec](https://github.com/WuKongIM/WuKongIMiOSSDK/blob/89bf9a1b95ce374caabdd8031d69cc8844d825ae/WuKongIMSDK.podspec) [framework podspec](https://github.com/WuKongIM/WuKongIMiOSSDK-Framework/blob/0cbfb99f18010fe76b7e13ed31b5d1ad4664b10c/WuKongIMSDK.podspec)

源码有内部重连和心跳，但未见 UIApplication lifecycle observer、background task、APNs transport 或 reachability monitor；示例生命周期方法为空。离线拉取/确认通过 `setOfflineMessageProvider` 交给宿主后端。这些是“未内置/未验证”，不是对产品可实现性的否定。

生产阻断项：GCDAsyncSocket 使用 `connectToHost:onPort:`，未见 `startTLS`；连接与 coder 路径存在无条件 `NSLog` 原始包或解码包；SQLCipher 数据库 key 是 UID。[连接管理器](https://github.com/WuKongIM/WuKongIMiOSSDK/blob/89bf9a1b95ce374caabdd8031d69cc8844d825ae/WuKongIMSDK/Classes/manager/WKConnectionManager.m) [数据库](https://github.com/WuKongIM/WuKongIMiOSSDK/blob/89bf9a1b95ce374caabdd8031d69cc8844d825ae/WuKongIMSDK/Classes/db/WKDB.m)

### API 参考（`S/A`）

[WKSDK.h](https://github.com/WuKongIM/WuKongIMiOSSDK/blob/89bf9a1b95ce374caabdd8031d69cc8844d825ae/WuKongIMSDK/Classes/WKSDK.h) 暴露 options、connection、chat、channel、media、coder/body coder、conversation、CMD、receipt、reaction、robot、pinned、reminder、flame managers，以及消息内容注册、离线 provider 和文件任务入口。

分发 framework 的 [Headers 目录](https://github.com/WuKongIM/WuKongIMiOSSDK-Framework/tree/0cbfb99f18010fe76b7e13ed31b5d1ad4664b10c/ios/WuKongIMSDK.framework/Headers) 是最接近消费者的 API 制品面。但 umbrella 同时暴露 manager/model/protocol/provider 以及数据库、队列、工具类等实现头文件。参考页必须把“推荐 app-facing SDK/managers/models/protocols/providers”和“public-but-not-recommended implementation headers”分栏；`PrivateHeaders` 不进入公共参考。

### 升级 1.1.0 → 1.1.1（`S/A`）

源码行为变化位于 `WKChatManager.filterNoCMDAndNoStreamMessages`：1.1.1 不再过滤 `isDeleted != 0`，因此已删除的非 CMD 消息可能重新出现在同步后返回的列表中；公开 header 签名没有变化。[版本比较](https://github.com/WuKongIM/WuKongIMiOSSDK/compare/1.1.0...1.1.1) framework 的公开 Headers 未变但 binary 变化，framework podspec 正确写 1.1.1；source tag 中 podspec 仍写 1.1.0。

迁移检查应覆盖删除消息在历史、同步结果和会话列表中的可见性，并同时记录 source revision 与 framework revision。没有证据要求调用方改签名，不应编造源码级迁移步骤。

## Flutter 三页

### 平台能力（`S/A/U/B`）

根入口 [lib/wkim.dart](https://github.com/WuKongIM/WuKongIMFlutterSDK/blob/de1024276523119e38305c49a3a873caae4d5c59/lib/wkim.dart) 暴露 `WKIM`，源码使用 `dart:io` 的 raw `Socket.connect`，并依赖 sqflite、connectivity_plus、shared_preferences。因此当前源码本身不支持 Flutter Web；pub.dev 展示的 Android/iOS/macOS 标签是包元数据，不是本报告拥有的运行收据。[pubspec](https://github.com/WuKongIM/WuKongIMFlutterSDK/blob/de1024276523119e38305c49a3a873caae4d5c59/pubspec.yaml)

SDK 内没有 `WidgetsBindingObserver`。官方 example 通过 `SystemChannels.lifecycle` 在 paused 时 disconnect、resumed 时 reconnect，说明生命周期所有权在宿主；未见 FCM/APNs transport。连接层有网络监听、重连和心跳，数据同步/上传由宿主 callback 提供，均不证明后台存活。

生产阻断项：连接使用 raw `Socket` 而非 `SecureSocket`；`WKOptions.debug` 默认 true；日志会输出解码后的接收包。发布前还需单独审计数据库、shared preferences 和密钥处理。[连接管理器](https://github.com/WuKongIM/WuKongIMFlutterSDK/blob/de1024276523119e38305c49a3a873caae4d5c59/lib/manager/connect_manager.dart) [配置](https://github.com/WuKongIM/WuKongIMFlutterSDK/blob/de1024276523119e38305c49a3a873caae4d5c59/lib/common/options.dart) [日志](https://github.com/WuKongIM/WuKongIMFlutterSDK/blob/de1024276523119e38305c49a3a873caae4d5c59/lib/common/logs.dart)

### API 参考（`S/A`）

`WKIM` 提供 setup/options/runMode，以及 connection、message、conversation、channel、channel member、reminder、CMD managers。完整参考应覆盖：

- connect/disconnect、地址 provider、连接状态；
- 内容注册、send、message DB/history/search、ACK/status/listeners、offline/channel sync providers、extra/reaction；
- Channel 缓存/拉取/搜索/状态/刷新；Channel member 查询/分页/同步；
- conversation CRUD、未读、extra、同步和监听；
- reminder、CMD、models、content、constants 和 callback types。

关键导入边界：Dart 的 `import` 不会自动重导出被导入库的符号，且 `wkim.dart` 没有 `export` 全部 manager/model/type。消费者往往需要 `package:wukongimfluttersdk/manager/...` 或其他深层路径；页面必须明确“根入口”和“当前制品可见的深层路径”，不能把深层路径承诺成长期稳定 API。[lib 树](https://github.com/WuKongIM/WuKongIMFlutterSDK/tree/de1024276523119e38305c49a3a873caae4d5c59/lib)

### 升级 1.7.7 → 1.7.9（`S/A`）

1.7.7 源码基线是 `d99990f41ecb31166af82b9d20c121f33ff8385d`，1.7.9 匹配 `de1024276523119e38305c49a3a873caae4d5c59`；中间发布语义包括：

- 1.7.8 增加 `getMaxReactionSeqWithChannel`；
- 1.7.9 的 conversation `queryAll` 会填充 last message、message extra 和 sender；
- `WKMsg` 增加 `getFromAsync`、`getMemberOfFromAsync`；
- reaction insert 改为 await。

[精确比较](https://github.com/WuKongIM/WuKongIMFlutterSDK/compare/d99990f41ecb31166af82b9d20c121f33ff8385d...de1024276523119e38305c49a3a873caae4d5c59) 共 184 行新增、13 行删除。迁移测试应重点覆盖过去为 lazy/null 的会话列表字段、sender/member 查找和 reaction sequence。因为没有 1.7.9 tag，页面必须固定 pub archive/hash 与匹配 commit，不能用 main 代替发布身份。

## HarmonyOS 三页

### 平台能力（`S/A/U/B`）

HAR metadata 声明 compile SDK `6.1.1.125`、release，并面向 default/tablet；[module.json5](https://github.com/WuKongIM/WuKongIMHarmonyOSSDK/blob/0c41810a1e0a5fc2936929d63ca32a50ffb11bec/wkim/src/main/module.json5) 只申请 INTERNET 与 GET_NETWORK_INFO。源码使用 `@ohos.net.socket` TCPSocket 和 `@ohos.net.connection` 网络监听/重连。没有 DevEco、模拟器或真机运行收据，因此这些只能作为 source/artifact facts。

官方 example 的 EntryAbility 前后台回调只记录日志；SDK 内没有生命周期、推送/通知或后台任务 API。文档应明确由宿主负责，但不能把源码缺失夸大成 HarmonyOS 产品不可能实现。

生产阻断项：TCPSocket 未配置 TLS；本地 relational store 使用 `encrypt: false`、security level S1；logger 使用 `%{public}s` 输出接收包，包的 `toString()` 包含 payload，且部分 send/status 路径直接调用 `hilog.info` 绕过 logger switch。[ConnectionManager](https://github.com/WuKongIM/WuKongIMHarmonyOSSDK/blob/0c41810a1e0a5fc2936929d63ca32a50ffb11bec/wkim/src/main/ets/manager/ConnectionManager.ets) [WKDBHelper](https://github.com/WuKongIM/WuKongIMHarmonyOSSDK/blob/0c41810a1e0a5fc2936929d63ca32a50ffb11bec/wkim/src/main/ets/db/WKDBHelper.ets) [WKLogger](https://github.com/WuKongIM/WuKongIMHarmonyOSSDK/blob/0c41810a1e0a5fc2936929d63ca32a50ffb11bec/wkim/src/main/ets/common/WKLogger.ets)

### API 参考（`S/A`）

[package 根入口](https://github.com/WuKongIM/WuKongIMHarmonyOSSDK/blob/0c41810a1e0a5fc2936929d63ca32a50ffb11bec/wkim/index.ets) 只导出 `WKIM`；[WKIM.ets](https://github.com/WuKongIM/WuKongIMHarmonyOSSDK/blob/0c41810a1e0a5fc2936929d63ca32a50ffb11bec/wkim/src/main/ets/WKIM.ets) 提供 init 和 channel、channel member、message、CMD、connection、conversation、reminder managers。

参考页应逐组覆盖 manager 方法、provider/callback、entity、message content、protocol、constants/status types；同时醒目标示 manager/model 的 `src/main/ets/...` 深层导入只是该精确 HAR 的制品事实，不是 package root 的稳定导出契约。

### 升级 1.1.6 → 1.1.7（`S/A`）

基线 `a79df83f2794c581096850f0f77d34b95566a9ae` 到 `0c41810a1e0a5fc2936929d63ca32a50ffb11bec` 的变化包括：

- Channel 增加 `getWithFollowAndStatus`；
- Message 增加 `getMinMessageSeqWithChannel`、`getMaxReactionSeqWithChannel`、`getMessageOrderSeq`；
- Conversation 增加 `updateMsgExtra`、`getWithChannel`、`getMsgExtraWithChannel`；
- 连接 attempt/timer/reconnect 行为变化，并在初始化时把 sending 消息置为 failed；
- 同步持久化 message extra/reaction，且增加若干日志。

[精确比较](https://github.com/WuKongIM/WuKongIMHarmonyOSSDK/compare/a79df83f2794c581096850f0f77d34b95566a9ae...0c41810a1e0a5fc2936929d63ca32a50ffb11bec) 应驱动编译检查、断网重连、应用恢复、sending 状态、extra/reaction 持久化和新增查询 API 的测试。由于没有 tag，版本身份必须锁 exact HAR/hash 与源码区间。

## JavaScript 两页

### API 参考（`S/A/U/B`）

公共入口是 [src/index.ts](https://github.com/WuKongIM/WuKongIMJSSDK/blob/3c507ea3ebc08eae9d74fc1f76b150c380752008/src/index.ts) 和 npm 包内 `lib/*.d.ts`；[src/sdk.ts](https://github.com/WuKongIM/WuKongIMJSSDK/blob/3c507ea3ebc08eae9d74fc1f76b150c380752008/src/sdk.ts) 是范围更窄的旧重复实现，不应作为 API 权威。

`WKSDK` 暴露 config、content、connect、chat、channel、task、conversation、reminder、security、receipt、event managers，以及 content 注册/工厂、system content 检查、connect/disconnect、subscribe/unsubscribe 和模型构造器。根模块还导出 model、const、conversation/connect/proto/chat/task/channel/provider/event/config。完整参考应按以下组整理声明：

- config/provider、connect status/delay 与地址提供；
- chat send/sync/listeners/status；
- Channel/subscriber/cache；
- conversation/unread/sync；
- receipt/reminder/event/task；
- models、protocol packets 与 enums。

[1.3.5 package export map](https://github.com/WuKongIM/WuKongIMJSSDK/blob/3c507ea3ebc08eae9d74fc1f76b150c380752008/package.json) 只声明 `.` 和 `package.json`，所以 `lib/*` 深层导入不是稳定公共 API。审计时生成的 `docs-site/out/compatibility.json` 明确为 `verified: false`、verification `missing`；生成器在没有精确匹配收据时会 fail closed。[兼容性快照生成器](../../../docs-site/lib/developer-contracts.ts) 即使未来场景通过，接收报告契约仍固定 `production_readiness.result=not_assessed`、`publication_attestation=not_issued`，且只覆盖所列 Node/Chromium/服务端 tuple，不能外推到 Safari、Firefox、Node 服务端或小程序。[接收报告契约](../../../docs-site/examples/javascript-web-quickstart/src/acceptance/report.ts)

生产阻断项：源码会无条件 `console.log` 解密后的接收包和重试 SendPacket；`debug=false` 不足以关闭这些输出。浏览器 Token/地址必须来自受信 BFF，生产必须使用 WSS，并审计最终 bundle、sourcemap 和日志收集器；构建期删除 console 的意图不能代替逐制品检查。[WebSocket 实现](https://github.com/WuKongIM/WuKongIMJSSDK/blob/3c507ea3ebc08eae9d74fc1f76b150c380752008/src/websocket.ts) [连接管理器](https://github.com/WuKongIM/WuKongIMJSSDK/blob/3c507ea3ebc08eae9d74fc1f76b150c380752008/src/connect_manager.ts)

### 升级 1.3.4 → 1.3.5（`S/A`）

1.3.4 源码基线是 `533a60cdd1b9229fc4a87d7d22b5b860eb4aa43c`。直接升级有一个真实 API/行为迁移：`WKEvent.dataText?: string` 改为 `dataJson?: any`，构造器会对解码文本执行 `JSON.parse`。[精确比较](https://github.com/WuKongIM/WuKongIMJSSDK/compare/533a60cdd1b9229fc4a87d7d22b5b860eb4aa43c...3c507ea3ebc08eae9d74fc1f76b150c380752008)

迁移必须搜索 `dataText`、改用 `dataJson`，并测试合法 JSON、结构变化以及服务器发送非 JSON 文本时的失败路径。1.3.4 tarball SHA-256 为 `463b76613fc35c66fbec0d7f9bd8b5802a5b2a26f8e17954d9cd1b82b88fafd0`；它还包含 1.3.5 已删除的孤立 `agent.d.ts`、`agent_manager.d.ts`、`stream_manager.d.ts`。这些文件未从根 index/export map 导出，且 1.3.4 对应源码没有匹配实现，因此应记录为制品清理，不能宣称根公共 API 被删除。

若读者从 1.3.0 `3747f4477829cf87d9003725038506aa5591b1ab` 跨升到 1.3.5，还必须另列：默认 `protoVersion` 4→5、`sdkVersion` 从固定 `1.2.8` 改为构建生成、stream manager/Stream/stream fields 移除、event manager/WKEvent/EventType 加入。[跨版本比较](https://github.com/WuKongIM/WuKongIMJSSDK/compare/3747f4477829cf87d9003725038506aa5591b1ab...3c507ea3ebc08eae9d74fc1f76b150c380752008) 这些不是 1.3.4→1.3.5 的全部直接变化，升级页必须按起始版本分节。

## 跨页面发布约束

1. 中英文页面必须同步落地；15 个路由的状态只能在内容、导航和 contract tests 同步满足后一起取消“规划中”。
2. 每个 API 页首屏写清 SDK 版本、源码 commit、发布渠道和制品身份；深层 import、implementation header 和未从 package root 导出的类型必须单独标注。
3. 每个平台能力页采用“已从源码确认 / 已从制品确认 / 运行验证 / 未验证 / 生产阻断”矩阵，禁止把 repo 声明、商店标签或示例工程当运行收据。
4. 每个升级页固定 `from`、`to`、精确 diff、破坏性/行为变化、迁移步骤和回归清单；不得写无边界的“升级到最新版”。
5. 示例不得含真实 Token、Manager JWT、join token 或可用公网地址；生产连接必须要求 TLS/WSS 或明确当前 SDK 缺口。
6. “自动重连”只描述当前进程和网络事件内的实现；后台挂起、系统杀进程、推送唤醒和离线同步是不同能力。
7. “本地数据库加密”“消息协议加密”“HTTPS 获取地址”均不能单独证明端到端传输、凭据存储和日志安全。
8. Kubernetes 页发布前至少需要 v3 manifest/chart 静态契约测试；若要升级为 Stable，还需精确 Kubernetes/CNI/CSI/镜像的安装、滚动升级、故障、扩缩容、备份恢复收据。

## 来源清单

### 当前服务端与 Kubernetes

- [WuKongIM local frozen source](../../../) at `6ccaf7f442900a3bfdf345cae2b055cf5a1e72cd`
- [Historical official Helm chart](https://github.com/WuKongIM/helm/tree/b0eddcfce07f6be8e90ba1f4fecd6fa21fc894cd/charts/vera-byte-wkim)
- [Kubernetes StatefulSet contract](https://kubernetes.io/docs/concepts/workloads/controllers/statefulset/)
- [Kubernetes probes contract](https://kubernetes.io/docs/concepts/configuration/liveness-readiness-startup-probes/)
- [Kubernetes storage contract](https://kubernetes.io/docs/concepts/storage/persistent-volumes/)
- [Kubernetes disruption contract](https://kubernetes.io/docs/concepts/workloads/pods/disruptions/)
- [Kubernetes network policy contract](https://kubernetes.io/docs/concepts/services-networking/network-policies/)

### SDK 源码与制品

- [Android 1.5.5 source](https://github.com/WuKongIM/WuKongIMAndroidSDK/tree/662a559a50d181540a0448454beb57e939b0c50e)
- [iOS 1.1.1 source](https://github.com/WuKongIM/WuKongIMiOSSDK/tree/89bf9a1b95ce374caabdd8031d69cc8844d825ae)
- [iOS 1.1.1 framework](https://github.com/WuKongIM/WuKongIMiOSSDK-Framework/tree/0cbfb99f18010fe76b7e13ed31b5d1ad4664b10c)
- [Flutter 1.7.9 matching source](https://github.com/WuKongIM/WuKongIMFlutterSDK/tree/de1024276523119e38305c49a3a873caae4d5c59)
- [HarmonyOS 1.1.7 matching source](https://github.com/WuKongIM/WuKongIMHarmonyOSSDK/tree/0c41810a1e0a5fc2936929d63ca32a50ffb11bec)
- [JavaScript 1.3.5 source](https://github.com/WuKongIM/WuKongIMJSSDK/tree/3c507ea3ebc08eae9d74fc1f76b150c380752008)

## 方法与限制

本次只采用官方 WuKongIM 仓库、精确发布制品/registry 元数据、当前仓库锁定源码和 Kubernetes/Helm 官方契约作为当前事实来源。旧站只用于旧路由映射、主题覆盖和教学顺序。README、搜索摘要、第三方博客、包索引的营销标签和未锁定的 main 均不作为兼容性结论。

源码“未检出某能力”只表示在审计范围内没有找到 SDK 内置实现，不证明宿主应用、系统服务或未来版本不能实现。所有运行矩阵结论都需要新的可复现收据；本报告本身不是兼容性认证或安全审计通过证明。
