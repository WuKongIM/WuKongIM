import { expect, test, type Page } from "../../../../web/e2e/playwright"

const username = requiredEnvironment("WK_MANAGER_E2E_USERNAME")
const password = requiredEnvironment("WK_MANAGER_E2E_PASSWORD")

const desktopRoutes = [
  ["/cluster/monitor", "Live Monitor"],
  ["/cluster/nodes", "Nodes"],
  ["/cluster/slots", "Slots"],
  ["/business/connections", "Connections"],
  ["/system/permissions", "Permissions"],
] as const

test("operator can inspect the production route and copy matrix", async ({ page }) => {
  const failures = capturePageFailures(page)
  await page.setViewportSize({ width: 1440, height: 1000 })
  await signIn(page)

  for (const [path, heading] of desktopRoutes) {
    await openRoute(page, path, heading)
  }

  await page.getByRole("button", { name: "中文", exact: true }).click()
  await expect(page.getByRole("heading", { name: "权限管理", exact: true, level: 1 })).toBeVisible()
  await openRoute(page, "/release-readiness-missing-route", "页面不存在")

  expect(failures, failures.join("\n")).toEqual([])
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

  expect(failures, failures.join("\n")).toEqual([])
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

async function openRoute(page: Page, path: string, heading: string) {
  await page.goto(path)
  await expect(page).toHaveURL(new RegExp(`${escapeRegExp(path)}$`))
  await expect(page.getByRole("heading", { name: heading, exact: true, level: 1 })).toBeVisible()
  await page.waitForLoadState("networkidle")
}

function capturePageFailures(page: Page) {
  const failures: string[] = []

  page.on("console", (message) => {
    if (message.type() === "warning" || message.type() === "error") {
      failures.push(`console.${message.type()}: ${message.text()}`)
    }
  })
  page.on("pageerror", (error) => {
    failures.push(`pageerror: ${error.message}`)
  })
  page.on("response", (response) => {
    if (response.status() >= 400) {
      failures.push(`http ${response.status()} ${response.request().method()} ${response.url()}`)
    }
  })

  return failures
}

function requiredEnvironment(name: string) {
  const value = process.env[name]?.trim()
  if (!value) {
    throw new Error(`${name} is required`)
  }
  return value
}

function escapeRegExp(value: string) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")
}
