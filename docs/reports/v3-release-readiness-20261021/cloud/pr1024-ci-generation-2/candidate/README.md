# PR #1024 第二代首次 candidate CI

[Run39659391125](https://github.com/WuKongIM/WuKongIM/actions/runs/39659391125)，attempt1，整体failure；未重跑/取消/派发/修改源码。Head `2cc562fa39f7b430c61a4494636f6ee0ec0d631a`，base cee5，实际事件checkout `884f88e74b68ec2db26f37bc6659195d24c417a2`，tree `85f578ba19135892c3d4f5e18db37329cd515cf7`。查询时PR合并对象45c1f5与实际事件对象884不同，但parents/tree相同；完整对象及实际三个job日志/identity均保留，不用PR字段代替checkout。产品cmd/internal/pkg/依赖及本workflow/protected policy与cee5 Git对象相同。

## 结果

- **Default PASS**：219 packages通过、15无测试包；GOFLAGS=-p=2，原go-unit argv/cwd、1800s/8MiB边界不变，423.721s、exit0。
- **Scripts FAIL**：原命名检查 `go test -tags=integration ./scripts/... -count=1 -timeout=9m -parallel=2`、GOFLAGS=-p=2 -json，原600s/8MiB。575.954s、exit1/wrapper1，包463.478s；这次不是timeout。641根PASS/6SKIP/1FAIL，共1290测试PASS/6SKIP/1FAIL，无未结束测试事件，输出未截断。
- 唯一失败 `TestLocalThresholdPprofInterruptJoinsRequestsAndWritesPartialMetadata`（6.03s）：`wukongim_local_threshold_pprof_script_integration_test.go:438: server 2 still has 1 active request(s) after helper exit`。完整原事件见 `scripts-first-failure-excerpt.json`；尚不推断根因或把失败称基础设施噪声。上一代600.108s首次超时保持独立原件。
- **Collector实际完成**：Retention在19:17:41.991Z PASS（14.79s），缺失终态锚用例PASS（0.37s）。固定源码20 Python方法/79进程场景由完整module执行，父测试检查进程success和fixture JSON零failure/error；因此这是该固定命名测试完成证据。内部79逐条JSON在Go TempDir，未直接上传留存，不冒填独立逐case Linux收据；这不是额外门禁。
- **E2E PASS**：迁移1→1/1→3/3→1/3→3/3→5和55条history verified；跨节点4项PASS，原可选2kQPS比较项SKIP；单节点集群/三节点集群自聊默认鉴权及整群重启通过。唯一服务器二进制SHA `83c3b3ff70fa86228f7844ecc4ae1ac7c77b7d52c38e38e26ee6b18b43f7bca9`，构建一次、四次hash检查一致。

default、scripts、build和三个E2E命令共6个owned SID cleanup均verified/complete/remaining=[]；scripts原断言失败不被清理成功抵消。6个scripts skip为显式环境/opt-in门禁，全部名称和原根事件保留。该CI不证明长期稳定性、发布或公网验收；三节点性能另行封存，不能抵消scripts红灯。

## 取证界限

三个小ZIP完整SHA256/size匹配API、所有成员CRC通过。公开保留完整脱敏job日志、有限文本/JSON成员与各成员inventory/receipt/test-events；原ZIP、下载stderr及原始job日志在独立0600 private目录，不入allowlist。无产品binary/私钥/原profile/临时signed URL。归档源码规则名使用`.source`，没有实际AGENTS.md或FLOW.md。

所有GET成功，无CI重试。有限allowlist、SHA256SUMS与archive-sha256.json绑定本代；所有自有exec已终态。不写PR、不合并、不提交证据分支。
