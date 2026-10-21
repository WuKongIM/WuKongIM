#!/usr/bin/env python3
"""Prepare local transport objects and simulate delivery; never contact a cloud.

The CLI only prepares files. simulate_delivery is a function-level test seam:
no SDK, endpoint loader, credentials, network runner or production mode exists.
Its observations can never authorize publication, including after fake success.
"""
from __future__ import annotations

import argparse
import copy
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import sys
import tempfile


def _module(name, filename):
    spec = importlib.util.spec_from_file_location(name, Path(__file__).parent / filename)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


P = _module('delivery_planner', 'plan-object-storage-publication.py')
C = P.C
V = _module('delivery_clients', 'validate-production-package-clients.py')
MAX_JSON_BYTES = 8 * 1024 * 1024
MAX_ROUTES = 10000
RESOURCE_FIELDS = {'production_enabled', 'account_id', 'bucket', 'region', 'endpoint', 'domain'}
FACT_FIELDS = ('sha256', 'size', 'cache_control')
ERRORS = (ValueError, RuntimeError, OSError, KeyError, TypeError)


class DeliveryError(ValueError):
    """The offline preparation or simulated observation is inconsistent."""


def require(condition, message):
    if not condition:
        raise DeliveryError(message)


def canonical(value):
    return json.dumps(value, sort_keys=True, separators=(',', ':'), default=str).encode() + b'\n'


def sha256(raw):
    return hashlib.sha256(raw).hexdigest()


def _json_write(path, value):
    raw = canonical(value)
    require(len(raw) <= MAX_JSON_BYTES, 'delivery evidence exceeds JSON limit')
    C.write_exclusive(path, raw, 'delivery evidence')


def _read(path, label):
    checked = C.candidate_path(path.parent, path.name, label)
    return C.read_checked(checked, label, maximum_bytes=MAX_JSON_BYTES)


def _resource(value):
    C.exact(value, RESOURCE_FIELDS, 'disabled resource intent')
    require(value['production_enabled'] is False, 'production must remain disabled')
    require(all(value[key] is None for key in ('account_id', 'bucket', 'region', 'endpoint')),
            'offline resource identities must remain unknown')
    require(value['domain'] == C.REPOSITORY_ORIGIN.removeprefix('https://'),
            'resource domain must remain the canonical public domain')
    return copy.deepcopy(value)


def _load_plan(inputs, expected_digest, composite, test_only):
    raw = _read(inputs, 'transport inputs')
    require(isinstance(expected_digest, str) and re.fullmatch('[0-9a-f]{64}', expected_digest),
            'expected plan digest must be SHA-256')
    plan = P.make_plan(inputs, composite=composite, test_only=test_only)
    require(sha256(C.canonical_json(plan)) == expected_digest, 'recomputed plan digest differs')
    require(_read(inputs, 'transport inputs') == raw, 'transport inputs changed during planning')
    return plan, raw, json.loads(raw, object_pairs_hook=C.reject_duplicate_pairs)


def _descriptors(value, composite):
    if composite:
        values = [value['current']['preview'], value['current']['stable'], *value['retain']]
        return [(v['channel'], v['snapshot'] if v['channel'] == 'preview' else v) for v in values]
    return [('preview', v) for v in [value['current'], *value['retain']]]


def _facts(value):
    return {field: value[field] for field in FACT_FIELDS}


def _object_key(key):
    C.safe_relative(key, 'object key')
    require(key.startswith(('snapshots/', 'public/', 'generated/')), 'object key is outside transport namespaces')


