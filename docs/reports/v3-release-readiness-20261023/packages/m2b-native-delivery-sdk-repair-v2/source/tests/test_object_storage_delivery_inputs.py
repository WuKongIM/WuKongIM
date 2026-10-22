"""Read-only fresh identity wiring and independent SDK CI contracts."""
import copy
import hashlib
import importlib.util
import json
from pathlib import Path
import socket
import tempfile
import unittest
from unittest import mock

from test_stable_seal_inputs import GitObjects, HEAD, HISTORY, TREE
from test_stable_offline_candidate import CandidateFixture, canonical
from tests.test_plan_object_storage_publication import InputSnapshot

ROOT=Path(__file__).resolve().parents[1]


def load():
    spec=importlib.util.spec_from_file_location('fresh_delivery_inputs',ROOT/'scripts/verify-object-storage-delivery-inputs.py')
    module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module);return module


class FreshInputContract(unittest.TestCase):
    def setUp(self):
        self.p=load();self.temp=tempfile.TemporaryDirectory();self.addCleanup(self.temp.cleanup)
        self.root=Path(self.temp.name).resolve()
        guard=mock.patch.object(socket.socket,'connect',side_effect=AssertionError('network forbidden'))
        guard.start();self.addCleanup(guard.stop)

    def test_current_historical_candidate_and_seal_controls_remain_distinct(self):
        f=CandidateFixture(self.root);plan=f.plan();plan['control_sha']=HISTORY
        plan['candidate']['test_only']=False;plan['candidate']['source_kind']='stable_preflight'
        plan['release'].update(source_release_id=12,source_sha='d'*40)
        path=self.root/'plan.json';path.write_bytes(canonical(plan))
        reader=GitObjects(f,plan);seal='e'*40;original=reader.api_json
        def read(p):
            if p==f'repos/WuKongIM/packages/compare/{seal}...{HEAD}':
                return dict(status='ahead',base_commit={'sha':seal},merge_base_commit={'sha':seal})
            return original(p)
        reader.api_json=read
        value=self.p.verify_stable_control(reader=reader,plan_path=path,control_root=f.control,
                                          executor_sha=HEAD,seal_control_sha=seal)
        self.assertEqual(value['candidate_control_sha'],HISTORY)
        self.assertEqual(value['seal_control_sha'],seal)
        self.assertEqual(value['executor_sha'],HEAD)
        self.assertTrue(value['github_control_verified'])

    def test_nonancestor_seal_current_main_and_historical_blob_fail_closed(self):
        for fault in ('seal','main','blob'):
            with self.subTest(fault=fault),tempfile.TemporaryDirectory(dir=self.root) as root:
                f=CandidateFixture(Path(root));plan=f.plan();plan['control_sha']=HISTORY
                plan['candidate']['test_only']=False;plan['candidate']['source_kind']='stable_preflight'
                plan['release'].update(source_release_id=12,source_sha='d'*40)
                path=f.root/'plan.json';path.write_bytes(canonical(plan));reader=GitObjects(f,plan)
                if fault=='seal':reader.status='diverged'
                elif fault=='main':reader.main='f'*40
                else:next(iter(reader.blobs.values()))['content']='QQ=='
                with self.assertRaises(ValueError):self.p.verify_stable_control(reader=reader,plan_path=path,
                    control_root=f.control,executor_sha=HEAD,seal_control_sha=HISTORY)

    def test_audit_identity_binds_ids_bytes_and_immutable_state_not_observation_time(self):
        original={'repository':'WuKongIM/packages','release_id':91,'tag_name':'native-package-preview-r91',
            'control_sha':HISTORY,'classification':'immutable_complete','draft':False,'immutable':True,
            'published_at':'2026-10-21T00:00:00Z','tag_commit_verified':True,'id_bound_downloads':True,
            'release_revalidated':True,'assets':[{'id':1,'name':'a','size':1,'sha256':'a'*64},
                                                {'id':2,'name':'b','size':2,'sha256':'b'*64}]}
        changed=copy.deepcopy(original);changed['observed_at']='later'
        self.assertEqual(self.p.audit_identity(original),self.p.audit_identity(changed))
        for field,value in [('id',3),('size',9),('sha256','c'*64)]:
            changed=copy.deepcopy(original);changed['assets'][0][field]=value
            self.assertNotEqual(self.p.audit_identity(original),self.p.audit_identity(changed))
        for field,value in [('immutable',False),('draft',True),('classification','complete_draft'),('tag_commit_verified',False)]:
            changed=copy.deepcopy(original);changed[field]=value
            with self.assertRaises(ValueError):self.p.audit_identity(changed)

    def test_original_plan_digest_is_checked_before_github_or_any_writer(self):
        fixture=InputSnapshot(self.root/'preview');path=self.root/'inputs.json'
        path.write_bytes(canonical(dict(schema='wukongim.object_storage_plan_inputs/v1',channel='preview',
                                       current=fixture.descriptor(),retain=[])))
        with self.assertRaises(ValueError):self.p.verify_delivery_inputs(inputs=path,expected_plan_sha256='0'*64,executor_sha=HEAD)

    def test_test_only_composite_and_arbitrary_authority_are_rejected(self):
        path=self.root/'inputs.json'
        for value in ({'schema':'wukongim.object_storage_plan_inputs/v2','channel':'composite','test_only':True},
                      {'production_enabled':True,'verified':True}):
            path.write_bytes(canonical(value))
            with self.assertRaises(ValueError):self.p.verify_delivery_inputs(inputs=path,expected_plan_sha256='0'*64,executor_sha=HEAD)

    def test_fence_rechecks_exact_fresh_anchors_and_local_digests(self):
        old=dict(executor_sha=HEAD,inputs_sha256='1'*64,plan_sha256='2'*64,
                 audits=[{'release_id':3}],sources=[{'source_sha':'3'*40}],production_enabled=False)
        self.p.require_same_anchors(old,copy.deepcopy(old))
        for field,value in [('executor_sha','e'*40),('inputs_sha256','e'*64),('plan_sha256','f'*64),
                            ('audits',[{'release_id':4}]),('sources',[{'source_sha':'f'*40}])]:
            changed=copy.deepcopy(old);changed[field]=value
            with self.assertRaises(ValueError):self.p.require_same_anchors(old,changed)

    def test_library_has_no_production_entrypoint_or_history_execution(self):
        text=(ROOT/'scripts/verify-object-storage-delivery-inputs.py').read_text()
        self.assertNotIn('if __name__',text)
        self.assertNotIn('production_enabled\': True',text)
        self.assertIn('verify_seal_inputs',text)
        self.assertIn('resolve_audit_release',text)
        self.assertIn("'--channel', 'preview'",text)


