#!/usr/bin/env python3
"""TEST ONLY native materialization and official SDK HTTP transport fixture.

No production authentication, public client, live CDN or activation claim.
The prepare stage consumes public M1 exports; it never generates signing keys.
"""
from __future__ import annotations
import argparse
import copy
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import shutil
import socket
import sys
import tempfile
import time
from unittest import mock

ROOT=Path(__file__).resolve().parents[2]
sys.path.insert(0,str(ROOT));sys.path.insert(0,str(ROOT/'tests'))
CASES=(
 'native-public-binding','native-composite-materialization','sdk-all-objects-A','sdk-all-objects-B',
 'sdk-object-resource-fence','sdk-local-object-drift','sdk-unknown-put-no-retry','sdk-full-get-failures',
 'sdk-initial-input-current-fence','sdk-mid-object-drift','sdk-generation-source-binding',
 'sdk-full-config-A','sdk-config-A-B-reuse','sdk-config-fresh-fence','sdk-config-removal-unsupported',
 'sdk-config-incomplete-sideeffects','sdk-full-refresh-set','sdk-aggregate-task-coverage',
 'sdk-production-boundaries-refuse','driver-ci-receipt-cleanup')
FALSE_FIELDS=('production_enabled','publication_verified','cloud_verified','wire_equivalence_verified',
              'github_provenance_verified','fresh_production_auth_verified','public_clients_verified','activation_authorized')


def native_cases(args,report):
    """Exercise public export identity and unchanged native materialization seams."""
    exports=read_public_exports(args.public_dir,args.expected_export_source)
    for source in ('0'*40,'bad'):
        refused(lambda:read_public_exports(args.public_dir,source))
    with tempfile.TemporaryDirectory(prefix='wk-sdk-bad-export-') as temp:
        target=Path(temp);shutil.copyfile(args.public_dir/'result.json',target/'result.json')
        refused(lambda:read_public_exports(target,args.expected_export_source))
        for key,value in (('secret_tree_removed',False),('test_only',False),('data_only',False),('source_provenance_verified',True)):
            changed=copy.deepcopy(exports);changed[key]=value
            (target/'fixture-export.json').write_bytes(canonical(changed))
            refused(lambda:read_public_exports(target,args.expected_export_source))
    report['cases'].append({'case':CASES[0],'passed':True})
    bundle=prepare_bundle(args,exports,report)
    assert set(bundle['generations'])=={'A','B'}
    for label in ('A','B'):
        data=bundle['generations'][label];delivery=read_json(Path(data['delivery']))
        plan=read_json(Path(data['plan']))
        assert len(delivery['objects'])==153 and len(delivery['routes'])==178
        assert delivery['planner_verification']==plan['verification']
        expected={x['object_key']:x for name in ('snapshot_objects','immutable_aliases','generated_objects') for x in plan[name]}
        assert set(expected)=={x['object_key'] for x in delivery['objects']}
        for item in delivery['objects']:
            assert all(item[key]==value for key,value in expected[item['object_key']].items())
            assert facts(Path(data['root'])/item['materialized_file'])=={key:item[key] for key in ('sha256','size')}
        assert [x['audit_release_id'] for x in delivery['client_requests']]==[10,4242 if label=='A' else 4243]
        assert all(delivery[key] is False for key in ('production_enabled','publication_verified','cloud_verified'))
    assert bundle['resource_intent_sha256']!=bundle['sdk_resource_sha256']
    report['cases'].append({'case':CASES[1],'passed':True})
    return bundle


class ObservedRead:
    """Observe only consumed bytes; close and read(0) never establish EOF."""
    def __init__(self,raw,record,on_eof=None):
        self.raw=raw;self.record=record;self.sha=hashlib.sha256();self.on_eof=on_eof
        self.buffer=bytearray() if on_eof else None
        record.update(bytes_read=0,sha256=self.sha.hexdigest(),eof=False)

    def __getattr__(self,name):return getattr(self.raw,name)

    def read(self,size=-1,*args,**kwargs):
        try:chunk=self.raw.read(size,*args,**kwargs)
        except Exception as error:
            self.record['error_type']=type(error).__name__;raise
        if chunk:
            self.record['bytes_read']+=len(chunk);self.sha.update(chunk);self.record['sha256']=self.sha.hexdigest()
            if self.buffer is not None:
                require(len(self.buffer)+len(chunk)<=8*1024*1024,'response observation exceeds bound');self.buffer.extend(chunk)
        elif size!=0:
            self.record['eof']=True
            if self.on_eof is not None:self.on_eof(bytes(self.buffer))
        return chunk


