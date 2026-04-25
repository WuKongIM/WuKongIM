function icon(name) {
  return `https://unpkg.com/lucide-static@latest/icons/${name}.svg`;
}

function pageHref(path) {
  const base = document.body.dataset.base || ".";
  return `${base}/${path}`;
}

function currentPageMeta() {
  const page = document.body.dataset.page;
  return window.ADMIN_UI_DATA.pages[page] || {
    title: "WuKongIM Admin",
    description: "",
  };
}

function queryStatusFilter() {
  const params = new URLSearchParams(window.location.search);
  return params.get("status");
}

function renderSidebar(currentKey) {
  const groups = window.ADMIN_UI_DATA.nav
    .map((group) => {
      const items = group.items
        .map((item) => {
          const activeClass = item.key === currentKey ? " is-active" : "";
          return `
            <a class="nav-item${activeClass}" href="${pageHref(item.path)}">
              <img class="nav-icon" src="${icon(item.icon)}" alt="" />
              <span>${item.text}</span>
            </a>
          `;
        })
        .join("");

      return `
        <section class="nav-group">
          <div class="nav-group-title">${group.label}</div>
          <div class="nav-list">${items}</div>
        </section>
      `;
    })
    .join("");

  return `
    <div class="brand">
      <div class="brand-mark">W</div>
      <div class="brand-copy">
        <h1>WuKongIM</h1>
        <p>Distributed Admin</p>
      </div>
    </div>
    <div class="nav-groups">${groups}</div>
    <div class="sidebar-footer">
      <a class="logout-button" href="#">
        <img class="nav-icon" src="${icon("log-out")}" alt="" />
        <span>退出登录</span>
      </a>
    </div>
  `;
}

function renderTopbar(meta) {
  return `
    <div class="topbar-meta">
      <div class="cluster-chip">
        <span class="chip-dot"></span>
        <div class="meta-copy">
          <strong>${meta.name} / ${meta.deployment}</strong>
          <span>${meta.window} · ${meta.refresh}</span>
        </div>
      </div>
    </div>
    <div class="topbar-actions">
      <div class="surface-pill search-pill">
        <img class="nav-icon" src="${icon("search")}" alt="" />
        <span>搜索节点 / Slot / 地址</span>
      </div>
      <div class="surface-pill">
        <img class="nav-icon" src="${icon("refresh-cw")}" alt="" />
        <span>刷新</span>
      </div>
      <div class="surface-pill">
        <img class="nav-icon" src="${icon("clock-3")}" alt="" />
        <span>${meta.window}</span>
      </div>
    </div>
  `;
}

function renderPlaceholder(title, copy, tags) {
  const tagHtml = (tags || [])
    .map((tag) => `<span class="tag">${tag}</span>`)
    .join("");

  return `
    <section class="page-shell">
      <header class="page-header">
        <h1>${title}</h1>
        <p>${copy}</p>
      </header>
      <section class="panel placeholder-panel">
        <div class="placeholder-card">
          <h2>${title}</h2>
          <p>${copy}</p>
          ${tagHtml ? `<div class="placeholder-tags">${tagHtml}</div>` : ""}
        </div>
      </section>
    </section>
  `;
}

function renderStubPage(meta) {
  return `
    <section class="page-shell">
      <header class="page-header">
        <h1>${meta.title}</h1>
        <p>${meta.description}</p>
      </header>
      <section class="panel skeleton-panel">
        <div class="skeleton-title"></div>
        <div class="skeleton-line"></div>
        <div class="skeleton-line short"></div>
        <div class="skeleton-line"></div>
      </section>
    </section>
  `;
}

function renderDashboard() {
  const dashboard = window.ADMIN_UI_DATA.dashboard;
  const windowPills = dashboard.windows
    .map((label, index) => {
      const activeClass = index === 0 ? " is-active" : "";
      return `<button class="window-pill${activeClass}" type="button">${label}</button>`;
    })
    .join("");

  const metrics = dashboard.metrics
    .map((item) => {
      const toneClass = item.tone ? ` ${item.tone}` : "";
      return `
        <article class="metric-card${toneClass}">
          <span class="metric-label">${item.label}</span>
          <strong class="metric-value">${item.value}</strong>
          <span class="metric-hint">${item.hint}</span>
        </article>
      `;
    })
    .join("");

  const risks = dashboard.risks
    .map((risk) => {
      return `
        <a class="risk-item" href="${pageHref(risk.path)}">
          <span class="risk-level ${risk.level}">${risk.level}</span>
          <div class="risk-copy">
            <strong>${risk.title}</strong>
            <span>${risk.detail}</span>
          </div>
          <img class="nav-icon" src="${icon("arrow-up-right")}" alt="" />
        </a>
      `;
    })
    .join("");

  const snapshotCells = dashboard.snapshot
    .map((tone) => `<div class="snapshot-cell ${tone}"></div>`)
    .join("");

  const trendBars = dashboard.trend
    .map(
      (point) => `
        <div class="trend-bar">
          <span class="trend-fill" style="height: ${point.value}%;"></span>
          <label>${point.label}</label>
        </div>
      `,
    )
    .join("");

  const hotNodes = dashboard.hotNodes
    .map(
      (node) => `
        <div class="rank-row">
          <div>
            <strong>${node.name}</strong>
            <span>${node.meta}</span>
          </div>
          <span class="inline-badge ${node.status}">${node.status}</span>
        </div>
      `,
    )
    .join("");

  return `
    <section data-view="dashboard" class="page-shell">
      <header class="page-header">
        <div>
          <h1>Dashboard</h1>
          <p>把集群健康、异常线索和节点承载情况放在一个首屏里，先判断，再下钻。</p>
        </div>
        <div class="window-pills">${windowPills}</div>
      </header>

      <section class="metric-grid">${metrics}</section>

      <section class="dashboard-grid">
        <article class="panel content-panel">
          <div class="section-head">
            <div>
              <h2>风险摘要</h2>
              <p>按优先级列出当前最值得处理的线索</p>
            </div>
            <a class="section-link" href="${pageHref("nodes.html")}">进入节点管理</a>
          </div>
          <div class="risk-list">${risks}</div>
        </article>

        <article class="panel content-panel">
          <div class="section-head">
            <div>
              <h2>集群快照</h2>
              <p>用轻量热度矩阵观察 Slot 分布与健康差异</p>
            </div>
          </div>
          <div class="snapshot-grid">${snapshotCells}</div>
          <div class="snapshot-legend">
            <span><i class="legend-dot accent"></i>高负载</span>
            <span><i class="legend-dot warning"></i>风险</span>
            <span><i class="legend-dot healthy"></i>健康</span>
          </div>
        </article>
      </section>

      <section class="dashboard-grid lower-grid">
        <article class="panel content-panel">
          <div class="section-head">
            <div>
              <h2>连接趋势</h2>
              <p>最近 30 分钟内的连接波动</p>
            </div>
          </div>
          <div class="trend-chart">${trendBars}</div>
        </article>

        <article class="panel content-panel">
          <div class="section-head">
            <div>
              <h2>异常节点排行</h2>
              <p>优先查看需要下钻的节点</p>
            </div>
          </div>
          <div class="rank-list">${hotNodes}</div>
        </article>
      </section>
    </section>
  `;
}

function roleBadge(role) {
  return role === "Leader"
    ? `<span class="role-badge leader">${role}</span>`
    : `<span class="role-badge follower">${role}</span>`;
}

function statusBadge(status) {
  return `<span class="inline-badge ${status}">${status}</span>`;
}

