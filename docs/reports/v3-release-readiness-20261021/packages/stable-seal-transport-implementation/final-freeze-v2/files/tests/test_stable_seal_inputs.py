"""Public read-only preflight seam: real Git-object bytes, never caller assertions."""
from __future__ import annotations
import base64
import copy
import hashlib
import json
from pathlib import Path
import tempfile
import unittest
from unittest import mock
from test_stable_audit_transport import load
from test_stable_offline_candidate import CandidateFixture, canonical, C

P=load('verify-stable-seal-inputs.py')
HEAD='a'*40
HISTORY='b'*40
TREE='c'*40

class GitObjects:
    def __init__(self, fixture, plan):
        self.calls=[]
        self.main=HEAD
        self.status='ahead'
        self.blobs={}
        self.entries=[]
        for relative in C.CANDIDATE_CONTROL_FILES:
            self.add(relative,(fixture.control/relative).read_bytes())
        for name in C.CANDIDATE_PRODUCER_FILES:
            self.add('scripts/'+name,(Path(C.__file__).parent/name).read_bytes())
        self.truncated=False
    def add(self,path,raw):
        sha=hashlib.sha1(b'blob '+str(len(raw)).encode()+b'\0'+raw).hexdigest()
        self.blobs[sha]={'sha':sha,'size':len(raw),'encoding':'base64','content':base64.b64encode(raw).decode()}
        self.entries.append({'path':path,'mode':'100755' if path.startswith('scripts/') else '100644','type':'blob','sha':sha,'size':len(raw)})
    def api_json(self,path):
        self.calls.append(path)
        prefix='repos/WuKongIM/packages/'
        if path==prefix+'git/ref/heads/main':
            return {'ref':'refs/heads/main','object':{'type':'commit','sha':self.main}}
        if path==prefix+f'compare/{HISTORY}...{HEAD}':
            return {'status':self.status,'base_commit':{'sha':HISTORY},'merge_base_commit':{'sha':HISTORY}}
        if path==prefix+'git/commits/'+HISTORY:
            return {'sha':HISTORY,'tree':{'sha':TREE}}
        if path==prefix+'git/trees/'+TREE+'?recursive=1':
            return {'sha':TREE,'truncated':self.truncated,'tree':copy.deepcopy(self.entries)}
        if path.startswith(prefix+'git/blobs/'):
            return copy.deepcopy(self.blobs[path.rsplit('/',1)[1]])
        raise AssertionError('unexpected API path: '+path)

class StableControlAuthenticationTest(unittest.TestCase):
    def setUp(self):
        self.temp=tempfile.TemporaryDirectory(); self.addCleanup(self.temp.cleanup)
        self.f=CandidateFixture(Path(self.temp.name).resolve())
        self.plan=self.f.plan()
        self.plan['control_sha']=HISTORY
        self.plan['candidate']['test_only']=False
        self.plan['candidate']['source_kind']='stable_preflight'
        self.plan['release']['source_release_id']=12
        self.plan['release']['source_sha']='d'*40
        self.path=self.f.root/'plan.json'
        self.path.write_bytes(canonical(self.plan))
        self.reader=GitObjects(self.f,self.plan)
    def check(self):
        return P.verify_control(reader=self.reader,plan_path=self.path,
            control_root=self.f.control,expected_control_sha=HEAD,test_only=False)
    def test_actual_history_bytes_and_ancestry_are_checked(self):
        result=self.check()
        self.assertEqual(result['candidate_control_sha'],HISTORY)
        self.assertEqual(result['seal_control_sha'],HEAD)
        self.assertEqual(result['control_files_verified'],10)
        self.assertEqual(result['producer_files_verified'],12)
        self.assertGreaterEqual(sum('git/ref/heads/main' in p for p in self.reader.calls),2)
    def test_linked_plan_parent_is_rejected_before_api_reads(self):
        alias=self.f.root/'linked'; alias.symlink_to(self.f.root,target_is_directory=True)
        self.path=alias/'plan.json'
        with self.assertRaisesRegex(P.StableInputError,'parent|linked'):
            self.check()
        self.assertFalse(self.reader.calls)

    def test_coherent_local_rewrite_does_not_change_remote_git_history(self):
        path=self.f.control/'manifests/channels.json'
        path.write_bytes(path.read_bytes()+b' ')
        self.plan['candidate']['control_files']['manifests/channels.json']={'size':path.stat().st_size,'sha256':hashlib.sha256(path.read_bytes()).hexdigest()}
        self.path.write_bytes(canonical(self.plan))
        with self.assertRaisesRegex(P.StableInputError,'blob|historical|bytes'): self.check()
    def test_history_tree_ancestry_and_blob_fail_closed(self):
        changes=[('diverged',lambda: setattr(self.reader,'status','diverged')),
                 ('moved main',lambda: setattr(self.reader,'main','e'*40)),
                 ('truncated',lambda: setattr(self.reader,'truncated',True)),
                 ('symlink',lambda: self.reader.entries[0].update(mode='120000')),
                 ('duplicate',lambda: self.reader.entries.append(copy.deepcopy(self.reader.entries[0]))),
                 ('missing',lambda: self.reader.entries.pop()),
                 ('wrong blob',lambda: next(iter(self.reader.blobs.values())).update(content='QQ=='))]
        for label,change in changes:
            with self.subTest(label=label):
                self.reader=GitObjects(self.f,self.plan); change()
                with self.assertRaises(P.StableInputError): self.check()