class ObservedAdapter:
    """Observe the fixture's actual HTTP responses without draining or replaying."""
    ACTIONS=('DescribeCdnDomainDetail','DescribeCdnDomainConfigs','BatchSetCdnDomainConfig','RefreshObjectCaches','DescribeRefreshTaskById')
    def __init__(self,wire):self.wire=wire;self.observations=[];self.operations=[]
    def __getattr__(self,name):return getattr(self.wire,name)
    def close(self):return self.wire.close()

    def send(self,request,**kwargs):
        from urllib.parse import parse_qs,urlsplit
        parsed=urlsplit(request.url);query=parse_qs(parsed.query,keep_blank_values=True)
        if request.body and parsed.hostname in ('cdn.aliyuncs.com','sts.aliyuncs.com'):
            query.update(parse_qs(request.body.decode() if isinstance(request.body,bytes) else request.body))
        action=query.get('Action',[''])[0]
        is_object=not action and not parsed.query and request.method in ('GET','PUT')
        if action not in self.ACTIONS and not is_object:return self.wire.send(request,**kwargs)
        row={'method':request.method,'action':action or 'object','object_key':parsed.path[1:] if is_object else None,'request':{}}
        require(len(self.observations)<2000,'exchange observation count exceeded');self.observations.append(row)
        for name in ('DomainName','DomainNames','ObjectType','TaskId'):
            if name in query:row['request'][name]=query[name][0]
        if 'ObjectPath' in query:row['request']['ObjectPath']=query['ObjectPath'][0].split('\n')
        if 'Functions' in query:row['request']['Functions']=json.loads(query['Functions'][0])
        if is_object and request.method=='PUT':
            row['request'].update(cache_control=request.headers.get('Cache-Control'),content_length=request.headers.get('Content-Length'),forbid_overwrite=request.headers.get('x-oss-forbid-overwrite'))
            row['request_body']={}
            if hasattr(request.body,'read'):request.body=ObservedRead(request.body,row['request_body'])
            elif isinstance(request.body,bytes):
                raw=request.body;row['request_body'].update(bytes_read=len(raw),sha256=hashlib.sha256(raw).hexdigest(),eof=False)
            else:
                body=request.body;record=row['request_body'];sha=hashlib.sha256()
                record.update(bytes_read=0,sha256=sha.hexdigest(),eof=False)
                def chunks():
                    try:
                        for chunk in body:
                            record['bytes_read']+=len(chunk);sha.update(chunk);record['sha256']=sha.hexdigest();yield chunk
                        record['eof']=True
                    except Exception as error:
                        record['error_type']=type(error).__name__;raise
                request.body=chunks()
        try:response=self.wire.send(request,**kwargs)
        except Exception as error:
            row['error_type']=type(error).__name__;raise
        row['status']=response.status_code
        if is_object:row['response']={'cache_control':response.headers.get('Cache-Control'),'content_length':response.headers.get('Content-Length')}
        row['stream']={}
        def complete(raw):
            try:row['response']=public_rpc_response(action,json.loads(raw))
            except (ValueError,TypeError,KeyError):row['response_decode_error']=True
        response.raw=ObservedRead(response.raw,row['stream'],complete if action else None)
        return response


def public_rpc_response(action,value):
    """Keep explicit public protocol fields; never exception messages or credentials."""
    def selected(row,keys):
        return {key:copy.deepcopy(row[key]) for key in keys if key in row} if isinstance(row,dict) else {'invalid_row_type':type(row).__name__}
    if action=='DescribeRefreshTaskById':
        result=selected(value,('TotalCount',))
        if 'Tasks' in value:
            result['Tasks']=[selected(row,('TaskId','ObjectPath','ObjectType','Status','Process','CreationTime')) for row in value['Tasks']] if isinstance(value['Tasks'],list) else {'invalid_tasks_type':type(value['Tasks']).__name__}
        return result
    if action=='RefreshObjectCaches':return selected(value,('RefreshTaskId',))
    if action=='BatchSetCdnDomainConfig':
        rows=value.get('DomainConfigList',{}).get('DomainConfigModel',[])
        return {'DomainConfigList':{'DomainConfigModel':[selected(row,('ConfigId','DomainName','FunctionName')) for row in rows]}}
    if action=='DescribeCdnDomainConfigs':
        rows=[]
        for row in value.get('DomainConfigs',{}).get('DomainConfig',[]):
            entry=selected(row,('ConfigId','FunctionName','Status','ParentId'))
            entry['FunctionArgs']={'FunctionArg':[selected(arg,('ArgName','ArgValue')) for arg in row.get('FunctionArgs',{}).get('FunctionArg',[])]};rows.append(entry)
        return {'DomainConfigs':{'DomainConfig':rows}}
    detail=value.get('GetDomainDetailModel',{});result=selected(detail,('DomainName','Cname','DomainStatus'))
    result['SourceModels']={'SourceModel':[selected(row,('Type','Content','Port','Priority','Weight','Enabled')) for row in detail.get('SourceModels',{}).get('SourceModel',[])]}
    return {'GetDomainDetailModel':result}


