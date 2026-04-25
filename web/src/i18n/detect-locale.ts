import { DEFAULT_LOCALE, LOCALE_STORAGE_KEY } from "@/i18n/constants"
import type { AppLocale } from "@/i18n/types"

export function normalizeLocale(input: string | null | undefined): AppLocale {
  const value = (input ?? "").toLowerCase()

  if (value.startsWith("zh")) {
    return "zh-CN"
  }

  if (value.startsWith("en")) {
    return "en"
  }

  return DEFAULT_LOCALE
}

export function detectInitialLocale(): AppLocale {
  if (typeof window === "undefined") {
    return DEFAULT_LOCALE
  }

  const storedLocale = window.localStorage.getItem(LOCALE_STORAGE_KEY)
  if (storedLocale) {
    return normalizeLocale(storedLocale)
  }

  return normalizeLocale(window.navigator.language)
}
