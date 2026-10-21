"""Stable audit transport seams; loopback fixtures never assert live provenance."""
from __future__ import annotations
import hashlib
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest import mock

from test_resolve_audit_release import FixtureState, serve

ROOT = Path(__file__).resolve().parents[1]

def load(filename):
    spec = importlib.util.spec_from_file_location(filename.replace('-', '_'), ROOT/'scripts'/filename)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module

R = load('resolve-audit-release.py')

class StableReaderNetworkTest(unittest.TestCase):
    def test_production_override_and_test_remote_fail_before_output_or_request(self):
        for test_only, base in [(False, 'http://127.0.0.1:1'),
                                (False, 'https://example.invalid'),
                                (True, 'https://api.github.com')]:
            with self.subTest(test_only=test_only, base=base), tempfile.TemporaryDirectory() as temp:
                output=Path(temp)/'output'
                with self.assertRaisesRegex(R.ResolutionError, 'origin|loopback|endpoint'):
                    R.resolve_audit_release(release_id=42, expected_control_sha='a'*40,
                        output_dir=output, api_base_url=base, download_base_url=base,
                        token=None, channel='stable', test_only=test_only)
                self.assertFalse(output.exists())

    def test_test_reader_rejects_every_redirect_without_second_request(self):
        state=FixtureState()
        for location in ['https://outside.invalid/secret', 'http://127.0.0.1:1/next']:
            state.redirect_location=location
            with serve(state) as (api, download):
                reader=R.GitHubReader(api, download, 'TEST ONLY', deny_redirects=True)
                with self.assertRaisesRegex(R.ResolutionError, 'redirect'):
                    reader.open_asset(9001)
            self.assertEqual(len(state.requests),1)
            state.requests.clear()

class StableProxyIsolationTest(unittest.TestCase):
    def test_test_clients_do_not_consult_environment_proxies(self):
        B=load('bind-audit-release.py')
        with mock.patch.object(R.urllib.request, 'getproxies', side_effect=AssertionError('proxy environment read')):
            R.GitHubReader('http://127.0.0.1:1','http://127.0.0.1:1','TEST ONLY',deny_redirects=True)
            B.GitHubWriter('http://127.0.0.1:1','TEST ONLY',test_only=True)

class StablePairFixture:
    def __init__(self, root):
        from test_stable_offline_candidate import CandidateFixture, canonical
        self.fixture=CandidateFixture(root)
        self.plan=root/'plan.json'
        self.plan.write_bytes(canonical(self.fixture.plan()))
        self.archive=root/('wukongim-stable-3.0.0-'+ 'c'*64 +'-candidate-site.tar')
        self.archive.write_bytes(b'TEST ONLY opaque reader fixture')
        self.receipt=root/('wukongim-stable-3.0.0-'+ 'c'*64 +'-candidate-receipt.json')
        self.receipt.write_bytes(canonical({'schema':'wukongim/package-audit-receipt/v3',
            'stage':'offline_candidate','channel':'stable','test_only':True,'audit_release_id':None,
            'control_sha':None,'archive':{'name':self.archive.name,'size':self.archive.stat().st_size,
                'sha256':hashlib.sha256(self.archive.read_bytes()).hexdigest(),'snapshot_sha256':'c'*64},
            'inputs':{'audit/plan.json':{'size':self.plan.stat().st_size,
                'sha256':hashlib.sha256(self.plan.read_bytes()).hexdigest()}},
            'verification':{'target_version':'3.0.0'}}))
    def state(self):
        state=FixtureState()
        state.asset_bytes={self.archive.name:self.archive.read_bytes(),self.receipt.name:self.receipt.read_bytes()}
        state.asset_ids={self.archive.name:9001,self.receipt.name:9002}
        state.asset_names=list(state.asset_bytes)
        state.tag_name='native-package-stable-r4242'
        state.name='WuKongIM stable package snapshot r4242'
        state.draft=False; state.immutable=True; state.published_at='2026-10-21T00:00:00Z'
        return state