class ProviderWorkflowContract(unittest.TestCase):
    def test_sdk_dependencies_and_ci_are_mandatory_without_cloud_permissions(self):
        lock=ROOT/'requirements-object-storage-provider.lock';self.assertTrue(lock.is_file())
        rows=lock.read_text();self.assertIn('alibabacloud-oss-v2==1.4.1',rows)
        self.assertIn('aliyun-python-sdk-core==2.16.1',rows)
        for chunk in rows.split('\n\n'):
            if chunk.strip() and not chunk.lstrip().startswith('#'):
                self.assertRegex(chunk,r'^[a-zA-Z0-9_-]+==[0-9][^\s]*');self.assertRegex(chunk,r'--hash=sha256:[0-9a-f]{64}')
        path=ROOT/'.github/workflows/object-storage-provider-contract.yml';self.assertTrue(path.is_file())
        text=path.read_text();self.assertIn('pull_request:',text);self.assertIn('push:',text)
        self.assertIn('contents: read',text);self.assertIn('persist-credentials: false',text)
        self.assertIn('--require-hashes',text);self.assertIn('tests/provider_sdk/test_aliyun_object_storage_provider.py',text)
        self.assertIn('timeout-minutes:',text)
        for forbidden in ('workflow_dispatch','id-token:','secrets.','environment:','continue-on-error:'):
            self.assertNotIn(forbidden,text)
        self.assertFalse((ROOT/'tests/provider_sdk/__init__.py').exists())
        runner=(ROOT/'tests/provider_sdk/test_aliyun_object_storage_provider.py').read_text()
        self.assertIn('result.testsRun>0',runner);self.assertIn('not result.skipped',runner)




