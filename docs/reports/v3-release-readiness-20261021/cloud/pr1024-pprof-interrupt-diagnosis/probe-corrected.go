package main

import (
 "encoding/json"
 "fmt"
 "io"
 "net/http"
 "net/http/httptest"
 "os"
 "os/exec"
 "path/filepath"
 "strconv"
 "strings"
 "sync"
 "sync/atomic"
 "syscall"
 "time"
)

type Event struct { Seconds float64 `json:"seconds"`; Kind string `json:"kind"`; Node int `json:"node,omitempty"`; Active int64 `json:"active,omitempty"` }
func main(){ os.Exit(run()) }
func run() int {
 root,dir,mode:=os.Args[1],os.Args[2],os.Args[3]
 if mode!="before" && mode!="after" {panic("invalid mode")}
 if err:=os.Mkdir(dir,0700);err!=nil {panic(err)}
 started:=time.Now();var mu sync.Mutex;events:=[]Event{};add:=func(kind string,node int,active int64){mu.Lock();events=append(events,Event{time.Since(started).Seconds(),kind,node,active});mu.Unlock()}
 var active [3]atomic.Int64
 servers:=make([]*httptest.Server,3)
 for i:=range servers {node:=i;servers[i]=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  active[node].Add(1);add("request_started",node+1,active[node].Load());defer func(){active[node].Add(-1);add("handler_return",node+1,active[node].Load())}()
  if node==0 && r.URL.Path=="/debug/pprof/profile" {if err:=os.WriteFile(filepath.Join(dir,"request-started"),[]byte("ready"),0600);err!=nil{panic(err)}}
  select{case <-time.After(10*time.Second):add("delay_elapsed",node+1,active[node].Load());case <-r.Context().Done():add("context_done",node+1,active[node].Load());return}
  _,_=io.WriteString(w,"profile:"+r.URL.Path)
 }));defer servers[i].Close()}
 phase:=filepath.Join(dir,"phase");os.WriteFile(phase,[]byte("measurement\n"),0600)
 envFile:=filepath.Join(dir,"timing-hook.sh")
 hook:=`PROBE_MAIN_PID=$$
PROBE_FIRED=0
set -T
probe_boundary() {
 [[ "$PROBE_FIRED" == 0 ]] || return 0
 local hit=0
 case "$PROBE_MODE:$BASH_COMMAND" in
  'before:CAPTURE_PIDS+=("$!")') hit=1 ;;
  after:start_profile_request*heap*) hit=1 ;;
 esac
 [[ "$hit" == 1 ]] || return 0
 PROBE_FIRED=1
 local ready=0
 for ((probe_i=0; probe_i<200; probe_i++)); do
  if [[ -f "$PROBE_DIR/request-started" ]]; then ready=1; break; fi
  sleep .01
 done
 [[ "$ready" == 1 ]] || exit 97
 printf 'helper_pid=%s\nchild_pid=%s\nregistered=%s\n' "$$" "$!" "${#CAPTURE_PIDS[@]}" >"$PROBE_DIR/boundary.txt"
 kill -TERM "$PROBE_MAIN_PID"
}
trap probe_boundary DEBUG
`
 os.WriteFile(envFile,[]byte(hook),0600)
 args:=[]string{filepath.Join(root,"scripts/capture-wukongim-local-threshold-pprof.sh"),"--out-dir",filepath.Join(dir,"capture"),"--phase-state-file",phase,"--trigger-kind","sendack_p99","--trigger-observed-phase","measurement","--previous-utc","2026-08-13T08:00:00.123456789+08:00","--current-utc","2026-08-13T08:00:01.500+08:00"}
 for _,s:=range servers{args=append(args,"--node",s.URL)};args=append(args,"--cpu-seconds","1")
 cmd:=exec.Command("bash",args...);cmd.Dir=root;cmd.Env=append(os.Environ(),"WK_BENCH_API_TOKEN=synthetic-probe-token","BASH_ENV="+envFile,"PROBE_DIR="+dir,"PROBE_MODE="+mode);cmd.SysProcAttr=&syscall.SysProcAttr{Setpgid:true}
 log,err:=os.OpenFile(filepath.Join(dir,"helper-output.log"),os.O_CREATE|os.O_EXCL|os.O_WRONLY,0600);if err!=nil{panic(err)};defer log.Close();cmd.Stdout=log;cmd.Stderr=log
 add("helper_start",0,0);if err:=cmd.Start();err!=nil{panic(err)}
 helperPID:=cmd.Process.Pid;defer syscall.Kill(-helperPID,syscall.SIGKILL)
 done:=make(chan error,1);go func(){done<-cmd.Wait()}()
 var waitErr error
 select{case waitErr=<-done:case <-time.After(12*time.Second):syscall.Kill(-helperPID,syscall.SIGKILL);waitErr=<-done}
 add("helper_exit",0,0);exit:=cmd.ProcessState.ExitCode();boundaryBody,_:=os.ReadFile(filepath.Join(dir,"boundary.txt"));boundary:=map[string]int{}
 for _,line:=range strings.Split(strings.TrimSpace(string(boundaryBody)),"\n"){parts:=strings.SplitN(line,"=",2);if len(parts)==2{value,_:=strconv.Atoi(parts[1]);boundary[parts[0]]=value}}
 pidState:=func()string{if boundary["child_pid"]<=0{return "not_recorded"};out,_:=exec.Command("ps","-p",strconv.Itoa(boundary["child_pid"]),"-o","pid=,ppid=,state=,comm=").Output();return strings.TrimSpace(string(out))}
 childAtExit:=pidState();atExit:=[]int64{active[0].Load(),active[1].Load(),active[2].Load()}
 deadline:=time.Now().Add(2*time.Second)
 for time.Now().Before(deadline){if active[0].Load()+active[1].Load()+active[2].Load()==0{break};time.Sleep(10*time.Millisecond)}
 atTwo:=[]int64{active[0].Load(),active[1].Load(),active[2].Load()};childAtTwo:=pidState();add("original_zero_assertion",0,atTwo[0]+atTwo[1]+atTwo[2])
 temporary:=[]string{};filepath.WalkDir(filepath.Join(dir,"capture"),func(path string,entry os.DirEntry,err error)error{if err==nil&&!entry.IsDir()&&strings.Contains(entry.Name(),".next."){rel,_:=filepath.Rel(filepath.Join(dir,"capture"),path);temporary=append(temporary,rel)};return nil})
 finalDeadline:=started.Add(9*time.Second)
 for time.Now().Before(finalDeadline){if active[0].Load()+active[1].Load()+active[2].Load()==0{break};time.Sleep(10*time.Millisecond)}
 finalActive:=[]int64{active[0].Load(),active[1].Load(),active[2].Load()};childAtEnd:=pidState();syscall.Kill(-helperPID,syscall.SIGKILL)
 for _,server:=range servers{server.Close()}
 metadataBody,_:=os.ReadFile(filepath.Join(dir,"capture/metadata.json"));var metadata any;json.Unmarshal(metadataBody,&metadata)
 passed:=exit==0&&atTwo[0]+atTwo[1]+atTwo[2]==0&&len(temporary)==0
 receipt:=map[string]any{"mode":mode,"source_root":root,"boundary":boundary,"helper_exit_code":exit,"wait_error":fmt.Sprint(waitErr),"active_at_helper_exit":atExit,"active_at_original_two_second_assertion":atTwo,"active_at_end":finalActive,"child_at_helper_exit":childAtExit,"child_at_two_second_assertion":childAtTwo,"child_at_end":childAtEnd,"temporary_files_at_assertion":temporary,"original_contract_pass":passed,"metadata":metadata,"events":events,"signal_hook_only":true,"repository_edited":false,"group_cleanup_attempted":true}
 body,_:=json.MarshalIndent(receipt,"","  ");os.WriteFile(filepath.Join(dir,"receipt.json"),append(body,'\n'),0600)
 fmt.Printf("mode=%s helper_exit=%d registered=%d active_at_exit=%v active_at_2s=%v final_active=%v temporary=%v contract_pass=%v\n",mode,exit,boundary["registered"],atExit,atTwo,finalActive,temporary,passed)
 if !passed{return 1};return 0
}
