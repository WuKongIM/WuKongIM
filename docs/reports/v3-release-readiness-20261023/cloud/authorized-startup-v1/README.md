# 79a 云 RC：恢复准入、唯一启动与采购证据

截至 2026-10-23 01:31:02 Asia/Shanghai（2026-10-22 17:31:02 UTC），已按用户 `call_dd802aec589f4d058343640027bb7cf9` 的明确批准启动一次云 RC。唯一 request 为 `chat-20261021T214505Z-00299e37`，没有重生 request、重复 dispatch 或增加预算。产品与控制均为 `79a7177032a6ae1ec7015cefe0e6f94eb0f19f06`。本件仅封存已完成的启动/采购核验与所示时点，不声称 2h、72h 或 v3 正式发布通过。

- 跟踪 Issue [#1025](https://github.com/WuKongIM/WuKongIM/issues/1025) 单次创建。创建后首次 Search 未索引（计数 0）的失败记录保留；随后仅只读 Search/GET 对账，唯一 open/unlocked，原源码 `--preflight` 返回 ready，没有重复创建。
- 顶层 [39820950900](https://github.com/WuKongIM/WuKongIM/actions/runs/39820950900) 于 01:14:52 启动，run number 35 / attempt 1 / exact main 79a。原 request 在中断前无 workflow dispatch，恢复时只读完整对账确认；第一次初始 dispatch 已被消费，不得重派。
- 恢复准入重新核对两份冻结计划清单、24 个规则/源文件、远端 main、8 项身份/公钥变量、OIDC subject、所有受保护活跃状态和有界制品库存。直接调用原源码只读认证器，两阶段均为 0 个仍活跃 handoff。不能把存在 cleanup 的历史候选直接视作活跃 Lease；也没有伪称重新独立扫描 provider。
- 本机一次生成的诊断 Ed25519 身份通过公私钥配对，私钥留在原受限本机身份目录，未进入此封存。这里仅含公钥/指纹/非密钥路径记录。
- Bundle 39821046654 首次成功：实际 141,109,198 B ZIP 摘要匹配；110 个 tar 普通文件逐一重算哈希、109 个 manifest 文件匹配、9 个 ELF 为 Linux AMD64；实际 control/product checkout 均为 79a。没有执行下载的 payload。
- Quote 39821268304 首次成功，376 秒。现有总 job 限制 45 分钟，SDK 单次连接/读取为 10/30 秒、发现分页最多 20 页。没有逐 API 计时证据，不能唯一归因于库存枚举或网络等待；没有改期限/取消重派。
- Acquire 39822336878 首次成功。Lease `chat-20261021T214505Z-00299e37-rehearsal-1` 于 01:24:38.761366925 创建；杭州 4 台 4c/8GiB，3 service + 1 load、8 磁盘、1 EIP，全部资源身份标签与 source/bundle/预算精确匹配，临时端口仅原批准 22/80。Quote 与 Acquire 保存的 Quote 字节相同，两个小 ZIP 完整大小/SHA/CRC 均验证。
- 新 request 上限 1276567200、停止线 1126567200 CNY 微元；外部三份历史报价预留 223432800，整项仍 1500000000/1350000000。新完整 Quote 74477600；四份完整报价合计保守预留 297910400 不是账单。本 request 初始 committed=0 只表示合法初始 prior，不表示实际费用为零。
- 原 Acquire 的 arm_release 作业于 01:24:14 成功，早于 01:24:38 的采购；采购后 rehearsal finalizer 和通用 Release 都为 active。本观察器未手工修改调度、IAM 或权限。
- 部署 39822605006 在该时点仍执行原四机 readiness 步骤；尚未观察到远端 run-start 或顶层 release-selector 原文件。当前只验证了 Receipt 身份和全部资源标签，不能以此冒充已经看到 selector 或已开始 2h 计时。后续由原编排及安全调度负责交接、完整证据和最终精确清零。

原 v1/guard-v2 中断记录保持原样，冻结计划/评审已在此前证据分支保存。本封存仅按明确白名单拷贝公开 JSON/脚本/文稿，选择了所列时点的轮询；不是所有中间轮询的全集。完整下载 ZIP、原 job 日志、错误原文、身份私钥及加密 envelope 不复制。未调用第三次 live Analysis MCP、未改源码、未启动新采购请求、未发布 stable。
