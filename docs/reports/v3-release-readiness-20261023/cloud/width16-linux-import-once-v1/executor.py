from pathlib import Path
import os,json,hashlib,subprocess,selectors,time,signal,datetime,re,uuid,tarfile,traceback
os.umask(0o077);e=Path('/tmp/wukongim-v3-readiness-20261023/cloud/width16-linux-import-once-v1');e.mkdir(exist_ok=False);logs=e/'logs';logs.mkdir();old=Path('/tmp/wukongim-v3-readiness-20261023/cloud/width16-linux-scratch-once');plan=json.loads(Path('/tmp/wukongim-v3-readiness-20261023/cloud/width16-linux-scratch-plan-v1/recipe.json').read_text());source=Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/v3-rc-exact-source-width16-candidate');records=[];owned=[];results=[];image_id=None;image_removed=False
baseenv={'PATH':'/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin','HOME':os.environ['HOME']}
if 'TMPDIR' in os.environ:baseenv['TMPDIR']=os.environ['TMPDIR']
def sha(p):return hashlib.sha256(p.read_bytes()).hexdigest()
def git(*a):return subprocess.check_output(['git',*a],cwd=source,text=True).strip()
assert git('rev-parse','HEAD')==plan['source']['head'] and git('rev-parse','HEAD^{tree}')==plan['source']['tree'] and not git('status','--short')
elf=json.loads((old/'binary-elf-receipt.json').read_text())
for v in elf:
 p=old/'context'/(v['name']+'.test');assert sha(p)==v['sha256'] and p.stat().st_mode&0o777==0o555
(e/'source-binary-binding.json').write_text(json.dumps({'source':str(source),'head':git('rev-parse','HEAD'),'tree':git('rev-parse','HEAD^{tree}'),'clean':True,'binary_receipts':elf,'prior_actual_buildinfo_sha256':sha(old/'logs/binary-build-info.stdout'),'prior_attempt_manifest_sha256':sha(old/'SHA256SUMS'),'no_recompile':True},indent=2)+'\n')
rootfs=e/'empty-rootfs.tar'
with tarfile.open(rootfs,'w',format=tarfile.USTAR_FORMAT) as t:
 for name,mode in [('tests/',0o555),('tmp/',0o1777)]:
  info=tarfile.TarInfo(name);info.type=tarfile.DIRTYPE;info.mode=mode;info.uid=info.gid=0;info.mtime=0;info.uname=info.gname='';info.size=0;t.addfile(info)
assert rootfs.stat().st_size==10240
(e/'tar-receipt.json').write_text(json.dumps({'sha256':sha(rootfs),'bytes':rootfs.stat().st_size,'format':'USTAR','entries':[{'name':'tests/','mode':'0555'},{'name':'tmp/','mode':'1777'}],'uid':0,'gid':0,'mtime':0,'files':0,'host_reads_during_tar_generation':False},indent=2)+'\n')
def run(name,args,outer=15,outcap=1048576,errcap=65536,allow_error=False):
 assert not (logs/(name+'.json')).exists();print('START '+name,flush=True);t0=time.monotonic();started=datetime.datetime.now(datetime.timezone.utc).isoformat();p=subprocess.Popen(args,cwd=e,env=baseenv,stdout=subprocess.PIPE,stderr=subprocess.PIPE,start_new_session=True);sel=selectors.DefaultSelector();files={};sizes={'stdout':0,'stderr':0};caps={'stdout':outcap,'stderr':errcap};killed=None
 for n,pipe in [('stdout',p.stdout),('stderr',p.stderr)]:os.set_blocking(pipe.fileno(),False);sel.register(pipe,selectors.EVENT_READ,n);files[n]=open(logs/(name+'.'+n),'xb')
 while sel.get_map():
  if killed is None and time.monotonic()-t0>outer:killed='outer_timeout';os.killpg(p.pid,signal.SIGKILL)
  for key,_ in sel.select(.1):
   data=os.read(key.fileobj.fileno(),65536);n=key.data
   if not data:sel.unregister(key.fileobj);continue
   remain=max(0,caps[n]-sizes[n]);files[n].write(data[:remain]);sizes[n]+=len(data)
   if sizes[n]>caps[n] and killed is None:killed=n+'_cap';os.killpg(p.pid,signal.SIGKILL)
 rc=p.wait()
 for f in files.values():f.close()
 r={'phase':name,'argv':args,'started_utc':started,'elapsed_seconds':round(time.monotonic()-t0,3),'exit_code':rc,'outer_or_output_kill':killed,'pgid':p.pid,'observed_bytes':sizes,'outer_seconds':outer,'stdout_bytes_limit':outcap,'stderr_bytes_limit':errcap};records.append(r);(logs/(name+'.json')).write_text(json.dumps(r,indent=2)+'\n');print('DONE '+json.dumps(r),flush=True)
 if not allow_error:assert rc==0 and killed is None,(name,rc,killed)
 return (logs/(name+'.stdout')).read_text(errors='replace'),rc
