import { readFileSync } from "node:fs"
import { join } from "node:path"

import { describe, expect, it } from "vitest"

const template = readFileSync(join(process.cwd(), "nginx.conf.template"), "utf8")

describe("nginx manager proxy template", () => {
  it("re-resolves the Docker service name after manager containers are recreated", () => {
    expect(template).toContain("resolver 127.0.0.11 ipv6=off")
    expect(template).toContain("set $wk_web_api_url ${WK_WEB_API_URL};")
    expect(template).toContain("proxy_pass $wk_web_api_url;")
    expect(template).not.toContain("proxy_pass ${WK_WEB_API_URL};")
  })
})
