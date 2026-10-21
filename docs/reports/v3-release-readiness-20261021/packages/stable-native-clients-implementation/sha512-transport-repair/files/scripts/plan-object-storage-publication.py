#!/usr/bin/env python3
"""Plan offline transport of preview audit snapshots; never upload or activate."""

from __future__ import annotations

import argparse
import hashlib
import importlib.util
import json
import os
import re
import stat
import sys
import tempfile
from pathlib import Path
from typing import Any

ROOT = Path(__file__).resolve().parents[1]


def _load(name: str, filename: str) -> Any:
    spec = importlib.util.spec_from_file_location(name, ROOT / 'scripts' / filename)
    if spec is None or spec.loader is None:
        raise RuntimeError(f'cannot load reviewed helper: {filename}')
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


R = _load('storage_plan_audit_receipt', 'package-audit-receipt.py')
C = _load('storage_plan_composer', 'compose-package-site.py')
INPUT_SCHEMA = 'wukongim.object_storage_plan_inputs/v1'
PLAN_SCHEMA = 'wukongim.object_storage_publication_plan/v1'
MAX_SNAPSHOTS = 8
MAX_TOTAL_ARCHIVE_BYTES = 2 * 1024 * 1024 * 1024
CONTROL_FILES = {
    'manifests/channels.json', 'manifests/preview-signing.json',
    'manifests/signing-toolchain.json', 'manifests/bootstrap-packages.json',
    'keys/apt-preview.asc', 'keys/rpm-preview.asc',
}
DESCRIPTOR_FIELDS = {
    'archive', 'receipt', 'control_dir', 'audit_release_id', 'control_sha',
    'archive_sha256', 'archive_size', 'receipt_sha256', 'snapshot_sha256',
    'source', 'control_files_sha256',
}
SOURCE_FIELDS = {'repository', 'version', 'release_id', 'source_sha'}
PUBLIC_PATH = re.compile(r'^[A-Za-z0-9_./+~-]+$')
RPM_REPO = 'rpm/preview/el/9/x86_64/'
IMMUTABLE_CACHE = 'public, max-age=31536000, immutable'


class PlanningError(ValueError):
    """The offline inputs cannot produce a consistent, bounded transport plan."""


def require(condition: bool, message: str) -> None:
    if not condition:
        raise PlanningError(message)


def local_path(value: Any, base: Path, label: str) -> Path:
    require(isinstance(value, str) and value and '\x00' not in value
            and '://' not in value, f'{label} must be a local path')
    path = Path(value)
    return path if path.is_absolute() else base / path


def real_directory(path: Path, label: str) -> None:
    mode = path.lstat().st_mode
    require(stat.S_ISDIR(mode) and not stat.S_ISLNK(mode), f'{label} must be a real directory')


def checked_file(path: Path, label: str, *, maximum_bytes: int,
                 expected_size: int | None = None) -> dict[str, Any]:
    """Reject size mismatches before opening a single-link regular input."""
    metadata = path.lstat()
    require(stat.S_ISREG(metadata.st_mode) and metadata.st_nlink == 1,
            f'{label} must be a single-link regular file')
    if expected_size is not None:
        require(metadata.st_size == expected_size, f'{label} size differs from independent anchor')
    return C.hash_file(path, label, maximum_bytes=maximum_bytes)


def control_size_limit(relative: str) -> int:
    return (1024 * 1024 if relative.startswith('keys/')
            or relative == 'manifests/signing-toolchain.json' else R.MAX_JSON_BYTES)


def descriptor_identity(raw: Any) -> dict[str, Any]:
    item = C.exact(raw, DESCRIPTOR_FIELDS, 'snapshot descriptor')
    C.positive_integer(item['audit_release_id'], 'audit_release_id')
    C.positive_integer(item['archive_size'], 'archive_size')
    require(isinstance(item['control_sha'], str) and R.LOWER_SHA.fullmatch(item['control_sha']),
            'expected control identity must be a lowercase commit SHA')
    for field in ('archive_sha256', 'receipt_sha256', 'snapshot_sha256'):
        R.sha256_string(item[field], field)
    source = C.exact(item['source'], SOURCE_FIELDS, 'expected source identity')
    require(source['repository'] == R.SOURCE_REPOSITORY, 'source identity repository is unsupported')
    C.positive_integer(source['release_id'], 'source release ID')
    require(isinstance(source['source_sha'], str) and R.LOWER_SHA.fullmatch(source['source_sha']),
            'source identity commit is invalid')
    require(isinstance(source['version'], str) and C.VERSION_RE.fullmatch(source['version']),
            'source identity must be a strict preview version')
    hashes = C.exact(item['control_files_sha256'], CONTROL_FILES, 'control file SHA-256 inventory')
    for path, value in hashes.items():
        R.sha256_string(value, f'control file SHA-256 {path}')
    return item


