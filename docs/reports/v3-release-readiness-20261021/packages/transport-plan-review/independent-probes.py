"""Focused independent probes; synthetic TEST ONLY inputs, no network or keys."""
from pathlib import Path
import contextlib
import gzip
import hashlib
import importlib.util
import io
import json
import subprocess
import sys
import tempfile

ROOT = Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/packages-object-storage-plan')
sys.path.insert(0, str(ROOT))
from tests.test_plan_object_storage_publication import InputSnapshot, canonical

SCRIPT = ROOT / 'scripts/plan-object-storage-publication.py'
spec = importlib.util.spec_from_file_location('reviewed_transport_planner', SCRIPT)
M = importlib.util.module_from_spec(spec)
spec.loader.exec_module(M)
RESULTS = []

def inputs_for(snapshot, retain=()):
    return {'schema': M.INPUT_SCHEMA, 'channel': 'preview',
            'current': snapshot.descriptor(), 'retain': [x.descriptor() for x in retain]}

def run(name, root, value, token=None):
    inputs = root / (name + '-inputs.json')
    output = root / (name + '-output.json')
    inputs.write_bytes(canonical(value))
    p = subprocess.run([sys.executable, str(SCRIPT), '--inputs', str(inputs), '--output', str(output)],
                       capture_output=True, text=True, timeout=30)
    ok = p.returncode == (1 if token else 0)
    if token:
        ok = ok and token in p.stderr and not output.exists() and not p.stdout
    result = {'case': name, 'returncode': p.returncode, 'stdout': p.stdout, 'stderr': p.stderr,
              'output_exists': output.exists(), 'passed_expected_boundary': ok}
    if output.exists():
        raw = output.read_bytes()
        result['output_sha256'] = hashlib.sha256(raw).hexdigest()
    RESULTS.append(result)
    assert ok, result
    return json.loads(output.read_bytes()) if output.exists() else None

with tempfile.TemporaryDirectory(prefix='wk-transport-independent-') as temp:
    root = Path(temp)
    current = InputSnapshot(root / 'current')
    plan = run('real-cli-baseline', root, inputs_for(current))
    assert plan['plan_only'] is True and plan['verification']['cloud_verified'] is False
    assert plan['verification']['signatures_verified'] is False
    assert plan['retention']['delete_objects'] == []

    old = InputSnapshot(root / 'retained', 8, '3.1.0-beta.8', b'old-deb')
    old_hash_dir = old.site / 'apt/dists/preview/main/binary-amd64/by-hash/SHA256'
    removed = next(old_hash_dir.iterdir())
    removed.unlink()
    old.seal()
    run('retained-fully-receipted-missing-byhash', root, inputs_for(current, [old]), 'APT by-hash')

    original_anchor = inputs_for(current)
    (current.site / 'index.html').write_bytes(b'<html>coherently changed envelope</html>')
    current.seal()
    run('coherent-rewrite-original-anchor', root, original_anchor, 'archive SHA-256')

    # A small real archive reproduces a prevalidation read beyond the caller's
    # declared archive bound without allocating or reading a multi-GiB fixture.
    declared = inputs_for(current)
    declared['current']['archive_size'] = 1
    input_path = root / 'declared-one-byte.json'
    input_path.write_bytes(canonical(declared))
    observed = []
    original_hash = M.C.hash_file
    def observed_hash(path, label, **kwargs):
        result = original_hash(path, label, **kwargs)
        if label == 'archive':
            observed.append({'actual_bytes_read_for_hash': result['size'], 'maximum_bytes_argument': kwargs.get('maximum_bytes')})
        return result
    M.C.hash_file = observed_hash
    try:
        try:
            M.make_plan(input_path)
            raise AssertionError('mismatched size accepted')
        except M.PlanningError as e:
            error = str(e)
            assert 'archive size differs' in error
    finally:
        M.C.hash_file = original_hash
    RESULTS.append({'case': 'declared-archive-prehash-budget', 'declared_archive_size': 1,
                    'observed_archive_reads': observed, 'stderr': error,
                    'passed_expected_boundary': not observed,
                    'finding': ('The entire archive is hashed without a limit before actual size is compared to the independent declared bound.'
                                if observed else None)})

    compressed = current.site / 'apt/dists/preview/main/binary-amd64/Packages.gz'
    with compressed.open('wb') as raw:
        with gzip.GzipFile(fileobj=raw, mode='wb', mtime=0) as zipped:
            for _ in range(1024):
                zipped.write(b'x' * 65536)
            zipped.write(b'x')
    current.seal()
    run('apt-decompression-limit-receipted', root, inputs_for(current), 'decompressed size limit')

print(json.dumps({'schema': 'independent-transport-review-probes/v1', 'results': RESULTS}, indent=2))
