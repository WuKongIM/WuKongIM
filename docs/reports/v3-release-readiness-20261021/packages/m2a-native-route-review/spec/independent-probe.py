from pathlib import Path
import ast,copy,hashlib,importlib.util,json,re,subprocess,unittest
ROOT=Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/packages-native-route-compiler')
OUT=Path(__file__).resolve().parent
READBACK=Path('/tmp/wukongim-v3-readiness-20261021/packages/provider-native-route-readback')
SOURCE='scripts/aliyun-object-storage-provider.py';BASE='bbcfb377c7d5bb23742ebf28394a0cbc4f45cc17'
sha=lambda b:hashlib.sha256(b).hexdigest()
def compiler(raw):
 tree=ast.parse(raw);nodes=[n for n in tree.body if isinstance(n,(ast.ClassDef,ast.FunctionDef)) and n.name in ('ProviderError','require','safe_path','compile_rewrites')]
 ns={'re':re,'FUNCTION':'back_to_origin_url_rewrite'};exec(compile(ast.Module(body=nodes,type_ignores=[]),'exact-compiler','exec'),ns);return ns
before=subprocess.check_output(['git','show',BASE+':'+SOURCE],cwd=ROOT);after=(ROOT/SOURCE).read_bytes();old=compiler(before);new=compiler(after)
a=ast.parse(before);b=ast.parse(after)
for tree in (a,b):tree.body=[n for n in tree.body if not isinstance(n,ast.FunctionDef) or n.name!='compile_rewrites']
assert ast.dump(a)==ast.dump(b)
fixture=json.loads((ROOT/'tests/provider_sdk/fixtures/native-composite-routes.json').read_bytes())
assert fixture['readback_manifest_json'].encode()==(READBACK/'manifest.json').read_bytes()
assert fixture['readback_manifest_sha256']==sha((READBACK/'manifest.json').read_bytes())
assert fixture['source_binding']==json.loads((READBACK/'sourcebinding.json').read_bytes())
manifest={v['path']:v for v in fixture['readback_manifest']['files']}
# Decode the complete finite language; regex samples alone cannot prove no extra paths.
def unescape(s):
 result='';i=0
 while i<len(s):
  if s[i]=='\\':
   i+=1;assert i<len(s)
  result+=s[i];i+=1
 assert re.escape(result)==s
 return result
def inverse(rules):
 result={}
 for seq,rule in enumerate(rules,1):
  assert rule['functionName']=='back_to_origin_url_rewrite';args=rule['functionArgs'];assert [a['argName'] for a in args]==['source_url','target_url','flag','sequence']
  source,target,flag,number=[a['argValue'] for a in args];assert flag=='break' and number==str(seq) and len(source.encode())<=1024
  assert source.startswith('^') and source.endswith(r'\z');body=source[1:-2]
  if '(' in body:
   prefix,choices=body.split('(',1);assert choices.endswith(')') and choices.count(')')==1 and '(' not in choices
   assert target.endswith('$1') and target.count('$')==1
   pairs=[(unescape(prefix)+unescape(s),target[:-2]+unescape(s)) for s in choices[:-1].split('|')]
  else:
   assert '$' not in target;pairs=[(unescape(body),target)]
  for uri,key in pairs:assert uri not in result;result[uri]=key
 return result
