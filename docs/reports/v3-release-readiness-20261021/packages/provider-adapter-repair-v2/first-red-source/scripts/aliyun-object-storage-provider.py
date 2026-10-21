"""Thin, explicit-credential SDK operations; no production publication entrypoint.

SDK readback is evidence about one operation, not routing equivalence, approval,
public availability or publication. Literal rewrites are review candidates only.
"""
from __future__ import annotations

import copy
import hashlib
import io
import json
import logging
import os
from pathlib import Path
import re
import stat
import tempfile
import time
from urllib.parse import urlsplit, parse_qs
import xml.etree.ElementTree as ET

import requests
import alibabacloud_oss_v2 as oss
from alibabacloud_oss_v2.transport import RequestsHttpClient
from aliyunsdkcore.client import AcsClient
from aliyunsdkcore.auth.credentials import StsTokenCredential
from aliyunsdkcdn.request.v20180510.BatchSetCdnDomainConfigRequest import BatchSetCdnDomainConfigRequest
from aliyunsdkcdn.request.v20180510.DescribeCdnDomainConfigsRequest import DescribeCdnDomainConfigsRequest
from aliyunsdkcdn.request.v20180510.DescribeCdnDomainDetailRequest import DescribeCdnDomainDetailRequest
from aliyunsdkcdn.request.v20180510.RefreshObjectCachesRequest import RefreshObjectCachesRequest
from aliyunsdkcdn.request.v20180510.DescribeRefreshTaskByIdRequest import DescribeRefreshTaskByIdRequest
from aliyunsdksts.request.v20150401.GetCallerIdentityRequest import GetCallerIdentityRequest

MAX_OBJECT = 2 * 1024**3
MAX_RESPONSE = 2 * 1024**2
CHUNK = 64 * 1024
FUNCTION = 'back_to_origin_url_rewrite'
FALSE_FIELDS = dict(production_enabled=False, publication_verified=False, cloud_verified=False,
                    wire_equivalence_verified=False)


class ProviderError(ValueError):
    """A bounded operation failed; raw SDK errors and credentials stay private."""


def require(condition, message):
    if not condition:
        raise ProviderError(message)


def digest(value):
    return hashlib.sha256(json.dumps(value,sort_keys=True,separators=(',',':')).encode()).hexdigest()


def safe_path(value, absolute=False):
    require(isinstance(value,str) and len(value)<=1024 and re.fullmatch(r'[A-Za-z0-9_./+~-]+',value),
            'unsupported literal path')
    path=value[1:] if absolute and value.startswith('/') else value
    require((not absolute or value.startswith('/')) and not path.startswith('/')
            and (not path or all(p not in ('','.', '..') for p in path.split('/'))), 'unsafe path')


def compile_rewrites(routes):
    """Compile a bounded literal candidate; never attest live PCRE/query semantics."""
    require(isinstance(routes,list) and routes, 'routes required')
    seen={}; result=[]
    for row in routes:
        uri=row['public_uri'];key=row['object_key'];safe_path(uri,True);safe_path(key)
        require(key.startswith(('snapshots/','public/','generated/')), 'object namespace differs')
        facts={k:row[k] for k in ('object_key','size','sha256','cache_control')}
        if uri in seen:
            require(seen[uri]==facts, 'conflicting literal route');continue
        seen[uri]=facts
        # PCRE \z is an absolute end anchor; $ also admits a final newline.
        expression='^'+re.escape(uri)+r'\z'
        require(len(expression.encode())<=1024, 'literal expression exceeds local capability bound')
        result.append({'functionName':FUNCTION,'functionArgs':[
            {'argName':'source_url','argValue':expression}, {'argName':'target_url','argValue':'/'+key},
            {'argName':'flag','argValue':'break'}, {'argName':'sequence','argValue':str(len(result)+1)}]})
    require(len(result)<=50, 'domain rewrite limit exceeded')
    return result


