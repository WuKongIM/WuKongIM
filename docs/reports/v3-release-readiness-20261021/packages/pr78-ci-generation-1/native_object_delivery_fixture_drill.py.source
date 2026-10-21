#!/usr/bin/env python3
"""TEST ONLY: native preview + stable A/B through the unchanged offline delivery.

Run in the pinned non-root, network-disabled Linux signing container. Tiny
data-only packages prove native format/signatures, not product CLI or GitHub
provenance. The exported public fixtures never authorize production delivery.
"""
from __future__ import annotations

import argparse
import copy
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import shutil
import sys
import tempfile
import time

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))
sys.path.insert(0, str(ROOT / 'tests'))
import native_stable_candidate_drill as candidate_fixture
import native_stable_clients_drill as client_fixture

CASES = (
    'export-native-public-fixtures', 'client-rejects-missing-binaries',
    'client-rejects-malformed-binaries', 'client-rejects-wrong-source',
    'verify-native-preview', 'reject-structural-preview', 'repeat-canonical-archive',
    'reject-preview-deb', 'reject-preview-rpm', 'reject-preview-apt-index',
    'reject-preview-rpm-metadata', 'reject-preview-signature-crypto',
    'plan-native-composite-A', 'plan-native-composite-B', 'repeat-composite-plan',
    'reject-preview-archive', 'reject-preview-receipt', 'reject-preview-control',
    'reject-preview-audit-id', 'reject-preview-control-sha', 'reject-missing-preview',
    'reject-missing-retained-archive', 'reject-test-without-optin',
    'reject-fresh-production-auth', 'reject-plan-digest', 'reject-route-limit',
    'materialize-exact-A', 'materialize-exact-B', 'export-public-only',
)
RESOURCE = {'production_enabled': False, 'account_id': None, 'bucket': None,
            'region': None, 'endpoint': None, 'domain': 'packages.githubim.com'}
PRODUCT_SHA = 'fc18221282f289088ec9b4282fda8e0bfc67a3b8'


class Preview:
    """Resolve the exported public descriptor; never synthesize native bytes."""
    def __init__(self, root, entry):
        self.archive = root / entry['archive']
        self.root = root
        self.entry = entry

    def descriptor(self):
        value = copy.deepcopy(self.entry['descriptor'])
        for name in ('archive', 'receipt', 'control_dir'):
            value[name] = str(self.root / value[name])
        return value


def require(condition, message):
    if not condition:
        raise RuntimeError(message)


def module(name):
    return client_fixture.module('scripts/' + name)


