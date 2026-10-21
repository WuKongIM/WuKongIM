# 76a3 首轮 Linux：255 发生在 fixture 的 signaler wait

run39572139549 attempt1、scripts artifact12333615019 已完整校ZIP API SHA/size与CRC。受验source974545635cc9be14a6b5f00413eb4bdb56ae0b7b，与PR head76a3ba23303e288345dbd37bc354b41d81b6f070 tree一致。

完整scripts命令545.255s后exit1，不是timeout；600顶层pass/6skip/1fail，无未结束测试，输出未截断，owned SID cleanup已verified/complete且剩余为空。唯一失败是shakeout/attempt_03；首两次通过，第3次失败即停止，observer20次全通过。

failure-trace.txt 直接显示：生产wait先因TERM返回143，确认child仍活，再wait成功，WAIT_CHILD_STATUS=0，生产join返回0、PPROF_EXIT_STATUS=0并清空PID。然后fixture执行wait99189（其Bash signaler），EXIT trap取得内部状态-1、phase=signaler_wait，保留-1退出成为外部255。没有把原失败normalize为0。

因此H1（末尾signaler wait）得到直接支持；H2（本次生产join返回异常）不成立；H3（子进程异常退出/信号）未获得支持，但本次trace不独立提供signaler正常exit的内核记录。GNU上游关于specific-PID wait255的修复是相符线索，尚不能从此证明实际Ubuntu包应用了何patch或就是某一源码行。

这不是产品wait失败的证据。旧observer255未被此单例独立定位，不能仅因新轮20绿归为同一root cause。后续只考虑fixture从Go发送有ready握手的operator TERM，保留真实production join及原退出/无存活断言；此目录不包含该修复或通过宣称。

本地Mac Bash3/5观测各40子例全绿，未复现，不能代替Linux失败。trace与stdout已隔离，只有失败显示至多64KiB且显式截断；本次失败trace完整。其它CI终态仍另代保留，本目录封存后不可修改。
