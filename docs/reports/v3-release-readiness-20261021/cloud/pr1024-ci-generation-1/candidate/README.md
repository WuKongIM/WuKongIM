# PR #1024 首次 candidate CI（失败，原件保留）

- Run [39654237841](https://github.com/WuKongIM/WuKongIM/actions/runs/39654237841)，attempt 1，最终 failure；只读取证，无 rerun/dispatch/cancel/源码修改。
- Head `c91e37b4e35e1009c9c5cb55ff351a38b870014f`；base `cee5d51f325fb84033060f546337d326110adae5`；实际 checkout `4f9e71ac3976fab087833bb230c391cd5ea5f46e`，Git tree 均为 `47eb4e4b7240441456d54c1935eb37372c80cddf`。三个实际 job 日志及 identity 均匹配。
- Protected policy SHA256 `2ee1b3162b431d77bec8cf68acc5b8aec0eede735f03c9f89d6c42e1d86cb0f9`，base/head 原字节相同。两个命名检查完整 argv/cwd/timeout/output limit 与该 policy 一致，无 filter/retry/放宽。

## 实际结果

1. **go-unit PASS**（job119410168261）：219 packages PASS，15 no-test packages。GOFLAGS=-p=2；1800s/8MiB，335.008s，exit0。
2. **scripts FAIL**（job119410167925）：原 `go test -tags=integration ./scripts/... -count=1 -timeout=9m -parallel=2`、GOFLAGS=-p=2 -json、600s/8MiB。600.108s 触发外层 timeout，exit -15、wrapper124，1,403,179B未截断。包 start 18:38:51.173Z 比命令 step 18:36:48Z 晚约123s；这一间隔不能全归因为编译，package 尚未到自身9m时限。已观察1195测试PASS/6SKIP，其中602顶级PASS/6SKIP，0FAIL事件；未完成、PAUSE/CONT及全部skip保留于 `scripts-first-failure-analysis.json`。最后RUN为预算finalizer的 `formal/previous_false/final_deployment`，不是已证明根因。
3. **E2E PASS**（job119413321477）：迁移1→1/1→3/3→1/3→3/3→5全通过，55次实际 history verified；4项跨节点测试PASS，显式负载比较项SKIP；单节点集群/三节点集群自聊、默认鉴权拒绝和全群重启通过。服务器仅构建一次，SHA256 `1d0475d1d298d6f00887ffa2bade75cfa20bf47b9338416c49cae500c9d22a40`，四次前/后校验OK。

新增 collector 缺失终态锚测试PASS(0.43s)；Retention于18:45:39.843Z CONT，但600s外层终止前未完成，没有内嵌20 Python/79进程用例完成收据，**不得计为全部通过**。六项 scripts skips 为Docker runtime/embedded Prometheus、SDK串行延迟、三项原生包相关显式可选门禁；完整名称/位置保留原事件。

六个 owned SID（default、scripts、build、三E2E命令）cleanup均 verified/complete、remaining=[]；scripts原失败没有被cleanup成功掩盖。只证明此workflow范围，不代替长期稳定性、发布/SDK/native或其他性能门禁。

## 有界归档

`candidate-summary.json` 是机器摘要；三个 job 的脱敏完整日志在 `job-logs-public/`；三个小 artifact 的选取文本/JSON原件、完整成员inventory、API身份与下载收据在 `artifacts-public/`。三ZIP均全量计算SHA256并与API digest/size相等，所有成员CRC通过；本次未用range-only证据。原ZIP和下载stderr/raw joblog在独立0600 private目录，不入public allowlist；未下载产品二进制。公开脱敏日志保留每条被移除行的SHA与位置，test事件在脱敏前提取。所有GET成功，无CI重试；自有exec均终态。