def observe_operation(adapter,details,action):
    """Retain expected bindings and partial actual outcomes before scratch removal."""
    row={**copy.deepcopy(details),'state':'incomplete','first_exchange':len(adapter.observations)}
    require(len(adapter.operations)<1000,'operation observation count exceeded');adapter.operations.append(row)
    try:
        result=action();row['result']=copy.deepcopy(result)
        if not isinstance(result,dict) or result.get('state')!='incomplete':row['state']='complete'
        return result
    except Exception as error:
        row['error_type']=type(error).__name__;raise
    finally:
        row.update(copy.deepcopy(details));row['end_exchange']=len(adapter.observations)


def sdk_cases(args,report):
    """All assertions are against actual SDK requests, bytes and public receipts."""
    contracts=module(ROOT/'tests/provider_sdk/test_aliyun_object_storage_provider.py')
    provider=module(ROOT/'scripts/aliyun-object-storage-provider.py')
    bundle=load_bundle(args.bundle,args.bundle_sha256,args.source_sha)
    report['cases']=copy.deepcopy(bundle['native_cases'])
    a=read_json(Path(bundle['generations']['A']['delivery']));b=read_json(Path(bundle['generations']['B']['delivery']))
    report['resource_intent_sha256']=bundle['resource_intent_sha256'];report['sdk_resource_sha256']=bundle['sdk_resource_sha256']
    report['native_bindings']={'export_source_sha':bundle['export_source_sha'],'current_control':read_json(Path(bundle['current_control'])),
        'resource_intent_sha256':bundle['resource_intent_sha256'],'sdk_resource_sha256':bundle['sdk_resource_sha256'],
        'generations':{label:{'inputs':read_json(Path(data['inputs'])),'inputs_sha256':bundle['files'][data['inputs']]['sha256'],
            'plan_sha256':data['plan_sha256'],'client_requests':generation(bundle,label)['client_requests'],
            'planner_verification':generation(bundle,label)['planner_verification']} for label,data in bundle['generations'].items()}}
    owned=[];observed=[]
    def client(wire=None):
        wire=wire or contracts.Wire();adapter=ObservedAdapter(wire);wire.evidence=adapter
        value=provider.Provider(resource=contracts.RESOURCE,access_key_id='FIXTURE-AK',access_key_secret=contracts.SECRET,security_token=contracts.TOKEN,http_adapter=adapter)
        owned.append(value);observed.append(adapter);return value,wire
    def case(name,action):
        report['stage']=name;action();report['cases'].append({'case':name,'passed':True})
    def writes(wire):return [row for row in wire.requests if row[0].method=='PUT' or row[2] in ('BatchSetCdnDomainConfig','RefreshObjectCaches')]
    def put_rows(wire):return [row for row in wire.requests if row[0].method=='PUT']
    def checked_object(label,item,c,w):
        return send_object(bundle,args,label,item,c,w)
    shared,wire=client()
    outcomes={}
    try:
        def all_objects(label,delivery):
            start=len(put_rows(wire));results=[]
            for item in delivery['objects']:results.append(checked_object(label,item,shared,wire))
            assert len(put_rows(wire))-start==153
            assert [url_path(row[0].url) for row in put_rows(wire)[start:]]==[item['object_key'] for item in delivery['objects']]
            for item in delivery['objects']:
                data,cache=wire.objects[item['object_key']]
                assert len(data)==item['size'] and hashlib.sha256(data).hexdigest()==item['sha256'] and cache==item['cache_control']
            assert all(row['readback']['size']==item['size'] for row,item in zip(results,delivery['objects']))
            assert all(all(row[k] is False for k in provider.FALSE_FIELDS) for row in results)
            outcomes[label]=[row['outcome'] for row in results]
            if label=='A':assert len(wire.objects)==153
            else:assert len(wire.objects)==154 and outcomes[label].count('already_exists')==152
        case(CASES[2],lambda:all_objects('A',a));case(CASES[3],lambda:all_objects('B',b))
        item=a['objects'][0]
        def resource_fence():
            for field,value in (('owner','000'),('principal','wrong'),('versioning',b'<VersioningConfiguration><Status>Suspended</Status></VersioningConfiguration>')):
                c,w=client();setattr(w,field,value);refused(lambda:checked_object('A',item,c,w));assert not writes(w)
        case(CASES[4],resource_fence)
        def local_drift():
            c,w=client();path=Path(bundle['generations']['A']['root'])/item['materialized_file']
            original=path.read_bytes()
            try:
                path.write_bytes(original+b'drift');refused(lambda:checked_object('A',item,c,w));assert not writes(w)
            finally:path.write_bytes(original)
            link=path.with_name(path.name+'.link');os.link(path,link)
            try:refused(lambda:checked_object('A',item,c,w));assert not writes(w)
            finally:link.unlink()
        case(CASES[5],local_drift)
        def unknown_put():
            for fault in ('timeout_before','timeout_after'):
                c,w=client();w.put_fault=fault
                if fault=='timeout_before':refused(lambda:checked_object('A',item,c,w));assert not w.objects
                else:assert checked_object('A',item,c,w)['outcome']=='unknown_readback_verified'
                assert len(put_rows(w))==1
        case(CASES[6],unknown_put)
        def bad_get():
            for fault in ('truncated','overlong','digest','cache','encoding','interrupted'):
                c,w=client();w.get_fault=fault;refused(lambda:checked_object('A',item,c,w))
                assert len(put_rows(w))==1 and len(w.objects)==1
        case(CASES[7],bad_get)
        def initial_fences():
            c,w=client();refused(lambda:load_bundle(args.bundle,'0'*64,args.source_sha))
            for path in (Path(bundle['current_control']),Path(bundle['generations']['A']['inputs']),Path(bundle['generations']['A']['plan'])):
                original=path.read_bytes()
                try:path.write_bytes(original+b' ');refused(lambda:checked_object('A',item,c,w));assert not writes(w)
                finally:path.write_bytes(original)
        case(CASES[8],initial_fences)
        def mid_drift():
            c,w=client();checked_object('A',item,c,w);path=Path(bundle['current_control']);original=path.read_bytes()
            try:
                path.write_bytes(original+b' ');refused(lambda:checked_object('A',a['objects'][1],c,w));assert len(put_rows(w))==1 and len(w.objects)==1
            finally:path.write_bytes(original)
        case(CASES[9],mid_drift)
        def source_binding():
            c,w=client();changed=copy.deepcopy(next(row for row in a['objects'] if 'source' in row));changed['source']['audit_release_id']=999
            refused(lambda:checked_object('A',changed,c,w));assert not writes(w)
            mirror=next(row for row in b['objects'] if row['object_key'] not in {x['object_key'] for x in a['objects']})
            refused(lambda:checked_object('A',mirror,c,w));assert not writes(w)
        case(CASES[10],source_binding)
        config_results={}
        def config_a():
            before=shared.read_configuration();result=apply_configuration(bundle,args,'A',shared,wire,before,[])
            assert result['state']=='readback_verified';config_results['A']=result
            functions=provider.compile_rewrites(a['routes']);assert len(functions)==17
            assert contracts.inverse_finite_rules(functions)=={x['public_uri']:'/'+x['object_key'] for x in a['routes']}
            assert len([r for r in wire.requests if r[2]=='BatchSetCdnDomainConfig'])==1
        case(CASES[11],config_a)
        def config_b():
            result=apply_configuration(bundle,args,'B',shared,wire,shared.read_configuration(),config_results['A']['config_ids'])
            assert result['state']=='readback_verified' and result['config_ids']==config_results['A']['config_ids'];config_results['B']=result
            left=provider.compile_rewrites(a['routes']);right=provider.compile_rewrites(b['routes'])
            assert [x['functionArgs'][0] for x in left]==[x['functionArgs'][0] for x in right]
            assert sum(x!=y for x,y in zip(left,right))==2
        case(CASES[12],config_b)
        def config_fence():
            c,w=client();before=c.read_configuration();before['configs']=[]
            refused(lambda:apply_configuration(bundle,args,'A',c,w,before,[]));assert not writes(w)
            before=c.read_configuration();w.configs.append(contracts.config('unmanaged',{'x':'y'},99))
            refused(lambda:apply_configuration(bundle,args,'A',c,w,before,[]));assert not writes(w)
        case(CASES[13],config_fence)
        def deletion():
            c,w=client();result=c.apply_rewrites([contracts.route()],before=c.read_configuration(),managed_ids=[]);ids=[str(row['ConfigId']) for row in c.read_configuration()['configs'] if row['FunctionName']==provider.FUNCTION];before=len(writes(w))
            refused(lambda:apply_configuration(bundle,args,'A',c,w,c.read_configuration(),ids));assert len(writes(w))==before
        case(CASES[14],deletion)
        def partial_config():
            for fault in ('zero','missing','duplicate','wrongid','partial','timeout'):
                c,w=client();w.batch_fault=fault;result=apply_configuration(bundle,args,'A',c,w,c.read_configuration(),[])
                assert result['state']=='incomplete' and result['batches_attempted']==1
                assert len(writes(w))==1 and not any(r[2]=='RefreshObjectCaches' for r in w.requests)
        case(CASES[15],partial_config)
        def full_refresh():
            for label,delivery in (('A',a),('B',b)):
                c,w=client(contracts.RefreshWire());result=refresh_generation(bundle,args,label,c,w)
                assert result['state']=='tasks_complete' and result['task_ids']==['33643947491']
                responses=[row['response'] for row in w.evidence.observations if row['action']=='DescribeRefreshTaskById']
                assert [row['ObjectPath'] for response in responses for row in response['Tasks']]==refresh_scope(delivery['routes'])['urls']
                assert sum(len(response['Tasks']) for response in responses)==178 and w.submissions==4 and w.polls==1
        case(CASES[16],full_refresh)
        def aggregate_faults():
            for fault in ('missing','duplicate','total','foreign_url','submit_timeout','query_timeout'):
                c,w=client(contracts.RefreshWire(fault=fault));refused(lambda:refresh_generation(bundle,args,'A',c,w))
                assert w.submissions==(2 if fault=='submit_timeout' else 4)
                assert 0<len(writes(w))<=4
                report.setdefault('incomplete_observations',[]).append({'fault':fault,'state':'incomplete','submissions_attempted':w.submissions,'task_rows_observed':sum(len(row.get('response',{}).get('Tasks',[])) for row in w.evidence.observations if row['action']=='DescribeRefreshTaskById'),'repeated_writes':False})
        case(CASES[17],aggregate_faults)
        def production_refused():
            fresh=module(ROOT/'scripts/verify-object-storage-delivery-inputs.py')
            refused(lambda:fresh.verify_delivery_inputs(inputs=Path(bundle['generations']['A']['inputs']),expected_plan_sha256=bundle['generations']['A']['plan_sha256'],executor_sha=args.source_sha))
            refused(lambda:shared.activate());assert all(v is False for v in report['verification'].values())
        case(CASES[18],production_refused)
        report['object_operations']={'A':153,'B':153,'shared_exact_conflicts':152,'unique_objects':len(wire.objects)}
        report['route_counts']={'A':178,'B':178};report['config_rule_counts']={'A':17,'B':17}
        report['sdk_transport_observations']=sanitized(wire)
    finally:
        pending=sys.exc_info()[0] is not None;errors=[]
        try:record_attempts(report,observed)
        except Exception as error:errors.append({'operation':'record_attempts','error_type':type(error).__name__})
        for value in owned:
            try:value.close()
            except Exception as error:errors.append({'operation':'client_close','error_type':type(error).__name__})
        if errors:
            report['sdk_cleanup_errors']=errors
            if not pending:raise RuntimeError('SDK cleanup incomplete')


