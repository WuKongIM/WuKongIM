# PR #1017 的 0672 候选 CI

精确 head `0672c115950f65e6d92381aa5b3ea9f0a20fc790`；实际 PR checkout `753005853ea3253d9d1ce6c525a3c3ba8c2f5c09`。GitHub Git API 验证两者完整 tree 均为 `ad05497c99502a4d076bacb7dee150d10e939d28`。四个工作流均为首次 attempt 1；源状态和所有 job 结论在原始 API 收据中。

默认层、迁移/消息 E2E、三节点正确性与 race、三个 500-QPS seam、Android/iOS/Flutter 和四发行版 systemd 包生命周期检查通过。候选工作流整体仍失败：独立 scripts 门禁在原 600 秒外层时限终止，cleanup verified/complete、remaining=[]，不能标记整轮合格。随后只增加 Go JSON 观测的 revision `eda77697db42ba14f14bfad58cdd3557342b9989` 是新的检查，原失败保留。

这里完整下载了候选 default/E2E/scripts 的三个小型 artifact 和三个 500-QPS artifact；大体积三节点/原生包二进制 artifact 未下载，相关完成状态来自 job/step API。SDK 没有 artifact。不得把未下载文件声称为已经逐字节核验。

E2E 的构建、五拓扑迁移、跨节点和 self-chat 四段 result 均通过且清理完成。self-chat 覆盖单节点集群/三节点集群，每个拓扑六条 ACK、一次全群重启；共十二次历史观察和八个发送会话抑制窗口。每个拓扑存在八个预期实时重复帧，不能声称 wire exactly-once。完整数字见 `summary.json` 与原始报告。

本轮只认证相应回归范围，不替代完整 Linux 性能矩阵、72 小时长稳、正式版本 tag 或 stable 公网 APT/RPM 安装验收。
