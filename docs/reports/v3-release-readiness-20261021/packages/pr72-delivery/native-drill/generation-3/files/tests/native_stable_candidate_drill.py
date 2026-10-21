#!/usr/bin/env python3
"""TEST ONLY: compose, sign and verify an unpublished stable offline candidate.

Run in the existing non-root, network-disabled Linux signing image. Tiny native
packages contain only data. Only a bounded public JSON receipt leaves scratch;
ephemeral private keys, packages and candidate archives are never uploaded.
"""
from __future__ import annotations

import argparse
from contextlib import contextmanager
import gzip
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile
import time
import xml.etree.ElementTree as ET


ROOT = Path(__file__).resolve().parents[1]
VERSION = '3.0.0'
APT_RELEASE = 'dists/stable/Release'
RPM_REPOSITORY = 'stable/el/9/x86_64'
CASES = (
    'plan', 'prepare', 'sign-apt', 'sign-rpm', 'compose',
    'verify-after-secret-removal', 'archive-repeat', 'audit-create', 'audit-verify',
    'extracted-verify', 'reject-apt-detached-receipt-integrity', 'reject-apt-cleartext-receipt-integrity',
    'reject-next-key-receipt-integrity', 'reject-wrong-family-key-receipt-integrity',
    'reject-next-key-crypto', 'reject-wrong-family-key-crypto',
    'reject-deb-bytes', 'reject-rpm-bytes', 'reject-source-anchor', 'reject-control-anchor',
    'reject-bootstrap-bytes', 'reject-download-alias', 'reject-apt-index',
    'reject-apt-gzip', 'reject-apt-by-hash', 'reject-rpm-compressed-metadata',
    'reject-rpm-signed-repomd-integrity', 'reject-archive-plan', 'reject-default-verifier',
    'reject-default-audit', 'reject-default-transport',
)

