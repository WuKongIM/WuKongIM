import { render, screen } from "@testing-library/react"
import { expect, test, vi } from "vitest"

import { I18nProvider } from "@/i18n/provider"
import { WorkqueuesPage } from "@/pages/workqueues/page"

const getRuntimeWorkqueuesMock = vi.fn()

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return {
    ...actual,
    getRuntimeWorkqueues: (...args: unknown[]) => getRuntimeWorkqueuesMock(...args),
  }
})

test("renders the workqueue monitor heading", () => {
  render(
    <I18nProvider>
      <WorkqueuesPage />
    </I18nProvider>,
  )

  expect(screen.getByRole("heading", { name: "Workqueue Monitor" })).toBeInTheDocument()
  expect(getRuntimeWorkqueuesMock).not.toHaveBeenCalled()
})
