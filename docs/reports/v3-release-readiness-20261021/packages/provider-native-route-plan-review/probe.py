"""Independent finite-language readback; no SDK, network, or source mutation."""
from pathlib import Path
import ast, hashlib, json, re, subprocess, sys
from re import _parser, _constants

ROOT = Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/packages-native-preview-fixture')
EVIDENCE = Path('/tmp/wukongim-v3-readiness-20261021/packages/provider-native-route-readback')
OUT = Path(__file__).parent

def sha(raw):
    return hashlib.sha256(raw).hexdigest()

def git(*args):
    return subprocess.check_output(['git', '-C', str(ROOT), *args], text=True).strip()

def expand(tokens):
    """Expand only a bounded finite set of literal branches and one capture."""
    result = [('', None)]
    for op, value in tokens:
        if op is _constants.LITERAL:
            additions = [(chr(value), None)]
        elif op is _constants.IN:
            assert all(item[0] is _constants.LITERAL for item in value)
            additions = [(chr(item[1]), None) for item in value]
        elif op is _constants.BRANCH:
            assert value[0] is None
            additions = [item for branch in value[1] for item in expand(branch)]
        elif op is _constants.SUBPATTERN:
            group, add_flags, del_flags, child = value
            assert group == 1 and add_flags == del_flags == 0
            additions = [(text, text) for text, capture in expand(child) if capture is None]
        else:
            raise AssertionError('non-finite token: ' + str(op))
        updated = []
        for left, capture in result:
            for right, new_capture in additions:
                assert capture is None or new_capture is None
                updated.append((left + right, capture if new_capture is None else new_capture))
        result = updated
        assert len(result) <= 1000
    return result

before = {'head': git('rev-parse', 'HEAD'), 'tree': git('rev-parse', 'HEAD^{tree}'), 'status': git('status', '--short')}
assert before == {'head': '19ce29fb2233b3039e29c503963e27bd1fab972a', 'tree': '8275ccb92c310f488d911b5aa077a962e1faad5c', 'status': ''}
manifest_raw = (EVIDENCE / 'manifest.json').read_bytes()
assert sha(manifest_raw) == 'fecd657aa0440079faa851f7450893d00aebb872799fda947b1036f7d21e44a6'
manifest = json.loads(manifest_raw)
for item in manifest['files']:
    path = Path(item['path'])
    assert not path.is_absolute() and '..' not in path.parts
    raw = (EVIDENCE / path).read_bytes()
    assert len(raw) == item['bytes'] and sha(raw) == item['sha256']
assert sum(x['bytes'] for x in manifest['files']) == manifest['total_bytes']
binding = json.loads((EVIDENCE / 'sourcebinding.json').read_bytes())
source_files = dict(binding['source_files'])
for path in ('tests/provider_sdk/test_aliyun_object_storage_provider.py', 'docs/object-storage-delivery.md', 'docs/release-contract.md', 'README.md', '.github/workflows/object-storage-provider-contract.yml', 'requirements-object-storage-provider.lock'):
    source_files[path] = sha((ROOT / path).read_bytes())
for path, digest in source_files.items():
    assert sha((ROOT / path).read_bytes()) == digest