def cleanup_case(args,report):
    """Cleanup/receipt is a real final assertion, not an expected skipped case."""
    assert not args.bundle.parent.exists()
    assert len(report['cases'])==19 and len({row['case'] for row in report['cases']})==19
    report['cases'].append({'case':CASES[19],'passed':True})
    report['scratch_removed']=True
    validate_report(report)
    for change in ({'cases':[]},{'skips':1},{'test_only':False},{'scratch_removed':False}):
        refused(lambda:validate_report({**report,**change}))


def require(condition,message):
    if not condition:raise ValueError(message)


def refused(action):
    try:action()
    except (ValueError,RuntimeError,OSError):return
    raise AssertionError('expected refusal did not happen')


def canonical(value):return json.dumps(value,sort_keys=True,separators=(',',':')).encode()+b'\n'
def digest(value):return hashlib.sha256(canonical(value)).hexdigest()


def facts(path):
    import stat
    metadata=path.lstat()
    require(stat.S_ISREG(metadata.st_mode) and metadata.st_nlink==1 and metadata.st_size<=64*1024*1024,'bounded regular file required')
    raw=path.read_bytes();return {'sha256':hashlib.sha256(raw).hexdigest(),'size':len(raw)}


def read_json(path):
    require(facts(path)['size']<=8*1024*1024,'JSON exceeds limit')
    return json.loads(path.read_bytes())


