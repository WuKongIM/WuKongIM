"""Explicit composite inputs cannot upgrade unsealed or TEST ONLY evidence."""
import contextlib
import hashlib
import json
from pathlib import Path
import shutil
import tempfile
import unittest
from unittest import mock
from test_stable_audit_transport import load
from test_stable_offline_candidate import canonical
P=load('plan-object-storage-publication.py')


class StableTransportFixture:
    """Real archive I/O for alias planning, with native verification isolated below."""

    def __init__(self, root, preview, audit_id):
        from test_stable_audit_transport import StablePairFixture
        root.mkdir()
        self.pair = StablePairFixture(root)
        self.archive = self.pair.archive
        self.tree = root / 'tree'
        shutil.copytree(preview.f.output, self.tree)
        (self.tree / 'site/apt/dists/preview').rename(self.tree / 'site/apt/dists/stable')
        manifest_path = self.tree / 'site/bootstrap/manifest.json'
        manifest = json.loads(manifest_path.read_bytes())
        manifest['schema'] = 'wukongim.native_package_bootstrap_inventory/v2'
        for family, item in manifest['packages'].items():
            stable = P.C.expected_bootstrap_package(family, manifest['version'], channel='stable')
            for field in ('download_path', 'repository_path'):
                target = self.tree / 'site' / stable[field]
                target.parent.mkdir(parents=True, exist_ok=True)
                (self.tree / 'site' / item[field]).rename(target)
            item.update(stable)
        manifest_path.write_bytes(canonical(manifest))
        shutil.copytree(self.tree / 'site/rpm/preview', self.tree / 'site/rpm/stable',
                        dirs_exist_ok=True)
        shutil.rmtree(self.tree / 'site/rpm/preview')
        self.archive.unlink()
        P.R.snapshot_archive.create_snapshot(source_dir=self.tree, archive_path=self.archive)
        receipt = json.loads(self.pair.receipt.read_bytes())
        receipt['archive'].update(size=self.archive.stat().st_size,
                                 sha256=hashlib.sha256(self.archive.read_bytes()).hexdigest())
        self.pair.receipt.write_bytes(canonical(receipt))
        verifier = load('verify-stable-seal-inputs.py')
        candidate = verifier.candidate_identity(self.pair.plan, self.pair.receipt, test_only=True)
        resolution = {
            'schema': 'wukongim/package-audit-release-resolution/v2',
            'repository': 'WuKongIM/packages', 'release_id': audit_id,
            'tag_name': f'native-package-stable-r{audit_id}',
            'name': f'WuKongIM stable package snapshot r{audit_id}',
            'prerelease': True, 'control_sha': 'a' * 40, 'tag_commit_verified': True,
            'classification': 'immutable_complete', 'draft': False, 'immutable': True,
            'published_at': 'TEST ONLY local observation', 'asset_count': 2,
            'total_size': sum(candidate[key]['size'] for key in ('archive', 'receipt')),
            'assets': [dict(candidate[key], id=9000 + i, downloaded_file=candidate[key]['name'])
                       for i, key in enumerate(('archive', 'receipt'), 1)],
            'release_revalidated': True, 'id_bound_downloads': True, 'channel': 'stable',
            'test_only': True, 'candidate': candidate,
        }
        resolution_path = root / 'resolution.json'
        resolution_path.write_bytes(canonical(resolution))
        self.descriptor = {
            'channel': 'stable', 'audit_release_id': audit_id, 'seal_control_sha': 'a' * 40,
            'candidate': candidate, 'resolution': str(resolution_path),
            'resolution_sha256': hashlib.sha256(resolution_path.read_bytes()).hexdigest(),
            'archive': str(self.archive), 'receipt': str(self.pair.receipt),
            'plan': str(self.pair.plan), 'control_dir': str(self.pair.fixture.control),
            'source_dir': str(self.pair.fixture.source),
            'apt_receipt': str(root / 'apt.json'), 'rpm_receipt': str(root / 'rpm.json'),
        }


