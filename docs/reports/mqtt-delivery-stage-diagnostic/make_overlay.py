from pathlib import Path
import json, subprocess, hashlib, argparse

parser=argparse.ArgumentParser()
parser.add_argument('--worktree',type=Path,default=Path.cwd())
parser.add_argument('--out',type=Path,default=Path('/tmp/mqtt-stage-v3-5dfa-overlay'))
args=parser.parse_args()
root=args.worktree.resolve()
out=args.out.resolve()
out.mkdir(parents=True,exist_ok=True)
files = {}
def edit(rel):
    if rel not in files: files[rel] = (root / rel).read_text()
    return files[rel]
def replace(rel, old, new):
    s = edit(rel)
    assert s.count(old) == 1, (rel, old, s.count(old))
    files[rel] = s.replace(old, new)
def imports(rel, names):
    s = edit(rel)
    add = ''.join('\t"'+n+'"\n' for n in names if '"'+n+'"' not in s)
    files[rel] = s.replace('import (\n', 'import (\n'+add, 1)

stages = ['unattributed','turn','state','recovery','selection','account','window','final_enqueue','account_auth','window_auth','final_auth','other_auth','background_account','ack','background_account_auth']
helper = r'''
// Temporary diagnostic overlay: fixed-size aggregate counters, no identities.
var diagStageNames = []string{STAGES}
var diagStageBounds = [...]int64{1000000,2000000,5000000,10000000,20000000,50000000,100000000,250000000,500000000,1000000000,5000000000}
type diagStageCell struct { count, ns atomic.Uint64; hist [12]atomic.Uint64 }
var diagStageCells [15*40]diagStageCell
var diagStageResults [15][12]atomic.Uint64
var diagStageEmit atomic.Int64
var diagStageEnqueued atomic.Uint64
var diagStageRecoveryEmpty atomic.Uint64
func diagStageID(ctx context.Context) int { if ctx != nil { if n,ok := ctx.Value("DEBUG-mqtt-stage-v3-5dfa").(int); ok && n>=0 && n<len(diagStageNames) {return n} }; return 0 }
func diagStageBegin(ctx context.Context, stage int) (context.Context, func()) {
 if ctx != nil {ctx = context.WithValue(ctx,"DEBUG-mqtt-stage-v3-5dfa",stage)}
 start:=time.Now(); return ctx,func(){diagStageRecord(stage,0,time.Since(start))}
}
func diagStageRecord(stage, kind int, elapsed time.Duration) {
 if stage<0 || stage>=15 || kind<0 || kind>=40 {return}
 c:=&diagStageCells[stage*40+kind]; c.count.Add(1); c.ns.Add(uint64(elapsed)); b:=0;for b<len(diagStageBounds) && int64(elapsed)>diagStageBounds[b] {b++};c.hist[b].Add(1)
 now:=time.Now().UnixNano(); previous:=diagStageEmit.Load();if now-previous<5*int64(time.Second) || !diagStageEmit.CompareAndSwap(previous,now) {return}
 type row struct { Stage string `json:"stage"`; Kind int `json:"kind"`; Calls uint64 `json:"calls"`; NS uint64 `json:"ns"`; Hist [12]uint64 `json:"hist"`; Results [12]uint64 `json:"results"` }
 rows:=make([]row,0,64);for i:=range diagStageCells {c:=&diagStageCells[i];if c.count.Load()==0 {continue};r:=row{Stage:diagStageNames[i/40],Kind:i%40,Calls:c.count.Load(),NS:c.ns.Load()};for j:=range r.Hist {r.Hist[j]=c.hist[j].Load()};if i%40==0 {for j:=range r.Results {r.Results[j]=diagStageResults[i/40][j].Load()}};rows=append(rows,r)}
 encoded,_:=json.Marshal(struct{Component string `json:"component"`; At int64 `json:"unix_ns"`; Enqueued uint64 `json:"enqueued"`; Empty uint64 `json:"recovery_empty"`; Rows []row `json:"rows"`}{COMPONENT,now,diagStageEnqueued.Load(),diagStageRecoveryEmpty.Load(),rows});fmt.Fprintf(os.Stderr,"[DEBUG-mqtt-stage-v3-5dfa] %s\n",encoded)
}
'''.replace('STAGES', ','.join(json.dumps(s) for s in stages))
def append_helper(rel, component):
    imports(rel, ['encoding/json','fmt','os','sync/atomic','time'])
    files[rel] += helper.replace('COMPONENT',json.dumps(component))

def instrument(rel, signature, ctx, stage, extra=''):
    replace(rel,signature,signature+f'\n\t{ctx}, diagFinish := diagStageBegin({ctx}, {stage})\n\tdefer diagFinish()\n'+extra)

