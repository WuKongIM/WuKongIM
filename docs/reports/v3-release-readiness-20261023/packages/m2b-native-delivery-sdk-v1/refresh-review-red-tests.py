"""Official SDK HTTP contracts; no live sockets, cloud credentials or production claim."""
import copy
import hashlib
import importlib.util
import io
import json
import logging
import os
from pathlib import Path
import re
import socket
import tempfile
import unittest
from unittest import mock
from urllib.parse import parse_qs, urlsplit

import requests
from requests.adapters import BaseAdapter

ROOT = Path(__file__).resolve().parents[2]
SECRET = 'FIXTURE-SECRET-NEVER-REAL'
TOKEN = 'FIXTURE-TOKEN-NEVER-REAL'
RESOURCE = dict(account_id='1234567890123456', principal_arn='acs:ram::1234567890123456:role/fixture',
                identity_type='AssumedRoleUser', bucket='fixture-package-bucket', region='cn-hangzhou',
                endpoint='https://oss-cn-hangzhou.aliyuncs.com', domain='packages.githubim.com',
                cname='packages.githubim.com.w.kunlunaq.com')
DATA = b'exact package bytes\x00\xff'
FACTS = dict(size=len(DATA), sha256=hashlib.sha256(DATA).hexdigest(), cache_control='public, max-age=31536000, immutable')
KEY = 'public/rpm/stable/Packages/A.rpm'


def route(uri='/repo', key='snapshots/preview/r10-abc/site/repo'):
    return dict(public_uri=uri, object_key=key, size=FACTS['size'], sha256=FACTS['sha256'],
                cache_control='no-store', kind='entrypoint')


def config(name, args, config_id):
    return dict(ConfigId=str(config_id), FunctionName=name, Status='success', ParentId='0',
                FunctionArgs={'FunctionArg':[{'ArgName':k,'ArgValue':v} for k,v in args.items()]})


def native_routes_fixture():
    return json.loads((ROOT/'tests/provider_sdk/fixtures/native-composite-routes.json').read_bytes())


def inverse_finite_rules(functions):
    """Independently decode the closed literal grammar; do not enumerate regex samples."""
    def literal(value):
        decoded=re.sub(r'\\(.)',r'\1',value)
        if re.escape(decoded)!=value or not decoded:
            raise AssertionError('nonliteral alternative')
        return decoded
    pairs={}
    for number,item in enumerate(functions,1):
        if item['functionName']!='back_to_origin_url_rewrite':raise AssertionError('function')
        if [x['argName'] for x in item['functionArgs']]!=['source_url','target_url','flag','sequence']:
            raise AssertionError('argument order')
        args={x['argName']:x['argValue'] for x in item['functionArgs']}
        source=args['source_url'];target=args['target_url']
        if args['flag']!='break' or args['sequence']!=str(number) or len(source.encode())>1024:
            raise AssertionError('rule bounds')
        if not source.startswith('^') or not source.endswith(r'\z'):raise AssertionError('anchors')
        inner=source[1:-2]
        if '(' in inner:
            prefix,tail=inner.split('(',1)
            if not tail.endswith(')') or tail.count(')')!=1 or '(' in tail:
                raise AssertionError('capture grammar')
            if not target.endswith('$1') or target.count('$')!=1:raise AssertionError('capture target')
            values=[(literal(prefix)+literal(s),target[:-2]+literal(s)) for s in tail[:-1].split('|')]
        else:
            if '$' in target:raise AssertionError('literal target')
            values=[(literal(inner),target)]
        for uri,destination in values:
            if uri in pairs:raise AssertionError('duplicate language')
            pairs[uri]=destination
    return pairs


