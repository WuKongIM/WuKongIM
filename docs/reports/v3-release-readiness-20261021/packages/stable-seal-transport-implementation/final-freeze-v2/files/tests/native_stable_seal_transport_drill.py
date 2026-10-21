"""Extend the signed candidate drill with loopback sealing and composite planning.

Stable signatures use the original real native fixture after its secrets have
been destroyed. Preview uses the existing structural transport fixture; this
module does not claim independent preview signature verification or GitHub truth.
"""
from pathlib import Path
import hashlib
import json
import subprocess
import sys

ROOT=Path(__file__).resolve().parents[1]

def canonical(value):
    return json.dumps(value,sort_keys=True,separators=(',',':')).encode()+b'\n'

def facts(path):
    raw=path.read_bytes();return {'size':len(raw),'sha256':hashlib.sha256(raw).hexdigest()}


def exercise(*,scratch,control,source,plan,archive,receipt,receipts,evidence,env):
    sys.path.insert(0,str(ROOT))
    from stable_audit_fixture import AuditState,serve_audit
    from test_plan_object_storage_publication import InputSnapshot
    records=[];evidence['transport_cases']=records
    def run(name,filename,args,*,reject=False,reason=None):
        evidence['phase']=name
        command=[sys.executable,'-B',str(ROOT/'scripts'/filename),*map(str,args)]
        result=subprocess.run(command,stdin=subprocess.DEVNULL,capture_output=True,env=env,timeout=180)
        passed=(result.returncode==1 and not result.stdout and reason in result.stderr.decode()) if reject else result.returncode==0
        records.append({'case':name,'passed':passed,'expected_rejection':reject,'exit_code':result.returncode,
            'stdout_sha256':hashlib.sha256(result.stdout).hexdigest(),'stderr_sha256':hashlib.sha256(result.stderr).hexdigest()})
        if not passed:
            # All commands below consume public data after test-secret removal.
            error=result.stderr.decode(errors='replace').splitlines()
            evidence['transport_failure']={'case':name,'diagnostic':error[0][:1024] if error else 'no diagnostic'}
            raise RuntimeError('native seal/transport command failed')
        return result
    before={str(path):facts(path) for path in (plan,archive,receipt)}
    state=AuditState(); seal_control='a'*40
    with serve_audit(state) as base:
        test_env={**env,'WK_TEST_AUDIT_TOKEN':'TEST ONLY loopback token'}
        common=['--channel','stable','--test-only','--release-id','4242','--expected-control-sha',seal_control,
                '--api-base-url',base,'--token-env','WK_TEST_AUDIT_TOKEN','--candidate-plan',plan]
        # Bind/seal subprocesses use only this explicit synthetic token.
        saved=env;env=test_env
        run('stable-bind','bind-audit-release.py',[*common,'--expected-previous-control-sha',seal_control,'--control-root',control])
        seal=run('stable-seal','seal-audit-release.py',[*common,'--download-base-url',base,
            '--archive',archive,'--receipt',receipt,'--control-root',control,'--candidate-source-root',source,
            '--apt-receipt',receipts['apt'],'--rpm-receipt',receipts['rpm']])
        sealed=json.loads(seal.stdout)
        assert sealed['test_only'] is True and sealed['source_verified'] is False
        resolve=run('stable-resolve','resolve-audit-release.py',[*common,'--download-base-url',base,
            '--candidate-receipt',receipt,'--output-dir',scratch/'resolved-stable','--expected-tag-state','exact'])
        resolution=json.loads(resolve.stdout)
        env=saved
    assert state.immutable and not state.draft and len(state.assets)==2
    assert len([x for x in state.requests if x[0]=='POST' and '/assets?' in x[1]])==2
    assert not [x for x in state.requests if x[0]=='DELETE']
    assert before=={str(path):facts(path) for path in (plan,archive,receipt)}
    resolution_path=scratch/'stable-resolution.json';resolution_path.write_bytes(canonical(resolution))
    preview=InputSnapshot(scratch/'preview-transport-fixture')
    stable={'channel':'stable','audit_release_id':4242,'seal_control_sha':seal_control,
        'candidate':resolution['candidate'],'resolution':str(resolution_path),
        'resolution_sha256':facts(resolution_path)['sha256'],'archive':str(archive),'receipt':str(receipt),
        'plan':str(plan),'control_dir':str(control),'source_dir':str(source),
        'apt_receipt':str(receipts['apt']),'rpm_receipt':str(receipts['rpm'])}
    inputs={'schema':'wukongim.object_storage_plan_inputs/v2','channel':'composite','test_only':True,
        'current':{'preview':{'channel':'preview','snapshot':preview.descriptor()},'stable':stable},'retain':[]}
    input_path=scratch/'composite-inputs.json';input_path.write_bytes(canonical(inputs))
    output=scratch/'composite-plan.json'
    run('composite-plan','plan-object-storage-publication.py',['--composite','--test-only','--inputs',input_path,'--output',output])
    planned=json.loads(output.read_bytes())
    assert planned['test_only'] is True and planned['plan_only'] is True
    assert planned['verification']['stable_signatures_verified'] is True
    assert planned['verification']['github_provenance_verified'] is False
    assert planned['verification']['activation_authorized'] is False
    assert planned['retention']['delete_objects']==[]
    shared={'/repo','/index.html','/status.json','/signing-manifest.txt','/bootstrap/manifest.json'}
    for item in planned['entrypoints']:
        if item['public_uri'] in shared: assert item['object_key'].startswith('snapshots/preview/')
        assert item['public_uri']!='/repo-stable'
        assert not item['public_uri'].startswith('/rpm/stable/el/9/x86_64/repodata/')
    mirror=planned['generated_objects'][0]
    assert mirror['content_utf8'].startswith('https://packages.githubim.com/snapshots/stable/r4242-')
    assert mirror['content_utf8'].endswith('/site/rpm/stable/el/9/x86_64/\n')
    assert mirror['sha256']==hashlib.sha256(mirror['content_utf8'].encode()).hexdigest()
    second=scratch/'composite-repeat.json'
    run('composite-repeat','plan-object-storage-publication.py',['--composite','--test-only','--inputs',input_path,'--output',second])
    assert output.read_bytes()==second.read_bytes()
    run('reject-test-without-optin','plan-object-storage-publication.py',['--composite','--inputs',input_path,'--output',scratch/'forbidden-plan.json'],
        reject=True,reason='TEST ONLY input requires matching explicit test_only mode')
    # A local seal observation is explicitly a TEST ONLY input claim. Give an
    # older generation its own observation/ID and require independent validation.
    retained_resolution=json.loads(canonical(resolution))
    retained_resolution.update(release_id=4241,tag_name='native-package-stable-r4241',
                               name='WuKongIM stable package snapshot r4241')
    retained_resolution_path=scratch/'retained-resolution.json'
    retained_resolution_path.write_bytes(canonical(retained_resolution))
    retained={**stable,'audit_release_id':4241,'resolution':str(retained_resolution_path),
              'resolution_sha256':facts(retained_resolution_path)['sha256']}
    inputs['retain']=[retained]
    input_path.write_bytes(canonical(inputs))
    retained_output=scratch/'composite-retained.json'
    run('composite-retained','plan-object-storage-publication.py',
        ['--composite','--test-only','--inputs',input_path,'--output',retained_output])
    retained_plan=json.loads(retained_output.read_bytes())
    assert retained_plan['retention']['retained_audit_ids']==[4241]
    assert retained_plan['retention']['delete_objects']==[]
    assert {item['source']['audit_release_id'] for item in retained_plan['snapshot_objects']}=={10,4241,4242}
    assert all('/r4241-' not in item['object_key'] for item in retained_plan['entrypoints'])
    altered=scratch/'altered-retained.tar'
    raw=bytearray(archive.read_bytes()); raw[len(raw)//2]^=1; altered.write_bytes(raw)
    retained['archive']=str(altered);input_path.write_bytes(canonical(inputs))
    forbidden=scratch/'forbidden-retained.json'
    run('reject-changed-retained','plan-object-storage-publication.py',
        ['--composite','--test-only','--inputs',input_path,'--output',forbidden],
        reject=True,reason='candidate archive differs from independent anchor')
    assert not forbidden.exists()
    evidence['transport']={'candidate_bytes_preserved':True,'actual_stable_signatures_verified':True,
        'preview_fixture':'structural-only','source_provenance_verified':False,'public_api_writes':False,
        'plan':facts(output),'asset_count':len(state.assets),'request_methods':[method for method,_ in state.requests]}
    assert len(records)==8 and all(item['passed'] for item in records)
