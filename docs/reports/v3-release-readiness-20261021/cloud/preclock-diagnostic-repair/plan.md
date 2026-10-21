# 最小 pre-clock 诊断切片

基线 fc18221282f289088ec9b4282fda8e0bfc67a3b8。原 run 39581355444 的初始 readiness 原因仍未知；现有只证明部署通过、无 run-start、repair 状态发布失败、精确释放。此次只补未来可判别证据，不声称修复产品根因。

有序假设：
1. 服务快速 terminal，且 coordinator summary 缺失/不属于现有严格语法。预测原 ActiveState/Job classifier 为 terminal，Result/ExecMainCode/Status 或安全 summary code 可区分。
2. SSH 读取失败或 classifier 拒绝响应。预测命令 exit 非 0，或命令 0 / classifier 非 0；需分别保留而非只记 unreachable。
3. 实际 deadline 或墙钟异常。预测 break 时 now >= deadline 且原状态 running/pending；旧约 18 秒不足以支持正常 7200 秒耗尽。

范围：stage-orchestrate.sh、一个 pre-clock-diagnostic.py、现有 classify-pre-clock-summary.sh 的显式诊断 JSON 模式、两个新测试文件。root 维护说明/Changelog。Issue preflight由 ci_audit 独占实现，当前切片只在 output/work dirs 创建后调用 --preflight，失败原 set-e 退出，在 bundle/Quote/key/Acquire 前。

行为边界：原 timeout 60 / readiness deadline / sleep / retry / operator-stop / terminal classification均保持。原 SSH 调用由窄 shell capture 函数保留真实 exit，stdout只在进程内，stderr经恒定内存流式 bytes/SHA collector，不落原文。state那一次 systemctl show增取 LoadState/SubState/Result/ExecMainCode/ExecMainStatus；仅原 ActiveState/Job仍交原classifier决定。新增属性非法或缺失只使诊断 incomplete，不改状态决定。原有terminal summary查询复用原cursor与1025byte上限，不额外扫journal。

只在决定退出后、kill之前冻结 receipt：request/source/control/bundle/generation、start/SSH/classifier exit、原状态分类、精确break branch、观察now/deadline、固定systemd枚举及数字、strict summary语法/已知code、各输入bytes/hash。没有观测过的属性明确unavailable，start失败时不增发状态SSH来补假完整证据。kill后不覆盖。诊断目录每代独立，原raw env/journal/stderr/manager/token均不公开。

status/readiness label只改为实际观察分支（start_command_failed、state_read_failed、state_parse_failed、terminal_before_run_start、deadline_elapsed）；终止/repair流程仍由原条件决定。diagnostic write失败保留安全incomplete状态，不能使原Release、exit code或cleanup跳过。summary的诊断模式可输出preflight安全code，默认模式仍把preflight/unknown留在原repair路径；不能让新增元数据驱动业务判定。

先红：元数据CLI严格字段/重复/超界/secret-canary；default与诊断mode决定分离；真实Bash capture函数+fake SSH返回0/17/255且stderr只留bytes/hash。继续补kill前调用顺序、实际deadline分支、state/summary失败和diagnostic输出不可写仍cleanup、未知code/多行/超长隐私负例。过程测试放integration tag，静态/解析测试默认层。无云、无网络API、无生产负载。