def _public_path(path: str) -> None:
    require(PUBLIC_PATH.fullmatch(path) is not None, f'unsafe public path: {path}')
    C.safe_relative(path, 'public path')


def _metadata_closure(root: Path, control: Path, receipt: dict[str, Any]) -> tuple[dict, set[str]]:
    """Check reference bytes and safe paths, without running signatures or payloads."""
    site = root / 'site'
    files, directories = C.collect_tree(site, 'snapshot site')
    for path in files:
        _public_path(path)
    snapshot = C.load_json(root / 'audit/snapshot.json', 'snapshot', canonical=True)
    plan = C.load_json(root / 'audit/plan.json', 'publication plan', canonical=True)
    bootstrap = C.validate_bootstrap_inventory(
        C.load_json(site / 'bootstrap/manifest.json', 'bootstrap manifest', canonical=True),
        prepared=False,
    )
    bootstrap_control = C.exact(
        C.load_json(control / 'manifests/bootstrap-packages.json', 'bootstrap control'),
        {'schema', 'enabled', 'version'}, 'bootstrap control',
    )
    require(bootstrap_control == {'schema': 'wukongim.native_package_bootstrap/v1',
                                  'enabled': True, 'version': bootstrap['version']},
            'bootstrap control does not match the snapshot')
    for item in bootstrap['packages'].values():
        facts = {'sha256': item['published_sha256'], 'size': item['published_size']}
        for field in ('repository_path', 'download_path'):
            require(files.get(item[field]) == facts, f'bootstrap {field} bytes differ from manifest')
    rpm_key = snapshot['public_keys']['rpm']['sha256']
    require(C.read_checked(site / 'repo', 'repository entrypoint', maximum_bytes=C.MAX_METADATA_BYTES)
            == C.repository_entrypoint_bytes(bootstrap, rpm_key),
            'repository entrypoint differs from bootstrap identity')
    require(C.load_json(site / 'status.json', 'site status', canonical=True) == {
        'schema': C.STATUS_SCHEMA, 'apt': True, 'rpm': True, 'reason': 'ready',
        'audit_release_id': receipt['audit_release_id'], 'control_sha': receipt['control_sha'],
        'snapshot_sha256': receipt['archive']['snapshot_sha256'],
        'operation': receipt['plan']['operation'], 'target_version': receipt['plan']['target_version'],
    }, 'site status differs from the reviewed audit')
    inventory = {
        family: {entry['version']: dict(entry, new=entry['version'] in plan['new_versions'])
                 for entry in snapshot['payloads'][family]}
        for family in ('apt', 'rpm')
    }
    # Native zstd is deliberately outside this pure-Python planner's boundary.
    rpm_receipt = C.load_json(root / 'audit/rpm-signing.json', 'RPM signing receipt', canonical=True)
    repodata = rpm_receipt['result']['repodata']
    for item in repodata:
        relative = item['path']
        _public_path(relative)
        if relative in {'repodata/repomd.xml', 'repodata/repomd.xml.asc'}:
            continue
        require(Path(relative).suffix not in {'.zst', '.zstd'},
                'unsupported RPM metadata compression: zstd requires native verification')
    apt_files, _, _ = C.validate_apt_tree(
        site / 'apt', root / 'audit/apt-signing.json', inventory['apt'],
        bootstrap['packages']['apt'], plan['active_versions'], plan['retained_versions'],
    )
    rpm_files, _, _ = C.validate_rpm_tree(
        site / 'rpm', root / 'audit/rpm-signing.json', inventory['rpm'],
        bootstrap['packages']['rpm'], plan['active_versions'], plan['retained_versions'],
    )
    root_files = {'index.html', 'repo', 'status.json', 'signing-manifest.txt',
                  'keys/apt-preview.asc', 'keys/rpm-preview.asc', 'bootstrap/manifest.json'}
    bootstrap_downloads = {item['download_path'] for item in bootstrap['packages'].values()}
    expected = root_files | bootstrap_downloads | {'apt/' + p for p in apt_files} | {'rpm/' + p for p in rpm_files}
    require(set(files) == expected, 'unexpected site object or missing site closure')
    C.require_tree_closure(files, directories, expected, 'snapshot site')
    immutable = bootstrap_downloads | {
        path for path in files
        if path.startswith('apt/pool/') or '/by-hash/SHA256/' in path
        or path.startswith(RPM_REPO + 'Packages/')
    } | {RPM_REPO + item['path'] for item in repodata
         if item['path'] not in {'repodata/repomd.xml', 'repodata/repomd.xml.asc'}
         and Path(item['path']).name.startswith(item['sha256'] + '-')}
    return files, immutable


