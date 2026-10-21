from pathlib import Path
import json,hashlib,subprocess
D=Path(__file__).resolve().parent
R=Path('/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/v3-rc-terminal-evidence')
s=(R/'scripts/chat_lifecycle_direct_lab_test.go').read_text()
start=s.index('func TestChatLifecycleDirectLabStartBuildsAndQuotesBeforePaidAcquire(')
end=s.index('\nfunc ',start+5)
f=s[start:end].strip()
(D/'original-test-function.go.txt').write_text(f+'\n')
shim='''#!/usr/bin/env python3
import json,os,sys,time,subprocess
p=os.environ['DIRECT_LAB_GO_TRACE']
start=time.monotonic()
with open(p,'a') as f: f.write(json.dumps({'event':'start','argv':sys.argv[1:],'monotonic':start})+'\\n')
r=subprocess.run([os.environ['DIRECT_LAB_REAL_GO'],*sys.argv[1:]])
with open(p,'a') as f: f.write(json.dumps({'event':'exit','argv':sys.argv[1:],'elapsed_s':time.monotonic()-start,'exit_code':r.returncode})+'\\n')
sys.exit(r.returncode)
'''
(D/'go-trace.py').write_text(shim)
inject='''
	traceDir := filepath.Join(directory, "trace-bin")
	if err := os.Mkdir(traceDir, 0700); err != nil { t.Fatal(err) }
	traceSource, err := os.ReadFile(os.Getenv("DIRECT_LAB_GO_TRACE_SOURCE"))
	if err != nil { t.Fatal(err) }
	writeDirectLabExecutable(t, filepath.Join(traceDir, "go"), string(traceSource))
	goTrace := filepath.Join(directory, "go-trace.jsonl")
	unusedTool := filepath.Join(directory, "unused-control-tool")
	writeDirectLabExecutable(t, unusedTool, "#!/usr/bin/env bash\\nprintf 'unexpected-unused-control-tool\\\\n' >>\\\"$WK_TEST_CALL_LOG\\\"\\nexit 96\\n")
'''
f=f.replace('\tcallLog := filepath.Join(directory, "calls")','\tcallLog := filepath.Join(directory, "calls")'+inject)
needle='\tif output, err := command.CombinedOutput(); err != nil {'
setup='''
	command.Env = append(command.Env,
		"PATH="+traceDir+":"+os.Getenv("PATH"),
		"DIRECT_LAB_GO_TRACE="+goTrace,
		"DIRECT_LAB_REAL_GO="+os.Getenv("DIRECT_LAB_REAL_GO"),
	)
	if os.Getenv("DIRECT_LAB_INJECT_UNUSED_TOOLS") == "true" {
		command.Env = append(command.Env, "WK_CHAT_LAB_GATE_TOOL="+unusedTool, "WK_CHAT_LAB_BUNDLE_TOOL="+unusedTool)
	}
	fixtureStarted := time.Now()
'''
f=f.replace(needle,setup+needle)
f=f[:-1]+'''
	fixtureElapsed := time.Since(fixtureStarted).Seconds()
	traceRaw, traceErr := os.ReadFile(goTrace)
	if traceErr != nil && !os.IsNotExist(traceErr) { t.Fatal(traceErr) }
	trace := []json.RawMessage{}
	for _, line := range strings.Split(strings.TrimSpace(string(traceRaw)), "\\n") {
		if line != "" { trace = append(trace, json.RawMessage(line)) }
	}
	jsonFiles := map[string]any{}
	files := map[string]any{}
	entries, err := os.ReadDir(requestDirectory)
	if err != nil { t.Fatal(err) }
	for _, entry := range entries {
		info, err := entry.Info(); if err != nil { t.Fatal(err) }
		files[entry.Name()] = map[string]any{"mode":fmt.Sprintf("%04o",info.Mode().Perm()),"directory":entry.IsDir()}
		if strings.HasSuffix(entry.Name(), ".json") {
			raw, err := os.ReadFile(filepath.Join(requestDirectory,entry.Name())); if err != nil { t.Fatal(err) }
			var v any; if err := json.Unmarshal(raw,&v); err != nil { t.Fatal(err) }; jsonFiles[entry.Name()]=v
		}
	}
	tools, err := os.ReadDir(filepath.Join(requestDirectory,"tools")); if err != nil { t.Fatal(err) }
	toolNames := []string{}; for _, entry := range tools { toolNames = append(toolNames, entry.Name()) }
	writeScratchReceipt(t, map[string]any{"schema":"wukongim.fixture_probe/v1","original_assertions_passed":true,"injected_unused_tools":os.Getenv("DIRECT_LAB_INJECT_UNUSED_TOOLS")=="true","fixture_elapsed_s":fixtureElapsed,"operation_calls":string(calls),"go_calls":trace,"json_files":jsonFiles,"files":files,"tool_names":toolNames})
	if len(trace) != 0 { t.Errorf("unused control-tool builds still executed: %s",traceRaw) }
}
'''
header='''package scripts_test
import("os";"os/exec";"path/filepath";"strings";"testing";"encoding/json";"fmt";"time")
func repoRoot(t *testing.T) string {t.Helper();return os.Getenv("DIRECT_LAB_FIXTURE_SOURCE")}
func writeDirectLabExecutable(t *testing.T, path, body string) {t.Helper();if err:=os.WriteFile(path,[]byte(body),0700);err!=nil{t.Fatal(err)}}
func writeScratchReceipt(t *testing.T,v any){t.Helper();b,e:=json.MarshalIndent(v,"","  ");if e!=nil{t.Fatal(e)};if e=os.WriteFile(os.Getenv("DIRECT_LAB_DIAG_ARTIFACT"),append(b,'\\n'),0600);e!=nil{t.Fatal(e)}}
'''
(D/'fixture_test.go').write_text(header+f)
freeze={'head':subprocess.check_output(['git','-C',str(R),'rev-parse','HEAD'],text=True).strip(),'source_files':[{ 'path':p, 'sha256':hashlib.sha256((R/p).read_bytes()).hexdigest()} for p in ['AGENTS.md','CONTEXT.md','scripts/chat_lifecycle_direct_lab_test.go','scripts/chat-lifecycle/direct-lab.sh','scripts/script_test_helpers_test.go','scripts/script_test_helpers_integration_test.go']], 'scratch_files':[{'path':p,'sha256':hashlib.sha256((D/p).read_bytes()).hexdigest()} for p in ['plan-and-failure-matrix.md','original-test-function.go.txt','go-trace.py','fixture_test.go']]}
(D/'before-freeze.json').write_text(json.dumps(freeze,indent=2)+'\n')
