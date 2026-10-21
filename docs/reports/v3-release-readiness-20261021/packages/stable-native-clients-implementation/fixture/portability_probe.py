import hashlib
import importlib.util
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
from unittest import mock
root=Path(os.environ['FIXTURE_ROOT'])
spec=importlib.util.spec_from_file_location('existing_candidate_fixture',root/'tests/test_stable_offline_candidate.py')
m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
with tempfile.TemporaryDirectory() as temporary:
    scratch=Path(temporary).resolve()
    f=m.CandidateFixture(scratch/'container-control-root')
    with mock.patch.object(subprocess,'run',side_effect=AssertionError('native command forbidden')):
        original=f.plan()
        moved=scratch/'host-output-root';moved.mkdir()
        shutil.copytree(f.control,moved/'A/control');shutil.copytree(f.source,moved/'A/source')
        validated=m.C.validate_candidate_plan(original,moved/'A/control',moved/'A/source',test_only=True)
        rebuilt=m.C.build_candidate_plan(moved/'A/control',moved/'A/source',None,test_only=True)
    assert m.canonical(original)==m.canonical(rebuilt)==m.canonical(validated)
    def strings(value):
        if isinstance(value,str):yield value
        elif isinstance(value,dict):
            for item in value.values():yield from strings(item)
        elif isinstance(value,list):
            for item in value:yield from strings(item)
    assert not any(value.startswith('/') for value in strings(original))
    result={'passed':True,'native_commands_executed':False,'plan_bytes_unchanged_after_external_root_relocation':True,
        'plan_sha256':hashlib.sha256(m.canonical(original)).hexdigest(),'absolute_string_values':0,
        'control_anchor_count':len(original['candidate']['control_files']),
        'producer_anchor_count':len(original['candidate']['producer_files']),
        'producer_anchor_kind':'relative script name to content size/sha256; local verifier producer bytes must match',
        'trusted_builder_path_persisted':False,
        'limitation':'Pure candidate-plan relocation only. Signed archive/family receipt portability is source-inspected; native A/B host revalidation remains pending.'}
    print(json.dumps(result,indent=2))
