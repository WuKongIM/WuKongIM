# Linux signal-join exit255：上游源码线索（只读，未确诊）

**可行动结论：先用新 FD3 trace 判断255来自生产 join 内的 wait，还是其后的裸 `wait "$signaler"`。** 两个 fixture 都有后一条命令，且在 `set -e` 下执行；不能从Go看到的shell exit255反推生产join返回255。当前读取的观察提交为 `76a3ba23303e288345dbd37bc354b41d81b6f070`。本次未测试、编译、修改shipping或读取秘密。

已核事实：

- 原GNU `bash-5.2.21` 的 `jobs.c:2986–2991` 在阻塞 `waitchld` 返回 `-1/ECHILD` 且正在执行wait builtin时，将 `termination_state` 设为`-1`并返回；`wait_for_single_pid`继续返回该值。`builtins/wait.def:261–267`仅将大于256的“非子进程”状态变为127，没有把负值映射到127。`shell.c:1043–1053`最终调用`exit(s)`。这提供一条负内部状态成为进程exit255的源码路径，**没有证明此次命中了它**。摘录含原行号，源文件及原tarball摘要见source-freeze。
- 正常的“已设置trap的TERM中断wait”走 `trap.c:554–560` 的signal记录/longjmp，以及 `wait.def:180–190` 的 `128+signal`，TERM为143。正常“不属于子进程”的显式路径返回257后映射127。**255不能直接解释为正常TERM或正常未知PID。** [GNU Signals说明](https://www.gnu.org/software/bash/manual/html_node/Signals.html)
- Chet Ramey的 **2025-04-08** [Bash 5.3-rc1官方公告](https://lists.gnu.org/archive/html/bug-bash/2025-04/msg00023.html)明确列出修复“等待指定PID时状态偶尔成为255”。这是相关上游症状，不足以证明与当前race相同；未成功取到该修复的准确diff，不能断言升级某版本即可修复本项目。
- 原wait helper使用 `wait "$pid" || status=$?` 后再用`kill -0`判断是否继续；其自身返回0、子状态放在变量中。两个fixture却直接等待另一个早退出的signaler。这使后一等待成为最窄的优先判别点。

待Linux trace证伪的排序：

1. **内部子状态/回收竞态假说。** 若join记录成功，随后 `fixture_phase=signaler_wait` 紧接exit255，且signaler最后`kill`成功，则支持“第二次指定PID wait拿到异常状态”，应进一步在同一Linux包版本上取有界wait4/SIGCHLD/TERM系统调用证据；若255发生在join之前或signaler自身非零，则该具体假说被推翻。
2. **真实子命令退出255或测试错误传播。** 若trace显示signaler/目标子进程或其他命令确实以255退出，wait可能只是正确透传；需追那个命令，不能在helper里忽略255。新的FD3分阶段与PID记录可区分它和上面的内部状态路径。
3. **发行版或调度差异。** 既有Mac二进制虽来自同一5.2.21 tarball，仍是Darwin arm64、独立configure/libc/调度，非Ubuntu构建；20次未复现不能排除此race。先记录Linux `BASH_VERSION`、`command -v bash`及发行包完整版本，再核其patch集合；这比只对齐上游版本字符串可靠。仅当trace确实指向上游wait路径时，再做版本/单补丁对照，保留原runner门禁。

Linux/Darwin边界：当前Mac配置启用JOB_CONTROL、HAVE_POSIX_SIGNALS、HAVE_WAITPID；源码通用分支调用waitpid。`sig.c:811–826`为SIGCHLD设置SA_RESTART，普通trapped TERM并不通过交互专用的SIGTERM重启条件。`jobs.c:3856`的Linux注释讨论的是 **SIGINT** 的内核行为，不能拿它当TERM根因证据。本次没有核实Ubuntu实际包补丁，也没有证据声称Darwin/Linux存在一个已确定的TERM语义差异。

官方源码入口：[原5.2.21 tarball](https://ftp.gnu.org/gnu/bash/bash-5.2.21.tar.gz)（既有下载收据 SHA256 `c8e31bdc59b69aaffc5b36509905ba3e5cbb12747091d27b4b977f078560d5b8`）。本轮Savannah plain来源返回502，GNU 5.3 tarball返回TLS EOF，公告直接下载timeout；这些失败记录保留，公告内容由官方域搜索读取交叉核对。未引用二手修复建议，未把任何未取到的diff当作证据。
