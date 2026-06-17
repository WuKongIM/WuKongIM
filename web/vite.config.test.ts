import type { UserConfig } from "vite"
import { describe, expect, it } from "vitest"

import { createViteConfig } from "./vite.config"

type ManagerProxyConfig = {
  target?: string
  changeOrigin?: boolean
}

function getManagerProxy(config: UserConfig) {
  const proxy = config.server?.proxy
  if (!proxy || Array.isArray(proxy)) {
    return undefined
  }

  return proxy["/manager"] as ManagerProxyConfig | undefined
}

describe("vite manager proxy", () => {
  it("proxies manager requests to the first local v2 manager server by default", () => {
    const config = createViteConfig({ mode: "development" }, {})

    expect(getManagerProxy(config)).toMatchObject({
      target: "http://127.0.0.1:5311",
      changeOrigin: true,
    })
  })

  it("allows the manager proxy target to be overridden", () => {
    const config = createViteConfig(
      { mode: "development" },
      { VITE_MANAGER_API_TARGET: "http://127.0.0.1:5399/" },
    )

    expect(getManagerProxy(config)?.target).toBe("http://127.0.0.1:5399")
  })
})
