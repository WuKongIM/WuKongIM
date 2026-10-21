#!/usr/bin/env python3
"""Offline verification of the exact PR 67 native preflight CI evidence."""
import argparse, hashlib, json, re, zipfile
from pathlib import Path

HEAD = '0ce7184bbf69472f607159f74a096e9160852843'
MERGE = '4e73d6fbeb255d1206409537d00f1d76eb151623'
RUNS = {39549984615: '.github/workflows/pages.yml', 39549984616: '.github/workflows/native-package-repository-drill.yml'}
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--root', required=True, type=Path)
parser.add_argument('--output', required=True, type=Path)
args = parser.parse_args()
assert not args.output.exists(), 'output must be new'
def read(name): return json.loads((args.root/name).read_text())
head, merge = read('head-commit.json'), read('synthetic-merge-commit.json')
assert head['sha'] == HEAD and merge['sha'] == MERGE
assert head['tree']['sha'] == merge['tree']['sha']
assert [p['sha'] for p in merge['parents']] == ['feb7f21503d35133cf23df7d7ab250c5d4129e71', HEAD]
runs = read('runs-final.json')['workflow_runs']
assert {r['id'] for r in runs} == set(RUNS), 'unexpected duplicate or missing run'
run_facts = []
for r in runs:
    assert r['head_sha'] == HEAD and r['path'] == RUNS[r['id']]
    assert r['event'] == 'pull_request' and r['run_attempt'] == 1
    assert r['status'] == 'completed' and r['conclusion'] == 'success'
    jobs = read(f"{r['id']}-jobs-final.json")['jobs']
    assert all(j['status'] == 'completed' for j in jobs)
    if r['id'] == 39549984615:
        assert len(jobs) == 2
        job = next(j for j in jobs if j['name'] == 'Validate complete Pages snapshot')
        assert job['conclusion'] == 'success'
        stages = {s['name']:s['conclusion'] for s in job['steps']}
        assert stages['Validate repository contracts'] == 'success'
        assert stages['Configure Pages'] == 'skipped'
        assert stages['Upload complete Pages artifact'] == 'skipped'
        assert next(j for j in jobs if j['name'] == 'Deploy complete Pages snapshot')['conclusion'] == 'skipped'
    else:
        assert len(jobs) == 1 and jobs[0]['conclusion'] == 'success'
        stages = {s['name']:s['conclusion'] for s in jobs[0]['steps']}
        for stage in ['Validate reviewed control','Verify trusted repository tool bytes',
                      'Check real native source metadata in isolated TEST ONLY fixtures',
                      'Preserve TEST ONLY native metadata evidence','Build unsigned TEST ONLY packages',
                      'Build, sign, verify, and install TEST ONLY repositories','Reject tracked-tree mutation']:
            assert stages[stage] == 'success', stage
    log_path = args.root/f"{r['id']}-logs.zip"
    with zipfile.ZipFile(log_path) as logs:
        assert logs.testzip() is None
        combined = [name for name in logs.namelist() if '/' not in name and name.endswith('.txt')]
        assert len(combined) == 1
        lines = logs.read(combined[0]).decode().splitlines()
        checkout = next(i for i,line in enumerate(lines) if 'git log -1 --format=%H' in line)
        assert lines[checkout+1].endswith(' ' + MERGE), 'actual checkout is not the equivalent synthetic merge'
        start = next(i for i,line in enumerate(lines) if '##[group]GITHUB_TOKEN Permissions' in line)
        end = next(i for i in range(start+1,len(lines)) if '##[endgroup]' in lines[i])
        permissions = [line.split('Z ',1)[1] for line in lines[start+1:end]]
        assert permissions == ['Contents: read','Metadata: read'], permissions
        if r['id'] == 39549984615:
            assert any('Ran 342 tests in 100.482s' in line for line in lines)
    run_facts.append({'actual_checkout':MERGE,'token_permissions':permissions,
                      'logs_sha256':hashlib.sha256(log_path.read_bytes()).hexdigest(),
                      'run_id':r['id'],'url':r['html_url'],'attempt':1,'conclusion':'success',
                      'jobs':[{'name':j['name'],'conclusion':j['conclusion'],'steps':j['steps']} for j in jobs]})
