import "@testing-library/jest-dom/vitest"

import { loadMessages } from "@/i18n/load-messages"

// Components render synchronously in tests, as the entry preloads in production.
await Promise.all([loadMessages("en"), loadMessages("zh-CN")])

class TestResizeObserver implements ResizeObserver {
  disconnect() {}
  observe() {}
  unobserve() {}
}

globalThis.ResizeObserver ??= TestResizeObserver
