# PR #1017 d7affe 首轮 Linux scripts 完整失败（第一代冻结）

产品 PR 已普通 push d7affe274935e78a644f901400db15ff1353a2c9；测试 merge61cab03b700ff07439733171cbdcbd9072bfebc5 与 head 的 tree7104e17c912ce1d87030cb08b9fd0fe30b112f3c 一致。没有 merge、手动调度或重跑。

本代只封存已结束的 default 和 scripts artifact，以及提交/验证/源码身份。default完整go-unit通过408.937s。scripts完整命令533.570s后正常返回失败码1（未超时，scripts主包424.312s）；600顶层通过、6跳过、唯一失败TestChatLifecycleShakeoutJoinSurvivesFirstSignal在0.41s报告exit255，无未结束用例。输出未截断，两个命令SID清理均verified/complete且无剩余进程。

旧CI九类已修复失败本代均通过；旧SingleNodeTerminalObserverJoinSurvivesParentSignal也通过0.40s，但不能因此消除旧255未定位记录。新八子例通过，选定worktree fresh-link/VCS fixture39.38s通过。整个门禁仍为失败，后续诊断和仍运行的E2E/native终态将另代归档。

artifact元数据记录ZIP API digest/size；下载时完整SHA256、size、ZIP CRC均核对。轻量归档保留原六文件内容和元数据，不重复ZIP。纯fixture/只读CI，不含生产密钥。原失败未覆盖。