artifacts = read('artifacts-final.json')['artifacts']
assert len(artifacts) == 1
artifact = artifacts[0]
assert artifact['id'] == 12321989695 and artifact['name'] == 'native-source-metadata-test-only'
assert artifact['expired'] is False
assert artifact['workflow_run']['id'] == 39549984616
assert artifact['workflow_run']['head_sha'] == HEAD
archive = args.root/'native-source-metadata-test-only.zip'
blob = archive.read_bytes()
assert len(blob) == artifact['size_in_bytes'] == 1267
assert 'sha256:' + hashlib.sha256(blob).hexdigest() == artifact['digest']
with zipfile.ZipFile(archive) as z:
    assert z.testzip() is None
    assert z.namelist() == ['native-source-metadata.json']
    raw = z.read('native-source-metadata.json')
assert raw == (args.root/'native-source-metadata.json').read_bytes()
j = json.loads(raw)
assert j['schema'] == 'wukongim/test-only-native-source-metadata/v1'
assert j['test_only'] is True and j['passed'] is True
assert j['payloads_executed'] is False and j['synthetic_source_provenance'] is True
assert j['native_signature_tags'] == ['RSAHEADER']
expected = [('stable',0,None),('wrong-channel',1,'preview source tag'),
            ('wrong-deb-version',1,'DEB Version mismatch'),('preview',0,None),
            ('wrong-rpm-version',1,'RPM VERSION mismatch'),('wrong-rpm-epoch',1,'RPM EPOCHNUM mismatch'),
            ('signed-rpm',1,'RPM RSAHEADER:pgpsig mismatch')]
assert len(j['cases']) == len(expected)
for c,(name,code,error) in zip(j['cases'],expected):
    assert c['case'] == name and c['exit_code'] == code and c['passed'] is True
    if error:
        assert error in c['stderr'] and c['metadata'] is None
        continue
    m = c['metadata']
    version = '3.1.0' if name == 'stable' else '3.1.0-rc.1-hotfix'
    assert m['schema'] == 'wukongim/source-package-metadata/v2' and m['channel'] == name
    assert m['version'] == version and m['package'] == 'wukongim'
    assert m['source'] == {'repository':'WuKongIM/WuKongIM','release_id':4242,'tag':'v'+version,'source_sha':'1'*40}
    assert m['assets_revalidated_after_metadata'] is True
    assert m['deb']['version'] == ('3.1.0' if name == 'stable' else '3.1.0~rc.1-hotfix')
    assert m['rpm']['version'] == ('3.1.0' if name == 'stable' else '3.1.0~rc.1_hotfix')
    assert m['deb']['architecture'] == 'amd64' and m['rpm']['architecture'] == 'x86_64'
    assert m['rpm']['epoch'] == '0' and m['rpm']['release'] == '1' and m['rpm']['signed'] is False
    for family in ('deb','rpm'):
        a=m[family]['asset']
        assert a['name'] == f'wukongim_{version}_linux_amd64.{family}'
        assert isinstance(a['id'],int) and a['id']>0 and isinstance(a['size'],int) and a['size']>0
        assert re.fullmatch('[0-9a-f]{64}',a['sha256'])
result={'verified':True,'source_head':HEAD,'synthetic_merge_sha':MERGE,'tree_sha':head['tree']['sha'],
        'synthetic_merge_tree_equals_head':True,'run_facts':run_facts,
        'artifact':{'id':artifact['id'],'size':len(blob),'sha256':hashlib.sha256(blob).hexdigest(),
                    'member_sha256':hashlib.sha256(raw).hexdigest(),'zip_crc_verified':True},
        'native_cases':j['cases'],'actual_native_signature_tags':j['native_signature_tags'],
        'native_tools':j['tool_versions'],
        'limits':['TEST ONLY synthetic source receipt is not a published stable Release or GitHub provenance proof.',
                  'Native rejection actually exercised RSAHEADER; other signature field tests use a tool-boundary stub.',
                  'Metadata fixture payloads were not executed; existing complete TEST ONLY repository drill separately installs its own test packages.',
                  'Stable publication is still disabled; no production signing/deployment occurred.']}
with args.output.open('x') as f:json.dump(result,f,indent=2);f.write('\n')
print(json.dumps({'verified':True,'runs':[r['run_id'] for r in run_facts],'native_cases':len(j['cases']),'output':str(args.output)}))
