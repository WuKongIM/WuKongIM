#!/usr/bin/env bash
set -euo pipefail

required_files=(
  "ui/channels.html"
  "ui/index.html"
  "ui/connections.html"
  "ui/dashboard.html"
  "ui/groups.html"
  "ui/network.html"
  "ui/nodes.html"
  "ui/topology.html"
  "ui/placeholder/groups.html"
  "ui/placeholder/network.html"
  "ui/placeholder/topology.html"
  "ui/placeholder/connections.html"
  "ui/assets/styles.css"
  "ui/assets/data.js"
  "ui/assets/app.js"
)

for file in "${required_files[@]}"; do
  [[ -f "$file" ]] || { echo "missing: $file" >&2; exit 1; }
done

html_files=(
  "ui/channels.html"
  "ui/index.html"
  "ui/connections.html"
  "ui/dashboard.html"
  "ui/groups.html"
  "ui/network.html"
  "ui/nodes.html"
  "ui/topology.html"
  "ui/placeholder/groups.html"
  "ui/placeholder/network.html"
  "ui/placeholder/topology.html"
  "ui/placeholder/connections.html"
)

for file in "${html_files[@]}"; do
  grep -q 'tailwindcss.com' "$file" || { echo "missing tailwind cdn: $file" >&2; exit 1; }
  grep -q '\./assets/styles.css\|../assets/styles.css' "$file" || { echo "missing styles reference: $file" >&2; exit 1; }
  grep -q '\./assets/app.js\|../assets/app.js' "$file" || { echo "missing app.js reference: $file" >&2; exit 1; }
done

! rg -n "<svg" ui >/dev/null || { echo "inline svg is forbidden" >&2; exit 1; }
rg -n "unpkg.com/lucide-static@latest/icons/" ui >/dev/null || { echo "missing lucide static cdn usage" >&2; exit 1; }

