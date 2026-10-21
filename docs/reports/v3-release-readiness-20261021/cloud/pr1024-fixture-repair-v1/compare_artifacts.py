from pathlib import Path
import copy,hashlib,json,re,sys
baseline=Path('/tmp/wukongim-v3-readiness-20261021/cloud/rc-new-cee5/terminal-evidence-repair-v2/focused-corrected-results.json')
candidate=Path(sys.argv[1]);destination=Path(sys.argv[2]);before=json.loads(baseline.read_text());after=json.loads(candidate.read_text())
assert len(before['cases'])==len(after['cases'])==79
assert [x['case'] for x in before['cases']]==[x['case'] for x in after['cases']]
assert before['tests_run']==after['tests_run']==20
assert before['failure_count']==after['failure_count']==0
assert before['error_count']==after['error_count']==0
assert before['schema']==after['schema']
assert before['fixture_only']==after['fixture_only']==True
results=[]
for old,new in zip(before['cases'],after['cases']):
    left=copy.deepcopy(old);right=copy.deepcopy(new);normalization=[]
    for side,case in [('baseline',left),('candidate',right)]:
        manifest=case['manifest'];raw=(json.dumps(manifest,indent=2,ensure_ascii=False)+'\n').encode();entry=next(x for x in case['files'] if x['path']=='manifest.json')
        assert len(raw)==entry['bytes'];assert hashlib.sha256(raw).hexdigest()==entry['sha256']
        stamp=manifest['observed_at'];assert re.fullmatch(r'\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ',stamp)
        manifest['observed_at']='2000-01-01T00:00:00Z';canonical=(json.dumps(manifest,indent=2,ensure_ascii=False)+'\n').encode()
        normalized_sha=hashlib.sha256(canonical).hexdigest();normalization.append(dict(side=side,observed_at=stamp,original_manifest_sha256=entry['sha256'],normalized_manifest_sha256=normalized_sha));entry['sha256']=normalized_sha
    result=dict(case=old['case'],equal=left==right,normalization=normalization)
    if left!=right:
        result['baseline']=left;result['candidate']=right
    results.append(result)
summary=dict(schema='fixture-equivalence/v1',baseline=dict(path=str(baseline),sha256=hashlib.sha256(baseline.read_bytes()).hexdigest(),source_sha=before['source_sha']),candidate=dict(path=str(candidate),sha256=hashlib.sha256(candidate.read_bytes()).hexdigest(),source_sha=after['source_sha']),normalization_allowlist=['cases[].manifest.observed_at','cases[].files[path=manifest.json].sha256 derived only from observed_at normalization; originals verified'],provenance_note='source_sha headers are separately recorded provenance, not normalized or compared as runtime behavior; case receipts otherwise compare entire objects',cases_compared=len(results),cases_equal=sum(x['equal'] for x in results),results=results)
destination.write_text(json.dumps(summary,indent=2)+'\n');print(json.dumps({k:v for k,v in summary.items() if k!='results'},indent=2));sys.exit(summary['cases_equal']!=79)
