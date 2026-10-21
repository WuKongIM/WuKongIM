from pathlib import Path
import json
p=Path(__file__).resolve().parent
a=p/'candidate-artifacts/artifact-12344894821'; summary={'artifact_id':12344894821,'identity':json.loads((a/'identity.json').read_text()),'suites':{},'analysis_note':'Initial offline generic assertion assumed no E2E skips; raw report showed existing opt-in 2000QPS comparison skipped. Only that exact documented opt-in is accepted below; no workflow rerun or test change.'}
for name in ['ordinary_history','cross_node_delivery','self_chat']:
 events=[json.loads(x) for x in (a/name/'stdout.jsonl').read_text().splitlines() if x.startswith('{')]
 run=[e['Test'] for e in events if e['Action']=='run' and e.get('Test')];terminal=[e for e in events if e['Action'] in ['pass','fail','skip'] and e.get('Test')]
 unmatched=sorted(set(run)-{e['Test'] for e in terminal});r=json.loads((a/name/'result.json').read_text())
 assert r['passed'] and r['cleanup']['complete'] and not r['output_truncated'] and not r['cleanup']['remaining_processes']
 assert not unmatched and not any(e['Action']=='fail' for e in terminal)
 skips=[e for e in terminal if e['Action']=='skip']; expected_skips=['TestThreeNodeReplicaLoadComparisonAtTwoThousandQPS'] if name=='cross_node_delivery' else []
 assert [e['Test'] for e in skips]==expected_skips
 summary['suites'][name]={'pass_events':sum(e['Action']=='pass' for e in terminal),'root_pass':sum(e['Action']=='pass' and '/' not in e['Test'] for e in terminal),'events':terminal,'skip_reasons':[e for e in events if e['Action']=='output' and 'set WK_E2E_REPLICA_LOAD_COMPARISON=1' in e.get('Output','')],'unfinished':unmatched,'result':r}
info=(a/'binary-build-info.txt').read_text();assert 'vcs.revision=922bc66a9cebcbe09bef5fc43afc0593c4f07fb8' in info and 'vcs.modified=false' in info
summary['binary_sha256']=(a/'binary.sha256').read_text().split()[0];summary['build_vcs_revision']='922bc66a9cebcbe09bef5fc43afc0593c4f07fb8';summary['build_vcs_modified']=False
report=json.loads((a/'self_chat/report.json').read_text()); assert report['passed'] and all(c['passed'] and c['binary_sha256']==summary['binary_sha256'] for c in report['cases'])
summary['self_chat_cases']=[{k:c[k] for k in ['nodes','phase','passed','binary_sha256','invalid_auth_refused','whole_cluster_restarts']} for c in report['cases']]
(p/'candidate-e2e-summary.json').write_text(json.dumps(summary,indent=2)+'\n')
f=json.loads((p/'chat-selected/artifact-12344867793/artifacts/report/final.json').read_text());q=json.loads((p/'chat-selected/artifact-12344867793/artifacts/report/qualification.json').read_text()); local=json.loads((p/'chat-selected/artifact-12344867793/artifacts/local-step.json').read_text())
assert local['outcome']=='clean' and local['reason']=='complete' and local['qualification_reached'] and f['verdict']['outcome']=='operator_stop' and f['verdict']['cleanup_error_count']==0 and not f['harness']['failures']
(p/'chat-correctness-summary.json').write_text(json.dumps({'artifact_id':12344867793,'source_sha':(p/'chat-selected/artifact-12344867793/source.sha').read_text().strip(),'local_step':local,'final_window':f['window'],'final_verdict':f['verdict'],'messages':f['messages'],'harness':f['harness'],'correlation':f['correlation'],'qualification_window':q['window'],'qualification_online':q['sessions']['online'],'limitation':'This is PR smoke with bounded measured 90-second step and coordinated operator_stop; not formal 2h/72h qualification.'},indent=2)+'\n')
print(json.dumps({k:{x:v[x] for x in ['root_pass','pass_events']} for k,v in summary['suites'].items()}))
