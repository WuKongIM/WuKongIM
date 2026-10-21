#!/usr/bin/env python3
"""Authenticate stable candidate inputs before an audit writer can mutate GitHub."""
from __future__ import annotations

import ipaddress
import urllib.parse


class StableInputError(ValueError):
    """Stable identity or a production/test network boundary is invalid."""


def require(condition: bool, message: str) -> None:
    if not condition:
        raise StableInputError(message)


def network_mode(channel: str, test_only: bool, api: str, download: str) -> None:
    """Keep legacy preview routing and separate stable official/test endpoints."""
    require(channel in ('preview', 'stable'), 'unsupported audit channel')
    require(type(test_only) is bool, 'test_only must be boolean')
    if channel == 'preview':
        require(not test_only, 'TEST ONLY is only supported for stable audit transport')
        return
    for value in (api, download):
        parsed = urllib.parse.urlsplit(value)
        require(parsed.scheme in ('http', 'https') and bool(parsed.hostname)
                and parsed.username is None and parsed.password is None
                and not parsed.query and not parsed.fragment,
                'stable endpoint is invalid')
        if test_only:
            try:
                loopback = ipaddress.ip_address(parsed.hostname).is_loopback
            except ValueError:
                loopback = parsed.hostname == 'localhost'
            require(loopback, 'TEST ONLY endpoint must be loopback')
        else:
            require(value.rstrip('/') == 'https://api.github.com',
                    'production stable endpoint must use the official GitHub origin')

# Dependencies are loaded only from the reviewed executable checkout. Data roots
# never provide Python modules, tool paths, commands, or network endpoints.
import base64
import binascii
import hashlib
import importlib.util
import json
from pathlib import Path
from typing import Any

ROOT = Path(__file__).resolve().parent

def local_module(name: str) -> Any:
    spec = importlib.util.spec_from_file_location('seal_input_'+name.replace('-', '_'), ROOT/name)
    require(spec is not None and spec.loader is not None, 'cannot load reviewed dependency')
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module

C = local_module('compose-package-site.py')
PACKAGE_API = 'repos/WuKongIM/packages/'


def candidate_plan(path: Path, test_only: bool) -> dict[str, Any]:
    path = C.candidate_path(Path(path).parent, Path(path).name, 'candidate plan')
    plan = C.load_json(path, 'external candidate plan', canonical=True)
    require(isinstance(plan, dict) and plan.get('schema') == C.CANDIDATE_PLAN_SCHEMA,
            'stable requires an explicit candidate plan')
    candidate = plan.get('candidate')
    require(isinstance(candidate, dict), 'candidate must be an object')
    require(candidate.get('stage') == 'offline_candidate'
            and candidate.get('channel') == 'stable'
            and candidate.get('test_only') is test_only,
            'candidate mode differs from stable test/production mode')
    require(plan.get('audit_release_id') is None and plan.get('base_audit_release_id') is None,
            'candidate audit IDs must remain null')
    require(plan.get('operation') == 'add_release', 'only first-generation add_release is supported')
    require((test_only and plan.get('control_sha') is None)
            or (not test_only and isinstance(plan.get('control_sha'), str)
                and C.SHA_RE.fullmatch(plan['control_sha'])),
            'candidate control identity differs from mode')
    return plan


def current_control(reader: Any, expected_sha: str) -> None:
    value = reader.api_json(PACKAGE_API+'git/ref/heads/main')
    require(value.get('ref') == 'refs/heads/main'
            and value.get('object', {}).get('type') == 'commit'
            and value.get('object', {}).get('sha') == expected_sha,
            'packages main differs from expected seal control')


def require_ancestor(reader: Any, historical: str, current: str) -> None:
    require(isinstance(historical, str) and C.SHA_RE.fullmatch(historical),
            'historical control must be a commit SHA')
    if historical == current:
        return
    value = reader.api_json(PACKAGE_API+f'compare/{historical}...{current}')
    require(value.get('status') in ('ahead','identical')
            and value.get('base_commit', {}).get('sha') == historical
            and value.get('merge_base_commit', {}).get('sha') == historical,
            'historical control is not an ancestor of current packages main')