function renderNodeRole(role) {
  return role === "Leader" ? "领导" : "副本";
}

function renderNodeOnlineState(state) {
  return state === "offline"
    ? `<span class="node-online-badge offline">离线</span>`
    : `<span class="node-online-badge online">在线</span>`;
}

function renderNodeJoinStatus(status) {
  if (status === "joining") return "加入中";
  if (status === "left") return "未加入";
  return "已加入";
}

function renderNodeVote(hasVote) {
  return hasVote ? "有" : "无";
}

function defaultNodeFilters() {
  const status = queryStatusFilter();
  return {
    keyword: "",
    role: "",
    onlineState: ["online", "offline"].includes(status) ? status : "",
    programVersion: "",
    configVersion: "",
  };
}

function uniqueNodeValues(key) {
  return [...new Set(window.ADMIN_UI_DATA.nodes.map((node) => node[key]).filter(Boolean))];
}

function renderSelectOptions(values, selectedValue) {
  const options = ['<option value="">全部</option>'];
  values.forEach((value) => {
    const selected = value === selectedValue ? " selected" : "";
    options.push(`<option value="${value}"${selected}>${value}</option>`);
  });
  return options.join("");
}

function filterNodes(filters) {
  const keyword = filters.keyword.trim().toLowerCase();
  return window.ADMIN_UI_DATA.nodes.filter((node) => {
    const matchesKeyword = !keyword
      || [String(node.id), node.address]
        .filter(Boolean)
        .some((value) => String(value).toLowerCase().includes(keyword));
    const matchesRole = !filters.role || node.role === filters.role;
    const matchesOnlineState = !filters.onlineState || node.onlineState === filters.onlineState;
    const matchesProgramVersion = !filters.programVersion || node.programVersion === filters.programVersion;
    const matchesConfigVersion = !filters.configVersion || node.configVersion === filters.configVersion;
    return matchesKeyword && matchesRole && matchesOnlineState && matchesProgramVersion && matchesConfigVersion;
  });
}

function renderNodeTableRows(nodes) {
  if (!nodes.length) {
    return `
      <tr>
        <td class="node-table-empty" colspan="13">没有匹配的节点，请调整筛选条件后重试。</td>
      </tr>
    `;
  }

  return nodes
    .map(
      (node) => `
        <tr>
          <td><strong class="node-id">${node.id}</strong></td>
          <td>${renderNodeRole(node.role)}</td>
          <td>${node.term}</td>
          <td>${node.slotLeaderCount}/${node.slotCount}</td>
          <td>${renderNodeVote(node.hasVote)}</td>
          <td>${renderNodeOnlineState(node.onlineState)}</td>
          <td>
            <strong>${node.offlineCount}</strong>
            <div class="cell-subtle">${node.lastOfflineAt}</div>
          </td>
          <td>${node.uptime}</td>
          <td>${node.address}</td>
          <td>${node.programVersion}</td>
          <td>${node.configVersion}</td>
          <td>${renderNodeJoinStatus(node.joinStatus)}</td>
          <td><button class="table-action" type="button">日志</button></td>
        </tr>
      `,
    )
    .join("");
}

function buildNodeFilterSummary(filters, filteredNodes) {
  const tags = [];
  if (filters.keyword.trim()) tags.push(`关键词：${filters.keyword.trim()}`);
  if (filters.role) tags.push(`角色：${renderNodeRole(filters.role)}`);
  if (filters.onlineState) tags.push(`在线：${filters.onlineState === "online" ? "在线" : "离线"}`);
  if (filters.programVersion) tags.push(`程序版本：${filters.programVersion}`);
  if (filters.configVersion) tags.push(`配置版本：${filters.configVersion}`);

  return {
    count: `共 ${filteredNodes.length} / ${window.ADMIN_UI_DATA.nodes.length} 个节点`,
    active: tags.length ? `当前筛选：${tags.join(" · ")}` : "默认显示全部节点",
    subtle: !tags.length,
  };
}

function renderDrawerTable(columns, rows) {
  if (!rows || !rows.length) {
    return `<p>暂无可展示的复制进度明细。</p>`;
  }

  const headerHtml = columns.map((column) => `<th>${column.label}</th>`).join("");
  const rowHtml = rows
    .map(
      (row) => `
        <tr>
          ${columns.map((column) => `<td>${row[column.key]}</td>`).join("")}
        </tr>
      `,
    )
    .join("");

  return `
    <div class="drawer-table-wrap">
      <table class="node-table">
        <thead><tr>${headerHtml}</tr></thead>
        <tbody>${rowHtml}</tbody>
      </table>
    </div>
  `;
}

function renderGroupDrawer(group) {
  if (!group) {
    return `
      <div class="drawer-head">
        <div>
          <h2>Slot 详情</h2>
          <p>选择 Slot 后查看详情</p>
        </div>
      </div>
    `;
  }

  const raftOverview = group.detail.raftOverview
    .map(
      (item) => `
        <div class="drawer-metric">
          <span>${item.label}</span>
          <strong>${item.value}</strong>
        </div>
      `,
    )
    .join("");
  const logProgress = group.detail.logProgress
    .map(
      (item) => `
        <div class="drawer-metric">
          <span>${item.label}</span>
          <strong>${item.value}</strong>
        </div>
      `,
    )
    .join("");
  const replicaProgress = renderDrawerTable(
    [
      { key: "node", label: "副本节点" },
      { key: "role", label: "角色" },
      { key: "match", label: "Match" },
      { key: "next", label: "Next" },
      { key: "lag", label: "Lag" },
      { key: "state", label: "状态" },
    ],
    group.detail.replicaProgress,
  );
  const hotChannels = group.detail.hotChannels.map((channel) => `<span class="mini-tag">${channel}</span>`).join("");
  const events = group.detail.events.map((event) => `<li>${event}</li>`).join("");

  return `
    <div class="drawer-head">
      <div>
        <h2>${group.id}</h2>
        <p>${group.leader} · ${group.status} · term ${group.term} · epoch ${group.epoch}</p>
      </div>
      <button class="icon-button" type="button" data-close-group-drawer>关闭</button>
    </div>
    <div class="drawer-body">
      <section class="drawer-section">
        <h3>Raft 概览</h3>
        <p>${group.detail.summary}</p>
        <div class="drawer-metrics">${raftOverview}</div>
      </section>
      <section class="drawer-section">
        <h3>日志进度</h3>
        <div class="drawer-metrics">${logProgress}</div>
      </section>
      <section class="drawer-section">
        <h3>副本进度</h3>
        ${replicaProgress}
      </section>
      <section class="drawer-section">
        <h3>热点 Channel</h3>
        <div class="mini-tags">${hotChannels}</div>
      </section>
      <section class="drawer-section">
        <h3>最近事件</h3>
        <ul class="drawer-list">${events}</ul>
      </section>
    </div>
  `;
}

