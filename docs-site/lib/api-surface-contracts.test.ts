import { describe, expect, test } from 'bun:test';
import {
  basicOperationsHTTP,
  benchHTTP,
  benchmarkWorkerHTTP,
  chatLifecycleWorkerHTTP,
  cloudAnalysisHTTP,
  cloudAnalysisMCPTools,
  debugHTTP,
  issueAgentCLICommands,
  managerRoutes,
  nodeTransportServices,
  operationsMCPTools,
  pluginHostRPCPaths,
  reviewAgentCLICommands,
  reviewCheckMCPTools,
  reviewCheckSelectorCommands,
  webhookEvents,
} from './api-surface-contracts';

async function source(relativePath: string) {
  return Bun.file(new URL(relativePath, import.meta.url)).text();
}

function routeKey(method: string, path: string) {
  return `${method.toUpperCase()} ${path}`;
}

function sorted(values: Iterable<string>) {
  return [...values].sort();
}

function parseGinCalls(
  goSource: string,
  prefixes: Readonly<Record<string, string>>,
): string[] {
  const routes: string[] = [];
  const pattern = /\b([A-Za-z][A-Za-z0-9_.]*)\s*\.\s*(GET|POST|PUT|PATCH|DELETE|Any)\s*\(\s*"([^"]*)"/gu;
  for (const match of goSource.matchAll(pattern)) {
    const [, receiver, rawMethod, path] = match;
    const prefix = prefixes[receiver];
    if (prefix === undefined) continue;
    routes.push(routeKey(rawMethod === 'Any' ? 'ANY' : rawMethod, `${prefix}${path}`));
  }
  return routes;
}

function parseStringArray(goSource: string, declaration: string): string[] {
  const block = goSource.match(
    new RegExp(`(?:var|const)\\s+${declaration}\\s*=\\s*\\[\\]string\\s*\\{([\\s\\S]*?)\\}`),
  )?.[1];
  if (!block) throw new Error(`Go string array ${declaration} is missing`);
  return [...block.matchAll(/"([^"]+)"/gu)].map((match) => match[1]);
}

function parseSwitchCases(goSource: string): string[] {
  return [...goSource.matchAll(/case\s+"([^"]+)"\s*:/gu)].map((match) => match[1]);
}

describe('operations HTTP surface', () => {
  test('matches all four non-product base registrations', async () => {
    const server = await source('../../internal/access/api/server.go');
    const actual = parseGinCalls(server, { 's.engine': '' }).filter((route) =>
      basicOperationsHTTP.some(({ method, path }) => route === routeKey(method, path)),
    );

    expect(basicOperationsHTTP).toHaveLength(4);
    expect(sorted(actual)).toEqual(
      sorted(basicOperationsHTTP.map(({ method, path }) => routeKey(method, path))),
    );
  });

  test('matches every conditional Debug and Bench registration', async () => {
    const [server, debug] = await Promise.all([
      source('../../internal/access/api/server.go'),
      source('../../internal/access/api/debug.go'),
    ]);
    const debugActual = [
      ...parseGinCalls(server, { 's.engine': '' }),
      ...parseGinCalls(debug, { 's.engine': '' }),
    ].filter((route) => route.includes(' /debug/'));
    const benchActual = parseGinCalls(server, { bench: '/bench/v1' });

    expect(debugHTTP).toHaveLength(9);
    expect(benchHTTP).toHaveLength(12);
    expect(sorted(debugActual)).toEqual(
      sorted(debugHTTP.map(({ method, path }) => routeKey(method, path))),
    );
    expect(sorted(benchActual)).toEqual(
      sorted(benchHTTP.map(({ method, path }) => routeKey(method, path))),
    );
    expect(server).toContain('s.engine.Use(s.debugBearerMiddleware())');
    expect(server).toMatch(/s\.benchToken == ""[\s\S]*strings\.HasPrefix\(path, "\/debug\/"\)/u);
    expect(server).toContain('if s.benchToken != "" {');
    expect(server).toContain('if s.benchTerminalFence != nil && s.benchToken != "" {');
  });
});

