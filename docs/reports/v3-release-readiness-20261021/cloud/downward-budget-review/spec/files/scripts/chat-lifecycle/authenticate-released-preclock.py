#!/usr/bin/env python3
"""Authenticate one explicit released pre-clock continuation; never mutate GitHub or cloud."""
import argparse
import base64
import collections
import io
import stat
import zipfile
import datetime as dt
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re
import selectors
import signal
import subprocess
import sys
import time

SCHEMA = 'wukongim.chat_lifecycle.released_preclock_continuation/v1'
RESULT = 'wukongim.chat_lifecycle.released_preclock_authentication/v1'
JSON_LIMIT = 2 * 1024 * 1024
ZIP_LIMIT = 16 * 1024 * 1024
SHA = re.compile(r'^[0-9a-f]{40}$')
IDENTITY = re.compile(r'^[A-Za-z0-9][A-Za-z0-9._-]{0,95}$')
REHEARSAL_PATH = '.github/workflows/chat-lifecycle-rehearsal.yml'


class Rejected(Exception):
    """Only fixed reason codes cross the public diagnostic boundary."""


def require(value, reason):
    if not value:
        raise Rejected(reason)


def natural(value):
    return type(value) is int and value > 0


def strict_json(data, limit=JSON_LIMIT):
    require(len(data) <= limit, 'json_limit')
    def pairs(items):
        result = {}
        for key, value in items:
            require(key not in result, 'duplicate_json_field')
            result[key] = value
        return result
    try:
        return json.loads(data, object_pairs_hook=pairs,
                          parse_constant=lambda _: (_ for _ in ()).throw(Rejected('invalid_json')))
    except (ValueError, UnicodeError) as error:
        raise Rejected('invalid_json') from error


def load(path, limit=JSON_LIMIT):
    with open(path, 'rb') as stream:
        return strict_json(stream.read(limit + 1), limit)