class _Session(requests.Session):
    """Per-instance, nonredirecting HTTP boundary shared by official SDK clients."""
    def __init__(self, hosts, adapter=None):
        super().__init__();self.trust_env=False;self.hosts=frozenset(hosts)
        if adapter is not None:
            self.mount('https://',adapter)

    def send(self, request, **kwargs):
        u=urlsplit(request.url)
        require(u.scheme=='https' and u.hostname in self.hosts and u.port in (None,443)
                and u.username is None and u.password is None, 'SDK endpoint outside bound resource')
        require(os.environ.get('DEBUG','').lower()!='sdk' and not logging.getLogger('aliyunsdkcore.client').isEnabledFor(logging.DEBUG),
                'SDK debug logging must be disabled')
        kwargs.update(proxies={'http':None,'https':None},allow_redirects=False,verify=True,timeout=(5,10))
        query=parse_qs(u.query,keep_blank_values=True)
        streaming=kwargs.get('stream',False)
        kwargs['stream']=True
        response=None;started=time.monotonic()
        try:
            response=super().send(request,**kwargs)
            require(not 300<=response.status_code<400, 'redirect refused')
            require(not response.headers.get('Content-Encoding'), 'encoded provider response refused')
            if not streaming or bool({'versioning','bucketInfo'} & set(query)) or response.status_code>=400:
                raw=bytearray()
                for chunk in response.iter_content(CHUNK):
                    raw.extend(chunk);require(len(raw)<=MAX_RESPONSE and time.monotonic()-started<=30,'provider response exceeds bound')
                response.close();raw=bytes(raw)
                if 'versioning' in query and response.status_code==200:
                    require(b'<!' not in raw, 'versioning XML declarations refused')
                    node=ET.fromstring(raw)
                    require(node.tag=='VersioningConfiguration' and not node.attrib
                            and not list(node) and not (node.text or '').strip(), 'bucket was versioned or versioning response is ambiguous')
                if response.status_code>=400:
                    # Preserve only an allowlisted service code; errors can echo secrets.
                    try:
                        code=(ET.fromstring(raw).findtext('Code') if u.hostname not in ('cdn.aliyuncs.com','sts.aliyuncs.com')
                              else json.loads(raw).get('Code'))
                    except (ValueError,ET.ParseError):code=None
                    code=code if code in ('FileAlreadyExists','NoSuchKey','AccessDenied','BucketNotEmpty','Throttling') else 'ProviderError'
                    raw=(('<Error><Code>'+code+'</Code><Message>provider operation failed</Message></Error>').encode()
                         if u.hostname not in ('cdn.aliyuncs.com','sts.aliyuncs.com')
                         else json.dumps({'Code':code,'Message':'provider operation failed','RequestId':'redacted'}).encode())
                response._content=raw;response._content_consumed=True
            return response
        except Exception:
            # Do not let either SDK log an exception containing a signed URL/token.
            if response is not None:response.close()
            raise requests.RequestException('bounded provider transport failed') from None


