from pathlib import Path
import subprocess,json,hashlib
P=Path('/tmp/wukongim-v3-readiness-20261023/cloud/full-original-distribution-profile-node2-v1')
O=P.parent/'profile-node2-signals-v1/provenance-addendum'
S=Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/v3-rc-loss-diagnosis')
BASE='a804f4a57c2b9f1bf6dcfa74382d9cfdc9e34f83';DECLARED='79a7177032a6ae1ec7015cefe0e6f94eb0f19f06'
def sha(b):return hashlib.sha256(b).hexdigest()
def git(*args):return subprocess.check_output(['git',*args],cwd=S)
def blob(rev,path):return git('show',rev+':'+path)
pre=json.loads((P/'preflight.json').read_text());binary=[]
for name in ['wukongim','wkcli']:
 q=P/'bin'/name;raw=q.read_bytes();actual=sha(raw);assert actual==pre['binary_sha256'][name]
 text=subprocess.check_output(['go','version','-m',str(q)],text=True);lines=text.splitlines()
 build={};package=None;module=None
 for line in lines[1:]:
  parts=line.strip().split('\t')
  if parts[0]=='build':
   key,value=parts[1].split('=',1);build[key]=value
  elif parts[0]=='path':package=parts[1]
  elif parts[0]=='mod':module=parts[1:]
 assert build['vcs.revision']==BASE and build['vcs.modified']=='false'
 binary.append({'binary':name,'bytes':len(raw),'sha256':actual,'embedded_go':lines[0].rsplit(': ',1)[-1],'main_package':package,'main_module':module,'embedded_build_settings':build,'method':'go version -m reads binary buildinfo; product not executed','raw_version_output_sha256':sha(text.encode())})
rows=[]
for line in git('diff','--name-status',BASE,DECLARED).decode().splitlines():
 status,path=line.split('\t',1)
 entry={'status':status,'path':path}
 if status!='A':entry['base_sha256']=sha(blob(BASE,path))
 if status!='D':entry['declared_sha256']=sha(blob(DECLARED,path))
 rows.append(entry)
production=[x for x in rows if x['path'].endswith('.go') and not x['path'].endswith('_test.go') and x['path'].split('/')[0] in ['cmd','internal','pkg']]
paths=['AGENTS.md','go.mod','go.sum','internal/bench/FLOW.md','internal/bench/chatlifecycle/FLOW.md','internal/app/FLOW.md','internal/infra/cluster/FLOW.md','internal/usecase/presence/FLOW.md','internal/runtime/presence/FLOW.md','internal/runtime/online/FLOW.md','internal/runtime/delivery/FLOW.md','internal/access/node/FLOW.md','internal/access/gateway/FLOW.md','pkg/slot/FLOW.md','pkg/cluster/FLOW.md','internal/bench/chatlifecycle/traffic.go','internal/bench/chatlifecycle/production_controller.go','internal/bench/target/client.go','internal/app/observability.go','pkg/metrics/registry.go','internal/access/gateway/error_map.go','internal/usecase/presence/recovery.go','internal/runtime/presence/recovery.go','internal/infra/cluster/presence_recovery.go','internal/infra/cluster/channel_metadata.go','internal/app/online_delivery.go','internal/runtime/online/registry.go','pkg/slot/proxy/send_permission_cohort.go','pkg/slot/proxy/send_permission_rpc.go','pkg/slot/proxy/send_permission_observer.go','pkg/slot/multiraft/read_barrier.go','pkg/cluster/node_message_update.go','pkg/transport/internal/conn/conn.go']
same=[]
for path in paths:
 a=blob(BASE,path);b=blob(DECLARED,path)
 assert a==b,(path,'binary source is not byte-equivalent')
 assert b==(S/path).read_bytes(),(path,'worktree differs from frozen declared source')
 same.append({'path':path,'base_sha256':sha(a),'declared_sha256':sha(b),'worktree_sha256':sha((S/path).read_bytes())})
# The two normal message-product implementation differences are deliberately
# listed, not interpreted through newer code as if it were in the older binary.
inputs=[]
for rel in ['preflight.json','profile-driver-status.json','profiles-node2/metadata.json','profiles-node2/profiles/node-2-cpu.pb.gz','pprof-offline-receipt.json','safe-cpu-top.txt','safe-cpu-cum.txt','report/run-start.json','report/final.json','cleanup-receipt.json']:
 q=P/rel;inputs.append({'path':str(q),'bytes':q.stat().st_size,'sha256':sha(q.read_bytes())})
assert inputs[3]['bytes']==40474 and inputs[3]['sha256']=='d3c5e46713de6add18641f2f4f4a0672cbac6db16322d555cdb0236cf32dcd23'
x={'schema':'wukongim/local_profile_provenance_correction/v1','declared_analysis_source_revision':DECLARED,'actual_embedded_binary_revision':BASE,'actual_binary_metadata':binary,'host_go_command':pre['go_version'],'host_go_is_not_embedded_go':True,'all_repository_differences':rows,'all_changed_non_test_go_implementation_files_under_cmd_internal_pkg':production,'normal_message_product_implementation_differences':['internal/usecase/conversation/legacy_sync.go','pkg/channel/replication/recovery_barrier.go'],'analysis_files_verified_byte_identical':same,'source_trees':{r:git('rev-parse',r+'^{tree}').decode().strip() for r in [BASE,DECLARED]},'worktree_status':git('status','--short').decode().splitlines(),'input_bindings':inputs,'limits':['Embedded metadata identifies the retained executable; no new reproducible build was performed.','No claim of whole-product equivalence between a804 and 79a.','This run is an a804/c253 executable with a 79a-derived launcher/config analysis context, not a validated 79a candidate reproduction.','CPU describes this retained node2 executable only; no cloud-performance or message-loss root cause is established.','No previous frozen proposal/artifact was rewritten; this is an independent correction addendum.'],'product_executed':False,'new_profile_or_load_or_cloud_operation':False}
(O/'binding.json').write_text(json.dumps(x,indent=2)+'\n')
print('binary metadata confirmed; repository changes',len(rows),'non-test implementation changes',len(production),'byte-identical analysis files',len(same))