class StableSourceAuthenticationTest(unittest.TestCase):
    def test_source_identity_ignores_moving_main_only(self):
        original={'repository':'WuKongIM/WuKongIM','release_id':12,'tag':'v3.0.0',
            'version':'3.0.0','prerelease':False,'source_sha':'d'*40,
            'published_at':'2026-10-21T00:00:00Z','assets':[{'id':1,'name':'a','size':1,'sha256':'0'*64}],
            'checksum_asset':'checksums','checksum_entries':[], 'main_sha':'1'*40}
        fresh=copy.deepcopy(original); fresh['main_sha']='2'*40
        self.assertEqual(P.source_identity(original),P.source_identity(fresh))
        for field,value in [('release_id',13),('source_sha','e'*40),('tag','v3.0.1')]:
            fresh=copy.deepcopy(original); fresh[field]=value
            self.assertNotEqual(P.source_identity(original),P.source_identity(fresh))

    def test_caller_success_json_cannot_replace_actual_attestation_command(self):
        # This tests the public fresh-source preflight seam with a source-service
        # adapter, not a production endpoint override. The CLI has no such flag.
        with tempfile.TemporaryDirectory() as temp:
            source=Path(temp).resolve()/'source'; source.mkdir()
            original={'repository':'WuKongIM/WuKongIM','release_id':12,'tag':'v3.0.0',
                'version':'3.0.0','prerelease':False,'source_sha':'d'*40,
                'published_at':'2026-10-21T00:00:00Z','assets':[],
                'checksum_asset':'checksums','checksum_entries':[]}
            (source/'resolution.json').write_bytes(canonical(original))
            (source/'metadata.json').write_bytes(canonical({'test_only':True}))
            anchors={path.name:{'size':path.stat().st_size,'sha256':hashlib.sha256(path.read_bytes()).hexdigest()}
                     for path in source.iterdir()}
            with mock.patch.object(P.SOURCE,'resolve_source_release',return_value=copy.deepcopy(original)) as resolve, \
                 mock.patch.object(P.subprocess,'run',return_value=mock.Mock(returncode=9)) as execute:
                with self.assertRaisesRegex(P.StableInputError,'actual source verification failed'):
                    P.verify_source(source_root=source,source_token='TEST ONLY',source_files=anchors)
                self.assertEqual(resolve.call_args.kwargs['release_id'],12)
                self.assertEqual(resolve.call_args.kwargs['channel'],'stable')
                self.assertEqual(execute.call_count,1)
                command=execute.call_args.args[0]
                self.assertEqual(Path(command[2]).name,'verify-source-attestations.py')
                self.assertEqual(execute.call_args.kwargs['env']['GH_HOST'],'github.com')
                self.assertNotIn('TEST ONLY',command)

class StableSourceBytesAnchorTest(unittest.TestCase):
    def test_replaced_source_json_cannot_change_verified_plan_identity(self):
        for changed in ('resolution.json','metadata.json'):
            with self.subTest(changed=changed), tempfile.TemporaryDirectory() as temp:
                source=Path(temp).resolve()
                for name in ('resolution.json','metadata.json'):
                    (source/name).write_bytes(canonical({'original':name}))
                anchors={path.name:{'size':path.stat().st_size,'sha256':hashlib.sha256(path.read_bytes()).hexdigest()}
                         for path in source.iterdir()}
                (source/changed).write_bytes(canonical({'replaced':changed}))
                with mock.patch.object(P.SOURCE,'resolve_source_release') as download, \
                     mock.patch.object(P.subprocess,'run') as execute:
                    with self.assertRaisesRegex(P.StableInputError,'source.*bytes|anchor'):
                        P.verify_source(source_root=source,source_token=None,source_files=anchors)
                    download.assert_not_called();execute.assert_not_called()

class StableFinalSourceFenceTest(unittest.TestCase):
    def test_fresh_final_source_changes_are_typed_rejections(self):
        from test_resolve_source_release import FixtureState, serve, RELEASE_ID
        reader_class=P.SOURCE.GitHubReader
        for changed in ('none','published_at','asset_digest','ancestry'):
            with self.subTest(changed=changed), tempfile.TemporaryDirectory() as temp:
                state=FixtureState(tag='v3.0.0',prerelease=False)
                with serve(state) as (api,download):
                    resolution=P.SOURCE.resolve_source_release(release_id=RELEASE_ID,
                        output_dir=Path(temp)/'assets',api_base_url=api,download_base_url=download,
                        token=None,channel='stable')
                    identity=P.source_identity(resolution)
                    if changed=='published_at': state.published_at='2026-10-21T00:00:00Z'
                    if changed=='asset_digest': state.assets[0]['digest']='sha256:'+'0'*64
                    if changed=='ancestry': state.ancestry_valid=False
                    adapter=reader_class(api,download,None)
                    with mock.patch.object(P.SOURCE,'GitHubReader',return_value=adapter) as factory:
                        if changed=='none': P.revalidate_source(identity,None)
                        else:
                            with self.assertRaises(P.StableInputError): P.revalidate_source(identity,None)
                        self.assertEqual(factory.call_args.args[:2],('https://api.github.com','https://api.github.com'))

if __name__=='__main__': unittest.main()