class Provider:
    """Explicit resource/credential binding for SDK primitives, with no credential discovery."""
    def __init__(self, *, resource, access_key_id, access_key_secret, security_token, http_adapter=None):
        fields={'account_id','principal_arn','identity_type','bucket','region','endpoint','domain','cname'}
        require(isinstance(resource,dict) and set(resource)==fields, 'exact resource binding required')
        self.resource=copy.deepcopy(resource);r=self.resource
        require(all(isinstance(v,str) and v for v in r.values()),'resource fields required')
        require(re.fullmatch(r'[a-z0-9][a-z0-9-]{1,61}[a-z0-9]',r['bucket'])
                and re.fullmatch(r'[a-z]+-[a-z]+(?:-[0-9]+)?',r['region']), 'invalid bucket/region')
        require(r['endpoint']=='https://oss-'+r['region']+'.aliyuncs.com'
                and r['domain']=='packages.githubim.com' and r['cname'].endswith('.kunlunaq.com'),
                'canonical endpoint/domain required')
        require(all(isinstance(v,str) and v for v in (access_key_id,access_key_secret,security_token)),
                'explicit temporary credentials required')
        self.origin=r['bucket']+'.oss-'+r['region']+'.aliyuncs.com'
        self.session=_Session((self.origin,'cdn.aliyuncs.com','sts.aliyuncs.com'),http_adapter)
        transport=RequestsHttpClient(session=self.session,enabled_redirect=False,connect_timeout=5,readwrite_timeout=10)
        self.oss=oss.Client(oss.Config(region=r['region'],endpoint=r['endpoint'],signature_version='v4',
            credentials_provider=oss.credentials.StaticCredentialsProvider(access_key_id,access_key_secret,security_token),
            http_client=transport,retry_max_attempts=1,enabled_redirect=False,insecure_skip_verify=False,
            connect_timeout=5,readwrite_timeout=10))
        self.rpc=AcsClient(region_id=r['region'],credential=StsTokenCredential(access_key_id,access_key_secret,security_token),
            auto_retry=False,max_retry_time=0,connect_timeout=5,timeout=10,verify=True,proxy={'http':None,'https':None})
        self.rpc.session.close();self.rpc.session=self.session

    def close(self):
        self.session.close()

    def _call(self, cls, **values):
        request=cls();request.set_protocol_type('https');request.set_method('POST')
        request.set_endpoint('sts.aliyuncs.com' if cls is GetCallerIdentityRequest else 'cdn.aliyuncs.com')
        for key,value in values.items():getattr(request,'set_'+key)(value)
        try:
            result=json.loads(self.rpc.do_action_with_exception(request))
            require(isinstance(result,dict),'provider response must be an object');return result
        except Exception:
            raise ProviderError('provider RPC failed') from None

    def observe_resource(self):
        """Re-read caller, private owner-bound bucket and explicit never-versioned XML."""
        r=self.resource
        identity=self._call(GetCallerIdentityRequest)
        require((identity.get('AccountId'),identity.get('Arn'),identity.get('IdentityType'))==
                (r['account_id'],r['principal_arn'],r['identity_type']), 'caller identity differs')
        try:
            info=self.oss.get_bucket_info(oss.GetBucketInfoRequest(bucket=r['bucket'])).bucket_info
            require(info.name==r['bucket'] and info.location=='oss-'+r['region']
                    and info.extranet_endpoint==r['endpoint'][8:] and info.owner.id==r['account_id']
                    and info.acl=='private', 'bucket identity or private ACL differs')
            value=self.oss.get_bucket_versioning(oss.GetBucketVersioningRequest(bucket=r['bucket']))
            require(value.version_status is None,'bucket was versioned')
        except Exception:
            raise ProviderError('bucket identity/versioning verification failed') from None
        return {'resource_sha256':digest(r),'versioning':'never_enabled',**FALSE_FIELDS}

    def read_object(self, key, facts):
        """Read through EOF with byte/hash/cache checks and an elapsed-time bound."""
        safe_path(key);require(key.startswith(('snapshots/','public/','generated/')),'object namespace differs')
        require(type(facts['size']) is int and 0<facts['size']<=MAX_OBJECT,'object size exceeds bound')
        body=None;start=time.monotonic();total=0;sha=hashlib.sha256()
        try:
            result=self.oss.get_object(oss.GetObjectRequest(bucket=self.resource['bucket'],key=key))
            body=result.body
            require(result.content_length==facts['size'] and result.cache_control==facts['cache_control'], 'object metadata differs')
            for chunk in body.iter_bytes(block_size=CHUNK):
                total+=len(chunk);require(total<=facts['size'] and time.monotonic()-start<=120,'object stream exceeds bound')
                sha.update(chunk)
            require(total==facts['size'] and sha.hexdigest()==facts['sha256'],'full object bytes differ')
            return dict(size=total,sha256=sha.hexdigest(),cache_control=result.cache_control,
                        status=200,request_id=(result.request_id if isinstance(result.request_id,str) and re.fullmatch(r'[A-Za-z0-9-]{1,128}',result.request_id) else None),elapsed_ms=round((time.monotonic()-start)*1000),**FALSE_FIELDS)
        except Exception:
            raise ProviderError('complete object readback failed') from None
        finally:
            if body is not None:body.close()

    def put_create_only(self, key, path, facts):
        """Upload one frozen local file once; unknown outcomes only trigger a GET."""
        safe_path(key);require(key.startswith(('snapshots/','public/','generated/')),'object namespace differs')
        require(type(facts['size']) is int and 0<facts['size']<=MAX_OBJECT,'object size exceeds bound')
        require(re.fullmatch('[0-9a-f]{64}',facts['sha256']) is not None and facts['cache_control'] in
                ('no-store','public, max-age=31536000, immutable'),'invalid object facts')
        with tempfile.TemporaryFile() as frozen:
            try:
                fd=os.open(path,os.O_RDONLY|os.O_NOFOLLOW)
                with os.fdopen(fd,'rb') as source:
                    metadata=os.fstat(source.fileno())
                    require(stat.S_ISREG(metadata.st_mode) and metadata.st_nlink==1 and metadata.st_size==facts['size'],'unsafe local object')
                    count=0;sha=hashlib.sha256()
                    for chunk in iter(lambda:source.read(CHUNK),b''):
                        count+=len(chunk);require(count<=facts['size'],'local object changed');sha.update(chunk);frozen.write(chunk)
                require(count==facts['size'] and sha.hexdigest()==facts['sha256'],'local object differs')
            except OSError:raise ProviderError('local object cannot be frozen') from None
            self.observe_resource();frozen.seek(0);outcome='created'
            try:
                self.oss.put_object(oss.PutObjectRequest(bucket=self.resource['bucket'],key=key,body=frozen,
                    content_length=facts['size'],cache_control=facts['cache_control'],forbid_overwrite=True))
            except Exception as error:
                if isinstance(error,oss.exceptions.OperationError):error=error.unwrap()
                if isinstance(error,oss.exceptions.ServiceError):
                    require(error.status_code==409 and error.code=='FileAlreadyExists','create-only upload rejected')
                    outcome='already_exists'
                elif isinstance(error,(oss.exceptions.RequestError,oss.exceptions.ResponseError)):
                    outcome='unknown_readback_verified'
                else:
                    raise ProviderError('create-only upload failed') from None
        return {'outcome':outcome,'write_attempted':True,'readback':self.read_object(key,facts),**FALSE_FIELDS}

    def read_configuration(self):
        """Observe all returned functions, IDs, arguments and order without normalizing them away."""
        domain=self.resource['domain']
        detail=self._call(DescribeCdnDomainDetailRequest,DomainName=domain).get('GetDomainDetailModel')
        configs=self._call(DescribeCdnDomainConfigsRequest,DomainName=domain).get('DomainConfigs',{}).get('DomainConfig')
        require(isinstance(detail,dict) and isinstance(configs,list),'incomplete CDN configuration')
        value={'domain':domain,'detail':detail,'configs':configs}
        return {**value,'sha256':digest(value)}

    def _configuration_ready(self, before):
        detail=before['detail'];r=self.resource
        require(detail.get('DomainName')==r['domain'] and detail.get('Cname')==r['cname']
                and detail.get('DomainStatus')=='online', 'CDN domain binding differs')
        sources=detail.get('SourceModels',{}).get('SourceModel')
        require(isinstance(sources,list) and len(sources)==1 and sources[0].get('Type')=='oss'
                and sources[0].get('Content')==self.origin and sources[0].get('Port')==443
                and sources[0].get('Enabled')=='online', 'CDN origin differs')
        rows={};ids=set()
        expected={'forward_scheme':{'enable':'on','scheme_origin':'https'},
                  'set_req_host_header':{'domain_name':self.origin},
                  'https_origin_sni':{'enabled':'on','https_origin_sni':self.origin},
                  'set_hashkey_args':{'hashkey_args':'','disable':'off','keep_oss_args':'on'}}
        for row in before['configs']:
            cid=str(row.get('ConfigId',''));name=row.get('FunctionName')
            require(cid.isdecimal() and int(cid)>0 and cid not in ids and row.get('ParentId') in ('0',0,None)
                    and row.get('Status')=='success', 'ambiguous configuration ID/group/status')
            ids.add(cid)
            require(name in (*expected,FUNCTION), 'unsupported existing CDN function')
            args=row.get('FunctionArgs',{}).get('FunctionArg',[])
            require(all(set(x)=={'ArgName','ArgValue'} for x in args) and len({x['ArgName'] for x in args})==len(args),'ambiguous function arguments')
            if name!=FUNCTION:
                require(name not in rows and {x['ArgName']:x['ArgValue'] for x in args}==expected[name],'origin/cache configuration differs')
                rows[name]=row
        require(set(rows)==set(expected),'origin/cache configuration incomplete')

    def apply_rewrites(self, routes, *, before, managed_ids):
        """Apply SDK configuration primitives only, retaining partial/unknown outcomes.

        This is not activation: query normalization, PCRE and provider length
        capabilities remain unverified. There is intentionally no delivery CLI.
        """
        functions=compile_rewrites(routes);self._configuration_ready(before)
        require(len(set(map(str,managed_ids)))==len(managed_ids),'duplicate managed IDs')
        old=[x for x in before['configs'] if x['FunctionName']==FUNCTION]
        require({str(x['ConfigId']) for x in old}==set(map(str,managed_ids)),'unmanaged rewrite or unknown managed ID')
        by_source={}
        for row in old:
            args={x['ArgName']:x['ArgValue'] for x in row['FunctionArgs']['FunctionArg']}
            require(set(args)=={'source_url','target_url','flag','sequence'} and args['flag']=='break'
                    and args['source_url'] not in by_source,'unknown/duplicate rewrite')
            by_source[args['source_url']]=row
        wanted={f['functionArgs'][0]['argValue'] for f in functions}
        require(set(by_source)<=wanted,'configuration requires unapproved deletion')
        for f in functions:
            row=by_source.get(f['functionArgs'][0]['argValue'])
            if row:f['configId']=str(row['ConfigId'])
        self.observe_resource()
        require(self.read_configuration()['sha256']==before['sha256'],'CDN configuration drifted')
        receipt={'state':'incomplete','write_attempted':False,'before_sha256':before['sha256'],
                 'after_sha256':None,'batches_attempted':0,**FALSE_FIELDS}
        returned_ids=[]
        try:
            for start in range(0,len(functions),20):
                batch=functions[start:start+20];receipt['write_attempted']=True;receipt['batches_attempted']+=1
                response=self._call(BatchSetCdnDomainConfigRequest,DomainNames=self.resource['domain'],Functions=json.dumps(batch,separators=(',',':')))
                rows=response.get('DomainConfigList',{}).get('DomainConfigModel',[])
                require(len(rows)==len(batch) and len({str(x.get('ConfigId')) for x in rows})==len(batch)
                        and all(str(x.get('ConfigId','')).isdecimal() and int(x['ConfigId'])>0
                                and x.get('DomainName')==self.resource['domain'] and x.get('FunctionName')==FUNCTION for x in rows),
                        'CDN returned missing/duplicate/failed configuration IDs')
                returned_ids.extend(str(x['ConfigId']) for x in rows)
            after=self.read_configuration();receipt['after_sha256']=after['sha256']
            actual=[x for x in after['configs'] if x['FunctionName']==FUNCTION]
            require(len(set(returned_ids))==len(functions) and set(returned_ids)=={str(x['ConfigId']) for x in actual},
                    'returned configuration IDs differ from readback')
            expected=[{x['argName']:x['argValue'] for x in f['functionArgs']} for f in functions]
            got=[{a['ArgName']:a['ArgValue'] for a in x['FunctionArgs']['FunctionArg']} for x in actual]
            require(sorted(got,key=lambda x:int(x['sequence']))==expected,'CDN configuration readback differs')
            require([x for x in after['configs'] if x['FunctionName']!=FUNCTION]==[x for x in before['configs'] if x['FunctionName']!=FUNCTION]
                    and after['detail']==before['detail'],'unrelated CDN configuration changed')
            self._configuration_ready(after);receipt['state']='readback_verified'
        except Exception:
            receipt['error']='configuration operation incomplete'
            try:receipt['after_sha256']=self.read_configuration()['sha256']
            except Exception:pass
        return receipt

    def refresh(self, urls, *, max_polls=3, poll_interval=1):
        """Refresh exact file URLs and accept only this request's exact complete tasks."""
        require(isinstance(urls,list) and 0<len(urls)<=50 and len(set(urls))==len(urls),'bounded unique URLs required')
        require(type(max_polls) is int and 1<=max_polls<=10 and 0<=poll_interval<=5,'bounded polling required')
        for url in urls:
            parsed=urlsplit(url)
            require(url.startswith('https://'+self.resource['domain']+'/') and parsed.netloc==self.resource['domain']
                    and not parsed.query and not parsed.fragment and parsed.path!='/' and not parsed.path.endswith('/'),'exact canonical file URL required')
            safe_path(parsed.path,True)
        self.observe_resource()
        raw=self._call(RefreshObjectCachesRequest,ObjectPath='\n'.join(urls),ObjectType='File').get('RefreshTaskId','')
        ids=raw.split(',');require(len(ids)==len(urls) and len(set(ids))==len(ids) and all(x.isdecimal() and int(x)>0 for x in ids),'refresh task IDs incomplete')
        bindings=None
        for attempt in range(max_polls):
            rows=[]
            for start in range(0,len(ids),10):
                group=ids[start:start+10]
                response=self._call(DescribeRefreshTaskByIdRequest,TaskId=','.join(group));batch=response.get('Tasks',[])
                require(len(batch)==len(group) and {str(x.get('TaskId')) for x in batch}==set(group),'foreign/missing refresh task')
                rows.extend(batch)
            mapping={str(x['TaskId']):x.get('ObjectPath') for x in rows}
            require(len(rows)==len(ids) and set(mapping.values())==set(urls) and (bindings is None or mapping==bindings),'refresh URL identity differs')
            bindings=mapping
            require(all(x.get('ObjectType')=='file' and x.get('Status') in ('Complete','Refreshing') for x in rows),'refresh failed or ambiguous')
            if all(x['Status']=='Complete' for x in rows):return {'state':'tasks_complete','task_ids':ids,**FALSE_FIELDS}
            if attempt+1<max_polls:time.sleep(poll_interval)
        raise ProviderError('refresh deadline exhausted')

    def activate(self, *args, **kwargs):
        """Refuse publication until independently reviewed authority and live wire proof exist."""
        raise ProviderError('production authority and live routing equivalence are not implemented')
