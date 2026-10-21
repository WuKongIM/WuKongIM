# Packages #74 首轮 CI

截至 2026-10-21T12:10:38.545777+00:00，approved head `6760cbedb0d87ad51be958f0f78c91db49f27328`，实际 PR checkout `492ff426eab5345d738cb6679d12dcf0a320660d`；两者完整 tree `ca524ac75520f8614f7d06e0e46e44902203145e` 相同。API/tree证明与三份实际job日志一致，均attempt1。

| 工作流/job | 结果 | 实际证据 |
| --- | --- | --- |
| [Pages39596459745](https://github.com/WuKongIM/packages/actions/runs/39596459745) | success | 489 Python tests，无skip；4合同tests+shell合同；deploy job skipped |
| [旧native job119209867267](https://github.com/WuKongIM/packages/actions/runs/39596459800/job/119209867267) | success，12:08:58Z结束 | source7、local signer10、bootstrap6、candidate31+transport8真实通过；完整legacy签名/安装drill通过 |
| [新增stable49 job119209866922](https://github.com/WuKongIM/packages/actions/runs/39596459800/job/119209866922) | failure，12:03:11Z结束 | Rocky固定digest拉取manifest unknown；driver skipped，0/49执行，无该driver cleanup收据 |

首失败精确在镜像准备，不能归为产品/签验/安装错误；该job上传step成功但没有匹配文件，不能充作验收通过。旧drill31+8不能代替新的四发行版A/B和完整installed CLI门禁。Pages未部署，监控未取消、重跑、dispatch、评论、修改源码或启用stable。

四份实际公开收据在 artifacts/，完整API及三个完整job日志在本目录。下载的小ZIP均独立重算完整SHA256匹配GitHubAPI digest，并核全部成员CRC；只保留公开JSON成员和ZIP校验inventory，不保留ZIP/产品二进制。candidate/sourceSha为实际synthetic checkout，private test树和scratch删除已在相关收据中明确。legacy完整drill日志含Good signature、实际安装Complete及 `native-package signed repository container validation passed`。

mirror-diagnosis/单独保存只读官方manifest调查：新的Rocky9 init/amd64固定摘要候选已按rawHTTP验证，Alma原固定摘要仍存在；旧Rocky为何历史失效未知。本轮CI原失败不受随后pin修复影响，后续必须用新head首次完整验收。

归档以 ARCHIVE_ALLOWLIST.txt / archive-sha256.json 为准。
