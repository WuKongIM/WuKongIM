import datetime,json,pathlib,tempfile,time,unittest
P=pathlib.Path(__file__).parent
class WarningJoin(unittest.TestCase):
 def test_only_exact_uint64_tuple_in_window_is_projected(self):
  with tempfile.TemporaryDirectory() as d:
   run=pathlib.Path(d);(run/'report').mkdir();start='2026-10-23T07:39:22+08:00';end='2026-10-23T07:39:45+08:00';(run/'report/run-start.json').write_text(json.dumps({'started_at':start}));(run/'safe-retained-worker-cuts.json').write_text(json.dumps({'cuts':[{'cut':'terminal','at':end}]}))
   for node in [1,2,3]:(run/f'logs/node{node}').mkdir(parents=True);(run/f'logs/node{node}/warn.log').write_text('')
   message={'event':'synthetic-fixed-warning','phase':'presence','result':'timeout','mode':'durable','messageID':9223372036854775931,'messageSeq':1,'uid':'synthetic-private-uid','error':'context deadline exceeded'};line='2026-10-23 07:39:30.000 WARN delivery plan incomplete '+json.dumps(message)+'\n';wrongseq=message|{'messageSeq':2};(run/'logs/node2/warn.log').write_text(line+'2026-10-23 07:39:31.000 WARN delivery plan incomplete '+json.dumps(wrongseq)+'\n'+'2026-10-23 07:39:21.000 WARN delivery plan incomplete '+json.dumps(message)+'\n')
   ns={'__name__':'synthetic-warning-join'};exec(compile((P/'PRIVATE_offline_db_inspect.proposed.py').read_text(),'synthetic-join','exec'),ns);ns['RUN']=run;nodes=[{'matching_messages':[{'message_id':'9223372036854775931','message_seq':'1'}]}]*3;out=ns['join_canonical_warnings'](nodes,time.monotonic()+5)
   self.assertEqual(out['exact_message_warning_match_count'],1);self.assertEqual(out['exact_message_warning_matches'][0]['message_id'],'9223372036854775931');self.assertEqual(out['exact_message_warning_matches'][0]['fixed_error_reason'],'deadline_exceeded');self.assertNotIn('synthetic-private-uid',json.dumps(out))
 def test_helper_function_exists_before_real_use(self):
  text=(P/'PRIVATE_offline_db_inspect.proposed.py').read_text();self.assertIn('def join_canonical_warnings(',text)
if __name__=='__main__':unittest.main()
