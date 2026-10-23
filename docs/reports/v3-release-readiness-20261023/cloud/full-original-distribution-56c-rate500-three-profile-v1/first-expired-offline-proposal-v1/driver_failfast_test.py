import datetime,hashlib,json,os,pathlib,shutil,tempfile,time,unittest
from unittest.mock import patch
P=pathlib.Path(__file__).parent
class FailFast(unittest.TestCase):
 def case(self,cases,expected,valid=False):
  with tempfile.TemporaryDirectory() as d:
   root=pathlib.Path(d);run=root/'run';run.mkdir();out=run/'out';cli=root/'fake-cli';cli.write_text('#!'+os.sys.executable+'\n'+(P/'fake_cli.py').read_text().split('\n',1)[1]);cli.chmod(0o700)
   ns={'__name__':'synthetic_offline_driver'};exec(compile((P/'PRIVATE_offline_db_inspect.proposed.py').read_text(),'fixed-offline-driver-fixture','exec'),ns)
   for node in [1,2,3]:base=run/f'data/node{node}/messages';base.mkdir(parents=True);(base/'synthetic').write_bytes(b'fixture')
   ns.update(RUN=run,OUT=out,CLI=cli);ns['join_canonical_warnings']=lambda rows,deadline:{'status':'synthetic-loop-test-join-not-exercised'};original=ns['original_inventory']();raw={'ChannelID':'synthetic-channel','ClientMsgNo':'synthetic-client'};ns['guard_before_mkdir']=lambda deadline:(original,original,raw)
   env={'OFFLINE_PARENT_PID':str(os.getppid()),'OFFLINE_WORK_DEADLINE_MONO':str(time.monotonic()+85),'FAKE_DB_ROOT':str(root),'FAKE_DB_CASES':json.dumps(cases)}
   with patch.dict(os.environ,env):
    if valid:result=ns['worker']();self.assertEqual(result['queries'],3)
    else:
     with self.assertRaises(AssertionError):ns['worker']()
   hits=[int(x) for x in (root/'hits.jsonl').read_text().splitlines()];self.assertEqual(hits,expected)
   safe=json.loads((out/'safe-offline-db-message-identity.json').read_text());self.assertTrue(safe['original_bytes_mtime_unchanged'])
   if valid:self.assertEqual([n['matching_messages'][0]['message_id'] for n in safe['nodes']],['9223372036854775931']*3)
 def test_bad_first_frame_prevents_all_later_queries(self):
  for kind in ['invalid_json','missing_stats','has_more','no_rows','two_rows','wrong_clientno','message_id_string','nonzero_exit']:
   with self.subTest(kind=kind):self.case({'1':kind},[1])
 def test_valid_first_then_bad_second_stops_third(self):
  for kind in ['invalid_json','divergent_ID']:
   with self.subTest(kind=kind):self.case({'2':kind},[1,2])
 def test_three_valid_same_rows_use_exactly_three_queries(self):self.case({},[1,2,3],valid=True)
if __name__=='__main__':unittest.main()