# Match the actual validator layer, not merely a nonzero process exit. Patterns
# are complete diagnostic lines; missing-input, usage and traceback text cannot
# count as a successful negative. Only these public categories enter the receipt.
VERIFY_PREFIX = 'production package-site verification failed: '
AUDIT_PREFIX = 'package audit receipt operation failed: '
TRANSPORT_PREFIX = 'object-storage planning failed: '
REJECTIONS = {
    'reject-apt-detached-receipt-integrity': ('apt-signature-receipt-integrity', VERIFY_PREFIX,
        r'APT release_gpg identity differs from its signing receipt: dists/stable/Release\.gpg'),
    'reject-apt-cleartext-receipt-integrity': ('apt-signature-receipt-integrity', VERIFY_PREFIX,
        r'APT inrelease identity differs from its signing receipt: dists/stable/InRelease'),
    'reject-next-key-receipt-integrity': ('apt-signature-receipt-integrity', VERIFY_PREFIX,
        r'APT release_gpg identity differs from its signing receipt: dists/stable/Release\.gpg'),
    'reject-wrong-family-key-receipt-integrity': ('apt-signature-receipt-integrity', VERIFY_PREFIX,
        r'APT release_gpg identity differs from its signing receipt: dists/stable/Release\.gpg'),
    'reject-next-key-crypto': ('apt-exact-current-signer', VERIFY_PREFIX,
        r'APT Release\.gpg was not signed by the exact current signing subkey'),
    'reject-wrong-family-key-crypto': ('apt-untrusted-signature', VERIFY_PREFIX,
        r'OpenPGP signature is bad, revoked, or untrusted'),
    'reject-deb-bytes': ('apt-index-payload-integrity', VERIFY_PREFIX,
        r'APT Packages index does not exactly contain active payloads'),
    'reject-rpm-bytes': ('rpm-payload-receipt-integrity', VERIFY_PREFIX,
        r'RPM payload identity differs from its signing receipt: stable/el/9/x86_64/Packages/wukongim_3\.0\.0_linux_amd64\.rpm'),
    'reject-source-anchor': ('external-source-asset-anchor', VERIFY_PREFIX,
        r'candidate source asset differs: wukongim_3\.0\.0_linux_amd64\.deb'),
    'reject-control-anchor': ('external-control-plan-anchor', VERIFY_PREFIX,
        r'candidate plan differs from external control/source/producer anchors or first-generation contract'),
    'reject-bootstrap-bytes': ('bootstrap-indexed-integrity', VERIFY_PREFIX,
        r'indexed APT bootstrap package differs from its manifest'),
    'reject-download-alias': ('bootstrap-download-integrity', VERIFY_PREFIX,
        r'direct APT bootstrap download differs from its manifest'),
    'reject-apt-index': ('apt-index-gzip-closure', VERIFY_PREFIX,
        r'APT Packages\.gz differs from Packages'),
    'reject-apt-gzip': ('apt-index-gzip-closure', VERIFY_PREFIX,
        r'APT Packages\.gz differs from Packages'),
    'reject-apt-by-hash': ('apt-by-hash-integrity', VERIFY_PREFIX,
        r'APT by-hash object differs from dists/stable/main/binary-amd64/Packages(?:\.gz)?'),
    'reject-rpm-compressed-metadata': ('rpm-metadata-receipt-integrity', VERIFY_PREFIX,
        r'RPM repodata identity differs from its signing receipt: stable/el/9/x86_64/repodata/[A-Za-z0-9_.-]{1,256}'),
    'reject-rpm-signed-repomd-integrity': ('rpm-repomd-receipt-integrity', VERIFY_PREFIX,
        r'RPM repodata identity differs from its signing receipt: stable/el/9/x86_64/repodata/repomd\.xml'),
    'reject-archive-plan': ('archive-plan-external-anchor', AUDIT_PREFIX,
        r'archived audit/plan\.json identity differs from its reviewed receipt'),
    'reject-default-verifier': ('preview-verifier-control-schema', VERIFY_PREFIX,
        r'preview signing manifest fields must be exactly \[[^\n]{1,2048}\]'),
    'reject-default-audit': ('preview-audit-plan-schema', AUDIT_PREFIX,
        r'publication plan fields must be exactly \[[^\n]{1,2048}\]'),
    'reject-default-transport': ('preview-only-transport-scope', TRANSPORT_PREFIX,
        r'only preview offline transport planning is supported'),
}


def require(value: bool, message: str) -> None:
    if not value:
        raise RuntimeError(message)


def canonical(value: dict) -> bytes:
    return json.dumps(value, sort_keys=True, separators=(',', ':')).encode() + b'\n'


def facts(path: Path) -> dict:
    raw = path.read_bytes()
    return {'sha256': hashlib.sha256(raw).hexdigest(), 'size': len(raw)}


def mutate_json_anchor(raw: bytes, path: tuple, value) -> bytes:
    """Change one existing leaf while keeping all other parsed JSON intact."""
    document = json.loads(raw)
    parent = document
    for component in path[:-1]:
        parent = parent[component]
    require(parent[path[-1]] != value, 'JSON mutation must change an existing anchor')
    parent[path[-1]] = value
    return canonical(document)


def rebind_apt_signature_receipt(raw: bytes, signature: bytes) -> bytes:
    """Pass receipt byte closure to reach the existing real crypto validator."""
    receipt = json.loads(raw)
    artifact = receipt['result']['release_gpg']
    artifact['sha256'] = hashlib.sha256(signature).hexdigest()
    artifact['size'] = len(signature)
    return canonical(receipt)


def load_fixture(name: str, filename: str):
    spec = importlib.util.spec_from_file_location(name, ROOT / 'tests' / filename)
    module = importlib.util.module_from_spec(spec)
    sys.modules[name] = module
    spec.loader.exec_module(module)
    return module