function renderLinkDrawer(link) {
  if (!link) {
    return `
      <div class="drawer-head">
        <div>
          <h2>链路详情</h2>
          <p>选择链路后查看详情</p>
        </div>
      </div>
    `;
  }

  const metrics = link.detail.metrics
    .map(
      (item) => `
        <div class="drawer-metric">
          <span>${item.label}</span>
          <strong>${item.value}</strong>
        </div>
      `,
    )
    .join("");
  const impactedSlots = (link.detail.impactedSlots || [])
    .map((slot) => `<span class="mini-tag">${slot}</span>`)
    .join("");
  const impactedChannels = (link.detail.impactedChannels || [])
    .map((channel) => `<span class="mini-tag">${channel}</span>`)
    .join("");
  const events = link.detail.events.map((event) => `<li>${event}</li>`).join("");

  return `
    <div class="drawer-head">
      <div>
        <h2>${link.id}</h2>
        <p>${link.source} → ${link.target} · ${link.status}</p>
      </div>
      <button class="icon-button" type="button" data-close-link-drawer>关闭</button>
    </div>
    <div class="drawer-body">
      <section class="drawer-section">
        <h3>链路摘要</h3>
        <p>${link.detail.summary}</p>
        <p>RTT ${link.rtt} · Retry ${link.retry} · Packet ${link.packet}</p>
      </section>
      <section class="drawer-section">
        <h3>关键指标</h3>
        <div class="drawer-metrics">${metrics}</div>
      </section>
      <section class="drawer-section">
        <h3>受影响 Slot</h3>
        <div class="mini-tags">${impactedSlots || "<span class=\"mini-tag\">暂无明显影响</span>"}</div>
      </section>
      <section class="drawer-section">
        <h3>受影响 Channel ISR</h3>
        <div class="mini-tags">${impactedChannels || "<span class=\"mini-tag\">暂无明显影响</span>"}</div>
      </section>
      <section class="drawer-section">
        <h3>最近事件</h3>
        <ul class="drawer-list">${events}</ul>
      </section>
    </div>
  `;
}

function renderConnectionDrawer(connection) {
  if (!connection) {
    return `
      <div class="drawer-head">
        <div>
          <h2>连接详情</h2>
          <p>选择连接后查看详情</p>
        </div>
      </div>
    `;
  }

  const metrics = connection.detail.metrics
    .map(
      (item) => `
        <div class="drawer-metric">
          <span>${item.label}</span>
          <strong>${item.value}</strong>
        </div>
      `,
    )
    .join("");
  const tags = connection.detail.tags.map((tag) => `<span class="mini-tag">${tag}</span>`).join("");
  const events = connection.detail.events.map((event) => `<li>${event}</li>`).join("");

  return `
    <div class="drawer-head">
      <div>
        <h2>${connection.id}</h2>
        <p>${connection.uid} · ${connection.device} · ${connection.status}</p>
      </div>
      <button class="icon-button" type="button" data-close-connection-drawer>关闭</button>
    </div>
    <div class="drawer-body">
      <section class="drawer-section">
        <h3>连接摘要</h3>
        <p>${connection.detail.summary}</p>
        <p>Node ${connection.node} · Client ${connection.client} · Connected ${connection.since}</p>
      </section>
      <section class="drawer-section">
        <h3>关键指标</h3>
        <div class="drawer-metrics">${metrics}</div>
      </section>
      <section class="drawer-section">
        <h3>会话标签</h3>
        <div class="mini-tags">${tags}</div>
      </section>
      <section class="drawer-section">
        <h3>最近事件</h3>
        <ul class="drawer-list">${events}</ul>
      </section>
    </div>
  `;
}

function renderChannelDrawer(channel) {
  if (!channel) {
    return `
      <div class="drawer-head">
        <div>
          <h2>频道详情</h2>
          <p>选择频道后查看详情</p>
        </div>
      </div>
    `;
  }

  const isrOverview = channel.detail.isrOverview
    .map(
      (item) => `
        <div class="drawer-metric">
          <span>${item.label}</span>
          <strong>${item.value}</strong>
        </div>
      `,
    )
    .join("");
  const logProgress = channel.detail.logProgress
    .map(
      (item) => `
        <div class="drawer-metric">
          <span>${item.label}</span>
          <strong>${item.value}</strong>
        </div>
      `,
    )
    .join("");
  const metrics = channel.detail.metrics
    .map(
      (item) => `
        <div class="drawer-metric">
          <span>${item.label}</span>
          <strong>${item.value}</strong>
        </div>
      `,
    )
    .join("");
  const tags = channel.detail.tags.map((tag) => `<span class="mini-tag">${tag}</span>`).join("");
  const replicaProgress = renderDrawerTable(
    [
      { key: "node", label: "副本节点" },
      { key: "role", label: "角色" },
      { key: "ack", label: "Ack" },
      { key: "lag", label: "HW Lag" },
      { key: "state", label: "状态" },
    ],
    channel.detail.replicaProgress,
  );
  const events = channel.detail.events.map((event) => `<li>${event}</li>`).join("");

  return `
    <div class="drawer-head">
      <div>
        <h2>${channel.id}</h2>
        <p>${channel.type} · ${channel.status} · ${channel.group} · ISR ${channel.replicasLabel}</p>
      </div>
      <button class="icon-button" type="button" data-close-channel-drawer>关闭</button>
    </div>
    <div class="drawer-body">
      <section class="drawer-section">
        <h3>ISR 概览</h3>
        <p>${channel.detail.summary}</p>
        <div class="drawer-metrics">${isrOverview}</div>
      </section>
      <section class="drawer-section">
        <h3>日志进度</h3>
        <div class="drawer-metrics">${logProgress}</div>
      </section>
      <section class="drawer-section">
        <h3>副本确认进度</h3>
        ${replicaProgress}
      </section>
      <section class="drawer-section">
        <h3>运行指标</h3>
        <div class="drawer-metrics">${metrics}</div>
      </section>
      <section class="drawer-section">
        <h3>频道标签</h3>
        <div class="mini-tags">${tags}</div>
      </section>
      <section class="drawer-section">
        <h3>复制摘要</h3>
        <p>Leader ${channel.leader} · Slot ${channel.group} · HW ${channel.hw} · LEO ${channel.leo} · Ack Lag ${channel.ackLag}</p>
      </section>
      <section class="drawer-section">
        <h3>最近事件</h3>
        <ul class="drawer-list">${events}</ul>
      </section>
    </div>
  `;
}

function renderTopologyDrawer(node) {
  if (!node) {
    return `
      <div class="drawer-head">
        <div>
          <h2>拓扑节点详情</h2>
          <p>选择节点后查看详情</p>
        </div>
      </div>
    `;
  }

  const metrics = node.detail.metrics
    .map(
      (item) => `
        <div class="drawer-metric">
          <span>${item.label}</span>
          <strong>${item.value}</strong>
        </div>
      `,
    )
    .join("");
  const slotReplication = node.detail.slotReplication
    .map(
      (item) => `
        <div class="drawer-metric">
          <span>${item.label}</span>
          <strong>${item.value}</strong>
        </div>
      `,
    )
    .join("");
  const channelReplication = node.detail.channelReplication
    .map(
      (item) => `
        <div class="drawer-metric">
          <span>${item.label}</span>
          <strong>${item.value}</strong>
        </div>
      `,
    )
    .join("");
  const links = node.detail.links.map((item) => `<span class="mini-tag">${item}</span>`).join("");
  const events = node.detail.events.map((event) => `<li>${event}</li>`).join("");

  return `
    <div class="drawer-head">
      <div>
        <h2>${node.name}</h2>
        <p>${node.role} · ${node.status} · ${node.groups} slots</p>
      </div>
      <button class="icon-button" type="button" data-close-topology-drawer>关闭</button>
    </div>
    <div class="drawer-body">
      <section class="drawer-section">
        <h3>节点概览</h3>
        <p>${node.detail.summary}</p>
        <p>Outbound ${node.outbound} · Slots ${node.groups}</p>
      </section>
      <section class="drawer-section">
        <h3>关键指标</h3>
        <div class="drawer-metrics">${metrics}</div>
      </section>
      <section class="drawer-section">
        <h3>Slot MultiRaft 热点</h3>
        <div class="drawer-metrics">${slotReplication}</div>
      </section>
      <section class="drawer-section">
        <h3>Channel ISR 热点</h3>
        <div class="drawer-metrics">${channelReplication}</div>
      </section>
      <section class="drawer-section">
        <h3>关键链路</h3>
        <div class="mini-tags">${links}</div>
      </section>
      <section class="drawer-section">
        <h3>最近事件</h3>
        <ul class="drawer-list">${events}</ul>
      </section>
    </div>
  `;
}

