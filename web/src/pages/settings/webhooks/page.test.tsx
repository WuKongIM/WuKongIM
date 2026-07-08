import { StrictMode } from "react"
import { act, render, screen, waitFor, within } from "@testing-library/react"
import { beforeEach, expect, test, vi } from "vitest"

import { resetLocale } from "@/i18n/locale-store"
import { I18nProvider } from "@/i18n/provider"
import { ManagerApiError } from "@/lib/manager-api"
import type { ManagerWebhookConfigResponse } from "@/lib/manager-api.types"
import { WebhooksPage } from "@/pages/settings/webhooks/page"

const getWebhookConfigMock = vi.fn()

vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return {
    ...actual,
    getWebhookConfig: (...args: unknown[]) => getWebhookConfigMock(...args),
  }
})

beforeEach(() => {
  localStorage.clear()
  resetLocale()
  getWebhookConfigMock.mockReset()
})

function renderWebhooksPage() {
  return render(
    <I18nProvider>
      <WebhooksPage />
    </I18nProvider>,
  )
}

function webhookConfig(overrides: Partial<ManagerWebhookConfigResponse> = {}): ManagerWebhookConfigResponse {
  return {
    enabled: true,
    http_addr: "https://hooks.example.test/default",
    focus_events: ["msg.notify"],
    supported_events: ["msg.notify", "msg.offline"],
    queue_size: 2048,
    workers: 4,
    msg_notify_batch_max_items: 128,
    msg_notify_batch_max_wait: "500ms",
    online_status_batch_max_items: 64,
    online_status_batch_max_wait: "1s",
    offline_uid_batch_size: 200,
    request_timeout: "3s",
    retry_max_attempts: 5,
    source: "wukongim.conf",
    requires_restart: false,
    ...overrides,
  }
}

function deferredConfig() {
  let resolve!: (value: ManagerWebhookConfigResponse) => void
  const promise = new Promise<ManagerWebhookConfigResponse>((promiseResolve) => {
    resolve = promiseResolve
  })
  return { promise, resolve }
}

test("renders enabled webhook config with endpoint and selected focus events", async () => {
  getWebhookConfigMock.mockResolvedValueOnce({
    enabled: true,
    http_addr: "https://hooks.example.test/wukong",
    focus_events: ["msg.notify", "user.onlinestatus"],
    supported_events: ["msg.notify", "msg.offline", "user.onlinestatus"],
    queue_size: 2048,
    workers: 4,
    msg_notify_batch_max_items: 128,
    msg_notify_batch_max_wait: "500ms",
    online_status_batch_max_items: 64,
    online_status_batch_max_wait: "1s",
    offline_uid_batch_size: 200,
    request_timeout: "3s",
    retry_max_attempts: 5,
    source: "wukongim.conf",
    requires_restart: true,
  })

  renderWebhooksPage()

  expect(await screen.findByRole("heading", { name: "Webhook Configuration" })).toBeInTheDocument()
  expect(screen.getByText("Webhook Status")).toBeInTheDocument()
  expect(screen.getByText("Enabled")).toBeInTheDocument()
  expect(screen.getByText("https://hooks.example.test/wukong")).toBeInTheDocument()
  expect(screen.getAllByText("msg.notify").length).toBeGreaterThan(0)
  expect(screen.getAllByText("user.onlinestatus").length).toBeGreaterThan(0)
  expect(screen.getByText("msg.offline")).toBeInTheDocument()
  expect(screen.getByText("wukongim.conf")).toBeInTheDocument()
  expect(screen.getAllByText("Requires restart").length).toBeGreaterThan(0)

  const deliveryTable = screen.getByRole("table", { name: "Delivery and retry settings" })
  expect(within(deliveryTable).getByText("Queue size")).toBeInTheDocument()
  expect(within(deliveryTable).getByText("2,048")).toBeInTheDocument()
  expect(within(deliveryTable).getByText("Workers")).toBeInTheDocument()
  expect(within(deliveryTable).getByText("4")).toBeInTheDocument()
  expect(within(deliveryTable).getByText("Message batch wait")).toBeInTheDocument()
  expect(within(deliveryTable).getByText("500ms")).toBeInTheDocument()
  expect(within(deliveryTable).getByText("Online status batch wait")).toBeInTheDocument()
  expect(within(deliveryTable).getByText("1s")).toBeInTheDocument()
  expect(within(deliveryTable).getByText("Retry attempts")).toBeInTheDocument()
  expect(within(deliveryTable).getByText("5")).toBeInTheDocument()
})