def module(path):
    spec=importlib.util.spec_from_file_location(path.stem.replace('-','_'),path)
    value=importlib.util.module_from_spec(spec);spec.loader.exec_module(value);return value


def read_public_exports(public,source):
    report=read_json(public/'result.json');exports=read_json(public/'fixture-export.json')
    require(report['source_sha']==source and len(source)==40 and set(source)<=set('0123456789abcdef')
            and report['passed'] is True and report['scratch_removed'] is True
            and len(report['cases'])==29 and all(row['passed'] is True for row in report['cases']), 'public native source/receipt differs')
    require(all(exports[key] is True for key in ('test_only','data_only','secret_tree_removed'))
            and exports['source_provenance_verified'] is False,'public export claims differ')
    require(report['verification']['native_preview_verified'] is True
            and report['verification']['native_stable_verified'] is True
            and report['verification']['fresh_production_auth_verified'] is False,'native verification receipt differs')
    return exports


def source_files():
    return sorted([*ROOT.glob('scripts/*.py'),ROOT/'tests/native_stable_clients_drill.py',
        ROOT/'tests/native_stable_candidate_drill.py',ROOT/'tests/native_object_delivery_fixture_drill.py',
        ROOT/'tests/stable_audit_fixture.py',ROOT/'tests/test_plan_object_storage_publication.py',
        ROOT/'tests/test_stable_offline_candidate.py',Path(__file__),
        ROOT/'tests/provider_sdk/test_aliyun_object_storage_provider.py',ROOT/'requirements-object-storage-provider.lock'])