function renderChannels() {
  const channels = window.ADMIN_UI_DATA.channels;
  const metrics = channels.metrics
    .map(
      (item) => `
        <article class="metric-card">
          <span class="metric-label">${item.label}</span>
          <strong class="metric-value">${item.value}</strong>
          <span class="metric-hint">${item.hint}</span>
        </article>
      `,
    )
    .join("");

  const typeMix = channels.typeMix
    .map(
      (item) => `
        <div class="drawer-metric">
          <span>${item.label}</span>
          <strong>${item.value}</strong>
        </div>
      `,
    )
    .join("");

  const watchlist = channels.watchlist
    .map(
      (item) => `
        <div class="rank-row">
          <div>
            <strong>${item.id}</strong>
            <span>${item.summary}</span>
          </div>
          <span class="inline-badge ${item.status}">${item.status}</span>
        </div>
      `,
    )
    .join("");

  const rows = channels.rows
    .map(
      (channel) => `
        <tr>
          <td>
            <strong>${channel.id}</strong>
            <div class="cell-subtle">${channel.note}</div>
          </td>
          <td>${channel.type}</td>
          <td>${channel.leader}</td>
          <td>${channel.group}</td>
          <td>${channel.replicasLabel}</td>
          <td>${channel.hw}</td>
          <td>${channel.leo}</td>
          <td>${channel.ackLag}</td>
          <td>${channel.minIsr}</td>
          <td>${statusBadge(channel.status)}</td>
          <td><button class="table-link" type="button" data-open-channel="${channel.id}">查看频道</button></td>
        </tr>
      `,
    )
    .join("");

  return `
    <section data-view="channels" class="page-shell">
      <header class="page-header">
        <div>
          <h1>频道管理</h1>
          <p>从 Channel 维度查看 Leader、Slot、订阅规模和消息积压，优先定位热点与异常频道。</p>
        </div>
      </header>

      <section class="panel content-panel">
        <div class="section-head">
          <div>
            <h2>Channel ISR 概况</h2>
            <p>先看 leader channels、ISR shrink、MinISR 风险与 ack 延迟热点</p>
          </div>
        </div>
        <div class="metric-grid section-metrics">${metrics}</div>
      </section>

      <section class="dashboard-grid lower-grid">
        <article class="panel content-panel">
          <div class="section-head">
            <div>
              <h2>频道类型分布</h2>
              <p>区分 room / dm / stream / system 的当前规模</p>
            </div>
          </div>
          <div class="drawer-metrics section-metrics">${typeMix}</div>
        </article>

        <article class="panel content-panel">
          <div class="section-head">
            <div>
              <h2>ISR 风险频道</h2>
              <p>优先处理逼近 MinISR 或 lease 风险升高的频道</p>
            </div>
          </div>
          <div class="rank-list">${watchlist}</div>
        </article>
      </section>

      <section class="panel toolbar-panel">
        <div class="toolbar-row">
          <div class="surface-pill search-pill">
            <img class="nav-icon" src="${icon("search")}" alt="" />
            <span>搜索频道ID / 类型 / Slot / Leader</span>
          </div>
          <div class="toolbar-actions">
            <button class="filter-pill" type="button">类型：全部</button>
            <button class="filter-pill" type="button">状态：全部</button>
            <button class="filter-pill" type="button">排序：Backlog</button>
          </div>
        </div>
      </section>

      <section class="panel table-panel">
        <div class="section-head table-head">
          <div>
            <h2>频道列表</h2>
            <p>按 Slot、ISR、HW / LEO 与 Ack Lag 快速筛查复制风险频道</p>
          </div>
        </div>
        <table class="node-table">
          <thead>
            <tr>
              <th>频道ID</th>
              <th>类型</th>
              <th>Leader 节点</th>
              <th>所属 Slot</th>
              <th>Replicas / ISR</th>
              <th>HW</th>
              <th>LEO</th>
              <th>Ack Lag</th>
              <th>MinISR</th>
              <th>状态</th>
              <th>操作</th>
            </tr>
          </thead>
          <tbody>${rows}</tbody>
        </table>
      </section>

      <aside class="drawer" data-channel-drawer hidden></aside>
    </section>
  `;
}

function renderTopology() {
  const topology = window.ADMIN_UI_DATA.topology;
  const metrics = topology.metrics
    .map(
      (item) => `
        <article class="metric-card">
          <span class="metric-label">${item.label}</span>
          <strong class="metric-value">${item.value}</strong>
          <span class="metric-hint">${item.hint}</span>
        </article>
      `,
    )
    .join("");

  const nodes = topology.nodes
    .map(
      (node) => `
        <button
          class="topology-node ${node.status}"
          type="button"
          data-open-topology-node="${node.id}"
          style="left:${node.x};top:${node.y};"
        >
          <strong>${node.name}</strong>
          <span>${node.role}</span>
        </button>
      `,
    )
    .join("");

  const flows = topology.flows
    .map(
      (flow) => `
        <div class="flow-row">
          <div class="flow-copy">
            <strong>${flow.label}</strong>
          </div>
          <div class="flow-bar">
            <span class="flow-fill ${flow.tone}" style="width:${flow.width};"></span>
          </div>
        </div>
      `,
    )
    .join("");

  const incidents = topology.incidents
    .map(
      (item) => `
        <div class="rank-row">
          <div>
            <strong>${item.name}</strong>
            <span>${item.summary}</span>
          </div>
          <span class="inline-badge ${item.status}">${item.status}</span>
        </div>
      `,
    )
    .join("");
  const slotReplication = topology.slotReplication
    .map(
      (item) => `
        <div class="rank-row">
          <div>
            <strong>${item.name}</strong>
            <span>${item.summary}</span>
          </div>
          <span class="inline-badge ${item.status}">${item.status}</span>
        </div>
      `,
    )
    .join("");
  const channelReplication = topology.channelReplication
    .map(
      (item) => `
        <div class="rank-row">
          <div>
            <strong>${item.name}</strong>
            <span>${item.summary}</span>
          </div>
          <span class="inline-badge ${item.status}">${item.status}</span>
        </div>
      `,
    )
    .join("");
  const replicationFlows = topology.replicationFlows
    .map(
      (flow) => `
        <div class="risk-item compact">
          <span class="risk-level ${flow.tone === "degraded" ? "critical" : "warning"}">${flow.tone}</span>
          <div class="risk-copy">
            <strong>${flow.title}</strong>
            <span>${flow.summary}</span>
          </div>
        </div>
      `,
    )
    .join("");

  return `
    <section data-view="topology" class="page-shell">
      <header class="page-header">
        <div>
          <h1>拓扑视图</h1>
          <p>把节点、Slot MultiRaft 与 Channel ISR 的复制联动放在同一个视图里，帮助定位跨层热点与链路风险。</p>
        </div>
      </header>

      <section class="metric-grid">${metrics}</section>

      <section class="dashboard-grid">
        <article class="panel content-panel">
          <div class="section-head">
            <div>
              <h2>节点拓扑总览</h2>
              <p>点击节点查看角色、负载和关键链路上下文</p>
            </div>
          </div>
          <div class="topology-canvas">
            <div class="topology-link link-a"></div>
            <div class="topology-link link-b"></div>
            <div class="topology-link link-c"></div>
            ${nodes}
          </div>
        </article>

        <article class="panel content-panel">
          <div class="section-head">
            <div>
              <h2>热点清单</h2>
              <p>优先处理拓扑中的风险点和集中热点</p>
            </div>
          </div>
          <div class="rank-list">${incidents}</div>
        </article>
      </section>

      <section class="dashboard-grid lower-grid">
        <article class="panel content-panel">
          <div class="section-head">
            <div>
              <h2>Slot MultiRaft 热点</h2>
              <p>先看 commit / applied 拉开、leader transfer 与 config review 的热点槽</p>
            </div>
          </div>
          <div class="rank-list">${slotReplication}</div>
        </article>

        <article class="panel content-panel">
          <div class="section-head">
            <div>
              <h2>Channel ISR 热点</h2>
              <p>关注 ack lag、lease risk 与逼近 MinISR 的频道</p>
            </div>
          </div>
          <div class="rank-list">${channelReplication}</div>
        </article>
      </section>

      <section class="dashboard-grid lower-grid">
        <article class="panel content-panel">
          <div class="section-head">
            <div>
              <h2>复制联动清单</h2>
              <p>把 Slot MultiRaft 与 Channel ISR 的联动风险串起来看</p>
            </div>
          </div>
          <div class="risk-list">${replicationFlows}</div>
        </article>

        <article class="panel content-panel">
          <div class="section-head">
            <div>
              <h2>角色分布</h2>
              <p>当前 Leader 与 Follower 在节点间的分布情况</p>
            </div>
          </div>
          <div class="drawer-metrics section-metrics">
            ${topology.nodes
              .map(
                (node) => `
                  <div class="drawer-metric">
                    <span>${node.name}</span>
                    <strong>${node.groups}</strong>
                    <span>${node.role}</span>
                  </div>
                `,
              )
              .join("")}
          </div>
        </article>
      </section>

      <section class="panel content-panel">
        <div class="section-head">
          <div>
            <h2>Slot 流向</h2>
            <p>观察热点 Slot 在节点间的主要方向</p>
          </div>
        </div>
        <div class="flow-list">${flows}</div>
      </section>

      <aside class="drawer" data-topology-drawer hidden></aside>
      <div class="topology-actions">
        <button class="table-link" type="button" data-open-topology-node="topo-node-3">查看拓扑节点</button>
      </div>
    </section>
  `;
}

