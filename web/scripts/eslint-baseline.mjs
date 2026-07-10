import { readFile, writeFile } from "node:fs/promises"
import path from "node:path"
import { fileURLToPath } from "node:url"

import { ESLint } from "eslint"

const scriptPath = fileURLToPath(import.meta.url)
const scriptDir = path.dirname(scriptPath)
const webRoot = path.dirname(scriptDir)
const baselinePath = path.join(webRoot, "eslint-baseline.json")
const baselineVersion = 1
const baselineFindingFields = ["file", "severity", "ruleId", "messageId", "message"]
const baselineFindingFieldSet = new Set(baselineFindingFields)

function toPosix(filePath) {
  return filePath.split(path.sep).join("/")
}

function normalizeMessage(message, filePath) {
  let semanticMessage = message
  const trailerPaths = new Set([filePath, toPosix(filePath)])
  for (const trailerPath of trailerPaths) {
    const marker = `\n\n${trailerPath}:`
    const markerIndex = semanticMessage.indexOf(marker)
    if (markerIndex < 0) continue

    const position = semanticMessage.slice(markerIndex + marker.length)
    if (/^\d+:\d+(?:\n|$)/.test(position)) {
      semanticMessage = semanticMessage.slice(0, markerIndex)
      break
    }
  }
  return semanticMessage.replace(/\bat line \d+\b/g, "at line <line>")
}

export function findingKey(finding) {
  return JSON.stringify([
    finding.file,
    finding.severity,
    finding.ruleId,
    finding.messageId,
    finding.message,
  ])
}

function compareFinding(left, right) {
  const leftKey = findingKey(left)
  const rightKey = findingKey(right)
  if (leftKey < rightKey) return -1
  if (leftKey > rightKey) return 1
  return 0
}

export function normalizeResults(results, rootDir = webRoot) {
  const findings = []
  for (const result of results) {
    for (const message of result.messages) {
      if (message.severity === 0) continue
      findings.push({
        file: toPosix(path.relative(rootDir, result.filePath)),
        severity: message.severity,
        ruleId: message.ruleId ?? null,
        messageId: message.messageId ?? null,
        message: normalizeMessage(message.message, result.filePath),
      })
    }
  }
  return findings.sort(compareFinding)
}

function multisetDifference(left, right) {
  const remaining = new Map()
  for (const finding of right) {
    const key = findingKey(finding)
    remaining.set(key, (remaining.get(key) ?? 0) + 1)
  }

  const difference = []
  for (const finding of left) {
    const key = findingKey(finding)
    const count = remaining.get(key) ?? 0
    if (count === 0) {
      difference.push(finding)
      continue
    }
    remaining.set(key, count - 1)
  }
  return difference.sort(compareFinding)
}

export function compareFindings(current, baseline) {
  return {
    added: multisetDifference(current, baseline),
    removed: multisetDifference(baseline, current),
  }
}

async function collectFindings() {
  const eslint = new ESLint({ cwd: webRoot })
  const results = await eslint.lintFiles(["."])
  const fatalMessages = results.flatMap((result) =>
    result.messages.filter((message) => message.fatal),
  )
  if (fatalMessages.length > 0) {
    throw new Error(`ESLint reported ${fatalMessages.length} fatal configuration or parse error(s)`)
  }
  return normalizeResults(results)
}

export function validateBaselineDocument(document) {
  if (
    document === null
    || typeof document !== "object"
    || Array.isArray(document)
    || document.version !== baselineVersion
    || !Array.isArray(document.findings)
  ) {
    throw new Error("unsupported ESLint baseline schema")
  }

  for (const [index, finding] of document.findings.entries()) {
    if (finding === null || typeof finding !== "object" || Array.isArray(finding)) {
      throw new Error(`invalid ESLint baseline finding at index ${index}: expected an object`)
    }
    for (const field of baselineFindingFields) {
      if (!Object.hasOwn(finding, field)) {
        throw new Error(`invalid ESLint baseline finding at index ${index}: missing field ${field}`)
      }
    }
    for (const field of Reflect.ownKeys(finding)) {
      if (typeof field !== "string" || !baselineFindingFieldSet.has(field)) {
        throw new Error(`invalid ESLint baseline finding at index ${index}: unexpected field ${String(field)}`)
      }
    }
    if (typeof finding.file !== "string" || finding.file.length === 0) {
      throw new Error(`invalid ESLint baseline finding at index ${index}: field file must be a non-empty string`)
    }
    if (!Number.isInteger(finding.severity) || ![1, 2].includes(finding.severity)) {
      throw new Error(`invalid ESLint baseline finding at index ${index}: field severity must be integer 1 or 2`)
    }
    if (finding.ruleId !== null && typeof finding.ruleId !== "string") {
      throw new Error(`invalid ESLint baseline finding at index ${index}: field ruleId must be a string or null`)
    }
    if (finding.messageId !== null && typeof finding.messageId !== "string") {
      throw new Error(`invalid ESLint baseline finding at index ${index}: field messageId must be a string or null`)
    }
    if (typeof finding.message !== "string") {
      throw new Error(`invalid ESLint baseline finding at index ${index}: field message must be a string`)
    }
  }

  return [...document.findings].sort(compareFinding)
}

async function readBaseline() {
  const parsed = JSON.parse(await readFile(baselinePath, "utf8"))
  try {
    return validateBaselineDocument(parsed)
  } catch (error) {
    const message = error instanceof Error ? error.message : error
    throw new Error(`${message} in ${baselinePath}`)
  }
}

function printDifference(label, findings) {
  if (findings.length === 0) return
  console.error(`${label} (${findings.length}):`)
  for (const finding of findings) {
    console.error(
      `  ${finding.file} severity=${finding.severity} rule=${finding.ruleId ?? "<none>"} messageId=${finding.messageId ?? "<none>"}: ${finding.message}`,
    )
  }
}

export async function main(args = process.argv.slice(2)) {
  if (args.length !== 1 || !["--check", "--update"].includes(args[0])) {
    throw new Error("usage: eslint-baseline.mjs --check|--update")
  }

  const findings = await collectFindings()
  if (args[0] === "--update") {
    const document = { version: baselineVersion, findings }
    await writeFile(baselinePath, `${JSON.stringify(document, null, 2)}\n`)
    console.log(`wrote ${findings.length} ESLint findings to ${baselinePath}`)
    return
  }

  const baseline = await readBaseline()
  const { added, removed } = compareFindings(findings, baseline)
  if (added.length === 0 && removed.length === 0) {
    console.log(`ESLint baseline matches ${findings.length} finding(s)`)
    return
  }

  printDifference("new or changed ESLint findings", added)
  printDifference("stale baseline findings; run lint:update-baseline after reviewing the fix", removed)
  process.exitCode = 1
}

if (process.argv[1] && path.resolve(process.argv[1]) === scriptPath) {
  main().catch((error) => {
    console.error(error instanceof Error ? error.message : error)
    process.exitCode = 1
  })
}
