import {spawn} from 'node:child_process';

// One container owns all five bounded loopback helpers. A failed child stops
// the remaining helpers so Compose can restart the complete business runtime.
const children=[];
let stopping=false, exitCode=0;
function stop(code=0) {
  if(stopping) return;
  stopping=true;exitCode=code;
  for(const child of children) child.kill('SIGTERM');
  setTimeout(()=>{for(const child of children) child.kill('SIGKILL');process.exit(exitCode);},6000).unref();
}
for(const [name,port] of [['stream',5175],['support',5177],['agent',5178],['mqtt',5179],['live',5180]]) {
  const child=spawn(process.execPath,['--max-old-space-size=128','server.mjs'],{cwd:`/workspace/demo/${name}demo`,env:{...process.env,WK_DEMO_PORT:String(port)},stdio:'inherit'});
  children.push(child);
  child.on('error',()=>stop(1));
  child.on('exit',code=>{
    if(!stopping) stop(code||1);
    if(children.every(c=>c.exitCode!==null || c.signalCode!==null)) process.exit(exitCode);
  });
}
for(const signal of ['SIGTERM','SIGINT']) process.once(signal,()=>stop());
