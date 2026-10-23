import json,os,pathlib,tempfile,time,unittest
from unittest.mock import patch
P=pathlib.Path(__file__).parent
class TwoSamples(unittest.TestCase):
 def case(self,cases,hits_expected,valid=False):
  with tempfile.TemporaryDirectory() as d:
   root=pathlib.Path(d);run=root/'run';run.mkdir();out=run/'out';cli=root/'fake-cli';program=(P/'fake_cli.py').read_text().replace('FIXTURE_ROOT',repr(str(root))).replace('FIXTURE_CASES',repr(cases));cli.write_text('#!'+os.sys.executable+'\n'+program.split('\n',1)[1]);cli.chmod(0o700)
   ns={'__name__':'synthetic_only'};exec(compile((P/'PRIVATE_offline_db_inspect.proposed.py').read_text(),'prepared-driver-fixture','exec'),ns)
   for n in [1,2,3]:base=run/f'data/node{n}/messages';base.mkdir(parents=True);(base/'synthetic').write_bytes(b'synthetic')
   ns.update(RUN=run,OUT=out,CLI=cli);original=ns['original_inventory']();samples=[({'worker_id':i,'sample_index':100+i,'fingerprint':str(i)*32,'decoded':{'ordinal':i},'matches':[{'target_group_index':i}]},{'ChannelID':f'synthetic-channel-{i}','ClientMsgNo':f'synthetic-client-{i}'}) for i in [0,1]];ns['guard_before_mkdir']=lambda deadline:(original,original,samples);ns['join_canonical_warnings']=lambda items,deadline:{'synthetic':True}
   with patch.dict(os.environ,{'OFFLINE_PARENT_PID':str(os.getppid()),'OFFLINE_WORK_DEADLINE_MONO':str(time.monotonic()+85),'EXECUTOR_SECRET_SENTINEL':'must-not-inherit'}):
    if valid:result=ns['worker']();self.assertEqual(result['queries'],6)
    else:
     with self.assertRaises(AssertionError):ns['worker']()
   hits=[json.loads(line) for line in (root/'hits.jsonl').read_text().splitlines()];self.assertEqual([(x['sample'],x['node']) for x in hits],hits_expected)
   for x in hits:self.assertNotIn('EXECUTOR_SECRET_SENTINEL',x['env_keys']);self.assertNotIn('WK_BENCH_API_TOKEN',x['env_keys'])
   safe=json.loads((out/'safe-offline-db-message-identity.json').read_text());self.assertTrue(safe['original_bytes_mtime_unchanged']);self.assertTrue(all(n['stdout_bytes']<=262144 and n['stderr_bytes']<=65536 and n['process_joined'] for item in safe['samples'] for n in item['nodes']))
 def test_two_valid_samples_use_six_queries(self):self.case({},[(s,n) for s in [0,1] for n in [1,2,3]],True)
 def test_first_invalid_and_second_sample_divergent_stop_all_later(self):
  for cases,hits in [({'0-1':'invalid'},[(0,1)]),({'0-2':'has_more'},[(0,1),(0,2)]),({'0-1':'incomplete_row'},[(0,1)]),({'1-1':'nonunique'},[(0,1),(0,2),(0,3),(1,1)]),({'1-2':'divergent'},[(0,1),(0,2),(0,3),(1,1),(1,2)])]:
   with self.subTest(cases=cases):self.case(cases,hits)
 def test_streaming_stdout_and_stderr_limits_stop_later_queries(self):
  for mode in ['overflow','stderr_overflow']:
   with self.subTest(mode=mode):self.case({'0-1':mode},[(0,1)])
if __name__=='__main__':unittest.main()