describe('Manager private surface', () => {
  test('matches all 108 registered method and path pairs', async () => {
    const [server, backups, restore] = await Promise.all([
      source('../../internal/access/manager/server.go'),
      source('../../internal/access/manager/backups.go'),
      source('../../internal/access/manager/restore.go'),
    ]);
    const managerGroups = {
      's.engine': '',
      permissions: '/manager',
      mcpReads: '/manager',
      mcpWrites: '/manager',
      nodes: '/manager',
      nodeWrites: '/manager',
      slots: '/manager',
      slotWrites: '/manager',
      controllerReads: '/manager',
      controllerRaftWrites: '/manager',
      diagnostics: '/manager',
      diagnosticsWrites: '/manager',
      appLogs: '/manager',
      dbInspect: '/manager',
      channels: '/manager',
      connections: '/manager',
      webhooks: '/manager',
      pluginReads: '/manager',
      pluginWrites: '/manager',
      channelWrites: '/manager',
      userReads: '/manager',
      userWrites: '/manager',
    } as const;
    const actual = [
      ...parseGinCalls(server, managerGroups),
      ...parseGinCalls(backups, { reads: '/manager/backups', writes: '/manager/backups' }),
      ...parseGinCalls(restore, { restore: '/manager/backups' }),
    ].filter((route) => route === 'ANY /mcp' || route.includes(' /manager'));

    expect(managerRoutes).toHaveLength(108);
    expect(new Set(managerRoutes.map(({ method, path }) => routeKey(method, path))).size).toBe(108);
    expect(sorted(actual)).toEqual(
      sorted(managerRoutes.map(({ method, path }) => routeKey(method, path))),
    );
  });

  test('records the real auth-off exceptions instead of promising blanket denial', async () => {
    const [server, backups, restore, mcp] = await Promise.all([
      source('../../internal/access/manager/server.go'),
      source('../../internal/access/manager/backups.go'),
      source('../../internal/access/manager/restore.go'),
      source('../../internal/access/manager/opsmcp.go'),
    ]);
    const ordinaryWrite = managerRoutes.find(
      ({ method, path }) => method === 'POST' && path === '/manager/channels',
    );
    const backupWrite = managerRoutes.find(({ path }) => path === '/manager/backups/jobs');
    const restoreWrite = managerRoutes.find(({ path }) => path.endsWith('/:archive_id/restore'));

    expect(ordinaryWrite?.authOff).toBe('unguarded');
    expect(backupWrite?.authOff).toBe('fail-closed');
    expect(restoreWrite?.authOff).toBe('fail-closed');
    expect(server).toMatch(/if s\.auth\.enabled\(\) \{[\s\S]*channelWrites\.Use/u);
    expect(backups).toContain('writes.Use(s.requireAuthenticatedBackupWrites())');
    expect(restore).toContain('restore.Use(s.requireExplicitPermission("cluster.restore", "w"))');
    expect(mcp).toMatch(/!s\.auth\.enabled\(\)[\s\S]*manager_auth_required/u);
  });
});

describe('cluster transport catalog', () => {
  test('matches every shared Go transport symbol and numeric ID', async () => {
    const ids = await source('../../pkg/cluster/net/ids.go');
    const parsed: Array<{ id: number; symbol: string }> = [];
    for (const blockMatch of ids.matchAll(/const \(([\s\S]*?)\n\)/gu)) {
      let nextID: number | undefined;
      for (const rawLine of blockMatch[1].split('\n')) {
        const line = rawLine.replace(/\/\/.*$/u, '').trim();
        const declaration = line.match(
          /^((?:RPC|Msg)[A-Za-z0-9]+)(?:\s+uint8)?(?:\s*=\s*(\d+)\s*\+\s*iota)?$/u,
        );
        if (!declaration) continue;
        if (declaration[2] !== undefined) nextID = Number(declaration[2]);
        if (nextID === undefined) throw new Error(`missing iota base for ${declaration[1]}`);
        parsed.push({ symbol: declaration[1], id: nextID });
        nextID += 1;
      }
    }

    expect(nodeTransportServices).toHaveLength(56);
    expect(parsed.sort((a, b) => a.id - b.id)).toEqual(
      nodeTransportServices
        .map(({ id, symbol }) => ({ id, symbol }))
        .sort((a, b) => a.id - b.id),
    );
  });

  test('matches the protocol registry names and preserves reserved IDs', async () => {
    const tests = await source('../../pkg/cluster/net/ids_test.go');
    const block = tests.match(/return map\[string\]uint8\{([\s\S]*?)\n\t\}/u)?.[1];
    if (!block) throw new Error('cluster transport test registry is missing');
    const actual = [...block.matchAll(/"([^"]+)"\s*:\s*([A-Za-z0-9]+)/gu)].map(
      ([, name, symbol]) => `${name}:${symbol}`,
    );

    expect(sorted(actual)).toEqual(
      sorted(nodeTransportServices.map(({ name, symbol }) => `${name}:${symbol}`)),
    );
    expect(nodeTransportServices.find(({ id }) => id === 16)?.stability).toBe('reserved');
    expect(nodeTransportServices.find(({ id }) => id === 20)?.stability).toBe('reserved');
  });

  test('keeps private Slot IDs outside default composition visible as catalog debt', async () => {
    const [store, identity, migration, plugin] = await Promise.all([
      source('../../pkg/slot/proxy/store.go'),
      source('../../pkg/slot/proxy/identity_rpc.go'),
      source('../../pkg/slot/proxy/channel_migration_rpc.go'),
      source('../../pkg/slot/proxy/plugin_binding_rpc.go'),
    ]);
    const defaultConstructor = store.match(
      /func NewChannelMetadataStore[\s\S]*?\n\}/u,
    )?.[0];
    if (!defaultConstructor) throw new Error('default Slot metadata constructor is missing');

    expect(identity).toContain('identityRPCServiceID uint8 = 4');
    expect(migration).toContain('channelMigrationRPCServiceID uint8 = 47');
    expect(plugin).toContain('pluginBindingRPCServiceID uint8 = 53');
    expect(defaultConstructor).not.toContain('identityRPCServiceID');
    expect(defaultConstructor).not.toContain('channelMigrationRPCServiceID');
    expect(defaultConstructor).not.toContain('pluginBindingRPCServiceID');
  });
});