def _route_table(plan, objects, max_routes):
    """Compile exact matches only; aliases precede original snapshot routes.

    This is a bounded request table, not Alibaba wire configuration. There are
    no regex/prefix rewrites to broaden access or recursively rewrite aliases.
    """
    require(type(max_routes) is int and 0 < max_routes <= MAX_ROUTES, 'route limit is invalid')
    routes = []
    selected = {}

    def add(uri, value, kind):
        if uri != '/':
            require(isinstance(uri, str) and uri.startswith('/'), 'route URI must be absolute')
            P._public_path(uri[1:])
        key = value['object_key']
        require(key in objects and _facts(objects[key]) == _facts(value),
                'route must reference an exact materialized object')
        row = {'public_uri': uri, 'object_key': key, **_facts(value), 'kind': kind}
        if uri in selected:
            # The original planner permits different physical snapshots to
            # serve the same URI when all byte/cache facts are identical.
            require(_facts(selected[uri]) == _facts(row),
                    'route URI collision')
            return
        selected[uri] = row
        routes.append(row)

    for value in plan['entrypoints']:
        require(value['cache_control'] == 'no-store', 'floating entrypoint must be no-store')
        add(value['public_uri'], value, 'entrypoint')
    require('/repo' in selected and '/index.html' in selected, 'preview shared entrypoints are required')
    preview_id = (plan['current_audit_release_ids']['preview'] if plan['channel'] == 'composite'
                  else plan['current_audit_release_id'])
    for uri in ('/repo', '/index.html'):
        require(selected[uri]['object_key'].startswith(f'snapshots/preview/r{preview_id}-'),
                'shared entrypoints must belong to current preview')
    add('/', selected['/index.html'], 'entrypoint')
    for value in plan['immutable_aliases']:
        require(value['cache_control'] == P.IMMUTABLE_CACHE, 'immutable alias cache metadata differs')
        add(value['public_uri'], value, 'alias')
    for value in plan['snapshot_objects']:
        add('/' + value['object_key'], value, 'snapshot')
    require(len(routes) <= max_routes, 'route table exceeds declared route limit')
    require(len(canonical(routes)) <= MAX_JSON_BYTES, 'route table exceeds JSON limit')
    return routes


def route_request(routes, public_uri):
    """Resolve only a listed URI, without URL decoding, normalization or fallback."""
    return next((copy.deepcopy(row) for row in routes if row['public_uri'] == public_uri), None)


def _client_requests(descriptors, inputs, output_dir, test_only):
    requests = []
    # Composite descriptors always start with current preview and stable;
    # retained generations must never become separate publication targets.
    current = descriptors[:2] if len(descriptors) > 1 and descriptors[1][0] == 'stable' else descriptors[:1]
    for channel, item in current:
        control = P.local_path(item['control_dir'], inputs.parent, 'client control')
        audit_id = item['audit_release_id']
        if channel == 'stable':
            candidate = item['candidate']
            version = candidate['version']
            source_sha = V.TEST_PRODUCT_SHA if test_only else candidate['source']['source_sha']
            snapshot_sha = candidate['snapshot_sha256']
        else:
            version = item['source']['version']
            source_sha = item['source']['source_sha']
            snapshot_sha = item['snapshot_sha256']
        kwargs = dict(site_root=None, snapshot_path=(None if channel == 'stable' else
                      output_dir / 'archives' / str(audit_id) / 'audit/snapshot.json'),
                      base_url=C.REPOSITORY_ORIGIN, apt_public_cert=control / f'keys/apt-{channel}.asc',
                      rpm_public_cert=control / f'keys/rpm-{channel}.asc', expected_version=version,
                      verify_installed_cli=True, channel=channel)
        if channel == 'stable':
            kwargs.update(transport_inputs=inputs, test_only=test_only,
                          test_product_sha=V.TEST_PRODUCT_SHA if test_only else None)
        requests.append({'channel': channel, 'audit_release_id': audit_id, 'snapshot_sha256': snapshot_sha,
                         'identity': {'version': version, 'commit': source_sha, 'build_source': 'release'},
                         'kwargs': kwargs})
    return requests