def _snapshot(item: dict[str, Any], base: Path) -> dict[str, Any]:
    archive = local_path(item['archive'], base, 'archive')
    receipt_path = local_path(item['receipt'], base, 'receipt')
    control = local_path(item['control_dir'], base, 'control directory')
    real_directory(control, 'control directory')
    initial_control = {}
    for relative, expected in sorted(item['control_files_sha256'].items()):
        initial_control[relative] = checked_file(control / relative, f'control file {relative}',
                                                 maximum_bytes=control_size_limit(relative))
        require(initial_control[relative]['sha256'] == expected,
                f'control file SHA-256 differs from independent anchor: {relative}')
    # The receipt helper checks byte relationships. Composer adds the explicit
    # preview-only version/capacity and disabled-stable scope restrictions.
    C.validate_channels(C.load_json(control / 'manifests/channels.json', 'channels control'))
    archive_facts = checked_file(archive, 'archive', maximum_bytes=item['archive_size'],
                                 expected_size=item['archive_size'])
    require(archive_facts['sha256'] == item['archive_sha256'], 'archive SHA-256 differs from independent anchor')
    receipt_facts = checked_file(receipt_path, 'receipt', maximum_bytes=R.MAX_JSON_BYTES)
    require(receipt_facts['sha256'] == item['receipt_sha256'], 'receipt SHA-256 differs from independent anchor')
    receipt, _ = R.load_json(receipt_path, 'external receipt', canonical=True)
    require(receipt.get('audit_release_id') == item['audit_release_id'], 'audit identity differs from independent anchor')
    require(receipt.get('control_sha') == item['control_sha'], 'control identity differs from independent anchor')
    require(receipt.get('archive', {}).get('snapshot_sha256') == item['snapshot_sha256'],
            'snapshot identity differs from independent anchor')
    require(receipt.get('plan', {}).get('operation') == 'add_release',
            'only add_release audit snapshots are supported by this planner')
    source = receipt.get('source')
    require(isinstance(source, dict) and {
        'repository': R.SOURCE_REPOSITORY, 'version': receipt['plan']['target_version'],
        'release_id': source.get('release_id'), 'source_sha': source.get('source_sha'),
    } == item['source'], 'source identity differs from independent anchor')
    with tempfile.TemporaryDirectory(prefix='wk-object-storage-plan-') as temporary:
        root = Path(temporary) / 'snapshot'
        summary = R.snapshot_archive.extract_snapshot(
            archive_path=archive, output_dir=root,
            max_members=R.snapshot_archive.DEFAULT_MAX_MEMBERS,
            max_total_size=R.ARCHIVE_MAX_TOTAL_BYTES,
        )
        require(summary['sha256'] == archive_facts['sha256'] and summary['size'] == archive_facts['size'],
                'archive changed before extraction')
        for member in summary['members']:
            if member['type'] == 'file' and member['name'].startswith('site/'):
                _public_path(member['name'][5:])
        verified = R.verify_audit_receipt(
            receipt_path=receipt_path, archive_path=archive,
            channels_path=control / 'manifests/channels.json',
            signing_path=control / 'manifests/preview-signing.json',
            signing_toolchain_path=control / 'manifests/signing-toolchain.json',
            apt_public_cert_path=control / 'keys/apt-preview.asc',
            rpm_public_cert_path=control / 'keys/rpm-preview.asc',
            plan_path=root / 'audit/plan.json', apt_receipt_path=root / 'audit/apt-signing.json',
            rpm_receipt_path=root / 'audit/rpm-signing.json',
            source_attestations_path=root / 'audit/source-attestations',
        )
        require(verified == receipt, 'receipt changed while validating snapshot')
        files, immutable = _metadata_closure(root, control, verified)
        prefix = f"snapshots/preview/r{item['audit_release_id']}-{item['snapshot_sha256']}"
        objects = []
        for member in summary['members']:
            if member['type'] != 'file':
                continue
            path = member['name']
            _public_path(path)
            facts = C.hash_file(root / path, 'snapshot object')
            objects.append({'object_key': prefix + '/' + path, 'sha256': facts['sha256'],
                            'size': facts['size'],
                            'cache_control': ('no-store' if path.startswith('site/')
                                              and path[5:] not in immutable else IMMUTABLE_CACHE),
                            'source': {'audit_release_id': item['audit_release_id'], 'member': path}})
        identity = {key: item[key] for key in (
            'audit_release_id', 'control_sha', 'archive_sha256', 'archive_size',
            'receipt_sha256', 'snapshot_sha256', 'source', 'control_files_sha256',
        )}
        result = {'identity': identity, 'prefix': prefix, 'objects': objects,
                  'files': files, 'immutable': immutable}
    require(checked_file(archive, 'archive', maximum_bytes=item['archive_size'],
                         expected_size=item['archive_size']) == archive_facts, 'archive changed while planning')
    require(checked_file(receipt_path, 'receipt', maximum_bytes=R.MAX_JSON_BYTES,
                         expected_size=receipt_facts['size']) == receipt_facts, 'receipt changed while planning')
    for relative, facts in initial_control.items():
        require(checked_file(control / relative, 'control file', maximum_bytes=control_size_limit(relative),
                             expected_size=facts['size']) == facts,
                'control file changed while planning')
    return result


