import {mkdir, readFile, writeFile} from 'node:fs/promises';
import {resolve, join} from 'node:path';

// Render a separate public Demo deployment. The loopback launcher and all
// frontend/backend defaults remain independent of this opt-in configuration.
const keys = ['public_url','project_name','service_prefix','network','cluster_id','mqtt_namespace','source_revision','product_image','node_image','proxy_image'];
const args = process.argv.slice(2);
if (args.length===1 && args[0]==='--help') {
  console.log('node demo/deployment.mjs --config deployment.json --output NEW_DIRECTORY\nWK_DEMO_<UPPERCASE_KEY> overrides the corresponding configuration value.\nRenders files only; does not deploy or modify an existing directory.');
  process.exit(0);
}
try {
  if(args.length!==4 || args[0]!=='--config' || args[2]!=='--output') throw Error('Use --config FILE --output NEW_DIRECTORY');
  const input = JSON.parse(await readFile(resolve(args[1]),'utf8'));
  if(!input || Array.isArray(input) || typeof input!=='object' || Object.keys(input).some(k=>!keys.includes(k))) throw Error('Unknown or invalid deployment configuration');
  const config = Object.fromEntries(keys.map(k=>[k,process.env['WK_DEMO_'+k.toUpperCase()] ?? input[k]]));
  for(const key of keys) if(typeof config[key]!=='string' || !config[key]) throw Error('Missing setting: '+key);
  const url = new URL(config.public_url);
  if(!['http:','https:'].includes(url.protocol) || url.username || url.password || url.pathname!=='/' || url.search || url.hash || ![url.origin,url.origin+'/'].includes(config.public_url) || !/^[a-z0-9.-]+$/.test(url.hostname)) throw Error('public_url must be an HTTP(S) origin without credentials, path, query or fragment');
  if(url.protocol==='http:' && !['127.0.0.1','localhost'].includes(url.hostname)) throw Error('Public deployments require HTTPS; HTTP is allowed only on loopback');
  for(const key of ['project_name','service_prefix','network','cluster_id','mqtt_namespace']) {
    if(!/^[a-z0-9][a-z0-9_.-]{0,63}$/.test(config[key])) throw Error('Invalid identifier: '+key);
  }
  if(!/^[a-f0-9]{40}$/.test(config.source_revision)) throw Error('source_revision must be an exact Git commit');
  for(const key of ['product_image','node_image','proxy_image']) {
    if(!/^[a-zA-Z0-9][a-zA-Z0-9./:_-]*@sha256:[a-f0-9]{64}$/.test(config[key])) throw Error(key+' must be pinned by sha256 digest');
  }
  config.public_url = url.origin;
  const origin = url.origin, ws = origin.replace(/^http/,'ws');
  const product = config.service_prefix+'-product', business = config.service_prefix+'-business', proxy = config.service_prefix+'-proxy';
  const common = {restart:'unless-stopped',security_opt:['no-new-privileges:true'],cap_drop:['ALL'],logging:{driver:'json-file',options:{'max-size':'5m','max-file':'2'}}};
  const compose = {name:config.project_name,services:{
    [product]:{...common,image:config.product_image,cpus:2,mem_limit:'1g',pids_limit:256,environment:{GOMAXPROCS:'2'},volumes:['./wukongim.toml:/etc/wukongim/wukongim.toml:ro','./data:/var/lib/wukongim'],networks:{default:{aliases:[product]}}},
    [business]:{...common,image:config.node_image,user:'1000:1000',cpus:1,mem_limit:'768m',pids_limit:128,read_only:true,tmpfs:['/tmp:size=64m,mode=1777'],network_mode:'service:'+product,depends_on:{[product]:{condition:'service_healthy'}},working_dir:'/workspace',volumes:['./source:/workspace:ro','./business-runner.mjs:/business-runner.mjs:ro'],command:['node','/business-runner.mjs'],environment:{WK_DEMO_API_URL:origin,WK_DEMO_MQTT_WS_URL:ws+'/mqtt'}},
    [proxy]:{...common,image:config.proxy_image,user:'101:101',cpus:0.25,mem_limit:'64m',pids_limit:64,read_only:true,tmpfs:['/var/cache/nginx:size=16m,mode=1777','/var/run:size=1m,mode=1777','/tmp:size=8m,mode=1777'],network_mode:'service:'+product,depends_on:{[business]:{condition:'service_started'}},volumes:['./frontdoor.conf:/etc/nginx/conf.d/default.conf:ro','./source/internal/access/api/demoui/homedist:/srv/demos:ro']},
  },networks:{default:{external:true,name:config.network}}};
  const toml = `# Dedicated Demo single-node cluster; no listener except the frontdoor is public.
[node]
id = 1
data_dir = "/var/lib/wukongim/data"
[cluster]
id = "${config.cluster_id}"
listen_addr = "127.0.0.1:7000"
nodes = [{id = 1, addr = "127.0.0.1:7000"}]
initial_slot_count = 8
hash_slot_count = 256
slot_replica_n = 1
[api]
listen_addr = "127.0.0.1:5001"
# Client-facing route includes the TLS proxy path, not the loopback listener.
external_ws_addr = "${ws}/ws"
[manager]
listen_addr = "127.0.0.1:5301"
[gateway]
token_auth_on = true
listeners = [{name = "ws", network = "websocket", address = "127.0.0.1:5200", transport = "gnet", protocol = "wsmux"}, {name = "mqtt-ws", network = "websocket", address = "127.0.0.1:1884", path = "/mqtt", transport = "gnet", protocol = "mqtt"}]
[mqtt]
enable = true
listen_addr = "127.0.0.1:1883"
namespace = "${config.mqtt_namespace}"
max_connections = 256
[plugin]
enable = false
[log]
level = "warn"
dir = "/var/lib/wukongim/logs"
`;
  let front = `map $http_upgrade $connection_upgrade { default upgrade; '' close; }
map $http_origin $demo_origin_allowed { default 0; "" 1; "${origin}" 1; }
server {
 listen 8088;
 server_name ${url.hostname};
 absolute_redirect off;
 client_max_body_size 128k;
 access_log off;
 add_header X-Content-Type-Options nosniff always;
 location = / { return 302 /demos/; }
 location = /demos { return 302 /demos/; }
 location /demos/ {
  root /srv;
  add_header Cache-Control "no-cache";
  add_header X-Content-Type-Options nosniff;
  try_files $uri $uri/ =404;
 }
 # WSMUX listens on /; strip the public /ws prefix during upgrade.
 location = /ws {
  proxy_pass http://127.0.0.1:5200/;
  proxy_http_version 1.1;
  proxy_set_header Upgrade $http_upgrade;
  proxy_set_header Connection $connection_upgrade;
  proxy_read_timeout 180s;
  proxy_buffering off;
 }
 location /mqtt {
  proxy_pass http://127.0.0.1:1884;
  proxy_http_version 1.1;
  proxy_set_header Upgrade $http_upgrade;
  proxy_set_header Connection $connection_upgrade;
  proxy_read_timeout 180s;
  proxy_buffering off;
 }
 # Validate browser Origin before adapting the strict loopback model relay.
 location = /streamdemo/api/chat {
  if ($demo_origin_allowed = 0) { return 403; }
  proxy_pass http://127.0.0.1:5175;
  proxy_http_version 1.1;
  proxy_set_header Host "127.0.0.1:5175";
  proxy_set_header Origin "http://127.0.0.1:5175";
  proxy_read_timeout 190s;
  proxy_buffering off;
 }
`;
  for(const [name,port] of [['stream',5175],['support',5177],['agent',5178],['mqtt',5179],['live',5180]]) front += ` location /${name}demo/ {
  if ($demo_origin_allowed = 0) { return 403; }
  proxy_pass http://127.0.0.1:${port};
  proxy_http_version 1.1;
  proxy_set_header Host "127.0.0.1:${port}";
  proxy_read_timeout 190s;
  proxy_buffering off;
 }
`;
  front += ` location / {
  proxy_pass http://127.0.0.1:5001;
  proxy_http_version 1.1;
  proxy_set_header Host "${url.host}";
  proxy_read_timeout 120s;
  proxy_buffering off;
 }
}
`;
  const output = resolve(args[3]);
  await mkdir(output); // Deliberately refuse to overwrite a live deployment.
  await writeFile(join(output,'deployment.json'),JSON.stringify(config,null,2)+'\n');
  await writeFile(join(output,'compose.json'),JSON.stringify(compose,null,2)+'\n');
  await writeFile(join(output,'wukongim.toml'),toml);
  await writeFile(join(output,'frontdoor.conf'),front);
  await writeFile(join(output,'business-runner.mjs'),await readFile(new URL('./business-runner.mjs',import.meta.url)));
  console.log(JSON.stringify({output,origin,upstream:product+':8088',sourceRevision:config.source_revision}));
} catch(error) {console.error(error.message);process.exitCode=1;}
