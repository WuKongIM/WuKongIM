import type { ReactNode } from "react"
import { render, screen, within } from "@testing-library/react"
import { beforeEach } from "vitest"

import { ActionFormDialog } from "@/components/manager/action-form-dialog"
import { ConfirmDialog } from "@/components/manager/confirm-dialog"
import { DetailSheet } from "@/components/manager/detail-sheet"
import { ResourceState } from "@/components/manager/resource-state"
import { StatusBadge } from "@/components/manager/status-badge"
import { I18nProvider } from "@/i18n/provider"
import { resetLocale } from "@/i18n/locale-store"
import { MetricPlaceholder } from "@/components/shell/metric-placeholder"
import { PageHeader } from "@/components/shell/page-header"
import { PlaceholderBlock } from "@/components/shell/placeholder-block"

beforeEach(() => {
  localStorage.clear()
  resetLocale()
})

function renderWithI18n(node: ReactNode) {
  return render(<I18nProvider>{node}</I18nProvider>)
}

test("page header renders a flat tool row", () => {
  render(
    <PageHeader
      title="Dashboard"
      description="Runtime summary."
      actions={<button type="button">Refresh</button>}
    >
      <div>Scope: single-node cluster</div>
    </PageHeader>,
  )

  expect(screen.getByRole("heading", { name: "Dashboard" })).toBeInTheDocument()
  expect(screen.getByText("Scope: single-node cluster")).toBeInTheDocument()
  expect(screen.getByRole("button", { name: "Refresh" })).toBeInTheDocument()
})

test("metric placeholder is a compact data cell", () => {
  render(<MetricPlaceholder label="Nodes" hint="Registered node count." />)

  expect(screen.getByText("Nodes")).toBeInTheDocument()
  expect(screen.getByText("--")).toBeInTheDocument()
  expect(screen.getByText("Registered node count.")).toBeInTheDocument()
  expect(screen.queryByText("Ready")).not.toBeInTheDocument()
})

test("table placeholder exposes structural table rows", () => {
  render(<PlaceholderBlock kind="table" />)

  const table = screen.getByTestId("placeholder-table")

  expect(within(table).getAllByTestId("placeholder-table-row")).toHaveLength(3)
})

test("resource state renders forbidden copy", () => {
  renderWithI18n(<ResourceState kind="forbidden" title="Nodes" />)

  expect(screen.getByText("Nodes")).toBeInTheDocument()
  expect(screen.getByText(/permission/i)).toBeInTheDocument()
})

test("resource state uses translated retry copy", () => {
  localStorage.setItem("wukongim_manager_locale", "zh-CN")

  renderWithI18n(<ResourceState kind="error" title="网络" onRetry={() => undefined} />)

  expect(screen.getByRole("button", { name: "重试" })).toBeInTheDocument()
})

test("status badge distinguishes runtime states", () => {
  render(
    <div>
      <StatusBadge value="alive" />
      <StatusBadge value="quorum_lost" />
      <StatusBadge value="failed" />
    </div>,
  )

  expect(screen.getByText("alive")).toHaveAttribute("data-variant", "success")
  expect(screen.getByText("quorum lost")).toHaveAttribute("data-variant", "warning")
  expect(screen.getByText("failed")).toHaveAttribute("data-variant", "danger")
})

test("detail sheet shows heading copy and children", () => {
  renderWithI18n(
    <DetailSheet open title="Node 1" description="Node detail panel" onOpenChange={() => undefined}>
      <div>Hosted IDs</div>
    </DetailSheet>,
  )

  expect(screen.getByRole("heading", { name: "Node 1" })).toBeInTheDocument()
  expect(screen.getByText("Node detail panel")).toBeInTheDocument()
  expect(screen.getByText("Hosted IDs")).toBeInTheDocument()
})

test("confirm dialog disables submit while pending", () => {
  renderWithI18n(
    <ConfirmDialog
      open
      title="Drain node"
      description="Move traffic off node 1"
      confirmLabel="Confirm"
      pending
      onConfirm={() => undefined}
      onOpenChange={() => undefined}
    />,
  )

  expect(screen.getByRole("button", { name: "Confirm" })).toBeDisabled()
})

test("action form dialog renders fields and error copy", () => {
  renderWithI18n(
    <ActionFormDialog
      open
      title="Transfer leader"
      description="Select the new leader"
      submitLabel="Transfer"
      error="target node is required"
      onSubmit={(event) => event.preventDefault()}
      onOpenChange={() => undefined}
    >
      <label htmlFor="target-node">Target node</label>
      <input id="target-node" name="target-node" />
    </ActionFormDialog>,
  )

  expect(screen.getByLabelText("Target node")).toBeInTheDocument()
  expect(screen.getByText("target node is required")).toBeInTheDocument()
  expect(screen.getByRole("button", { name: "Transfer" })).toBeInTheDocument()
})