function renderConnections() {
  const connections = window.ADMIN_UI_DATA.connections;
  const metrics = connections.metrics
    .map(
      (item) => `
        <article class="metric-card">
          <span class="metric-label">${item.label}</span>
          <strong class="metric-value">${item.value}</strong>
          <span class="metric-hint">${item.hint}</span>
        </article>
      `,
    )
    .join("");

  const deviceMix = connections.deviceMix
    .map(
      (item) => `
        <div class="drawer-metric">
          <span>${item.label}</span>
          <strong>${item.value}</strong>
        </div>
      `,
    )
    .join("");

  const nodeLoad = connections.nodeLoad
    .map(
      (row) => `
        <div class="rank-row">
          <div>
            <strong>${row.node}</strong>
            <span>${row.connections} 连接</span>
          </div>
          <span class="inline-badge ${row.status}">${row.status}</span>
        </div>
      `,
    )
    .join("");

  const trendBars = connections.trend
    .map(
      (point) => `
        <div class="trend-bar">
          <span class="trend-fill" style="height: ${point.value}%;"></span>
          <label>${point.label}</label>
        </div>
      `,
    )
    .join("");

  const rows = connections.rows
    .map(
      (row) => `
        <tr>
          <td><strong>${row.id}</strong></td>
          <td>${row.uid}</td>
          <td>${row.device}</td>
          <td>${row.node}</td>
          <td>${statusBadge(row.status)}</td>
          <td>${row.latency}</td>
          <td>${row.client}</td>
          <td>${row.since}</td>
          <td><button class="table-link" type="button" data-open-connection="${row.id}">查看连接</button></td>
        </tr>
      `,
    )
    .join("");

  return `
    <section data-view="connections" class="page-shell">
      <header class="page-header">
        <div>
          <h1>在线连接</h1>
          <p>从连接、设备和节点三个维度查看当前在线会话，优先定位高风险连接。</p>
        </div>
      </header>

      <section class="panel content-panel">
        <div class="section-head">
          <div>
            <h2>活跃连接概况</h2>
            <p>先看总体规模、异常连接数量和多端在线占比</p>
          </div>
        </div>
        <div class="metric-grid section-metrics">${metrics}</div>
      </section>

      <section class="dashboard-grid lower-grid">
        <article class="panel content-panel">
          <div class="section-head">
            <div>
              <h2>设备分布</h2>
              <p>按设备类型看当前连接结构</p>
            </div>
          </div>
          <div class="drawer-metrics section-metrics">${deviceMix}</div>
        </article>
        <article class="panel content-panel">
          <div class="section-head">
            <div>
              <h2>节点承载</h2>
              <p>结合节点状态看连接负载是否偏斜</p>
            </div>
          </div>
          <div class="rank-list">${nodeLoad}</div>
        </article>
      </section>

      <section class="dashboard-grid">
        <article class="panel content-panel">
          <div class="section-head">
            <div>
              <h2>活跃会话趋势</h2>
              <p>最近 30 分钟在线连接数变化</p>
            </div>
          </div>
          <div class="trend-chart">${trendBars}</div>
        </article>

        <article class="panel content-panel">
          <div class="section-head">
            <div>
              <h2>高风险连接</h2>
              <p>优先查看 warning / degraded 会话</p>
            </div>
          </div>
          <div class="risk-list">
            ${connections.rows
              .filter((row) => row.status !== "online")
              .map(
                (row) => `
                  <div class="risk-item compact">
                    <span class="risk-level ${row.status === "degraded" ? "critical" : "warning"}">${row.status}</span>
                    <div class="risk-copy">
                      <strong>${row.id}</strong>
                      <span>${row.note} · ${row.device} · ${row.node}</span>
                    </div>
                    <button class="table-link" type="button" data-open-connection="${row.id}">查看连接</button>
                  </div>
                `,
              )
              .join("")}
          </div>
        </article>
      </section>

      <section class="panel toolbar-panel">
        <div class="toolbar-row">
          <div class="surface-pill search-pill">
            <img class="nav-icon" src="${icon("search")}" alt="" />
            <span>搜索连接ID / UID / 设备 / 节点</span>
          </div>
          <div class="toolbar-actions">
            <button class="filter-pill" type="button">状态：全部</button>
            <button class="filter-pill" type="button">设备：全部</button>
            <button class="filter-pill" type="button">排序：Latency</button>
          </div>
        </div>
      </section>

      <section class="panel table-panel">
        <div class="section-head table-head">
          <div>
            <h2>连接会话列表</h2>
            <p>按连接状态、设备和节点快速筛查在线会话</p>
          </div>
        </div>
        <table class="node-table">
          <thead>
            <tr>
              <th>连接ID</th>
              <th>UID</th>
              <th>设备</th>
              <th>节点</th>
              <th>状态</th>
              <th>RTT</th>
              <th>Client</th>
              <th>在线时长</th>
              <th>操作</th>
            </tr>
          </thead>
          <tbody>${rows}</tbody>
        </table>
      </section>

      <aside class="drawer" data-connection-drawer hidden></aside>
    </section>
  `;
}