def invoke(evidence: dict, name: str, command: list[str], environment: dict,
           *, reject: bool = False, case: bool = True) -> subprocess.CompletedProcess:
    """Record actual exit and bounded output facts, never child output or secrets."""
    evidence['phase'] = name
    record = {'stage': name, 'program': Path(command[0]).name, 'exit_code': None,
              'expected_rejection': reject, 'timed_out': False}
    evidence['commands'].append(record)
    try:
        result = subprocess.run(command, stdin=subprocess.DEVNULL, capture_output=True,
                                env=environment, timeout=180)
    except subprocess.TimeoutExpired:
        record['timed_out'] = True
        raise RuntimeError('native command exceeded its bounded deadline') from None
    record['exit_code'] = result.returncode
    for field, raw in (('stdout', result.stdout), ('stderr', result.stderr)):
        record[field] = {'size': len(raw), 'sha256': hashlib.sha256(raw).hexdigest()}
    bounded = len(result.stdout) <= 8 * 1024 * 1024 and len(result.stderr) <= 1024 * 1024
    category = None
    if reject:
        expected = REJECTIONS.get(name)
        if expected and bounded and result.returncode == 1 and not result.stdout:
            declared, prefix, reason = expected
            diagnostic = result.stderr.decode('utf-8', errors='replace')
            if re.fullmatch(re.escape(prefix) + reason + r'\n?', diagnostic):
                category = declared
        passed = category is not None
    else:
        passed = result.returncode == 0 and bounded
    record['rejection_category'] = category
    if case:
        evidence['cases'].append({'case': name, 'passed': passed, 'exit_code': result.returncode,
                                  'expected_rejection': reject, 'rejection_category': category})
    require(passed, 'native command outcome differs from its declared case')
    return result


def cli(filename: str, *arguments) -> list[str]:
    return [sys.executable, '-B', str(ROOT / 'scripts' / filename), *map(str, arguments)]


def common(control: Path, source: Path) -> list[str]:
    return ['--channel', 'stable', '--offline-candidate', '--control-root', str(control),
            '--candidate-source-root', str(source), '--test-only']


def fixture_source(scratch: Path) -> Path:
    fixtures = load_fixture('stable_candidate_native_packages', 'native_source_metadata_drill.py')
    build = scratch / 'package-build'
    build.mkdir()
    deb, rpm = fixtures.build_packages(build, VERSION, VERSION)
    source = scratch / 'source'
    assets = source / 'assets'
    assets.mkdir(parents=True)
    records = []
    for extension, path in (('deb', deb), ('rpm', rpm)):
        name = f'wukongim_{VERSION}_linux_amd64.{extension}'
        shutil.copyfile(path, assets / name)
        records.append({'name': name, 'id': None, **facts(assets / name)})
    (source / 'resolution.json').write_bytes(canonical({
        'schema': 'wukongim/test-source-fixture/v1', 'version': VERSION,
        'source_sha': None, 'release_id': None, 'assets': records,
    }))
    return source


def generate_control(scratch: Path, factory, keys) -> tuple[Path, dict]:
    control = scratch / 'control'
    control.mkdir()
    for directory in ('manifests', 'keys'):
        shutil.copytree(ROOT / directory, control / directory)
    roles = json.loads((control / 'manifests/stable-public-keys.json').read_bytes())
    generated = {}
    for family in ('apt', 'rpm'):
        key = factory.generate(f'STABLE-CANDIDATE-{family.upper()}', signing_expiration='90d',
                               add_encryption_subkey=False, algorithm='rsa3072')
        factory._gpg(key.home, ['--pinentry-mode', 'loopback', '--passphrase-fd', '0',
                               '--quick-add-key', key.primary_fingerprint, 'rsa3072', 'sign', '180d'],
                     input_bytes=keys.PASSPHRASE + b'\n')
        _, subkeys = factory._fingerprints(key.home)
        require(len(subkeys) == 2 and subkeys[0] == key.signing_subkey_fingerprint,
                'TEST ONLY key topology differs')
        certificate = control / f'keys/{family}-stable.asc'
        certificate.write_bytes(factory._gpg(key.home, ['--armor', '--export', key.primary_fingerprint]))
        roles[family] = {'public_key': f'keys/{family}-stable.asc',
                         'primary_fingerprint': key.primary_fingerprint,
                         'signing_subkeys': {'current': subkeys[0], 'next': subkeys[1], 'historical': []}}
        generated[family] = (key, subkeys[1])
    (control / 'manifests/stable-public-keys.json').write_bytes(canonical(roles))
    require(json.loads((control / 'manifests/channels.json').read_bytes())['channels']['stable']['enabled'] is False,
            'TEST ONLY scratch unexpectedly enabled stable')
    return control, generated


