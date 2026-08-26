import {
  createBoundedFailureLog,
  expect,
  requiredEnvironment,
  test,
  type BoundedFailureLog,
  type Page,
} from "../../../../web/e2e/playwright"

const username = requiredEnvironment("WK_MANAGER_E2E_USERNAME")
const password = requiredEnvironment("WK_MANAGER_E2E_PASSWORD")

const desktopRoutes = [
  [
    "/cluster/monitor",
    "Live Monitor",
    '[data-cluster-monitor-surface="source-state"]',
    "Prometheus monitoring is not enabled",
  ],
  ["/cluster/nodes", "Nodes", 'table[aria-label="Nodes"]'],
  ["/cluster/slots", "Slots", 'table[aria-label="Slot Inventory"]'],
  ["/business/connections", "Connections", '[data-connections-surface="inventory"]'],
  ["/system/permissions", "Permissions", '[data-testid="permissions-summary-strip"]'],
] as const

const managerErrorStateSelector = [
  '[role="status"][data-kind="error"]',
  '[role="status"][data-kind="forbidden"]',
  '[role="status"][data-kind="unavailable"]',
].join(", ")

test("operator can inspect the production route and copy matrix", async ({ page }) => {
  const failures = capturePageFailures(page)
  await page.setViewportSize({ width: 1440, height: 1000 })
  await signIn(page)

  for (const [path, heading, successSelector, successText] of desktopRoutes) {
    await openRoute(page, path, heading, successSelector, successText)
  }

  await page.getByRole("button", { name: "中文", exact: true }).click()
  await expect(page.getByRole("heading", { name: "权限管理", exact: true, level: 1 })).toBeVisible()
  await openRoute(page, "/release-readiness-missing-route", "页面不存在")

  expectNoPageFailures(failures)
})

test("operator can use Manager navigation at a mobile viewport", async ({ page }) => {
  const failures = capturePageFailures(page)
  await page.setViewportSize({ width: 390, height: 844 })
  await signIn(page)

  await page.getByRole("button", { name: "Open navigation" }).click()
  const mobileNavigation = page.getByRole("navigation", { name: "Mobile navigation" })
  await expect(mobileNavigation).toBeVisible()
  await mobileNavigation.getByRole("link", { name: "Nodes", exact: true }).click()
  await expect(page).toHaveURL(/\/cluster\/nodes$/)
  await expect(page.getByRole("heading", { name: "Nodes", exact: true, level: 1 })).toBeVisible()
  await expect(mobileNavigation).not.toBeVisible()
  await page.waitForLoadState("networkidle")
  await expect(page.locator('table[aria-label="Nodes"]')).toBeVisible()
  await expectNoManagerErrorState(page)

  expectNoPageFailures(failures)
})

async function signIn(page: Page) {
  await page.goto("/login")
  await expect(page.getByRole("heading", { name: "Sign in", exact: true, level: 1 })).toBeVisible()
  await page.getByLabel("Username", { exact: true }).fill(username)
  await page.getByLabel("Password", { exact: true }).fill(password)
  await page.getByRole("button", { name: "Sign in", exact: true }).click()
  await expect(page).toHaveURL(/\/cluster\/monitor$/)
  await expect(page.getByRole("heading", { name: "Live Monitor", exact: true, level: 1 })).toBeVisible()
  await page.waitForLoadState("networkidle")
}

async function openRoute(
  page: Page,
  path: string,
  heading: string,
  successSelector?: string,
  successText?: string,
) {
  await page.goto(path)
  await expect(page).toHaveURL(new RegExp(`${escapeRegExp(path)}$`))
  await expect(page.getByRole("heading", { name: heading, exact: true, level: 1 })).toBeVisible()
  await page.waitForLoadState("networkidle")
  if (successSelector) {
    const successState = page.locator(successSelector)
    await expect(successState).toBeVisible()
    if (successText) {
      await expect(successState).toContainText(successText)
    }
  }
  await expectNoManagerErrorState(page)
}

function capturePageFailures(page: Page) {
  const failures = createBoundedFailureLog()

  page.on("console", (message) => {
    if (message.type() === "warning" || message.type() === "error") {
      failures.add(`console.${message.type()}: ${message.text()}`)
    }
  })
  page.on("pageerror", (error) => {
    failures.add(`pageerror: ${error.message}`)
  })
  page.on("requestfailed", (request) => {
    failures.add(
      `requestfailed: ${request.method()} ${request.url()} (${request.failure()?.errorText ?? "unknown error"})`,
    )
  })
  page.on("response", (response) => {
    if (response.status() >= 400) {
      failures.add(`http ${response.status()} ${response.request().method()} ${response.url()}`)
    }
  })

  return failures
}

async function expectNoManagerErrorState(page: Page) {
  await expect(page.locator(managerErrorStateSelector)).toHaveCount(0)
}

function expectNoPageFailures(failures: BoundedFailureLog) {
  const messages = failures.messages()
  expect(messages, messages.join("\n")).toEqual([])
}

function escapeRegExp(value: string) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")
}
