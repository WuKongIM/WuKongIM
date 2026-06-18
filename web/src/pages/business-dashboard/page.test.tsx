import { render, screen } from "@testing-library/react"
import { MemoryRouter } from "react-router-dom"
import { beforeEach, expect, test } from "vitest"

import { I18nProvider } from "@/i18n/provider"
import { resetLocale } from "@/i18n/locale-store"
import { BusinessDashboardPage } from "./page"

beforeEach(() => {
  localStorage.clear()
  resetLocale()
})

function renderBusinessDashboard() {
  return render(
    <I18nProvider>
      <MemoryRouter initialEntries={["/business/dashboard"]}>
        <BusinessDashboardPage />
      </MemoryRouter>
    </I18nProvider>,
  )
}

test("renders the static business command center layout", () => {
  renderBusinessDashboard()

  expect(screen.getByRole("heading", { name: "Business Dashboard" })).toBeInTheDocument()
  expect(screen.getByText("UI preview")).toBeInTheDocument()
  expect(screen.getByText("Business degraded")).toBeInTheDocument()
  expect(screen.getByText("Message flow")).toBeInTheDocument()
  expect(screen.getByText("Delivery quality")).toBeInTheDocument()
  expect(screen.getByText("Audience & routing")).toBeInTheDocument()
  expect(screen.getAllByText("Send msg/s").length).toBeGreaterThan(0)
  expect(screen.getAllByText("Deliver msg/s").length).toBeGreaterThan(0)
  expect(screen.getByText("Throughput")).toBeInTheDocument()
  expect(screen.getByText("Failure rate")).toBeInTheDocument()
  expect(screen.getByText("Operator shortcuts")).toBeInTheDocument()
})

test("renders business risks and management entry links", () => {
  renderBusinessDashboard()

  expect(screen.getByText("Retry queue pressure")).toBeInTheDocument()
  expect(screen.getByRole("link", { name: /Open Users/ })).toHaveAttribute("href", "/business/users")
  expect(screen.getByRole("link", { name: /Open Channels/ })).toHaveAttribute("href", "/business/channels")
  expect(screen.getByRole("link", { name: /^Users/ })).toHaveAttribute("href", "/business/users")
  expect(screen.getByRole("link", { name: /Live Monitor/ })).toHaveAttribute("href", "/business/monitor")
})