def sign_family(scratch: Path, family: str, key, successor: str, control: Path,
                unsigned: Path, evidence: dict, env: dict) -> tuple[Path, Path]:
    output = scratch / f'signed-{family}'
    args = cli('sign-package-family.py', '--family', family, '--input-root', unsigned / family,
               '--output-root', output, '--public-cert', control / f'keys/{family}-stable.asc',
               '--primary-fingerprint', key.primary_fingerprint,
               '--signing-subkey-fingerprint', key.signing_subkey_fingerprint,
               '--next-signing-subkey-fingerprint', successor,
               '--secret-subkey-base64-file', key.selected_secret_base64,
               '--passphrase-file', key.passphrase_file)
    if family == 'apt':
        args += ['--apt-release', APT_RELEASE]
    else:
        repository = unsigned / family / RPM_REPOSITORY
        paths = sorted(p.relative_to(repository).as_posix() for p in (repository / 'Packages').glob('*.rpm'))
        require(len(paths) == 2, 'RPM allowlist must cover product and stable bootstrap')
        for name, value in (
            ('new', {'schema': 'wukongim/rpm-package-allowlist/v1',
                     'packages': [{'path': path, **facts(repository / path)} for path in paths]}),
            ('signed', {'schema': 'wukongim/rpm-package-allowlist/v1', 'packages': []}),
            ('active', {'schema': 'wukongim/rpm-active-allowlist/v1', 'paths': paths}),
        ):
            path = scratch / f'{name}-allowlist.json'
            path.write_bytes(canonical(value))
            args += [f'--{name}-rpm-allowlist', str(path)]
        args += ['--rpm-repository', RPM_REPOSITORY]
    result = invoke(evidence, f'sign-{family}', args, env)
    receipt = scratch / f'{family}-receipt.json'
    receipt.write_bytes(result.stdout)
    parsed = json.loads(result.stdout)
    require(parsed['schema'] == 'wukongim/package-family-signing-receipt/v1', 'unexpected signer receipt')
    require(parsed['key']['signing_subkey_fingerprint'] == key.signing_subkey_fingerprint,
            'signer receipt current key differs')
    return output, receipt


@contextmanager
def changed(path: Path, raw: bytes):
    original, mode = path.read_bytes(), path.stat().st_mode & 0o777
    path.chmod(0o600)
    try:
        path.write_bytes(raw)
        yield
    finally:
        path.write_bytes(original)
        path.chmod(mode)


def only(paths) -> Path:
    paths = sorted(paths)
    require(len(paths) == 1, 'native candidate path selection is not exact')
    return paths[0]


