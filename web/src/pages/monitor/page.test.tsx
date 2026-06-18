import { render, screen, within } from "@testing-library/react"
import { beforeEach, expect, test } from "vitest"

import { resetLocale } from "@/i18n/locale-store"
import { I18nProvider } from "@/i18n/provider"
import { MonitorPage } from "@/pages/monitor/page"

function renderMonitorPage() {
  return render(
    <I18nProvider>
      <MonitorPage />
    </I18nProvider>,
  )
}

beforeEach(() => {
  localStorage.clear()
  resetLocale()
})

test("renders the local preview business monitor card wall", () => {
  renderMonitorPage()

  expect(screen.getByRole("heading", { name: "Live Monitor" })).toBeInTheDocument()
  expect(screen.getByText("UI Preview")).toBeInTheDocument()
  expect(screen.getByText("Global business message path health trends.")).toBeInTheDocument()

  const cards = screen.getAllByTestId("monitor-metric-card")
  expect(cards).toHaveLength(12)
  expect(within(cards[0]).getByText("Send Rate")).toBeInTheDocument()
  expect(within(cards[3]).getByText("Commit Rate")).toBeInTheDocument()
  expect(within(cards[6]).getByText("Delivery Rate")).toBeInTheDocument()
  expect(within(cards[10]).getByText("Retry Queue Depth")).toBeInTheDocument()
  expect(within(cards[11]).getByText("Path Error Rate")).toBeInTheDocument()

  for (const label of ["Send", "Delivery", "Entry P99", "Delivery P99", "Errors", "Retry Depth", "Online"]) {
    expect(screen.getByText(label)).toBeInTheDocument()
  }

  for (const label of ["5m time range", "15m time range", "30m time range", "1h time range", "Pause live preview"]) {
    expect(screen.getByRole("button", { name: label })).toBeInTheDocument()
  }
})
