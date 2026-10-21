# 本地 shakeout 外层执行器收尾修复

唯一实现目标为同目录 run-shakeout-once.py；没有修改 shipping 仓库源码，没有编译、启动产品或运行30分钟实机工作负载。

先保留 before（SHA256 65e5900dcc40736198d57e6be7a38a8229095880b844b4b5bb20999841999d94）和 failure-inventory.json，再用 probe.py 执行实际 lifecycle AST 及原函数，注入假子进程/observer/元数据I/O，将超时字面量缩短。这不是整体CLI预检或真实产品E2E；作用是重现独立进程session的终止与状态传播。假节点调用setpgid形成独立PGID并忽略TERM，另外一个独立session的sentinel必须存活。首红仅正常exit0通过；exit8/130、TERM、postspawn异常、collector异常和timeout均暴露问题，详见red/summary.json。测试supervisor仅使用记录的fake PID回收首红遗留。

修复复用 shipping scripts/run-v3-candidate-check.py 的 session_members / cleanup_session，不复制或修改它。每个被信号的PID由该函数即时复核SID；禁止名称匹配/global pkill。本helper的raw exit、wrapper exit、final report、owned cleanup、observer异常及来源校验各自写字段。计划stop130继续传播130，单独标记helper_planned_operator_stop；不会把它伪装成exit0，也不把collector问题标成产品失败。正常/异常/取消均在finally清理；硬SIGKILL、宿主丢失、不可写磁盘无法保证Python完成finally或写收据。

所有7个同一probe用例修复后通过（green/summary.json）：0/8/130保留；TERM=143、异常/collector=1、timeout=124；每次owned_session.verified=true、remaining_processes=[]，外部sentinel全部存活。helper/final/cleanup具体字段见green-result-fields.json。仅运行轻量假进程，约9秒。

原工作负载调用仍为 helper --run-dir RUN --stop-after 1800，没有新增速率/阈值/测量覆盖；Go/toolchain/cache设置不变。既有host metrics15秒/process.prom5秒收集不变，process.prom改为读取最多65537字节再判64KiB。新增复用的cleanup脚本进入source_files_sha256冻结。粗外层wait仍3000秒，取消/超时先给helper120秒正常收尾，随后SID限定TERM/KILL收尾、observer最多15秒join。

父代理需审runner.patch后才能启动已授权真实run。原before、red和原产品首失败均保留。归档清单只含代码/patch/JSON/日志，无产品二进制或缓存。

最终 generation-2 使用冻结 cleanup 脚本的原始源码 compile/exec，避免在候选worktree生成或信任ignored字节码缓存。首代fake probe的唯一cache文件已按精确路径删除，元数据见generation-1-bytecode-cleanup.json。重新运行同一7case全部通过，证据green-no-bytecode/summary.json；最终runner/run-shakeout-once.final.py SHA256为6caeb97707bcc49f93cb3881819dc5a79258672c4ee1f540dbba10a894d0dda9，最终差异runner-final.patch。首代green和after副本未覆盖。