class FreshPreviewWire(unittest.TestCase):
    """Real planner/resolvers over HTTP-shaped fixtures; subprocess is the native-tool boundary."""
    def setUp(self):
        import base64
        import io
        import shutil
        import urllib.request
        from tests.test_plan_object_storage_publication import C
        self.p=load();self.temp=tempfile.TemporaryDirectory();self.addCleanup(self.temp.cleanup)
        self.root=Path(self.temp.name).resolve();self.f=InputSnapshot(self.root/'preview',audit_id=11)
        names=self.p.S.SOURCE.expected_asset_names(self.f.version)
        self.assets={name:(b'deb-v1' if name.endswith('.deb') else b'unsigned-deb-v1' if name.endswith('.rpm') else name.encode()) for name in names[0]}
        self.assets[names[1]]=(''.join(hashlib.sha256(raw).hexdigest()+'  '+name+'\n' for name,raw in sorted(self.assets.items()))).encode()
        summary_path=self.f.f.source_attestations/'source-attestations.json'
        summary=json.loads(summary_path.read_text())
        for asset in summary['assets']:asset['asset_sha256']=hashlib.sha256(self.assets[asset['asset']]).hexdigest()
        summary_path.write_bytes(canonical(summary));shutil.rmtree(self.f.f.output);C.compose(self.f.f.args());self.f.seal()
        self.inputs=self.root/'inputs.json';self.inputs.write_bytes(canonical(dict(schema='wukongim.object_storage_plan_inputs/v1',channel='preview',current=self.f.descriptor(),retain=[])))
        self.plan=self.p.D.P.make_plan(self.inputs);self.plan_sha=hashlib.sha256(canonical(self.plan)).hexdigest()
        self.main=HEAD;self.bad_ancestry=False;self.bad_helper=False;self.dirty_checkout=False;self.asset_id_offset=0;self.events=[]
        self.blobs={};self.entries=[]
        for name in self.f.descriptor()['control_files_sha256']:
            raw=(self.f.control/name).read_bytes();sha=hashlib.sha1(b'blob '+str(len(raw)).encode()+b'\0'+raw).hexdigest()
            self.blobs[sha]={'sha':sha,'size':len(raw),'encoding':'base64','content':base64.b64encode(raw).decode()}
            self.entries.append(dict(path=name,mode='100644',type='blob',sha=sha,size=len(raw)))
        self.downloads={1000+i:raw for i,(name,raw) in enumerate(sorted(self.assets.items()),1)}
        self.source_assets=[dict(id=1000+i,name=name,size=len(raw),digest='sha256:'+hashlib.sha256(raw).hexdigest()) for i,(name,raw) in enumerate(sorted(self.assets.items()),1)]
        self.audit_assets=[]
        for i,path in enumerate((self.f.archive,self.f.receipt),6001):
            raw=path.read_bytes();self.downloads[i]=raw;self.audit_assets.append(dict(id=i,name=path.name,size=len(raw),state='uploaded',digest='sha256:'+hashlib.sha256(raw).hexdigest()))
        def open_http(request,timeout):
            from urllib.parse import urlsplit
            self.assertEqual(urlsplit(request.full_url).netloc,'api.github.com')
            path=urlsplit(request.full_url).path;self.events.append(path)
            packages='/repos/WuKongIM/packages';source='/repos/WuKongIM/WuKongIM';history=self.f.descriptor()['control_sha'];sha=self.f.descriptor()['source']['source_sha']
            if '/releases/assets/' in path:
                number=int(path.rsplit('/',1)[1]);return io.BytesIO(self.downloads[number-self.asset_id_offset] if number>6000 else self.downloads[number])
            if path==packages+'/git/ref/heads/main':value={'ref':'refs/heads/main','object':{'type':'commit','sha':self.main}}
            elif path==packages+'/git/commits/'+history:value={'sha':history,'tree':{'sha':TREE}}
            elif path==packages+'/git/trees/'+TREE:value={'sha':TREE,'truncated':False,'tree':self.entries}
            elif path.startswith(packages+'/git/blobs/'):value=self.blobs[path.rsplit('/',1)[1]]
            elif path.startswith(packages+'/compare/'):
                value={'status':'diverged' if self.bad_ancestry else 'ahead','base_commit':{'sha':history},'merge_base_commit':{'sha':history}}
            elif path==packages+'/releases/11':
                assets=copy.deepcopy(self.audit_assets)
                for a in assets:a['id']+=self.asset_id_offset
                value=dict(id=11,tag_name='native-package-preview-r11',name='WuKongIM preview package snapshot r11',prerelease=True,
                           target_commitish=history,draft=False,immutable=True,published_at='2026-10-21T00:00:00Z',assets=assets)
            elif path==packages+'/git/ref/tags/native-package-preview-r11':value={'ref':'refs/tags/native-package-preview-r11','object':{'type':'commit','sha':history}}
            elif path==source+'/releases/111':value=dict(id=111,tag_name='v'+self.f.version,prerelease=True,draft=False,immutable=True,published_at='2026-10-21T00:00:00Z',assets=self.source_assets)
            elif path==source+'/git/ref/tags/v'+self.f.version:value={'ref':'refs/tags/v'+self.f.version,'object':{'type':'commit','sha':sha}}
            elif path==source+'/branches/main':value={'commit':{'sha':'2'*40}}
            elif path.startswith(source+'/compare/'):value={'status':'ahead','merge_base_commit':{'sha':sha}}
            else:raise AssertionError('unexpected GitHub URL '+path)
            return io.BytesIO(canonical(value))
        def execute(args,**kwargs):
            from types import SimpleNamespace
            if args[0]=='git':return SimpleNamespace(returncode=0,stdout=(' M scripts/unreviewed.py\n' if self.dirty_checkout else '') if args[1]=='status' else HEAD+'\n')
            self.events.append(Path(args[2]).name)
            self.assertIn('--channel',args);self.assertEqual(args[args.index('--channel')+1],'preview')
            self.assertEqual(kwargs['env']['GH_HOST'],'github.com')
            kwargs['stdout'].write(b'{}')
            return SimpleNamespace(returncode=9 if self.bad_helper else 0)
        for patcher in (mock.patch.object(urllib.request.OpenerDirector,'open',side_effect=open_http),mock.patch.object(self.p.subprocess,'run',side_effect=execute),mock.patch.object(socket.socket,'connect',side_effect=AssertionError('network forbidden'))):
            patcher.start();self.addCleanup(patcher.stop)

    def verify(self,**kwargs):
        return self.p.verify_delivery_inputs(inputs=self.inputs,expected_plan_sha256=self.plan_sha,executor_sha=HEAD,**kwargs)

    def test_v1_uses_seven_downloads_two_native_helpers_and_exact_audit_pair(self):
        result=self.verify();self.assertFalse(result['production_enabled']);self.assertFalse(result['publication_verified'])
        self.assertEqual(result['plan_sha256'],self.plan_sha)
        self.assertEqual(result['original_verification'],self.plan['verification'])
        self.assertEqual(len([x for x in self.events if '/WuKongIM/releases/assets/' in x]),7)
        self.assertIn('verify-source-attestations.py',self.events);self.assertIn('verify-source-package-metadata.py',self.events)
        self.assertEqual(len(result['audits'][0]['assets']),2)
        self.assertEqual(self.verify(previous=result)['audits'],result['audits'])

    def test_helper_failure_main_ancestry_and_audit_id_drift_are_not_verified(self):
        first=self.verify()
        for fault in ('helper','main','ancestry','audit_id','dirty_checkout'):
            with self.subTest(fault=fault):
                self.dirty_checkout=fault=='dirty_checkout';self.bad_helper=fault=='helper';self.main='e'*40 if fault=='main' else HEAD
                self.bad_ancestry=fault=='ancestry';self.asset_id_offset=100 if fault=='audit_id' else 0
                with self.assertRaises(ValueError):self.verify(previous=first)