class StableByHashTransportTest(unittest.TestCase):
    def setUp(self):
        from test_plan_object_storage_publication import InputSnapshot
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name).resolve()
        self.preview = InputSnapshot(self.root / 'preview')
        self.stable = StableTransportFixture(self.root / 'stable', self.preview, 4242)
        self.inputs = {'schema': 'wukongim.object_storage_plan_inputs/v2', 'channel': 'composite',
                      'test_only': True, 'current': {
                          'preview': {'channel': 'preview', 'snapshot': self.preview.descriptor()},
                          'stable': self.stable.descriptor}, 'retain': []}
        self.path = self.root / 'inputs.json'
        self.verifier = load('verify-stable-seal-inputs.py')

    @contextlib.contextmanager
    def native_verification(self, side_effect=None):
        # Cryptography is covered by the actual native drill, not these structural fixtures.
        with mock.patch.object(self.verifier, 'verify_candidate', side_effect=side_effect) as verify:
            with mock.patch.object(P, '_load', return_value=self.verifier):
                yield verify

    def plan(self):
        self.path.write_bytes(canonical(self.inputs))
        return P.make_plan(self.path, composite=True, test_only=True)

    def test_sha512_aliases_reuse_original_index_members_without_changing_archive(self):
        original = self.stable.archive.read_bytes()
        with self.native_verification() as verify:
            plan = self.plan()
        verify.assert_called_once()
        aliases = {item['public_uri']: item for item in plan['immutable_aliases']}
        for filename in ('Packages', 'Packages.gz'):
            member = 'site/apt/dists/stable/main/binary-amd64/' + filename
            raw = (self.stable.tree / member).read_bytes()
            uri = '/apt/dists/stable/main/binary-amd64/by-hash/SHA512/' + hashlib.sha512(raw).hexdigest()
            self.assertIn(uri, aliases)
            self.assertEqual(aliases[uri], {
                'public_uri': uri, 'object_key': 'public' + uri,
                'sha256': hashlib.sha256(raw).hexdigest(), 'size': len(raw),
                'cache_control': P.IMMUTABLE_CACHE,
                'source': {'audit_release_id': 4242, 'channel': 'stable', 'member': member},
            })
            self.assertTrue(any(item['source']['member'] == member for item in plan['snapshot_objects']))
        self.assertFalse(any('/by-hash/SHA512/' in item['object_key'] for item in plan['snapshot_objects']))
        self.assertEqual(self.stable.archive.read_bytes(), original)

    def test_all_generations_keep_both_hash_algorithms_and_preview_is_unchanged(self):
        from test_plan_object_storage_publication import InputSnapshot
        other = InputSnapshot(self.root / 'other-source', audit_id=11,
                              version='3.1.0-rc.2', deb=b'different payload')
        retained = StableTransportFixture(self.root / 'retained', other, 4243)
        self.inputs['retain'] = [retained.descriptor]
        archives = {f.archive: f.archive.read_bytes() for f in (self.stable, retained, self.preview)}
        with self.native_verification():
            plan = self.plan()
            self.assertEqual(canonical(plan), canonical(self.plan()))
        aliases = {item['public_uri']: item for item in plan['immutable_aliases']}
        for fixture in (self.stable, retained):
            for filename in ('Packages', 'Packages.gz'):
                member = 'site/apt/dists/stable/main/binary-amd64/' + filename
                raw = (fixture.tree / member).read_bytes()
                for algorithm, constructor in (('SHA256', hashlib.sha256), ('SHA512', hashlib.sha512)):
                    uri = '/apt/dists/stable/main/binary-amd64/by-hash/' + algorithm + '/' + constructor(raw).hexdigest()
                    self.assertIn(uri, aliases)
                    self.assertEqual(aliases[uri]['sha256'], hashlib.sha256(raw).hexdigest())
        preview_inputs = self.root / 'preview-inputs.json'
        preview_inputs.write_bytes(canonical({'schema': 'wukongim.object_storage_plan_inputs/v1',
            'channel': 'preview', 'current': self.preview.descriptor(), 'retain': []}))
        legacy = P.make_plan(preview_inputs)
        for item in legacy['immutable_aliases']:
            actual = dict(aliases[item['public_uri']])
            actual['source'] = {k: v for k, v in actual['source'].items() if k != 'channel'}
            self.assertEqual(actual, item)
        self.assertFalse(any('/preview/' in uri and '/SHA512/' in uri for uri in aliases))
        self.assertEqual(plan['retention']['delete_objects'], [])
        for path, raw in archives.items():
            self.assertEqual(path.read_bytes(), raw)

    def test_failed_native_verification_cannot_produce_aliases(self):
        with self.native_verification(side_effect=P.PlanningError('native verification rejected')):
            with self.assertRaisesRegex(P.PlanningError, 'native verification rejected'):
                self.plan()

    def test_index_changed_after_inventory_is_rejected_before_alias_output(self):
        original = P.C.collect_tree
        for replacement in ('same-size', 'longer'):
            changed = []
            def change_index(root, label, **kwargs):
                result = original(root, label, **kwargs)
                if label == 'stable snapshot site':
                    path = root / 'apt/dists/stable/main/binary-amd64/Packages'
                    raw = path.read_bytes()
                    path.write_bytes((b'X' * len(raw)) if replacement == 'same-size' else raw + b'extra')
                    changed.append(path)
                return result
            with self.subTest(replacement=replacement), self.native_verification():
                with mock.patch.object(P.C, 'collect_tree', side_effect=change_index):
                    with self.assertRaisesRegex(P.C.CompositionError, 'identity differs|size differs'):
                        self.plan()
                self.assertEqual(len(changed), 1)

    def test_alias_index_read_obeys_the_existing_metadata_bound(self):
        original = P.C.read_checked
        def lower_bound(path, label, **kwargs):
            if label == 'stable APT transport index':
                self.assertEqual(kwargs['maximum_bytes'], P.C.MAX_METADATA_BYTES)
                kwargs['maximum_bytes'] = 1
            return original(path, label, **kwargs)
        with self.native_verification(), mock.patch.object(P.C, 'read_checked', side_effect=lower_bound):
            with self.assertRaisesRegex(P.C.CompositionError, 'size|limit'):
                self.plan()

    def test_conflicting_derived_uri_is_rejected_instead_of_selecting_a_generation(self):
        # Collision injection exercises the transport conflict gate, not SHA512 strength.
        class Collision:
            def hexdigest(self):
                return '0' * 128
        with self.native_verification(), mock.patch.object(P.hashlib, 'sha512', return_value=Collision()):
            with self.assertRaisesRegex(P.PlanningError, 'immutable URI conflict across snapshots'):
                self.plan()

