#!/usr/bin/env python3
"""Derive bounded comparisons from analyze.py output; no workloads or repository writes."""
import argparse,datetime,json,pathlib,statistics
parser=argparse.ArgumentParser(description=__doc__)
parser.add_argument('--analysis-dir',type=pathlib.Path,required=True,help='The fresh output directory from analyze.py')
args=parser.parse_args()
out=args.analysis_dir.resolve()
s=json.loads((out/'profile-summary.json').read_text())
def percent(a,b):return (a/b-1)*100
fields=['server_sampled_cpu_cores','window_cpu_ms_per_completed_request','window_alloc_bytes_per_completed_request','server_sampled_alloc_delta_bytes']
comparison={'B5_vs_B4_percent':{f:percent(s['05-B'][f],s['04-B'][f]) for f in fields},'C_vs_comparator_mean_percent':{}}
comparison['B5_vs_B4_percent']['driver_cpu_cores']=percent(s['05-B']['driver']['sampled_cpu_cores'],s['04-B']['driver']['sampled_cpu_cores'])
for j,endpoint in enumerate(['list','sync']):
 comparison['B5_vs_B4_percent'][endpoint+'_profile_p99']=percent(s['05-B']['window_phase_results'][j]['p99_ms'],s['04-B']['window_phase_results'][j]['p99_ms'])
for label,blocks in [('A',['03-A','06-A']),('B',['04-B','05-B'])]:
 comparison['C_vs_comparator_mean_percent'][label]={f:percent(s['07-C'][f],statistics.mean(s[b][f] for b in blocks)) for f in fields}
 comparison['C_vs_comparator_mean_percent'][label]['driver_cpu_cores']=percent(s['07-C']['driver']['sampled_cpu_cores'],statistics.mean(s[b]['driver']['sampled_cpu_cores'] for b in blocks))

selected=['runtime.mallocgc','runtime.gcBgMarkWorker','encoding/json.Marshal',
'github.com/WuKongIM/WuKongIM/pkg/transport/internal/rpc.(*Service).handle',
'github.com/WuKongIM/WuKongIM/pkg/cluster/channels.(*Service).readSelectedConversationHeads',
'github.com/WuKongIM/WuKongIM/pkg/cluster/channels.(*Service).readStoredConversationHead',
'github.com/WuKongIM/WuKongIM/pkg/db/message.(*channelRegistry).acquire',
'github.com/WuKongIM/WuKongIM/pkg/db/message.(*channelRegistry).retainWarmLocked']
comparison['server_selected_cumulative_core_rates']={f:{b:s[b]['server_cpu_core_rates']['cumulative'].get(f,0) for b in s} for f in selected}
comparison['candidate_only_functions_at_least_0_01_sampled_cpu_cores']=[{'function':f,'cores':value} for f,value in s['07-C']['server_cpu_core_rates']['cumulative'].items() if value>=0.01 and all(s[b]['server_cpu_core_rates']['cumulative'].get(f,0)==0 for b in ['03-A','04-B','05-B','06-A'])]
coverage={}
for b,v in s.items():
 ws=datetime.datetime.fromisoformat(v['profile_window']['start'].replace('Z','+00:00'));we=datetime.datetime.fromisoformat(v['profile_window']['end'].replace('Z','+00:00'))
 coverage[b]=[]
 for n,node in enumerate(v['nodes'],1):
  start=datetime.datetime.fromisoformat(node['start_utc']);end=datetime.datetime.fromisoformat(node['end_utc'])
  coverage[b].append({'node':n,'capture_seconds':node['duration_seconds'],'warmup_lead_seconds':(ws-start).total_seconds(),'overlap_profile_window_seconds':max(0,(min(end,we)-max(start,ws)).total_seconds()),'alloc_snapshot_seconds':node['alloc_snapshot_seconds']})
