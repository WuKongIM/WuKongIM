# PR #1024 第二代首次三节点 CI

Run [39659391116](https://github.com/WuKongIM/WuKongIM/actions/runs/39659391116)，attempt1，7 job success、2条件job skipped；没有CI重跑/派发/取消，baseline reuse=false。head `2cc562fa39f7b430c61a4494636f6ee0ec0d631a`；实际事件 checkout `884f88e74b68ec2db26f37bc6659195d24c417a2`，parents cee5+2cc，tree `85f578ba19135892c3d4f5e18db37329cd515cf7`。查询时PR.merge_commit_sha为45c1f5，同parents/tree但不是本次实际checkout；两个Git对象均保留，所有有源码job的实际日志与source.sha已逐项绑定884。早期两个job收集器 receipt 中 synthetic_sha_present=false 是它以当时PR字段45作观察预期的结果，并非源码错配。

| Seam | 三个60s窗 scheduled-to-completion P99 ms | 完成 | errors / drops |
| --- | --- | --- | --- |
| channel-append | 6.872664 / 7.183742 / 9.97001 | 90000 | 0 / 0 |
| mixed-send | 13.234821 / 14.306312 / 17.774992 | 90000 | 0 / 0 |
| tcp-sendack | 178.213185 / 178.222339 / 178.188236 | 90000 | 0 / 0 |

本代九窗各30,000 planned/started/completed，共270,000，均500QPS、低于400ms；另有各60s预热。P99不是最大单请求延迟，不将分布上界相加；不同host CPU型号不作为同硬件因果对照。真实unit/race及arrival饱和集成步骤通过，非verbose日志不能给逐测试skip总数。两个条件skip为Fixed Linux SEND diagnosis、Nightly qualification，未算通过。Mixed SEND flight/阈值profile是否触发以原各receipt为准；不能把未触发等同已执行诊断。

90s smoke：45,995 SEND/ACK、511.055556 SEND/s，2,500在线资格；终态累计76,002 SEND/ACK，839/839采样送达，loss/duplicate/corrupt/regression=0。原终态是operator_stop/operator_requested，local-step=clean，不是2h或72h通过。与上一代不同，本代qualification→final有1次workload retry及1次transport rejection，首次非零cut 19:15:08.124Z、预热结束19:14:12.994Z；数值如实保留，不与CI retry混淆，也不把周期cut时间当精确发生时间。

Session/队列/相关性最终清零、三产品queue/inflight归零、cleanup_error_count=0、drain_timed_out/unexpected_exit=false。该smoke没有独立post-shell OS PID库存，因此不声称独立枚举全部runner进程为零。六份完整选定service/app日志未匹配WARN/error/panic，不作全局无警告宣称。benchmark保留原Go输出截断标记（如有）。

三份小ZIP全量SHA256/size匹配GitHub API，全部成员CRC通过。229,605,238B正确性ZIP仅按边界range读取选定22成员；全部路径安全、所选成员CRC/SHA和对应checksum核验通过，整包SHA为API声明、未重算全包SHA或全包CRC。公开仅有限JSON/文本、脱敏job日志与聚合；原node/coordinator/driver日志、ZIP、读取stderr留独立0600 private目录。没有公私钥/私有profiles/临时signed URL进入allowlist。只证明当代CI性能/正确性，不替代candidate scripts失败、历史云故障、正式发布或公网stable验收。
