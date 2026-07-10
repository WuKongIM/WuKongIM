import path from "node:path"

import { describe, expect, it } from "vitest"

import * as eslintBaseline from "./eslint-baseline.mjs"

const {
  compareFindings,
  findingKey,
  normalizeResults,
} = eslintBaseline

const root = path.resolve("/repo/web")
const setStateSemanticMessage = [
  "Error: Calling setState synchronously within an effect can trigger cascading renders",
  "",
  "Effects are intended to synchronize state between React and external systems such as manually updating the DOM, state management libraries, or other platform APIs. In general, the body of an effect should do one or both of the following:",
  "* Update external systems with the latest state from React.",
  "* Subscribe for updates from some external system, calling setState in a callback function when external state changes.",
  "",
  "Calling setState synchronously within an effect body causes cascading renders that can hurt performance, and is not recommended. (https://react.dev/learn/you-might-not-need-an-effect).",
].join("\n")

const validFinding = {
  file: "src/page.tsx",
  severity: 2,
  ruleId: "react-hooks/set-state-in-effect",
  messageId: null,
  message: setStateSemanticMessage,
}

function resultAt(line, message = "Do not call setState in an effect") {
  return [{
    filePath: path.join(root, "src", "page.tsx"),
    messages: [{
      severity: 2,
      ruleId: "react-hooks/set-state-in-effect",
      messageId: "setStateInEffect",
      message,
      line,
      column: 7,
    }],
  }]
}

function realisticSetStateResult(rootDir, line, column, sourceLine) {
  const filePath = path.join(rootDir, "src", "page.tsx")
  return [{
    filePath,
    messages: [{
      severity: 2,
      ruleId: "react-hooks/set-state-in-effect",
      messageId: null,
      message: `${setStateSemanticMessage}\n\n${filePath}:${line}:${column}\n  ${line - 1} |   useEffect(() => {\n> ${line} |     ${sourceLine}\n       |          ^^^^^^^^^^^ Avoid calling setState() directly within an effect\n  ${line + 1} |   }, [loadSummary])`,
      line,
      column,
    }],
  }]
}

function exhaustiveDepsResult(line) {
  const message = `The 'activeTasks' logical expression could make the dependencies of useMemo Hook (at line ${line}) change on every render. To fix this, wrap the initialization of 'activeTasks' in its own useMemo() Hook.`
  return resultAt(line, message).map((result) => ({
    ...result,
    messages: result.messages.map((finding) => ({
      ...finding,
      severity: 1,
      ruleId: "react-hooks/exhaustive-deps",
      messageId: null,
    })),
  }))
}

describe("eslint baseline", () => {
  it("uses repository-relative identity and ignores source location", () => {
    const first = normalizeResults(resultAt(10), root)
    const moved = normalizeResults(resultAt(90), root)

    expect(first).toEqual([{
      file: "src/page.tsx",
      severity: 2,
      ruleId: "react-hooks/set-state-in-effect",
      messageId: "setStateInEffect",
      message: "Do not call setState in an effect",
    }])
    expect(findingKey(first[0])).toBe(findingKey(moved[0]))
  })

  it("removes checkout-specific set-state diagnostic trailers from identity", () => {
    const firstRoot = path.resolve("/tmp/checkout-one/web")
    const movedRoot = path.resolve("/tmp/checkout-two/web")
    const first = normalizeResults(
      realisticSetStateResult(firstRoot, 77, 10, "void loadSummary(false)"),
      firstRoot,
    )
    const moved = normalizeResults(
      realisticSetStateResult(movedRoot, 903, 5, "setSubmitted(nextQuery)"),
      movedRoot,
    )

    expect(first).toEqual([{
      ...validFinding,
      message: setStateSemanticMessage,
    }])
    expect(findingKey(first[0])).toBe(findingKey(moved[0]))
  })

  it("canonicalizes only exhaustive-deps at-line locations", () => {
    const [first] = normalizeResults(exhaustiveDepsResult(203), root)
    const [moved] = normalizeResults(exhaustiveDepsResult(947), root)

    expect(first.message).toBe("The 'activeTasks' logical expression could make the dependencies of useMemo Hook (at line <line>) change on every render. To fix this, wrap the initialization of 'activeTasks' in its own useMemo() Hook.")
    expect(findingKey(first)).toBe(findingKey(moved))
  })

  it("preserves duplicate findings as a multiset", () => {
    const [finding] = normalizeResults(resultAt(10), root)
    const diff = compareFindings([finding, finding], [finding])

    expect(diff.added).toEqual([finding])
    expect(diff.removed).toEqual([])
  })

  it("reports a replacement as one addition and one stale finding", () => {
    const [before] = normalizeResults(resultAt(10), root)
    const [after] = normalizeResults(resultAt(10, "Replacement text"), root)
    const diff = compareFindings([after], [before])

    expect(diff.added).toEqual([after])
    expect(diff.removed).toEqual([before])
  })

  it("rejects a baseline finding missing messageId", () => {
    const { messageId: _messageId, ...missingMessageId } = validFinding

    expect(() => eslintBaseline.validateBaselineDocument({
      version: 1,
      findings: [missingMessageId],
    })).toThrow(/finding at index 0.*messageId/i)
  })

  it("rejects a baseline finding with a wrong field type", () => {
    expect(() => eslintBaseline.validateBaselineDocument({
      version: 1,
      findings: [{ ...validFinding, severity: "2" }],
    })).toThrow(/finding at index 0.*severity/i)
  })

  it("rejects a baseline finding with a location-only extra field", () => {
    expect(() => eslintBaseline.validateBaselineDocument({
      version: 1,
      findings: [{ ...validFinding, line: 77 }],
    })).toThrow(/finding at index 0.*line/i)
  })
})
