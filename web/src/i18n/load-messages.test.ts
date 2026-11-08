import { expect, test } from "vitest"

import { getLoadedMessages, loadMessages } from "@/i18n/load-messages"

test("loads each catalog once and serves it synchronously afterwards", async () => {
  const [first, concurrent] = await Promise.all([loadMessages("zh-CN"), loadMessages("zh-CN")])

  expect(first).toBe(concurrent)
  expect(first["common.refresh"]).toBe("刷新")
  expect(getLoadedMessages("zh-CN")).toBe(first)
  expect(await loadMessages("zh-CN")).toBe(first)
})