try:
 helptext,_=run('import-help',['/usr/local/bin/docker','image','import','--help'],5,8192);assert '--platform' in helptext and '--change' in helptext
 tag='wukongim-width16-import:4cb45b340-once-v1';body,_=run('tag-preflight',['/usr/local/bin/docker','image','ls','--no-trunc','--filter','reference='+tag,'--format','{{.ID}}']);assert not body.strip(),'new tag already exists; refuse overwrite'
 body,_=run('rootfs-import',['/usr/local/bin/docker','image','import','--platform','linux/arm64','--change','USER 65534:65534','--change','WORKDIR /tmp',str(rootfs),tag],15);image_id=body.strip();assert re.fullmatch('sha256:[0-9a-f]{64}',image_id)
 tpl='{"Id":{{json .Id}},"Os":{{json .Os}},"Architecture":{{json .Architecture}},"RepoDigests":{{json .RepoDigests}},"Config":{{json .Config}}}'
 body,_=run('image-inspect',['/usr/local/bin/docker','image','inspect','--format',tpl,image_id]);image=json.loads(body);assert image['Id']==image_id and image['Os']=='linux' and image['Architecture']=='arm64' and image['Config']['User']=='65534:65534' and image['Config']['WorkingDir']=='/tmp' and not image['RepoDigests'];image['daemon_reported_config_digest']=image_id;image['config_projection_sha256']=hashlib.sha256(json.dumps(image['Config'],sort_keys=True,separators=(',',':')).encode()).hexdigest();image['registry_manifest_digest']=None;(e/'image-receipt.json').write_text(json.dumps(image,indent=2)+'\n');print('IMPORT_VERIFIED '+image_id,flush=True)
 expected={'presence':{'TestRecoveryOwnedUIDByteBudgetRemainsIndependentOfWidth','TestAuthorityRecoveryWidth16AdmitsCohortsAndFullKeyBudget','TestAuthorityRecoveryWidth16AdmitsCohortsAndFullKeyBudget/1','TestAuthorityRecoveryWidth16AdmitsCohortsAndFullKeyBudget/512','TestAuthorityRecoveryWidth16Keeps128MiBByteBudgetWithFreeCohorts','TestAuthorityRecoveryUIDFlightQuotaRechecksProofAndDoesNotReservePartialKeys'},'cluster':{'TestPresenceRecoveryOwnerAdmissionAllowsSixteenRoundsAndCancelsSeventeenth'}}
 for r in plan['runtime']:
  name=r['name'];binary=old/'context'/(name+'.test');cname='wk-width16-import-'+name+'-'+uuid.uuid4().hex[:12]
  args=['/usr/local/bin/docker','create','--name',cname,'--label','wukongim.width16.import=4cb45b340-once-v1','--platform','linux/arm64','--pull=never','--network=none','--cpus=2','--memory=2g','--memory-swap=2g','--pids-limit=128','--read-only','--cap-drop=ALL','--security-opt=no-new-privileges','--user=65534:65534','--tmpfs','/tmp:rw,nosuid,nodev,noexec,size=64m,mode=1777','--mount','type=bind,src='+str(binary)+',dst=/tests/'+name+'.test,readonly',image_id]+r['binary_argv']
  body,_=run('create-'+name,args);cid=body.strip();assert re.fullmatch('[0-9a-f]{64}',cid);owned.append((name,cid))
  output,cli=run('run-'+name,['/usr/local/bin/docker','start','--attach',cid],r['outer_seconds'],allow_error=True)
  body,_=run('state-'+name,['/usr/local/bin/docker','inspect','--type','container','--format','{{json .State}}',cid]);state=json.loads(body);passed=set(re.findall(r'^\s*--- PASS: (\S+)',output,re.M));failed=re.findall(r'^\s*--- FAIL: (\S+)',output,re.M);ok=cli==0 and state['ExitCode']==0 and not state['OOMKilled'] and not state['Running'] and not failed and passed==expected[name] and re.search(r'^PASS\s*$',output,re.M) is not None
  result={'name':name,'container_id':cid,'cli_exit':cli,'state':state,'expected_names':sorted(expected[name]),'actual_passed_names':sorted(passed),'failed_names':failed,'pass':ok};results.append(result);(e/'runtime-results.json').write_text(json.dumps(results,indent=2)+'\n');print('RUNTIME_RESULT '+json.dumps(result),flush=True);assert ok,('runtime assertion failure',name)
  run('remove-'+name,['/usr/local/bin/docker','rm','--force',cid],20);_,rc=run('absence-'+name,['/usr/local/bin/docker','inspect','--type','container',cid],10,allow_error=True);assert rc!=0;owned.remove((name,cid))
 assert all(sha(old/'context'/(v['name']+'.test'))==v['sha256'] for v in elf);assert not git('status','--short')
 run('remove-owned-import-image',['/usr/local/bin/docker','image','rm',image_id],20);image_removed=True;(e/'SUCCESS').write_text('Both normal-only Linux targeted integration groups passed; exact containers and owned import image removed. Linuxrace and performance unverified.\n');print('PIPELINE_SUCCESS',flush=True)
