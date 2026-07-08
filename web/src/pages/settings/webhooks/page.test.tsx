import { render, screen, within } from "@testing-library/react"
import { beforeEach, expect, test } from "vitest"

import { resetLocale } from "@/i18n/locale-store"
import { I18nProvider } from "@/i18n/provider"
import { WebhooksPage } from "@/pages/settings/webhooks/page"

beforeEach(() => {
  localStorage.clear()
  resetLocale()
})

function renderWebhooksPage() {
  return render(
    <I18nProvider>
      <WebhooksPage />
    </I18nProvider>,
  )
}

test("renders a restrained placeholder without webhook configuration controls", () => {
  renderWebhooksPage()

  expect(screen.getByRole("heading", { name: "Webhook Configuration" })).toBeInTheDocument()
  expect(screen.getByText("Event callback URL configuration, event type filtering, and callback logs.")).toBeInTheDocument()

  const surface = screen.getByTestId("webhooks-placeholder-surface")
  expect(surface).toHaveClass("rounded-md", "border", "border-border", "bg-card")
  expect(within(surface).getByText("Coming Soon")).toBeInTheDocument()
  expect(within(surface).getByRole("status")).toHaveAttribute("data-kind", "empty")

  expect(screen.queryByRole("textbox")).not.toBeInTheDocument()
  expect(screen.queryByRole("button")).not.toBeInTheDocument()
  expect(screen.queryByRole("checkbox")).not.toBeInTheDocument()
  expect(screen.queryByRole("switch")).not.toBeInTheDocument()
})