def verify_control(*, reader: Any, plan_path: Path, control_root: Path,
                   expected_control_sha: str, test_only: bool = False) -> dict[str, Any]:
    """Authenticate historical control/producer bytes, without trusting source JSON."""
    try:
        plan = candidate_plan(plan_path, test_only)
        require(isinstance(expected_control_sha,str) and C.SHA_RE.fullmatch(expected_control_sha),
                'seal control SHA is invalid')
        current_control(reader,expected_control_sha)
        expected = {}
        controls=C.exact(plan['candidate']['control_files'],set(C.CANDIDATE_CONTROL_FILES),'candidate control inventory')
        producers=C.exact(plan['candidate']['producer_files'],set(C.CANDIDATE_PRODUCER_FILES),'candidate producer inventory')
        for name,facts in controls.items():
            path=C.candidate_path(control_root,name,'control')
            require(C.hash_file(path,'control '+name,maximum_bytes=1024*1024)==facts,
                    'local control bytes differ: '+name)
            expected[name]=facts
        for name,facts in producers.items():
            require(C.hash_file(ROOT/name,'producer '+name,maximum_bytes=1024*1024)==facts,
                    'local reviewed producer bytes differ: '+name)
            expected['scripts/'+name]=facts
        if not test_only:
            historical=plan['control_sha']
            require_ancestor(reader,historical,expected_control_sha)
            commit=reader.api_json(PACKAGE_API+'git/commits/'+historical)
            tree_sha=commit.get('tree',{}).get('sha')
            require(commit.get('sha')==historical and isinstance(tree_sha,str)
                    and C.SHA_RE.fullmatch(tree_sha), 'historical commit tree is invalid')
            tree=reader.api_json(PACKAGE_API+'git/trees/'+tree_sha+'?recursive=1')
            require(tree.get('sha')==tree_sha and tree.get('truncated') is False
                    and isinstance(tree.get('tree'),list), 'historical tree is incomplete')
            entries={}
            for item in tree['tree']:
                require(isinstance(item,dict) and isinstance(item.get('path'),str), 'invalid Git tree entry')
                name=item['path']
                require(name not in entries, 'duplicate historical tree path')
                entries[name]=item
            for name,facts in expected.items():
                entry=entries.get(name,{})
                blob_sha=entry.get('sha')
                require(entry.get('type')=='blob' and entry.get('mode') in ('100644','100755')
                        and isinstance(blob_sha,str) and C.SHA_RE.fullmatch(blob_sha)
                        and type(entry.get('size')) is int and 0 < entry['size'] <= 1024*1024
                        and entry['size']==facts['size'], 'historical blob identity differs: '+name)
                blob=reader.api_json(PACKAGE_API+'git/blobs/'+blob_sha)
                require(blob.get('sha')==blob_sha and type(blob.get('size')) is int
                        and blob['size']==entry['size'] and blob.get('encoding')=='base64'
                        and isinstance(blob.get('content'),str)
                        and len(blob['content'])<=2*1024*1024, 'historical blob response differs: '+name)
                # GitHub wraps Base64 with newlines. No other ignored characters.
                raw=base64.b64decode(blob['content'].replace('\n',''),validate=True)
                require(len(raw)==facts['size'] and hashlib.sha256(raw).hexdigest()==facts['sha256']
                        and hashlib.sha1(b'blob '+str(len(raw)).encode()+b'\0'+raw).hexdigest()==blob_sha,
                        'historical blob bytes differ: '+name)
        current_control(reader,expected_control_sha)
        return {'candidate_control_sha':plan['control_sha'],'seal_control_sha':expected_control_sha,
                'control_files_verified':len(controls),'producer_files_verified':len(producers),
                'github_control_verified':not test_only,'test_only':test_only}
    except (C.CompositionError, OSError, KeyError, TypeError, binascii.Error) as error:
        raise StableInputError(str(error)) from error