compiled={};generations={}
for label,docs in fixture['generations'].items():
 for filename,value in docs.items():
  raw=(READBACK/'first-run'/label/filename).read_bytes();assert json.loads(raw)==value
  assert sha(raw)==manifest['first-run/'+label+'/'+filename]['sha256']
  assert sha((json.dumps(value,sort_keys=True,separators=(',',':'))+'\n').encode())==sha(raw)
 delivery=docs['delivery.json'];routes=delivery['routes'];objects={x['object_key']:x for x in delivery['objects']}
 assert len(routes)==178 and len(objects)==153
 assert json.loads((READBACK/'first-run'/label/'routes.json').read_bytes())==routes
 for row in routes:
  for key in ('sha256','size','cache_control'):assert row[key]==objects[row['object_key']][key]
 original=copy.deepcopy(routes);rules=new['compile_rewrites'](routes);assert routes==original
 assert len(rules)==17 and max(len(r['functionArgs'][0]['argValue'].encode()) for r in rules)==1017
 assert inverse(rules)=={x['public_uri']:'/'+x['object_key'] for x in routes}
 patterns=[re.compile(r['functionArgs'][0]['argValue'].replace(r'\z',r'\Z')) for r in rules]
 neighbour_count=0
 for uri in inverse(rules):
  assert sum(bool(p.fullmatch(uri)) for p in patterns)==1
  for other in (uri+'x',uri+'/',uri+'\n',uri+'?query',uri+'#fragment',uri+'%252f',uri.replace('/','//',1),uri+'/../x'):
   if other not in inverse(rules):assert not any(p.fullmatch(other) for p in patterns);neighbour_count+=1
 compiled[label]=rules;generations[label]={'routes':178,'objects':153,'rules':17,'max_expression_bytes':1017,'neighbour_rejections':neighbour_count,'canonical_original_documents_verified':len(docs),'exact_inverse':True}
assert [x['functionArgs'][0] for x in compiled['A']]==[x['functionArgs'][0] for x in compiled['B']]
assert sum(a['functionArgs'][1]!=b['functionArgs'][1] for a,b in zip(compiled['A'],compiled['B']))==2
route=lambda uri,key='snapshots/preview/unproved/site/pkg':dict(public_uri=uri,object_key=key,size=1,sha256='a'*64,cache_control='no-store')
compatibility=0
for size in (1,2,49,50):
 rows=[route('/p/a.b+c-d~_'+str(i)) for i in range(size)]
 for v in (rows,rows+copy.deepcopy(rows[:1])):
  assert json.dumps(new['compile_rewrites'](v))==json.dumps(old['compile_rewrites'](v));compatibility+=1
try:new['compile_rewrites']([route('/p'+str(i)) for i in range(51)]);raise AssertionError('unexpected coalescing')
except new['ProviderError']:pass
# Keep the original 22 shipping methods exactly unchanged; tests preceded implementation.
testfile=ROOT/'tests/provider_sdk/test_aliyun_object_storage_provider.py'
oldtest=ast.parse(subprocess.check_output(['git','show',BASE+':tests/provider_sdk/test_aliyun_object_storage_provider.py'],cwd=ROOT));newtest=ast.parse(testfile.read_bytes())
def methods(t):return {n.name:ast.dump(n) for c in t.body if isinstance(c,ast.ClassDef) and c.name=='ProviderContract' for n in c.body if isinstance(n,ast.FunctionDef) and n.name.startswith('test_')}
m1,m2=methods(oldtest),methods(newtest);assert len(m1)==22 and all(m2[k]==v for k,v in m1.items())
assert testfile.read_bytes()==Path('/tmp/wukongim-v3-readiness-20261021/packages/m2a-native-route-compiler-v1/preimplementation-tests.py').read_bytes()
# Only two focused existing SDK-Wire methods, each socket-guarded. No provider network.
spec=importlib.util.spec_from_file_location('review_native_sdk',testfile);module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module)
names=['test_native_sdk_configuration_reuses_source_ids_across_a_b','test_grouping_or_rechunking_cannot_delete_preexisting_sources']
result=unittest.TextTestRunner(verbosity=2).run(unittest.TestSuite(module.ProviderContract(n) for n in names));assert result.wasSuccessful() and result.testsRun==2 and not result.skipped
proof={'passed':True,'generations':generations,'unchanged_literal_serialization_cases':compatibility,'unproved_51_routes_rejected':True,'source_sets_equal_A_B':True,'A_B_changed_targets':2,'original_22_methods_AST_unchanged':True,'tests_equal_preimplementation_RED':True,'provider_AST_outside_compile_unchanged':True,'SDK_Wire_methods_executed':names,'SDK_Wire_tests':2,'SDK_live_network':False,'test_only_and_not_live_PCRE_equivalence':True,'all_processes_terminal':True}
(OUT/'probe-result.json').write_text(json.dumps(proof,sort_keys=True,indent=2)+'\n');print(json.dumps(proof))
