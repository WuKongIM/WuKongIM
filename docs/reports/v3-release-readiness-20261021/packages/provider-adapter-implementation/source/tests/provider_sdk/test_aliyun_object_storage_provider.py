"""Official SDK HTTP contracts; no live sockets, cloud credentials or production claim."""
import copy
import hashlib
import importlib.util
import io
import json
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

    def test_refresh_foreign_or_directory_urls_rejected_without_write(self):
        for urls in (['https://other.invalid/repo'],['https://packages.githubim.com/'],['https://packages.githubim.com/repo?x=1'],['https://packages.githubim.com/repo']*2):
            with self.assertRaises(self.p.ProviderError):self.client.refresh(urls,max_polls=1,poll_interval=0)
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