def prepare_delivery(*, inputs, expected_plan_sha256, output_dir, resource_intent,
                     activation_control_sha, max_routes, composite=False, test_only=False):
    """Recompute original planner output and extract each bounded archive once.

    Aliases share their source member, not a copied package or destination audit
    identity. The new directory is local preparation only; no API is invoked.
    """
    inputs, output_dir = Path(inputs), Path(output_dir)
    resource = _resource(resource_intent)
    require(activation_control_sha is None or (isinstance(activation_control_sha, str)
            and re.fullmatch('[0-9a-f]{40}', activation_control_sha)), 'activation control SHA is invalid')
    require(not os.path.lexists(output_dir), 'delivery output already exists')
    plan, raw, value = _load_plan(inputs, expected_plan_sha256, composite, test_only)
    descriptors = _descriptors(value, composite)
    boundaries = [inputs]
    for channel, item in descriptors:
        fields = ('archive', 'receipt', 'control_dir') if channel == 'preview' else (
            'archive', 'receipt', 'control_dir', 'plan', 'resolution', 'source_dir', 'apt_receipt', 'rpm_receipt')
        boundaries.extend(P.local_path(item[f], inputs.parent, f) for f in fields
                          if P.local_path(item[f], inputs.parent, f).exists())
    C.validate_candidate_output_paths((output_dir,), tuple(boundaries))
    require(output_dir.parent.is_dir(), 'delivery output parent must already exist')
    with tempfile.TemporaryDirectory(prefix='.delivery-', dir=output_dir.parent) as temporary:
        stage = Path(temporary) / 'prepared'
        stage.mkdir()
        archives = {}
        for channel, item in descriptors:
            audit_id = item['audit_release_id']
            archive = P.local_path(item['archive'], inputs.parent, 'archive')
            facts = ({'sha256': item['archive_sha256'], 'size': item['archive_size']} if channel == 'preview'
                     else {key: item['candidate']['archive'][key] for key in ('sha256', 'size')})
            destination = stage / 'archives' / str(audit_id)
            destination.parent.mkdir(exist_ok=True)
            extracted = P.R.snapshot_archive.extract_snapshot(archive_path=archive, output_dir=destination,
                max_members=P.R.snapshot_archive.DEFAULT_MAX_MEMBERS, max_total_size=P.R.ARCHIVE_MAX_TOTAL_BYTES)
            require({key: extracted[key] for key in ('sha256', 'size')} == facts, 'archive changed during materialization')
            archives[audit_id] = (channel, destination)
        objects = {}

        def add(value, path):
            key = value['object_key']
            _object_key(key)
            require(C.hash_file(path, 'materialized object', maximum_bytes=value['size'])
                    == {field: value[field] for field in ('sha256', 'size')}, 'materialized object bytes differ')
            if key in objects:
                require(_facts(objects[key]) == _facts(value), 'object key has conflicting bytes or cache metadata')
                return
            objects[key] = {**copy.deepcopy(value), 'materialized_file': path.relative_to(stage).as_posix()}

        for value in [*plan['snapshot_objects'], *plan['immutable_aliases']]:
            source = value['source']
            channel, archive_root = archives[source['audit_release_id']]
            require(source.get('channel', 'preview') == channel, 'object source channel differs')
            path = C.candidate_path(archive_root, source['member'], 'object source member')
            add(value, path)
        for value in plan.get('generated_objects', []):
            path = stage / 'generated' / (value['sha256'] + '.txt')
            C.write_exclusive(path, value['content_utf8'].encode('utf-8'), 'generated object')
            add(value, path)
        routes = _route_table(plan, objects, max_routes)
        # Rechecking the original planner also rechecks all supplied source,
        # archive and public control inputs. No persisted success journal exists.
        _, final_raw, _ = _load_plan(inputs, expected_plan_sha256, composite, test_only)
        require(final_raw == raw, 'transport inputs changed during materialization')
        result = {'schema': 'wukongim.object_storage_delivery_preparation/v1', 'mode': 'dry_run',
                  'production_enabled': False, 'publication_verified': False, 'cloud_verified': False,
                  'inputs_sha256': sha256(raw), 'plan_sha256': expected_plan_sha256, 'plan_schema': plan['schema'],
                  'channel': plan['channel'], 'activation_control_sha': activation_control_sha,
                  'resource_intent': resource, 'snapshots': plan['snapshots'],
                  'planner_verification': plan['verification'], 'objects': [objects[key] for key in sorted(objects)],
                  'routes': routes, 'client_requests': _client_requests(descriptors, inputs, output_dir, test_only)}
        _json_write(stage / 'delivery.json', result)
        C.write_exclusive(stage / 'transport-plan.json', C.canonical_json(plan), 'original transport plan')
        os.rename(stage, output_dir)
    return result


