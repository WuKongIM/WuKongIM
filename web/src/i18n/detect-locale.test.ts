import { beforeEach, describe, expect, it } from "vitest"

import { DEFAULT_LOCALE, LOCALE_STORAGE_KEY } from "@/i18n/constants"
import { detectInitialLocale, normalizeLocale } from "@/i18n/detect-locale"

function setNavigatorLanguage(value: string) {
  Object.defineProperty(window.navigator, "language", {
    value,
    configurable: true,
  })
}

describe("normalizeLocale", () => {
  it("maps zh variants to zh-CN", () => {
    expect(normalizeLocale("zh")).toBe("zh-CN")
    expect(normalizeLocale("zh-Hans-CN")).toBe("zh-CN")
  })

  it("maps en variants to en", () => {
    expect(normalizeLocale("en")).toBe("en")
    expect(normalizeLocale("en-US")).toBe("en")
  })

  it("falls back to the default locale for unsupported values", () => {
    expect(normalizeLocale("fr-FR")).toBe(DEFAULT_LOCALE)
    expect(normalizeLocale(undefined)).toBe(DEFAULT_LOCALE)
  })
})

describe("detectInitialLocale", () => {
  beforeEach(() => {
    localStorage.clear()
    setNavigatorLanguage("en-US")
  })

  it("prefers the persisted locale over the browser language", () => {
    localStorage.setItem(LOCALE_STORAGE_KEY, "zh-CN")
    setNavigatorLanguage("en-US")

    expect(detectInitialLocale()).toBe("zh-CN")
  })

  it("maps zh browser variants to zh-CN", () => {
    setNavigatorLanguage("zh-Hans-CN")

    expect(detectInitialLocale()).toBe("zh-CN")
  })

  it("falls back to the default locale for unsupported browser languages", () => {
    setNavigatorLanguage("fr-FR")

    expect(detectInitialLocale()).toBe(DEFAULT_LOCALE)
  })

  it("normalizes unsupported stored values back to the default locale", () => {
    localStorage.setItem(LOCALE_STORAGE_KEY, "fr")

    expect(detectInitialLocale()).toBe(DEFAULT_LOCALE)
  })
})