def prepare_bundle(args,exports,report):
    import native_object_delivery_fixture_drill as fixture
    import native_stable_clients_drill as clients
    require(sys.platform=='linux' and os.geteuid()!=0,'non-root isolated native Linux required')
    work=args.work_dir;require(work.name=='native-sdk-work' and work.is_dir() and not list(work.iterdir()),'empty owned native-sdk-work required')
    scratch=work/'prepared';scratch.mkdir();public=args.public_dir/'fixtures'
    preview=fixture.Preview(public,exports['preview']);descriptor=preview.descriptor();control=Path(descriptor['control_dir'])
    archive=clients.module('scripts/archive-package-snapshot.py');extracted=scratch/'preview-extracted'
    archive.extract_snapshot(archive_path=preview.archive,output_dir=extracted,max_members=archive.DEFAULT_MAX_MEMBERS,max_total_size=1024*1024*1024)
    command=[sys.executable,'-B',str(ROOT/'scripts/verify-production-package-site.py'),'--site-root',str(extracted/'site'),
        '--snapshot',str(extracted/'audit/snapshot.json'),'--channels',str(control/'manifests/channels.json'),
        '--signing',str(control/'manifests/preview-signing.json'),'--apt-public-cert',str(control/'keys/apt-preview.asc'),
        '--rpm-public-cert',str(control/'keys/rpm-preview.asc')]
    result=clients.run_command(report,'native-preview',command,timeout=180);require(result.returncode==0,'native preview failed')
    descriptors,plans,inputs,used=clients.seal_generations(exports,public,scratch,report,preview=preview)
    require(used is preview,'real native preview required')
    resource=work/'resource-intent.json';resource.write_bytes(canonical(fixture.RESOURCE))
    current=work/'current-control.json';current.write_bytes(canonical({'source_sha':args.source_sha,'test_only':True,'production_authority':False}))
    sdk_resource=dict(account_id='1234567890123456',principal_arn='acs:ram::1234567890123456:role/fixture',identity_type='AssumedRoleUser',
        bucket='fixture-package-bucket',region='cn-hangzhou',endpoint='https://oss-cn-hangzhou.aliyuncs.com',domain='packages.githubim.com',cname='packages.githubim.com.w.kunlunaq.com')
    bundle={'schema':'wukongim.native_sdk_fixture/v1','test_only':True,'source_sha':args.source_sha,
        'export_source_sha':args.expected_export_source,'current_control':str(current),'resource_intent_sha256':digest(fixture.RESOURCE),
        'sdk_resource_sha256':digest(sdk_resource),'generations':{},'native_cases':[]}
    for label in ('A','B'):
        target=work/label;plan_sha=hashlib.sha256(canonical(plans[label])).hexdigest()
        command=[sys.executable,'-B',str(ROOT/'scripts/prepare-object-storage-delivery.py'),'--inputs',str(inputs[label]),
            '--expected-plan-sha256',plan_sha,'--output-dir',str(target),'--resource-intent',str(resource),'--max-routes','10000','--composite','--test-only']
        result=clients.run_command(report,'materialize-'+label,command,timeout=180);require(result.returncode==0,'native materialization failed')
        bundle['generations'][label]={'root':str(target),'delivery':str(target/'delivery.json'),'plan':str(target/'transport-plan.json'),
            'plan_sha256':plan_sha,'inputs':str(inputs[label])}
    # Bind public inputs and prepared bytes separately from synthetic SDK resource identity.
    paths=sorted({p for parent in (args.public_dir,work) for p in parent.rglob('*') if p.is_file()})
    require(len(paths)<=2000,'fixture file count exceeds bound')
    bundle['files']={str(path):facts(path) for path in paths}
    bundle['source_files']={str(path.relative_to(ROOT)):facts(path) for path in source_files()}
    require(sum(value['size'] for value in bundle['files'].values())<=64*1024*1024,'fixture bytes exceed bound')
    return bundle


def load_bundle(path,expected,source):
    require(facts(path)['sha256']==expected,'bundle digest differs')
    value=read_json(path)
    require(value['schema']=='wukongim.native_sdk_fixture/v1' and value['test_only'] is True and value['source_sha']==source,'bundle identity differs')
    require(value['native_cases']==[{'case':name,'passed':True} for name in CASES[:2]],'native cases missing')
    require(0<len(value['files'])<=2000,'fixture file count differs')
    for name,wanted in value['files'].items():require(facts(Path(name))==wanted,'bound fixture bytes differ')
    require(set(value['source_files'])=={str(path.relative_to(ROOT)) for path in source_files()},'executor source set differs')
    for name,wanted in value['source_files'].items():require(facts(ROOT/name)==wanted,'executor source bytes differ')
    return value