class Wire(BaseAdapter):
    """Return provider XML/JSON at requests' transport boundary after SDK signing."""
    def __init__(self):
        self.requests=[]; self.objects={}; self.put_fault=None; self.get_fault=None
        self.versioning=b'<VersioningConfiguration/>'; self.owner=RESOURCE['account_id']
        self.principal=RESOURCE['principal_arn']; self.domain=RESOURCE['domain']
        origin=RESOURCE['bucket']+'.oss-cn-hangzhou.aliyuncs.com'
        self.detail=dict(DomainName=self.domain,Cname=RESOURCE['cname'],DomainStatus='online',
            SourceModels={'SourceModel':[dict(Type='oss',Content=origin,Port=443,Priority='20',Weight='10',Enabled='online')]})
        self.configs=[config('forward_scheme',{'enable':'on','scheme_origin':'https'},1),
            config('set_hashkey_args',{'hashkey_args':'','disable':'off','keep_oss_args':'on'},2),
            config('set_req_host_header',{'domain_name':origin},3),
            config('https_origin_sni',{'enabled':'on','https_origin_sni':origin},4)]
        self.batch_fault=None; self.refresh_fault=None; self.tasks={}; self.polls=0

    def close(self): pass

    def send(self, request, **kwargs):
        u=urlsplit(request.url); q=parse_qs(u.query,keep_blank_values=True)
        if request.body and u.hostname in ('cdn.aliyuncs.com','sts.aliyuncs.com'):
            q.update(parse_qs(request.body.decode() if isinstance(request.body,bytes) else request.body))
        action=q.get('Action',[''])[0]
        self.requests.append((request,kwargs,action,q))
        status=200; body=b''; headers={'x-oss-request-id':'fixture-request-id','Content-Type':'application/xml','Date':'Wed, 21 Oct 2026 00:00:00 GMT'}
        if action:
            headers={'Content-Type':'application/json'}
            result={'RequestId':'fixture-request-id'}
            if action=='GetCallerIdentity':result.update(AccountId=self.owner,Arn=self.principal,IdentityType='AssumedRoleUser')
            elif action=='DescribeCdnDomainDetail':result['GetDomainDetailModel']=self.detail
            elif action=='DescribeCdnDomainConfigs':result['DomainConfigs']={'DomainConfig':self.configs}
            elif action=='BatchSetCdnDomainConfig':
                values=json.loads(q['Functions'][0]); rows=[]
                for i,v in enumerate(values):
                    cid=v.get('configId',str(max(100,max(int(x['ConfigId']) for x in self.configs))+1))
                    item=config(v['functionName'],{x['argName']:x['argValue'] for x in v['functionArgs']},cid)
                    self.configs=[x for x in self.configs if x['ConfigId']!=str(cid)]+[item]
                    rows.append(dict(ConfigId=0 if self.batch_fault=='zero' else 99999+int(cid) if self.batch_fault=='wrongid' else int(cid),DomainName=self.domain,FunctionName=v['functionName']))
                    if self.batch_fault=='partial':break
                if self.batch_fault=='timeout':raise requests.ReadTimeout(SECRET+' '+TOKEN)
                if self.batch_fault=='missing':rows=[]
                if self.batch_fault=='duplicate':rows=rows+rows
                result['DomainConfigList']={'DomainConfigModel':rows}
            elif action=='RefreshObjectCaches':
                for i,url in enumerate(q['ObjectPath'][0].split('\n')):
                    self.tasks[str(501+i)]={'TaskId':str(501+i),'ObjectPath':url,'ObjectType':'file','Status':'Complete','Process':'100%'}
                result['RefreshTaskId']=','.join(self.tasks)
            elif action=='DescribeRefreshTaskById':
                self.polls+=1
                rows=[copy.deepcopy(self.tasks[k]) for k in q['TaskId'][0].split(',')]
                if self.refresh_fault in ('Failed','Canceled','Timeout','Refreshing'): rows[0]['Status']=self.refresh_fault
                elif self.refresh_fault=='progress' and self.polls==1:rows[0]['Status']='Refreshing'
                elif self.refresh_fault=='foreign':rows[0]['TaskId']='999'
                elif self.refresh_fault=='url':rows[0]['ObjectPath']='https://packages.githubim.com/other'
                elif self.refresh_fault=='type':rows[0]['ObjectType']='directory'
                elif self.refresh_fault=='missing':rows=[]
                elif self.refresh_fault=='duplicate':rows=rows+rows
                result.update(Tasks=rows,TotalCount=len(rows))
            else:raise AssertionError('unapproved API '+action)
            body=json.dumps(result).encode()
        elif 'bucketInfo' in q or u.query=='bucketInfo':
            body=('<BucketInfo><Bucket><Name>'+RESOURCE['bucket']+'</Name><Location>oss-cn-hangzhou</Location>'
                  '<ExtranetEndpoint>oss-cn-hangzhou.aliyuncs.com</ExtranetEndpoint><Owner><ID>'+self.owner+
                  '</ID></Owner><AccessControlList><Grant>private</Grant></AccessControlList></Bucket></BucketInfo>').encode()
        elif 'versioning' in q or u.query=='versioning':body=self.versioning
        elif request.method=='PUT':
            key=u.path[1:]
            if self.put_fault=='denied':status=403;body=b'<Error><Code>AccessDenied</Code><Message>FIXTURE-SECRET-NEVER-REAL</Message></Error>'
            elif self.put_fault=='other409':status=409;body=b'<Error><Code>BucketNotEmpty</Code></Error>'
            elif key in self.objects:status=409;body=b'<Error><Code>FileAlreadyExists</Code></Error>'
            else:
                if self.put_fault=='timeout_before':raise requests.ReadTimeout(SECRET)
                raw=request.body.read() if hasattr(request.body,'read') else request.body
                if not isinstance(raw,bytes):raw=b''.join(raw)
                self.objects[key]=(raw,request.headers['Cache-Control'])
                if self.put_fault=='timeout_after':raise requests.ReadTimeout(TOKEN)
        elif request.method=='GET':
            if u.path[1:] not in self.objects:status=404;body=b'<Error><Code>NoSuchKey</Code></Error>'
            else:
                body,cache=self.objects[u.path[1:]]
                if self.get_fault=='truncated':body=body[:-1]
                if self.get_fault=='overlong':body+=b'extra'
                if self.get_fault=='digest':body=b'x'*len(body)
                headers.update({'Cache-Control':'wrong' if self.get_fault=='cache' else cache,'Content-Length':str(len(self.objects[u.path[1:]][0]))})
                if self.get_fault=='encoding':headers['Content-Encoding']='gzip'
        else:raise AssertionError('unexpected request '+request.method)
        response=requests.Response();response.status_code=status;response.headers.update(headers)
        response.request=request;response.url=request.url;response.reason='fixture'
        response.raw=io.BytesIO(body);response._content=False
        if self.get_fault=='interrupted' and not action and request.method=='GET' and not u.query:
            class Broken(io.BytesIO):
                def read(self,*args,**kwargs):raise requests.ReadTimeout(SECRET)
            response.raw=Broken(body)
        return response


