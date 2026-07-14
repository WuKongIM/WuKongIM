package deploy

import "fmt"

func nodeConfig(nodeID int, addresses map[string]string) string {
	return fmt.Sprintf(`[node]
id = %d
data_dir = "/var/lib/wukongim-cloud/node"

[cluster]
listen_addr = "0.0.0.0:7000"
hash_slot_count = 256
initial_slot_count = 256
slot_replica_n = 3
slot_tick_interval = "50ms"
slot_heartbeat_tick = 2
slot_election_tick = 20

[[cluster.nodes]]
id = 1
addr = "%s:7000"

[[cluster.nodes]]
id = 2
addr = "%s:7000"

[[cluster.nodes]]
id = 3
addr = "%s:7000"

[api]
listen_addr = "0.0.0.0:5001"
external_tcp_addr = "%s:5100"
external_ws_addr = "ws://%s:5200"

[bench]
api_enable = true
api_max_batch_size = 10000
api_max_payload_bytes = 10485760

[manager]
listen_addr = "0.0.0.0:5301"
auth_on = true
jwt_issuer = "wukongim-cloud-sim"
jwt_expire = "1h"

[gateway]
gnet_multicore = true
gnet_num_event_loop = 4
runtime_async_send_workers = 128
runtime_async_send_queue_capacity = 131072

[[gateway.listeners]]
name = "tcp-wkproto"
network = "tcp"
address = "0.0.0.0:5100"
transport = "gnet"
protocol = "wkproto"

[[gateway.listeners]]
name = "ws-gateway"
network = "websocket"
address = "0.0.0.0:5200"
transport = "gnet"
protocol = "wsmux"

[log]
dir = "/var/lib/wukongim-cloud/logs"
level = "info"

[observability]
debug_api_enable = true
metrics_enable = true

[prometheus]
query_base_url = "http://%s:9090"

[diagnostics]
enable = true
`, nodeID, addresses["node-1"], addresses["node-2"], addresses["node-3"], addresses[fmt.Sprintf("node-%d", nodeID)], addresses[fmt.Sprintf("node-%d", nodeID)], addresses["sim"])
}

func targetConfig(addresses map[string]string) string {
	return fmt.Sprintf(`name: cloud-three-node-cluster
api:
  addrs:
    - http://%s:5001
    - http://%s:5001
    - http://%s:5001
gateway:
  tcp:
    addrs:
      - %s:5100
      - %s:5100
      - %s:5100
bench_api:
  enabled: true
  addrs:
    - http://%s:5001
    - http://%s:5001
    - http://%s:5001
  token: ${WK_BENCH_API_TOKEN}
metrics:
  enabled: true
  addrs:
    - http://%s:5001/metrics
    - http://%s:5001/metrics
    - http://%s:5001/metrics
`, addresses["node-1"], addresses["node-2"], addresses["node-3"], addresses["node-1"], addresses["node-2"], addresses["node-3"], addresses["node-1"], addresses["node-2"], addresses["node-3"], addresses["node-1"], addresses["node-2"], addresses["node-3"])
}

func workerConfig(sourceAddresses []string) string {
	addresses := ""
	for _, address := range sourceAddresses {
		addresses += fmt.Sprintf("      - %s\n", address)
	}
	return fmt.Sprintf(`workers:
  - id: simulator-worker
    addr: http://127.0.0.1:19090
    weight: 1
    control_token: ${WK_BENCH_WORKER_TOKEN}
    tcp_source:
      ipv4_addrs:
%s      port_min: 1024
      port_max: 65535
`, addresses)
}

func prometheusConfig(addresses map[string]string) string {
	return fmt.Sprintf(`global:
  scrape_interval: 10s
  evaluation_interval: 10s
scrape_configs:
  - job_name: wukongim
    static_configs:
      - targets: ["%s:5001"]
        labels: {role: "node-1"}
      - targets: ["%s:5001"]
        labels: {role: "node-2"}
      - targets: ["%s:5001"]
        labels: {role: "node-3"}
  - job_name: hosts
    static_configs:
      - targets: ["%s:9100"]
        labels: {role: "node-1"}
      - targets: ["%s:9100"]
        labels: {role: "node-2"}
      - targets: ["%s:9100"]
        labels: {role: "node-3"}
      - targets: ["%s:9100"]
        labels: {role: "sim"}
`, addresses["node-1"], addresses["node-2"], addresses["node-3"], addresses["node-1"], addresses["node-2"], addresses["node-3"], addresses["sim"])
}