test("renders disabled webhook config with empty endpoint and all events", async () => {
  getWebhookConfigMock.mockResolvedValueOnce({
    enabled: false,
    http_addr: "",
    focus_events: [],
    supported_events: ["msg.notify", "msg.offline"],
    queue_size: 1024,
    workers: 2,
    msg_notify_batch_max_items: 100,
    msg_notify_batch_max_wait: "1s",
    online_status_batch_max_items: 80,
    online_status_batch_max_wait: "2s",
    offline_uid_batch_size: 150,
    request_timeout: "5s",
    retry_max_attempts: 3,
    source: "environment",
    requires_restart: false,
  })

  renderWebhooksPage()

  expect(await screen.findByText("Webhook Status")).toBeInTheDocument()
  expect(screen.getByText("Disabled")).toBeInTheDocument()
  expect(screen.getByText("Off")).toBeInTheDocument()
  expect(screen.getAllByText("All supported events").length).toBeGreaterThan(0)
  expect(screen.getByText("No restart required")).toBeInTheDocument()
  expect(screen.getByText("environment")).toBeInTheDocument()
})

test("maps forbidden and unavailable errors to resource states", async () => {
  getWebhookConfigMock.mockRejectedValueOnce(new ManagerApiError(403, "forbidden", "forbidden"))
  const { unmount } = renderWebhooksPage()

  expect(await screen.findByText("You do not have permission to view this manager resource.")).toBeInTheDocument()
  expect(screen.getByRole("button", { name: "Retry" })).toBeInTheDocument()
  unmount()

  getWebhookConfigMock.mockRejectedValueOnce(new ManagerApiError(503, "service_unavailable", "unavailable"))
  renderWebhooksPage()

  expect(await screen.findByText("The manager service is currently unavailable.")).toBeInTheDocument()
  expect(screen.getByRole("button", { name: "Retry" })).toBeInTheDocument()
})

test("renders successful config without editable controls", async () => {
  getWebhookConfigMock.mockResolvedValueOnce({
    enabled: true,
    http_addr: "https://hooks.example.test/wukong",
    focus_events: ["msg.notify"],
    supported_events: ["msg.notify"],
    queue_size: 2048,
    workers: 4,
    msg_notify_batch_max_items: 128,
    msg_notify_batch_max_wait: "500ms",
    online_status_batch_max_items: 64,
    online_status_batch_max_wait: "1s",
    offline_uid_batch_size: 200,
    request_timeout: "3s",
    retry_max_attempts: 5,
    source: "wukongim.conf",
    requires_restart: false,
  })

  renderWebhooksPage()

  expect(await screen.findByText("Webhook Status")).toBeInTheDocument()

  expect(screen.queryByRole("textbox")).not.toBeInTheDocument()
  expect(screen.queryByRole("button")).not.toBeInTheDocument()
  expect(screen.queryByRole("checkbox")).not.toBeInTheDocument()
  expect(screen.queryByRole("switch")).not.toBeInTheDocument()
  expect(screen.queryByTestId("webhooks-placeholder-surface")).not.toBeInTheDocument()
})

test("keeps the newest webhook config when overlapping startup requests resolve out of order", async () => {
  const firstRequest = deferredConfig()
  const secondRequest = deferredConfig()
  getWebhookConfigMock
    .mockImplementationOnce(() => firstRequest.promise)
    .mockImplementationOnce(() => secondRequest.promise)

  render(
    <StrictMode>
      <I18nProvider>
        <WebhooksPage />
      </I18nProvider>
    </StrictMode>,
  )

  await waitFor(() => expect(getWebhookConfigMock).toHaveBeenCalledTimes(2))

  await act(async () => {
    secondRequest.resolve(webhookConfig({ http_addr: "https://hooks.example.test/newer" }))
  })

  expect(await screen.findByText("https://hooks.example.test/newer")).toBeInTheDocument()

  await act(async () => {
    firstRequest.resolve(webhookConfig({ http_addr: "https://hooks.example.test/stale" }))
  })

  expect(screen.getByText("https://hooks.example.test/newer")).toBeInTheDocument()
  expect(screen.queryByText("https://hooks.example.test/stale")).not.toBeInTheDocument()
})
