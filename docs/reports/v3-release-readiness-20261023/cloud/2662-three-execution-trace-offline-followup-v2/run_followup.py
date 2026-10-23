"""One separately approved nine-command followup; preserve all v1 partial facts."""
import argparse,datetime,json,os,pathlib,signal,time
import trace_offline as A
B=pathlib.Path('/tmp/wukongim-v3-readiness-20261023/cloud');V=B/'2662-three-execution-trace-offline-followup-v2'
P=pathlib.Path('/tmp/wukongim-v3-readiness-20261023/private/2662-three-execution-trace-offline-followup-v2')
Q=B/'2662-three-execution-trace-offline-proposal-v1';OLD=B/'2662-three-execution-trace-offline-v1'
STOP=False

def signal_stop(sig,frame):
    global STOP
    STOP=True

def save(path,value,cap=1048576):
    raw=(json.dumps(value,indent=2)+'\n').encode()
    if len(raw)>cap:raise A.OfflineError('projection_invalid')
    fd=os.open(path,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o600)
    with os.fdopen(fd,'wb') as f:
        if f.write(raw)!=len(raw):raise A.OfflineError('io_failure')

def verify_manifest(path,expected):
    A.verify_tool(path,expected)
    for line in path.read_text().splitlines():
        h,name=line.split(None,1)
        if pathlib.Path(name).name!=name:raise A.OfflineError('unsafe_path')
        A.verify_tool(path.parent/name,h)

