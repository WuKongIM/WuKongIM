# 独立审查结论

在本次获准的 **preview add_release 离线运输计划** 范围内，没有未解决的阻塞项。审查基于 packages `782c14602a6b243d893e6abf4d1682b7370736c4` 上的五文件未提交变更，最终源码和文档 SHA-256 见 `final-freeze.json`。

发现并关闭了一个 P2：初版在检查独立声明大小前先无上限哈希 archive，receipt/control 同样先读后限额。独立红灯显示声明 1 字节的 archive 先读取了 66,048 字节。实现方先增加 3 项、9 个子例失败测试，再在新 planner 内加入打开前的实际大小比较和已有 hash helper 的显式上限。独立复验中同一输入不再读取 archive；3 项定向测试通过，未修改共用验证器。

独立执行的五项聚焦探针全部符合预期：真实 CLI 正常输入；完整重新生成 receipt 的旧快照缺失 by-hash；同步改写 archive/receipt 后仍保留旧独立锚点；伪报 archive 大小；重新 receipted 的 gzip 解压到 64 MiB+1。四次真实 CLI 中，正常输入成功，三类恶意输入均失败且没有输出文件。合法输入的计划 SHA-256 在修复前后均为 `02b4d19fd378958b0a756c26b0edcebf64f6e3542912b2d8ee03fc31ae33aa51`。

已读新增实现、测试和文档，并对照既有 canonical archive、audit receipt、APT/RPM/composer 闭包。保留各代分别验证，冲突不可变 URI 拒绝，当前可变入口使用 no-store，删除集合恒空，稳定频道和非 add_release 操作拒绝。五文件变更不触及生产工作流或控制清单。`git diff --check` 通过。实现方的 83 项相关测试和 contract.sh 记录另存于 `../object-storage-plan/`，本审查没有重复全套执行。

本结论只证明离线结构和字节一致性：外部 descriptor 仍是调用者审核的声明；所有 fixture 的证书/签名都是 TEST ONLY 合成字节。签名有效性、GitHub 来源、control commit、云上路由/缓存和正式 stable 发布均未验证，输出也明确保持这些状态为 false。未访问云、密钥或任何发布接口。