def _configuration(value):
    C.exact(value, {'observation_id', 'channels', 'managed_routes', 'unmanaged_routes'}, 'simulated configuration')
    require(isinstance(value['observation_id'], str)
            and re.fullmatch(r'simulated:[1-9][0-9]*', value['observation_id']), 'invalid simulated configuration identity')
    require(value['channels'] in (['preview'], ['preview', 'stable']), 'configuration channels are invalid')
    require(isinstance(value['managed_routes'], list) and isinstance(value['unmanaged_routes'], list),
            'configuration route lists are invalid')
    require(len(canonical(value)) <= MAX_JSON_BYTES, 'configuration observation exceeds JSON limit')
    require(not value['unmanaged_routes'], 'unmanaged configuration cannot be modified by this simulation')
    seen = set()
    for row in value['managed_routes']:
        C.exact(row, {'public_uri', 'object_key', 'sha256', 'size', 'cache_control', 'kind'}, 'managed route')
        require(row['public_uri'] not in seen, 'duplicate managed URI')
        seen.add(row['public_uri'])
    return copy.deepcopy(value)


def _config_diff(before, routes, allow_removals):
    old = {row['public_uri']: row for row in before['managed_routes']}
    new = {row['public_uri']: row for row in routes}
    removed = [old[uri] for uri in sorted(old.keys() - new.keys())]
    require(not removed or allow_removals is True, 'managed route removals require explicit simulation opt-in')
    return {'add': [new[u] for u in sorted(new.keys() - old.keys())], 'remove': removed,
            'replace': [{'before': old[u], 'after': new[u]} for u in sorted(new.keys() & old.keys()) if old[u] != new[u]]}


def _preflight(api, resource):
    observation = api.preflight(copy.deepcopy(resource))
    C.exact(observation, {'simulated', 'resource_sha256', 'versioning', 'fresh'}, 'simulated preflight')
    require(observation['simulated'] is True and observation['fresh'] is True
            and observation['versioning'] == 'never_enabled'
            and observation['resource_sha256'] == sha256(canonical(resource)), 'simulated preflight is missing, stale or incompatible')
    return observation


def _activation_control(observation, expected_sha):
    """Fence independently observed current control, not historical plan controls."""
    C.exact(observation, {'simulated', 'activation_control_sha'}, 'simulated activation control')
    current_sha = observation['activation_control_sha']
    require(observation['simulated'] is True and isinstance(current_sha, str)
            and re.fullmatch('[0-9a-f]{40}', current_sha) and current_sha == expected_sha,
            'simulated current activation control is unknown or differs from expected fence')