func systemdUnits() map[string]string {
	return map[string]string{
		"wukongim.service": `[Unit]
Description=WuKongIM cloud simulation node
After=network-online.target local-fs.target
Wants=network-online.target

[Service]
Type=simple
User=wukongim
Group=wukongim
EnvironmentFile=/etc/wukongim/node.env
ExecStart=/opt/wukongim/bin/wukongim -config /etc/wukongim/wukongim.toml
Restart=on-failure
RestartSec=2s
LimitNOFILE=1048576
NoNewPrivileges=true

[Install]
WantedBy=multi-user.target
`,
		"wkbench-worker.service": `[Unit]
Description=WuKongIM cloud simulation worker
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=wukongim
Group=wukongim
EnvironmentFile=/etc/wukongim/sim.env
ExecStart=/opt/wukongim/bin/wkbench worker --listen 127.0.0.1:19090 --work-dir /var/lib/wukongim-cloud/worker --control-token ${WK_BENCH_WORKER_TOKEN}
Restart=on-failure
RestartSec=2s
LimitNOFILE=1048576
TasksMax=infinity
NoNewPrivileges=true

[Install]
WantedBy=multi-user.target
`,
		"wkbench-run.service": `[Unit]
Description=WuKongIM cloud simulation one-shot coordinator
After=wkbench-worker.service wkanalysis.service prometheus.service
Requires=wkbench-worker.service

[Service]
Type=oneshot
User=wukongim
Group=wukongim
EnvironmentFile=/etc/wukongim/sim.env
ExecStart=/opt/wukongim/bin/wkbench validate --target /etc/wukongim/target.yaml --workers /etc/wukongim/workers.yaml --scenario /etc/wukongim/scenario.yaml
ExecStart=/opt/wukongim/bin/wkbench doctor --target /etc/wukongim/target.yaml --workers /etc/wukongim/workers.yaml --scenario /etc/wukongim/scenario.yaml
ExecStart=/opt/wukongim/bin/wkbench run --target /etc/wukongim/target.yaml --workers /etc/wukongim/workers.yaml --scenario /etc/wukongim/scenario.yaml
Restart=no
TimeoutStartSec=infinity

[Install]
WantedBy=multi-user.target
`,
		"prometheus.service": `[Unit]
Description=Run-scoped Prometheus
After=network-online.target local-fs.target

[Service]
Type=simple
User=wukongim
Group=wukongim
ExecStart=/opt/wukongim/bin/prometheus --config.file=/etc/wukongim/prometheus.yml --storage.tsdb.path=/var/lib/wukongim-cloud/prometheus --storage.tsdb.retention.time=55h
Restart=on-failure
NoNewPrivileges=true

[Install]
WantedBy=multi-user.target
`,
		"node-exporter.service": `[Unit]
Description=Run-scoped host metrics exporter
After=network-online.target

[Service]
Type=simple
User=wukongim
Group=wukongim
ExecStart=/opt/wukongim/bin/node_exporter --web.listen-address=0.0.0.0:9100 --collector.textfile.directory=/var/lib/wukongim/textfile
Restart=on-failure
NoNewPrivileges=true

[Install]
WantedBy=multi-user.target
`,
		"wkanalysis.service": `[Unit]
Description=WuKongIM run-scoped Analysis MCP
After=network-online.target prometheus.service
Requires=prometheus.service

[Service]
Type=simple
User=wukongim
Group=wukongim
EnvironmentFile=/etc/wukongim/analysis.env
ExecStart=/opt/wukongim/bin/wkanalysis
Restart=on-failure
RestartSec=2s
NoNewPrivileges=true

[Install]
WantedBy=multi-user.target
`,
	}
}
