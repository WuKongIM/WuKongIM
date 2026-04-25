import { LOCALE_STORAGE_KEY } from "@/i18n/constants"
import { detectInitialLocale, normalizeLocale } from "@/i18n/detect-locale"
import type { AppLocale } from "@/i18n/types"

type LocaleListener = () => void

let currentLocale: AppLocale | null = null

const listeners = new Set<LocaleListener>()

function notifyListeners() {
  listeners.forEach((listener) => listener())
}

export function getLocale() {
  if (!currentLocale) {
    currentLocale = detectInitialLocale()
  }

  return currentLocale
}

export function setLocale(nextLocale: AppLocale) {
  currentLocale = normalizeLocale(nextLocale)

  if (typeof window !== "undefined") {
    window.localStorage.setItem(LOCALE_STORAGE_KEY, currentLocale)
  }

  notifyListeners()
}

export function resetLocale() {
  currentLocale = null

  if (typeof window !== "undefined") {
    window.localStorage.removeItem(LOCALE_STORAGE_KEY)
  }

  notifyListeners()
}

export function subscribeLocale(listener: LocaleListener) {
  listeners.add(listener)

  return () => {
    listeners.delete(listener)
  }
}