def fixture_fence(bundle,args,client):
    require(load_bundle(args.bundle,args.bundle_sha256,args.source_sha)==bundle,'input/current-control changed')
    require(digest(client.resource)==bundle['sdk_resource_sha256'],'SDK resource binding differs')


def generation(bundle,label):return read_json(Path(bundle['generations'][label]['delivery']))


def send_object(bundle,args,label,item,client,wire):
    details={'kind':'object','generation':label,'object':{key:copy.deepcopy(value) for key,value in item.items() if key not in ('materialized_file','content_utf8')},
             'inputs_sha256':bundle['files'][bundle['generations'][label]['inputs']]['sha256'],'plan_sha256':bundle['generations'][label]['plan_sha256']}
    def action():
        fixture_fence(bundle,args,client)
        require(item in generation(bundle,label)['objects'],'object generation/source binding differs')
        return client.put_create_only(item['object_key'],Path(bundle['generations'][label]['root'])/item['materialized_file'],
                                      {key:item[key] for key in ('sha256','size','cache_control')})
    return observe_operation(wire.evidence,details,action)


def apply_configuration(bundle,args,label,client,wire,before,managed):
    details={'kind':'config','generation':label,'before':copy.deepcopy(before),'managed_ids':list(managed)}
    def action():
        try:
            fixture_fence(bundle,args,client)
            result=client.apply_rewrites(generation(bundle,label)['routes'],before=before,managed_ids=managed)
            if result['state']=='readback_verified':
                result['config_ids']=[str(row['ConfigId']) for row in client.read_configuration()['configs'] if row['FunctionName']=='back_to_origin_url_rewrite']
            return result
        finally:
            try:details['after']=client.read_configuration()
            except Exception as error:details['after_error_type']=type(error).__name__
    return observe_operation(wire.evidence,details,action)


def refresh_scope(routes):
    urls=['https://packages.githubim.com'+row['public_uri'] for row in routes]
    require(0<len(urls)<=1000 and len(set(urls))==len(urls),'exact unique refresh routes required')
    return {'urls':urls,'object_type':'File','coverage_verified':False}


def refresh_generation(bundle,args,label,client,wire):
    details={'kind':'refresh','generation':label,'urls':refresh_scope(generation(bundle,label)['routes'])['urls'],'object_type':'File'}
    def action():
        fixture_fence(bundle,args,client)
        return client.refresh(details['urls'],max_polls=2,poll_interval=0)
    return observe_operation(wire.evidence,details,action)


def url_path(url):
    from urllib.parse import urlsplit
    return urlsplit(url).path[1:]


def sanitized(wire):
    # Never serialize signed URLs, headers, credential-bearing requests or exception text.
    return [{'method':row[0].method,'action':row[2] or 'object','object_key':url_path(row[0].url) if not row[2] else None}
            for row in wire.requests if row[0].method=='PUT' or row[2] in ('BatchSetCdnDomainConfig','RefreshObjectCaches')]


def record_attempts(report,wires):
    """Preserve all observed writes even when an assertion interrupts a stage."""
    rows=[row for wire in wires for row in sanitized(wire)]
    require(len(rows)<=2000,'attempt observation bound exceeded')
    report['sdk_transport_observations']=rows
    report['sdk_exchange_observations']=[{'wire':index,**copy.deepcopy(row)} for index,wire in enumerate(wires) if isinstance(wire,ObservedAdapter) for row in wire.observations]
    report['operation_receipts']=[{'wire':index,**copy.deepcopy(row)} for index,wire in enumerate(wires) if isinstance(wire,ObservedAdapter) for row in wire.operations]
    report['object_put_attempts']=sum(row['method']=='PUT' for row in rows)
    report['refresh_submit_attempts']=sum(row['action']=='RefreshObjectCaches' for row in rows)


def validate_report(report):
    require(report['test_only'] is True and report['passed'] is True and report['skips']==0
            and report['state']=='fixture_complete' and report['stage']=='sdk_fixture_complete'
            and report['scratch_removed'] is True,'fixture receipt incomplete')
    require(report['cases']==[{'case':name,'passed':True} for name in CASES],'fixture cases incomplete')
    require(set(report['verification'])==set(FALSE_FIELDS) and all(value is False for value in report['verification'].values()),'production claims forbidden')