function renderNetwork() {
  const network = window.ADMIN_UI_DATA.network;
  const metrics = network.metrics
    .map(
      (item) => `
        <article class="metric-card">
          <span class="metric-label">${item.label}</span>
          <strong class="metric-value">${item.value}</strong>
          <span class="metric-hint">${item.hint}</span>
        </article>
      `,
    )
    .join("");

  const linkRows = network.links
    .map(
      (link) => `
        <div class="risk-item compact">
          <span class="risk-level ${link.status === "degraded" ? "critical" : link.status === "warning" ? "warning" : "info"}">${link.status}</span>
          <div class="risk-copy">
            <strong>${link.id}</strong>
            <span>${link.note} · RTT ${link.rtt} · Retry ${link.retry}</span>
          </div>
          <button class="table-link" type="button" data-open-link="${link.id}">查看链路</button>
        </div>
      `,
    )
    .join("");

  const matrixCells = network.matrix
    .map((tone) => `<div class="matrix-cell ${tone || "empty"}"></div>`)
    .join("");

  const trendBars = network.trend
    .map(
      (point) => `
        <div class="trend-bar">
          <span class="trend-fill" style="height: ${point.value}%;"></span>
          <label>${point.label}</label>
        </div>
      `,
    )
    .join("");
  const replicationImpact = network.replicationImpact
    .map(
      (item) => `
        <div class="risk-item compact">
          <span class="risk-level ${item.status === "degraded" ? "critical" : "warning"}">${item.status}</span>
          <div class="risk-copy">
            <strong>${item.title}</strong>
            <span>${item.summary}</span>
          </div>
        </div>
      `,
    )
    .join("");

  const tableRows = network.links
    .map(
      (link) => `
        <tr>
          <td><strong>${link.id}</strong></td>
          <td>${statusBadge(link.status)}</td>
          <td>${link.rtt}</td>
          <td>${link.retry}</td>
          <td>${link.packet}</td>
          <td>${link.note}</td>
          <td><button class="table-link" type="button" data-open-link="${link.id}">查看链路</button></td>
        </tr>
      `,
    )
    .join("");

  return `
    <section data-view="network" class="page-shell">
      <header class="page-header">
        <div>
          <h1>网络监控</h1>
          <p>从节点到节点观察 RPC 链路健康、延迟抖动和重试热点，优先定位慢链路。</p>
        </div>
      </header>

      <section class="metric-grid">${metrics}</section>

      <section class="dashboard-grid">
        <article class="panel content-panel">
          <div class="section-head">
            <div>
              <h2>RPC 链路健康</h2>
              <p>先看最值得下钻的慢链路和抖动来源</p>
            </div>
          </div>
          <div class="risk-list">${linkRows}</div>
        </article>

        <article class="panel content-panel">
          <div class="section-head">
            <div>
              <h2>链路矩阵</h2>
              <p>横向对比 node 间的链路状态</p>
            </div>
          </div>
          <div class="network-matrix-head">
            <span></span><span>node1</span><span>node2</span><span>node3</span>
          </div>
          <div class="network-matrix-body">
            <span>node1</span><span>node2</span><span>node3</span>
            ${matrixCells}
          </div>
        </article>
      </section>

      <section class="dashboard-grid lower-grid">
        <article class="panel content-panel">
          <div class="section-head">
            <div>
              <h2>最近抖动趋势</h2>
              <p>链路 jitter 峰值在 10:15 前后出现明显抬升</p>
            </div>
          </div>
          <div class="trend-chart">${trendBars}</div>
        </article>

        <article class="panel content-panel">
          <div class="section-head">
            <div>
              <h2>链路清单</h2>
              <p>以 RTT、Retry 和 packet 行为快速排序</p>
            </div>
          </div>
          <section class="panel table-panel inset-table">
            <table class="node-table">
              <thead>
                <tr>
                  <th>链路</th>
                  <th>状态</th>
                  <th>RTT</th>
                  <th>Retry</th>
                  <th>Packet</th>
                  <th>备注</th>
                  <th>操作</th>
                </tr>
              </thead>
              <tbody>${tableRows}</tbody>
            </table>
          </section>
        </article>
      </section>

      <section class="panel content-panel">
        <div class="section-head">
          <div>
            <h2>复制影响摘要</h2>
            <p>把复制关联链路和受影响的 Slot / Channel ISR 直接串起来看</p>
          </div>
        </div>
        <div class="risk-list">${replicationImpact}</div>
      </section>

      <aside class="drawer" data-link-drawer hidden></aside>
    </section>
  `;
}

function renderGroups() {
  const groups = window.ADMIN_UI_DATA.groups;
  const metrics = groups.metrics
    .map(
      (item) => `
        <article class="metric-card">
          <span class="metric-label">${item.label}</span>
          <strong class="metric-value">${item.value}</strong>
          <span class="metric-hint">${item.hint}</span>
        </article>
      `,
    )
    .join("");

  const heatmap = groups.heatmap
    .map((tone) => `<div class="snapshot-cell ${tone}"></div>`)
    .join("");

  const rows = groups.rows
    .map(
      (group) => `
        <tr>
          <td><strong>${group.id}</strong></td>
          <td>${group.leader}</td>
          <td>${group.replicas}</td>
          <td>${group.term}</td>
          <td>${group.epoch}</td>
          <td>${group.commitIndex}</td>
          <td>${group.appliedIndex}</td>
          <td>${group.maxReplicaLag}</td>
          <td>${group.configState}</td>
          <td>${statusBadge(group.status)}</td>
          <td><button class="table-link" type="button" data-open-group="${group.id}">查看 Slot</button></td>
        </tr>
      `,
    )
    .join("");

  const incidents = groups.rows
    .filter((group) => group.status !== "online")
    .map(
      (group) => `
        <div class="rank-row">
          <div>
            <strong>${group.id}</strong>
            <span>Leader ${group.leader} · commit ${group.commitIndex} / applied ${group.appliedIndex} · ${group.configState}</span>
          </div>
          <span class="inline-badge ${group.status}">${group.status}</span>
        </div>
      `,
    )
    .join("");

  return `
    <section data-view="groups" class="page-shell">
      <header class="page-header">
        <div>
          <h1>槽管理</h1>
          <p>查看 Slot 的 Leader 分布、复制状态与当前热度，作为集群调度和排障入口。</p>
        </div>
      </header>

      <section class="metric-grid">${metrics}</section>

      <section class="dashboard-grid lower-grid">
        <article class="panel content-panel">
          <div class="section-head">
            <div>
              <h2>Raft 复制热度矩阵</h2>
              <p>快速观察 Slot 分布、风险点和 leader 集中位置</p>
            </div>
          </div>
          <div class="snapshot-grid">${heatmap}</div>
          <div class="snapshot-legend">
            <span><i class="legend-dot accent"></i>热点</span>
            <span><i class="legend-dot warning"></i>异常</span>
            <span><i class="legend-dot healthy"></i>健康</span>
          </div>
        </article>
        <article class="panel content-panel">
          <div class="section-head">
            <div>
              <h2>风险 Slot</h2>
              <p>优先看 commit / applied 拉开或配置变更中的槽</p>
            </div>
          </div>
          <div class="rank-list">${incidents}</div>
        </article>
      </section>

      <section class="panel toolbar-panel">
        <div class="toolbar-row">
          <div class="surface-pill search-pill">
            <img class="nav-icon" src="${icon("search")}" alt="" />
            <span>搜索 Slot ID / Leader / Replica</span>
          </div>
          <div class="toolbar-actions">
            <button class="filter-pill" type="button">状态：全部</button>
            <button class="filter-pill" type="button">Leader：全部</button>
            <button class="filter-pill" type="button">排序：Lag</button>
          </div>
        </div>
      </section>

      <section class="panel table-panel">
        <table class="node-table">
          <thead>
            <tr>
              <th>Slot ID</th>
              <th>Leader 节点</th>
              <th>Replica</th>
              <th>Term</th>
              <th>Epoch</th>
              <th>CommitIndex</th>
              <th>AppliedIndex</th>
              <th>Max Replica Lag</th>
              <th>Config State</th>
              <th>状态</th>
              <th>操作</th>
            </tr>
          </thead>
          <tbody>${rows}</tbody>
        </table>
      </section>

      <aside class="drawer" data-group-drawer hidden></aside>
    </section>
  `;
}