# Execute only the unchanged pure compiler functions extracted by AST. SDK imports cannot execute.
provider = ROOT / 'scripts/aliyun-object-storage-provider.py'
tree = ast.parse(provider.read_text())
names = {'ProviderError', 'require', 'safe_path', 'compile_rewrites'}
nodes = [node for node in tree.body if isinstance(node, (ast.FunctionDef, ast.ClassDef)) and node.name in names]
assert len(nodes) == len(names)
namespace = {'re': re, 'FUNCTION': 'back_to_origin_url_rewrite'}
exec(compile(ast.Module(body=nodes, type_ignores=[]), str(provider), 'exec'), namespace)
results = {}
rule_maps = {}
for generation in ('A', 'B'):
    delivery = json.loads((EVIDENCE / 'first-run' / generation / 'delivery.json').read_bytes())
    rows = delivery['routes']
    try:
        namespace['compile_rewrites'](rows)
    except namespace['ProviderError'] as error:
        baseline_error = str(error)
    else:
        raise AssertionError('baseline unexpectedly accepts full native route set')
    assert baseline_error == 'domain rewrite limit exceeded'
    for count in (1, 49, 50):
        subset = rows[:count]
        expected = [{'functionName': namespace['FUNCTION'], 'functionArgs': [
            {'argName': 'source_url', 'argValue': '^' + re.escape(row['public_uri']) + r'\z'},
            {'argName': 'target_url', 'argValue': '/' + row['object_key']},
            {'argName': 'flag', 'argValue': 'break'},
            {'argName': 'sequence', 'argValue': str(index)},
        ]} for index, row in enumerate(subset, 1)]
        actual = namespace['compile_rewrites'](subset + [subset[-1]])
        assert json.dumps(actual, separators=(',', ':')) == json.dumps(expected, separators=(',', ':'))
    rules = json.loads((EVIDENCE / ('candidate-rules-' + generation + '.json')).read_bytes())['rules']
    finite = {}
    for sequence, rule in enumerate(rules, 1):
        expression = rule['source_url']; target = rule['target_url']
        assert expression.startswith('^') and expression.endswith(r'\z')
        assert len(expression.encode()) <= 1024
        assert rule['sequence'] == str(sequence) and rule['flag'] == 'break'
        parsed = _parser.parse(expression[1:-2], 0)
        assert parsed.state.groups in (1, 2)
        values = expand(parsed)
        for uri, capture in values:
            assert uri not in finite
            if capture is None:
                assert '$' not in target
                mapped = target
            else:
                assert target.endswith('$1') and target.count('$') == 1
                mapped = target[:-2] + capture
            namespace['safe_path'](uri, True); namespace['safe_path'](mapped, True)
            finite[uri] = mapped
    expected = {row['public_uri']: '/' + row['object_key'] for row in rows}
    assert finite == expected and len(finite) == len(rows) == 178
    objects = {row['object_key']: row for row in delivery['objects']}
    assert len(objects) == 153
    for row in rows:
        actual_object = objects[finite[row['public_uri']][1:]]
        assert all(row[field] == actual_object[field] for field in ('size', 'sha256', 'cache_control'))
    rule_maps[generation] = {rule['source_url']: (rule['target_url'], rule['sequence']) for rule in rules}
    results[generation] = {'routes': len(rows), 'objects': len(objects), 'rules': len(rules), 'maximum_expression_bytes': max(len(x['source_url'].encode()) for x in rules), 'baseline_full_set_rejection': baseline_error, 'legacy_1_49_50_unique_plus_duplicate_byte_comparison': True, 'independent_finite_ast_expansion_exact': True, 'all_targets_object_facts_equal': True}
assert set(rule_maps['A']) == set(rule_maps['B'])
changes = []
for source, (target, sequence) in rule_maps['A'].items():
    target_b, sequence_b = rule_maps['B'][source]
    assert sequence == sequence_b
    if target != target_b:
        changes.append({'source_url_sha256': sha(source.encode()), 'sequence': sequence, 'old_target': target, 'new_target': target_b})
assert len(changes) == 2
for path, digest in source_files.items():
    assert sha((ROOT / path).read_bytes()) == digest
assert before == {'head': git('rev-parse', 'HEAD'), 'tree': git('rev-parse', 'HEAD^{tree}'), 'status': git('status', '--short')}
result = {'source': before, 'input_manifest_sha256': sha(manifest_raw), 'verified_input_files': len(manifest['files']), 'verified_input_bytes': manifest['total_bytes'], 'source_files_pre_post_equal': source_files, 'generation_results': results, 'a_b_same_sources_and_sequence': True, 'a_b_target_changes': changes, 'sdk_executed': False, 'network_used': False, 'cloud_or_live_pcre_proven': False, 'source_changed': False}
print(json.dumps(result, indent=2))
