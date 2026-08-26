// @vitest-environment node

import { afterAll, beforeAll, expect, test } from "vitest"
import { mkdtemp, readFile, rm } from "node:fs/promises"
import { tmpdir } from "node:os"
import path from "node:path"
import { fileURLToPath } from "node:url"
import { gzipSync } from "node:zlib"
import { build } from "vite"

const webRoot = fileURLToPath(new URL("..", import.meta.url))
const initialGzipBudgetBytes = 220_000
const expectedLazyRoutes = [
  "src/app/protected-app-shell.tsx",
  "src/pages/cluster-monitor/page.tsx",
  "src/pages/nodes/page.tsx",
  "src/pages/node-config/page.tsx",
  "src/pages/slots/page.tsx",
  "src/pages/cluster/channels/page.tsx",
  "src/pages/plugins/page.tsx",
  "src/pages/tasks/page.tsx",
  "src/pages/workqueues/page.tsx",
  "src/pages/app-logs/page.tsx",
  "src/pages/cluster/diagnostics/page.tsx",
  "src/pages/backups/page.tsx",
  "src/pages/users/page.tsx",
  "src/pages/channels-biz/page.tsx",
  "src/pages/messages/page.tsx",
  "src/pages/conversations/page.tsx",
  "src/pages/system-users/page.tsx",
  "src/pages/connections/page.tsx",
  "src/pages/settings/permissions/page.tsx",
  "src/pages/settings/mcp/page.tsx",
  "src/pages/db-inspect/page.tsx",
  "src/pages/settings/webhooks/page.tsx",
]

let outputDir
let manifest

function collectStaticFiles(manifestEntries, entryId, files = new Set()) {
  const entry = manifestEntries[entryId]
  if (!entry || files.has(entry.file)) {
    return files
  }

  files.add(entry.file)
  for (const importedId of entry.imports ?? []) {
    collectStaticFiles(manifestEntries, importedId, files)
  }
  return files
}

beforeAll(async () => {
  outputDir = await mkdtemp(path.join(tmpdir(), "wukongim-login-bundle-"))
  const originalNodeEnv = process.env.NODE_ENV
  process.env.NODE_ENV = "production"
  try {
    await build({
      root: webRoot,
      configFile: path.join(webRoot, "vite.config.ts"),
      logLevel: "silent",
      mode: "production",
      build: {
        emptyOutDir: true,
        manifest: true,
        outDir: outputDir,
      },
    })
  } finally {
    if (originalNodeEnv === undefined) {
      delete process.env.NODE_ENV
    } else {
      process.env.NODE_ENV = originalNodeEnv
    }
  }

  manifest = JSON.parse(
    await readFile(path.join(outputDir, ".vite", "manifest.json"), "utf8"),
  )
}, 30_000)

afterAll(async () => {
  if (outputDir) {
    await rm(outputDir, { force: true, recursive: true })
  }
})

test("keeps authenticated route modules out of the login entry bundle", async () => {
  const entry = Object.values(manifest).find((candidate) => candidate.isEntry)
  expect(entry).toBeDefined()
  expect(entry.dynamicImports).toEqual(expect.arrayContaining(expectedLazyRoutes))

  const entryId = Object.entries(manifest).find(([, candidate]) => candidate.isEntry)?.[0]
  const initialFiles = collectStaticFiles(manifest, entryId)
  let initialGzipBytes = 0
  for (const file of initialFiles) {
    const contents = await readFile(path.join(outputDir, file))
    initialGzipBytes += gzipSync(contents).byteLength
  }

  expect(initialGzipBytes).toBeLessThanOrEqual(initialGzipBudgetBytes)
})
