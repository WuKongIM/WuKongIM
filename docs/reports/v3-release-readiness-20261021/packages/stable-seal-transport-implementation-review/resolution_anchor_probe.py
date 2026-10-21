#!/usr/bin/env python3
"""Bounded independent real-file replacement probe; no crypto/network/native tools."""
import argparse,hashlib,json,tempfile,types
from pathlib import Path
from unittest import mock
ap=argparse.ArgumentParser();ap.add_argument('--source',type=Path,required=True);ap.add_argument('--dependencies',type=Path,required=True);ap.add_argument('--output',type=Path,required=True);a=ap.parse_args()
p=types.ModuleType('review_storage');p.__file__=str(a.dependencies/'scripts/plan-object-storage-publication.py')
exec(compile(a.source.read_bytes(),str(a.source),'exec'),p.__dict__)
class ReachedCandidate(Exception): pass
observed={'source_sha256':hashlib.sha256(a.source.read_bytes()).hexdigest()}
with tempfile.TemporaryDirectory(prefix='wk-review-resolution-') as td:
 root=Path(td).resolve();(root/'control').mkdir();(root/'source').mkdir()
 for n in ('archive','receipt','plan'): (root/n).write_bytes(b'fixture')
 original={'schema':'wukongim/package-audit-release-resolution/v2','repository':'WuKongIM/packages','release_id':4242,'tag_name':'native-package-stable-r4242','name':'WuKongIM stable package snapshot r4242','prerelease':True,'control_sha':'a'*40,'tag_commit_verified':True,'classification':'immutable_complete','draft':False,'immutable':True,'published_at':'2026-10-21T00:00:00Z','asset_count':2,'total_size':2,'assets':[],'release_revalidated':True,'id_bound_downloads':True,'channel':'stable','test_only':True,'candidate':{}}
 old=p.C.canonical_json(original);new=p.C.canonical_json(dict(original,published_at='2026-10-22T00:00:00Z'))
 resolution=root/'resolution.json';resolution.write_bytes(old)
 item={'control_dir':str(root/'control'),'source_dir':str(root/'source'),'archive':str(root/'archive'),'receipt':str(root/'receipt'),'plan':str(root/'plan'),'resolution':str(resolution),'resolution_sha256':hashlib.sha256(old).hexdigest(),'audit_release_id':4242,'seal_control_sha':'a'*40}
 real_read=p.C.read_checked;real_exact=p.C.exact
 def replacing_read(path,label,**kw):
  raw=real_read(path,label,**kw)
  if label=='external seal resolution':resolution.write_bytes(new)
  return raw
 def observe_exact(value,keys,label):
  if label=='seal resolution':observed['actually_parsed_published_at']=value['published_at']
  return real_exact(value,keys,label)
 def reached(*args,**kw):raise ReachedCandidate()
 with mock.patch.object(p.C,'read_checked',side_effect=replacing_read),mock.patch.object(p.C,'exact',side_effect=observe_exact),mock.patch.object(p,'_load',return_value=types.SimpleNamespace(candidate_identity=reached)):
  try:p._stable_snapshot(item,root,True)
  except ReachedCandidate:observed['reached_candidate_gate']=True
 observed.update(original_sha256=hashlib.sha256(old).hexdigest(),replacement_sha256=hashlib.sha256(new).hexdigest(),parsed_bytes_match_anchor=observed.get('actually_parsed_published_at')==original['published_at'],scope='Only resolution hash/parse boundary; candidate verifier stopped deliberately. Original freeze body with unchanged reviewed sibling dependencies. No full plan/signature/GitHub verification claim.')
a.output.write_text(json.dumps(observed,indent=2)+'\n');print(json.dumps(observed))