import os
import subprocess
import sys
import tempfile

SOURCE = local_module('resolve-source-release.py')
AUDIT = local_module('package-audit-receipt.py')


def source_identity(value: dict[str, Any]) -> dict[str, Any]:
    """Exclude moving main observations, but bind every immutable source fact."""
    fields = ('repository','release_id','tag','version','prerelease','published_at',
              'source_sha','checksum_asset','checksum_entries')
    return {**{name:value[name] for name in fields},
            'assets':sorted(({key:item[key] for key in ('id','name','size','sha256')}
                             for item in value['assets']),key=lambda item:item['name'])}


def run_checked_helper(filename: str, arguments: list[str], *, home: Path,
                       source_token: str | None = None) -> dict[str, Any]:
    """Run a fixed reviewed CLI with isolated credentials and bounded receipt IO."""
    require(filename in ('verify-source-attestations.py','verify-source-package-metadata.py'),
            'unreviewed source helper')
    environment={'PATH':os.environ.get('PATH',os.defpath),'HOME':str(home),'LC_ALL':'C',
                 'GH_HOST':'github.com','GH_CONFIG_DIR':str(home/'gh'),
                 'GH_NO_UPDATE_NOTIFIER':'1','GH_PROMPT_DISABLED':'1','PYTHONNOUSERSITE':'1'}
    if source_token:
        environment['GH_TOKEN']=source_token
    with tempfile.TemporaryFile() as output, tempfile.TemporaryFile() as errors:
        try:
            result=subprocess.run([sys.executable,'-B',str(ROOT/filename),*arguments],
                stdin=subprocess.DEVNULL,stdout=output,stderr=errors,env=environment,
                timeout=700,check=False)
        except (OSError,subprocess.TimeoutExpired) as error:
            raise StableInputError('source verification helper did not complete: '+filename) from error
        require(result.returncode==0,'actual source verification failed: '+filename)
        output.seek(0); raw=output.read(AUDIT.MAX_JSON_BYTES+1)
        require(0 < len(raw) <= AUDIT.MAX_JSON_BYTES,'source verification receipt exceeds limit')
        return json.loads(raw,object_pairs_hook=C.reject_duplicate_pairs)


def verify_source(*, source_root: Path, source_token: str | None,
                  source_files: dict[str, Any]) -> dict[str, Any]:
    """Fetch and attest source bytes now; saved successful JSON is insufficient."""
    try:
        # Parse the same bounded bytes whose digest is in the verified plan.
        # Reopening the path after hashing would reintroduce a replacement window.
        def anchored_json(name: str) -> dict[str, Any]:
            path=C.candidate_path(source_root,name,'candidate source')
            raw=C.read_checked(path,'candidate source '+name,maximum_bytes=AUDIT.MAX_JSON_BYTES)
            actual={'size':len(raw),'sha256':hashlib.sha256(raw).hexdigest()}
            require(actual==source_files[name],'candidate source bytes differ from verified plan anchor: '+name)
            value=json.loads(raw,object_pairs_hook=C.reject_duplicate_pairs)
            require(isinstance(value,dict),'candidate source JSON must be an object')
            return value
        original=anchored_json('resolution.json')
        original_metadata=anchored_json('metadata.json')
        expected=source_identity(original)
        with tempfile.TemporaryDirectory(prefix='wk-seal-source-') as temporary:
            scratch=Path(temporary)
            fresh=SOURCE.resolve_source_release(release_id=original['release_id'],
                output_dir=scratch/'assets',api_base_url='https://api.github.com',
                download_base_url='https://api.github.com',token=source_token,channel='stable')
            require(source_identity(fresh)==expected,'fresh immutable source identity differs from candidate')
            receipt=scratch/'resolution.json'; receipt.write_bytes(C.canonical_json(fresh))
            run_checked_helper('verify-source-attestations.py',[
                '--channel','stable','--receipt',str(receipt),'--asset-dir',str(scratch/'assets'),
                '--evidence-dir',str(scratch/'attestations')],home=scratch,source_token=source_token)
            metadata=run_checked_helper('verify-source-package-metadata.py',[
                '--channel','stable','--receipt',str(receipt),'--asset-dir',str(scratch/'assets')],home=scratch)
            require(metadata==original_metadata,
                    'fresh native metadata differs from candidate')
        return expected
    except (SOURCE.ResolutionError,C.CompositionError,OSError,ValueError,KeyError,TypeError) as error:
        raise StableInputError(str(error)) from error


