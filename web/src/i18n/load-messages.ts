import type { AppLocale, AppMessages } from "@/i18n/types"

// Each catalog is a lazy chunk: the login bundle carries neither, and a page
// downloads only its active locale (plus the other one after a switch).
const loaders: Record<AppLocale, () => Promise<AppMessages>> = {
  en: async () => (await import("@/i18n/messages/en")).enMessages,
  "zh-CN": async () => (await import("@/i18n/messages/zh-CN")).zhCNMessages,
}

const loaded = new Map<AppLocale, AppMessages>()
const pending = new Map<AppLocale, Promise<AppMessages>>()

// getLoadedMessages returns a catalog already in memory, for synchronous render.
export function getLoadedMessages(locale: AppLocale): AppMessages | undefined {
  return loaded.get(locale)
}

// loadMessages fetches one catalog at most once at a time; a failed load is
// forgotten so a later call retries it.
export function loadMessages(locale: AppLocale): Promise<AppMessages> {
  const ready = loaded.get(locale)
  if (ready) {
    return Promise.resolve(ready)
  }
  const inFlight = pending.get(locale)
  if (inFlight) {
    return inFlight
  }
  const request = loaders[locale]().then(
    (messages) => {
      loaded.set(locale, messages)
      pending.delete(locale)
      return messages
    },
    (error: unknown) => {
      pending.delete(locale)
      throw error
    },
  )
  pending.set(locale, request)
  return request
}
