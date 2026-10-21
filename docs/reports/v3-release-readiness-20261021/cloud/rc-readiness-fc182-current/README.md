# fc182 云 RC：GitHub 只读 readiness 刷新

09:46:56–09:47:41 UTC（北京时间17:46:56–17:47:41）的 GET 快照没有发现新的 GitHub 配置阻塞。远端 main 前后均为 **fc18221282f289088ec9b4282fda8e0bfc67a3b8**，tree **3254faa77819a1dc7c4b7230b567ec1115be5a7a**，与已验收 #1017 相同。此快照采集时仅有计划授权，未做付费操作；后来的人类付费授权须另存收据，不改本快照。

- 七项 allowlisted OIDC public variables 和 wrapping public key 全部存在，值与04:56快照相同。ARN账号数字已隐去，仅留值摘要；Secret只读取名称/时间元数据。
- 四个 Environment 存在，均无 required-reviewer/branch policy，和旧快照相同；这是现有无人值守设计，不新增审批要求，也不能宣称 Environment 限制了 main。OIDC subject 包含 repo/context/job_workflow_ref；main protection及rulesets与旧快照相同。
- cloud-deployment 有 `WK_CHAT_LIFECYCLE_WRAPPING_PRIVATE_KEY` 名称；三个 Lease Environment 无 Secrets。仓库有两个旧 AK 名称；本任务没有读取值或使用这些凭据。所有四 Environment 的 variable 数均为0，仓库 variable 列表只保留名称/时间，不保留非allowlist值。
- 十二个相关工作流状态不变。formal、两个finalize、generic release 为 `disabled_manually`；顶层rehearsal等其余八项active。空闲时禁用状态不等于缺失；获准启动后由现有owner按合同证明cleanup backstop已arm，不能手工旁路。
- 全仓库 queued/in_progress/waiting/requested/pending 各0，五次查询各完整一页。artifact全部505项、6页，ID唯一且total稳定；相关chat-lifecycle/cloud-lease metadata共136项，与旧445项全仓快照中的相关136个ID完全相同。无handoff、transition、cleanup-pending命名artifact；仍有11个8月历史operator-stop marker，完整列表在summary.json。没有下载payload。

**以上不能证明 live OIDC、wrapping 公私钥匹配或云端库存为零。** cleanup-pending可以位于handoff内部；删除/过期证据不可由当前列表恢复。31个cleanup命名artifact不是31份新验证的zero proof。请求必须使用新ID；不复用旧stop marker。这是顺序读取的时点证据，执行前原入口仍需验证，不是未来状态保证。

## 可审批范围与执行边界

最终source为fc182；[既有离线计划](../rc-plan-ab75-premerge/README.md)与plan.json已绑定该source/tree。固定杭州同区同型4台4核8GiB：3台service各40GiB系统盘+500GiB数据盘；1台load为40+200GiB、20Mbps EIP，临时SSH/HTTP公网范围遵循ADR0042。预演2h后释放并证明exact zero，再新Lease连续72h、同老化数据容量≤8h、2000 SEND/s恢复30m。整个request累计预算¥1500，估算累计¥1350停止，含两Lease、诊断、流量和清理；不是每阶段单独预算，未进行Quote。

付费审批文字：

> 授权对 fc18221282f289088ec9b4282fda8e0bfc67a3b8 开始聊天生命周期全流程压测：按仓库正式 GitHub OIDC 方案在阿里云购买临时资源，执行预演、72小时长稳、容量及恢复，创建跟踪Issue并完成清理。整个请求累计预算上限人民币1500元，估算累计达到1350元停止。

授权后的原入口仍须fail-closed验证：唯一request、实际control/source绑定、live OIDC三角色与现有精确trust/policy、wrapping匹配、旧selector和当前库存、cleanup已arm、Quote预算/配额/可用区，再仅消费该获准Quote。任何失败停止，不自动改IAM、角色或Secrets，不追加预算。

顶层rehearsal自身构建并绑定fc182 bundle；**不为审批额外重复调度独立bundle CI**。旧ed705 bundle不能使用。source及正式阶段bundle固定；子工作流真实control revision分别记录，不能假设main永不前进。

依据：[正式流程授权](https://github.com/WuKongIM/WuKongIM/blob/fc18221282f289088ec9b4282fda8e0bfc67a3b8/.github/workflows/README.md#L965)、[身份/重试边界](https://github.com/WuKongIM/WuKongIM/blob/fc18221282f289088ec9b4282fda8e0bfc67a3b8/.github/workflows/README.md#L1008)、离线plan的36份源文件摘要。Direct Lab不是正式入口，其¥300方案不适用。

## 证据和复核

`collect.py`仅对仓库GitHub API GET，明确分页上界；不调用provider、不dispatch/enable、不改权限/Issue/key。变量list API返回的非秘密值在内存丢弃；只有八项授权allowlist保留脱敏值和摘要。`summary.json`记录完整结果、旧快照差异、历史marker、约束和未执行runtime检查。

```sh
python3 -B summarize.py --snapshot /path/to/this-snapshot --previous /path/to/0456-snapshot --plan /path/to/rc-plan-ab75-premerge/plan.json --output /path/to/new-summary.json
```

输出拒绝覆盖；复算是纯离线转换。原GET未因整理文字重跑。ARCHIVE_ALLOWLIST列明全部小证据，SHA256SUMS逐一绑定；不包含artifact payload、私钥或Secret值。
