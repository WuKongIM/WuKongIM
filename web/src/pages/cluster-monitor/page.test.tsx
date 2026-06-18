import { render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { beforeEach, expect, test } from "vitest"

import { resetLocale } from "@/i18n/locale-store"
import { I18nProvider } from "@/i18n/provider"
import { ClusterMonitorPage } from "@/pages/cluster-monitor/page"

function renderClusterMonitorPage() {
  return render(
    <I18nProvider>
      <ClusterMonitorPage />
    </I18nProvider>,
  )
}

beforeEach(() => {
  localStorage.clear()
  resetLocale()
})

test("renders the local preview cluster monitor card wall", () => {
  renderClusterMonitorPage()

  expect(screen.getByRole("heading", { name: "Live Monitor" })).toBeInTheDocument()
  expect(screen.getByText("UI Preview")).toBeInTheDocument()
  expect(screen.getByText("Cluster control plane, replication, internal network, queue, and storage watermarks.")).toBeInTheDocument()

  const cards = screen.getAllByTestId("cluster-monitor-metric-card")
  expect(cards).toHaveLength(12)
  expect(within(cards[0]).getByText("Controller Propose Rate")).toBeInTheDocument()
  expect(within(cards[2]).getByText("Slot Leader Stability")).toBeInTheDocument()
  expect(within(cards[4]).getByText("Channel ISR Health")).toBeInTheDocument()
  expect(within(cards[7]).getByText("RPC Success Rate")).toBeInTheDocument()
  expect(within(cards[11]).getByText("Incident Rate")).toBeInTheDocument()

  for (const label of ["Nodes Alive", "Slots Ready", "Apply Gap", "ISR Anomalies", "RPC Errors", "Queue Pressure", "Write P99"]) {
    expect(screen.getByText(label)).toBeInTheDocument()
  }

  for (const label of ["5m time range", "15m time range", "30m time range", "1h time range", "Pause live preview"]) {
    expect(screen.getByRole("button", { name: label })).toBeInTheDocument()
  }
})

test("updates selected time range and pause state from the toolbar", async () => {
  const user = userEvent.setup()
  renderClusterMonitorPage()

  await user.click(screen.getByRole("button", { name: "30m time range" }))
  expect(screen.getByRole("button", { name: "30m time range" })).toHaveAttribute("aria-pressed", "true")

  await user.click(screen.getByRole("button", { name: "Pause live preview" }))
  expect(screen.getByRole("button", { name: "Resume live preview" })).toBeInTheDocument()

  await user.click(screen.getByRole("button", { name: "Resume live preview" }))
  expect(screen.getByRole("button", { name: "Pause live preview" })).toBeInTheDocument()
})