class StableReadIdentityTest(unittest.TestCase):
    def test_stable_two_assets_keep_candidate_bytes_and_external_identity(self):
        with tempfile.TemporaryDirectory() as temp:
            f=StablePairFixture(Path(temp).resolve()); state=f.state()
            with serve(state) as (api,download):
                result=R.resolve_audit_release(release_id=4242,expected_control_sha='a'*40,
                    output_dir=Path(temp)/'download',api_base_url=api,download_base_url=download,
                    token=None,channel='stable',test_only=True,candidate_plan=f.plan,candidate_receipt=f.receipt)
            self.assertEqual(result['schema'],'wukongim/package-audit-release-resolution/v2')
            self.assertTrue(result['test_only'])
            self.assertIsNone(result['candidate']['control_sha'])
            self.assertEqual(result['candidate']['snapshot_sha256'],'c'*64)
            self.assertEqual((Path(temp)/'download'/f.archive.name).read_bytes(),f.archive.read_bytes())
            self.assertEqual((Path(temp)/'download'/f.receipt.name).read_bytes(),f.receipt.read_bytes())
    def test_malformed_nested_candidate_receipt_is_a_controlled_rejection(self):
        for field in ('inputs','verification'):
            with self.subTest(field=field), tempfile.TemporaryDirectory() as temp:
                f=StablePairFixture(Path(temp).resolve())
                receipt=json.loads(f.receipt.read_bytes());receipt[field]=[]
                f.receipt.write_bytes(R.stable.C.canonical_json(receipt))
                with self.assertRaisesRegex(R.stable.StableInputError,'object'):
                    R.stable.candidate_identity(f.plan,f.receipt,test_only=True)

    def test_stable_receipt_asset_bytes_must_match_external_anchor(self):
        with tempfile.TemporaryDirectory() as temp:
            f=StablePairFixture(Path(temp).resolve()); state=f.state()
            state.asset_bytes[f.receipt.name]+=b' '
            with serve(state) as (api,download),self.assertRaisesRegex(R.ResolutionError,'candidate|anchor'):
                R.resolve_audit_release(release_id=4242,expected_control_sha='a'*40,
                    output_dir=Path(temp)/'download',api_base_url=api,download_base_url=download,
                    token=None,channel='stable',test_only=True,candidate_plan=f.plan,candidate_receipt=f.receipt)
            self.assertFalse((Path(temp)/'download').exists())

class StableWriterTest(unittest.TestCase):
    def test_bind_only_reserves_empty_stable_draft_and_labels_test_evidence(self):
        from stable_audit_fixture import AuditState,serve_audit
        B=load('bind-audit-release.py')
        with tempfile.TemporaryDirectory() as temp:
            f=StablePairFixture(Path(temp).resolve()); state=AuditState()
            with serve_audit(state) as base:
                result=B.bind_audit_release(release_id=4242,expected_previous_control_sha='a'*40,
                    expected_control_sha='a'*40,api_base_url=base,token='TEST ONLY',channel='stable',
                    test_only=True,candidate_plan=f.plan,control_root=f.fixture.control)
            self.assertTrue(state.tag_exists);self.assertTrue(state.draft);self.assertFalse(state.assets)
            self.assertTrue(result['test_only'])
            self.assertFalse(result['source_verified'])
            self.assertEqual(result['schema'],'wukongim/package-audit-release-binding/v2')
    def test_seal_invalid_candidate_cannot_upload_or_publish(self):
        from stable_audit_fixture import AuditState,serve_audit
        S=load('seal-audit-release.py')
        with tempfile.TemporaryDirectory() as temp:
            f=StablePairFixture(Path(temp).resolve());state=AuditState();state.tag_exists=True
            with serve_audit(state) as base,self.assertRaisesRegex(Exception,'archive|candidate|signing|receipt'):
                S.seal_audit_release(release_id=4242,expected_control_sha='a'*40,
                    archive_path=f.archive,receipt_path=f.receipt,api_base_url=base,download_base_url=base,
                    token='TEST ONLY',channel='stable',test_only=True,candidate_plan=f.plan,
                    control_root=f.fixture.control,candidate_source_root=f.fixture.source,
                    apt_receipt=f.root/'absent-apt.json' if hasattr(f,'root') else Path(temp)/'absent-apt.json',
                    rpm_receipt=Path(temp)/'absent-rpm.json')
            self.assertFalse([item for item in state.requests if item[0]!='GET'])
            self.assertFalse(state.assets);self.assertTrue(state.draft)

if __name__ == '__main__': unittest.main()
