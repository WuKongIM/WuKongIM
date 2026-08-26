export { expect, test } from "@playwright/test"
export type { Page } from "@playwright/test"

const defaultFailureEntryLimit = 50
const defaultFailureEntryCharacterLimit = 1_000

export function requiredEnvironment(name: string) {
  const value = process.env[name]?.trim()
  if (!value) {
    throw new Error(`${name} is required`)
  }
  return value
}

export function createBoundedFailureLog(
  entryLimit = defaultFailureEntryLimit,
  entryCharacterLimit = defaultFailureEntryCharacterLimit,
) {
  const entries: string[] = []
  let omitted = 0

  return {
    add(message: string) {
      if (entries.length >= entryLimit) {
        omitted += 1
        return
      }
      entries.push(
        message.length <= entryCharacterLimit
          ? message
          : `${message.slice(0, entryCharacterLimit)}… [entry truncated]`,
      )
    },
    messages() {
      return omitted > 0
        ? [...entries, `[${omitted} additional browser failure event(s) omitted]`]
        : [...entries]
    },
  }
}

export type BoundedFailureLog = ReturnType<typeof createBoundedFailureLog>