co='internal/usecase/mqttsession/delivery_coordinator.go'
instrument(co,'func (s *ConnectionDelivery) Turn(parent context.Context) (out runtime.DeliveryWork, err error) {','parent',1)
instrument(co,'func (s *ConnectionDelivery) connectionState(ctx context.Context) (*meta.MQTTSession, error) {','ctx',2)
instrument(co,'func (s *ConnectionDelivery) selectSource(parent context.Context) (out deliverySelection, err error) {','parent',4)
append_helper(co,'usecase')
imports(co,['github.com/WuKongIM/WuKongIM/pkg/channel'])
files[co]=files[co].replace('\"github.com/WuKongIM/WuKongIM/pkg/channel\"','ch \"github.com/WuKongIM/WuKongIM/pkg/channel\"')
replace('internal/usecase/mqttsession/subscriptions.go','ctx, cancel := context.WithDeadline(op.Context(), deadline)','ctx, cancel := context.WithDeadline(op.Context(), deadline)\n\tctx = context.WithValue(ctx, \"DEBUG-mqtt-stage-v3-5dfa\", diagStageID(parent))')
instrument('internal/usecase/mqttsession/acknowledgements.go','func (a *Acknowledgements) Acknowledge(parent context.Context, q AcknowledgementCommand) (out AcknowledgementResult, err error) {','parent',13)
instrument('internal/usecase/mqttsession/accounting.go','func (a *Accounting) Account(parent context.Context, key meta.MQTTDeliveryCursorKey) (out AccountingResult, err error) {','parent','func() int {if diagStageID(parent)==0 {return 12};return 5}()')
instrument('internal/usecase/mqttsession/window_admission.go','func (w *WindowAdmission) Prepare(parent context.Context, o contract.Owner, key meta.MQTTDeliveryCursorKey) (out WindowPreparation, err error) {','parent',6)
instrument('internal/usecase/mqttsession/exchange_recovery.go','func (r *ExchangeRecovery) Next(parent context.Context, o contract.Owner, after meta.MQTTInflightCursor) (out RecoveryPreparation, err error) {','parent',3,'\tdefer func(){if out.Delivery==nil && err==nil {diagStageRecoveryEmpty.Add(1)}}()\n')
instrument('internal/usecase/mqttsession/sender_completion.go','func (s *DeliveryStream) authorizeEnqueue(ctx context.Context, op *subscriptionOperation, d PreparedDelivery) error {','ctx',7)
instrument('internal/usecase/mqttsession/receive_authorization.go','func (a *ReceiveAuthorization) AuthorizeSubscription(ctx context.Context, uid string, r SubscriptionRequest) (version uint64, err error) {','ctx','func() int {switch diagStageID(ctx) {case 5:return 8;case 12:return 14;case 6:return 9;case 7:return 10};return 11}()')
replace('internal/usecase/mqttsession/sender.go','\t\tout.Enqueued = true','\t\tout.Enqueued = true\n\t\tdiagStageEnqueued.Add(1)')

node='pkg/cluster/node_mqtt.go'
replace(node,'func (n *Node) ReadMQTT(ctx context.Context, q metadb.MQTTRead) (metadb.MQTTReadResult, error) {','func (n *Node) ReadMQTT(ctx context.Context, q metadb.MQTTRead) (metadb.MQTTReadResult, error) {\n\tdiagStart:=time.Now();defer func(){diagStageRecord(diagStageID(ctx),int(q.Kind),time.Since(diagStart))}()')
append_helper(node,'node')
for rel, signature, kind in [
 ('pkg/cluster/node_mqtt_plan.go','func (n *Node) PlanChannelMQTTReplay(ctx context.Context, q ch.MQTTReplayPlanRequest) (ch.MQTTReplayPlan, error) {',24),
 ('pkg/cluster/node_mqtt_replay.go','func (n *Node) ReadChannelMQTTReplay(ctx context.Context, req ch.MQTTReplayConsumerRequest) (ch.MQTTReplayConsumerPage, error) {',25),
 ('pkg/cluster/node_mqtt_source.go','func (s defaultChannelRuntimeMetaStore) GetChannelRuntimeMetaFresh(ctx context.Context, id string, typ int64) (metadb.ChannelRuntimeMeta, error) {',26),
]:
    imports(rel,['time'])
    replace(rel, signature, signature+f'\n\tdiagStart:=time.Now();defer func(){{diagStageRecord(diagStageID(ctx),{kind},time.Since(diagStart))}}()')

bar='pkg/slot/multiraft/read_barrier.go'
replace(bar,'\tdefer func() { observeReadBarrier(r.opts.Observer, err, time.Since(started)) }()', '\tdefer func() { observeReadBarrier(r.opts.Observer, err, time.Since(started)) }()\n\tdefer func(){diagStageRecord(diagStageID(ctx),0,time.Since(started))}()')
append_helper(bar,'barrier')

