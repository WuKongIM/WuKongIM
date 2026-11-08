import { createContext, useContext, useEffect, useMemo, useState, useSyncExternalStore, type PropsWithChildren } from "react"
import { IntlProvider } from "react-intl"

import { DEFAULT_LOCALE } from "@/i18n/constants"
import { getLoadedMessages, loadMessages } from "@/i18n/load-messages"
import { getLocale, setLocale, subscribeLocale } from "@/i18n/locale-store"
import type { AppLocale, AppMessages } from "@/i18n/types"

type LocaleContextValue = {
  locale: AppLocale
  setLocale: (locale: AppLocale) => void
}

type ShownCatalog = {
  locale: AppLocale
  messages: AppMessages
}

const LocaleContext = createContext<LocaleContextValue | null>(null)

export function I18nProvider({ children }: PropsWithChildren) {
  const locale = useSyncExternalStore(subscribeLocale, getLocale, getLocale)
  // The entry preloads the active catalog; a switch to a catalog not yet in
  // memory keeps the previous one visible until it loads, never raw IDs.
  const [fallback, setFallback] = useState<ShownCatalog | null>(() => {
    const messages = getLoadedMessages(locale)
    return messages ? { locale, messages } : null
  })
  const current = getLoadedMessages(locale)
  const shown = current ? { locale, messages: current } : fallback

  useEffect(() => {
    if (getLoadedMessages(locale)) {
      return
    }
    let active = true
    loadMessages(locale).then(
      (messages) => {
        if (active) {
          setFallback({ locale, messages })
        }
      },
      (error: unknown) => {
        console.error(`load ${locale} messages`, error)
      },
    )
    return () => {
      active = false
    }
  }, [locale])

  const value = useMemo<LocaleContextValue>(() => ({
    locale,
    setLocale,
  }), [locale])

  return (
    <LocaleContext.Provider value={value}>
      <IntlProvider defaultLocale={DEFAULT_LOCALE} locale={shown?.locale ?? locale} messages={shown?.messages ?? {}}>
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
