#!/usr/bin/env python3
import json,os,pathlib,sys
root=pathlib.Path(FIXTURE_ROOT);cases=FIXTURE_CASES;node=int(pathlib.Path(sys.argv[sys.argv.index('--message-path')+1]).name.removeprefix('node'));sample=0 if 'synthetic-client-0' in sys.argv[-1] else 1;key=f'{sample}-{node}'
with (root/'hits.jsonl').open('a') as stream:stream.write(json.dumps({'sample':sample,'node':node,'env_keys':sorted(os.environ)})+'\n')
mode=cases.get(key,'valid');row={'client_msg_no':f'synthetic-client-{sample}','message_id':9223372036854775931+sample,'message_seq':1,'server_timestamp_ms':1234,'payload_hash':'synthetic-hash','payload_size':8};data={'rows':[row],'stats':{'has_more':False}}
if mode=='invalid':print('{');raise SystemExit(0)
if mode=='has_more':data['stats']['has_more']=True
if mode=='incomplete_row':row.pop('payload_size')
if mode=='nonunique':data['rows']=[row,row.copy()]
if mode=='divergent':row['message_id']+=100
if mode=='overflow':sys.stdout.buffer.write(b'x'*262145);raise SystemExit(0)
if mode=='stderr_overflow':sys.stderr.buffer.write(b'x'*65537);raise SystemExit(0)
print(json.dumps(data))
