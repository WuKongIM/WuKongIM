"""Explicit composite inputs cannot upgrade unsealed or TEST ONLY evidence."""
from pathlib import Path
import tempfile
import unittest
from test_stable_audit_transport import load
from test_stable_offline_candidate import canonical
P=load('plan-object-storage-publication.py')

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

if __name__=='__main__': unittest.main()
