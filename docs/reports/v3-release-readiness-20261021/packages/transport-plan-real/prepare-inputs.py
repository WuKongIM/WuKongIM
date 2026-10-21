#!/usr/bin/env python3
"""Check already downloaded public audit bytes and write anchored planner input."""
import argparse
import datetime
import hashlib
import json
from pathlib import Path
import tarfile

parser = argparse.ArgumentParser()
parser.add_argument('--root', required=True, type=Path)
args = parser.parse_args()
root = args.root.resolve()

def load(name):
    return json.loads((root / name).read_text())

def digest(path):
    h = hashlib.sha256()
    with path.open('rb') as f:
        for chunk in iter(lambda: f.read(1024 * 1024), b''):
            h.update(chunk)
    return h.hexdigest()

audit = load('audit-release.json')
tag = load('audit-tag.json')
source = load('source-release.json')
source_tag = load('source-tag.json')
source_tag_object = load('source-tag-object.json')
control = load('control-commit.json')
control_files = load('control-file-anchors.json')
assert audit['id'] == 402342684 and audit['immutable'] is True and audit['draft'] is False
assert audit['tag_name'] == 'native-package-preview-r402342684'
assert tag['object']['type'] == 'commit' and tag['object']['sha'] == control['sha'] == 'feb7f21503d35133cf23df7d7ab250c5d4129e71'
assert source['id'] == 402354460 and source['immutable'] is True and source['draft'] is False
assert source['tag_name'] == source_tag_object['tag'] == 'v3.0.0-beta.24'
assert source_tag['object']['type'] == 'tag' and source_tag['object']['sha'] == source_tag_object['sha']
assert source_tag_object['object']['type'] == 'commit' and source_tag_object['object']['sha'] == '4d44b39dda6fdaa7dafb637ca6cfb718e9530f77'
expected = {
    'wukongim-preview-r402342684-site.tar': (607274043, 308536832, '71eb2b1b338742b9669ebfc7135ede8a12f73f395b3171e6375f18240d5c3a07'),
    'wukongim-preview-r402342684-receipt.json': (607274289, 3743, 'c156796d4559f93c600ac3196537fc0a858b9de1ae0d233bfe94fd333d7617bb'),
}
assert {a['name'] for a in audit['assets']} == set(expected)
assets = []
for item in audit['assets']:
    identifier, size, sha = expected[item['name']]
    assert (item['id'], item['size'], item['digest']) == (identifier, size, 'sha256:' + sha)
    path = root / item['name']
    actual = {'size': path.stat().st_size, 'sha256': digest(path)}
    assert actual == {'size': size, 'sha256': sha}
    assets.append({'name': item['name'], 'asset_id': identifier, 'numeric_api': f'https://api.github.com/repos/WuKongIM/packages/releases/assets/{identifier}',
                   'public_url': item['browser_download_url'], 'expected': {'size':size, 'sha256':sha}, 'actual':actual})

receipt = load('wukongim-preview-r402342684-receipt.json')
assert receipt['audit_release_id'] == audit['id']
assert receipt['control_sha'] == control['sha']
assert receipt['archive']['name'] == 'wukongim-preview-r402342684-site.tar'
assert receipt['archive']['size'] == expected[receipt['archive']['name']][1]
assert receipt['archive']['sha256'] == expected[receipt['archive']['name']][2]
assert receipt['source']['release_id'] == source['id'] and receipt['source']['source_sha'] == source_tag_object['object']['sha']
assert receipt['plan']['operation'] == 'add_release' and receipt['plan']['target_version'] == '3.0.0-beta.24'
source_assets = {a['name']:a for a in source['assets']}
for family in ['deb','rpm']:
    name = f'wukongim_3.0.0-beta.24_linux_amd64.{family}'
    assert source_assets[name]['digest'] == 'sha256:' + receipt['source'][family + '_sha256']
for item in control_files['files']:
    assert item['exact_git_commit'] == receipt['control_sha']
    assert item['sha256'] == digest(root / 'control' / item['path'])

with tarfile.open(root / receipt['archive']['name'], mode='r:') as archive:
    selected = {}
    for name in ['audit/snapshot.json','audit/source-attestations/source-attestations.json','site/status.json','audit/rpm-signing.json']:
        member = archive.getmember(name)
        assert member.isfile() and 0 < member.size < 1024*1024
        selected[name] = archive.extractfile(member).read()
snapshot_sha = hashlib.sha256(selected['audit/snapshot.json']).hexdigest()
assert snapshot_sha == receipt['archive']['snapshot_sha256'] == '242af0efec792b089661a5411e0b57b954998432cf13df9c657fb2d823851be8'
assert hashlib.sha256(selected['audit/source-attestations/source-attestations.json']).hexdigest() == receipt['source']['attestation_summary_sha256']
status = json.loads(selected['site/status.json'])
assert status['audit_release_id'] == audit['id'] and status['control_sha'] == control['sha'] and status['snapshot_sha256'] == snapshot_sha
channels = json.loads((root / 'control/manifests/channels.json').read_text())
rpm_receipt = json.loads(selected['audit/rpm-signing.json'])
inputs = {'schema':'wukongim.object_storage_plan_inputs/v1', 'channel':'preview',
          'current': {'archive':receipt['archive']['name'], 'receipt':'wukongim-preview-r402342684-receipt.json', 'control_dir':'control',
                      'audit_release_id':audit['id'], 'control_sha':receipt['control_sha'], 'archive_sha256':receipt['archive']['sha256'],
                      'archive_size':receipt['archive']['size'], 'receipt_sha256':expected['wukongim-preview-r402342684-receipt.json'][2],
                      'snapshot_sha256':snapshot_sha,
                      'source':{'repository':'WuKongIM/WuKongIM','version':'3.0.0-beta.24','release_id':source['id'],'source_sha':receipt['source']['source_sha']},
                      'control_files_sha256':{i['path']:i['sha256'] for i in control_files['files']}}, 'retain':[]}
verification = {'schema':'wukongim/real-preview-plan-input-preparation/v1','observed_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),
                'audit_release_id':audit['id'],'audit_immutable_observed':True,'assets':assets,'control_sha':control['sha'],
                'source_release_id':source['id'],'source_sha':receipt['source']['source_sha'],'snapshot_sha256':snapshot_sha,
                'rpm_metadata_paths':[i['path'] for i in rpm_receipt['result']['repodata']],
                'signatures_verified':False,'full_source_provenance_verified':False,'payload_executed':False,
                'scope':'Public API identity observations and complete downloaded SHA256/size; selected JSON consistency. Planner must independently validate archive and metadata closure.'}
for name,data in [('inputs.json',inputs),('input-verification.json',verification)]:
    path=root/name
    with path.open('x') as out:
        out.write(json.dumps(data,ensure_ascii=False,indent=2)+'\n')
print(json.dumps({'status':'inputs_ready','audit_release_id':audit['id'],'assets_verified':len(assets),'control_files':len(control_files['files']),'snapshot_sha256':snapshot_sha}))