def pipeline(scratch: Path, trusted: Path, evidence: dict, env: dict) -> None:
    evidence['phase'] = 'source-fixture'
    source = fixture_source(scratch)
    evidence['phase'] = 'key-fixture-import'
    keys = load_fixture('stable_candidate_test_keys', 'test_validate_signing_material.py')
    private = scratch / 'private-test-keys'
    factory = keys.TestKeyFactory(private)
    plan, inventory, bootstrap = (scratch / name for name in ('plan.json', 'inventory.json', 'bootstrap.json'))
    unsigned, composed = scratch / 'unsigned', scratch / 'composed'
    bad_signatures = {}
    try:
        evidence['phase'] = 'test-key-generation'
        control, generated = generate_control(scratch, factory, keys)
        shared = common(control, source)
        channels = control / 'manifests/channels.json'
        result = invoke(evidence, 'plan', cli('plan-package-publication.py', '--channels', channels, *shared), env)
        plan.write_bytes(result.stdout)
        value = json.loads(result.stdout)
        require(value['schema'] == 'wukongim.native_package_publication_plan/v2' and value['control_sha'] is None,
                'TEST ONLY candidate plan identity differs')
        invoke(evidence, 'prepare', cli('prepare-package-site.py', '--channels', channels, '--plan', plan,
               '--source-assets', source / 'assets', '--bootstrap-manifest', control / 'manifests/bootstrap-packages.json',
               '--apt-public-cert', control / 'keys/apt-stable.asc', '--rpm-public-cert', control / 'keys/rpm-stable.asc',
               '--bootstrap-builder', ROOT / 'scripts/build-repository-bootstrap-packages.py',
               '--builder', trusted / 'scripts/build-native-package-repositories.sh', '--output', unsigned,
               '--inventory', inventory, '--bootstrap-inventory', bootstrap, *shared), env)
        require(json.loads(inventory.read_bytes())['schema'] == 'wukongim.native_package_payload_inventory/v2'
                and json.loads(bootstrap.read_bytes())['schema'] == 'wukongim.native_package_bootstrap_inventory/v2',
                'candidate preparation schemas differ')
        signed, receipts = {}, {}
        for family in ('apt', 'rpm'):
            key, successor = generated[family]
            signed[family], receipts[family] = sign_family(scratch, family, key, successor, control,
                                                          unsigned, evidence, env)
        # Manufacture actual wrong-issuer signatures while TEST ONLY secrets exist.
        for name, family, use_next in (('next', 'apt', True), ('wrong-family', 'rpm', False)):
            key, successor = generated[family]
            fingerprint = successor if use_next else key.signing_subkey_fingerprint
            path = scratch / f'{name}.sig'
            factory._gpg(key.home, ['--pinentry-mode', 'loopback', '--passphrase-fd', '0',
                                   '--local-user', fingerprint + '!', '--digest-algo', 'SHA256',
                                   '--output', str(path), '--detach-sign', str(signed['apt'] / APT_RELEASE)],
                         input_bytes=keys.PASSPHRASE + b'\n')
            bad_signatures[name] = path
        invoke(evidence, 'compose', cli('compose-package-site.py', '--channels', channels,
               '--signing', control / 'manifests/stable-public-keys.json',
               '--signing-toolchain', control / 'manifests/signing-toolchain.json', '--plan', plan,
               '--inventory', inventory, '--bootstrap-inventory', bootstrap,
               '--apt-tree', signed['apt'], '--apt-receipt', receipts['apt'],
               '--apt-public-cert', control / 'keys/apt-stable.asc',
               '--rpm-tree', signed['rpm'], '--rpm-receipt', receipts['rpm'],
               '--rpm-public-cert', control / 'keys/rpm-stable.asc', '--output-root', composed, *shared), env)
    finally:
        factory.close()
        if private.exists():
            shutil.rmtree(private)
    evidence['secret_tree_removed_before_verification'] = not private.exists()
    require(evidence['secret_tree_removed_before_verification'], 'TEST ONLY secret cleanup failed')
    site, snapshot = composed / 'site', composed / 'audit/snapshot.json'
    require(json.loads(snapshot.read_bytes())['schema'] == 'wukongim.native_package_snapshot/v4',
            'candidate snapshot schema differs')
    anchors = ['--channels', str(channels), '--signing', str(control / 'manifests/stable-public-keys.json'),
               '--apt-public-cert', str(control / 'keys/apt-stable.asc'),
               '--rpm-public-cert', str(control / 'keys/rpm-stable.asc')]

    def verifier(root: Path, candidate: bool = True) -> list[str]:
        return cli('verify-production-package-site.py', '--site-root', root / 'site',
                   '--snapshot', root / 'audit/snapshot.json', *anchors,
                   *(['--plan', str(plan), *shared] if candidate else []))

    result = invoke(evidence, 'verify-after-secret-removal', verifier(composed), env)
    require(json.loads(result.stdout)['schema'] == 'wukongim/production-package-site-verification/v2',
            'candidate verification schema differs')
    archive_name = f'wukongim-stable-{VERSION}-{facts(snapshot)["sha256"]}-candidate-site.tar'
    archive = scratch / archive_name
    invoke(evidence, 'archive-create', cli('archive-package-snapshot.py', 'create', '--source-dir', composed,
                                          '--archive', archive), env, case=False)
    repeat_dir = scratch / 'archive-repeat'; repeat_dir.mkdir()
    repeated = repeat_dir / archive_name
    invoke(evidence, 'archive-repeat', cli('archive-package-snapshot.py', 'create', '--source-dir', composed,
                                          '--archive', repeated), env)
    require(facts(archive) == facts(repeated), 'canonical archive bytes differ for identical signed inputs')
    receipt = scratch / archive_name.replace('-site.tar', '-receipt.json')

    def audit(mode: str, path: Path, candidate: bool = True) -> list[str]:
        return cli('package-audit-receipt.py', mode, *anchors,
                   '--signing-toolchain', control / 'manifests/signing-toolchain.json', '--plan', plan,
                   '--apt-receipt', receipts['apt'], '--rpm-receipt', receipts['rpm'],
                   '--archive', path, *(shared if candidate else []))

    invoke(evidence, 'audit-create', [*audit('create', archive), '--output', str(receipt)], env)
    require(json.loads(receipt.read_bytes())['schema'] == 'wukongim/package-audit-receipt/v3',
            'candidate audit schema differs')
    invoke(evidence, 'audit-verify', [*audit('verify', archive), '--receipt', str(receipt)], env)
    extracted = scratch / 'extracted'
    invoke(evidence, 'archive-inspect', cli('archive-package-snapshot.py', 'inspect', '--archive', archive), env, case=False)
    invoke(evidence, 'archive-extract', cli('archive-package-snapshot.py', 'extract', '--archive', archive,
                                          '--output-dir', extracted), env, case=False)
    invoke(evidence, 'extracted-verify', verifier(extracted), env)

    release = site / 'apt' / APT_RELEASE
    repodata = site / 'rpm' / RPM_REPOSITORY / 'repodata'
    repomd = repodata / 'repomd.xml'
    namespace = '{http://linux.duke.edu/metadata/repo}'
    xml = ET.fromstring(repomd.read_bytes())
    entries = xml.findall(f'{namespace}data')
    require(len(entries) == 3, 'native RPM metadata inventory differs')
    metadata = repodata.parent / entries[0].find(f'{namespace}location').attrib['href']
    by_hash = sorted(p for p in (site / 'apt').rglob('*') if p.is_file() and 'by-hash' in p.parts)
    require(bool(by_hash), 'candidate lacks APT by-hash objects')
    targets = {
        'reject-apt-detached-receipt-integrity': release.with_name('Release.gpg'),
        'reject-apt-cleartext-receipt-integrity': release.with_name('InRelease'),
        'reject-deb-bytes': only((site / 'apt').rglob('wukongim_*.deb')),
        'reject-rpm-bytes': only((site / 'rpm').rglob('wukongim_*.rpm')),
        'reject-bootstrap-bytes': only((site / 'apt').rglob('wukongim-archive-keyring-stable_*.deb')),
        'reject-download-alias': only((site / 'bootstrap').glob('*.deb')),
        'reject-apt-index': release.parent / 'main/binary-amd64/Packages',
        'reject-apt-by-hash': by_hash[0],
        'reject-rpm-compressed-metadata': metadata,
    }
    for name, path in targets.items():
        with changed(path, path.read_bytes() + b'\nTEST ONLY mutation\n'):
            invoke(evidence, name, verifier(composed), env, reject=True)
    # Keep the gzip structurally valid so this tests decompressed byte closure.
    compressed = release.parent / 'main/binary-amd64/Packages.gz'
    with changed(compressed, gzip.compress(gzip.decompress(compressed.read_bytes()) + b'\n', mtime=0)):
        invoke(evidence, 'reject-apt-gzip', verifier(composed), env, reject=True)
    resolution = source / 'resolution.json'
    current_digest = json.loads(resolution.read_bytes())['assets'][0]['sha256']
    wrong_digest = ('1' if current_digest[0] == '0' else '0') + current_digest[1:]
    with changed(resolution, mutate_json_anchor(resolution.read_bytes(), ('assets', 0, 'sha256'), wrong_digest)):
        invoke(evidence, 'reject-source-anchor', verifier(composed), env, reject=True)
    control_bootstrap = control / 'manifests/bootstrap-packages.json'
    with changed(control_bootstrap, mutate_json_anchor(control_bootstrap.read_bytes(), ('version',), '1.1.1')):
        invoke(evidence, 'reject-control-anchor', verifier(composed), env, reject=True)
    apt_receipt = composed / 'audit/apt-signing.json'
    for name, path in bad_signatures.items():
        # First retain the complete candidate's original receipt-integrity refusal.
        with changed(release.with_name('Release.gpg'), path.read_bytes()):
            invoke(evidence, f'reject-{name}-key-receipt-integrity', verifier(composed), env, reject=True)
            # Rebind only archived signature bytes, keeping reviewed current key
            # and external original family receipt intact. This reaches the
            # existing full verifier's actual OpenPGP/current-signer checks.
            with changed(apt_receipt, rebind_apt_signature_receipt(apt_receipt.read_bytes(), path.read_bytes())):
                invoke(evidence, f'reject-{name}-key-crypto', verifier(composed), env, reject=True)
    entries[0].find(f'{namespace}open-checksum').text = '0' * 64
    with changed(repomd, ET.tostring(xml) + b'\n'):
        # The signing receipt catches these changed XML bytes first. This is
        # deliberately not claimed as native open-checksum semantic coverage.
        invoke(evidence, 'reject-rpm-signed-repomd-integrity', verifier(composed), env, reject=True)
    embedded = composed / 'audit/plan.json'
    with changed(embedded, mutate_json_anchor(embedded.read_bytes(), ('target_version',), '3.0.1')):
        bad_dir = scratch / 'bad-archive'; bad_dir.mkdir()
        bad_archive = bad_dir / archive_name
        invoke(evidence, 'archive-mutated-plan', cli('archive-package-snapshot.py', 'create',
               '--source-dir', composed, '--archive', bad_archive), env, case=False)
        invoke(evidence, 'reject-archive-plan', [*audit('create', bad_archive), '--output',
               str(bad_dir / receipt.name)], env, reject=True)
    invoke(evidence, 'reject-default-verifier', verifier(composed, False), env, reject=True)
    invoke(evidence, 'reject-default-audit', [*audit('verify', archive, False), '--receipt', str(receipt)], env, reject=True)
    descriptor = scratch / 'transport-inputs.json'
    transport_controls = ('manifests/channels.json', 'manifests/preview-signing.json',
                          'manifests/signing-toolchain.json', 'manifests/bootstrap-packages.json',
                          'keys/apt-preview.asc', 'keys/rpm-preview.asc')
    descriptor.write_bytes(canonical({'schema': 'wukongim.object_storage_plan_inputs/v1', 'channel': 'stable',
        'current': {'archive': str(archive), 'receipt': str(receipt), 'control_dir': str(control),
                    'audit_release_id': None, 'control_sha': None, 'archive_sha256': facts(archive)['sha256'],
                    'archive_size': facts(archive)['size'], 'receipt_sha256': facts(receipt)['sha256'],
                    'snapshot_sha256': facts(snapshot)['sha256'],
                    'source': {'repository': 'WuKongIM/WuKongIM', 'version': VERSION,
                               'release_id': None, 'source_sha': None},
                    'control_files_sha256': {name: facts(control / name)['sha256'] for name in transport_controls}},
        'retain': []}))
    invoke(evidence, 'reject-default-transport', cli('plan-object-storage-publication.py', '--inputs', descriptor,
           '--output', scratch / 'forbidden-transport.json'), env, reject=True)
    require({case['case'] for case in evidence['cases']} == set(CASES) and len(evidence['cases']) == len(CASES),
            'native case inventory differs from its predeclared set')
    evidence['public_artifacts'] = {name: facts(path) for name, path in
                                    [('plan', plan), ('snapshot', snapshot), ('archive', archive), ('receipt', receipt)]}


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output-dir', required=True, type=Path)
    parser.add_argument('--source-sha', required=True)
    parser.add_argument('--trusted-root', required=True, type=Path)
    args = parser.parse_args(argv)
    if not re.fullmatch('[0-9a-f]{40}', args.source_sha):
        parser.error('--source-sha must be a full lowercase commit')
    output = args.output_dir / 'native-stable-candidate.json'
    # Reserve before all work; a first result cannot be overwritten by a retry.
    with output.open('x') as stream:
        evidence = {'schema': 'wukongim/test-only-native-stable-candidate/v1', 'source_sha': args.source_sha,
                    'test_only': True, 'passed': False, 'scratch_removed': False,
                    'secret_tree_removed_before_verification': False, 'production_keys_used': False,
                    'payloads_executed': False, 'published': False, 'source_provenance_verified': False,
                    'checked_in_control_unchanged': False,
                    'phase': 'preflight', 'cases': [], 'commands': [], 'failure': None,
                    'started_at_unix': int(time.time())}
        scratch = None
        old_environment = dict(os.environ)
        try:
            # Leave room for all GnuPG Unix-domain socket suffixes under the
            # fixture factory's nested homes on both Linux and macOS.
            with tempfile.TemporaryDirectory(prefix='wk-sc-', dir='/tmp') as tmp:
                scratch = Path(tmp).resolve()
                require(sys.platform.startswith('linux') and os.geteuid() != 0,
                        'native candidate requires the isolated non-root Linux container')
                for tool in ('gpg', 'gpgconf', 'rpm', 'rpmbuild', 'rpmsign', 'rpmkeys', 'createrepo_c',
                             'dpkg-deb', 'dpkg-scanpackages', 'apt-ftparchive'):
                    require(shutil.which(tool) is not None, 'required native tool unavailable')
                env = {'PATH': '/usr/bin:/bin', 'HOME': str(scratch), 'LANG': 'C', 'LC_ALL': 'C', 'TZ': 'UTC'}
                os.environ.clear(); os.environ.update(env)
                controls = [path for name in ('manifests', 'keys') for path in (ROOT / name).rglob('*') if path.is_file()]
                before = {str(path.relative_to(ROOT)): facts(path) for path in controls}
                evidence['input_control_files'] = before
                trusted = args.trusted_root.resolve(strict=True)
                manifest = json.loads((ROOT / 'manifests/trusted-toolchain.json').read_bytes())
                builder = 'scripts/build-native-package-repositories.sh'
                require(facts(trusted / builder)['sha256'] == manifest['files'][builder], 'trusted builder bytes differ')
                pipeline(scratch, trusted, evidence, env)
                require(before == {str(path.relative_to(ROOT)): facts(path) for path in controls},
                        'checked-in control changed during native drill')
                evidence['checked_in_control_unchanged'] = True
                evidence['passed'] = all(case['passed'] for case in evidence['cases'])
        except Exception as error:
            # Child output/private GPG diagnostics never enter the public receipt.
            evidence['failure'] = {'type': type(error).__name__,
                                   'stage': evidence['phase']}
        finally:
            os.environ.clear(); os.environ.update(old_environment)
            evidence['scratch_removed'] = scratch is not None and not scratch.exists()
            evidence['passed'] = evidence['passed'] and evidence['scratch_removed']
            evidence['finished_at_unix'] = int(time.time())
            raw = json.dumps(evidence, sort_keys=True, indent=2) + '\n'
            require(len(raw.encode()) <= 1024 * 1024, 'public native receipt exceeds one MiB')
            stream.write(raw)
        print(json.dumps({'passed': evidence['passed'], 'cases': len(evidence['cases']), 'failure': evidence['failure']}))
        return 0 if evidence['passed'] else 1


if __name__ == '__main__':
    raise SystemExit(main())
