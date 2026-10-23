#!/usr/bin/env python3
import json,os,pathlib,sys
node=int(pathlib.Path(sys.argv[sys.argv.index('--message-path')+1]).name.removeprefix('node'));root=pathlib.Path(os.environ['FAKE_DB_ROOT']);case=json.loads(os.environ['FAKE_DB_CASES']).get(str(node),'valid')
with (root/'hits.jsonl').open('a') as f:f.write(str(node)+'\n')
row={'client_msg_no':'synthetic-client','message_id':9223372036854775931,'message_seq':1};data={'rows':[row],'stats':{'has_more':False}}
if case=='invalid_json':print('{');raise SystemExit(0)
if case=='missing_stats':data.pop('stats')
if case=='has_more':data['stats']['has_more']=True
if case=='no_rows':data['rows']=[]
if case=='two_rows':data['rows']=[row,row.copy()]
if case=='wrong_clientno':row['client_msg_no']='other-synthetic-client'
if case=='message_id_string':row['message_id']=str(row['message_id'])
if case=='divergent_ID':row['message_id']+=1
if case=='nonzero_exit':raise SystemExit(3)
print(json.dumps(data))
