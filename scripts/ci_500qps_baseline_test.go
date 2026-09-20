package scripts_test

import (
	"os"
	"os/exec"
	"testing"
)

// Pure fixture checks never call GitHub, git, or a background process.
func Test500QPSBaselineRequiresEquivalentInputsAndCompleteTrustedRun(t *testing.T) {
	source := `
import datetime, importlib.util
s=importlib.util.spec_from_file_location('baseline','ci-500qps-baseline.py');m=importlib.util.module_from_spec(s);s.loader.exec_module(m)
now=datetime.datetime(2026,9,20,tzinfo=datetime.timezone.utc)
base='a'*40;head='b'*40;old='c'*40
run={'id':1,'run_attempt':1,'head_sha':old,'head_branch':'main','status':'completed','conclusion':'success','event':'schedule','path':'.github/workflows/'+m.WORKFLOW,'created_at':'2026-09-19T00:00:00Z'}
jobs={'total_count':4,'jobs':[{'name':n,'status':'completed','conclusion':'success','run_attempt':1,'head_sha':old,'run_id':1} for n in m.REQUIRED_JOBS]}
def choose(r=run, paths=['docs/example.md'], upstream=[], ancestor=True, evidence=jobs):
 return m.choose(base,head,[{**r,'id':i} for i in range(1,4)],now,lambda *_:ancestor,lambda a,b:paths if a==base else upstream,lambda run_id:{**evidence,'jobs':[{**j,'run_id':run_id} for j in evidence['jobs']]})['reuse']
assert choose()
assert not m.complete_jobs(jobs,{**run,'id':2})
for field,value in [('run_attempt',2),('head_sha','d'*40)]:
 bad=[{**j,field:value} for j in jobs['jobs']]
 assert not choose(evidence={'total_count':4,'jobs':bad})
assert not m.choose(base,head,[run]*3,now,lambda *_:True,lambda *_:['docs/a.md'],lambda _:jobs)['reuse']
assert not m.choose(base,head,[run],now,lambda *_:True,lambda *_:['docs/a.md'],lambda _:jobs)['reuse']
sequence=[{**run,'id':i} for i in range(1,5)];sequence[1]['conclusion']='failure'
assert not m.choose(base,head,sequence,now,lambda *_:True,lambda *_:['docs/a.md'],lambda _:jobs)['reuse']
for path in ['internal/app/app.go','go.sum','wukongim.toml.example','scripts/run-500qps-seam.sh','.github/workflows/'+m.WORKFLOW]:
 assert not choose(paths=[path]), path
 assert not choose(upstream=[path]), path
for field,value in [('head_branch','candidate'),('conclusion','failure'),('status','in_progress'),('run_attempt',2),('event','pull_request'),('path','.github/workflows/other.yml'),('created_at','2026-09-01T00:00:00Z'),('created_at','2026-09-21T00:00:00Z'),('head_sha','invalid')]:
 assert not choose(r={**run,field:value}),field
assert not choose(ancestor=False)
assert not choose(evidence={**jobs,'total_count':5})
assert not choose(evidence={**jobs,'jobs':jobs['jobs'][:-1]})
for i in range(4):
 bad=[dict(j) for j in jobs['jobs']];bad[i]['conclusion']='skipped'
 assert not choose(evidence={'total_count':4,'jobs':bad})
assert not choose(evidence={'total_count':5,'jobs':jobs['jobs']+[jobs['jobs'][0]]})
assert not m.choose(base,head,[],now,None,lambda *_:['docs/a.md'],None)['reuse']
`
	command := exec.Command("python3", "-c", source)
	command.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("baseline evidence contract: %v\n%s", err, output)
	}
}