comparison['coverage']=coverage
comparison['original_window_median_p99_ms']={b:v['original_window_median_p99_ms'] for b,v in s.items()}
comparison['B5_vs_B4_original_window_median_p99_percent']=[percent(s['05-B']['original_window_median_p99_ms'][j],s['04-B']['original_window_median_p99_ms'][j]) for j in range(2)]
comparison['profile_start_after_original_window_seconds']={b:min((datetime.datetime.fromisoformat(n['start_utc'])-datetime.datetime.fromisoformat(v['original_window_end'].replace('Z','+00:00'))).total_seconds() for n in [v['driver'],*v['nodes']]) for b,v in s.items()}
assert all(gap>0 for gap in comparison['profile_start_after_original_window_seconds'].values())
comparison['driver_json_unmarshal']={b:{'sampled_cpu_cores':s[b]['driver']['cumulative_core_rates'].get('encoding/json.Unmarshal',0),'share_of_driver_cpu_percent':s[b]['driver']['cumulative_core_rates'].get('encoding/json.Unmarshal',0)/s[b]['driver']['sampled_cpu_cores']*100} for b in s}
(out/'profile-comparison.json').write_text(json.dumps(comparison,indent=2)+'\n')
md=['# 固定主机短 profile 离线分析（run 39453556254）','',
'结论：未找到足以归因到候选新增热点、并支持特定产品代码修复的证据。同一 B 二进制短采样的 CPU 与延迟已经有明显波动；这些结果不能证明或排除小幅回归，更不能确认历史 7763 环境的失败根因。C 完整门禁已通过属于另一个独立证据；本分析只检查已有短 profile。','',
'本次离线分析检查三项假设：① 候选新增热点应在归一化函数成本上持续超过 A/B；② 若驱动争用相关，驱动 JSON/响应处理与等待信号应随变化；③ 若短采样自身不稳定，同一二进制 B4/B5 应出现相近波动。这里只检验已有证据，不新运行负载。','',
'## 身份与归一化','',
'- A：`'+s['03-A']['product_sha']+'`；B：`'+s['04-B']['product_sha']+'`；C/H：`'+s['07-C']['product_sha']+'`。脚本逐一核验实际产品和驱动 binary SHA256。B4/B5 使用同一字节摘要 `'+s['04-B']['binary_sha256']+'`。',
'- CPU“核数”=采样 CPU 纳秒 / 各自真实 capture 时长；server 核数是三节点各自归一化值之和，不把三个同时运行的 duration 相加后当单一墙钟时长。累计函数值可重叠，不可相加。',
'- “CPU ms/请求”和“分配字节/请求”取 report.profile_window 的共享 server metrics / 2600 个完成请求。该 10 秒窗 list=2000、sync=600，五组均无 errors/drops；它不包含 driver CPU。',
'- alloc pprof 使用每节点 after − before 的 alloc_space；这是统计采样估计，快照跨度还包括 warmup/抓取开销，因此不冒充精确 10 秒窗的每请求分配。','',
'| 块 | driver CPU核 | server CPU核 | 10s server CPU ms/请求 | 10s 分配 MB/请求 | 短窗 list/sync P99 ms |',
'|---|---:|---:|---:|---:|---:|']
for b,v in s.items():
 md.append(f"| {b} | {v['driver']['sampled_cpu_cores']:.4f} | {v['server_sampled_cpu_cores']:.4f} | {v['window_cpu_ms_per_completed_request']:.4f} | {v['window_alloc_bytes_per_completed_request']/1e6:.6f} | {v['window_phase_results'][0]['p99_ms']:.3f} / {v['window_phase_results'][1]['p99_ms']:.3f} |")