def _client_result(value, request, inputs_sha256, plan_sha256):
    """Check the outer binding, retaining the original verifier receipt unchanged."""
    expected_schema = V.STABLE_RECEIPT_SCHEMA if request['channel'] == 'stable' else V.RECEIPT_SCHEMA
    require(value.get('schema') == expected_schema and value.get('expected_version') == request['identity']['version']
            and value.get('expected_version_verified') is True and value.get('installed_cli_verified') is True
            and value.get('bootstrap_verified') is True and value.get('audit_release_id') == request['audit_release_id']
            and value.get('snapshot_sha256') == request['snapshot_sha256'], 'client receipt target or acceptance differs')
    if request['channel'] == 'stable':
        require(value.get('transport_inputs_sha256') == inputs_sha256
                and value.get('transport_plan_sha256') == plan_sha256,
                'stable client receipt transport input or plan digest differs')
    for family, clients in (('apt', V.APT_CLIENTS), ('rpm', V.RPM_CLIENTS)):
        results = value.get(family)
        require(isinstance(results, list) and [r.get('distribution') for r in results] == [c[0] for c in clients],
                'client receipt requires all four distributions')
        for row in results:
            require(row.get('bootstrap_installed') is True
                    and row.get('download', {}).get('snapshot_versions') == [request['identity']['version']]
                    and row.get('installed_cli', {}).get('verified') is True
                    and row['installed_cli'].get('identity') == request['identity'], 'client download or installed CLI identity differs')


