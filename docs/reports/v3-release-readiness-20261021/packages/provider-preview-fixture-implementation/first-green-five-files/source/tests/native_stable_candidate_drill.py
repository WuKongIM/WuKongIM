#!/usr/bin/env python3
"""TEST ONLY: compose, sign and verify an unpublished stable offline candidate.

Run in the existing non-root, network-disabled Linux signing image. The default
drill uses tiny data-only packages and exports only a public JSON receipt. The
explicit client fixture callable exports public A/B packages after key removal.
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


def fixture_source(scratch: Path, *, version: str = VERSION,
                   binary_payloads: dict[str, Path] | None = None) -> Path:
    fixtures = load_fixture('stable_candidate_native_packages', 'native_source_metadata_drill.py')
    build = scratch / 'package-build'
    build.mkdir()
    deb, rpm = fixtures.build_packages(build, version, version, binary_payloads=binary_payloads)
    source = scratch / 'source'
    assets = source / 'assets'
    assets.mkdir(parents=True)
    records = []
    for extension, path in (('deb', deb), ('rpm', rpm)):
        name = f'wukongim_{version}_linux_amd64.{extension}'
        shutil.copyfile(path, assets / name)
        records.append({'name': name, 'id': None, **facts(assets / name)})
    (source / 'resolution.json').write_bytes(canonical({
        'schema': 'wukongim/test-source-fixture/v1', 'version': version,
        'source_sha': None, 'release_id': None, 'assets': records,
    }))
    return source


def export_client_generations(*, output_root: Path, binaries_root: Path,
                              trusted_root: Path, product_source_sha: str) -> dict:
    """Export public TEST ONLY fixtures built from explicit fixed-source binaries."""
    require(product_source_sha == 'fc18221282f289088ec9b4282fda8e0bfc67a3b8',
            'client fixtures require the fixed product source')
    require(output_root.is_dir() and not output_root.is_symlink() and not any(output_root.iterdir()),
            'client fixture output directory must exist, be nonlinked and empty')
    require(binaries_root.is_dir() and not binaries_root.is_symlink(),
            'client fixture binary generation root must be a nonlinked directory')
    fixtures = load_fixture('stable_client_native_packages', 'native_source_metadata_drill.py')
    payloads = {}
    for version in ('3.0.0', '3.0.1'):
        directory = binaries_root / version
        require(directory.is_dir() and not directory.is_symlink(),
                'client fixture binary generation is missing or linked')
        payloads[version] = {name: directory / name for name in ('wukongim', 'wkcli')}
        fixtures.validate_binary_payloads(payloads[version])
    return _export_generations(output_root=output_root, trusted_root=trusted_root,
                               product_source_sha=product_source_sha, payloads=payloads)


def export_delivery_generations(*, output_root: Path, trusted_root: Path, evidence: dict | None = None) -> dict:
    """Export data-only native preview/A/B for TEST ONLY offline delivery checks.

    This separate entrypoint never substitutes for export_client_generations:
    it contains no product binaries and proves neither installed CLI nor source
    attestation truth. Its explicit test_only descriptor is production-refused.
    """
    return _export_generations(output_root=output_root, trusted_root=trusted_root,
                               product_source_sha=None, payloads={}, include_preview=True, evidence=evidence)


def _export_generations(*, output_root: Path, trusted_root: Path,
                        product_source_sha: str | None, payloads: dict,
                        include_preview: bool = False, evidence: dict | None = None) -> dict:
    """Share native construction while keeping client/data-only entrypoints separate."""
    require(output_root.is_dir() and not output_root.is_symlink() and not any(output_root.iterdir()),
            'client fixture output directory must exist, be nonlinked and empty')
    require(sys.platform.startswith('linux') and os.geteuid() != 0,
            'client fixtures require the isolated non-root Linux container')
    for tool in ('gpg', 'gpgconf', 'rpm', 'rpmbuild', 'rpmsign', 'rpmkeys', 'createrepo_c',
                 'dpkg-deb', 'dpkg-scanpackages', 'apt-ftparchive'):
        require(shutil.which(tool) is not None, 'required native client fixture tool unavailable')
    trusted = trusted_root.resolve(strict=True)
    manifest = json.loads((ROOT / 'manifests/trusted-toolchain.json').read_bytes())
    builder = 'scripts/build-native-package-repositories.sh'
    require(facts(trusted / builder)['sha256'] == manifest['files'][builder], 'trusted builder bytes differ')
    controls = [path for directory in ('manifests', 'keys') for path in (ROOT / directory).rglob('*')
                if path.is_file()]
    before = {str(path.relative_to(ROOT)): facts(path) for path in controls}
    previous_environment = dict(os.environ)
    result = {'test_only': True, 'source_sha': product_source_sha,
              'data_only': include_preview, 'source_provenance_verified': False,
              'secret_tree_removed': False, 'generations': {}}
    exported = []
    try:
        with tempfile.TemporaryDirectory(prefix='wk-cc-', dir='/tmp') as temporary:
            scratch = Path(temporary).resolve()
            env = {'PATH': '/usr/bin:/bin', 'HOME': str(scratch), 'LANG': 'C', 'LC_ALL': 'C', 'TZ': 'UTC'}
            os.environ.clear(); os.environ.update(env)
            if evidence is None:
                evidence = {'cases': [], 'commands': []}
            keys = load_fixture('stable_client_test_keys', 'test_validate_signing_material.py')
            private = scratch / 'private-test-keys'
            factory = keys.TestKeyFactory(private)
            prepared = {}
            try:
                # Generate each family once; both controls copy the same public certificates.
                shared_control, generated = generate_control(scratch, factory, keys)
                for label, version, bootstrap_version in (('A', '3.0.0', '1.1.1'), ('B', '3.0.1', '1.1.2')):
                    generation = scratch / label
                    generation.mkdir()
                    control = generation / 'control'
                    shutil.copytree(shared_control, control)
                    bootstrap_manifest = control / 'manifests/bootstrap-packages.json'
                    bootstrap_value = json.loads(bootstrap_manifest.read_bytes())
                    bootstrap_value['version'] = bootstrap_version
                    bootstrap_manifest.write_bytes(canonical(bootstrap_value))
                    source = fixture_source(generation, version=version, binary_payloads=payloads.get(version))
                    shared = common(control, source)
                    channels = control / 'manifests/channels.json'
                    plan, inventory, bootstrap = (generation / name for name in
                                                   ('plan.json', 'inventory.json', 'bootstrap.json'))
                    unsigned, composed = generation / 'unsigned', generation / 'composed'
                    planned = invoke(evidence, 'plan', cli('plan-package-publication.py', '--channels', channels,
                                                         *shared), env)
                    plan.write_bytes(planned.stdout)
                    value = json.loads(planned.stdout)
                    require(value['schema'] == 'wukongim.native_package_publication_plan/v2'
                            and value['control_sha'] is None, 'TEST ONLY candidate plan identity differs')
                    invoke(evidence, 'prepare', cli('prepare-package-site.py', '--channels', channels, '--plan', plan,
                           '--source-assets', source / 'assets', '--bootstrap-manifest', bootstrap_manifest,
                           '--apt-public-cert', control / 'keys/apt-stable.asc',
                           '--rpm-public-cert', control / 'keys/rpm-stable.asc',
                           '--bootstrap-builder', ROOT / 'scripts/build-repository-bootstrap-packages.py',
                           '--builder', trusted / builder, '--output', unsigned, '--inventory', inventory,
                           '--bootstrap-inventory', bootstrap, *shared), env)
                    signed, receipts = {}, {}
                    for family in ('apt', 'rpm'):
                        key, successor = generated[family]
                        signed[family], receipts[family] = sign_family(generation, family, key, successor,
                                                                      control, unsigned, evidence, env)
                    invoke(evidence, 'compose', cli('compose-package-site.py', '--channels', channels,
                           '--signing', control / 'manifests/stable-public-keys.json',
                           '--signing-toolchain', control / 'manifests/signing-toolchain.json', '--plan', plan,
                           '--inventory', inventory, '--bootstrap-inventory', bootstrap,
                           '--apt-tree', signed['apt'], '--apt-receipt', receipts['apt'],
                           '--apt-public-cert', control / 'keys/apt-stable.asc',
                           '--rpm-tree', signed['rpm'], '--rpm-receipt', receipts['rpm'],
                           '--rpm-public-cert', control / 'keys/rpm-stable.asc', '--output-root', composed,
                           *shared), env)
                    prepared[label] = (generation, version, bootstrap_version, control, source, plan,
                                       composed, receipts)
                if include_preview:
                    preview = _prepare_delivery_preview(scratch, shared_control, generated, trusted, evidence, env)
            finally:
                try:
                    factory.close()
                finally:
                    if private.exists():
                        shutil.rmtree(private)
            require(not private.exists(), 'TEST ONLY secret cleanup failed')
            result['secret_tree_removed'] = True
            public = scratch / 'public'
            public.mkdir()
            for label, (generation, version, bootstrap_version, control, source, plan,
                        composed, receipts) in prepared.items():
                shared = common(control, source)
                anchors = ['--channels', str(control / 'manifests/channels.json'),
                           '--signing', str(control / 'manifests/stable-public-keys.json'),
                           '--apt-public-cert', str(control / 'keys/apt-stable.asc'),
                           '--rpm-public-cert', str(control / 'keys/rpm-stable.asc')]
                snapshot = composed / 'audit/snapshot.json'
                invoke(evidence, 'verify-after-secret-removal', cli('verify-production-package-site.py',
                       '--site-root', composed / 'site', '--snapshot', snapshot, '--plan', plan,
                       *anchors, *shared), env)
                archive_name = f'wukongim-stable-{version}-{facts(snapshot)["sha256"]}-candidate-site.tar'
                archive = generation / archive_name
                receipt = generation / archive_name.replace('-site.tar', '-receipt.json')
                invoke(evidence, 'archive-create', cli('archive-package-snapshot.py', 'create',
                       '--source-dir', composed, '--archive', archive), env)
                audit = [*anchors, '--signing-toolchain', str(control / 'manifests/signing-toolchain.json'),
                         '--plan', str(plan), '--apt-receipt', str(receipts['apt']),
                         '--rpm-receipt', str(receipts['rpm']), '--archive', str(archive), *shared]
                invoke(evidence, 'audit-create', cli('package-audit-receipt.py', 'create', *audit,
                       '--output', receipt), env)
                invoke(evidence, 'audit-verify', cli('package-audit-receipt.py', 'verify', *audit,
                       '--receipt', receipt), env)
                destination = public / label
                destination.mkdir()
                paths = {'control': control, 'source': source, 'plan': plan, 'archive': archive,
                         'receipt': receipt, 'apt_receipt': receipts['apt'], 'rpm_receipt': receipts['rpm'],
                         'site': composed / 'site'}
                exported_paths = {}
                for name, path in paths.items():
                    target = destination / path.name
                    if path.is_dir():
                        shutil.copytree(path, target)
                    else:
                        shutil.copyfile(path, target)
                    exported_paths[name] = f'{label}/{path.name}'
                result['generations'][label] = {**exported_paths, 'version': version,
                                                 'bootstrap_version': bootstrap_version}
            if include_preview:
                result['preview'] = _finish_delivery_preview(preview, public, evidence, env)
            require(before == {str(path.relative_to(ROOT)): facts(path) for path in controls},
                    'checked-in control changed during client fixture generation')
            # No output crosses the public boundary until both generations validate
            # without private material. Clean only directories created by this call.
            for label in (('A', 'B', 'preview') if include_preview else ('A', 'B')):
                target = output_root / label
                require(not target.exists(), 'client fixture export path already exists')
                exported.append(target)
                shutil.copytree(public / label, target)
        return result
    except Exception:
        for path in exported:
            if path.exists():
                shutil.rmtree(path)
        raise
    finally:
        os.environ.clear(); os.environ.update(previous_environment)


def _prepare_delivery_preview(scratch, shared_control, generated, trusted, evidence, env):
    """Build a native preview using public synthetic source anchors, never GitHub truth."""
    generation = scratch / 'preview'; generation.mkdir()
    control = generation / 'control'; shutil.copytree(shared_control, control)
    signing_path = control / 'manifests/preview-signing.json'
    signing = json.loads(signing_path.read_bytes())
    stable_roles = json.loads((control / 'manifests/stable-public-keys.json').read_bytes())
    for family in ('apt', 'rpm'):
        shutil.copyfile(control / f'keys/{family}-stable.asc', control / f'keys/{family}-preview.asc')
        for field in ('primary_fingerprint', 'signing_subkeys'):
            signing[family][field] = stable_roles[family][field]
    signing_path.write_bytes(canonical(signing))
    fixtures = load_fixture('delivery_preview_native_packages', 'native_source_metadata_drill.py')
    build = generation / 'package-build'; build.mkdir()
    version = '3.1.0-rc.1'
    deb, rpm = fixtures.build_packages(build, '3.1.0~rc.1', '3.1.0~rc.1')
    source = generation / 'source'; source.mkdir()
    resolution_path, assets = fixtures.fixture_receipt(source, version, deb, rpm)
    assets.chmod(0o755)
    resolution = json.loads(resolution_path.read_bytes())
    release = {'version': version, 'source_sha': resolution['source_sha'],
        'source_release_id': resolution['release_id'], 'package_release_id': 10,
        'deb_sha256': facts(deb)['sha256'], 'rpm_sha256': facts(rpm)['sha256'],
        'state': 'active', 'not_before': None}
    prepare_assets = generation / 'prepare-assets'
    shutil.copytree(assets, prepare_assets / str(release['source_release_id']))
    channels_path = control / 'manifests/channels.json'
    channels = json.loads(channels_path.read_bytes())
    channels['channels']['preview'].update(releases=[release],
        retirement={'phase': 'none', 'version': None, 'not_before': None},
        publication={'audit_release_id': 10, 'base_audit_release_id': None,
                     'operation': 'add_release', 'target_version': version})
    channels_path.write_bytes(canonical(channels))
    # These JSON files satisfy only the archive's byte-binding contract. They
    # are conspicuously synthetic, not verified GitHub/Sigstore attestations.
    attestations = generation / 'source-attestations'; attestations.mkdir()
    items = []
    for item in resolution['assets']:
        name = item['name'] + '.attestation.json'
        proof = attestations / name
        proof.write_bytes(canonical({'test_only': True, 'github_verified': False,
            'asset': item['name'], 'sha256': item['sha256']}))
        items.append({'asset': item['name'], 'asset_sha256': item['sha256'],
                      'evidence_file': name, 'evidence_sha256': facts(proof)['sha256']})
    (attestations / 'source-attestations.json').write_bytes(canonical({
        'schema': 'wukongim/source-attestation-verification/v1',
        'repository': 'WuKongIM/WuKongIM', 'release_id': release['source_release_id'],
        'tag': 'v' + version, 'version': version, 'source_sha': release['source_sha'],
        'source_ref': 'refs/tags/v' + version,
        'signer_workflow': 'WuKongIM/WuKongIM/.github/workflows/binary-release-publish.yml',
        'deny_self_hosted_runners': True, 'asset_count': 7, 'assets': items,
        'assets_revalidated_after_attestations': True}))
    plan, inventory, bootstrap = (generation / name for name in ('plan.json', 'inventory.json', 'bootstrap.json'))
    unsigned, composed = generation / 'unsigned', generation / 'composed'
    planned = invoke(evidence, 'preview-plan', cli('plan-package-publication.py', '--channels', channels_path,
                     '--control-sha', 'b' * 40), env)
    plan.write_bytes(planned.stdout)
    invoke(evidence, 'preview-prepare', cli('prepare-package-site.py', '--channels', channels_path, '--plan', plan,
        '--source-assets', prepare_assets, '--bootstrap-manifest', control / 'manifests/bootstrap-packages.json',
        '--apt-public-cert', control / 'keys/apt-preview.asc', '--rpm-public-cert', control / 'keys/rpm-preview.asc',
        '--bootstrap-builder', ROOT / 'scripts/build-repository-bootstrap-packages.py',
        '--builder', trusted / 'scripts/build-native-package-repositories.sh', '--output', unsigned,
        '--inventory', inventory, '--bootstrap-inventory', bootstrap), env)
    signed, receipts = {}, {}
    for family in ('apt', 'rpm'):
        key, successor = generated[family]
        signed[family], receipts[family] = sign_family(generation, family, key, successor, control,
            unsigned, evidence, env, channel='preview')
    invoke(evidence, 'preview-compose', cli('compose-package-site.py', '--channels', channels_path,
        '--signing', signing_path, '--signing-toolchain', control / 'manifests/signing-toolchain.json',
        '--plan', plan, '--inventory', inventory, '--bootstrap-inventory', bootstrap,
        '--source-attestations', attestations, '--apt-tree', signed['apt'], '--apt-receipt', receipts['apt'],
        '--apt-public-cert', control / 'keys/apt-preview.asc', '--rpm-tree', signed['rpm'],
        '--rpm-receipt', receipts['rpm'], '--rpm-public-cert', control / 'keys/rpm-preview.asc',
        '--output-root', composed), env)
    for name, path in {'plan.json': plan, 'apt-signing.json': receipts['apt'],
                       'rpm-signing.json': receipts['rpm']}.items():
        shutil.copyfile(path, composed / 'audit' / name)
    return generation, control, source, plan, composed, receipts, attestations


def _finish_delivery_preview(preview, public, evidence, env):
    """Verify with public keys after private-tree removal, then export a canonical archive."""
    generation, control, source, plan, composed, receipts, attestations = preview
    anchors = ['--channels', control / 'manifests/channels.json', '--signing',
        control / 'manifests/preview-signing.json', '--apt-public-cert', control / 'keys/apt-preview.asc',
        '--rpm-public-cert', control / 'keys/rpm-preview.asc']
    invoke(evidence, 'preview-verify-after-secret-removal', cli('verify-production-package-site.py',
        '--site-root', composed / 'site', '--snapshot', composed / 'audit/snapshot.json', *anchors), env)
    archive = generation / 'wukongim-preview-r10-site.tar'
    receipt = generation / 'wukongim-preview-r10-receipt.json'
    invoke(evidence, 'preview-archive', cli('archive-package-snapshot.py', 'create',
        '--source-dir', composed, '--archive', archive), env)
    audit = [*anchors, '--signing-toolchain', control / 'manifests/signing-toolchain.json',
        '--plan', plan, '--apt-receipt', receipts['apt'], '--rpm-receipt', receipts['rpm'],
        '--source-attestations', attestations, '--archive', archive]
    invoke(evidence, 'preview-audit-create', cli('package-audit-receipt.py', 'create', *audit, '--output', receipt), env)
    invoke(evidence, 'preview-audit-verify', cli('package-audit-receipt.py', 'verify', *audit, '--receipt', receipt), env)
    destination = public / 'preview'; destination.mkdir()
    exported = {}
    for name, path in {'control': control, 'source': source, 'plan': plan, 'archive': archive, 'receipt': receipt,
                       'apt_receipt': receipts['apt'], 'rpm_receipt': receipts['rpm']}.items():
        target = destination / path.name
        if path.is_dir():
            shutil.copytree(path, target)
        else:
            shutil.copyfile(path, target)
        exported[name] = 'preview/' + path.name
    value = json.loads(receipt.read_bytes())
    exported['descriptor'] = {'archive': exported['archive'], 'receipt': exported['receipt'],
        'control_dir': exported['control'], 'audit_release_id': 10, 'control_sha': 'b' * 40,
        'archive_sha256': facts(archive)['sha256'], 'archive_size': facts(archive)['size'],
        'receipt_sha256': facts(receipt)['sha256'], 'snapshot_sha256': value['archive']['snapshot_sha256'],
        'source': {'repository': 'WuKongIM/WuKongIM', 'version': '3.1.0-rc.1',
                   **{key: value['source'][key] for key in ('release_id', 'source_sha')}},
        'control_files_sha256': {name: facts(control / name)['sha256'] for name in (
            'manifests/channels.json', 'manifests/preview-signing.json', 'manifests/signing-toolchain.json',
            'manifests/bootstrap-packages.json', 'keys/apt-preview.asc', 'keys/rpm-preview.asc')}}
    return exported


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
                unsigned: Path, evidence: dict, env: dict, *, channel: str = 'stable') -> tuple[Path, Path]:
    require(channel in ('stable', 'preview'), 'TEST ONLY signing channel differs')
    apt_release = f'dists/{channel}/Release'
    rpm_repository = f'{channel}/el/9/x86_64'
    output = scratch / f'signed-{family}'
    args = cli('sign-package-family.py', '--family', family, '--input-root', unsigned / family,
               '--output-root', output, '--public-cert', control / f'keys/{family}-{channel}.asc',
               '--primary-fingerprint', key.primary_fingerprint,
               '--signing-subkey-fingerprint', key.signing_subkey_fingerprint,
               '--next-signing-subkey-fingerprint', successor,
               '--secret-subkey-base64-file', key.selected_secret_base64,
               '--passphrase-file', key.passphrase_file)
    if family == 'apt':
        args += ['--apt-release', apt_release]
    else:
        repository = unsigned / family / rpm_repository
        paths = sorted(p.relative_to(repository).as_posix() for p in (repository / 'Packages').glob('*.rpm'))
        require(len(paths) == 2, 'RPM allowlist must cover product and channel bootstrap')
        for name, value in (
            ('new', {'schema': 'wukongim/rpm-package-allowlist/v1',
                     'packages': [{'path': path, **facts(repository / path)} for path in paths]}),
            ('signed', {'schema': 'wukongim/rpm-package-allowlist/v1', 'packages': []}),
            ('active', {'schema': 'wukongim/rpm-active-allowlist/v1', 'paths': paths}),
        ):
            path = scratch / f'{name}-allowlist.json'
            path.write_bytes(canonical(value))
            args += [f'--{name}-rpm-allowlist', str(path)]
        args += ['--rpm-repository', rpm_repository]
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
    transport = load_fixture('stable_seal_transport_native', 'native_stable_seal_transport_drill.py')
    transport.exercise(scratch=scratch,control=control,source=source,plan=plan,archive=archive,
        receipt=receipt,receipts=receipts,evidence=evidence,env=env)
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
