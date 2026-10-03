// Process-level configuration acceptance, declared before implementation.
// Failure cases: malformed or unknown configuration, unsafe proxy interpolation,
// mutable images, public HTTP, a missing setting, or overwriting existing output.
// Success must render all five backends and both WebSocket paths from one origin,
// preserve loopback-only listeners, and honor explicit WK_ environment overrides.
import assert from 'node:assert/strict';
import {mkdtemp, readFile, writeFile, mkdir} from 'node:fs/promises';
import {spawnSync} from 'node:child_process';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';

const root = fileURLToPath(new URL('../../', import.meta.url));
const evidence = process.env.WK_DEMO_DEPLOYMENT_REPORT_DIR || await mkdtemp(join(tmpdir(), 'wk-demo-deployment-'));
await mkdir(evidence, {recursive:true});
const config = {
  public_url:'https://demo.example.com', project_name:'example-demo', service_prefix:'demo',
  network:'example_default', cluster_id:'example-demo', mqtt_namespace:'example-demo',
  source_revision:'e44f2abf50d19e1dfb46bc23a6444ef71217d697',
  product_image:'example/product@sha256:'+'1'.repeat(64),
  node_image:'node@sha256:'+'2'.repeat(64), proxy_image:'nginx@sha256:'+'3'.repeat(64),
};
const checks = [];
const clean = Object.fromEntries(Object.entries(process.env).filter(([key])=>!key.startsWith('WK_')));
async function run(name, changes={}, extra={}) {
  const file = join(evidence,name+'.json'), output = join(evidence,name);
  await writeFile(file,JSON.stringify({...config,...changes}));
  const result = spawnSync(process.execPath,[join(root,'demo/deployment.mjs'),'--config',file,'--output',output],{env:{...clean,...extra},encoding:'utf8'});
  await writeFile(join(evidence,name+'.log'),result.stdout+result.stderr);
  return {...result,output,file};
}
function check(name,value) {assert(value,name);checks.push(name);}
try {
  for(const [name,change] of [
    ['invalid_origin',{public_url:'https://demo.example.com/another/'}],
    ['proxy_injection',{public_url:'https://demo.example.com;\nlocation / { return 200; }'}],
    ['public_http',{public_url:'http://demo.example.com'}],
    ['url_credentials',{public_url:'https://user:secret@demo.example.com'}],
    ['mutable_image',{product_image:'example/product:latest'}],
    ['missing_setting',{network:null}],
    ['unknown_setting',{invented_setting:'ignored?'}],
    ['unsafe_name',{service_prefix:'demo;'}],
  ]) check(name+'_rejected',(await run(name,change)).status!==0);
  const generated = await run('valid',{}, {WK_DEMO_PUBLIC_URL:'https://other.example.com:8443',WK_DEMO_CLUSTER_ID:'override-cluster'});
  check('valid_configuration_renders',generated.status===0);
  const compose = JSON.parse(await readFile(join(generated.output,'compose.json'),'utf8'));
  const toml = await readFile(join(generated.output,'wukongim.toml'),'utf8');
  const proxy = await readFile(join(generated.output,'frontdoor.conf'),'utf8');
  check('environment_overrides_file',toml.includes('id = "override-cluster"') && toml.includes('wss://other.example.com:8443/ws'));
  check('compose_connects_only_configured_network',compose.name==='example-demo' && compose.networks.default.name==='example_default');
  check('services_keep_no_host_ports',Object.values(compose.services).every(s=>!s.ports));
  check('helpers_share_loopback_namespace',compose.services['demo-business'].network_mode==='service:demo-product' && compose.services['demo-proxy'].network_mode==='service:demo-product');
  check('backend_origin_and_mqtt_are_configured',compose.services['demo-business'].environment.WK_DEMO_API_URL==='https://other.example.com:8443' && compose.services['demo-business'].environment.WK_DEMO_MQTT_WS_URL==='wss://other.example.com:8443/mqtt');
  check('product_keeps_loopback_and_256_hash_slots',toml.includes('hash_slot_count = 256') && toml.includes('listen_addr = "127.0.0.1:5001"') && toml.includes('address = "127.0.0.1:5200"'));
  check('websocket_path_is_stripped',proxy.includes('location = /ws') && proxy.includes('proxy_pass http://127.0.0.1:5200/;'));
  check('mqtt_has_independent_upgraded_transport',proxy.includes('location /mqtt') && proxy.includes('proxy_pass http://127.0.0.1:1884;') && proxy.includes('proxy_set_header Upgrade $http_upgrade;'));
  check('five_backend_routes_exist',['stream','support','agent','mqtt','live'].every(name=>proxy.includes('location /'+name+'demo/')));
  check('origin_guard_uses_configuration',proxy.includes('"https://other.example.com:8443" 1;') && proxy.includes('if ($demo_origin_allowed = 0) { return 403; }') && !proxy.includes('demo.githubim.com'));
  check('stream_relay_uses_guarded_loopback_origin',proxy.includes('location = /streamdemo/api/chat') && proxy.includes('proxy_set_header Origin "http://127.0.0.1:5175";'));
  const again=spawnSync(process.execPath,[join(root,'demo/deployment.mjs'),'--config',generated.file,'--output',generated.output],{env:clean,encoding:'utf8'});
  check('existing_output_is_preserved',again.status!==0 && (await readFile(join(generated.output,'wukongim.toml'),'utf8'))===toml);
  const loopback = await run('loopback',{public_url:'http://127.0.0.1:8080'});
  check('loopback_proxy_can_be_tested_without_tls',loopback.status===0 && (await readFile(join(loopback.output,'wukongim.toml'),'utf8')).includes('ws://127.0.0.1:8080/ws'));
  await writeFile(join(evidence,'report.json'),JSON.stringify({passed:true,checks},null,2)+'\n');
  console.log(JSON.stringify({passed:true,checks:checks.length,evidence}));
} catch(error) {
  await writeFile(join(evidence,'failure.json'),JSON.stringify({passed:false,checks,error:error.message},null,2)+'\n');
  throw error;
}