b= comparison['B5_vs_B4_percent']
md+=['','## 可支持与不可支持的归因','',
f"1. B4→B5（完全相同二进制）：server 归一化采样 CPU +{b['server_sampled_cpu_cores']:.2f}%，driver +{b['driver_cpu_cores']:.2f}%；10 秒窗 CPU/请求 +{b['window_cpu_ms_per_completed_request']:.2f}%，分配/请求仅 +{b['window_alloc_bytes_per_completed_request']:.4f}%；list/sync P99 +{b['list_profile_p99']:.2f}% / +{b['sync_profile_p99']:.2f}%。CPU 与延迟变化不能都解释成版本变化，且原始三窗 A/A 重复性不满足预声明容差。",
'2. 五组主要 server 热点相同：RPC 服务、readSelectedConversationHeads/readStoredConversationHead、mallocgc；分配平坦热点仍为 JSON Marshal、channelRegistry.acquire/retainWarmLocked、会话头组装和响应缓冲。未见只在 C 出现且达到 0.01 采样 CPU 核的累计函数。这个 0.01 仅用于列举新热点，非验收阈值；低频新函数不能据此断言为零。',
'3. C 的 server 归一化采样 CPU 位于 A/B 范围内，10 秒每请求 CPU 也在其范围内，分配约 2.36 MB/请求几乎不变。C 的 HTTP 累计栈略高，而 RPC/存储/运行时在其他块也波动，未形成稳定、独有的新增热点。这里没有足以选择某段代码做修复的相对回退证据。',
'4. driver 的 JSON Unmarshal 占自身 CPU 约 58%–66%，五组一直存在。C 短 profile 窗 list P99=243.14 ms，其中 driver_wait P99=202.05 ms、request P99=67.38 ms；这与短时调度/排队变化相容，但 CPU profile 只记录运行时间，无法证明 off-CPU 等待原因。request 还包含响应读取、JSON 反序列化和身份校验，不是纯服务器耗时；两个 P99 不可相加。','',
'## 捕获范围与限制','',
'所有 CPU/alloc 抓取发生在三次原始 60 秒窗口之后。server CPU 请求 8 秒、driver capture 约 11 秒；节点 capture 覆盖 warmup 和约前 7.x 秒的额外 10 秒窗，不覆盖全部额外窗，更不覆盖原先 180 秒正式混合读窗口。详见 profile-comparison.json.coverage。不能把某个短 profile 的函数占比用于解释原始三窗的尾延迟或历史失败。',
'样本较短且没有精确逐请求 CPU attribution。alloc_space 有采样误差与 GC 更新时点影响；前后差值不能冒充全部真实分配。一次 C 短采样也不足以证明稳定提升或回退；现有证据支持继续保留“根因未定”，不支持仅依据这些 profile 修改产品实现。','',
'## 重现与原始来源','',
'输入：`download/conversation-fixed-host/blocks/{03-A,04-B,05-B,06-A,07-C}/report.json`、`driver-cpu.pprof`、`node{1,2,3}-{cpu,alloc-before,alloc-after}.pprof`，以及对应 `binaries/{A,B,C}/wukongim-e2e` 和 `harness/conversation_qps.test`。',
'运行 `python3 profile-analysis/analyze.py --artifact-root <解压根> --repo <冻结 H 仓库> --output-dir /tmp/new-profile-analysis`，然后 `python3 profile-analysis/summarize.py --analysis-dir /tmp/new-profile-analysis`。output-dir 必须是新目录，拒绝覆盖已有证据；repo 缺省为当前目录；需要 Go >=1.25，若 PATH 的 go 较旧可传 `--go /path/to/installed/go`。extract.go 使用仓库已锁定的 google/pprof parser；工具链必须已在本机安装，重现显式禁用模块/校验和网络和隐式工具链下载；同时保留 go tool pprof 对真实 Linux binary 的文本输出、完整命令、输入 hash 与解码 JSON。未执行 artifact 中的任何产品或 driver binary。','']
(out/'profile-findings.md').write_text('\n'.join(md))
print(json.dumps(comparison['B5_vs_B4_percent'],indent=2))
print('candidate-only:',comparison['candidate_only_functions_at_least_0_01_sampled_cpu_cores'])
print('coverage',json.dumps(coverage['07-C']))