run='internal/runtime/mqttsession/deliveries.go'
replace(run,'\tdue     time.Time','\tdue     time.Time\n\tdiagQueued time.Time')
replace(run,'\t\t\t\te = heap.Pop(&s.due).(*deliveryEntry)', '\t\t\t\te = heap.Pop(&s.due).(*deliveryEntry)\n\t\t\t\tdiagStageRecord(1,0,now.Sub(e.due))\n\t\t\t\te.diagQueued=now')
replace(run,'\t\t\tif err := s.queue.SubmitWait(s.ctx, e); err != nil {','\t\t\tdiagStart:=time.Now()\n\t\t\terrSubmit:=s.queue.SubmitWait(s.ctx,e)\n\t\t\tdiagStageRecord(2,0,time.Since(diagStart))\n\t\t\tif err := errSubmit; err != nil {')
replace(run,'func (s *Deliveries) execute(_ context.Context, e *deliveryEntry) error {','func (s *Deliveries) execute(_ context.Context, e *deliveryEntry) error {\n\tdiagStageRecord(3,0,time.Since(e.diagQueued))\n\tdiagStart:=time.Now();defer func(){diagStageRecord(4,0,time.Since(diagStart))}()')
append_helper(run,'scheduler')

fixture='test/e2e/mqtt/scale/scale_test.go'
def fixture_first(old,new):
    s=edit(fixture);assert old in s;files[fixture]=s.replace(old,new,1)
fixture_first('\tapi := n.APIAddr()','\tapi := n.APIAddr()\n\tdefer func(){b,e:=os.ReadFile(n.Spec.StderrPath);if e==nil {lines:=bytes.Split(b,[]byte("\\n"));var kept [][]byte;for _,line:=range lines {if bytes.HasPrefix(line,[]byte("[DEBUG-mqtt-stage-v3-5dfa]")){kept=append(kept,line)}};if len(kept)>200 {kept=kept[len(kept)-200:]};_ = os.WriteFile(filepath.Join(os.Getenv("WK_E2E_MQTT_REPORT_DIR"),"stage-aggregates.log"),bytes.Join(kept,[]byte("\\n")),0600)}}()')
imports(fixture,['bytes'])
replace(fixture,'\t\tt.Logf("phase %s completed in %d ms", name, phases[name])','\t\tt.Logf("phase %s completed in %d ms unix_ns=%d", name, phases[name],time.Now().UnixNano())')
replace(fixture,'\tphase = "fanout"','\tphase = "fanout"\n\tt.Logf("phase fanout started unix_ns=%d",time.Now().UnixNano())')


for rel, marker, ctx in [
 (co,'func (s *ConnectionDelivery) Turn(parent context.Context)', 'parent'),
 ('internal/usecase/mqttsession/accounting.go','func (a *Accounting) Account(parent context.Context','parent'),
 ('internal/usecase/mqttsession/window_admission.go','func (w *WindowAdmission) Prepare(parent context.Context','parent'),
 ('internal/usecase/mqttsession/exchange_recovery.go','func (r *ExchangeRecovery) Next(parent context.Context','parent'),
 ('internal/usecase/mqttsession/acknowledgements.go','func (a *Acknowledgements) Acknowledge(parent context.Context','parent'),
 ('internal/usecase/mqttsession/receive_authorization.go','func (a *ReceiveAuthorization) AuthorizeSubscription(ctx context.Context','ctx')]:
    content=edit(rel); begin=content.index(marker); pos=content.index('defer diagFinish()',begin)+len('defer diagFinish()')
    content=content[:pos]+'\n\tdefer func(){diagStageResult(diagStageID('+ctx+'),err)}()'+content[pos:]
    files[rel]=content
files[co]+=r"""
func diagStageResult(stage int, err error) {n:=0;switch {case err==nil:case errors.Is(err,ch.ErrNotReady):n=8;case errors.Is(err,ch.ErrBackpressured):n=9;case errors.Is(err,runtime.ErrOwnerLimit):n=10;case errors.Is(err,ErrClock)||errors.Is(err,ErrFenced)||errors.Is(err,runtime.ErrOwnerFenced):n=11;case errors.Is(err,ErrConflict):n=1;case errors.Is(err,ErrEvidence):n=2;case errors.Is(err,context.Canceled):n=3;case errors.Is(err,context.DeadlineExceeded):n=4;default:n=5};diagStageResults[stage][n].Add(1)}
"""

mapping={}
for i,(rel,data) in enumerate(files.items()):
    p=out / f'{i}-{Path(rel).name}'
    p.write_text(data)
    subprocess.run(['gofmt','-w',str(p)],check=True)
    mapping[str(root/rel)]=str(p)
(out/'overlay.json').write_text(json.dumps({'Replace':mapping},indent=2)+'\n')
frozen={}
for p in [root/'AGENTS.md',root/'internal/usecase/mqttsession/FLOW.md',root/'internal/runtime/mqttsession/FLOW.md',root/'internal/infra/cluster/FLOW.md',root/'pkg/cluster/FLOW.md',root/'pkg/slot/FLOW.md',root/'test/e2e/AGENTS.md',root/'test/e2e/mqtt/AGENTS.md',root/'test/e2e/mqtt/scale/AGENTS.md',root/'test/e2e/suite/FLOW.md',root/'pkg/db/FLOW.md',root/'pkg/db/meta/FLOW.md',root/'internal/access/mqtt/FLOW.md',root/'internal/app/FLOW.md']:
    frozen[str(p.relative_to(root))]=hashlib.sha256(p.read_bytes()).hexdigest()
(out/'instructions.json').write_text(json.dumps(frozen,indent=2)+'\n')
print('Overlay ready:',len(mapping),'files; no repository source edits')

