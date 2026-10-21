# packages PR #68 首次 CI 审计

两条首次运行均成功，run_attempt 均为 1，无重试。PR head `09360d07d0bffa3258be252d7730902041026ba8`；两份实际 checkout 日志均为 PR synthetic merge `c69c3208a2b0f8f8a70ecadb400474a324cfe660`，GitHub commit API 证明与 head 的 tree 同为 `69dface82da5728544aeb217e8f48e7ea0057343`。最终 PR head 未漂移。

- [Pages validation 39554961478](https://github.com/WuKongIM/packages/actions/runs/39554961478)：合同成功，完整 Python suite 373 项/92.495 秒，OK。合同内另运行 2 项（与全量可能重合，不汇总成 375 唯一测试）。Configure Pages、Upload Pages artifact 和部署 job 都实际 skipped，不构成生产发布。
- [Native drill 39554961461](https://github.com/WuKongIM/packages/actions/runs/39554961461)：06:22:07–06:26:43 UTC；控制合同、固定工具源码 hash 校验、隔离 source-metadata、unsigned TEST ONLY 构建、TEST ONLY APT/RPM 签名/closure/客户端步骤、tracked-tree mutation 检查和 post steps 全部成功。日志确认 APT 签名/索引/包下载和 Rocky Linux RPM 仓库验证/安装；不扩大解释为生产多发行版验收。该 drill 的 product builder 固定为独立可信工具源码 `ed7e67d8f781b4f92872e42116c11b304fe03b5c`，并非本次产品 ed705 源码。

source-metadata artifact 的 7 用例均符合预期：stable、preview 为 exit 0；wrong-channel、wrong-deb-version、wrong-rpm-version、wrong-rpm-epoch、signed-rpm 为预期 exit 1。`test_only=true`、`payloads_executed=false`。完整 artifact ZIP 仅 1,268 字节，全下载 SHA-256 与 API digest 一致，所选 JSON CRC/sha 另记录；不保留 ZIP 或包产物。两份日志均在 2 MiB 界限内完整获取，未截断。

产品 main `ed705c0fbffdea485bbb65608d6e340bb049d97e` 的一次性查询只返回 [Docs 39551678483](https://github.com/WuKongIM/WuKongIM/actions/runs/39551678483)，已成功；build、Pages deploy、direct-origin verification 和 bounded CDN refresh 均实际成功。未看到该 exact SHA 的失败运行；这是文档发布证据，不替代产品候选原有验收。

监控没有调用 rerun、cancel、dispatch、评论、合并或任何生产修改。新 planner 的 CI 成功仍不等于 stable 签名频道和正式发布完成。