def composite_inputs(input_path: Path, test_only: bool) -> dict[str, Any]:
    input_path=C.candidate_path(input_path.parent,input_path.name,'composite inputs')
    inputs=C.exact(C.load_json(input_path,'composite planner inputs'),
        {'schema','channel','current','retain','test_only'},'composite planner inputs')
    require(inputs['schema']=='wukongim.object_storage_plan_inputs/v2'
            and inputs['channel']=='composite','explicit composite v2 input is required')
    require(type(inputs['test_only']) is bool and inputs['test_only'] is test_only,
            'TEST ONLY input requires matching explicit test_only mode')
    C.exact(inputs['current'],{'preview','stable'},'current composite channels')
    require(isinstance(inputs['retain'],list),'retained snapshots must be an array')
    return inputs


STABLE_DESCRIPTOR_FIELDS = {
    'channel','audit_release_id','seal_control_sha','candidate','resolution','resolution_sha256',
    'archive','receipt','plan','control_dir','source_dir','apt_receipt','rpm_receipt',
}


def _stable_snapshot(item: dict[str, Any], base: Path, test_only: bool) -> dict[str, Any]:
    P=_load('storage_stable_inputs','verify-stable-seal-inputs.py')
    def path(field: str, directory: bool = False) -> Path:
        value=local_path(item[field],base,field)
        C.candidate_path(value if directory else value.parent,'probe' if directory else value.name,field)
        if directory: real_directory(value,field)
        return value
    control=path('control_dir',True); source=path('source_dir',True)
    archive=path('archive'); receipt=path('receipt'); plan=path('plan')
    resolution_path=path('resolution')
    resolution_raw=C.read_checked(resolution_path,'external seal resolution',maximum_bytes=R.MAX_JSON_BYTES)
    require(hashlib.sha256(resolution_raw).hexdigest()==item['resolution_sha256'],
            'seal resolution differs from independent anchor')
    resolution=json.loads(resolution_raw,object_pairs_hook=C.reject_duplicate_pairs)
    require(resolution_raw==C.canonical_json(resolution),'seal resolution must use canonical JSON encoding')
    C.exact(resolution,{'schema','repository','release_id','tag_name','name','prerelease','control_sha',
        'tag_commit_verified','classification','draft','immutable','published_at','asset_count','total_size',
        'assets','release_revalidated','id_bound_downloads','channel','test_only','candidate'},'seal resolution')
    require(resolution['schema']=='wukongim/package-audit-release-resolution/v2'
            and resolution['repository']=='WuKongIM/packages' and resolution['channel']=='stable'
            and resolution['test_only'] is test_only
            and resolution['release_id']==item['audit_release_id']
            and resolution['control_sha']==item['seal_control_sha'], 'seal identity differs from descriptor')
    require(resolution['classification']=='immutable_complete' and resolution['draft'] is False
            and resolution['immutable'] is True and resolution['prerelease'] is True
            and resolution['tag_commit_verified'] is True and resolution['release_revalidated'] is True
            and resolution['id_bound_downloads'] is True and isinstance(resolution['published_at'],str)
            and bool(resolution['published_at']), 'planner requires a complete immutable seal observation')
    require(resolution['tag_name']==f"native-package-stable-r{item['audit_release_id']}"
            and resolution['name']==f"WuKongIM stable package snapshot r{item['audit_release_id']}",
            'stable reserved audit identity differs')
    candidate=P.candidate_identity(plan,receipt,test_only=test_only)
    require(candidate==item['candidate']==resolution['candidate'],'candidate tuple differs from external seal anchors')
    archive_facts=checked_file(archive,'stable archive',maximum_bytes=candidate['archive']['size'],
                               expected_size=candidate['archive']['size'])
    require(archive_facts=={key:candidate['archive'][key] for key in ('size','sha256')},
            'candidate archive differs from independent anchor')
    require(isinstance(resolution['assets'],list) and len(resolution['assets'])==2
            and type(resolution['asset_count']) is int and resolution['asset_count']==2,
            'sealed audit requires exactly two assets')
    assets={};ids=set()
    for raw in resolution['assets']:
        value=C.exact(raw,{'id','name','size','sha256','downloaded_file'},'sealed asset')
        C.positive_integer(value['id'],'sealed asset id');C.positive_integer(value['size'],'sealed asset size')
        require(value['id'] not in ids and value['name'] not in assets,'duplicate sealed asset identity')
        require(value['downloaded_file']==value['name'],'sealed downloaded filename differs')
        ids.add(value['id']);assets[value['name']]={key:value[key] for key in ('name','size','sha256')}
    require(assets=={candidate[k]['name']:candidate[k] for k in ('archive','receipt')}
            and type(resolution['total_size']) is int
            and resolution['total_size']==sum(value['size'] for value in assets.values()),
            'sealed assets differ from candidate pair')
    P.verify_candidate(archive_path=archive,receipt_path=receipt,plan_path=plan,control_root=control,
        source_root=source,apt_receipt=path('apt_receipt'),rpm_receipt=path('rpm_receipt'),test_only=test_only)
    with tempfile.TemporaryDirectory(prefix='wk-stable-transport-') as temporary:
        root=Path(temporary)/'snapshot'
        summary=R.snapshot_archive.extract_snapshot(archive_path=archive,output_dir=root,
            max_members=R.snapshot_archive.DEFAULT_MAX_MEMBERS,max_total_size=R.ARCHIVE_MAX_TOTAL_BYTES)
        require({'sha256':summary['sha256'],'size':summary['size']}==archive_facts,
                'stable archive changed during planning')
        files,_=C.collect_tree(root/'site','stable snapshot site')
        for name in files: _public_path(name)
        bootstrap=C.validate_bootstrap_inventory(C.load_json(root/'site/bootstrap/manifest.json',
            'stable bootstrap',canonical=True),prepared=False,channel='stable')
        rpm_receipt=C.load_json(root/'audit/rpm-signing.json','stable RPM receipt',canonical=True)
        repo='rpm/stable/el/9/x86_64/'
        immutable={value['download_path'] for value in bootstrap['packages'].values()} | {
            name for name in files if name.startswith('apt/pool/') or '/by-hash/SHA256/' in name
            or name.startswith(repo+'Packages/')}
        for value in rpm_receipt['result']['repodata']:
            name=value['path'];_public_path(name)
            if name not in ('repodata/repomd.xml','repodata/repomd.xml.asc') and Path(name).name.startswith(value['sha256']+'-'):
                immutable.add(repo+name)
        prefix=f"snapshots/stable/r{item['audit_release_id']}-{candidate['snapshot_sha256']}"
        objects=[]
        for member in summary['members']:
            if member['type']!='file': continue
            name=member['name'];_public_path(name)
            facts=C.hash_file(root/name,'stable snapshot member')
            objects.append({'object_key':prefix+'/'+name,**facts,
                'cache_control':'no-store' if name.startswith('site/') and name[5:] not in immutable else IMMUTABLE_CACHE,
                'source':{'audit_release_id':item['audit_release_id'],'channel':'stable','member':name}})
        # Native verification above authenticates every Release hash section.
        # Expose the SHA512 URLs preferred by APT without changing archive bytes.
        derived_aliases=[]
        for filename in ('Packages','Packages.gz'):
            name='apt/dists/stable/main/binary-amd64/'+filename
            raw=C.read_checked(root/'site'/name,'stable APT transport index',
                               expected=files[name],maximum_bytes=C.MAX_METADATA_BYTES)
            alias='apt/dists/stable/main/binary-amd64/by-hash/SHA512/'+hashlib.sha512(raw).hexdigest()
            derived_aliases.append({'public_uri':'/'+alias,'object_key':'public/'+alias,
                **files[name],'cache_control':IMMUTABLE_CACHE,
                'source':{'audit_release_id':item['audit_release_id'],'channel':'stable','member':'site/'+name}})
    require(checked_file(archive,'stable archive',maximum_bytes=archive_facts['size'],expected_size=archive_facts['size'])==archive_facts,
            'stable archive changed during planning')
    require(P.candidate_identity(plan,receipt,test_only=test_only)==candidate,
            'candidate anchors changed during planning')
    return {'identity':{'channel':'stable','audit_release_id':item['audit_release_id'],
        'seal_control_sha':item['seal_control_sha'],'candidate':candidate,
        'resolution_sha256':item['resolution_sha256']},'prefix':prefix,'objects':objects,
        'files':files,'immutable':immutable,'derived_aliases':derived_aliases}


