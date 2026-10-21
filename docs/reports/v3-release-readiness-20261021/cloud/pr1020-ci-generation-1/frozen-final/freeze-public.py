from pathlib import Path
import datetime,gzip,hashlib,json,re,shutil
p=Path(__file__).resolve().parent
out=p/'frozen-final';out.mkdir()
head='0cdfaa37b00c58819895dee54029c27c83c9caa9';base='ce3c4d9110f17bb71dc99a89dc3c09758fabf980';merge='922bc66a9cebcbe09bef5fc43afc0593c4f07fb8'
pr=json.loads((p/'final-pr.json').read_text());assert pr['head']['sha']==head and pr['base']['sha']==base
checks=json.loads((p/'final-head-checks.json').read_text());assert len(checks['check_runs'])==checks['total_count']==21
assert all(c['head_sha']==head and c['conclusion'] in ['success','skipped'] for c in checks['check_runs'])
runs=json.loads((p/'poll-08/summary.json').read_text());assert len(runs['runs'])==5 and all(r['status']=='completed' and r['conclusion']=='success' for r in runs['runs'])
identity=json.loads((p/'source-binding.json').read_text())
localpath=p.parent/'released-continuation-implementation/validation-final-v1/summary.json'
local=json.loads(localpath.read_text())
summary={'schema':'wukongim.pr-first-ci-evidence/v1','observed_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'pull_request':1020,'head_sha':head,'base_sha':base,'actual_checkout_sha':merge,'source_tree':'36f96357c967418517a00444e52ae03f77c3ff90','attempt':1,'all_five_runs_completed_success':True,'check_counts':{'success':sum(c['conclusion']=='success' for c in checks['check_runs']),'skipped':sum(c['conclusion']=='skipped' for c in checks['check_runs']),'failure':0},'runs':runs['runs'],'linux_named_checks':{'go-unit':{'passed':True,'tested_packages':219,'no_test_packages':15,'cleanup_complete':True},'scripts-integration':{'passed':True,'root_pass':633,'pass_events':1162,'skip_events':6,'fail_events':0,'unfinished':0,'released_preclock_pass_events':85,'three_summary_ssh_tests':'all passed, none skipped','wrapper_seconds':595.434,'package_seconds':470.805,'cleanup_complete':True}},'e2e':{'ordinary_history':'5 topology cases + root passed','cross_node_delivery':'4 cases passed; existing opt-in 2000 QPS comparison skipped','self_chat':'1/3-node-cluster cases + root passed','cleanup_complete_all_commands':True},'local_named_history':[{'check':c['check'],'status':c['status'],'elapsed_seconds':c['elapsed_seconds']} for c in local['named_checks']],'local_named_source_limitation':'Full local go-unit binds freeze-v2, v3 changes Python helper + integration tests only; v3 delta focused/quick contracts passed. Original local scripts 9m timeout preserved. Final exact-tree Linux go-unit + scripts now pass.','no_workflow_rerun':True,'no_cloud_operations':True,'not_proven':['live released-continuation authentication of the real old cloud request','actual old cloud invoice','2h rehearsal, 72h formal soak, capacity and recovery acceptance','production package stable publication'],'read_api_errors_preserved':'read-observation-errors.json'}
(out/'summary.json').write_text(json.dumps(summary,indent=2)+'\n')
files=[]
for name in ['initial-pr.json','initial-runs.json','initial-summary.json','synthetic-merge.json','source-binding.json','final-pr.json','final-main-ref.json','final-head-checks.json','scripts-first-linux-summary.json','default-unit-summary.json','candidate-e2e-summary.json','chat-performance-summary.json','chat-correctness-summary.json','consumer-linux-confirmation.json','read-observation-errors.json','poll-07-error.log','39598983278-artifacts-01.json','poll_once.py','watch.py','download-candidate-artifacts.py','download-chat-artifacts.py','download_job_logs.py','summarize-final-artifacts.py','freeze-public.py']:
 f=p/name
 if f.exists():files.append(f)
files+=list((p/'poll-08').glob('*.json'))
files+=list((p/'candidate-artifacts').glob('artifact-*.json'))
for d in ['artifact-12345996635','artifact-12346062287','artifact-12344894821']:
 files += [f for f in (p/'candidate-artifacts'/d).rglob('*') if f.is_file()]
files+=list((p/'chat-artifacts').glob('*.json'))
files+=[f for f in (p/'chat-selected').rglob('*') if f.is_file()]
files+=[f for f in (p/'job-logs-public').glob('*') if f.is_file()]
transforms=[]
for f in sorted(set(files)):
 rel=f.relative_to(p);b=f.read_bytes();original=b
 # Job log files were already redacted; this second pass covers test stdout and rare auth-related printouts.
 if f.suffix in ['.log','.jsonl']:
  text=b.decode('utf-8');red=[];lines=[]
  for i,line in enumerate(text.splitlines(keepends=True),1):
   if re.search(r'token|password|authorization|private.?key|secret',line,re.I):
    red.append({'line':i,'bytes':len(line.encode()),'sha256':hashlib.sha256(line.encode()).hexdigest()});lines.append('[redacted authentication-related line; original line hash retained]\n')
   else:lines.append(line)
  b=''.join(lines).encode()
  transforms.append({'source':str(rel),'raw_sha256':hashlib.sha256(original).hexdigest(),'raw_bytes':len(original),'redacted_lines':red,'public_uncompressed_sha256':hashlib.sha256(b).hexdigest()})
 if f.suffix in ['.log','.jsonl'] or (f.name.startswith('arrival') and f.suffix=='.json'):
  target=out/str(rel+'.gz') if False else out/(str(rel)+'.gz');b=gzip.compress(b,mtime=0)
 else:target=out/rel
 target.parent.mkdir(parents=True,exist_ok=True);target.write_bytes(b)
(out/'archive-transforms.json').write_text(json.dumps(transforms,indent=2)+'\n')
readme='''# PR #1020 首轮 CI 公开证据

固定 head `0cdfaa37b00c58819895dee54029c27c83c9caa9`，base/远端 main `ce3c4d9110f17bb71dc99a89dc3c09758fabf980`。实际 checkout 为 PR merge `922bc66a9cebcbe09bef5fc43afc0593c4f07fb8`，tree `36f96357c967418517a00444e52ae03f77c3ff90` 与提交完全一致。

5 个自动工作流首次 attempt 1 全部 completed success；21 checks 中 19 success、2 非 PR 诊断/夜间任务按条件 skipped。没有 rerun、手动 dispatch 或云操作。

- Linux `go-unit`：219 有测试包通过、15 无测试包；`scripts-integration`：633 顶层通过、1162 pass 事件、6 既有可选 skip、0 fail/unfinished。外层 595.434 秒、包 470.805 秒；所有 85 新续跑测试事件通过。三个远端摘要 SSH 子例实际执行并通过。执行器均 complete cleanup、零遗留、输出未截断。
- 迁移五拓扑全部通过；跨节点 4 例通过（既有 opt-in 2000 QPS 比较未启用）；自聊 1/3 节点集群跨重启两例通过。构建和三组测试的独立 cleanup 均完成；binary VCS=实际 merge、modified=false。
- 三个 500 QPS seam 各 3×60 秒窗口共 9 窗均通过，错误/压测请求丢弃为 0、P99 <400ms。chat correctness 的90秒测量 clean，final 为协调 operator_stop，不冒充正式时间窗完成。
- iOS/Android/Flutter 发布包互通验收、DEB/RPM metadata/四发行版安装及生命周期均 success。原生包仅 PR preview 演练，不是稳定渠道生产发布。

本地历史仍保留：go-format、flow-doc-contracts、workflow-contracts、go-unit、go-vet、go-mod-tidy 通过；Mac scripts-integration 首轮9分钟超时，owned session 已清零。完整本地 go-unit 属于 freeze-v2；v3 窄 helper/集成回归另有证明，不能把它们合称本地同源全部首绿。本次最终 tree 的完整 Linux go-unit/scripts 独立首次通过。

尚未证明真实旧请求 continuation authority/live cloud、旧实际账单、2h→72h/容量/恢复，生产 stable 发布也不在本证据内。此次 CI 不能授权或替代云启动决策。

原 ZIP 已下载的 7 个产物均核 API SHA256/字节数/ZIP CRC；大 ZIP 和二进制不在此包。Native preview ZIP 仅保留 API 元数据（未下载），四发行版的公开 job logs/步骤记录证明实际执行。日志以 `.gz` 保留，经行级认证关键词脱敏；原日志 SHA/字节数与变换在 `archive-transforms.json` 及 job receipts，完整原文仅在本机私有原始目录。JSON/业务报告使用精确白名单，不含配置、私钥、解密凭证、缓存和 payload 二进制。`SHA256SUMS` 校验本目录；`ARCHIVE_ALLOWLIST.json` 是完整文件列表。

一次只读 jobs API 获取、一次只读 log 下载曾失败，后续有界 GET 成功；不是 CI retry，原观察错误已保留。初始离线 E2E 分析器对所有 skip 作拒绝，发现既有 opt-in 比较后以精确名单记录此限制；没有修改测试。
'''
(out/'README.md').write_text(readme)
entries=[]
for f in sorted(out.rglob('*')):
 if not f.is_file():continue
 b=f.read_bytes();plain=gzip.decompress(b) if f.suffix=='.gz' else b
 assert not re.search(rb'-----BEGIN (?:OPENSSH|RSA|EC|DSA)? ?PRIVATE KEY',plain),str(f)
 assert not re.search(rb'(?:github_pat_[A-Za-z0-9_]{30,}|gh[pousr]_[A-Za-z0-9]{30,})',plain),str(f)
 entries.append({'path':str(f.relative_to(out)),'bytes':len(b),'sha256':hashlib.sha256(b).hexdigest()})
(out/'ARCHIVE_ALLOWLIST.json').write_text(json.dumps({'schema':'wukongim.public_evidence_allowlist/v1','entries':entries,'excluded':['raw logs','artifact ZIPs','product binaries','private credentials/configurations','shared caches'],'scans':{'private_key_marker_found':False,'github_token_shape_found':False,'limitation':'bounded shape/line filters, not a general secret audit'}},indent=2)+'\n')
rows=[]
for f in sorted(out.rglob('*')):
 if f.is_file():rows.append(hashlib.sha256(f.read_bytes()).hexdigest()+'  '+str(f.relative_to(out)))
(out/'SHA256SUMS').write_text('\n'.join(rows)+'\n')
print(json.dumps({'files':len(rows)+1,'bytes':sum(f.stat().st_size for f in out.rglob('*') if f.is_file()),'manifest_sha256':hashlib.sha256((out/'SHA256SUMS').read_bytes()).hexdigest(),'directory':str(out)}))