def stage_failure(report,error):
    """Keep the first stage error; cleanup has a separate receipt."""
    import traceback
    frames=traceback.extract_tb(error.__traceback__);frame=frames[-1] if frames else None
    if 'error_type' not in report:
        report['error_type']=type(error).__name__
        if frame:report['failure_location']={'file':Path(frame.filename).name,'line':frame.lineno,'function':frame.name}
        report['error']='TEST ONLY fixture operation failed; stage identifies boundary'
    report['passed']=False;report['state']='incomplete'


def guarded_sdk(args,report):
    """Count forbidden attempts independently of expected-refusal classification."""
    counts={'self_check_attempts':0,'unexpected_attempts':0,'passed':False};checking=True
    def blocked(*unused,**kwargs):
        counts['self_check_attempts' if checking else 'unexpected_attempts']+=1
        raise RuntimeError('provider network forbidden')
    try:
        with mock.patch.object(socket.socket,'connect',side_effect=blocked):
            with socket.socket() as probe:
                probe.settimeout(1)
                refused(lambda:probe.connect(('127.0.0.1',9)))
            require(counts['self_check_attempts']==1,'network guard self-check failed');checking=False
            sdk_cases(args,report)
        require(counts['unexpected_attempts']==0,'unexpected provider network attempt')
    finally:
        counts['passed']=counts['self_check_attempts']==1 and counts['unexpected_attempts']==0
        report['network_guard']=counts


def write_report(path,report):
    """Preserve a finite incomplete receipt even if detailed evidence overflows."""
    raw=canonical(report)
    if len(raw)>8*1024*1024:
        keep=('schema','test_only','source_sha','skips','stage','scratch_removed','verification','error_type','failure_location',
              'error','cleanup','network_guard','object_put_attempts','refresh_submit_attempts','sdk_cleanup_errors')
        fallback={key:report[key] for key in keep if key in report}
        fallback.update(passed=False,state='incomplete',receipt_error='ReceiptLimit',
                        omitted_evidence_bytes=len(raw),omitted_evidence_sha256=hashlib.sha256(raw).hexdigest())
        report.clear();report.update(fallback);raw=canonical(report)
    require(len(raw)<=8*1024*1024,'finite receipt exceeds bound');path.write_bytes(raw)


def main():
    parser=argparse.ArgumentParser(description=__doc__);sub=parser.add_subparsers(dest='phase',required=True)
    for name in ('prepare','sdk'):
        phase=sub.add_parser(name);phase.add_argument('--source-sha',required=True);phase.add_argument('--output',type=Path,required=True)
    phase=sub.choices['prepare'];phase.add_argument('--public-dir',type=Path,required=True);phase.add_argument('--work-dir',type=Path,required=True);phase.add_argument('--expected-export-source',required=True)
    phase=sub.choices['sdk'];phase.add_argument('--bundle',type=Path,required=True);phase.add_argument('--bundle-sha256',required=True)
    args=parser.parse_args();report={'schema':'wukongim.native_sdk_drill/v1','test_only':True,'source_sha':args.source_sha,
        'passed':False,'cases':[],'skips':0,'state':'incomplete','stage':args.phase,'scratch_removed':False,'verification':dict.fromkeys(FALSE_FIELDS,False),'commands':[]}
    sdk_complete=False
    try:
        if args.phase=='prepare':
            bundle=native_cases(args,report);bundle['native_cases']=copy.deepcopy(report['cases'])
            target=args.work_dir/'bundle.json';target.write_bytes(canonical(bundle));report['bundle_sha256']=facts(target)['sha256'];report['state']='native_prepared';report['passed']=True
        else:
            require(args.bundle.name=='bundle.json' and args.bundle.parent.name=='native-sdk-work','owned bundle directory required')
            guarded_sdk(args,report);sdk_complete=True
    except Exception as error:stage_failure(report,error)
    finally:
        if args.phase=='sdk' and args.bundle.name=='bundle.json' and args.bundle.parent.name=='native-sdk-work':
            cleanup={'attempted':True,'removed':False}
            try:
                if args.bundle.parent.exists():shutil.rmtree(args.bundle.parent)
                cleanup['removed']=not args.bundle.parent.exists()
                if not cleanup['removed']:cleanup['error_type']='CleanupIncomplete'
            except Exception as error:cleanup['error_type']=type(error).__name__
            report['cleanup']=cleanup;report['scratch_removed']=cleanup['removed']
            if not cleanup['removed']:
                report['passed']=False;report['state']='incomplete'
            elif sdk_complete:
                try:
                    report.update(passed=True,state='fixture_complete',stage='sdk_fixture_complete');cleanup_case(args,report)
                except Exception as error:stage_failure(report,error)
        write_report(args.output,report)
    print(json.dumps({key:report.get(key) for key in ('passed','state','stage','error_type','bundle_sha256','scratch_removed')}))
    return 0 if report['passed'] else 1


if __name__=='__main__':raise SystemExit(main())
