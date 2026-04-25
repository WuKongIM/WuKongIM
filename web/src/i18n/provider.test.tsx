import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { beforeEach, expect, test } from "vitest"
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
