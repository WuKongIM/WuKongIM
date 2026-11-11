import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { beforeEach, expect, test, vi } from "vitest"
import { useIntl } from "react-intl"

import { LOCALE_STORAGE_KEY } from "@/i18n/constants"
import { I18nProvider } from "@/i18n/provider"
import { useLocale } from "@/i18n/use-locale"

function LocaleProbe() {
  const intl = useIntl()
  const { locale, setLocale } = useLocale()

  return (
    <div>
      <div data-testid="locale">{locale}</div>
      <div>{intl.formatMessage({ id: "common.refresh" })}</div>
      <button onClick={() => setLocale("zh-CN")} type="button">
        Switch to Chinese
      </button>
      <button onClick={() => setLocale("en")} type="button">
        Switch to English
      </button>
    </div>
  )
}

beforeEach(() => {
  localStorage.clear()
})

test("renders the persisted locale and updates text plus storage when switched", async () => {
  localStorage.setItem(LOCALE_STORAGE_KEY, "en")
  const user = userEvent.setup()

  render(
    <I18nProvider>
      <LocaleProbe />
    </I18nProvider>,
  )

  expect(screen.getByTestId("locale")).toHaveTextContent("en")
  expect(screen.getByText("Refresh")).toBeInTheDocument()

  await user.click(screen.getByRole("button", { name: "Switch to Chinese" }))

  expect(screen.getByTestId("locale")).toHaveTextContent("zh-CN")
  expect(screen.getByText("刷新")).toBeInTheDocument()
  expect(localStorage.getItem(LOCALE_STORAGE_KEY)).toBe("zh-CN")
})

test("keeps the previous locale visible until a newly selected catalog loads", async () => {
  // Fresh modules start with an empty catalog cache, as in a real page load.
  vi.resetModules()
  const { I18nProvider: FreshProvider } = await import("@/i18n/provider")
  const { loadMessages, getLoadedMessages } = await import("@/i18n/load-messages")
  const { useLocale: useFreshLocale } = await import("@/i18n/use-locale")
  localStorage.setItem(LOCALE_STORAGE_KEY, "en")
  await loadMessages("en")
  expect(getLoadedMessages("zh-CN")).toBeUndefined()

  function FreshProbe() {
    const intl = useIntl()
    const { setLocale } = useFreshLocale()
    return (
      <div>
        <div>{intl.formatMessage({ id: "common.refresh" })}</div>
        <button onClick={() => setLocale("zh-CN")} type="button">Switch to Chinese</button>
      </div>
    )
  }

  const user = userEvent.setup()
  render(
    <FreshProvider>
      <FreshProbe />
    </FreshProvider>,
  )
  expect(screen.getByText("Refresh")).toBeInTheDocument()

  await user.click(screen.getByRole("button", { name: "Switch to Chinese" }))

  expect(screen.queryByText("common.refresh")).not.toBeInTheDocument()
  expect(await screen.findByText("刷新")).toBeInTheDocument()
})
