import { readFileSync } from "node:fs"
import { resolve } from "node:path"

import { expect, test } from "vitest"

import { nodeConfigLabelUnknownWords } from "@/lib/node-config-i18n"

test("covers every word in the production node config schema", () => {
  const schema = readFileSync(resolve(process.cwd(), "../internal/config/schema.go"), "utf8")
  const labels = [...schema.matchAll(/EnvKey: "[^"]+".*Label: "([^"]+)"/g)].map((match) => match[1])
  const unknownWords = [...new Set(labels.flatMap(nodeConfigLabelUnknownWords))].sort()

  expect(labels.length).toBeGreaterThan(100)
  expect(unknownWords).toEqual([])
})