def make_composite_plan(input_path: Path, test_only: bool, output_path: Path | None = None) -> dict[str, Any]:
    inputs=composite_inputs(input_path,test_only)
    descriptors=[inputs['current']['preview'],inputs['current']['stable'],*inputs['retain']]
    require(len(descriptors)<=MAX_SNAPSHOTS,'planner accepts at most 8 snapshots')
    identities=[];sizes=[]
    for number,raw in enumerate(descriptors):
        require(isinstance(raw,dict),'snapshot descriptor must be an object')
        channel=raw.get('channel')
        if number<2: require(channel==('preview','stable')[number],'current snapshot channel differs')
        if channel=='preview':
            C.exact(raw,{'channel','snapshot'},'preview descriptor')
            item=descriptor_identity(raw['snapshot'])
            identities.append(item['audit_release_id']);sizes.append(item['archive_size'])
        else:
            require(channel=='stable','unsupported snapshot channel')
            C.exact(raw,STABLE_DESCRIPTOR_FIELDS,'stable descriptor')
            C.positive_integer(raw['audit_release_id'],'stable audit release ID')
            require(isinstance(raw['seal_control_sha'],str) and R.LOWER_SHA.fullmatch(raw['seal_control_sha']),
                    'seal control SHA is invalid')
            R.sha256_string(raw['resolution_sha256'],'resolution SHA')
            candidate=C.exact(raw['candidate'],{'version','control_sha','source','archive','receipt',
                'snapshot_sha256','plan_sha256'},'candidate identity')
            size=C.positive_integer(candidate['archive']['size'],'candidate archive size')
            identities.append(raw['audit_release_id']);sizes.append(size)
    require(len(set(identities))==len(identities),'duplicate audit identity in planner inputs')
    require(sum(sizes)<=MAX_TOTAL_ARCHIVE_BYTES,'planner total archive byte limit is 2 GiB')
    if output_path is not None:
        boundaries=[input_path]
        for raw in descriptors:
            item=raw['snapshot'] if raw['channel']=='preview' else raw
            fields=('archive','receipt','control_dir') if raw['channel']=='preview' else (
                'archive','receipt','plan','resolution','control_dir','source_dir','apt_receipt','rpm_receipt')
            for field in fields:
                path=local_path(item[field],input_path.parent,field)
                if os.path.lexists(path): boundaries.append(path)
        C.validate_candidate_output_paths((output_path,),tuple(boundaries))
    snapshots=[]
    for raw in descriptors:
        if raw['channel']=='preview':
            snapshot=_snapshot(raw['snapshot'],input_path.parent)
            snapshot['identity']['channel']='preview'
            for obj in snapshot['objects']: obj['source']['channel']='preview'
        else:
            snapshot=_stable_snapshot(raw,input_path.parent,test_only)
        snapshots.append(snapshot)
    preview,stable=snapshots[:2]
    aliases={}
    def add_alias(value: dict[str, Any]) -> None:
        path=value['public_uri'][1:]
        if path in aliases:
            require(all(aliases[path][key]==value[key] for key in ('object_key','sha256','size','cache_control')),
                    'immutable URI conflict across snapshots: /'+path)
        else: aliases[path]=value
    for snapshot in snapshots:
        for path in sorted(snapshot['immutable']):
            facts=snapshot['files'][path]
            value={'public_uri':'/'+path,'object_key':'public/'+path,**facts,'cache_control':IMMUTABLE_CACHE,
                'source':{'audit_release_id':snapshot['identity']['audit_release_id'],
                          'channel':snapshot['identity']['channel'],'member':'site/'+path}}
            add_alias(value)
        for value in snapshot.get('derived_aliases',[]):
            add_alias(value)
    entrypoints=[]
    for snapshot in (preview,stable):
        for path,facts in sorted(snapshot['files'].items()):
            if path in snapshot['immutable']: continue
            if snapshot is stable and not (path.startswith('apt/') or path.startswith('keys/')): continue
            entrypoints.append({'public_uri':'/'+path,'object_key':snapshot['prefix']+'/site/'+path,
                                **facts,'cache_control':'no-store'})
    # The mirrorlist is a transport object. The archived, signed repomd bytes are untouched.
    mirror=('https://packages.githubim.com/'+stable['prefix']+'/site/rpm/stable/el/9/x86_64/\n')
    mirror_facts={'sha256':hashlib.sha256(mirror.encode()).hexdigest(),'size':len(mirror.encode())}
    mirror_key='generated/stable-mirrorlists/'+mirror_facts['sha256']+'.txt'
    generated=[{'object_key':mirror_key,**mirror_facts,'cache_control':'no-store','content_utf8':mirror}]
    entrypoints.append({'public_uri':'/rpm/stable/el/9/x86_64/mirrorlist','object_key':mirror_key,
                        **mirror_facts,'cache_control':'no-store'})
    public={}
    for value in [*aliases.values(),*entrypoints]:
        uri=value['public_uri']
        require(uri not in public or all(public[uri][key]==value[key] for key in ('sha256','size','cache_control')),
                'public URI conflict between channels: '+uri)
        public[uri]=value
    return {'schema':'wukongim.object_storage_publication_plan/v2','channel':'composite',
        'status':'plan_only','plan_only':True,'test_only':test_only,
        'current_audit_release_ids':{'preview':identities[0],'stable':identities[1]},
        'verification':{'archive_and_receipt_consistent':True,'metadata_closure_checked':True,
            'signatures_verified':False,'stable_signatures_verified':True,'github_provenance_verified':False,
            'control_commit_verified':False,'cloud_verified':False,'activation_authorized':False},
        'snapshots':sorted((item['identity'] for item in snapshots),key=lambda item:item['audit_release_id']),
        'snapshot_objects':sorted((obj for item in snapshots for obj in item['objects']),key=lambda obj:obj['object_key']),
        'immutable_aliases':[aliases[key] for key in sorted(aliases)],
        'entrypoints':sorted(entrypoints,key=lambda item:item['public_uri']),'generated_objects':generated,
        'retention':{'policy':'retain_all_supplied_snapshots_no_gc','retained_audit_ids':sorted(identities[2:]),'delete_objects':[]},
        'limitations':['Local seal JSON is caller-reviewed evidence, not authenticated GitHub truth.',
            'Production activation must fresh-resolve exact audit/source/control identities.',
            'Preview signatures are not reverified by the legacy offline planner.',
            'No upload, activation, cloud state, atomic switch or deletion is established.']}


