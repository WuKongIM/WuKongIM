import { createContext, useContext, useMemo, useSyncExternalStore, type PropsWithChildren } from "react"
import { IntlProvider } from "react-intl"

import { DEFAULT_LOCALE } from "@/i18n/constants"
import { enMessages } from "@/i18n/messages/en"
import { zhCNMessages } from "@/i18n/messages/zh-CN"
import { getLocale, setLocale, subscribeLocale } from "@/i18n/locale-store"
import type { AppLocale } from "@/i18n/types"

type LocaleContextValue = {
  locale: AppLocale
  setLocale: (locale: AppLocale) => void
}

const messages = {
  en: enMessages,
  "zh-CN": zhCNMessages,
} as const

const LocaleContext = createContext<LocaleContextValue | null>(null)

export function I18nProvider({ children }: PropsWithChildren) {
  const locale = useSyncExternalStore(subscribeLocale, getLocale, getLocale)

  const value = useMemo<LocaleContextValue>(() => ({
    locale,
    setLocale,
  }), [locale])

  return (
    <LocaleContext.Provider value={value}>
      <IntlProvider defaultLocale={DEFAULT_LOCALE} locale={locale} messages={messages[locale]}>
        {children}
      </IntlProvider>
    </LocaleContext.Provider>
  )
}

export function useLocaleContext() {
  const context = useContext(LocaleContext)

  if (!context) {
    throw new Error("useLocaleContext must be used within I18nProvider")
  }

  return context
}