class NativeSdkWorkflowContract(unittest.TestCase):
    def test_native_sdk_reuses_public_exports_with_bounded_two_phase_cleanup(self):
        text=(ROOT/'.github/workflows/native-package-repository-drill.yml').read_text()
        start=text.index('      - name: Prepare verified native SDK object fixtures')
        end=text.index('      - name: Set up fixed GoReleaser toolchain',start)
        block=text[start:end]
        self.assertLess(text.index('native_object_delivery_fixture_drill.py'),start)
        for required in ('native_object_delivery_sdk_drill.py prepare','native_object_delivery_sdk_drill.py sdk',
                         '--init','--network none','--read-only','--user 65532:65532','--cpus 1 --memory 512m --pids-limit 128',
                         'size=536870912','15m docker run','timeout 240','--require-hashes','timeout 300',
                         '--expected-export-source "$GITHUB_SHA"','--bundle-sha256','--source-sha "$GITHUB_SHA"',
                         'native-delivery-fixture','native-sdk-result.json','if: always()',
                         'if-no-files-found: error','docker rm --force','rm -rf -- "$sdk_work"'):
            self.assertIn(required,block)
        for forbidden in ('export_delivery_generations','secrets.','id-token:','actions: write','continue-on-error:'):
            self.assertNotIn(forbidden,block)
        self.assertIn('timeout-minutes: 60',text)

    def test_native_sdk_cleanup_rejects_docker_observation_failure(self):
        import os
        import subprocess
        text=(ROOT/'.github/workflows/native-package-repository-drill.yml').read_text()
        start=text.index('      - name: Remove owned native SDK fixture work')
        end=text.index('      - name:',start+10)
        block=text[start:end];body=block.split('        run: |\n',1)[1]
        script='\n'.join(line[10:] for line in body.splitlines() if line.startswith('          '))
        with tempfile.TemporaryDirectory() as temp:
            root=Path(temp);binary=root/'bin';binary.mkdir();docker=binary/'docker'
            docker.write_text('#!/bin/sh\nexit 41\n');docker.chmod(0o755)
            env={**os.environ,'PATH':str(binary)+':'+os.environ['PATH'],'RUNNER_TEMP':str(root),
                 'GITHUB_RUN_ID':'fixture','GITHUB_RUN_ATTEMPT':'1'}
            result=subprocess.run(['bash','-c',script],env=env,capture_output=True,timeout=10)
            self.assertNotEqual(result.returncode,0)
            self.assertFalse((root/'native-sdk-evidence/cleanup.json').exists())
            docker.write_text('#!/bin/sh\nexit 0\n')
            result=subprocess.run(['bash','-c',script],env=env,capture_output=True,timeout=10)
            self.assertEqual(result.returncode,0,result.stderr)
            self.assertEqual(json.loads((root/'native-sdk-evidence/cleanup.json').read_text()),
                             {'owned_work_removed':True,'owned_container_removed':True})

    def test_native_sdk_guard_cannot_be_swallowed_by_refusal_or_provider_error(self):
        import sys
        spec=importlib.util.spec_from_file_location('native_sdk_drill',ROOT/'tests/provider_sdk/native_object_delivery_sdk_drill.py')
        driver=importlib.util.module_from_spec(spec);spec.loader.exec_module(driver)
        for mode in ('clean','refused','converted','primary_error'):
            with self.subTest(mode=mode),tempfile.TemporaryDirectory() as temp:
                root=Path(temp);work=root/'native-sdk-work';work.mkdir();bundle=work/'bundle.json';bundle.write_text('{}');output=root/'receipt.json'
                def cases(args,report):
                    if mode in ('refused','primary_error'):
                        driver.refused(lambda:socket.create_connection(('127.0.0.1',9),timeout=1))
                    elif mode=='converted':
                        def wrapped():
                            try:socket.create_connection(('127.0.0.1',9),timeout=1)
                            except Exception:raise ValueError('SDK converted refusal') from None
                        driver.refused(wrapped)
                    if mode=='primary_error':raise KeyError('original stage error')
                    report['cases']=[{'case':name,'passed':True} for name in driver.CASES[:19]]
                argv=['driver','sdk','--source-sha',HEAD,'--bundle',str(bundle),'--bundle-sha256','0'*64,'--output',str(output)]
                with mock.patch.object(sys,'argv',argv),mock.patch.object(driver,'sdk_cases',side_effect=cases):code=driver.main()
                report=json.loads(output.read_bytes());self.assertEqual(code,0 if mode=='clean' else 1)
                self.assertEqual(report['network_guard'],{'self_check_attempts':1,'unexpected_attempts':0 if mode=='clean' else 1,'passed':mode=='clean'})
                if mode=='primary_error':self.assertEqual(report['error_type'],'KeyError')
                self.assertTrue(report['scratch_removed'])

    def test_native_sdk_cleanup_failure_preserves_original_error_and_finite_receipt(self):
        import sys
        spec=importlib.util.spec_from_file_location('native_sdk_drill',ROOT/'tests/provider_sdk/native_object_delivery_sdk_drill.py')
        driver=importlib.util.module_from_spec(spec);spec.loader.exec_module(driver)
        for original,cleanup in ((True,'raise'),(False,'raise'),(True,'noop')):
            with self.subTest(original=original,cleanup=cleanup),tempfile.TemporaryDirectory() as temp:
                root=Path(temp);work=root/'native-sdk-work';work.mkdir();bundle=work/'bundle.json';bundle.write_text('{}');output=root/'receipt.json'
                def cases(args,report):
                    report['object_put_attempts']=7
                    if original:raise KeyError('original stage error')
                    report['cases']=[{'case':name,'passed':True} for name in driver.CASES[:19]]
                argv=['driver','sdk','--source-sha',HEAD,'--bundle',str(bundle),'--bundle-sha256','0'*64,'--output',str(output)]
                outer=None
                with mock.patch.object(sys,'argv',argv),mock.patch.object(driver,'sdk_cases',side_effect=cases),mock.patch.object(driver.shutil,'rmtree',side_effect=PermissionError('cleanup failed') if cleanup=='raise' else None):
                    try:code=driver.main()
                    except Exception as error:outer=type(error).__name__
                self.assertIsNone(outer);self.assertEqual(code,1);self.assertTrue(output.is_file())
                report=json.loads(output.read_bytes());self.assertEqual(report['state'],'incomplete');self.assertFalse(report['passed'])
                self.assertEqual(report['object_put_attempts'],7);self.assertFalse(report['scratch_removed'])
                self.assertEqual(report['cleanup']['error_type'],'PermissionError' if cleanup=='raise' else 'CleanupIncomplete')
                if original:self.assertEqual(report['error_type'],'KeyError')
                self.assertLess(output.stat().st_size,8*1024*1024)

    def test_native_sdk_oversize_evidence_still_emits_incomplete_receipt(self):
        import sys
        spec=importlib.util.spec_from_file_location('native_sdk_drill',ROOT/'tests/provider_sdk/native_object_delivery_sdk_drill.py')
        driver=importlib.util.module_from_spec(spec);spec.loader.exec_module(driver)
        with tempfile.TemporaryDirectory() as temp:
            root=Path(temp);work=root/'native-sdk-work';work.mkdir();bundle=work/'bundle.json';bundle.write_text('{}');output=root/'receipt.json'
            def cases(args,report):
                report['excessive_evidence']='x'*(8*1024*1024)
                report['object_put_attempts']=7
                raise KeyError('original stage error')
            argv=['driver','sdk','--source-sha',HEAD,'--bundle',str(bundle),'--bundle-sha256','0'*64,'--output',str(output)]
            outer=None
            with mock.patch.object(sys,'argv',argv),mock.patch.object(driver,'sdk_cases',side_effect=cases):
                try:code=driver.main()
                except Exception as error:outer=type(error).__name__
            self.assertIsNone(outer);self.assertEqual(code,1);report=json.loads(output.read_bytes())
            self.assertFalse(report['passed']);self.assertEqual(report['state'],'incomplete')
            self.assertEqual(report['error_type'],'KeyError');self.assertEqual(report['receipt_error'],'ReceiptLimit')
            self.assertEqual(report['object_put_attempts'],7);self.assertLess(output.stat().st_size,8*1024*1024)

    def test_native_sdk_driver_is_explicit_test_only_and_ci_requires_all_cases(self):
        text=(ROOT/'.github/workflows/native-package-repository-drill.yml').read_text()
        self.assertIn('(.cases | length) == 20',text)
        self.assertIn('.state == "fixture_complete"',text)
        self.assertIn('.stage == "sdk_fixture_complete"',text)
        self.assertIn('.scratch_removed == true',text)
        spec=importlib.util.spec_from_file_location('native_sdk_drill',ROOT/'tests/provider_sdk/native_object_delivery_sdk_drill.py')
        driver=importlib.util.module_from_spec(spec);spec.loader.exec_module(driver)
        self.assertEqual(len(driver.CASES),20)
        self.assertEqual(len(set(driver.CASES)),20)


if __name__=='__main__':unittest.main()
