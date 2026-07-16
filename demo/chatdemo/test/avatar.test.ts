import assert from "node:assert/strict"
import { createHash } from "node:crypto"
import test from "node:test"

import { avatarURLForUID } from "../src/services/avatar.ts"

function visibleAvatarSVG(uid: string): string {
  const url = avatarURLForUID(uid)
  return decodeURIComponent(url.slice(url.indexOf(",") + 1))
    .replace(/ aria-label="[^"]*"/, "")
}

test("the same UID produces a stable avatar", () => {
  assert.equal(avatarURLForUID("alice"), avatarURLForUID("alice"))
})

test("different UIDs produce visibly different avatars", () => {
  assert.notEqual(visibleAvatarSVG("alice"), visibleAvatarSVG("albert"))
  assert.notEqual(visibleAvatarSVG("aa80908"), visibleAvatarSVG("aa90736"))
})

test("100,000 sequential UIDs produce distinct visible avatars", () => {
  const signatures = new Set<string>()
  for (let index = 0; index < 100_000; index += 1) {
    const svg = visibleAvatarSVG(`user-${index}`)
    const signature = createHash("sha256").update(svg).digest("hex")
    assert.equal(signatures.has(signature), false, `visible collision for user-${index}`)
    signatures.add(signature)
  }
})

test("the avatar is a safe local SVG data URL", () => {
  const url = avatarURLForUID("<script>alert(1)</script>")
  assert.match(url, /^data:image\/svg\+xml;charset=UTF-8,/)

  const svg = decodeURIComponent(url.slice(url.indexOf(",") + 1))
  assert.doesNotMatch(svg, /<script>/)
  assert.match(svg, /&lt;S/)
})
