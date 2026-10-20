#!/usr/bin/env python3
"""No-load parser tests for the separate offline auditor."""
import copy
import importlib.util
import json
from pathlib import Path
import unittest

spec=importlib.util.spec_from_file_location('audit',Path(__file__).with_name('verify_artifact.py'))
m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
p=json.loads(Path(__file__).with_name('expected-profile.json').read_text())
ids=[{'pid':i,'starttime_ticks':i*10} for i in range(1,5)]

def stat(pid):
    return f'{pid} (owned) S '+' '.join(['0']*18+[str(pid*10)])

def proc(i):
    cpus='0' if i==1 else '0,2-3'
    raw={'content':stat(i)};status={'content':f'Cpus_allowed_list:\t{cpus}\n'}
    return {'valid':True,'expected':dict(ids[i-1],affinity=cpus),'stat':ids[i-1], 'affinity':cpus,'enumerated_threads':1,'valid_threads':1,'stat_file':raw,'stat_after_threads':raw,'status':status,'threads':[{'tid':i,'starttime_ticks':i*10,'affinity':cpus,'valid':True,'stat_before':raw,'stat_after':raw,'status':status}]}

base={'runtime_loads':0,'membership_writes':0,'active_runtimes_before':0,'active_runtimes_after':0,'allocated_bytes':10,'start':'2026-10-21T00:00:00Z','phases':[]}
for ep,q in [('/conversation/list',200),('/conversation/sync',60)]:
    base['phases'].append({'case':{'endpoint':ep,'page_size':100,'offered_qps':q},'nodes':3,'driver_workers':8,'duration_seconds':60,'queue_capacity':q//2,'unexpected_errors':0,'verdict':'pass','scheduled':q*60,'completed':q*60,'completed_in_window':q*60,'errors':0,'dropped':0,'p99_ms':10,'actual_qps':q})
base['cpu_samples']={'schema':'wukongim/cpu-window-samples/v1','valid':True,'duration_ns':60000000000,'start':base['start'],'stop_reason':'caller_stop','missed_slots':0,'finished_offset_ns':59995000000,'points':[{'slot':j,'valid':True,'start_offset_ns':j*10**9,'read_duration_ns':1000000,'deadline_lag_ns':0,'processes':[proc(i) for i in range(1,5)]} for j in range(60)]}

def audit(w):
    a=m.Audit(); result=m.check_window(a,w,ids,[[0],[0,2,3],[0,2,3],[0,2,3]],p,'fixture')
    return a,result

class Check(unittest.TestCase):
    def test_complete_original_arrival_interval(self):
        a,r=audit(copy.deepcopy(base));self.assertEqual(a.failures,[]);self.assertTrue(r['numeric_pass'])
    def test_numeric_reject_preserved_without_corrupting_integrity(self):
        w=copy.deepcopy(base);w['phases'][0]['p99_ms']=501;w['phases'][0]['verdict']='P99 exceeds budget'
        a,r=audit(w);self.assertEqual(a.failures,[]);self.assertFalse(r['numeric_pass'])
    def test_incomplete_point_coverage(self):
        w=copy.deepcopy(base);w['cpu_samples']['points']=w['cpu_samples']['points'][:1]
        self.assertTrue(audit(w)[0].failures)
    def test_thread_mask_and_raw_identity(self):
        for mutate in [lambda x:x.update(affinity='1'),lambda x:x['stat_before'].update(content=stat(99))]:
            w=copy.deepcopy(base);mutate(w['cpu_samples']['points'][1]['processes'][0]['threads'][0]);self.assertTrue(audit(w)[0].failures)
    def test_early_stop(self):
        w=copy.deepcopy(base);w['cpu_samples']['finished_offset_ns']=59994999999;self.assertTrue(audit(w)[0].failures)
    def test_confirmed_exit_retained(self):
        w=copy.deepcopy(base);w['cpu_samples']['points'][1]['processes'][0]['exited_or_unobserved_threads']=['44:880']
        a,r=audit(w);self.assertEqual(a.failures,[]);self.assertEqual(r['confirmed_exit_observations'],1)

if __name__=='__main__':unittest.main(verbosity=2)