def revalidate_source(identity: dict[str, Any], source_token: str | None) -> None:
    """Re-read immutable source facts and ancestry immediately before publication."""
    try:
        reader=SOURCE.GitHubReader('https://api.github.com','https://api.github.com',source_token)
        release=SOURCE._release_snapshot(reader.api_json(
            f"repos/{SOURCE.SOURCE_REPOSITORY}/releases/{identity['release_id']}"),identity['release_id'],'stable')
        actual={'repository':SOURCE.SOURCE_REPOSITORY,'release_id':identity['release_id'],
            'tag':release['tag'],'version':release['version'],'prerelease':False,
            'published_at':release['published_at'],'source_sha':SOURCE._peel_tag(reader,release['tag']),
            'assets':sorted(release['assets'].values(),key=lambda item:item['name']),
            'checksum_asset':release['checksum_name'],'checksum_entries':identity['checksum_entries']}
        require(source_identity(actual)==identity,'source identity changed before immutable publication')
        SOURCE._verify_main_ancestry(reader,identity['source_sha'])
    except (SOURCE.ResolutionError, OSError, ValueError, KeyError, TypeError) as error:
        raise StableInputError(str(error)) from error


def candidate_identity(plan_path: Path, receipt_path: Path, *, test_only: bool) -> dict[str, Any]:
    """Bind a reader's expected pair without claiming signature or provenance trust."""
    try:
        require(plan_path is not None and receipt_path is not None,'stable candidate plan and receipt are required')
        plan_path=C.candidate_path(Path(plan_path).parent,Path(plan_path).name,'plan')
        receipt_path=C.candidate_path(Path(receipt_path).parent,Path(receipt_path).name,'receipt')
        plan=candidate_plan(plan_path,test_only)
        receipt=C.exact(C.load_json(receipt_path,'candidate receipt',canonical=True),
            {'schema','stage','channel','test_only','audit_release_id','control_sha','archive','inputs','verification'},
            'candidate receipt')
        require(receipt['schema']=='wukongim/package-audit-receipt/v3'
                and receipt['stage']=='offline_candidate' and receipt['channel']=='stable'
                and receipt['test_only'] is test_only and receipt['audit_release_id'] is None
                and receipt['control_sha']==plan['control_sha'], 'candidate receipt mode/control differs')
        archive=C.exact(receipt['archive'],{'name','size','sha256','snapshot_sha256'},'candidate archive')
        for name in ('sha256','snapshot_sha256'): AUDIT.sha256_string(archive[name],name)
        C.positive_integer(archive['size'],'candidate archive size')
        require(archive['size']<=AUDIT.ARCHIVE_MAX_TOTAL_BYTES,'candidate archive exceeds limit')
        names=AUDIT.candidate_archive_names(plan['target_version'],archive['snapshot_sha256'])
        require(archive['name']==names[0] and receipt_path.name==names[1], 'candidate filenames differ')
        require(isinstance(receipt['inputs'],dict) and isinstance(receipt['verification'],dict),
                'candidate inputs and verification must be objects')
        plan_facts=C.hash_file(plan_path,'candidate plan',maximum_bytes=AUDIT.MAX_JSON_BYTES)
        require(receipt['inputs'].get('audit/plan.json')==plan_facts,'candidate plan differs from receipt anchor')
        require(receipt['verification'].get('target_version')==plan['target_version'], 'candidate verified version differs')
        release=plan['release']
        require(release.get('package_release_id') is None, 'candidate package Release ID must remain null')
        if test_only:
            require(release['source_release_id'] is None and release['source_sha'] is None,
                    'TEST ONLY source IDs must remain null')
        else:
            C.positive_integer(release['source_release_id'],'source release ID')
            require(isinstance(release['source_sha'],str) and C.SHA_RE.fullmatch(release['source_sha']), 'source SHA is invalid')
        return {'version':plan['target_version'],'control_sha':plan['control_sha'],
            'source':{'repository':'WuKongIM/WuKongIM','release_id':release['source_release_id'],
                      'source_sha':release['source_sha']},
            'archive':{key:archive[key] for key in ('name','size','sha256')},
            'receipt':{'name':receipt_path.name,**C.hash_file(receipt_path,'candidate receipt',maximum_bytes=AUDIT.MAX_JSON_BYTES)},
            'snapshot_sha256':archive['snapshot_sha256'],'plan_sha256':plan_facts['sha256']}
    except (C.CompositionError,AUDIT.AuditReceiptError,OSError,KeyError,TypeError) as error:
        raise StableInputError(str(error)) from error


