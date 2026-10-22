#!/usr/bin/env python3
"""Pure offline artifact parser/arithmetic probe; no service/process/network actions.

This replays recorded evidence and deterministic identity math. It does not
execute the product or reproduce its original delivery failure.
"""
import argparse, base64, collections, hashlib, json, pathlib, re, struct

def main():
    parser=argparse.ArgumentParser()
    parser.add_argument('--evidence', type=pathlib.Path, required=True)
    args=parser.parse_args(); p=args.evidence
    expected='50fba8e114bf93182734305a312808bc8c840981bd3ba03e869834cd482f7def'
    manifest=p/'SHA256SUMS'
    assert hashlib.sha256(manifest.read_bytes()).hexdigest()==expected
    verified=[]
    for line in manifest.read_text().splitlines():
        digest,name=line.split('  ',1)
        assert not pathlib.PurePosixPath(name).is_absolute() and '..' not in pathlib.PurePosixPath(name).parts
        q=p/name; assert q.is_file() and hashlib.sha256(q.read_bytes()).hexdigest()==digest
        verified.append(name)
    term=p/'terminal'; final=json.loads((term/'final.json').read_text())
    ev=json.loads((term/'evidence/workload/rehearsal/worker-evidence.json').read_text())
    examples=[(w['worker_id'],c,x) for w in ev['workers'] for c in w['evidence']['classes'] for x in c['first']]
    assert len(examples)==1
    worker,c,x=examples[0]
    fingerprint=bytes(x['fingerprint']).hex(); n=x['sample_index']
    assert (worker,c['class'],c['count'],x['stage'],x['code'],x['value'])==(2,3,1,5,11,10000000000)
    domain=n>>61; generation=(n>>43)&((1<<18)-1); ordinal=n&((1<<43)-1)
    assert (domain,generation,ordinal)==(4,1,9014)
    run=ev['run_id']; seed=1
    u64=lambda v:struct.pack('>Q',v)
    key=hashlib.sha256(b'wukongim/chat-lifecycle/run-key/v1'+u64(len(run))+run.encode()+u64(seed)).digest()
    namespace=base64.b32encode(hashlib.sha256(b'identity-namespace/v1'+key).digest()[:10]).decode().lower().rstrip('=')
    def below(purpose,bound,*values):
        prefix=b'wukongim/chat-lifecycle/decision/v1'+key+u64(len(purpose))+purpose.encode()
        suffix=b''.join(u64(v) for v in values)
        draw=int.from_bytes(hashlib.sha256(prefix+suffix).digest()[:8],'big')
        threshold=((1<<64)-bound)%bound
        for attempt in range(1,129):
            if draw>=threshold:return draw%bound
            retry=purpose+'/rejection-retry/v1'
            draw=int.from_bytes(hashlib.sha256(b'wukongim/chat-lifecycle/decision/v1'+key+u64(len(retry))+retry.encode()+suffix+u64(attempt)).digest()[:8],'big')
        raise AssertionError('bounded decision exhausted')
    def b36(v):
        out=''; digits='0123456789abcdefghijklmnopqrstuvwxyz'
        while v:out=digits[v%36]+out;v//=36
        return out or '0'
    def identity_fp(purpose,value):
        return hashlib.sha256(b'wukongim/chat-lifecycle/marker-fingerprint/v1'+key+purpose+value.encode()).digest()[:16]
    matches=[]; attempted=0
    for user_index in range(10000):
        group_index=user_index%2000
        if group_index%3!=worker:continue
        attempted+=1
        sender='wku-'+namespace+'-'+b36(user_index); target='wkg-'+namespace+'-'+b36(group_index)
        raw=hashlib.sha256(b'wukongim/chat-lifecycle/client-msg-no/v1'+key+u64(worker)+u64(n)+b'\x02'+identity_fp(b'sender/v1',sender)+identity_fp(b'target/v1',target)).digest()
        client_msg_no='wkm-'+base64.b32encode(raw[:20]).decode().rstrip('=')
        actual=hashlib.sha256(b'wukongim/chat-lifecycle/message-identity/v1'+client_msg_no.encode()).digest()[:16].hex()
        if actual==fingerprint:
            matches.append({'sender_user_index':user_index,'target_group_index':group_index,'fingerprint':actual})
    assert matches==[{'sender_user_index':1730,'target_group_index':1730,'fingerprint':'dd2ac72c40e88775085c2847098368e0'}]
    group_count=100+below('fixed-group-member-count/v1',401,1730,1)
    assert group_count==435
    group_phase=below('primary-group-class-ordinal-phase/v1',100)
    traffic_phase=below('traffic-kind-ordinal-phase/v1',100)
    sample_phase=below('verification-group-sample-phase/v1',100)
    assert (group_phase,traffic_phase,sample_phase)==(75,69,70)
    assert (n%100+sample_phase)%100==0
    def nominal_group(group_ordinal,worker):
        position=(group_ordinal+group_phase)%100
        category=0 if position<80 else 1 if position<95 else 2
        start=[0,1600,1900][category];count=[1600,300,99][category]
        first=start+(worker+3-start%3)%3;owned=1+(start+count-1-first)//3
        index=first+3*below('primary-owned-group-index/v1',owned,group_ordinal,category,worker)
        return index,category+1
    assert nominal_group(ordinal,worker)==(1730,2)
    # Fixed 90/10 traffic, phase 69: group positions in each 100 are 21..30.
    # Group ordinal 9014 is exactly the fifth group of cycle 901.
    primary_ordinal=next(v for v in range(90100,90200) if sum(1 for q in range(90100,v) if ((q+traffic_phase)%100)>=90)+9010==ordinal and ((v+traffic_phase)%100)>=90)
    assert primary_ordinal==90125 and primary_ordinal%3==worker
    selected=[]
    for go in range(12200):
        raw=go//10*100+(go%10)+21;owner=raw%3
        if nominal_group(go,owner)[0]==1730:
            selected.append({'group_ordinal':go,'worker_id':owner,'primary_ordinal':raw,'ideal_grant_second':raw//2000})
    warnings=[]
    for name in ['service-1.log','service-2.log','service-3.log']:
        for line_number,line in enumerate((term/'evidence/logs'/name).read_text().splitlines(),1):
            if 'online delivery plan incomplete' not in line:continue
            row=json.loads(line[line.index('{'):]); uid=row.pop('uid'); uid_index=int(uid.rsplit('-',1)[1],36)
            warnings.append({'file':name,'line':line_number,'at':re.search(r'\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\.\d+',line)[0], 'result':row['result'],'phase':row['phase'],'recipients':row['recipients'],'uid_user_index':uid_index,'roster_group_index':uid_index%2000,'owner_node_id':row['ownerNodeID']})
    same_group=[v for v in warnings if v['roster_group_index']==1730]
    assert len(warnings)==37 and len(same_group)==1
    assert same_group[0]['phase']=='presence' and same_group[0]['recipients']==group_count
    cuts=[]
    for line_number,line in enumerate((term/'evidence/logs/load.log').read_text().splitlines(),1):
        if '"event":"wkbench.chat_lifecycle.worker_status_cut"' not in line:continue
        row=json.loads(line[line.index('{'):]);cuts.append({'line':line_number,'at':row['at'],'cut':row['cut'],'online':row['totals']['online'],'sent':row['messages']['sent'],'acknowledged':row['messages']['send_acknowledged'],'received':row['messages']['received'],'losses':row['messages']['losses']})
    assert cuts[-2]['online']==10000 and cuts[-2]['losses']==1
    assert cuts[-1]['online']==0 and cuts[-1]['sent']==122002 and cuts[-1]['acknowledged']==122002
    first_loss=next(v for v in cuts if v['losses'])
    idx=cuts.index(first_loss);before=cuts[idx-1]
    output={
      'schema':'wukongim/rc-offline-loss-sample-analysis/v1','mode':'offline-artifact-and-arithmetic-only','product_failure_reproduced':False,
      'input_manifest_sha256':expected,'verified_files':len(verified),
      'run_id':run,'worker_id':worker,'sample_index':n,'sample_fingerprint':fingerprint,
      'decoded':{'domain':domain,'domain_name':'ordinary_group','generation':generation,'group_ordinal':ordinal,'canary':False},
      'fingerprint_reconstruction':{'seed_assumption':seed,'candidate_population':'initial user indexes [0,10000), each fixed roster index modulo2000; target owned by worker2','attempted_candidates':attempted,'matches':matches,'same128bit_fingerprint':True,'actual_source_function_executed':False,'method':'independent Python transcription of frozen Go hash/math'},
      'group':{'nominal_and_actual_fingerprint_target_index':1730,'category':'medium','member_count':group_count,'initial_online_member_indexes':[1730,3730,5730,7730,9730],'worker2_local_member_indexes':[1730,7730],'inferred_distinct_local_recipient_index':7730,'primary_ordinal':primary_ordinal,'ideal_grant_second':primary_ordinal//2000,'ideal_grant_is_not_wire_timestamp':True},
      'same_group_delivery_warning':same_group,
      'all_plan_incomplete_warning_count':len(warnings),'warning_phase_counts':dict(collections.Counter(v['phase'] for v in warnings)),
      'all_nominal_group1730_primary_sends':selected,'fallback_targets_not_replayed':True,
      'loss_observation_bracket':{'last_zero_cut':before,'first_positive_cut':first_loss,'not_exact_expiry_timestamp':True},
      'terminal':{'window':final['window'],'messages':final['messages'],'correlation':final['correlation'],'sessions':final['sessions'],'harness':final['harness'],'historical_average_send_ack_per_second':final['messages']['sent']/(final['window']['elapsed']/1e9),'current_sending':False,'current_qps_measured':False},
      'missing_exact_sample_fields':['registered_at','wire_written_at','positive_sendack_at','MessageID','MessageSeq','selected_recipient_lease_timeline','per-client physical RECV arrival and verifier consumption','exact delivery plan event identity'],
      'network_requests':0,'cloud_mutations':0,'source_changes':0
    }
    print(json.dumps(output,indent=2,sort_keys=True))
if __name__=='__main__':main()