function renderNodeDrawer(node) {
  if (!node) {
    return `
      <div class="drawer-head">
        <div>
          <h2>节点详情</h2>
          <p>选择节点后查看详情</p>
        </div>
      </div>
    `;
  }

  const metrics = node.detail.metrics
    .map(
      (metric) => `
        <div class="drawer-metric">
          <span>${metric.label}</span>
          <strong>${metric.value}</strong>
        </div>
      `,
    )
    .join("");
  const groups = node.detail.groups.map((group) => `<span class="mini-tag">${group}</span>`).join("");
  const slotProfile = node.detail.slotProfile
    .map(
      (metric) => `
        <div class="drawer-metric">
          <span>${metric.label}</span>
          <strong>${metric.value}</strong>
        </div>
      `,
    )
    .join("");
  const channelProfile = node.detail.channelProfile
    .map(
      (metric) => `
        <div class="drawer-metric">
          <span>${metric.label}</span>
          <strong>${metric.value}</strong>
        </div>
      `,
    )
    .join("");
  const anomalySlots = node.detail.anomalySlots
    .map(
      (slot) => `
        <div class="rank-row">
          <div>
            <strong>${slot.id}</strong>
            <span>${slot.summary}</span>
          </div>
        </div>
      `,
    )
    .join("");
  const anomalyChannels = node.detail.anomalyChannels
    .map(
      (channel) => `
        <div class="rank-row">
          <div>
            <strong>${channel.id}</strong>
            <span>${channel.summary}</span>
          </div>
        </div>
      `,
    )
    .join("");
  const events = node.detail.events.map((event) => `<li>${event}</li>`).join("");

  return `
    <div class="drawer-head">
      <div>
        <h2>${node.name}</h2>
        <p>${node.address} · ${node.role} · ${node.status}</p>
      </div>
      <button class="icon-button" type="button" data-close-node-drawer>关闭</button>
    </div>
    <div class="drawer-body">
      <section class="drawer-section">
        <h3>基础信息</h3>
        <p>${node.detail.summary}</p>
      </section>
      <section class="drawer-section">
        <h3>近 15 分钟负载</h3>
        <div class="drawer-metrics">${metrics}</div>
      </section>
      <section class="drawer-section">
        <h3>Slot 复制画像</h3>
        <div class="drawer-metrics">${slotProfile}</div>
      </section>
      <section class="drawer-section">
        <h3>Channel ISR 画像</h3>
        <div class="drawer-metrics">${channelProfile}</div>
      </section>
      <section class="drawer-section">
        <h3>重点 Slot</h3>
        <div class="mini-tags">${groups}</div>
      </section>
      <section class="drawer-section">
        <h3>异常 Slot</h3>
        <div class="rank-list">${anomalySlots}</div>
      </section>
      <section class="drawer-section">
        <h3>异常 Channel</h3>
        <div class="rank-list">${anomalyChannels}</div>
      </section>
      <section class="drawer-section">
        <h3>最近异常事件</h3>
        <ul class="drawer-list">${events}</ul>
      </section>
      <section class="drawer-section">
        <h3>网络指标摘要</h3>
        <p>RPC Latency ${node.latency} · 连接数 ${node.connections} · Slot Max Commit Lag ${node.slotLag} · Channel ISR ${node.channelRisk}</p>
      </section>
    </div>
  `;
}

function renderNodes() {
  const filters = defaultNodeFilters();
  const rows = renderNodeTableRows(filterNodes(filters));
  const summary = buildNodeFilterSummary(filters, filterNodes(filters));
  const programVersionOptions = renderSelectOptions(uniqueNodeValues("programVersion"), filters.programVersion);
  const configVersionOptions = renderSelectOptions(uniqueNodeValues("configVersion"), filters.configVersion);

  return `
    <section data-view="nodes" class="page-shell node-page">
      <header class="page-header">
        <div>
          <h1>节点列表</h1>
          <p>前期只保留基础筛选和节点清单，先把节点信息快速看清楚。</p>
        </div>
      </header>

      <section class="panel content-panel node-filter-panel">
        <div class="section-head">
          <div>
            <h2>筛选条件</h2>
            <p>支持按节点ID、地址、角色、在线状态和版本快速过滤。</p>
          </div>
        </div>
        <form class="node-filter-form" data-node-filter-form>
          <div class="filter-grid">
            <label class="filter-field filter-field-wide">
              <span>节点ID / 地址关键词</span>
              <input
                class="filter-input"
                data-node-filter="keyword"
                name="keyword"
                placeholder="例如 1001 或 node1.wk.local"
                type="text"
                value="${filters.keyword}"
              />
            </label>
            <label class="filter-field">
              <span>角色</span>
              <select class="filter-select" data-node-filter="role" name="role">
                <option value="">全部</option>
                <option value="Leader"${filters.role === "Leader" ? " selected" : ""}>领导</option>
                <option value="Follower"${filters.role === "Follower" ? " selected" : ""}>副本</option>
              </select>
            </label>
            <label class="filter-field">
              <span>在线状态</span>
              <select class="filter-select" data-node-filter="onlineState" name="onlineState">
                <option value="">全部</option>
                <option value="online"${filters.onlineState === "online" ? " selected" : ""}>在线</option>
                <option value="offline"${filters.onlineState === "offline" ? " selected" : ""}>离线</option>
              </select>
            </label>
            <label class="filter-field">
              <span>程序版本</span>
              <select class="filter-select" data-node-filter="programVersion" name="programVersion">
                ${programVersionOptions}
              </select>
            </label>
            <label class="filter-field">
              <span>配置版本</span>
              <select class="filter-select" data-node-filter="configVersion" name="configVersion">
                ${configVersionOptions}
              </select>
            </label>
          </div>
          <div class="filter-actions">
            <button class="filter-primary-button" type="submit">查询</button>
            <button class="filter-secondary-button" data-node-reset type="button">重置</button>
            <button class="filter-secondary-button" data-node-refresh type="button">刷新</button>
          </div>
        </form>
      </section>

      <section class="panel table-panel node-list-panel">
        <div class="node-table-toolbar">
          <div>
            <h2>节点列表</h2>
            <p data-node-filter-summary>${summary.count}</p>
          </div>
          <span class="active-filter${summary.subtle ? " subtle" : ""}" data-node-active-filter>${summary.active}</span>
        </div>
        <table class="node-table">
          <thead>
            <tr>
              <th>节点ID</th>
              <th>角色</th>
              <th>任期</th>
              <th>槽领导/槽数量</th>
              <th>投票权</th>
              <th>在线</th>
              <th>离线次数</th>
              <th>运行时间</th>
              <th>地址</th>
              <th>程序版本</th>
              <th>配置版本</th>
              <th>状态</th>
              <th>操作</th>
            </tr>
          </thead>
          <tbody data-node-table-body>${rows}</tbody>
        </table>
      </section>
    </section>
  `;
}

