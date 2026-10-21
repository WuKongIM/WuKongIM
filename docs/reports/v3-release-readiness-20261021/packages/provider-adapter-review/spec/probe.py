"""Independent offline spec probes against frozen source; fake credentials only."""
import copy
import hashlib
import importlib.util
import io
import json
import logging
from pathlib import Path
import socket
from unittest import mock

ROOT = Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/packages-object-provider')
HERE = Path(__file__).resolve().parent

def load(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    value = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(value)
    return value

p = load('review_provider', ROOT/'scripts/aliyun-object-storage-provider.py')
t = load('review_wire', ROOT/'tests/provider_sdk/test_aliyun_object_storage_provider.py')

def client(wire):
    return p.Provider(resource=t.RESOURCE, access_key_id='FIXTURE-AK',
                      access_key_secret=t.SECRET, security_token=t.TOKEN, http_adapter=wire)

results = {'network_allowed': False, 'source_sha256': hashlib.sha256((ROOT/'scripts/aliyun-object-storage-provider.py').read_bytes()).hexdigest()}
with mock.patch.object(socket.socket, 'connect', side_effect=AssertionError('provider network forbidden')):
    # A correct observation with an unmanaged rule must reject before a write.
    w = t.Wire()
    w.configs.append(t.config(p.FUNCTION, {'source_url': r'^/old\z', 'target_url': '/public/old', 'flag': 'break', 'sequence': '1'}, 90))
    c = client(w)
    before = c.read_configuration()
    try:
        c.apply_rewrites([t.route()], before=before, managed_ids=[])
        control_rejected = False
    except p.ProviderError:
        control_rejected = True
    control_writes = sum(r[2] == 'BatchSetCdnDomainConfig' for r in w.requests)
    # Corrupt only the observed content, retaining the still-current digest.
    changed = copy.deepcopy(before)
    changed['configs'] = [x for x in changed['configs'] if x['FunctionName'] != p.FUNCTION]
    w.requests.clear()
    observed = c.apply_rewrites([t.route()], before=changed, managed_ids=[])
    results['tampered_before'] = dict(control_rejected=control_rejected, control_writes=control_writes,
        before_digest_still_fresh=before['sha256'] == changed['sha256'],
        tampered_body_digest_differs=p.digest({k: changed[k] for k in ('domain','detail','configs')}) != changed['sha256'],
        actual_write_calls=sum(r[2] == 'BatchSetCdnDomainConfig' for r in w.requests), receipt=observed)
    c.close()

    # SDK request signing precedes the transport-level debug guard.
    w = t.Wire()
    c = client(w)
    logger = logging.getLogger('aliyunsdkcore.client')
    old_level, old_propagate = logger.level, logger.propagate
    stream = io.StringIO()
    handler = logging.StreamHandler(stream)
    logger.addHandler(handler)
    logger.setLevel(logging.DEBUG)
    logger.propagate = False
    try:
        try:
            c.observe_resource()
            rejected = False
        except p.ProviderError:
            rejected = True
        raw = stream.getvalue()
        results['debug_guard'] = dict(rejected=rejected, network_adapter_calls=len(w.requests),
            fake_sts_token_logged=t.TOKEN in raw, log_sha256=hashlib.sha256(raw.encode()).hexdigest())
        # Retain only TEST ONLY log data, with the fake token replaced for clarity.
        (HERE/'debug-log.txt').write_text(raw.replace(t.TOKEN, '<TEST-ONLY-STS-TOKEN>'))
    finally:
        logger.removeHandler(handler)
        logger.setLevel(old_level)
        logger.propagate = old_propagate
        c.close()

(HERE/'probe.json').write_text(json.dumps(results, indent=2)+'\n')
print(json.dumps(results, indent=2))
# A regression probe intentionally fails when either spec guarantee fails.
assert results['tampered_before']['actual_write_calls'] == 0, 'tampered observation allowed a CDN write'
assert not results['debug_guard']['fake_sts_token_logged'], 'SDK debug guard ran after token logging'