def main():
    p=argparse.ArgumentParser();p.add_argument('--execute',action='store_true');p.add_argument('--ready-sha',required=True);a=p.parse_args()
    if not a.execute:return
    os.umask(0o077)
    for s in [signal.SIGINT,signal.SIGTERM]:signal.signal(s,signal_stop)
    start=time.monotonic();deadline=start+300;began=datetime.datetime.now(datetime.timezone.utc).isoformat()
    verify_manifest(V/'READY_SHA256SUMS',a.ready_sha)
    plan=json.loads((V/'followup-plan.json').read_text())
    A.verify_tool(pathlib.Path(plan['original_approval_path']),plan['original_approval_sha256'])
    verify_manifest(OLD/'PUBLIC_SHA256SUMS',plan['original_public_manifest_sha256'])
    verify_manifest(Q/'PUBLIC_SHA256SUMS','84d9c0b9aa1cb465376893512de8fc0b55d0ab4b385174cdd6c88b111c479330')
    original=json.loads(pathlib.Path(plan['original_approval_path']).read_text());pins=json.loads((Q/'tool-source-pins.json').read_text())
    if plan['original_inputs']!=original['inputs'] or plan['original_metadata']!=original['metadata'] or len(plan['commands'])!=9:raise A.OfflineError('projection_invalid')
    def verify_sources():
        verify_manifest(V/'READY_SHA256SUMS',a.ready_sha)
        verify_manifest(OLD/'PUBLIC_SHA256SUMS',plan['original_public_manifest_sha256'])
        for row in pins['tool_pins']+pins['source_and_prior_artifact_pins']:A.verify_tool(pathlib.Path(row['path']),row['sha256'])
        A.verify_tool(pathlib.Path('/opt/homebrew/Cellar/python@3.13/3.13.5/Frameworks/Python.framework/Versions/3.13/bin/python3.13'),'a1f6d9dc20d4787a84dc2fe782094a7bac5946f49a962aa0af48a02f0e8d5bc5')
    all_inputs=plan['original_inputs']+plan['original_metadata']+plan['derived_sched_inputs']
    def snapshot():
        result=[]
        for row in all_inputs:
            path=A._safe_path(pathlib.Path(row['path']));s=path.stat()
            now={'path':str(path),'sha256':A._sha(path),'bytes':s.st_size,'mtime_ns':s.st_mtime_ns,'device':s.st_dev,'inode':s.st_ino}
            if any(now[k]!=row[k] for k in now):raise A.OfflineError('input_changed')
            result.append(now)
        return result
    for d in [V,P,*[P/s for s in ['isolated-home','tmp','empty-binary-path']]]:
        A._safe_path(d)
        if not d.is_dir() or d.stat().st_uid!=os.getuid() or d.stat().st_mode&0o077:raise A.OfflineError('unsafe_path')
    verify_sources();before=snapshot();(V/'followup-once.claim').mkdir(mode=0o700);save(V/'inputs-before.json',before)
    results=[];coverage={};decoded={};state='complete';failure=None
    try:
        for cmd in plan['commands']:
            node=cmd['node_id'];kind=cmd['kind']
            if kind=='top_sched' and not decoded.get(node,False):continue
            if STOP or time.monotonic()+cmd['timeout_seconds']+3>deadline:raise A.OfflineError('timeout')
            argv=list(cmd['argv'])
            if kind=='decode_sched':argv[6]=coverage[node]
            pre=snapshot()
            result=A.run_bounded(argv,plan['environment_exact'],stdout_path=None if cmd['stdout_private_path'] is None else pathlib.Path(cmd['stdout_private_path']),stderr_path=pathlib.Path(cmd['stderr_private_path']),stdout_cap=cmd['stdout_stream_cap'],stderr_cap=16384,timeout_seconds=cmd['timeout_seconds'],stop_requested=lambda:STOP)
            result.update({'node_id':node,'kind':kind,'argv':argv,'env':plan['environment_exact'],'input_before':pre,'input_after':snapshot(),'timeout_seconds':cmd['timeout_seconds'],'stdout_cap':cmd['stdout_stream_cap'],'stderr_cap':16384});results.append(result);save(V/f'command-{len(results):02d}.json',result)
            if kind=='full_reader_validation_v2':coverage[node]=A.coverage_from_reader(result)
            elif kind=='decode_sched':
                decoded[node]=result['status']=='complete'
                if decoded[node]:
                    data=json.loads(pathlib.Path(cmd['stdout_private_path']).read_text())
                    if (data.get('node_id'),data.get('profile_type'),data.get('coverage'))!=(node,'sched',coverage[node]):raise A.OfflineError('projection_invalid')
                    allowed={'node_id','profile_type','coverage','aggregate_record_count','total_contentions_count','total_delay_ns','flat_disjoint_known_symbol_or_other','overlapping_known_symbol_counts_and_delay_explicitly_not_additive'}
                    if set(data)!=allowed:raise A.OfflineError('projection_invalid')
                    for name in ['flat_disjoint_known_symbol_or_other','overlapping_known_symbol_counts_and_delay_explicitly_not_additive']:
                        for row in data[name]:
                            if set(row)!={'function','count','delay_ns'} or row['function'] not in A.SYMBOLS|{'other'}:raise A.OfflineError('projection_invalid')
                    save(V/f'node-{node}-sched-aggregate.json',data,65536)
            print(json.dumps({'command':len(results),'node_id':node,'kind':kind,'status':result['status'],'error_class':result['error_class'],'elapsed_seconds':result['elapsed_seconds']}),flush=True)
        if len(results)!=9 or any(r['status']!='complete' for r in results):state='partial'
    except Exception as error:state='partial';failure=str(error) if isinstance(error,A.OfflineError) else 'unknown'
    finally:
        if STOP:state='partial';failure='timeout'
        try:after=snapshot();save(V/'inputs-after.json',after);unchanged=after==before;verify_sources()
        except Exception:unchanged=False;state='partial';failure='input_changed'
        save(V/'commands.json',results)
        save(V/'completion-receipt.json',{'status':state,'error_class':failure,'command_count':len(results),'coverage_by_node':coverage,'original18_plus_derived3_unchanged':unchanged,'all_direct_roots_joined':all(r['joined'] for r in results),'started_at_utc':began,'completed_at_utc':datetime.datetime.now(datetime.timezone.utc).isoformat(),'elapsed_seconds':time.monotonic()-start,'controlled_stop':STOP,'automatic_retry':False,'new_profile_generation':False,'optional_state_extractor':False,'v1_partials_retained':True})
if __name__=='__main__':main()
