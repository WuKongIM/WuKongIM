import { fileURLToPath } from "node:url"

import { defineConfig, devices } from "@playwright/test"

const managerURL = process.env.WK_MANAGER_E2E_URL?.trim().replace(/\/+$/, "")
if (!managerURL) {
  throw new Error("WK_MANAGER_E2E_URL is required")
}

export default defineConfig({
  testDir: fileURLToPath(new URL("../test/e2e/cluster/manager_browser_smoke", import.meta.url)),
  testMatch: "manager_browser_smoke.spec.ts",
  timeout: 30_000,
  expect: {
    timeout: 10_000,
  },
  fullyParallel: false,
  forbidOnly: Boolean(process.env.CI),
  retries: 0,
  workers: 1,
  reporter: [
    ["line"],
    ["html", { open: "never", outputFolder: "playwright-report" }],
  ],
  outputDir: "test-results",
  use: {
    ...devices["Desktop Chrome"],
    baseURL: managerURL,
    colorScheme: "light",
    locale: "en-US",
    screenshot: "only-on-failure",
    trace: "retain-on-failure",
    video: "retain-on-failure",
  },
  projects: [
    {
      name: "chromium",
      use: { browserName: "chromium" },
    },
  ],
})