describe('MCP and agent-only contracts', () => {
  test('matches Operations and Cloud Analysis MCP tool allowlists', async () => {
    const [ops, cloud] = await Promise.all([
      source('../../internal/access/opsmcp/handler.go'),
      source('../../internal/access/cloudanalysismcp/handler.go'),
    ]);
    const cloudTools = [...cloud.matchAll(/mcp\.AddTool\([^\n]*Name:\s*"([^"]+)"/gu)].map(
      (match) => match[1],
    );

    expect(parseStringArray(ops, 'v1ToolNames')).toEqual([...operationsMCPTools]);
    expect(cloudTools).toEqual([...cloudAnalysisMCPTools]);
  });

  test('matches Review Check MCP and strict agent CLI selectors', async () => {
    const [checkMCP, issueCLI, reviewCLI, selectorCLI] = await Promise.all([
      source('../../internal/access/reviewagentcheckmcp/server.go'),
      source('../../internal/access/issueagentcli/command.go'),
      source('../../internal/access/reviewagentcli/command.go'),
      source('../../cmd/wkreviewcheck/main.go'),
    ]);

    expect(parseStringArray(checkMCP, 'toolNames')).toEqual([...reviewCheckMCPTools]);
    expect(parseSwitchCases(issueCLI)).toEqual([...issueAgentCLICommands]);
    expect(parseSwitchCases(reviewCLI)).toEqual([...reviewAgentCLICommands]);
    expect(parseSwitchCases(selectorCLI)).toEqual([...reviewCheckSelectorCommands]);
  });

  test('matches Cloud Analysis HTTP mounts', async () => {
    const main = await source('../../cmd/wkanalysis/main.go');
    for (const { method, path } of cloudAnalysisHTTP) {
      if (path === '/mcp') {
        expect(main).toContain('mux.Handle("/mcp", gateway)');
      } else {
        expect(main).toContain(`mux.Handle${path === '/analysis/token' ? '' : 'Func'}("${method} ${path}"`);
      }
    }
  });
});

describe('other private protocol inventories', () => {
  test('matches plugin-host RPC paths and outbound webhook event names', async () => {
    const [plugin, webhook] = await Promise.all([
      source('../../internal/access/plugin/server.go'),
      source('../../internal/runtime/webhook/types.go'),
    ]);
    const routeBlock = plugin.match(/var routePaths = \[\]string\{([^\n]+)\}/u)?.[1];
    if (!routeBlock) throw new Error('plugin host route catalog is missing');
    const pluginPaths = [...routeBlock.matchAll(/"([^"]+)"/gu)].map((match) => match[1]);
    const events = [...webhook.matchAll(/Event[A-Za-z]+\s*=\s*"([^"]+)"/gu)].map(
      (match) => match[1],
    );

    expect(pluginPaths).toEqual([...pluginHostRPCPaths]);
    expect(events).toEqual([...webhookEvents]);
  });

  test('matches generic and chat-lifecycle worker controls', async () => {
    const [generic, lifecycle] = await Promise.all([
      source('../../internal/bench/worker/server.go'),
      source('../../internal/bench/chatlifecycle/worker_server.go'),
    ]);
    const genericPaths = [...generic.matchAll(/s\.mux\.HandleFunc\("(\/[^" ]+)"/gu)]
      .map((match) => match[1])
      .filter((path) => path !== '/');
    const lifecycleRoutes = [...lifecycle.matchAll(/s\.mux\.HandleFunc\("((?:GET|POST) \/[^" ]+)"/gu)]
      .map((match) => match[1]);

    expect(genericPaths).toEqual(benchmarkWorkerHTTP.map((route) => route.split(' ')[1]));
    expect(lifecycleRoutes).toEqual([...chatLifecycleWorkerHTTP]);
  });
});
