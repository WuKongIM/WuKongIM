import { describe, expect, it } from "vitest"

import { decodeManagerMessagePayload } from "@/lib/manager-message-payload"

describe("manager message payload decoding", () => {
  it("decodes printable UTF-8 including Chinese and emoji", () => {
    expect(decodeManagerMessagePayload("5L2g5aW977yM8J+Riw==")).toBe("你好，👋")
  })

  it("keeps binary payloads in base64 form", () => {
    expect(decodeManagerMessagePayload("AAE=")).toBe("AAE=")
    expect(decodeManagerMessagePayload("/w==")).toBe("/w==")
  })

  it("keeps malformed base64 unchanged", () => {
    expect(decodeManagerMessagePayload("not base64!")).toBe("not base64!")
  })
})