def simulate_delivery(*, api, client_verifier, expected_configuration_sha256,
                      allow_managed_removals=False, **prepare_args):
    """Exercise only caller-supplied in-memory seams and record observed effects.

    The module never discovers a provider or executes verifier commands. Every
    invocation prepares and revalidates from scratch; receipts cannot resume or
    authorize a later run. A failed activation can still have observed effects.
    """
    require(getattr(api, 'simulation_only', None) is True, 'only an explicit simulation API is accepted')
    require(callable(client_verifier), 'simulation requires a client verifier seam')
    preparation = prepare_delivery(**prepare_args)
    resource = preparation['resource_intent']
    target_channels = ['preview', 'stable'] if preparation['channel'] == 'composite' else ['preview']
    receipt = {'schema': 'wukongim.object_storage_delivery_simulation/v1', 'mode': 'simulated',
               'status': 'incomplete', 'stage': 'preflight', 'production_enabled': False,
               'publication_verified': False, 'cloud_verified': False, 'github_provenance_verified': False,
               'inputs_sha256': preparation['inputs_sha256'], 'plan_sha256': preparation['plan_sha256'],
               'plan_schema': preparation['plan_schema'], 'activation_control_sha': preparation['activation_control_sha'],
               'activation_control_observations': [],
               'snapshots': preparation['snapshots'], 'resource_intent': resource,
               'objects': [], 'clients': [], 'configuration_diff': None,
               'configuration': {'before': None, 'after': None, 'write_attempted': False,
                                 'observed_change': None, 'rollback_performed': False},
               'refresh': None, 'public': None}
    output_dir = Path(prepare_args['output_dir'])
    try:
        receipt['preflight'] = _preflight(api, resource)
        receipt['activation_control_observations'].append(copy.deepcopy(api.read_activation_control()))
        _activation_control(receipt['activation_control_observations'][-1], preparation['activation_control_sha'])
        before = _configuration(api.read_configuration())
        receipt['configuration']['before'] = before
        require(sha256(canonical(before)) == expected_configuration_sha256, 'current configuration differs from expected fence')
        require(not ('stable' in before['channels'] and 'stable' not in target_channels),
                'cannot replace public stable with preview-only configuration')
        receipt['configuration_diff'] = _config_diff(before, preparation['routes'], allow_managed_removals)
        receipt['stage'] = 'upload'
        for value in preparation['objects']:
            path = C.candidate_path(output_dir, value['materialized_file'], 'upload source')
            require(C.hash_file(path, 'upload source', maximum_bytes=value['size'])
                    == {key: value[key] for key in ('sha256', 'size')}, 'upload source bytes changed')
            observation = {'object_key': value['object_key'], **_facts(value), 'put_outcome': 'unknown',
                           'readback_verified': False}
            receipt['objects'].append(observation)
            try:
                result = api.put_create_only(value['object_key'], path, value['cache_control'])
                require(result == 'created', 'unknown create-only PUT response')
                observation['put_outcome'] = 'created'
            except FileExistsError:
                observation['put_outcome'] = 'exists'
            except TimeoutError:
                pass  # A timeout does not say whether PUT happened: GET decides.
            observed = api.get_object(value['object_key'])
            C.exact(observed, {'body', 'cache_control'}, 'simulated GET')
            body = observed['body']
            require(isinstance(body, bytes) and len(body) == value['size'] and sha256(body) == value['sha256']
                    and observed['cache_control'] == value['cache_control'], 'GET bytes or cache metadata differ')
            observation['readback_verified'] = True
        receipt['stage'] = 'activation_fence'
        _, raw, _ = _load_plan(prepare_args['inputs'], preparation['plan_sha256'],
                               prepare_args.get('composite', False), prepare_args.get('test_only', False))
        require(sha256(raw) == preparation['inputs_sha256'], 'inputs changed before activation')
        _preflight(api, resource)
        require(_configuration(api.read_configuration()) == before, 'configuration drifted before activation')
        receipt['activation_control_observations'].append(copy.deepcopy(api.read_activation_control()))
        _activation_control(receipt['activation_control_observations'][-1], preparation['activation_control_sha'])
        receipt['stage'] = 'activation'
        receipt['configuration']['write_attempted'] = True
        try:
            api.apply_configuration(copy.deepcopy(preparation['routes']), target_channels)
        finally:
            # A failed API call may have applied some routes. Read independently,
            # preserve that observation and never invent a rollback or no-write.
            after = api.read_configuration()
            receipt['configuration']['after'] = copy.deepcopy(after)
            receipt['configuration']['observed_change'] = after != before
        after = _configuration(after)
        require(after['managed_routes'] == preparation['routes'] and after['channels'] == target_channels,
                'configuration readback differs from complete target')
        receipt['stage'] = 'refresh'
        receipt['refresh'] = api.refresh()
        require(receipt['refresh'] == {'simulated': True, 'complete': True}, 'simulated refresh incomplete')
        receipt['stage'] = 'public'
        receipt['public'] = api.check_public(copy.deepcopy(preparation['routes']))
        require(receipt['public'] == {'simulated': True, 'origin_verified': True, 'entrypoints_verified': True},
                'simulated public or origin observation failed')
        receipt['stage'] = 'clients'
        for request in preparation['client_requests']:
            result = client_verifier(copy.deepcopy(request))
            receipt['clients'].append({'channel': request['channel'], 'receipt_sha256': sha256(canonical(result)),
                                       'receipt': copy.deepcopy(result)})
            _client_result(result, request, preparation['inputs_sha256'], preparation['plan_sha256'])
        receipt.update(status='simulated_complete', stage='complete')
    except ERRORS as error:
        receipt['error'] = str(error)[:1024]
    _json_write(output_dir / 'simulation.json', receipt)
    return receipt


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--inputs', required=True, type=Path)
    parser.add_argument('--expected-plan-sha256', required=True)
    parser.add_argument('--output-dir', required=True, type=Path)
    parser.add_argument('--resource-intent', required=True, type=Path)
    parser.add_argument('--activation-control-sha')
    parser.add_argument('--max-routes', required=True, type=int, help='Offline request table capacity, not a provider claim')
    parser.add_argument('--composite', action='store_true')
    parser.add_argument('--test-only', action='store_true')
    args = parser.parse_args(argv)
    try:
        resource = json.loads(_read(args.resource_intent, 'resource intent'), object_pairs_hook=C.reject_duplicate_pairs)
        result = prepare_delivery(inputs=args.inputs, expected_plan_sha256=args.expected_plan_sha256,
            output_dir=args.output_dir, resource_intent=resource, activation_control_sha=args.activation_control_sha,
            max_routes=args.max_routes, composite=args.composite, test_only=args.test_only)
    except ERRORS as error:
        print('offline object delivery failed: ' + str(error), file=sys.stderr)
        return 1
    print(canonical({key: result[key] for key in ('schema', 'mode', 'plan_schema', 'plan_sha256',
        'inputs_sha256', 'production_enabled', 'publication_verified', 'cloud_verified')}).decode(), end='')
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