function bindNodeFilters() {
  const form = document.querySelector("[data-node-filter-form]");
  if (!form) return;

  const summary = document.querySelector("[data-node-filter-summary]");
  const activeFilter = document.querySelector("[data-node-active-filter]");
  const tableBody = document.querySelector("[data-node-table-body]");
  const resetButton = form.querySelector("[data-node-reset]");
  const refreshButton = form.querySelector("[data-node-refresh]");
  const defaultFilters = defaultNodeFilters();

  const readFilters = () => ({
    keyword: form.elements.keyword.value,
    role: form.elements.role.value,
    onlineState: form.elements.onlineState.value,
    programVersion: form.elements.programVersion.value,
    configVersion: form.elements.configVersion.value,
  });

  const applyFilters = (filters) => {
    const filteredNodes = filterNodes(filters);
    const nextSummary = buildNodeFilterSummary(filters, filteredNodes);
    tableBody.innerHTML = renderNodeTableRows(filteredNodes);
    summary.textContent = nextSummary.count;
    activeFilter.textContent = nextSummary.active;
    activeFilter.classList.toggle("subtle", nextSummary.subtle);
  };

  const resetFilters = () => {
    form.reset();
    form.elements.keyword.value = defaultFilters.keyword;
    form.elements.role.value = defaultFilters.role;
    form.elements.onlineState.value = defaultFilters.onlineState;
    form.elements.programVersion.value = defaultFilters.programVersion;
    form.elements.configVersion.value = defaultFilters.configVersion;
    applyFilters(defaultFilters);
  };

  form.addEventListener("submit", (event) => {
    event.preventDefault();
    applyFilters(readFilters());
  });

  if (resetButton) {
    resetButton.addEventListener("click", resetFilters);
  }

  if (refreshButton) {
    refreshButton.addEventListener("click", () => {
      applyFilters(readFilters());
    });
  }
}

function bindGroupDrawer() {
  const drawer = document.querySelector("[data-group-drawer]");
  if (!drawer) return;

  const open = (id) => {
    const group = window.ADMIN_UI_DATA.groups.rows.find((item) => item.id === id);
    drawer.innerHTML = renderGroupDrawer(group);
    drawer.hidden = false;
    document.body.classList.add("drawer-open");
    const closeButton = drawer.querySelector("[data-close-group-drawer]");
    if (closeButton) {
      closeButton.addEventListener("click", close);
    }
  };

  const close = () => {
    drawer.hidden = true;
    drawer.innerHTML = "";
    document.body.classList.remove("drawer-open");
  };

  document.querySelectorAll("[data-open-group]").forEach((button) => {
    button.addEventListener("click", () => open(button.dataset.openGroup));
  });
}

function bindLinkDrawer() {
  const drawer = document.querySelector("[data-link-drawer]");
  if (!drawer) return;

  const open = (id) => {
    const link = window.ADMIN_UI_DATA.network.links.find((item) => item.id === id);
    drawer.innerHTML = renderLinkDrawer(link);
    drawer.hidden = false;
    document.body.classList.add("drawer-open");
    const closeButton = drawer.querySelector("[data-close-link-drawer]");
    if (closeButton) {
      closeButton.addEventListener("click", close);
    }
  };

  const close = () => {
    drawer.hidden = true;
    drawer.innerHTML = "";
    document.body.classList.remove("drawer-open");
  };

  document.querySelectorAll("[data-open-link]").forEach((button) => {
    button.addEventListener("click", () => open(button.dataset.openLink));
  });
}

function bindConnectionDrawer() {
  const drawer = document.querySelector("[data-connection-drawer]");
  if (!drawer) return;

  const open = (id) => {
    const connection = window.ADMIN_UI_DATA.connections.rows.find((item) => item.id === id);
    drawer.innerHTML = renderConnectionDrawer(connection);
    drawer.hidden = false;
    document.body.classList.add("drawer-open");
    const closeButton = drawer.querySelector("[data-close-connection-drawer]");
    if (closeButton) {
      closeButton.addEventListener("click", close);
    }
  };

  const close = () => {
    drawer.hidden = true;
    drawer.innerHTML = "";
    document.body.classList.remove("drawer-open");
  };

  document.querySelectorAll("[data-open-connection]").forEach((button) => {
    button.addEventListener("click", () => open(button.dataset.openConnection));
  });
}

function bindChannelDrawer() {
  const drawer = document.querySelector("[data-channel-drawer]");
  if (!drawer) return;

  const open = (id) => {
    const channel = window.ADMIN_UI_DATA.channels.rows.find((item) => item.id === id);
    drawer.innerHTML = renderChannelDrawer(channel);
    drawer.hidden = false;
    document.body.classList.add("drawer-open");
    const closeButton = drawer.querySelector("[data-close-channel-drawer]");
    if (closeButton) {
      closeButton.addEventListener("click", close);
    }
  };

  const close = () => {
    drawer.hidden = true;
    drawer.innerHTML = "";
    document.body.classList.remove("drawer-open");
  };

  document.querySelectorAll("[data-open-channel]").forEach((button) => {
    button.addEventListener("click", () => open(button.dataset.openChannel));
  });
}

function bindTopologyDrawer() {
  const drawer = document.querySelector("[data-topology-drawer]");
  if (!drawer) return;

  const open = (id) => {
    const node = window.ADMIN_UI_DATA.topology.nodes.find((item) => item.id === id);
    drawer.innerHTML = renderTopologyDrawer(node);
    drawer.hidden = false;
    document.body.classList.add("drawer-open");
    const closeButton = drawer.querySelector("[data-close-topology-drawer]");
    if (closeButton) {
      closeButton.addEventListener("click", close);
    }
  };

  const close = () => {
    drawer.hidden = true;
    drawer.innerHTML = "";
    document.body.classList.remove("drawer-open");
  };

  document.querySelectorAll("[data-open-topology-node]").forEach((button) => {
    button.addEventListener("click", () => open(button.dataset.openTopologyNode));
  });
}

function renderCurrentPage() {
  const page = document.body.dataset.page;
  const meta = currentPageMeta();

  if (page === "topology") {
    return renderTopology();
  }

  if (page === "connections") {
    return renderConnections();
  }

  if (page === "channels") {
    return renderChannels();
  }

  if (page === "dashboard") {
    return renderDashboard();
  }

  if (page === "network") {
    return renderNetwork();
  }

  if (page === "groups") {
    return renderGroups();
  }

  if (page === "nodes") {
    return renderNodes();
  }

  if (["groups", "network", "topology", "connections"].includes(page)) {
    return renderPlaceholder(meta.title, meta.description, meta.tags);
  }

  return renderStubPage(meta);
}

document.addEventListener("DOMContentLoaded", () => {
  const page = document.body.dataset.page;
  document.title = `${currentPageMeta().title} | WuKongIM Admin`;
  document.querySelector("[data-sidebar]").innerHTML = renderSidebar(page);
  document.querySelector("[data-topbar]").innerHTML = renderTopbar(window.ADMIN_UI_DATA.cluster);
  document.querySelector("[data-page-root]").innerHTML = renderCurrentPage();
  bindTopologyDrawer();
  bindChannelDrawer();
  bindConnectionDrawer();
  bindLinkDrawer();
  bindGroupDrawer();
  bindNodeFilters();
});
