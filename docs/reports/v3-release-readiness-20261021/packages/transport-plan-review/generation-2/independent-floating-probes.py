"""Independent real-layout classification probes; local TEST ONLY snapshots."""
from pathlib import Path
import gzip
import hashlib
import json
import subprocess
import sys
import tempfile
import xml.etree.ElementTree as ET

ROOT = Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/packages-object-storage-plan')
sys.path.insert(0, str(ROOT))
from tests.test_plan_object_storage_publication import InputSnapshot, canonical

SCRIPT = ROOT / 'scripts/plan-object-storage-publication.py'
PLANNER_BEFORE = hashlib.sha256(SCRIPT.read_bytes()).hexdigest()
PREFIX = 'rpm/preview/el/9/x86_64/repodata/'
RESULTS = []

def sha(raw):
    return hashlib.sha256(raw).hexdigest()

def child(node, name):
    return next(x for x in node if x.tag.rsplit('}', 1)[-1] == name)

def floating(snapshot):
    repodata = snapshot.site / PREFIX
    index = ET.fromstring((repodata / 'repomd.xml').read_bytes())
    for record in index:
        href = child(record, 'location')
        previous = repodata.parent / href.attrib['href']
        # Valid trailing XML comment creates different same-named bytes per audit.
        opened = previous.read_bytes() + f'\n<!-- audit {snapshot.audit_id} -->\n'.encode()
        compressed = gzip.compress(opened, mtime=0)
        name = record.attrib['type'] + '.xml.gz'
        previous.unlink()
        (repodata / name).write_bytes(compressed)
        href.attrib['href'] = 'repodata/' + name
        child(record, 'checksum').text = sha(compressed)
        child(record, 'open-checksum').text = sha(opened)
        child(record, 'size').text = str(len(compressed))
        child(record, 'open-size').text = str(len(opened))
    (repodata / 'repomd.xml').write_bytes(ET.tostring(index))
    snapshot.seal()

def invoke(root, name, current, old, expected_error=None):
    input_path = root / (name + '-inputs.json')
    output = root / (name + '-plan.json')
    input_path.write_bytes(canonical({'schema':'wukongim.object_storage_plan_inputs/v1',
        'channel':'preview','current':current.descriptor(),'retain':[old.descriptor()]}))
    result = subprocess.run([sys.executable, str(SCRIPT), '--inputs', str(input_path),
                             '--output', str(output)], capture_output=True, text=True, timeout=30)
    row = {'case':name,'exit_code':result.returncode,'stdout':result.stdout,'stderr':result.stderr,
           'output_exists':output.exists()}
    if expected_error:
        row['passed_expected_boundary'] = (result.returncode == 1 and expected_error in result.stderr
                                            and not output.exists() and not result.stdout)
    else:
        row['passed_expected_boundary'] = result.returncode == 0
    RESULTS.append(row)
    return json.loads(output.read_bytes()) if output.exists() else None

with tempfile.TemporaryDirectory(prefix='wk-floating-independent-') as temporary:
    root = Path(temporary)
    current = InputSnapshot(root / 'current')
    old = InputSnapshot(root / 'old', 8, '3.1.0-beta.8', b'old-deb')
    floating(current)
    floating(old)
    plan = invoke(root, 'two-generations-ordinary-gzip-names', current, old)
    if plan is not None:
        paths = [PREFIX + name + '.xml.gz' for name in ('primary','filelists','other')]
        entries = {x['public_uri']:x for x in plan['entrypoints']}
        aliases = {x['public_uri']:x for x in plan['immutable_aliases']}
        objects = plan['snapshot_objects']
        checks = []
        for path in paths:
            rows = [x for x in objects if x['source']['member'] == 'site/' + path]
            checks.append(len(rows) == 2 and len({x['sha256'] for x in rows}) == 2)
            checks.append(all(x['cache_control'] == 'no-store' for x in rows))
            checks.append('/' + path not in aliases)
            checks.append(entries['/' + path]['cache_control'] == 'no-store')
            checks.append(entries['/' + path]['object_key'].startswith('snapshots/preview/r10-'))
        RESULTS[0]['classification_checks'] = checks
        RESULTS[0]['passed_expected_boundary'] = all(checks)
        RESULTS[0]['plain_metadata_snapshot_objects'] = 6
        RESULTS[0]['plain_metadata_shared_aliases'] = 0
        RESULTS[0]['plain_metadata_current_entrypoints'] = 3

        repomd = current.site / PREFIX / 'repomd.xml'
        original = repomd.read_bytes()
        index = ET.fromstring(original)
        child(index[0], 'checksum').text = '0' * 64
        repomd.write_bytes(ET.tostring(index))
        current.seal()
        invoke(root, 'ordinary-metadata-checksum-mismatch', current, old, 'repomd identity differs')

        index = ET.fromstring(original)
        child(index[0], 'open-checksum').text = '0' * 64
        repomd.write_bytes(ET.tostring(index))
        current.seal()
        invoke(root, 'ordinary-metadata-open-checksum-mismatch', current, old, 'repomd open identity differs')

        repomd.write_bytes(original)
        current.seal()
        old_repomd = old.site / PREFIX / 'repomd.xml'
        index = ET.fromstring(old_repomd.read_bytes())
        child(index[0], 'location').attrib['href'] = 'repodata/missing.xml.gz'
        old_repomd.write_bytes(ET.tostring(index))
        old.seal()
        invoke(root, 'retained-ordinary-metadata-unreceipted-reference', current, old, 'unreceipted')

print(json.dumps({'schema':'independent-floating-metadata-review/v1',
                  'planner_sha256':sha(SCRIPT.read_bytes()),
                  'planner_unchanged': PLANNER_BEFORE == sha(SCRIPT.read_bytes()),
                  'results':RESULTS},indent=2))