def make_plan(input_path: Path, *, composite: bool = False, test_only: bool = False,
              output_path: Path | None = None) -> dict[str, Any]:
    """Build a deterministic plan, retaining every explicitly supplied generation."""
    if composite:
        return make_composite_plan(input_path,test_only,output_path)
    require(not test_only,'TEST ONLY requires explicit composite mode')
    inputs = C.exact(C.load_json(input_path, 'planner inputs'),
                     {'schema', 'channel', 'current', 'retain'}, 'planner inputs')
    require(inputs['schema'] == INPUT_SCHEMA, f'planner input schema must be {INPUT_SCHEMA}')
    require(inputs['channel'] == 'preview', 'only preview offline transport planning is supported')
    require(isinstance(inputs['retain'], list), 'retain must be an array')
    descriptors = [inputs['current'], *inputs['retain']]
    require(len(descriptors) <= MAX_SNAPSHOTS, 'planner accepts at most 8 snapshots')
    descriptors = [descriptor_identity(item) for item in descriptors]
    require(sum(item['archive_size'] for item in descriptors) <= MAX_TOTAL_ARCHIVE_BYTES,
            'planner total archive byte limit is 2 GiB')
    audit_ids = [item['audit_release_id'] for item in descriptors]
    require(len(set(audit_ids)) == len(audit_ids), 'duplicate audit identity in planner inputs')
    snapshots = [_snapshot(item, input_path.parent) for item in descriptors]
    current = snapshots[0]
    snapshots.sort(key=lambda snapshot: snapshot['identity']['audit_release_id'])
    aliases: dict[str, dict[str, Any]] = {}
    for snapshot in snapshots:
        for path in sorted(snapshot['immutable']):
            facts = snapshot['files'][path]
            uri = '/' + path
            item = {'public_uri': uri, 'object_key': 'public/' + path,
                    'sha256': facts['sha256'], 'size': facts['size'], 'cache_control': IMMUTABLE_CACHE,
                    'source': {'audit_release_id': snapshot['identity']['audit_release_id'], 'member': 'site/' + path}}
            if uri in aliases:
                require(all(aliases[uri][field] == item[field]
                            for field in ('object_key', 'sha256', 'size', 'cache_control')),
                        f'immutable URI conflict across snapshots: {uri}')
            else:
                aliases[uri] = item
    entrypoints = [
        {'public_uri': '/' + path, 'object_key': current['prefix'] + '/site/' + path,
         'sha256': facts['sha256'], 'size': facts['size'], 'cache_control': 'no-store'}
        for path, facts in sorted(current['files'].items()) if path not in current['immutable']
    ]
    return {
        'schema': PLAN_SCHEMA, 'status': 'plan_only', 'plan_only': True, 'channel': 'preview',
        'current_audit_release_id': current['identity']['audit_release_id'],
        'verification': {'archive_and_receipt_consistent': True, 'metadata_closure_checked': True,
                         'signatures_verified': False, 'github_provenance_verified': False,
                         'control_commit_verified': False, 'cloud_verified': False,
                         'activation_authorized': False},
        'snapshots': [snapshot['identity'] for snapshot in snapshots],
        'snapshot_objects': sorted((obj for snapshot in snapshots for obj in snapshot['objects']),
                                   key=lambda item: item['object_key']),
        'immutable_aliases': [aliases[key] for key in sorted(aliases)],
        'entrypoints': entrypoints,
        'retention': {'policy': 'retain_all_supplied_snapshots_no_gc',
                      'retained_audit_ids': sorted(audit_ids[1:]), 'delete_objects': []},
        'limitations': ['External anchors are caller-reviewed claims, not authenticated by this command.',
                        'Only preview add_release snapshots are supported; no stable or multi-channel activation.',
                        'No CDN atomicity, signature freshness, rollback eligibility or cloud state is established.',
                        'No upload, activation, signing, credential access or deletion is performed.'],
    }


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--composite',action='store_true')
    parser.add_argument('--test-only',action='store_true')
    parser.add_argument('--inputs', required=True, type=Path, help='Local descriptor with independently reviewed identities')
    parser.add_argument('--output', required=True, type=Path, help='New canonical JSON plan; existing paths are rejected')
    args = parser.parse_args(argv)
    try:
        require(not os.path.lexists(args.output), 'planner output already exists')
        real_directory(args.output.parent, 'output parent')
        plan = make_plan(args.inputs,composite=args.composite,test_only=args.test_only,output_path=args.output)
        raw = C.canonical_json(plan)
        R._write_exclusive(args.output, plan)
    except (PlanningError, C.CompositionError, R.AuditReceiptError,
            R.snapshot_archive.ArchiveError, OSError, ValueError, TypeError, KeyError) as error:
        print(f'object-storage planning failed: {error}', file=sys.stderr)
        return 1
    print(C.canonical_json({'schema': 'wukongim.object_storage_plan_receipt/v1', 'status': 'plan_only',
                            'plan_sha256': hashlib.sha256(raw).hexdigest(), 'plan_size': len(raw),
                            'snapshot_count': len(plan['snapshots'])}).decode(), end='')
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