for file in ui/placeholder/*.html; do
  grep -q '该页面将在下一阶段扩展' "$file" || { echo "missing placeholder copy: $file" >&2; exit 1; }
done

dashboard_files=("ui/index.html" "ui/dashboard.html")
for file in "${dashboard_files[@]}"; do
  grep -q 'data-page="dashboard"' "$file" || { echo "missing dashboard page marker: $file" >&2; exit 1; }
done

grep -q 'renderDashboard' ui/assets/app.js || { echo "missing renderDashboard" >&2; exit 1; }
grep -q '在线节点数' ui/assets/data.js || { echo "missing dashboard metric copy" >&2; exit 1; }
grep -q '风险摘要' ui/assets/app.js || { echo "missing risk section" >&2; exit 1; }
grep -q '集群快照' ui/assets/app.js || { echo "missing cluster snapshot" >&2; exit 1; }

grep -q 'data-page="nodes"' ui/nodes.html || { echo "missing nodes marker" >&2; exit 1; }
grep -q '节点列表' ui/assets/app.js || { echo "missing nodes heading" >&2; exit 1; }
grep -q '节点ID / 地址关键词' ui/assets/app.js || { echo "missing node keyword filter" >&2; exit 1; }
grep -q '程序版本' ui/assets/app.js || { echo "missing program version column" >&2; exit 1; }
grep -q '配置版本' ui/assets/app.js || { echo "missing config version column" >&2; exit 1; }
grep -q '离线次数' ui/assets/app.js || { echo "missing offline count column" >&2; exit 1; }
grep -q '日志' ui/assets/app.js || { echo "missing log action" >&2; exit 1; }
grep -q 'bindNodeFilters' ui/assets/app.js || { echo "missing node filters binder" >&2; exit 1; }
! grep -q 'data-open-node' ui/assets/app.js || { echo "unexpected node detail action" >&2; exit 1; }
! grep -q 'data-node-drawer' ui/assets/app.js || { echo "unexpected node drawer container" >&2; exit 1; }
grep -q 'programVersion' ui/assets/data.js || { echo "missing node program version data" >&2; exit 1; }
grep -q 'configVersion' ui/assets/data.js || { echo "missing node config version data" >&2; exit 1; }
grep -q 'filter-grid' ui/assets/styles.css || { echo "missing node filter grid styles" >&2; exit 1; }

grep -q 'pageHref' ui/assets/app.js || { echo "missing relative path helper" >&2; exit 1; }
grep -q 'dashboard.html' ui/assets/data.js || { echo "missing dashboard nav target" >&2; exit 1; }
grep -q 'channels.html' ui/assets/data.js || { echo "missing channels nav target" >&2; exit 1; }
grep -q 'connections.html' ui/assets/data.js || { echo "missing connections nav target" >&2; exit 1; }
grep -q 'nodes.html' ui/assets/data.js || { echo "missing nodes nav target" >&2; exit 1; }
grep -q 'groups.html' ui/assets/data.js || { echo "missing groups nav target" >&2; exit 1; }
grep -q 'network.html' ui/assets/data.js || { echo "missing network nav target" >&2; exit 1; }
grep -q 'topology.html' ui/assets/data.js || { echo "missing topology nav target" >&2; exit 1; }
grep -q 'data-base="."' ui/index.html || { echo "index missing base marker" >&2; exit 1; }
grep -q 'data-base="."' ui/channels.html || { echo "channels missing base marker" >&2; exit 1; }
grep -q 'data-base="."' ui/connections.html || { echo "connections missing base marker" >&2; exit 1; }
grep -q 'data-base="."' ui/dashboard.html || { echo "dashboard missing base marker" >&2; exit 1; }
grep -q 'data-base="."' ui/groups.html || { echo "groups missing base marker" >&2; exit 1; }
grep -q 'data-base="."' ui/network.html || { echo "network missing base marker" >&2; exit 1; }
grep -q 'data-base="."' ui/nodes.html || { echo "nodes missing base marker" >&2; exit 1; }
grep -q 'data-base="."' ui/topology.html || { echo "topology missing base marker" >&2; exit 1; }
for file in ui/placeholder/*.html; do
  grep -q 'data-base=".."' "$file" || { echo "placeholder missing base marker: $file" >&2; exit 1; }
done

grep -q '\-\-accent-primary' ui/assets/styles.css || { echo "missing accent token" >&2; exit 1; }
grep -q 'drawer' ui/assets/styles.css || { echo "missing drawer styles" >&2; exit 1; }

grep -q 'data-page="groups"' ui/groups.html || { echo "missing groups page marker" >&2; exit 1; }
grep -q 'renderGroups' ui/assets/app.js || { echo "missing renderGroups" >&2; exit 1; }
grep -q '槽管理' ui/assets/app.js || { echo "missing groups heading" >&2; exit 1; }
grep -q 'Slot ID' ui/assets/app.js || { echo "missing groups table header" >&2; exit 1; }
grep -q 'Leader 节点' ui/assets/app.js || { echo "missing leader column" >&2; exit 1; }
grep -q '查看 Slot' ui/assets/app.js || { echo "missing group drawer trigger" >&2; exit 1; }
grep -q 'data-group-drawer' ui/assets/app.js || { echo "missing group drawer container" >&2; exit 1; }
grep -q 'slot-17' ui/assets/data.js || { echo "missing groups sample data" >&2; exit 1; }

grep -q 'data-page="network"' ui/network.html || { echo "missing network page marker" >&2; exit 1; }
grep -q 'renderNetwork' ui/assets/app.js || { echo "missing renderNetwork" >&2; exit 1; }
grep -q '网络监控' ui/assets/app.js || { echo "missing network heading" >&2; exit 1; }
grep -q 'RPC 链路健康' ui/assets/app.js || { echo "missing network section" >&2; exit 1; }
grep -q '链路矩阵' ui/assets/app.js || { echo "missing link matrix section" >&2; exit 1; }
grep -q '查看链路' ui/assets/app.js || { echo "missing network drawer trigger" >&2; exit 1; }
grep -q 'data-link-drawer' ui/assets/app.js || { echo "missing network drawer container" >&2; exit 1; }
grep -q 'node1→node3' ui/assets/data.js || { echo "missing network sample data" >&2; exit 1; }

grep -q 'data-page="connections"' ui/connections.html || { echo "missing connections page marker" >&2; exit 1; }
grep -q 'renderConnections' ui/assets/app.js || { echo "missing renderConnections" >&2; exit 1; }
grep -q '在线连接' ui/assets/app.js || { echo "missing connections heading" >&2; exit 1; }
grep -q '活跃连接概况' ui/assets/app.js || { echo "missing connections summary section" >&2; exit 1; }
grep -q '连接会话列表' ui/assets/app.js || { echo "missing session list section" >&2; exit 1; }
grep -q '查看连接' ui/assets/app.js || { echo "missing connection drawer trigger" >&2; exit 1; }
grep -q 'data-connection-drawer' ui/assets/app.js || { echo "missing connection drawer container" >&2; exit 1; }
grep -q 'conn-10f3' ui/assets/data.js || { echo "missing connections sample data" >&2; exit 1; }

grep -q 'data-page="channels"' ui/channels.html || { echo "missing channels page marker" >&2; exit 1; }
grep -q 'renderChannels' ui/assets/app.js || { echo "missing renderChannels" >&2; exit 1; }
grep -q '频道管理' ui/assets/app.js || { echo "missing channels heading" >&2; exit 1; }
grep -q '频道列表' ui/assets/app.js || { echo "missing channel list section" >&2; exit 1; }
grep -q '查看频道' ui/assets/app.js || { echo "missing channel drawer trigger" >&2; exit 1; }
grep -q 'data-channel-drawer' ui/assets/app.js || { echo "missing channel drawer container" >&2; exit 1; }
grep -q 'channel-room-01' ui/assets/data.js || { echo "missing channels sample data" >&2; exit 1; }

grep -q 'data-page="topology"' ui/topology.html || { echo "missing topology page marker" >&2; exit 1; }
grep -q 'renderTopology' ui/assets/app.js || { echo "missing renderTopology" >&2; exit 1; }
grep -q '拓扑视图' ui/assets/app.js || { echo "missing topology heading" >&2; exit 1; }
grep -q '节点拓扑总览' ui/assets/app.js || { echo "missing topology overview section" >&2; exit 1; }
grep -q 'Slot 流向' ui/assets/app.js || { echo "missing topology flow section" >&2; exit 1; }
grep -q '查看拓扑节点' ui/assets/app.js || { echo "missing topology drawer trigger" >&2; exit 1; }
grep -q 'data-topology-drawer' ui/assets/app.js || { echo "missing topology drawer container" >&2; exit 1; }
grep -q 'topo-node-3' ui/assets/data.js || { echo "missing topology sample data" >&2; exit 1; }
