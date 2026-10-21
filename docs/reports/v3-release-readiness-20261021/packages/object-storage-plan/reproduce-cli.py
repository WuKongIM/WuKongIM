#!/usr/bin/env python3
"""Recreate the small TEST ONLY two-generation plain-RPM CLI receipt."""
import argparse
import hashlib
import json
from pathlib import Path
import subprocess
import sys
from tempfile import TemporaryDirectory

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--repo', required=True, type=Path)
parser.add_argument('--output-dir', required=True, type=Path)
args = parser.parse_args()
args.repo = args.repo.resolve()
args.output_dir = args.output_dir.resolve()
args.output_dir.mkdir()
sys.path.insert(0, str(args.repo))
from tests.test_plan_object_storage_publication import InputSnapshot, canonical
script = args.repo / 'scripts/plan-object-storage-publication.py'
with TemporaryDirectory(prefix='wk-planner-cli-fixture-') as temporary:
    fixture = Path(temporary)
    current = InputSnapshot(fixture / 'current', plain_rpm_metadata=True)
    old = InputSnapshot(fixture / 'retained', 8, '3.1.0-beta.8', b'deb-old', plain_rpm_metadata=True)
    inputs = {'schema':'wukongim.object_storage_plan_inputs/v1', 'channel':'preview',
              'current':current.descriptor(), 'retain':[old.descriptor()]}
    input_path = fixture / 'inputs.json'
    input_path.write_bytes(canonical(inputs))
    def hashes():
        return {p.relative_to(fixture).as_posix():hashlib.sha256(p.read_bytes()).hexdigest()
                for p in fixture.rglob('*') if p.is_file()}
    before = hashes()
    results = []
    for number in (1, 2):
        output = args.output_dir / f'plan-{number}.json'
        command = [sys.executable, str(script), '--inputs', str(input_path), '--output', str(output)]
        result = subprocess.run(command, capture_output=True, text=True, check=True)
        assert result.stderr == ''
        results.append(json.loads(result.stdout))
        (args.output_dir / f'receipt-{number}.json').write_text(result.stdout)
    assert before == hashes()
    assert (args.output_dir / 'plan-1.json').read_bytes() == (args.output_dir / 'plan-2.json').read_bytes()
    receipt = {'schema':'wukongim.object_storage_planner_local_acceptance/v1',
               'fixture':'TEST ONLY synthetic certificates/signatures and packages; plain gzip RPM metadata',
               'base_commit':subprocess.check_output(['git','-C',str(args.repo),'rev-parse','HEAD'],text=True).strip(),
               'planner_sha256':hashlib.sha256(script.read_bytes()).hexdigest(),
               'input_files_unchanged':True, 'repeat_bytes_identical':True, 'cli_receipts':results,
               'snapshot_count':2, 'fixture_files_created':len(before),
               'note':'Temporary fixture automatically removed; reproduce with this script against the frozen patch.'}
    (args.output_dir / 'acceptance.json').write_text(json.dumps(receipt,sort_keys=True,indent=2)+'\n')
    print(json.dumps(receipt,sort_keys=True,indent=2))
