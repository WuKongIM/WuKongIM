import type { AppLocale } from "@/i18n/types"

export const SUPPORTED_LOCALES = ["en", "zh-CN"] as const satisfies readonly AppLocale[]

export const DEFAULT_LOCALE: AppLocale = "en"

export const LOCALE_STORAGE_KEY = "wukongim_manager_locale"

export const LOCALE_LABELS: Record<AppLocale, string> = {
  en: "EN",
  "zh-CN": "中文",
}