def api(endpoint, limit=JSON_LIMIT, binary=False):
    """Read one exact GitHub endpoint with bounded bytes/time and no raw stderr."""
    child = subprocess.Popen(['gh', 'api', '--hostname', 'github.com', '--method', 'GET', endpoint],
                             stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, start_new_session=True)
    data = bytearray()
    deadline = time.monotonic() + 60
    try:
        with selectors.DefaultSelector() as selected:
            selected.register(child.stdout, selectors.EVENT_READ)
            while selected.get_map():
                require(time.monotonic() < deadline, 'authority_unavailable')
                for key, _ in selected.select(min(1, max(0, deadline - time.monotonic()))):
                    chunk = os.read(key.fd, min(65536, limit + 1 - len(data)))
                    if not chunk:
                        selected.unregister(key.fileobj)
                        break
                    data.extend(chunk)
                    require(len(data) <= limit, 'authority_response_limit')
        require(child.wait(timeout=max(.01, deadline-time.monotonic())) == 0, 'authority_unavailable')
    except subprocess.TimeoutExpired as error:
        raise Rejected('authority_unavailable') from error
    finally:
        try:
            os.killpg(child.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        child.wait()
    return bytes(data) if binary else strict_json(data)


def environment(values=None):
    env = os.environ if values is None else values
    result = {key: env.get(key, '') for key in [
        'GITHUB_REPOSITORY', 'GITHUB_REPOSITORY_ID', 'GITHUB_RUN_ID', 'GITHUB_RUN_NUMBER',
        'GITHUB_RUN_ATTEMPT', 'GITHUB_SHA', 'WK_CHAT_SOURCE_SHA', 'WK_CHAT_REQUEST_ID',
        'WK_CHAT_OPERATOR', 'WK_CHAT_CODEX_DIAGNOSTIC_PUBKEY', 'WK_CHAT_RELEASED_PRECLOCK_RUN_ID']}
    require(env.get('GH_TOKEN') and re.fullmatch(r'[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+', result['GITHUB_REPOSITORY'])
            and IDENTITY.fullmatch(result['WK_CHAT_REQUEST_ID'])
            and SHA.fullmatch(result['GITHUB_SHA']) and SHA.fullmatch(result['WK_CHAT_SOURCE_SHA'])
            and result['WK_CHAT_OPERATOR'] == 'tangtaoit', 'environment_invalid')
    for name in ['GITHUB_REPOSITORY_ID', 'GITHUB_RUN_ID', 'GITHUB_RUN_NUMBER', 'GITHUB_RUN_ATTEMPT']:
        require(re.fullmatch(r'[1-9][0-9]*', result[name]), 'environment_invalid')
        result[name] = int(result[name])
    prior = result['WK_CHAT_RELEASED_PRECLOCK_RUN_ID']
    require(not prior or re.fullmatch(r'[1-9][0-9]*', prior), 'environment_invalid')
    return result


def authenticate_run(run, env, path, terminal=True, conclusion=None):
    repo = {'id': env['GITHUB_REPOSITORY_ID'], 'full_name': env['GITHUB_REPOSITORY']}
    require(isinstance(run, dict) and all(isinstance(run.get(k), dict) and
            all(run[k].get(field) == value for field, value in repo.items())
            for k in ['repository', 'head_repository']), 'producer_identity')
    require(natural(run.get('id')) and natural(run.get('workflow_id')) and natural(run.get('run_number'))
            and run.get('event') == 'workflow_dispatch' and run.get('head_branch') == 'main'
            and run.get('path') in [path, path+'@refs/heads/main']
            and SHA.fullmatch(run.get('head_sha', '')), 'producer_identity')
    require(run.get('run_attempt') == 1, 'rerun_not_supported')
    if terminal:
        require(run.get('status') == 'completed' and run.get('conclusion') in ['success', 'failure'], 'producer_identity')
    if conclusion:
        require(run.get('conclusion') == conclusion, 'producer_identity')


def authenticate_sequence(env):
    repo = env['GITHUB_REPOSITORY']
    prior_id = int(env['WK_CHAT_RELEASED_PRECLOCK_RUN_ID'])
    old = api(f'/repos/{repo}/actions/runs/{prior_id}')
    current = api(f'/repos/{repo}/actions/runs/{env["GITHUB_RUN_ID"]}')
    authenticate_run(old, env, REHEARSAL_PATH, conclusion='failure')
    authenticate_run(current, env, REHEARSAL_PATH, terminal=False)
    require(old['id'] == prior_id and current['id'] == env['GITHUB_RUN_ID']
            and current['run_number'] == env['GITHUB_RUN_NUMBER']
            and current['run_attempt'] == env['GITHUB_RUN_ATTEMPT']
            and current['head_sha'] == env['GITHUB_SHA']
            and current['workflow_id'] == old['workflow_id']
            and current['id'] > old['id']
            and old.get('display_title') == 'Chat Lifecycle Rehearsal '+env['WK_CHAT_REQUEST_ID']
            and current.get('display_title') == old.get('display_title'), 'producer_identity')
    require(current['run_number'] == old['run_number'] + 1, 'nonadjacent_run')
    return old, current


def retained_inventory(path):
    inventory = load(path, 64 * 1024 * 1024)
    require(isinstance(inventory, dict) and inventory.get('complete') is True and
            isinstance(inventory.get('artifacts'), list), 'inventory_incomplete')
    require(len(inventory['artifacts']) <= 20000, 'inventory_incomplete')
    require(all(isinstance(a, dict) for a in inventory['artifacts']), 'inventory_incomplete')
    return inventory['artifacts']


def stop_guard(env, artifacts):
    candidates = [a for a in artifacts if a.get('name') == 'chat-lifecycle-operator-stop-'+env['WK_CHAT_REQUEST_ID'] and a.get('expired') is False]
    require(len(candidates) <= 8, 'stopped_or_stop_unverified')
    for artifact in candidates:
        identifier = artifact.get('workflow_run', {}).get('id')
        require(natural(identifier), 'stopped_or_stop_unverified')
        run = api(f'/repos/{env["GITHUB_REPOSITORY"]}/actions/runs/{identifier}')
        if (run.get('id') == identifier and run.get('repository', {}).get('full_name') == env['GITHUB_REPOSITORY'] and
                run.get('repository', {}).get('id') == env['GITHUB_REPOSITORY_ID'] and
                run.get('head_repository', {}).get('full_name') == env['GITHUB_REPOSITORY'] and
                run.get('head_repository', {}).get('id') == env['GITHUB_REPOSITORY_ID'] and
                run.get('event') == 'workflow_dispatch' and run.get('head_branch') == 'main' and
                run.get('path', '').split('@')[0] == '.github/workflows/chat-lifecycle-stop.yml' and
                run.get('status') in ['in_progress', 'completed']):
            raise Rejected('stopped_or_stop_unverified')


def first_start_guard(env, artifacts):
    # Only retained, complete GitHub inventory is claimed. Deleted history cannot
    # be reconstructed here; this is not a durable request ledger. A trusted
    # finalizer can publish cleanup before it finishes, so status cannot reset prior.
    names = {f'chat-lifecycle-{stage}-{kind}-{env["WK_CHAT_REQUEST_ID"]}'
             for stage in ['rehearsal', 'formal'] for kind in ['cleanup', 'complete']}
    for artifact in artifacts:
        if artifact.get('name') not in names or artifact.get('expired') is not False:
            continue
        run_id = artifact.get('workflow_run', {}).get('id')
        require(natural(run_id), 'inventory_incomplete')
        run = api(f'/repos/{env["GITHUB_REPOSITORY"]}/actions/runs/{run_id}')
        paths = {f'.github/workflows/chat-lifecycle-{stage}{suffix}.yml'
                 for stage in ['rehearsal', 'formal'] for suffix in ['', '-finalize']}
        if (run.get('id') == run_id and run.get('repository', {}).get('id') == env['GITHUB_REPOSITORY_ID']
                and run.get('repository', {}).get('full_name') == env['GITHUB_REPOSITORY']
                and run.get('head_repository', {}).get('id') == env['GITHUB_REPOSITORY_ID']
                and run.get('head_repository', {}).get('full_name') == env['GITHUB_REPOSITORY']
                and run.get('head_branch') == 'main' and run.get('event') in ['workflow_dispatch', 'schedule']
                and run.get('path', '').split('@')[0] in paths
                and SHA.fullmatch(run.get('head_sha', ''))):
            raise Rejected('request_already_spent')


SCOPES = sorted(['disk_attachments', 'disks', 'eip_associations', 'eips', 'enis', 'instances',
                 'nat_gateways', 'route_entries', 'security_group_rules', 'security_groups', 'vpcs', 'vswitches'])
BUDGET = {'currency': 'CNY', 'limit_micros': 1500000000, 'committed_micros': 0,
          'operational_stop_micros': 1350000000}
SELECTED = ['attempt-1/run-plan.json', 'attempt-1/lease-plan.json', 'attempt-1/receipt.json',
            'attempt-1/quote.json', 'release-selector.json', 'zero-inventory.json',
            'cleanup.json', 'deployment-repair-pending.json']


def encoded(value):
    return (json.dumps(value, sort_keys=True, separators=(',', ':'))+'\n').encode()


def sha(data):
    return hashlib.sha256(data).hexdigest()


def instant(value):
    require(isinstance(value, str) and re.fullmatch(r'\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d{1,9})?Z', value), 'evidence_time_invalid')
    return dt.datetime.fromisoformat(value.replace('Z', '+00:00'))


def key_identity(value):
    fields = value.split()
    require(len(fields) >= 2 and len(value) <= 4096 and '\r' not in value and '\n' not in value and fields[0] == 'ssh-ed25519', 'prior_identity_mismatch')
    try:
        raw = base64.b64decode(fields[1], validate=True)
    except ValueError as error:
        raise Rejected('prior_identity_mismatch') from error
    require(len(raw) == 51 and raw[:19] == b'\x00\x00\x00\x0bssh-ed25519\x00\x00\x00\x20', 'prior_identity_mismatch')
    return ' '.join(fields[:2])


def archive(env, artifact, run, name):
    """Authenticate the exact archive then read safe public entries in memory only."""
    require(natural(artifact.get('id')), 'artifact_integrity')
    endpoint = f'/repos/{env["GITHUB_REPOSITORY"]}/actions/artifacts/{artifact["id"]}'
    exact = api(endpoint)
    require(exact == artifact and exact.get('expired') is False and exact.get('name') == name and
            exact.get('workflow_run', {}).get('id') == run['id'] and
            exact.get('workflow_run', {}).get('head_sha') == run['head_sha'] and
            natural(exact.get('size_in_bytes')) and exact['size_in_bytes'] <= ZIP_LIMIT and
            re.fullmatch(r'sha256:[0-9a-f]{64}', exact.get('digest', '')), 'artifact_integrity')
    raw = api(endpoint+'/zip', ZIP_LIMIT, binary=True)
    require(len(raw) == exact['size_in_bytes'] and 'sha256:'+sha(raw) == exact['digest'], 'artifact_integrity')
    try:
        with zipfile.ZipFile(io.BytesIO(raw)) as zipped:
            infos = zipped.infolist()
            require(0 < len(infos) <= 128 and sum(i.file_size for i in infos) <= 64*1024*1024, 'artifact_integrity')
            names = set()
            files = {}
            for item in infos:
                path = PurePosixPath(item.filename)
                require(item.filename and not path.is_absolute() and '\\' not in item.filename and
                        all(part not in ['', '.', '..'] for part in item.filename.rstrip('/').split('/')) and
                        item.filename not in names and not stat.S_ISLNK(item.external_attr >> 16) and
                        not item.flag_bits & 1, 'artifact_integrity')
                names.add(item.filename)
                if not item.is_dir():
                    # Merely seeing terminal, accepted-run or continuation evidence rejects
                    # this original, unmeasured predecessor, regardless of contents.
                    require(path.name not in ['pre-clock-terminal.json', 'handoff.json', 'run-start.json', 'transition.json', 'final.json'] and
                            'continuation' not in path.parts and not any(p.startswith('attempt-') and p != 'attempt-1' for p in path.parts),
                            'measured_or_runtime_predecessor')
                    # Never persist arbitrary artifact content, encrypted access,
                    # raw summaries or journals. Only selected typed JSON enters the sidecar.
                    allowed = set(SELECTED) | {'quote.json', 'receipt.json', 'release.json'}
                    if item.filename in allowed or re.fullmatch(r'attempt-1/pre-clock-generation-1/(summary|diagnostic)\.json', item.filename):
                        require(item.file_size <= JSON_LIMIT, 'artifact_integrity')
                        data = zipped.read(item)
                        strict_json(data)
                        files[item.filename] = data
            return exact, files
    except (zipfile.BadZipFile, RuntimeError, EOFError, NotImplementedError) as error:
        raise Rejected('artifact_integrity') from error


def run_artifact(env, run, name):
    page = api(f'/repos/{env["GITHUB_REPOSITORY"]}/actions/runs/{run["id"]}/artifacts?per_page=100&page=1')
    require(type(page.get('total_count')) is int and 0 <= page['total_count'] <= 100 and
            isinstance(page.get('artifacts'), list) and len(page['artifacts']) == page['total_count'], 'inventory_incomplete')
    matches = [a for a in page['artifacts'] if a.get('name') == name and a.get('expired') is False]
    require(len(matches) == 1, 'prior_evidence_missing')
    return archive(env, matches[0], run, name)


def public_child(env, identifier, kind, old):
    require(natural(identifier) and identifier != old['id'], 'child_producer_identity')
    child = api(f'/repos/{env["GITHUB_REPOSITORY"]}/actions/runs/{identifier}')
    try:
        authenticate_run(child, env, f'.github/workflows/cloud-lease-{kind}.yml', conclusion='success')
    except Rejected as error:
        raise Rejected('child_producer_identity') from error
    require(child['id'] == identifier and child['head_sha'] == old['head_sha'] and
            child.get('display_title') == f'Cloud Lease {"Provision" if kind == "provision" else "Release"} {env["WK_CHAT_REQUEST_ID"]}' and
            instant(old['created_at']) <= instant(child['created_at']) <= instant(child['updated_at']) <= instant(old['updated_at']),
            'child_producer_identity')
    return child


def plan_digest(plan):
    # The narrowly supported original profile has fully materialized fields.
    # Reproduce Go struct field order, with only map-valued Tags sorted; do not
    # claim to normalize arbitrary provider Plans or future schema versions.
    def ordered(value, keys):
        require(isinstance(value, dict) and set(value) == set(keys.split()), 'prior_identity_mismatch')
        return {k: value[k] for k in keys.split()}
    p = ordered(plan, 'schema lease_id request_id provider region repository operator expires_at budget provenance network placement host_groups tags')
    p['budget'] = ordered(p['budget'], 'currency limit_micros committed_micros operational_stop_micros')
    p['provenance'] = ordered(p['provenance'], 'source_sha bundle_digest')
    p['network'] = ordered(p['network'], 'isolated single_zone initial_access conservative_public_egress_bytes')
    p['network']['initial_access'] = [ordered(g, 'id target_role protocol port_from port_to source_prefix until') for g in p['network']['initial_access']]
    require(p['placement'] == {}, 'predecessor_not_original_rehearsal')
    groups = []
    for group in p['host_groups']:
        keys = 'role count compute system_disk data_disks public_ipv4 internet_egress'
        if group.get('public_ipv4') is True:
            keys += ' peak_bandwidth_mbps'
        g = ordered(group, keys)
        g['compute'] = ordered(g['compute'], 'vcpus memory_bytes architecture billing_model allow_burstable')
        disk_keys = 'role count_per_host size_bytes class performance_level'
        g['system_disk'] = ordered(g['system_disk'], disk_keys)
        g['data_disks'] = [ordered(d, disk_keys) for d in g['data_disks']]
        groups.append(g)
    p['host_groups'] = groups
    p['tags'] = dict(sorted(p['tags'].items()))
    return sha(json.dumps(p, separators=(',', ':'), ensure_ascii=False).encode())


def validate_prior_documents(env, docs, old, acquire, release, release_step):
    run = docs['attempt-1/run-plan.json']
    plan = docs['attempt-1/lease-plan.json']
    receipt = docs['attempt-1/receipt.json']['receipt']
    quote = docs['attempt-1/quote.json']['quote']
    selector = docs['release-selector.json']['selector']
    cleanup = docs['cleanup.json']
    pending = docs['deployment-repair-pending.json']
    result = docs['zero-inventory.json'].get('result', {})
    zero = result.get('zero_inventory', {})
    schemas = {'attempt-1/lease-plan.json':'wukongim.cloud_lease/v1',
               'attempt-1/receipt.json':'wukongim.cloud_lease.receipt/v1',
               'attempt-1/quote.json':'wukongim.cloud_lease.quote/v1',
               'release-selector.json':'wukongim.cloud_lease.selector/v1',
               'zero-inventory.json':'wukongim.cloud_lease.release/v1'}
    require(all(docs[n].get('schema') == value for n,value in schemas.items()), 'prior_identity_mismatch')
    lease = env['WK_CHAT_REQUEST_ID']+'-rehearsal-1'
    require(run.get('schema') == 'wukongim.chat_lifecycle.run_plan/v1' and run.get('stage') == 'rehearsal' and
            run.get('attempt') == 1 and run.get('workload_duration_seconds') == 7200 and
            run.get('readiness_timeout_seconds') == 7200 and run.get('operational_stop_micros') == 1350000000 and
            run.get('template_schema') == 'wukongim.chat_lifecycle.run_plan_template/v1' and plan.get('budget') == BUDGET and receipt.get('budget') == BUDGET and
            run.get('lease_plan') == plan and plan.get('lease_id') == lease, 'predecessor_not_original_rehearsal')
    digest = plan_digest(plan)
    expected_selector = {'lease_id': lease, 'request_id': env['WK_CHAT_REQUEST_ID'], 'provider': 'alibaba',
                         'region': 'cn-hangzhou', 'repository': env['GITHUB_REPOSITORY'], 'plan_digest': digest}
    require(selector == expected_selector and all(plan.get(k) == v for k, v in expected_selector.items() if k != 'plan_digest') and
            plan.get('operator') == env['WK_CHAT_OPERATOR'] and plan['provenance']['source_sha'] == env['WK_CHAT_SOURCE_SHA'] and
            all(receipt.get(k) == v for k, v in expected_selector.items()) and receipt.get('provenance') == plan['provenance'] and
            receipt.get('operator') == plan['operator'] and receipt.get('expires_at') == plan['expires_at'] and
            receipt.get('quote') == quote and all(quote.get(k) == receipt.get(k) for k in
                ['lease_id', 'request_id', 'provider', 'region', 'zone', 'account_id_hash', 'plan_digest']), 'prior_identity_mismatch')
    keys = run.get('bootstrap_access', {}).get('authorized_keys', [])
    require(len(keys) == 2 and len({key_identity(k) for k in keys}) == 2 and
            key_identity(env['WK_CHAT_CODEX_DIAGNOSTIC_PUBKEY']) in {key_identity(k) for k in keys}, 'prior_identity_mismatch')
    bootstrap = 'sha256:'+sha('\n'.join(sorted(key_identity(k) for k in keys)).encode())
    require(receipt.get('tags', {}).get('wukongim-bootstrap-access-digest') == bootstrap and
            all(r.get('tags', {}).get('wukongim-bootstrap-access-digest') == bootstrap for r in receipt.get('resources', [])), 'prior_identity_mismatch')
    require(pending.get('schema') == 'wukongim.chat_lifecycle.rehearsal_deployment_repair/v1' and
            pending.get('request_id') == env['WK_CHAT_REQUEST_ID'] and pending.get('source_sha') == env['WK_CHAT_SOURCE_SHA'] and
            pending.get('control_sha') == old['head_sha'] and pending.get('bundle_digest') == plan['provenance']['bundle_digest'] and
            pending.get('lease_id') == lease and pending.get('generation') == 1 and
            pending.get('last_successful_gate') == 'deployment_receipt' and pending.get('failure_code') in
            ['stage_readiness_timeout', 'stage_start_failed', 'stage_terminal', 'stage_unreachable'], 'measured_or_runtime_predecessor')
    require(cleanup.get('schema') == 'wukongim.chat_lifecycle.rehearsal_cleanup/v1' and
            cleanup.get('request_id') == env['WK_CHAT_REQUEST_ID'] and cleanup.get('zero_inventory') is True and
            cleanup.get('release_run_id') == release['id'] and pending.get('acquire_run_id') == acquire['id'], 'prior_identity_mismatch')
    require(set(result) == {'zero_inventory'} and set(zero) == {'selector', 'account_id_hash', 'observed_at', 'scopes'} and
            zero.get('selector') == selector and zero.get('account_id_hash') == receipt.get('account_id_hash') and
            isinstance(zero.get('scopes'), list) and sorted(zero['scopes']) == SCOPES, 'zero_incomplete')
    created, observed = instant(receipt['created_at']), instant(zero['observed_at'])
    require(instant(acquire['created_at']) <= created <= instant(acquire['updated_at']) and
            created <= instant(release['created_at']) <= instant(release_step['started_at']) <= observed < instant(release_step['completed_at']) + dt.timedelta(seconds=1) and
            instant(release_step['completed_at']) <= instant(release['updated_at']) and
            observed <= dt.datetime.now(dt.timezone.utc), 'zero_time_invalid')
    cost = validate_cost(plan, receipt, quote, observed)
    return receipt, zero, cost


def validate_cost(plan, receipt, quote, observed):
    fail = 'cost_coverage_unproven'
    selection = quote.get('selection', {})
    quoted, created = instant(quote['quoted_at']), instant(receipt['created_at'])
    require(quoted <= created <= instant(quote['valid_until']) and created <= observed <= instant(plan['expires_at']) and
            quote.get('capacity_available') is True and quote.get('quota_available') is True and quote.get('currency') == 'CNY' and
            observed <= instant(selection.get('eip_billing_evidence_valid_until')), fail)
    require(selection.get('lease_hours') == '12' and selection.get('peak_bandwidth_mbps') == '20' and
            selection.get('internet_charge_type') == 'PayByTraffic' and selection.get('billing_model') == 'PostPaid' and
            selection.get('architecture') == 'x86_64' and selection.get('conservative_public_egress_gib') == '11' and
            selection.get('eip_retention_risk_unit_micros') == '1000000' and
            selection.get('eip_retention_fee') == 'full_lease_plus_cleanup_risk_allowance;direct_ecs_waiver_expected_quota_lte_2000', fail)
    # Earliest Quote precedes Acquire. Even if charged before receipt.created_at,
    # all four hosts and the 20 Mbps EIP are covered through observed zero.
    seconds = (observed - quoted).total_seconds()
    require(0 < seconds <= 12*3600 and seconds*20_000_000/8 <= 11*(1<<30) and seconds <= 16*3600, fail)
    items = quote.get('line_items', [])
    expected = {('postpaid_host_hour', 'service'):36, ('postpaid_host_hour', 'load'):12,
                ('eip_public_egress_gib', 'load'):11, ('eip_retention_policy_risk_hour', 'load'):16}
    require(len(items) == len(expected) and len({(i.get('kind'), i.get('role')) for i in items}) == len(items), fail)
    for item in items:
        require(expected.get((item.get('kind'), item.get('role'))) == item.get('quantity') and natural(item.get('cost_micros')), fail)
        if item['kind'] == 'eip_retention_policy_risk_hour':
            require(item['cost_micros'] == 16*1_000_000, fail)
    cost = sum(i['cost_micros'] for i in items)
    require(type(quote.get('estimated_cost_micros')) is int and cost == quote['estimated_cost_micros'] and 0 < cost < 1350000000, fail)
    groups = plan.get('host_groups', [])
    require(len(groups) == 2 and {g.get('role') for g in groups} == {'service', 'load'}, fail)
    for g in groups:
        role = g['role']; count = 3 if role == 'service' else 1; data_gib = 500 if role == 'service' else 200
        require(g['count'] == count and g['compute'] == {'vcpus':4, 'memory_bytes':8<<30, 'architecture':'x86_64', 'billing_model':'postpaid', 'allow_burstable':False} and
                g['system_disk'] == {'role':'system','count_per_host':1,'size_bytes':40<<30,'class':'essd','performance_level':'PL0'} and
                g['data_disks'] == [{'role':'data','count_per_host':1,'size_bytes':data_gib<<30,'class':'essd','performance_level':'PL0'}] and
                g['public_ipv4'] == (role == 'load') and g['internet_egress'] == (role == 'load') and
                (role != 'load' or g['peak_bandwidth_mbps'] == 20) and
                selection.get(role+'_system_disk') == 'cloud_essd:PL0:40GiB' and selection.get(role+'_data_disk') == f'cloud_essd:PL0:{data_gib}GiB', fail)
    resources = receipt.get('resources', [])
    counts = collections.Counter(r.get('kind') for r in resources)
    require(counts == {'disk':8,'disk-attachment':4,'eip':1,'eip-association':1,'eni':4,'instance':4,'security-group':1,'security-rule':3,'vpc':1,'vswitch':1} and
            len({r.get('id') for r in resources}) == 28 and all(isinstance(r.get('id'), str) and r['id'] for r in resources), fail)
    ids = {r['id'] for r in resources}
    for resource in resources:
        tags = resource.get('tags', {}); kind = resource['kind']; role = resource.get('role'); attrs = resource.get('attributes', {})
        require(all(tags.get('wukongim-'+name) == receipt.get(field) for name, field in
                    [('lease-id','lease_id'),('request-id','request_id'),('source-sha','source_sha')]
                    if field != 'source_sha') and tags.get('wukongim-source-sha') == plan['provenance']['source_sha'] and
                tags.get('wukongim-lease-plan-digest') == receipt['plan_digest'] and
                tags.get('wukongim-alibaba-account-hash') == receipt['account_id_hash'] and
                tags.get('wukongim-alibaba-quote-cost-micros') == str(cost) and
                (not resource.get('parent_id') or resource['parent_id'] in ids), fail)
        if kind == 'instance':
            require(role in ['service','load'] and attrs.get('instance_type') == selection.get(role+'_instance_type') and
                    attrs.get('image_id') == selection.get('image_id') and resource.get('billable') is True, fail)
        elif kind == 'disk':
            require(role in ['service','load'] and attrs.get('category') == 'cloud_essd' and attrs.get('performance_level') == 'PL0' and
                    attrs.get('disk_type') in ['system','data'] and resource.get('size_bytes') ==
                    ((40 if attrs['disk_type'] == 'system' else (500 if role == 'service' else 200))<<30) and resource.get('billable') is True, fail)
    require(collections.Counter(r.get('role') for r in resources if r['kind'] == 'instance') == {'service':3,'load':1} and
            collections.Counter((r.get('role'),r.get('attributes',{}).get('disk_type')) for r in resources if r['kind'] == 'disk') ==
            {('service','system'):3,('service','data'):3,('load','system'):1,('load','data'):1}, fail)
    return cost


def authenticate_evidence(env, old, current, artifacts):
    ancestry = api(f'/repos/{env["GITHUB_REPOSITORY"]}/compare/{old["head_sha"]}...{current["head_sha"]}')
    require(ancestry.get('status') in ['ahead', 'identical'] and
            ancestry.get('base_commit', {}).get('sha') == old['head_sha'] and
            ancestry.get('merge_base_commit', {}).get('sha') == old['head_sha'], 'producer_identity')
    candidates = [a for a in artifacts if a.get('name') == 'chat-lifecycle-rehearsal-cleanup-'+env['WK_CHAT_REQUEST_ID'] and
                  a.get('workflow_run', {}).get('id') == old['id'] and a.get('expired') is False]
    require(len(candidates) == 1, 'prior_evidence_missing')
    top_meta, top_files = archive(env, candidates[0], old, candidates[0]['name'])
    require(all(n in top_files for n in SELECTED), 'prior_evidence_missing')
    docs = {n: strict_json(data) for n, data in top_files.items()}
    for name, document in docs.items():
        if '/pre-clock-generation-1/' not in name:
            continue
        summary = document.get('summary', {}) if name.endswith('/diagnostic.json') else document
        require(isinstance(summary, dict), 'evidence_invalid')
        if summary.get('status') == 'parsed' and summary.get('syntax_valid') is True:
            require(summary.get('coordinator_code') == 'preflight', 'measured_or_runtime_predecessor')

    pending = docs['deployment-repair-pending.json']; cleanup = docs['cleanup.json']
    acquire = public_child(env, pending.get('acquire_run_id'), 'provision', old)
    release = public_child(env, cleanup.get('release_run_id'), 'release', old)
    require(acquire['id'] != release['id'] and current['id'] not in [acquire['id'], release['id']], 'child_producer_identity')
    acquire_meta, acquire_files = run_artifact(env, acquire, 'cloud-lease-provision-'+env['WK_CHAT_REQUEST_ID'])
    release_meta, release_files = run_artifact(env, release, 'cloud-lease-release-'+env['WK_CHAT_REQUEST_ID'])
    require(set(acquire_files) == {'quote.json','receipt.json'} and set(release_files) == {'release.json'} and
            strict_json(acquire_files['quote.json']) == docs['attempt-1/quote.json'] and
            strict_json(acquire_files['receipt.json']) == docs['attempt-1/receipt.json'] and
            strict_json(release_files['release.json']) == docs['zero-inventory.json'], 'child_artifact_mismatch')
    jobs = api(f'/repos/{env["GITHUB_REPOSITORY"]}/actions/runs/{release["id"]}/jobs?per_page=100&page=1')
    require(isinstance(jobs.get('jobs'), list) and len(jobs['jobs']) == jobs.get('total_count') and len(jobs['jobs']) <= 100, 'authority_unavailable')
    steps = [step for job in jobs['jobs'] if job.get('run_id') == release['id'] and job.get('run_attempt') == 1 and job.get('head_sha') == release['head_sha']
             for step in job.get('steps', []) if step.get('name') == 'Release and retain residual evidence']
    require(len(steps) == 1 and steps[0].get('status') == 'completed' and steps[0].get('conclusion') == 'success', 'child_producer_identity')
    receipt, zero, cost = validate_prior_documents(env, docs, old, acquire, release, steps[0])
    files = {'predecessor-run.json':encoded(old), 'successor-run.json':encoded(current),
             'acquire-run.json':encoded(acquire), 'release-run.json':encoded(release),
             'release-jobs.json':encoded(jobs), 'control-ancestry.json':encoded(ancestry),
             'artifacts.json':encoded({'top':top_meta,'acquire':acquire_meta,'release':release_meta})}
    files.update({'original/'+n:data for n,data in top_files.items()})
    files.update({'acquire/'+n:b for n,b in acquire_files.items()})
    files.update({'release/'+n:b for n,b in release_files.items()})
    manifest = {'schema':'wukongim.chat_lifecycle.released_preclock_evidence/v1',
                'files':{n:{'sha256':sha(b),'size':len(b)} for n,b in sorted(files.items())}}
    proof = {'schema':SCHEMA, 'repository':env['GITHUB_REPOSITORY'], 'repository_id':env['GITHUB_REPOSITORY_ID'],
        'request_id':env['WK_CHAT_REQUEST_ID'], 'source_sha':env['WK_CHAT_SOURCE_SHA'], 'operator':env['WK_CHAT_OPERATOR'],
        'codex_diagnostic_public_key':key_identity(env['WK_CHAT_CODEX_DIAGNOSTIC_PUBKEY']),
        'predecessor_run_id':old['id'], 'predecessor_workflow_id':old['workflow_id'], 'predecessor_run_number':old['run_number'],
        'predecessor_run_attempt':1,'predecessor_control_sha':old['head_sha'], 'predecessor_lease_id':receipt['lease_id'],
        'predecessor_plan_digest':receipt['plan_digest'], 'predecessor_created_at':receipt['created_at'],
        'predecessor_zero_observed_at':zero['observed_at'], 'acquire_run_id':acquire['id'], 'release_run_id':release['id'],
        'current_run_id':current['id'],'current_workflow_id':current['workflow_id'],'current_run_number':current['run_number'],
        'current_run_attempt':1,'control_sha':current['head_sha'],'next_attempt':2,'committed_micros':cost,
        'hard_limit_micros':1500000000,'operational_stop_micros':1350000000,
        'failure_class':'unclassified_before_accepted_run_start','cost_method':'full_admitted_quote_reserve',
        'evidence_manifest_sha256':sha(encoded(manifest))}
    return proof, manifest, files


def write_sidecar(directory, proof, manifest, files):
    require(not directory.exists(), 'output_exists')
    directory.mkdir(mode=0o700, parents=True)
    for name, data in {'proof.json':encoded(proof), 'evidence-manifest.json':encoded(manifest),
                       **{'evidence/'+n:b for n,b in files.items()}}.items():
        path = directory/name
        path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
        with open(path, 'xb') as stream:
            os.chmod(path, 0o600)
            stream.write(data)


def request_budget_input(raw, predecessor=''):
    """Lower a new immutable request ceiling; never broaden continuation authority."""
    require(isinstance(raw, str) and re.fullmatch(r'[1-9][0-9]{0,9}', raw), 'request_budget_invalid')
    limit = int(raw)
    require(150000000 < limit <= 1500000000 and
            (not predecessor or limit == 1500000000), 'request_budget_invalid')
    return {'limit_micros': limit, 'operational_stop_micros': limit-150000000}


def plan_budget(plan):
    """Check local budget consistency only; producer authenticity belongs to callers."""
    reason = 'request_budget_identity'
    lease = plan.get('lease_plan', {})
    budget = lease.get('budget', {})
    require(plan.get('schema') == 'wukongim.chat_lifecycle.run_plan/v1' and
            plan.get('stage') in ['rehearsal', 'formal'] and type(plan.get('attempt')) is int and
            plan['attempt'] in ([1, 2] if plan['stage'] == 'rehearsal' else [1]) and
            isinstance(budget, dict) and set(budget) == set(BUDGET) and budget.get('currency') == 'CNY', reason)
    limit, stop, prior = (budget.get(k) for k in ['limit_micros', 'operational_stop_micros', 'committed_micros'])
    require(all(type(v) is int for v in [limit, stop, prior, plan.get('operational_stop_micros')]) and
            150000000 < limit <= 1500000000 and stop == limit-150000000 and 0 <= prior < stop and
            plan['operational_stop_micros'] == stop and
            (plan['attempt'] == 1 or limit == 1500000000), reason)
    return budget


def read_budget(arguments):
    plan = load(arguments.run_plan)
    budget = plan_budget(plan)
    lease = plan['lease_plan']
    receipt_document = load(arguments.receipt)
    receipt = receipt_document.get('receipt', {})
    require(receipt_document.get('schema') == 'wukongim.cloud_lease.receipt/v1' and
            isinstance(receipt.get('budget'), dict) and set(receipt['budget']) == set(budget) and
            all(type(receipt['budget'].get(k)) is type(v) and receipt['budget'][k] == v for k, v in budget.items()) and
            all(isinstance(lease.get(k), str) and lease[k] and receipt.get(k) == lease[k]
                for k in ['request_id', 'lease_id']), 'request_budget_identity')
    if arguments.deployment_plan:
        deployment = load(arguments.deployment_plan)
        actual = deployment.get('budget', {})
        require(deployment.get('schema') == 'wukongim.cloud_deployment.plan/v2' and
                all(deployment.get(k) == lease[k] for k in ['request_id', 'lease_id']) and
                all(type(actual.get(k)) is type(v) and actual[k] == v for k, v in budget.items()),
                'request_budget_identity')
    if arguments.matches_run_plan:
        original = load(arguments.matches_run_plan)
        expected = plan_budget(original)
        require(original.get('stage') == 'rehearsal' and
                original['lease_plan'].get('request_id') == lease['request_id'] and
                all(budget[k] == expected[k] for k in ['currency', 'limit_micros', 'operational_stop_micros']),
                'request_budget_identity')
    print(json.dumps({k: budget[k] for k in ['limit_micros', 'operational_stop_micros']}))


def prepare(arguments):
    env = environment()
    request_budget_input(os.environ.get('WK_CHAT_REQUEST_BUDGET_LIMIT_MICROS', '1500000000'),
                         env['WK_CHAT_RELEASED_PRECLOCK_RUN_ID'])
    if env['WK_CHAT_RELEASED_PRECLOCK_RUN_ID']:
        old, current = authenticate_sequence(env)
        artifacts = retained_inventory(arguments.inventory)
        stop_guard(env, artifacts)
        proof, manifest, files = authenticate_evidence(env, old, current, artifacts)
        write_sidecar(Path(arguments.output), proof, manifest, files)
        print(json.dumps({'schema':RESULT,'status':'ready','attempt':2,'committed_micros':proof['committed_micros']}, separators=(',', ':')))
        return
    artifacts = retained_inventory(arguments.inventory)
    stop_guard(env, artifacts)
    first_start_guard(env, artifacts)
    print(json.dumps({'schema': RESULT, 'status': 'first_start', 'attempt': 1,
                      'committed_micros': 0}, separators=(',', ':')))


def validate(arguments):
    plan = load(arguments.run_plan)
    lease = plan.get('lease_plan', {})
    require(plan.get('schema') == 'wukongim.chat_lifecycle.run_plan/v1' and plan.get('stage') == 'rehearsal', 'sidecar_identity')
    budget = plan_budget(plan)
    prior = budget['committed_micros']
    directory = Path(arguments.directory)/'continuation'
    if prior == 0 and plan.get('attempt') == 1:
        require(not directory.exists(), 'sidecar_identity')
        print(json.dumps({'schema':RESULT,'status':'first_start','attempt':1,'committed_micros':0}))
        return
    require(directory.is_dir() and not directory.is_symlink() and (directory/'proof.json').is_file(), 'sidecar_missing')
    proof = load(directory/'proof.json')
    require(proof.get('schema') == SCHEMA and plan.get('stage') == 'rehearsal' and plan.get('attempt') == 2 and
            lease.get('lease_id') == proof.get('request_id', '')+'-rehearsal-2' and
            lease.get('request_id') == proof.get('request_id') and lease.get('repository') == proof.get('repository') and
            lease.get('operator') == proof.get('operator') and lease.get('provenance', {}).get('source_sha') == proof.get('source_sha') and
            lease.get('budget') == {**BUDGET,'committed_micros':proof.get('committed_micros')} and
            key_identity(proof.get('codex_diagnostic_public_key', '')) in
                {key_identity(k) for k in plan.get('bootstrap_access', {}).get('authorized_keys', [])} and
            proof.get('repository') == os.environ.get('GITHUB_REPOSITORY') and
            str(proof.get('repository_id')) == os.environ.get('GITHUB_REPOSITORY_ID'), 'sidecar_identity')
    with open(directory/'evidence-manifest.json', 'rb') as stream:
        manifest_raw = stream.read(JSON_LIMIT+1)
    manifest = strict_json(manifest_raw)
    require(sha(manifest_raw) == proof.get('evidence_manifest_sha256') and
            manifest.get('schema') == 'wukongim.chat_lifecycle.released_preclock_evidence/v1' and
            isinstance(manifest.get('files'), dict) and 1 <= len(manifest['files']) <= 128, 'sidecar_integrity')
    raw = {}
    for name, record in manifest['files'].items():
        path = PurePosixPath(name)
        require(not path.is_absolute() and all(p not in ['', '.', '..'] for p in name.split('/')) and
                re.fullmatch(r'[A-Za-z0-9_./-]+',name), 'sidecar_integrity')
        target = directory/'evidence'/name
        require(not any((directory/part).is_symlink() for part in [Path('evidence')/Path(name), *(Path('evidence')/Path(name)).parents] if str(part) != '.') and
                type(record.get('size')) is int and 0 <= record['size'] <= JSON_LIMIT, 'sidecar_integrity')
        with open(target,'rb') as stream:
            data = stream.read(JSON_LIMIT+1)
        require(len(data) == record['size'] and sha(data) == record.get('sha256'), 'sidecar_integrity')
        raw[name] = data
    expected_paths = {'proof.json','evidence-manifest.json'} | {'evidence/'+n for n in raw}
    actual_paths = {str(p.relative_to(directory)) for p in directory.rglob('*') if not p.is_dir()}
    require(actual_paths == expected_paths, 'sidecar_integrity')
    values = {**os.environ, 'GITHUB_REPOSITORY':proof['repository'], 'GITHUB_REPOSITORY_ID':str(proof['repository_id']),
              'GITHUB_RUN_ID':str(proof['current_run_id']), 'GITHUB_RUN_NUMBER':str(proof['current_run_number']),
              'GITHUB_RUN_ATTEMPT':str(proof['current_run_attempt']), 'GITHUB_SHA':proof['control_sha'],
              'WK_CHAT_SOURCE_SHA':proof['source_sha'],'WK_CHAT_REQUEST_ID':proof['request_id'],
              'WK_CHAT_OPERATOR':proof['operator'],'WK_CHAT_CODEX_DIAGNOSTIC_PUBKEY':proof['codex_diagnostic_public_key'],
              'WK_CHAT_RELEASED_PRECLOCK_RUN_ID':str(proof['predecessor_run_id'])}
    env = environment(values)
    old, current = authenticate_sequence(env)
    page = api(f'/repos/{env["GITHUB_REPOSITORY"]}/actions/runs/{old["id"]}/artifacts?per_page=100&page=1')
    require(isinstance(page.get('artifacts'), list) and len(page['artifacts']) == page.get('total_count') and len(page['artifacts']) <= 100, 'inventory_incomplete')
    expected, _, fresh = authenticate_evidence(env, old, current, page['artifacts'])
    require(set(fresh) == set(raw), 'sidecar_integrity')
    for name in fresh:
        if name == 'successor-run.json':
            saved, live = strict_json(raw[name]), strict_json(fresh[name])
            # Status and updated_at naturally advance after handoff. Bind the
            # immutable producer identity, not the scheduled consumer's run ID.
            fields = ['id','workflow_id','run_number','run_attempt','path','head_sha','head_branch',
                      'event','display_title','repository','head_repository','created_at']
            require(all(saved.get(k) == live.get(k) for k in fields), 'sidecar_integrity')
        else:
            require(strict_json(raw[name]) == strict_json(fresh[name]), 'sidecar_integrity')
    expected['evidence_manifest_sha256'] = sha(manifest_raw)
    require(proof == expected, 'sidecar_identity')
    print(json.dumps({'schema':RESULT,'status':'ready','attempt':2,'committed_micros':expected['committed_micros']}))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest='mode', required=True)
    command = sub.add_parser('prepare')
    command.add_argument('--inventory', required=True)
    command.add_argument('--output', required=True)
    command = sub.add_parser('validate')
    command.add_argument('--directory', required=True)
    command.add_argument('--run-plan', required=True)
    command = sub.add_parser('budget-input')
    command.add_argument('--limit', required=True)
    command.add_argument('--released-preclock-run-id', default='')
    command = sub.add_parser('budget')
    command.add_argument('--run-plan', required=True)
    command.add_argument('--receipt', required=True)
    command.add_argument('--deployment-plan')
    command.add_argument('--matches-run-plan')
    args = parser.parse_args()
    try:
        if args.mode == 'budget-input':
            print(json.dumps(request_budget_input(args.limit, args.released_preclock_run_id)))
        else:
            {'prepare': prepare, 'validate': validate, 'budget': read_budget}[args.mode](args)
    except Rejected as error:
        print(json.dumps({'schema': RESULT, 'status': 'rejected', 'reason': str(error)}, separators=(',', ':')))
        return 1
    except (OSError, KeyError, TypeError, ValueError, AttributeError, IndexError, OverflowError):
        print(json.dumps({'schema': RESULT, 'status': 'rejected', 'reason': 'evidence_invalid'}, separators=(',', ':')))
        return 1
    return 0


if __name__ == '__main__':
    sys.exit(main())