class RefreshWire(Wire):
    """Observed aggregate shape: one exact URL per row, repeated task ID."""
    def __init__(self, mode='aggregate', fault=None):
        super().__init__(); self.mode=mode; self.fault=fault; self.rows=[]; self.submissions=0

    def send(self, request, **kwargs):
        q=parse_qs(urlsplit(request.url).query,keep_blank_values=True)
        if request.body:
            q.update(parse_qs(request.body.decode() if isinstance(request.body,bytes) else request.body))
        action=q.get('Action',[''])[0]
        if action not in ('RefreshObjectCaches','DescribeRefreshTaskById'):
            return super().send(request,**kwargs)
        self.requests.append((request,kwargs,action,q)); result={'RequestId':'fixture-aggregate-request'}
        if action=='RefreshObjectCaches':
            self.submissions+=1; ids=[]
            for url in q['ObjectPath'][0].split('\n'):
                number=len(self.rows)
                task=str(33643947491 if self.mode=='aggregate' else 501+number//2 if self.mode=='pairs' else 501+number)
                if task not in ids:ids.append(task)
                self.rows.append(dict(TaskId=task,ObjectPath=url,ObjectType='file',Status='Complete',Process='100%'))
            if self.fault=='submit_timeout' and self.submissions==2:raise requests.ReadTimeout(TOKEN)
            if self.fault=='submit_ids':ids=['0']
            if self.fault=='duplicate_ids':ids+=ids
            result['RefreshTaskId']=','.join(ids)
        else:
            self.polls+=1; group=q['TaskId'][0].split(',')
            rows=copy.deepcopy([row for row in self.rows if row['TaskId'] in group])
            if self.fault=='missing':rows=rows[:-1]
            elif self.fault=='duplicate':rows+=rows[:1]
            elif self.fault=='foreign_id':rows[0]['TaskId']='999999'
            elif self.fault=='foreign_url':rows[0]['ObjectPath']='https://packages.githubim.com/foreign'
            elif self.fault=='unused_id':rows=[row for row in rows if row['TaskId']!=group[-1]]
            elif self.fault=='directory':rows[0]['ObjectType']='directory'
            elif self.fault=='newline_shape':rows[0]['ObjectPath']+='\nhttps://packages.githubim.com/foreign'
            elif self.fault=='mapping_drift':
                if self.polls==1:rows[0]['Status']='Refreshing'
                else:rows[0]['TaskId'],rows[-1]['TaskId']=rows[-1]['TaskId'],rows[0]['TaskId']
            elif self.fault=='query_timeout':raise requests.ReadTimeout(TOKEN)
            result.update(Tasks=rows,TotalCount=len(rows)+(1 if self.fault=='total' else 0))
            if self.fault=='missing_total':del result['TotalCount']
            if self.fault=='boolean_total':result['TotalCount']=True
            if self.fault=='string_total':result['TotalCount']=str(len(rows))
            if self.fault=='dict_tasks':result['Tasks']={}
            if self.fault=='nonobject_row':result['Tasks']=['bad']
            if self.fault=='other_group_id':result['Tasks'][0]['TaskId']='521'
        response=requests.Response(); response.status_code=200; response.headers['Content-Type']='application/json'
        response.request=request; response.url=request.url; response.raw=io.BytesIO(json.dumps(result).encode()); response._content=False
        return response


class ProviderContract(unittest.TestCase):
    def setUp(self):
        self.socket_guard=mock.patch.object(socket.socket,'connect',side_effect=AssertionError('provider network forbidden'))
        self.socket_guard.start();self.addCleanup(self.socket_guard.stop)
        spec=importlib.util.spec_from_file_location('provider_contract',ROOT/'scripts/aliyun-object-storage-provider.py')
        self.p=importlib.util.module_from_spec(spec);spec.loader.exec_module(self.p)
        self.w=Wire();self.temp=tempfile.TemporaryDirectory();self.addCleanup(self.temp.cleanup)
        self.path=Path(self.temp.name)/'object';self.path.write_bytes(DATA)
        self.client=self.p.Provider(resource=RESOURCE,access_key_id='FIXTURE-AK',access_key_secret=SECRET,security_token=TOKEN,http_adapter=self.w)
        self.addCleanup(self.client.close)

    def writes(self):return [r for r in self.w.requests if r[0].method=='PUT' or r[2] in ('BatchSetCdnDomainConfig','RefreshObjectCaches')]

    def test_identity_versioning_and_sdk_wire_are_bound(self):
        result=self.client.observe_resource()
        self.assertEqual(result['versioning'],'never_enabled');self.assertFalse(result['production_enabled'])
        self.assertTrue(any(r[2]=='GetCallerIdentity' for r in self.w.requests))
        for req,kw,_,_ in self.w.requests:
            self.assertEqual(urlsplit(req.url).scheme,'https');self.assertFalse(kw.get('allow_redirects',False))
            self.assertTrue(kw['verify']);self.assertTrue(all(v is None for v in kw.get('proxies',{}).values()))
        self.assertFalse(self.writes())

    def test_versioning_ambiguous_inputs_all_rejected(self):
        for raw in [b'',b'<Other/>',b'<VersioningConfiguration xmlns="wrong"/>',b'<VersioningConfiguration><Status/></VersioningConfiguration>',b'<VersioningConfiguration><Status>Enabled</Status></VersioningConfiguration>',b'<VersioningConfiguration><Status>Suspended</Status></VersioningConfiguration>',b'<VersioningConfiguration><Status>unknown</Status></VersioningConfiguration>',b'<VersioningConfiguration><Unknown/></VersioningConfiguration>',b'<!DOCTYPE a><VersioningConfiguration/>']:
            with self.subTest(raw=raw):
                self.w.versioning=raw
                with self.assertRaises(self.p.ProviderError):self.client.observe_resource()
        self.assertFalse(self.writes())

    def test_resource_drift_and_arbitrary_endpoints_are_rejected(self):
        for field,value in [('owner','9'),('principal','acs:ram::other:role/no')]:
            with self.subTest(field=field):
                old=getattr(self.w,field);setattr(self.w,field,value)
                with self.assertRaises(self.p.ProviderError):self.client.observe_resource()
                setattr(self.w,field,old)
        for changed in ({'endpoint':'http://localhost:9999'},{'domain':'attacker.invalid'},{'region':'x/../y'},{'production_enabled':True}):
            with self.assertRaises(self.p.ProviderError):self.p.Provider(resource={**RESOURCE,**changed},access_key_id='FIXTURE',access_key_secret=SECRET,security_token=TOKEN,http_adapter=self.w)
        self.assertFalse(self.writes())

    def test_create_only_then_complete_get_and_exact_conflict(self):
        result=self.client.put_create_only(KEY,self.path,FACTS)
        self.assertEqual(result['outcome'],'created');self.assertEqual(result['readback']['sha256'],FACTS['sha256'])
        put=next(r[0] for r in self.w.requests if r[0].method=='PUT')
        self.assertEqual(put.headers['x-oss-forbid-overwrite'],'true');self.assertEqual(put.headers['Content-Length'],str(len(DATA)))
        self.assertEqual(put.headers['Cache-Control'],FACTS['cache_control']);self.assertEqual(urlsplit(put.url).path,'/'+KEY)
        self.assertIn('Authorization',put.headers)
        self.assertEqual(self.client.put_create_only(KEY,self.path,FACTS)['outcome'],'already_exists')
        self.assertFalse(result['publication_verified'])

    def test_unknown_put_never_retries_write(self):
        for fault in ('timeout_before','timeout_after','denied','other409'):
            with self.subTest(fault=fault):
                self.w.requests=[];self.w.objects={};self.w.put_fault=fault
                try:result=self.client.put_create_only(KEY,self.path,FACTS)
                except self.p.ProviderError as e:
                    self.assertNotIn(SECRET,str(e));self.assertNotIn(TOKEN,str(e));self.assertNotEqual(fault,'timeout_after')
                else:self.assertEqual(result['outcome'],'unknown_readback_verified');self.assertEqual(fault,'timeout_after')
                self.assertEqual(sum(r[0].method=='PUT' for r in self.w.requests),1)

    def test_readback_needs_full_eof_size_hash_cache_without_compression(self):
        self.w.objects[KEY]=(DATA,FACTS['cache_control'])
        for fault in ('truncated','overlong','digest','cache','encoding','interrupted'):
            with self.subTest(fault=fault):
                self.w.get_fault=fault
                with self.assertRaises(self.p.ProviderError):self.client.read_object(KEY,FACTS)

    def test_object_local_drift_and_key_escape_prevent_writes(self):
        for key,facts in [(KEY,{**FACTS,'sha256':'0'*64}),('../bad',FACTS),('public/a%2fb',FACTS),('public/a?b',FACTS),(KEY,{**FACTS,'size':5*1024**3+1})]:
            with self.assertRaises(self.p.ProviderError):self.client.put_create_only(key,self.path,facts)
        self.assertFalse(self.writes())

    def test_explicit_transport_does_not_consume_proxy_or_debug_environment(self):
        with mock.patch.dict(os.environ,{'HTTPS_PROXY':'http://credential-exfil.invalid','DEBUG':'sdk'}):
            with self.assertRaises(self.p.ProviderError):self.client.observe_resource()
        self.assertFalse(self.w.requests)

    def test_exact_rewrite_preserves_alias_target_and_priority(self):
        values=[route('/'),route(),route('/snapshots/stable/B/Packages/A.rpm',KEY),route('/apt/by-hash/SHA512/'+'a'*128,'public/apt/by-hash/SHA512/'+'a'*128)]
        functions=self.p.compile_rewrites(values)
        self.assertEqual(len(functions),4)
        for number,(original,item) in enumerate(zip(values,functions),1):
            self.assertEqual(item['functionName'],'back_to_origin_url_rewrite')
            args={x['argName']:x['argValue'] for x in item['functionArgs']}
            self.assertEqual(args['target_url'],'/'+original['object_key']);self.assertEqual(args['flag'],'break');self.assertEqual(args['sequence'],str(number))
            self.assertIsNotNone(re.fullmatch(args['source_url'].replace(r'\z',r'\Z'),original['public_uri']))
            for neighbour in (original['public_uri']+'x',original['public_uri']+'/',original['public_uri']+'?x=1',original['public_uri']+'\n'):
                self.assertIsNone(re.fullmatch(args['source_url'].replace(r'\z',r'\Z'),neighbour))

    def test_wire_limits_and_ambiguous_route_inputs_are_rejected(self):
        for rows in ([route('/p'+str(i)) for i in range(51)],[route('/a%2fb')],[route('/a/../b')],[route('//x')],[route('/x?z=1')],[route('/a'*1500)],[route(),route(key='public/other')]):
            with self.assertRaises(self.p.ProviderError):self.p.compile_rewrites(rows)
        self.assertEqual(len(self.p.compile_rewrites([route(),route()])),1)

    def test_config_snapshot_includes_unrelated_rules_and_rewrite_order(self):
        first=self.client.read_configuration()
        self.w.configs.append(config('gzip',{'enable':'on'},99))
        second=self.client.read_configuration();self.assertNotEqual(first['sha256'],second['sha256'])
        self.w.configs[-1]['FunctionArgs']['FunctionArg'][0]['ArgValue']='off'
        self.assertNotEqual(second['sha256'],self.client.read_configuration()['sha256'])

    def test_config_exact_batches_and_readback(self):
        rows=[route('/p'+str(i)) for i in range(21)]
        before=self.client.read_configuration()
        result=self.client.apply_rewrites(rows,before=before,managed_ids=[])
        self.assertEqual(result['state'],'readback_verified');self.assertFalse(result['publication_verified'])
        batches=[json.loads(r[3]['Functions'][0]) for r in self.w.requests if r[2]=='BatchSetCdnDomainConfig']
        self.assertEqual(list(map(len,batches)),[20,1]);self.assertEqual(len(self.w.configs),25)
        self.assertEqual(result['before_sha256'],before['sha256']);self.assertIsNotNone(result['after_sha256'])

    def test_bad_configid_partial_and_timeout_keep_truth(self):
        for fault in ('zero','missing','duplicate','partial','timeout','wrongid'):
            with self.subTest(fault=fault):
                self.w.configs=self.w.configs[:4];self.w.batch_fault=fault
                before=self.client.read_configuration()
                result=self.client.apply_rewrites([route('/a'),route('/b')],before=before,managed_ids=[])
                self.assertEqual(result['state'],'incomplete');self.assertTrue(result['write_attempted'])
                self.assertIsNotNone(result['after_sha256']);self.assertFalse(result['publication_verified'])
                self.assertNotIn(SECRET,json.dumps(result));self.assertNotIn(TOKEN,json.dumps(result))

    def test_unmanaged_rewrite_deletion_group_and_drift_fail_before_apply(self):
        for mutation in ('unmanaged','delete','group','drift'):
            with self.subTest(mutation=mutation):
                self.w.configs=self.w.configs[:4]
                if mutation in ('unmanaged','delete'):
                    self.w.configs.append(config('back_to_origin_url_rewrite',{'source_url':'^/old$','target_url':'/public/old','flag':'break','sequence':'1'},90))
                if mutation=='group':self.w.configs[0]['ParentId']='9'
                before=self.client.read_configuration()
                if mutation=='drift':self.w.configs[0]['FunctionArgs']['FunctionArg'][0]['ArgValue']='http'
                self.w.requests=[]
                with self.assertRaises(self.p.ProviderError):self.client.apply_rewrites([route()],before=before,managed_ids=[90] if mutation=='delete' else [])
                self.assertFalse(self.writes())

    def test_full_configuration_observation_tampering_rejects_before_write(self):
        baseline=copy.deepcopy(self.w.configs)
        for fault in ('hidden_rewrite','hidden_rehashed','hidden_unknown','reordered','domain','missing','extra','resource'):
            with self.subTest(fault=fault):
                self.w.configs=copy.deepcopy(baseline)
                if fault in ('hidden_rewrite','hidden_rehashed'):
                    self.w.configs.append(config(self.p.FUNCTION,{'source_url':r'^/old\z','target_url':'/public/old','flag':'break','sequence':'1'},90))
                if fault=='hidden_unknown':self.w.configs.append(config('gzip',{'enable':'on'},99))
                before=self.client.read_configuration()
                if fault.startswith('hidden'):before['configs']=before['configs'][:4]
                if fault=='hidden_rehashed':before['sha256']=self.p.digest({k:v for k,v in before.items() if k!='sha256'})
                if fault=='reordered':before['configs'].reverse()
                if fault=='domain':before['domain']='foreign.invalid'
                if fault=='missing':before.pop('domain')
                if fault=='extra':before['unbound_control']='claim'
                if fault=='resource':before['resource_sha256']='0'*64
                self.w.requests.clear()
                with self.assertRaises(self.p.ProviderError):self.client.apply_rewrites([route()],before=before,managed_ids=[])
                self.assertFalse(self.writes())

    def test_configuration_observation_cannot_cross_explicit_resource_binding(self):
        before=self.client.read_configuration()
        resource={**RESOURCE,'principal_arn':'acs:ram::1234567890123456:role/other'}
        self.w.principal=resource['principal_arn']
        other=self.p.Provider(resource=resource,access_key_id='FIXTURE-AK',access_key_secret=SECRET,security_token=TOKEN,http_adapter=self.w)
        self.addCleanup(other.close);self.w.requests.clear()
        with self.assertRaises(self.p.ProviderError):other.apply_rewrites([route()],before=before,managed_ids=[])
        self.assertFalse(self.writes())

    def test_exact_observation_allows_only_its_managed_rewrite_update(self):
        self.w.configs.append(config(self.p.FUNCTION,{'source_url':r'^/repo\z','target_url':'/public/old','flag':'break','sequence':'1'},90))
        before=self.client.read_configuration()
        result=self.client.apply_rewrites([route()],before=before,managed_ids=[90])
        self.assertEqual(result['state'],'readback_verified')
        batch=next(r for r in self.w.requests if r[2]=='BatchSetCdnDomainConfig')
        self.assertEqual(json.loads(batch[3]['Functions'][0])[0]['configId'],'90')
        self.assertEqual(before['configs'][-1]['FunctionArgs']['FunctionArg'][1]['ArgValue'],'/public/old')

    def test_debug_logging_rejected_before_sdk_credentials_enter_logs(self):
        core=logging.getLogger('aliyunsdkcore.client');parent=logging.getLogger('aliyunsdkcore');root=logging.getLogger()
        loggers=(core,parent,root)
        original=[(x.level,x.propagate,x.disabled) for x in loggers]
        stream=io.StringIO();handler=logging.StreamHandler(stream);core.addHandler(handler)
        self.w.objects[KEY]=(DATA,FACTS['cache_control'])
        before=self.client.read_configuration()
        operations=(self.client.observe_resource,self.client.read_configuration,
                    lambda:self.client.read_object(KEY,FACTS),lambda:self.client.put_create_only(KEY,self.path,FACTS),
                    lambda:self.client.apply_rewrites([route()],before=before,managed_ids=[]),
                    lambda:self.client.refresh(['https://packages.githubim.com/repo'],max_polls=1,poll_interval=0))
        try:
            for mode in ('client','parent','root','environment'):
                core.setLevel(logging.NOTSET);parent.setLevel(logging.NOTSET);root.setLevel(logging.WARNING)
                for logger in loggers:logger.disabled=False
                core.propagate=False
                if mode!='environment':loggers[('client','parent','root').index(mode)].setLevel(logging.DEBUG)
                with mock.patch.dict(os.environ,{'DEBUG':'sdk' if mode=='environment' else ''}):
                    expected=[(x.level,x.propagate,x.disabled,tuple(x.handlers)) for x in loggers]
                    for number,operation in enumerate(operations):
                        with self.subTest(mode=mode,operation=number):
                            stream.seek(0);stream.truncate();self.w.requests.clear()
                            with self.assertRaises(self.p.ProviderError):operation()
                            self.assertEqual([(x.level,x.propagate,x.disabled,tuple(x.handlers)) for x in loggers],expected)
                            self.assertFalse(self.w.requests)
                            for credential in (TOKEN,SECRET,'FIXTURE-AK'):self.assertNotIn(credential,stream.getvalue())
        finally:
            core.removeHandler(handler)
            for logger,(level,propagate,disabled) in zip(loggers,original):
                logger.setLevel(level);logger.propagate=propagate;logger.disabled=disabled

    def test_debug_logging_rejected_before_sdk_client_construction(self):
        logger=logging.getLogger('aliyunsdkcore.client');old=logger.level
        try:
            logger.setLevel(logging.DEBUG)
            with self.assertRaises(self.p.ProviderError):
                client=self.p.Provider(resource=RESOURCE,access_key_id='FIXTURE-AK',access_key_secret=SECRET,security_token=TOKEN,http_adapter=self.w)
                client.close()
        finally:logger.setLevel(old)
        self.assertFalse(self.w.requests)

    def test_refresh_only_exact_task_ids_in_bounded_batches(self):
        urls=['https://packages.githubim.com/p'+str(i) for i in range(11)]
        result=self.client.refresh(urls,max_polls=2,poll_interval=0)
        self.assertEqual(result['state'],'tasks_complete');self.assertEqual(len(result['task_ids']),11)
        polls=[r for r in self.w.requests if r[2]=='DescribeRefreshTaskById']
        self.assertEqual([len(r[3]['TaskId'][0].split(',')) for r in polls],[10,1])
        self.assertEqual(set(result['task_ids']),set(self.w.tasks))

    def test_refresh_rejects_other_tasks_and_noncomplete_states(self):
        for fault in ('Failed','Canceled','Timeout','Refreshing','foreign','url','type','missing','duplicate'):
            with self.subTest(fault=fault):
                self.w.refresh_fault=fault;self.w.requests=[];self.w.polls=0
                with self.assertRaises(self.p.ProviderError):self.client.refresh(['https://packages.githubim.com/repo'],max_polls=2,poll_interval=0)
                self.assertLessEqual(self.w.polls,2)
        self.w.refresh_fault='progress';self.w.polls=0
        self.assertEqual(self.client.refresh(['https://packages.githubim.com/repo'],max_polls=2,poll_interval=0)['state'],'tasks_complete')

    def test_refresh_foreign_or_ambiguous_file_urls_rejected_without_write(self):
        # Root/trailing slash File support is covered by the observed aggregate test.
        for urls in (['https://other.invalid/repo'],['https://packages.githubim.com/repo?x=1'],['https://packages.githubim.com/repo']*2):
            with self.assertRaises(self.p.ProviderError):self.client.refresh(urls,max_polls=1,poll_interval=0)
        self.assertFalse(self.writes())

    def refresh_client(self, mode='aggregate', fault=None):
        wire=RefreshWire(mode,fault)
        client=self.p.Provider(resource=RESOURCE,access_key_id='FIXTURE-AK',access_key_secret=SECRET,security_token=TOKEN,http_adapter=wire)
        self.addCleanup(client.close);return client,wire

    def test_refresh_observed_four_file_urls_share_one_task_including_root(self):
        client,wire=self.refresh_client()
        urls=['https://packages.githubim.com'+path for path in ('/','/zh/','/en/','/api/search')]
        result=client.refresh(urls,max_polls=1,poll_interval=0)
        self.assertEqual(result['state'],'tasks_complete');self.assertEqual(result['task_ids'],['33643947491'])
        self.assertEqual([r[3]['ObjectType'] for r in wire.requests if r[2]=='RefreshObjectCaches'],[['File']])
        self.assertEqual([row['ObjectPath'] for row in wire.rows],urls)
        for key in self.p.FALSE_FIELDS:self.assertFalse(result[key])

    def test_refresh_all_native_urls_submit_before_aggregate_query_without_omission(self):
        client,wire=self.refresh_client()
        urls=['https://packages.githubim.com'+row['public_uri'] for row in native_routes_fixture()['generations']['A']['delivery.json']['routes']]
        result=client.refresh(urls,max_polls=1,poll_interval=0)
        submits=[r for r in wire.requests if r[2]=='RefreshObjectCaches']
        self.assertEqual([len(r[3]['ObjectPath'][0].split('\n')) for r in submits],[50,50,50,28])
        self.assertEqual([url for r in submits for url in r[3]['ObjectPath'][0].split('\n')],urls)
        self.assertEqual([r[2] for r in wire.requests if r[2] in ('RefreshObjectCaches','DescribeRefreshTaskById')],['RefreshObjectCaches']*4+['DescribeRefreshTaskById'])
        self.assertEqual(result['task_ids'],['33643947491'])

    def test_refresh_aggregate_pairs_and_legacy_independent_ids_keep_query_limit(self):
        urls=['https://packages.githubim.com/p'+str(i) for i in range(52)]
        for mode,count,batches in (('pairs',26,[10,10,6]),('unique',52,[10,10,10,10,10,2])):
            with self.subTest(mode=mode):
                client,wire=self.refresh_client(mode)
                result=client.refresh(urls,max_polls=1,poll_interval=0)
                self.assertEqual(result['task_ids'],[str(501+i) for i in range(count)])
                self.assertEqual([len(r[3]['TaskId'][0].split(',')) for r in wire.requests if r[2]=='DescribeRefreshTaskById'],batches)

    def test_refresh_full_set_bounds_and_all_urls_preflight_before_any_submit(self):
        client,wire=self.refresh_client();prefix='https://packages.githubim.com'
        for urls in ([],[prefix+'/p'+str(i) for i in range(1001)],
                     [prefix+'/ok']*2,*([prefix+'/ok',prefix+path] for path in ('//','/a//','/a/../','/a/./','/a?x=1','/a?','/a#f','/a#','/a\n','/a\t','/ a','/%2f'))):
            with self.subTest(urls=urls[:2]),self.assertRaises(self.p.ProviderError):client.refresh(urls,max_polls=1,poll_interval=0)
        self.assertFalse([r for r in wire.requests if r[2]=='RefreshObjectCaches'])
        urls=[prefix+'/p'+str(i) for i in range(1000)]
        self.assertEqual(client.refresh(urls,max_polls=1,poll_interval=0)['state'],'tasks_complete')
        self.assertEqual(wire.submissions,20)

    def test_refresh_aggregate_coverage_totalcount_and_foreign_rows_fail_closed(self):
        urls=['https://packages.githubim.com/p'+str(i) for i in range(4)]
        for fault in ('missing','duplicate','foreign_id','foreign_url','unused_id','directory','newline_shape','total','missing_total','boolean_total','string_total','dict_tasks','nonobject_row','mapping_drift'):
            with self.subTest(fault=fault):
                client,wire=self.refresh_client('pairs',fault)
                with self.assertRaises(self.p.ProviderError):client.refresh(urls,max_polls=2,poll_interval=0)
                self.assertEqual(wire.submissions,1);self.assertLessEqual(wire.polls,2)

    def test_refresh_rejects_ids_from_another_query_group(self):
        client,wire=self.refresh_client('pairs','other_group_id')
        urls=['https://packages.githubim.com/p'+str(i) for i in range(52)]
        with self.assertRaises(self.p.ProviderError):client.refresh(urls,max_polls=1,poll_interval=0)
        self.assertEqual(wire.submissions,2);self.assertEqual(wire.polls,1)

    def test_refresh_unknown_submit_and_query_never_repeat_sideeffects(self):
        urls=['https://packages.githubim.com/p'+str(i) for i in range(51)]
        for fault,count in (('submit_timeout',2),('query_timeout',2),('submit_ids',1),('duplicate_ids',1)):
            with self.subTest(fault=fault):
                client,wire=self.refresh_client('aggregate',fault)
                with self.assertRaises(self.p.ProviderError):client.refresh(urls,max_polls=2,poll_interval=0)
                self.assertEqual(wire.submissions,count)


    def test_native_route_fixture_has_exact_public_provenance_and_object_facts(self):
        path=ROOT/'tests/provider_sdk/fixtures/native-composite-routes.json'
        self.assertEqual(hashlib.sha256(path.read_bytes()).hexdigest(),'b135eddc8b6691dfa983e2c5ed606aaa4ca3f877d6d03b391ef3949b79026ef8')
        fixture=native_routes_fixture();self.assertTrue(fixture['test_only']);self.assertFalse(fixture['production_authorization'])
        self.assertEqual(fixture['readback_manifest_sha256'],'fecd657aa0440079faa851f7450893d00aebb872799fda947b1036f7d21e44a6')
        manifest=fixture['readback_manifest']
        self.assertEqual(json.loads(fixture['readback_manifest_json']),manifest)
        self.assertEqual(hashlib.sha256(fixture['readback_manifest_json'].encode()).hexdigest(),fixture['readback_manifest_sha256'])
        hashes={x['path']:x['sha256'] for x in manifest['files']}
        self.assertEqual(fixture['source_binding']['head'],'19ce29fb2233b3039e29c503963e27bd1fab972a')
        self.assertEqual(fixture['source_binding']['fixture_manifest_sha256'],'d9ea88a690de4c82dfe7316cc3d6c7a8f8671a70963462c886f6bd0be7471ca0')
        for label,current in [('A',4242),('B',4243)]:
            docs=fixture['generations'][label];delivery=docs['delivery.json'];plan=docs['transport-plan.json']
            for name,value in {**docs,'routes.json':delivery['routes']}.items():
                raw=(json.dumps(value,sort_keys=True,separators=(',',':'))+'\n').encode()
                self.assertEqual(hashlib.sha256(raw).hexdigest(),hashes['first-run/'+label+'/'+name])
            self.assertEqual(delivery['plan_sha256'],hashes['first-run/'+label+'/transport-plan.json'])
            self.assertEqual(delivery['inputs_sha256'],hashes['first-run/'+label+'/inputs.json'])
            self.assertEqual(plan['current_audit_release_ids'],{'preview':10,'stable':current})
            self.assertEqual([x['audit_release_id'] for x in delivery['client_requests']],[10,current])
            self.assertEqual({x['audit_release_id'] for x in delivery['snapshots']},{10,4242,4243})
            self.assertEqual(len(delivery['routes']),178)
            objects={x['object_key']:x for x in delivery['objects']};self.assertEqual(len(objects),153)
            for row in delivery['routes']:
                self.assertEqual({k:row[k] for k in FACTS},{k:objects[row['object_key']][k] for k in FACTS})
            for key in ('production_enabled','publication_verified','cloud_verified'):self.assertFalse(delivery[key])
            for key in ('activation_authorized','github_provenance_verified','control_commit_verified'):
                self.assertFalse(delivery['planner_verification'][key])

    def test_native_generations_compile_to_exact_finite_maps_without_mutation(self):
        for label,docs in native_routes_fixture()['generations'].items():
            with self.subTest(generation=label):
                rows=docs['delivery.json']['routes'];original=copy.deepcopy(rows)
                functions=self.p.compile_rewrites(rows)
                self.assertEqual(len(functions),17)
                self.assertEqual(max(len(x['functionArgs'][0]['argValue'].encode()) for x in functions),1017)
                self.assertEqual(inverse_finite_rules(functions),{x['public_uri']:'/'+x['object_key'] for x in rows})
                self.assertEqual(rows,original)
                patterns=[re.compile(x['functionArgs'][0]['argValue'].replace(r'\z',r'\Z')) for x in functions]
                expected={x['public_uri'] for x in rows}
                for uri in expected:
                    matches=[p.fullmatch(uri) for p in patterns if p.fullmatch(uri)]
                    self.assertEqual(len(matches),1)
                    for other in (uri+'x',uri+'/',uri+'\n',uri+'?x=1',uri+'#x',uri.replace('/','//',1),uri+'%00',uri+'%252f',uri+'/../x'):
                        if other not in expected:self.assertFalse(any(p.fullmatch(other) for p in patterns),other)

    def test_literal_fifty_and_fifty_one_duplicate_inputs_keep_original_bytes_order(self):
        rows=[route('/p'+str(i)) for i in range(50)]
        expected=[{'functionName':'back_to_origin_url_rewrite','functionArgs':[
            {'argName':'source_url','argValue':'^/p'+str(i)+r'\z'},
            {'argName':'target_url','argValue':'/'+rows[i]['object_key']},
            {'argName':'flag','argValue':'break'},{'argName':'sequence','argValue':str(i+1)}]} for i in range(50)]
        for inputs in (rows,rows[:7]+[copy.deepcopy(rows[3])]+rows[7:]):
            self.assertEqual(json.dumps(self.p.compile_rewrites(inputs)),json.dumps(expected))
        with self.assertRaises(self.p.ProviderError):self.p.compile_rewrites(rows+[route('/p50')])

    def test_finite_groups_keep_unknown_mappings_literal_and_enforce_output_fifty(self):
        aliases=[route('/a'+str(i),'public/a'+str(i)) for i in range(51)]
        unknown=[route('/p'+str(i)) for i in range(49)]
        functions=self.p.compile_rewrites(aliases+unknown)
        self.assertEqual(len(functions),50)
        self.assertEqual(inverse_finite_rules(functions),{x['public_uri']:'/'+x['object_key'] for x in aliases+unknown})
        with self.assertRaises(self.p.ProviderError):self.p.compile_rewrites(aliases+unknown+[route('/p49')])
        prefix='/snapshots/preview/not-a-release/'
        with self.assertRaises(self.p.ProviderError):
            self.p.compile_rewrites([route(prefix+str(i),(prefix+str(i))[1:]) for i in range(51)])

    def test_group_expression_1024_accepts_and_1025_singleton_rejects(self):
        prefix='/snapshots/preview/r10-'+'a'*64+'/'
        aliases=[route('/a'+str(i),'public/a'+str(i)) for i in range(51)]
        suffix='x'*(1024-len(re.escape(prefix))-5);uri=prefix+suffix
        functions=self.p.compile_rewrites(aliases+[route(uri,uri[1:])])
        self.assertEqual(max(len(x['functionArgs'][0]['argValue'].encode()) for x in functions),1024)
        self.assertEqual(inverse_finite_rules(functions)[uri],uri)
        with self.assertRaises(self.p.ProviderError):self.p.compile_rewrites(aliases+[route(uri+'x',uri[1:]+'x')])

    def test_group_chunks_split_before_1025_without_losing_an_alternative(self):
        prefix='/snapshots/stable/r42-'+'b'*64+'/'
        aliases=[route('/a'+str(i),'public/a'+str(i)) for i in range(51)]
        first='a'*400;second='b'*(1024-len(re.escape(prefix))-6-len(first))
        for extra,wanted in [('',2),('b',3)]:
            rows=aliases+[route(prefix+s,(prefix+s)[1:]) for s in (first,second+extra)]
            functions=self.p.compile_rewrites(rows);self.assertEqual(len(functions),wanted)
            self.assertEqual(inverse_finite_rules(functions),{x['public_uri']:'/'+x['object_key'] for x in rows})
            self.assertLessEqual(max(len(x['functionArgs'][0]['argValue'].encode()) for x in functions),1024)

    def test_literal_and_group_escaping_reject_all_unsafe_inputs_and_conflicts(self):
        rows=[route('/apt/a.b+c-d~_/file'+str(i),'public/apt/a.b+c-d~_/file'+str(i)) for i in range(51)]
        original=copy.deepcopy(rows);functions=self.p.compile_rewrites(rows+[copy.deepcopy(rows[0])])
        self.assertEqual(inverse_finite_rules(functions),{x['public_uri']:'/'+x['object_key'] for x in rows})
        self.assertEqual(rows,original)
        self.assertEqual(inverse_finite_rules(self.p.compile_rewrites(rows[:2])),{x['public_uri']:'/'+x['object_key'] for x in rows[:2]})
        with self.assertRaises(self.p.ProviderError):self.p.compile_rewrites([])
        for changes in ({'object_key':'public/other'},{'size':2},{'sha256':'0'*64},{'cache_control':'other'}):
            with self.subTest(changes=changes),self.assertRaises(self.p.ProviderError):
                self.p.compile_rewrites(rows+[{**rows[0],**changes}])
        for uri in ('/a/../b','/a/./b','//x','/a//b','/x?z=1','/x#z','/x\n','/a%2fb','/a%252fb','/a(b)','/a|b','/a$b'):
            with self.subTest(uri=uri),self.assertRaises(self.p.ProviderError):self.p.compile_rewrites(rows+[route(uri)])
        with self.assertRaises(self.p.ProviderError):self.p.compile_rewrites(rows+[route('/unknown','foreign/a')])

    def test_distinct_current_retained_and_alias_prefixes_never_cross(self):
        rows=native_routes_fixture()['generations']['A']['delivery.json']['routes']
        functions=self.p.compile_rewrites(rows)
        mapped=inverse_finite_rules(functions)
        self.assertIn('/snapshots/stable/r4242-',mapped['/apt/dists/stable/InRelease'])
        for row in rows:
            self.assertEqual(mapped[row['public_uri']],'/'+row['object_key'])
        # Same physical suffix in another generation is a distinct target group.
        uri='/extra';target='snapshots/stable/r99-'+'c'*64+'/site/extra'
        changed=rows+[route(uri,target)]
        self.assertEqual(inverse_finite_rules(self.p.compile_rewrites(changed))[uri],'/'+target)

    def test_native_sdk_configuration_reuses_source_ids_across_a_b(self):
        generations=native_routes_fixture()['generations'];previous=None
        for label in ('A','B'):
            rows=generations[label]['delivery.json']['routes'];before=self.client.read_configuration()
            ids=[x['ConfigId'] for x in self.w.configs if x['FunctionName']==self.p.FUNCTION]
            self.w.requests.clear();result=self.client.apply_rewrites(rows,before=before,managed_ids=ids)
            self.assertEqual(result['state'],'readback_verified');self.assertEqual(result['batches_attempted'],1)
            for key in self.p.FALSE_FIELDS:self.assertFalse(result[key])
            batches=[r for r in self.w.requests if r[2]=='BatchSetCdnDomainConfig'];self.assertEqual(len(batches),1)
            self.assertEqual(batches[0][3]['DomainNames'],[RESOURCE['domain']])
            functions=json.loads(batches[0][3]['Functions'][0]);self.assertEqual(len(functions),17)
            self.assertEqual(inverse_finite_rules(functions),{x['public_uri']:'/'+x['object_key'] for x in rows})
            actual={x['FunctionArgs']['FunctionArg'][0]['ArgValue']:x for x in self.w.configs if x['FunctionName']==self.p.FUNCTION}
            if previous is not None:
                self.assertEqual(set(actual),set(previous))
                self.assertEqual({s:v['ConfigId'] for s,v in actual.items()},{s:v['ConfigId'] for s,v in previous.items()})
                self.assertEqual(sum(actual[s]['FunctionArgs']!=previous[s]['FunctionArgs'] for s in actual),2)
                self.assertEqual({x['configId'] for x in functions},set(ids))
            previous=copy.deepcopy(actual)
            with self.assertRaises(self.p.ProviderError):self.client.activate()
            self.assertFalse(any(r[0].method=='PUT' or r[2]=='RefreshObjectCaches' for r in self.w.requests))

    def test_grouping_or_rechunking_cannot_delete_preexisting_sources(self):
        real=native_routes_fixture()['generations']['A']['delivery.json']['routes']
        aliases=[route('/a'+str(i),'public/a'+str(i)) for i in range(51)]
        for before_rows,after_rows in [(real,real[1:]),(aliases[:50],aliases),(aliases,aliases+[route('/a51','public/a51')])]:
            self.w.configs=self.w.configs[:4]
            functions=self.p.compile_rewrites(before_rows)
            self.w.configs += [config(self.p.FUNCTION,{x['argName']:x['argValue'] for x in f['functionArgs']},100+i) for i,f in enumerate(functions)]
            before=self.client.read_configuration();ids=[x['ConfigId'] for x in self.w.configs[4:]];self.w.requests.clear()
            with self.assertRaisesRegex(self.p.ProviderError,'deletion'):
                self.client.apply_rewrites(after_rows,before=before,managed_ids=ids)
            self.assertFalse(self.writes())

    def test_grouped_sdk_unknown_outcomes_remain_incomplete_without_retry(self):
        rows=native_routes_fixture()['generations']['A']['delivery.json']['routes']
        for fault in ('zero','missing','duplicate','partial','timeout','wrongid'):
            with self.subTest(fault=fault):
                self.w.configs=self.w.configs[:4];self.w.batch_fault=fault;before=self.client.read_configuration();self.w.requests.clear()
                result=self.client.apply_rewrites(rows,before=before,managed_ids=[])
                self.assertEqual(result['state'],'incomplete');self.assertTrue(result['write_attempted'])
                self.assertIsNotNone(result['after_sha256']);self.assertFalse(result['wire_equivalence_verified'])
                self.assertEqual(len([r for r in self.w.requests if r[2]=='BatchSetCdnDomainConfig']),1)
                self.assertFalse(any(r[2]=='RefreshObjectCaches' for r in self.w.requests))

    def test_grouped_observation_tampering_and_drift_prevent_all_writes(self):
        rows=native_routes_fixture()['generations']['A']['delivery.json']['routes'];baseline=copy.deepcopy(self.w.configs)
        for fault in ('body','domain','resource','unmanaged','drift','cap'):
            with self.subTest(fault=fault):
                self.w.configs=copy.deepcopy(baseline)
                if fault=='unmanaged':self.w.configs.append(config(self.p.FUNCTION,{'source_url':r'^/old\z','target_url':'/public/old','flag':'break','sequence':'1'},90))
                before=self.client.read_configuration()
                if fault=='body':before['configs']=[]
                if fault=='domain':before['domain']='foreign.invalid'
                if fault=='resource':before['resource_sha256']='0'*64
                if fault=='drift':self.w.configs[0]['FunctionArgs']['FunctionArg'][0]['ArgValue']='off'
                self.w.requests.clear()
                values=[route('/p'+str(i)) for i in range(51)] if fault=='cap' else rows
                with self.assertRaises(self.p.ProviderError):self.client.apply_rewrites(values,before=before,managed_ids=[])
                self.assertFalse(self.writes())


    def test_native_sdk_driver_preserves_full_exact_file_refresh_coverage(self):
        spec=importlib.util.spec_from_file_location('native_sdk_drill',ROOT/'tests/provider_sdk/native_object_delivery_sdk_drill.py')
        driver=importlib.util.module_from_spec(spec);spec.loader.exec_module(driver)
        rows=native_routes_fixture()['generations']['A']['delivery.json']['routes']
        result=driver.refresh_scope(rows)
        self.assertEqual(result['urls'],['https://packages.githubim.com'+r['public_uri'] for r in rows])
        self.assertEqual(len(result['urls']),178);self.assertEqual(result['object_type'],'File')
        self.assertFalse(result['coverage_verified']);self.assertIn('https://packages.githubim.com/',result['urls'])
        with self.assertRaises(ValueError):driver.refresh_scope(rows+[rows[0]])
        self.assertFalse(self.writes())

    def test_native_sdk_driver_receipt_never_upgrades_fixture_success_to_publication(self):
        spec=importlib.util.spec_from_file_location('native_sdk_drill',ROOT/'tests/provider_sdk/native_object_delivery_sdk_drill.py')
        driver=importlib.util.module_from_spec(spec);spec.loader.exec_module(driver)
        report={'test_only':True,'passed':True,'cases':[{'case':name,'passed':True} for name in driver.CASES],
                'skips':0,'state':'fixture_complete','stage':'sdk_fixture_complete','scratch_removed':True,
                'verification':{key:False for key in driver.FALSE_FIELDS}}
        self.assertEqual(len(driver.CASES),20);driver.validate_report(report)
        for change in ({'cases':[]},{'cases':report['cases'][:-1]},{'cases':report['cases']+[report['cases'][0]]},
                       {'skips':1},{'state':'complete'},{'scratch_removed':False},{'test_only':False}):
            with self.assertRaises(ValueError):driver.validate_report({**report,**change})
        for key in driver.FALSE_FIELDS:
            with self.assertRaises(ValueError):driver.validate_report({**report,'verification':{**report['verification'],key:True}})
        self.assertFalse(self.writes())


if __name__=='__main__':
    import sys
    import importlib.metadata
    import platform
    import subprocess
    names=[name for name in unittest.defaultTestLoader.getTestCaseNames(ProviderContract)]
    result=unittest.TextTestRunner(verbosity=2).run(unittest.defaultTestLoader.loadTestsFromModule(sys.modules[__name__]))
    receipt={'schema':'wukongim.provider_sdk_contract/v1','tests':result.testsRun,'failures':len(result.failures),
             'errors':len(result.errors),'skips':len(result.skipped),'provider_network_allowed':False}
    receipt.update(python=platform.python_version(),test_names=names,
        sdk_versions={name:importlib.metadata.version(name) for name in ('alibabacloud-oss-v2','aliyun-python-sdk-core','aliyun-python-sdk-cdn','aliyun-python-sdk-sts')},
        checkout_sha=subprocess.check_output(['git','rev-parse','HEAD'],cwd=ROOT,text=True).strip(),
        checkout_tree=subprocess.check_output(['git','rev-parse','HEAD^{tree}'],cwd=ROOT,text=True).strip(),
        lock_sha256=hashlib.sha256((ROOT/'requirements-object-storage-provider.lock').read_bytes()).hexdigest())
    if '--receipt' in sys.argv:
        Path(sys.argv[sys.argv.index('--receipt')+1]).write_text(json.dumps(receipt,sort_keys=True)+'\n')
    print(json.dumps(receipt,sort_keys=True))
    sys.exit(0 if result.testsRun>0 and result.wasSuccessful() and not result.skipped else 1)