class CompositePlannerModeTest(unittest.TestCase):
    def test_v2_requires_explicit_composite_mode(self):
        with tempfile.TemporaryDirectory() as temp:
            path=Path(temp).resolve()/'inputs.json'
            path.write_bytes(canonical({'schema':'wukongim.object_storage_plan_inputs/v2',
                'channel':'composite','test_only':True,'current':{'preview':{},'stable':{}},'retain':[]}))
            with self.assertRaisesRegex((P.PlanningError,P.C.CompositionError),'schema|fields'):
                P.make_plan(path)
            with self.assertRaisesRegex(P.PlanningError,'TEST ONLY|test_only'):
                P.make_plan(path,composite=True)
    def test_composite_requires_both_current_channels_before_reading_paths(self):
        with tempfile.TemporaryDirectory() as temp:
            path=Path(temp).resolve()/'inputs.json'
            path.write_bytes(canonical({'schema':'wukongim.object_storage_plan_inputs/v2',
                'channel':'composite','test_only':False,'current':{'stable':{}},'retain':[]}))
            with self.assertRaisesRegex((P.PlanningError,P.C.CompositionError),'preview|fields'):
                P.make_plan(path,composite=True)

class CompositeSealRejectionTest(unittest.TestCase):
    def test_local_immutable_claim_does_not_skip_candidate_archive_verification(self):
        from test_stable_audit_transport import StablePairFixture,R
        from test_resolve_audit_release import serve
        from test_plan_object_storage_publication import InputSnapshot
        import hashlib,json
        with tempfile.TemporaryDirectory() as temp:
            root=Path(temp).resolve();stable_root=root/'stable';stable_root.mkdir()
            f=StablePairFixture(stable_root); state=f.state()
            with serve(state) as (api,download):
                resolution=R.resolve_audit_release(release_id=4242,expected_control_sha='a'*40,
                    output_dir=root/'download',api_base_url=api,download_base_url=download,token=None,
                    channel='stable',test_only=True,candidate_plan=f.plan,candidate_receipt=f.receipt)
            resolution_path=root/'resolution.json';resolution_path.write_bytes(canonical(resolution))
            preview=InputSnapshot(root/'preview')
            descriptor={'channel':'stable','audit_release_id':4242,'seal_control_sha':'a'*40,
                'candidate':resolution['candidate'],'resolution':str(resolution_path),
                'resolution_sha256':hashlib.sha256(resolution_path.read_bytes()).hexdigest(),
                'archive':str(f.archive),'receipt':str(f.receipt),'plan':str(f.plan),
                'control_dir':str(f.fixture.control),'source_dir':str(f.fixture.source),
                'apt_receipt':str(root/'apt.json'),'rpm_receipt':str(root/'rpm.json')}
            path=root/'inputs.json';path.write_bytes(canonical({
                'schema':'wukongim.object_storage_plan_inputs/v2','channel':'composite','test_only':True,
                'current':{'preview':{'channel':'preview','snapshot':preview.descriptor()},'stable':descriptor},'retain':[]}))
            # The output must not alter either control/source tree even if later
            # candidate validation would reject this intentionally malformed tar.
            for folder in (f.fixture.control, f.fixture.source, preview.control):
                with self.subTest(output_root=folder), self.assertRaisesRegex(
                        (P.PlanningError,P.C.CompositionError), 'overlap'):
                    P.make_plan(path,composite=True,test_only=True,output_path=folder/'plan-output.json')
            with self.assertRaisesRegex((P.PlanningError,P.C.CompositionError,ValueError),'archive|candidate|receipt'):
                P.make_plan(path,composite=True,test_only=True)

            # Replacing the file after its bounded read must not alter the JSON
            # whose independently supplied digest was just checked.
            original=resolution_path.read_bytes()
            real_read=P.C.read_checked
            replaced=[]
            def replace_after_read(file,label,**kwargs):
                raw=real_read(file,label,**kwargs)
                if file==resolution_path and not replaced:
                    changed=json.loads(raw);changed['classification']='changed-after-hash'
                    resolution_path.write_bytes(canonical(changed));replaced.append(True)
                return raw
            with mock.patch.object(P.C,'read_checked',side_effect=replace_after_read):
                with self.assertRaisesRegex((P.PlanningError,P.C.CompositionError,ValueError),'archive|candidate|receipt'):
                    P.make_plan(path,composite=True,test_only=True)
            self.assertEqual(replaced,[True])
            resolution_path.write_bytes(original)

            for raw,reason in ((b' '+original,'canonical'),
                (original[:-2]+b',"classification":"immutable_complete"}\n','duplicate')):
                with self.subTest(reason=reason):
                    resolution_path.write_bytes(raw)
                    descriptor['resolution_sha256']=hashlib.sha256(raw).hexdigest()
                    inputs=json.loads(path.read_bytes());inputs['current']['stable']=descriptor
                    path.write_bytes(canonical(inputs))
                    with self.assertRaisesRegex((P.PlanningError,P.C.CompositionError,ValueError),reason):
                        P.make_plan(path,composite=True,test_only=True)

if __name__=='__main__': unittest.main()
