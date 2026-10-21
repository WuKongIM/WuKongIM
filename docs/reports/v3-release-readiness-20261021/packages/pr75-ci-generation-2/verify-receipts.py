"""Read-only offline assertions over this frozen TEST ONLY CI evidence archive."""
import gzip, hashlib, json, re
from pathlib import Path
P = Path(__file__).resolve().parent
HEAD = 'd824fe37918ee4ef22b1b1745bc02518812563e3'
TREE = '50e099e7adcda9e205c9862dad7e1071022d2a3c'
PRODUCT = 'fc18221282f289088ec9b4282fda8e0bfc67a3b8'
def read(name):
    return json.loads((P / name).read_text())
d = read('artifacts/native-stable-four-clients-test-only/native-stable-clients.json')
h = read('head-commit.json'); m = read('merge-commit.json')
assert h['sha'] == HEAD and h['tree']['sha'] == TREE == m['tree']['sha']
assert d['control'] == {'checkout_sha': m['sha'], 'tree': TREE}
for name in ('native-run-final.json', 'pages-run-final.json'):
    v = read(name)
    assert v['head_sha'] == HEAD and v['run_attempt'] == 1 and v['event'] == 'pull_request'
    assert v['status'] == 'completed' and v['conclusion'] == ('failure' if name.startswith('native') else 'success')
assert d['test_only'] and d['passed'] is False and len(d['cases']) == 23
assert all(c['passed'] for c in d['cases'][:22]) and not d['cases'][22]['passed'] and len({c['case'] for c in d['cases']}) == 23
assert d['cases'][22]['case'] == 'rocky-linux-9-A-to-B-request-barrier'
assert d['failure'] == d['exercise_failure'] == 'downloaded rpm package is absent from the indexed snapshot'
assert d['published'] is False and d['public_origin_verified'] is False and d['source_provenance_verified'] is False
for key in ('reader_cleanup', 'cleanup'):
    v = d[key]
    assert v['verified'] and v['errors'] == [] and v['remaining'] == {'containers': [], 'networks': []}
assert len(d['cache_cleanup']) == 3
assert all(c['verified'] and c['result'] == {'remaining': []} for c in d['cache_cleanup'])
legacy = []
for name, count, transports in [('native-source-metadata', 7, 0), ('native-local-signing', 10, 0), ('native-bootstrap-preparation', 6, 0), ('native-stable-candidate', 31, 8)]:
    v = read('artifacts/' + name + '-test-only/' + name + '.json')
    assert v['passed'] and len(v['cases']) == count and len(v.get('transport_cases', [])) == transports
    assert all(c['passed'] for c in v['cases'] + v.get('transport_cases', []))
    clean = {k: v[k] for k in ('checked_in_control_unchanged', 'secret_tree_removed_before_verification', 'source_inputs_unchanged', 'scratch_removed') if k in v}
    assert all(clean.values())
    legacy.append({'name': name, 'cases': count, 'transport_cases': transports, 'cleanup': clean})
baseline = []
for gen, version in [('A', '3.0.0'), ('B', '3.0.1')]:
    v = read('artifacts/native-stable-four-clients-test-only/clients-' + gen + '.json')
    assert v['test_only'] and v['installed_cli_verified'] and v['expected_version_verified']
    assert v['expected_source_sha'] == PRODUCT and v['expected_version'] == version
    clients = v['apt'] + v['rpm']; assert len(clients) == 4
    for c in clients:
        installed = c['installed_cli']
        assert installed['verified'] and installed['identity']['commit'] == PRODUCT and installed['identity']['version'] == version
        assert installed['checks'] == ['version', 'help', 'bench_validate', 'db_query', 'migrate_diagnose', 'invalid_inputs', 'read_only_data']
        assert c['download']['snapshot_versions'] == [version]
    baseline.append({'generation': gen, 'version': version, 'source': PRODUCT, 'distributions': [c['distribution'] for c in clients]})