def exercise(args, evidence):
    require(sys.platform.startswith('linux') and os.geteuid() != 0,
            'non-root isolated Linux signing container required')
    exports_root = args.output_dir / 'fixtures'
    exports_root.mkdir()

    def case(name, action):
        evidence['phase'] = name
        result = action()
        evidence['cases'].append({'case': name, 'passed': True})
        return result

    def run(name, filename, arguments, *, reason=None):
        result = client_fixture.run_command(evidence, name,
            [sys.executable, '-B', str(ROOT / 'scripts' / filename), *map(str, arguments)], timeout=180)
        require(result.returncode == (1 if reason else 0), name + ': unexpected exit')
        if reason:
            require(reason in result.stdout.decode(errors='replace'), name + ': refusal differs')
        return result

    def rejected(action, reason):
        try:
            action()
        except (RuntimeError, ValueError) as error:
            require(reason in str(error), 'unexpected refusal: ' + str(error))
        else:
            raise RuntimeError('expected refusal did not happen')

    exports = case('export-native-public-fixtures', lambda: candidate_fixture.export_delivery_generations(
        output_root=exports_root, trusted_root=args.trusted_root,
        evidence=evidence.setdefault('export', {'commands': [], 'cases': []})))
    require(exports['test_only'] is True and exports['data_only'] is True
            and exports['secret_tree_removed'] is True
            and exports['source_provenance_verified'] is False, 'fixture export claims differ')
    (args.output_dir / 'fixture-export.json').write_bytes(candidate_fixture.canonical(exports))
    with tempfile.TemporaryDirectory(prefix='wk-d3-', dir='/tmp') as temporary:
        scratch = Path(temporary)
        empty = scratch / 'client-export'; empty.mkdir()
        binaries = scratch / 'missing-binaries'
        kwargs = dict(output_root=empty, binaries_root=binaries, trusted_root=args.trusted_root,
                      product_source_sha=PRODUCT_SHA)
        case('client-rejects-missing-binaries', lambda: rejected(
            lambda: candidate_fixture.export_client_generations(**kwargs), 'binary generation root'))
        for version in ('3.0.0', '3.0.1'):
            directory = binaries / version; directory.mkdir(parents=True)
            for name in ('wukongim', 'wkcli'):
                path = directory / name; path.write_bytes(b'TEST ONLY not an ELF binary\n' * 4); path.chmod(0o755)
        case('client-rejects-malformed-binaries', lambda: rejected(
            lambda: candidate_fixture.export_client_generations(**kwargs), 'ELF'))
        case('client-rejects-wrong-source', lambda: rejected(
            lambda: candidate_fixture.export_client_generations(**{**kwargs, 'product_source_sha': '1' * 40}), 'fixed product source'))
        require(not list(empty.iterdir()), 'rejected client export left output')

        preview = Preview(exports_root, exports['preview'])
        descriptor = preview.descriptor()
        control = Path(descriptor['control_dir'])
        archive_module = module('archive-package-snapshot.py')
        extracted = scratch / 'preview-extracted'
        archive_module.extract_snapshot(archive_path=preview.archive, output_dir=extracted,
            max_members=archive_module.DEFAULT_MAX_MEMBERS,
            max_total_size=1024 * 1024 * 1024)
        anchors = ['--channels', control / 'manifests/channels.json',
            '--signing', control / 'manifests/preview-signing.json',
            '--apt-public-cert', control / 'keys/apt-preview.asc',
            '--rpm-public-cert', control / 'keys/rpm-preview.asc']

        def verify(name, root=extracted, *, reason=None, override=None):
            return run(name, 'verify-production-package-site.py',
                ['--site-root', root / 'site', '--snapshot', root / 'audit/snapshot.json',
                 *(override or anchors)], reason=reason)

        case('verify-native-preview', lambda: verify('verify-native-preview'))
        from test_plan_object_storage_publication import InputSnapshot
        structural = InputSnapshot(scratch / 'structural-preview')
        fake_control = structural.control
        case('reject-structural-preview', lambda: verify('reject-structural-preview', structural.f.output,
            override=['--channels', fake_control / 'manifests/channels.json', '--signing',
                fake_control / 'manifests/preview-signing.json', '--apt-public-cert',
                fake_control / 'keys/apt-preview.asc', '--rpm-public-cert', fake_control / 'keys/rpm-preview.asc'],
            reason='production package-site verification failed:'))
        repeated = scratch / preview.archive.name
        case('repeat-canonical-archive', lambda: run('repeat-canonical-archive',
            'archive-package-snapshot.py', ['create', '--source-dir', extracted, '--archive', repeated]))
        require(candidate_fixture.facts(repeated) == candidate_fixture.facts(preview.archive), 'canonical archive differs')
        site = extracted / 'site'
        mutations = {
            'reject-preview-deb': candidate_fixture.only((site / 'apt').rglob('wukongim_*.deb')),
            'reject-preview-rpm': candidate_fixture.only((site / 'rpm').rglob('wukongim_*.rpm')),
            'reject-preview-apt-index': site / 'apt/dists/preview/main/binary-amd64/Packages',
            'reject-preview-rpm-metadata': site / 'rpm/preview/el/9/x86_64/repodata/repomd.xml',
        }
        for name, path in mutations.items():
            with candidate_fixture.changed(path, path.read_bytes() + b'\nTEST ONLY changed\n'):
                case(name, lambda n=name: verify(n, reason='production package-site verification failed:'))
        signature = site / 'apt/dists/preview/Release.gpg'
        receipt = extracted / 'audit/apt-signing.json'
        corrupt = b'TEST ONLY invalid OpenPGP signature\n'
        with candidate_fixture.changed(signature, corrupt), candidate_fixture.changed(receipt, candidate_fixture.rebind_apt_signature_receipt(receipt.read_bytes(), corrupt)):
            case('reject-preview-signature-crypto', lambda: verify('reject-preview-signature-crypto',
                reason='signature'))

        descriptors, plans, inputs, used_preview = client_fixture.seal_generations(exports, exports_root, scratch, evidence,
                                                                    preview=preview)
        require(used_preview is preview, 'seal helper substituted structural preview')
        for label in ('A', 'B'):
            plan = plans[label]
            def check_plan():
                require(plan['test_only'] is True and plan['plan_only'] is True, 'plan mode differs')
                require(plan['verification']['stable_signatures_verified'] is True, 'native stable verifier skipped')
                for field in ('github_provenance_verified', 'activation_authorized'):
                    require(plan['verification'][field] is False, 'false planner field upgraded')
                require(plan['retention']['delete_objects'] == [], 'unexpected deletion')
                require({x['source']['audit_release_id'] for x in plan['snapshot_objects']} == {10, 4242, 4243},
                        'snapshot source set differs')
            case('plan-native-composite-' + label, check_plan)
        planned = scratch / 'repeat-plan.json'
        case('repeat-composite-plan', lambda: run('repeat-composite-plan', 'plan-object-storage-publication.py',
            ['--composite', '--test-only', '--inputs', inputs['B'], '--output', planned]))
        require(planned.read_bytes() == candidate_fixture.canonical(plans['B']), 'repeated composite plan differs')
        original = json.loads(inputs['B'].read_bytes())

        def bad_plan(name, change, reason):
            value = copy.deepcopy(original); change(value)
            path = scratch / (name + '-inputs.json'); path.write_bytes(candidate_fixture.canonical(value))
            output = scratch / (name + '-plan.json')
            case(name, lambda: run(name, 'plan-object-storage-publication.py',
                ['--composite', '--test-only', '--inputs', path, '--output', output], reason=reason))
            require(not output.exists(), 'rejected plan published partial output')

        for name, field in (('reject-preview-archive', 'archive_sha256'), ('reject-preview-receipt', 'receipt_sha256'),
                            ('reject-preview-control-sha', 'control_sha')):
            bad_plan(name, lambda value, f=field: value['current']['preview']['snapshot'].__setitem__(
                f, ('0' * 40 if f == 'control_sha' else '0' * 64)), 'object-storage planning failed:')
        bad_plan('reject-preview-audit-id', lambda value: value['current']['preview']['snapshot'].__setitem__(
            'audit_release_id', 11), 'object-storage planning failed:')
        bad_plan('reject-preview-control', lambda value: value['current']['preview']['snapshot'][
            'control_files_sha256'].__setitem__('manifests/channels.json', '0' * 64), 'object-storage planning failed:')
        bad_plan('reject-missing-preview', lambda value: value['current'].pop('preview'), 'object-storage planning failed:')
        bad_plan('reject-missing-retained-archive', lambda value: value['retain'][0].__setitem__(
            'archive', str(scratch / 'missing.tar')), 'object-storage planning failed:')
        case('reject-test-without-optin', lambda: run('reject-test-without-optin',
            'plan-object-storage-publication.py', ['--composite', '--inputs', inputs['B'],
            '--output', scratch / 'forbidden-plan.json'], reason='TEST ONLY input requires matching explicit test_only mode'))
        fresh = module('verify-object-storage-delivery-inputs.py')
        case('reject-fresh-production-auth', lambda: rejected(lambda: fresh.verify_delivery_inputs(
            inputs=inputs['B'], expected_plan_sha256=candidate_fixture.facts(planned)['sha256'], executor_sha=args.source_sha),
            'fresh delivery input authentication failed'))
        resource = scratch / 'resource.json'; resource.write_bytes(candidate_fixture.canonical(RESOURCE))

        def prepare(name, label, output, *, digest=None, routes=10000, reason=None):
            return run(name, 'prepare-object-storage-delivery.py', ['--inputs', inputs[label],
                '--expected-plan-sha256', digest or hashlib.sha256(candidate_fixture.canonical(plans[label])).hexdigest(),
                '--output-dir', output, '--resource-intent', resource, '--max-routes', routes,
                '--composite', '--test-only'], reason=reason)

        for name, options, reason in (
            ('reject-plan-digest', {'digest': '0' * 64}, 'recomputed plan digest differs'),
            ('reject-route-limit', {'routes': 1}, 'route table exceeds declared route limit')):
            output = scratch / name
            case(name, lambda n=name, o=output, kw=options, r=reason: prepare(n, 'B', o, **kw, reason=r))
            require(not output.exists(), 'rejected preparation published output')
        evidence['materializations'] = {}
        for label in ('A', 'B'):
            output = scratch / ('delivery-' + label)
            case('materialize-exact-' + label, lambda: prepare('materialize-exact-' + label, label, output))
            delivery = json.loads((output / 'delivery.json').read_bytes())
            require(delivery['planner_verification'] == plans[label]['verification'], 'planner evidence changed')
            for field in ('production_enabled', 'publication_verified', 'cloud_verified'):
                require(delivery[field] is False, 'materializer false field upgraded')
            expected = {item['object_key']: item for name in ('snapshot_objects', 'immutable_aliases', 'generated_objects')
                        for item in plans[label][name]}
            require({item['object_key'] for item in delivery['objects']} == set(expected), 'object set differs')
            for item in delivery['objects']:
                require(candidate_fixture.facts(output / item['materialized_file']) == {k: item[k] for k in ('sha256', 'size')},
                        'materialized bytes differ')
                require(all(item[key] == value for key, value in expected[item['object_key']].items()),
                        'materialized object lost exact source/cache binding')
            require(len(delivery['client_requests']) == 2 and
                    {x['audit_release_id'] for x in delivery['client_requests']} ==
                    {10, descriptors[label]['audit_release_id']}, 'retained generation became publication target')
            require(any(x['source']['audit_release_id'] != descriptors[label]['audit_release_id']
                        for x in plans[label]['immutable_aliases'] if x['source']['channel'] == 'stable'),
                    'cross-generation stable alias missing')
            evidence['materializations'][label] = {'plan': candidate_fixture.facts(output / 'transport-plan.json'),
                'delivery': candidate_fixture.facts(output / 'delivery.json'), 'objects': len(delivery['objects']),
                'routes': len(delivery['routes']), 'source_audit_ids': [10, 4242, 4243]}
            # Evidence describes this successful invocation; absolute scratch paths
            # are intentionally not exported as a reusable production request.
        evidence['verification'] = {'native_preview_verified': True, 'native_stable_verified': True,
            'data_only_packages': True, 'installed_cli_verified': False,
            'github_provenance_verified': False, 'fresh_production_auth_verified': False,
            'sdk_integration_verified': False, 'cloud_verified': False,
            'public_clients_verified': False, 'activation_authorized': False}
    def public_only():
        for path in exports_root.rglob('*'):
            require(not path.is_symlink(), 'public fixture contains a link')
            if path.is_file():
                require(not any(x in path.name.lower() for x in ('passphrase', 'secret', 'private')),
                        'public fixture contains private filename')
                raw = path.read_bytes()
                require(b'BEGIN PGP PRIVATE KEY BLOCK' not in raw, 'public fixture contains private key armor')
    case('export-public-only', public_only)
    require({x['case'] for x in evidence['cases']} == set(CASES)
            and len(evidence['cases']) == len(CASES), 'case inventory incomplete or duplicated')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output-dir', type=Path, required=True)
    parser.add_argument('--trusted-root', type=Path, required=True)
    parser.add_argument('--source-sha', required=True)
    args = parser.parse_args()
    require(args.output_dir.is_dir() and not any(args.output_dir.iterdir()), 'output must exist and be empty')
    evidence = {'schema': 'wukongim/test-only-native-delivery-fixture/v1', 'test_only': True,
        'source_sha': args.source_sha, 'passed': False, 'phase': 'start', 'cases': [], 'commands': [],
        'started_at_unix': int(time.time()), 'scratch_removed': False, 'failure': None}
    output = args.output_dir / 'result.json'
    try:
        with output.open('x') as stream:
            try:
                exercise(args, evidence)
                evidence['passed'] = True
            except Exception as error:
                evidence['failure'] = {'type': type(error).__name__, 'message': str(error)[:1024]}
            finally:
                evidence['scratch_removed'] = not list(Path('/tmp').glob('wk-d3-*'))
                evidence['finished_at_unix'] = int(time.time())
                stream.write(candidate_fixture.canonical(evidence).decode())
    finally:
        if not evidence['passed']:
            shutil.rmtree(args.output_dir / 'fixtures', ignore_errors=True)
    print(json.dumps({'passed': evidence['passed'], 'cases': len(evidence['cases']),
                      'phase': evidence['phase'], 'failure': evidence['failure']}))
    return 0 if evidence['passed'] and evidence['scratch_removed'] else 1


if __name__ == '__main__':
    raise SystemExit(main())