def verify_candidate(*, archive_path: Path, receipt_path: Path, plan_path: Path,
                     control_root: Path, source_root: Path, apt_receipt: Path,
                     rpm_receipt: Path, test_only: bool) -> dict[str, Any]:
    """Reuse the actual signature/closure verifier with independent local anchors."""
    require(all(item is not None for item in (archive_path,receipt_path,plan_path,control_root,
            source_root,apt_receipt,rpm_receipt)), 'all stable candidate anchors are required')
    try:
        return AUDIT.verify_audit_receipt(receipt_path=receipt_path,
            channels_path=control_root/'manifests/channels.json',
            signing_path=control_root/'manifests/stable-public-keys.json',
            signing_toolchain_path=control_root/'manifests/signing-toolchain.json',
            apt_public_cert_path=control_root/'keys/apt-stable.asc',
            rpm_public_cert_path=control_root/'keys/rpm-stable.asc',
            source_attestations_path=None,plan_path=plan_path,
            apt_receipt_path=apt_receipt,rpm_receipt_path=rpm_receipt,
            archive_path=archive_path,channel='stable',offline_candidate=True,
            control_root=control_root,candidate_source_root=source_root,test_only=test_only)
    except (AUDIT.AuditReceiptError,C.CompositionError,OSError,ValueError,TypeError,KeyError) as error:
        raise StableInputError(str(error)) from error


def verify_seal_inputs(*, reader: Any, expected_control_sha: str, archive_path: Path,
                       receipt_path: Path, plan_path: Path, control_root: Path,
                       source_root: Path, apt_receipt: Path, rpm_receipt: Path,
                       source_token: str | None, test_only: bool) -> dict[str, Any]:
    """Run checks in this process; no caller supplied 'verified' stamp can skip them."""
    plan=candidate_plan(plan_path,test_only)
    identity=candidate_identity(plan_path,receipt_path,test_only=test_only)
    require(hashlib.sha256(C.canonical_json(plan)).hexdigest()==identity['plan_sha256'],
            'candidate plan changed before seal input verification')
    control=verify_control(reader=reader,plan_path=plan_path,control_root=control_root,
                           expected_control_sha=expected_control_sha,test_only=test_only)
    verify_candidate(archive_path=archive_path,receipt_path=receipt_path,plan_path=plan_path,
        control_root=control_root,source_root=source_root,apt_receipt=apt_receipt,
        rpm_receipt=rpm_receipt,test_only=test_only)
    source=None if test_only else verify_source(source_root=source_root,source_token=source_token,
                                               source_files=plan['candidate']['source_files'])
    if source is not None:
        require(source['release_id']==identity['source']['release_id']
                and source['source_sha']==identity['source']['source_sha']
                and source['version']==identity['version'],
                'fresh source identity differs from verified candidate')
    require(candidate_identity(plan_path,receipt_path,test_only=test_only)==identity,
            'candidate anchors changed during seal input verification')
    current_control(reader,expected_control_sha)
    return {'control':control,'source':source,'source_verified':not test_only,'test_only':test_only}
