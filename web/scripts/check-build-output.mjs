import { readdir, stat } from "node:fs/promises"
import path from "node:path"
import { fileURLToPath } from "node:url"

const maxJavaScriptChunkBytes = 500_000
const maxJavaScriptChunks = 60
const maxTotalJavaScriptBytes = 2_000_000
const scriptDirectory = path.dirname(fileURLToPath(import.meta.url))
const assetsDirectory = path.resolve(scriptDirectory, "../../internal/access/manager/webui/dist/assets")
const entries = await readdir(assetsDirectory)
const javaScriptChunks = entries.filter((entry) => entry.endsWith(".js"))

if (javaScriptChunks.length < 2) {
  throw new Error(`expected route-split production output, found ${javaScriptChunks.length} JavaScript chunk`)
}
if (javaScriptChunks.length > maxJavaScriptChunks) {
  throw new Error(
    `production output has ${javaScriptChunks.length} JavaScript chunks; budget is ${maxJavaScriptChunks}`,
  )
}

const oversizedChunks = []
let totalJavaScriptBytes = 0
for (const chunk of javaScriptChunks) {
  const chunkPath = path.join(assetsDirectory, chunk)
  const metadata = await stat(chunkPath)
  totalJavaScriptBytes += metadata.size
  if (metadata.size > maxJavaScriptChunkBytes) {
    oversizedChunks.push(`${chunk} (${metadata.size} bytes)`)
  }
}

if (totalJavaScriptBytes > maxTotalJavaScriptBytes) {
  throw new Error(
    `production JavaScript output is ${totalJavaScriptBytes} bytes; total budget is ${maxTotalJavaScriptBytes}`,
  )
}

if (oversizedChunks.length > 0) {
  throw new Error(
    `JavaScript chunks exceed the ${maxJavaScriptChunkBytes}-byte production budget:\n${oversizedChunks.join("\n")}`,
  )
}

console.log(
  `Production bundle budget passed: ${javaScriptChunks.length} JavaScript chunks, ${totalJavaScriptBytes} total bytes, each <= ${maxJavaScriptChunkBytes} bytes.`,
)