raw_requests = [json.loads(line) for line in (P / 'artifacts/native-stable-four-clients-test-only/https-requests.jsonl').read_text().splitlines()]
cache = []
for number, c in enumerate(d['cases'], 1):
    result = c.get('result', {})
    v = result.get('cache')
    if v:
        paths = v['metadata_reads']
        if v['reuse_required']: assert paths
        for path in paths:
            before, after = v['before'][path], v['after'][path]
            assert all(before[k] == after[k] for k in ('device', 'inode', 'sha256', 'size'))
            assert after['atime_ns'] > 1
        cache.append({'number': number, 'case': c['case'], 'reuse_required': v['reuse_required'], 'metadata_before_count': len(v['before']), 'metadata_after_count': len(v['after']), 'metadata_reads': paths})
        if c['case'].startswith(('rocky-', 'alma-')):
            assert len(v['after']) == 14
            assert any(p.startswith('dnf-cli-cache/') and '/repodata/' in p for p in v['after'])
            gen = 'B' if c['case'].endswith('B-target') else 'A'
            audit = 4243 if gen == 'B' else 4242
            assert result['download']['snapshot_versions'] == ['3.0.1' if gen == 'B' else '3.0.0']
            requests = result['requests']
            assert all(r in raw_requests for r in requests)
            assert all(r['status'] == 200 and r['response_write_completed'] for r in requests)
            metadata = [r for r in requests if '/repodata/' in r['path']]
            assert metadata and all('/snapshots/stable/r' + str(audit) + '-' in r['path'] for r in metadata)
            for path in v['metadata_reads']:
                if '/repodata/' not in path: continue
                base = path.rsplit('/', 1)[1]
                # primary may be read from cache without a second network fetch.
                seen = [r for r in raw_requests if '/snapshots/stable/r4242-' in r['path'] and r['path'].endswith('/repodata/' + base) and r['status'] == 200 and r['response_write_completed']]
                assert any(r['sha256'] == v['after'][path]['sha256'] and r['size'] == v['after'][path]['size'] for r in seen)
            if v['reuse_required']:
                assert len(paths) == 5
                assert all(v['after'][path]['atime_ns'] > v['before'][path]['atime_ns'] for path in paths)
    if c['expected_rejection'] and 'exit_code' in result:
        assert result['exit_code'] not in (0, 124, 125, 126, 127, 137, 143)
        matching = [v for v in d['commands'] if v.get('output_sha256') == result['output_sha256'] and v['exit_code'] == result['exit_code']]
        assert matching and any(not v['timed_out'] and not v['output_limit'] for v in matching)
    fault = result.get('by_hash_fault_response')
    if fault:
        assert fault in raw_requests and fault['response_write_completed']
        assert fault['status'] == (404 if result['fault'] == 'missing-byhash' else 200)
        if result['fault'] == 'corrupt-byhash': assert fault['sha256'] == '54958456b8d2c755a77144f155197b215e11eb47703fa0123c3659787ab7ca04' and fault['size'] == 28
assert len(cache) == 6 and sum(c['reuse_required'] for c in cache) == 2
assert all(v['exit_code'] == 0 and not v['timed_out'] for v in d['commands'] if v['stage'] in ('cache-arm', 'cache-inventory', 'cache-remove'))
assert len([v for v in d['commands'] if v['stage'] == 'cache-arm']) == 6
text = (P / 'pages-job.log').read_text()
assert re.search(r'Ran 557 tests[^\n]*\n[^\n]*\n[^\n]*OK\n', text) and 'Ran 4 tests' in text
jobs = read('pages-jobs-final.json')['jobs']; assert [j['conclusion'] for j in jobs] == ['success', 'skipped']
log = gzip.decompress((P / 'legacy-native-job.log.gz').read_bytes())
assert hashlib.sha256(log).hexdigest() == read('legacy-native-log-excerpts.json')['sha256']
assert b'native-package signed repository container validation passed' in log
# Verify this first failure independently instead of treating process exit 0 as acceptance.
f=read('first-failure.json'); assert f['case_number']==23 and f['download_command_index_zero_based']==161
assert f['download_command']['exit_code']==0 and not f['download_command']['timed_out'] and not f['download_command']['output_limit']
assert '--setopt=wukongim-stable.metadata_expire=-1 install --downloadonly' in '\n'.join(f['container_create']['command'])
assert len(f['requests'])==17 and {x['serial'] for x in f['requests']}=={24}
assert all(x in raw_requests and x['status']==200 and x['response_write_completed'] for x in f['requests'])
assert f['requests'][1]['path'].startswith('/snapshots/stable/r4242-')
last=f['requests'][-1]; assert last['path'].startswith('/snapshots/stable/r4243-') and last['path'].endswith('/Packages/wukongim_3.0.1_linux_amd64.rpm')
baseline_b=read('artifacts/native-stable-four-clients-test-only/clients-B.json')
assert any(x['download']['sha256']==last['sha256'] and x['download']['size']==last['size'] for x in baseline_b['rpm'])
for content in [text,log.decode(),(P/'stable-job.log').read_text()]: assert m['sha'] in content
assert 'skipped=' not in text
pages=read('pages-jobs-final.json')['jobs'];assert all(x['conclusion']=='skipped' for x in pages[0]['steps'] if x['name'] in ('Configure Pages','Upload complete Pages artifact'))
print(json.dumps({'receipt_audit_passed': True, 'ci_passed': False, 'head': HEAD, 'tree': TREE, 'synthetic_checkout': m['sha'], 'planned_cases': 49, 'executed_cases': 23, 'passed_cases': 22, 'failed_cases': 1, 'not_reached': 26, 'baseline': baseline, 'cache': cache, 'legacy': legacy, 'cleanup_zero': True, 'created_caches_cleaned':3,'fourth_cache_not_created':True,'new_override_observed':True,'failed_download_B_identity_matches_baseline':True,'test_only': True}, indent=2))
