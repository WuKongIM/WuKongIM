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


def sdk_cases(args,report):
    """All assertions are against actual SDK requests, bytes and public receipts."""
    contracts=module(ROOT/'tests/provider_sdk/test_aliyun_object_storage_provider.py')
    provider=module(ROOT/'scripts/aliyun-object-storage-provider.py')
    bundle=load_bundle(args.bundle,args.bundle_sha256,args.source_sha)
    report['cases']=copy.deepcopy(bundle['native_cases'])
    a=read_json(Path(bundle['generations']['A']['delivery']));b=read_json(Path(bundle['generations']['B']['delivery']))
    report['resource_intent_sha256']=bundle['resource_intent_sha256'];report['sdk_resource_sha256']=bundle['sdk_resource_sha256']
    owned=[]
    def client(wire=None):
        wire=wire or contracts.Wire()
        value=provider.Provider(resource=contracts.RESOURCE,access_key_id='FIXTURE-AK',access_key_secret=contracts.SECRET,security_token=contracts.TOKEN,http_adapter=wire)
        owned.append(value);return value,wire
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
            else:assert len(wire.objects)==154 and outcomes[label].count('already_exact')==152
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
                else:assert checked_object('A',item,c,w)['outcome']=='unknown_but_exact'
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
            c,w=client();changed=copy.deepcopy(item);changed['source']['audit_release_id']=999
            refused(lambda:checked_object('A',changed,c,w));assert not writes(w)
            mirror=next(row for row in b['objects'] if row['object_key'] not in {x['object_key'] for x in a['objects']})
            refused(lambda:checked_object('A',mirror,c,w));assert not writes(w)
        case(CASES[10],source_binding)
        config_results={}
        def config_a():
            before=shared.observe_config();result=apply_configuration(bundle,args,'A',shared,wire,before,[])
            assert result['state']=='configured';config_results['A']=result
            functions=provider.compile_rewrites(a['routes']);assert len(functions)==17
            assert contracts.inverse_finite_rules(functions)=={x['public_uri']:'/'+x['object_key'] for x in a['routes']}
            assert len([r for r in wire.requests if r[2]=='BatchSetCdnDomainConfig'])==1
        case(CASES[11],config_a)
        def config_b():
            result=apply_configuration(bundle,args,'B',shared,wire,shared.observe_config(),config_results['A']['config_ids'])
            assert result['state']=='configured' and result['config_ids']==config_results['A']['config_ids'];config_results['B']=result
            left=provider.compile_rewrites(a['routes']);right=provider.compile_rewrites(b['routes'])
            assert [x['functionArgs'][0] for x in left]==[x['functionArgs'][0] for x in right]
            assert sum(x!=y for x,y in zip(left,right))==2
        case(CASES[12],config_b)
        def config_fence():
            c,w=client();before=c.observe_config();before['configs']=[]
            refused(lambda:apply_configuration(bundle,args,'A',c,w,before,[]));assert not writes(w)
            before=c.observe_config();w.configs.append(contracts.config('unmanaged',{'x':'y'},99))
            refused(lambda:apply_configuration(bundle,args,'A',c,w,before,[]));assert not writes(w)
        case(CASES[13],config_fence)
        def deletion():
            c,w=client();result=c.apply_rewrites([contracts.route()],c.observe_config(),[]);before=len(writes(w))
            refused(lambda:apply_configuration(bundle,args,'A',c,w,c.observe_config(),result['config_ids']));assert len(writes(w))==before
        case(CASES[14],deletion)
        def partial_config():
            for fault in ('zero','missing','duplicate','wrongid','partial','timeout'):
                c,w=client();w.batch_fault=fault;result=apply_configuration(bundle,args,'A',c,w,c.observe_config(),[])
                assert result['state']=='incomplete' and result['batches_attempted']==1
                assert len(writes(w))==1 and not any(r[2]=='RefreshObjectCaches' for r in w.requests)
        case(CASES[15],partial_config)
        def full_refresh():
            for label,delivery in (('A',a),('B',b)):
                c,w=client(contracts.RefreshWire());result=refresh_generation(bundle,args,label,c,w)
                assert result['state']=='tasks_complete' and result['task_ids']==['33643947491']
                assert [row['ObjectPath'] for row in w.rows]==refresh_scope(delivery['routes'])['urls']
                assert len(w.rows)==178 and w.submissions==4 and w.polls==1
        case(CASES[16],full_refresh)
        def aggregate_faults():
            for fault in ('missing','duplicate','total','foreign_url','submit_timeout','query_timeout'):
                c,w=client(contracts.RefreshWire(fault=fault));refused(lambda:refresh_generation(bundle,args,'A',c,w))
                assert w.submissions==(2 if fault=='submit_timeout' else 4)
                assert 0<len(writes(w))<=4
                report.setdefault('incomplete_observations',[]).append({'fault':fault,'state':'incomplete','submissions_attempted':w.submissions,'task_rows_observed':len(w.rows),'repeated_writes':False})
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
        for value in owned:value.close()


# Operational helpers and CLI are intentionally absent for the tests-first freeze.
if __name__=='__main__':
    raise SystemExit('RED: native SDK fixture CLI has not been wired')
