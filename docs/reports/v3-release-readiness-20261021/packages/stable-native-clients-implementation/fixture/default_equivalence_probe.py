import ast
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import tempfile
from unittest import mock

EVIDENCE = Path(__file__).parent
ROOT = Path(os.environ['FIXTURE_ROOT'])

def load(path):
    spec = importlib.util.spec_from_file_location(path.stem.replace('-', '_'), path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module

def observe(path, deb_version, rpm_version, epoch):
    module = load(path)
    with tempfile.TemporaryDirectory() as tmp:
        root = Path(tmp)
        commands = []
        def command_boundary(command, **kwargs):
            commands.append([str(item).replace(tmp, '<scratch>') for item in command])
            if command[0] == 'dpkg-deb': Path(command[-1]).write_bytes(b'DEB command boundary')
            elif command[0] == 'rpmbuild':
                rpm = root / 'rpmbuild/RPMS/x86_64/fixture.rpm'; rpm.parent.mkdir(parents=True)
                rpm.write_bytes(b'RPM command boundary')
            else: raise AssertionError('unexpected native command')
            return subprocess.CompletedProcess(command, 0, '', '')
        with mock.patch.object(module.subprocess, 'run', side_effect=command_boundary):
            module.build_packages(root, deb_version, rpm_version, epoch=epoch)
        files = {str(path.relative_to(root)): {'sha256': hashlib.sha256(path.read_bytes()).hexdigest(),
                 'mode': path.stat().st_mode & 0o777} for path in root.rglob('*') if path.is_file()}
        return {'commands': commands, 'files': files}

checks = []
for deb_version, rpm_version, epoch in [('3.1.0', '3.1.0', 0), ('3.1.0~rc.1-hotfix', '3.1.0~rc.1_hotfix', 9)]:
    old = observe(EVIDENCE / 'baseline-native_source_metadata_drill.py', deb_version, rpm_version, epoch)
    new = observe(ROOT / 'tests/native_source_metadata_drill.py', deb_version, rpm_version, epoch)
    assert old == new
    checks.append({'deb_version': deb_version, 'rpm_version': rpm_version, 'epoch': epoch,
                   'native_command_and_default_staging_equal': True, 'file_count': len(new['files'])})
for filename, names in [('native_source_metadata_drill.py', ['main', 'fixture_receipt']),
                        ('native_stable_candidate_drill.py', ['pipeline', 'main', 'generate_control', 'sign_family'])]:
    old = ast.parse((EVIDENCE / ('baseline-' + filename)).read_text())
    new = ast.parse((ROOT / 'tests' / filename).read_text())
    for name in names:
        original = next(node for node in old.body if isinstance(node, ast.FunctionDef) and node.name == name)
        current = next(node for node in new.body if isinstance(node, ast.FunctionDef) and node.name == name)
        assert ast.dump(original) == ast.dump(current), name
        checks.append({'file': filename, 'function': name, 'ast_unchanged': True})
print(json.dumps({'passed': True, 'native_commands_executed': False, 'checks': checks}, indent=2))