except BaseException as exc:
 (e/'first-failure.txt').write_text(type(exc).__name__+': '+str(exc)+'\n'+traceback.format_exc());print('PIPELINE_FIRST_FAILURE '+type(exc).__name__+': '+str(exc),flush=True);raise
finally:
 for name,cid in list(owned):
  try:
   run('cleanup-'+name,['/usr/local/bin/docker','rm','--force',cid],20,allow_error=True)
   run('cleanup-absence-'+name,['/usr/local/bin/docker','inspect','--type','container',cid],10,allow_error=True)
  except BaseException as exc:print('CLEANUP_ERROR '+str(exc),flush=True)
 try:
  body,rc=run('final-task-tags',['/usr/local/bin/docker','image','ls','--no-trunc','--filter','reference=wukongim-width16-scratch:4cb45b340-local','--filter','reference=wukongim-width16-import:4cb45b340-once-v1','--format','{{json .}}'],10)
  (e/'final-task-tags.json').write_text(json.dumps({'exit_code':rc,'stdout':body,'old_iid_exists':(old/'image.iid').exists(),'new_image_id':image_id,'new_image_removed':image_removed},indent=2)+'\n')
 except BaseException as exc:print('FINAL_METADATA_ERROR '+str(exc),flush=True)
 (e/'execution-summary.json').write_text(json.dumps({'records':records,'runtime_results':results,'success':(e/'SUCCESS').exists(),'image_id':image_id,'image_removed':image_removed,'source':str(source),'head':plan['source']['head'],'tree':plan['source']['tree']},indent=2)+'\n')
